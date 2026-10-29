package main

import (
	"ariadne/internal/backupretention"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const macOS = "darwin"

// Explicit sources avoid guessing ownership of shared models/client settings.
// The private plan contains paths, never credential values. Its exact bytes are
// included in each full set. Missing/changed sources fail closed.
type fullBackupPlan struct {
	Format              int                          `json:"format"`
	Sources             map[string]string            `json:"sources"`
	SQLite              map[string]string            `json:"sqlite"`
	Prefixes            map[string]string            `json:"prefixes"`
	AuditPaths          []string                     `json:"audit_paths"`
	GitRoots            []string                     `json:"git_roots"`
	ModelsRoot          string                       `json:"models_root"`
	OllamaBinary        string                       `json:"ollama_binary"`
	EmbeddingModel      string                       `json:"embedding_model"`
	EmbeddingDimensions int                          `json:"embedding_dimensions"`
	Clients             bool                         `json:"clients"`
	Legacy              []backupretention.LegacyFull `json:"legacy_full"`
}

func fullPlanPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "full-backup.json")
}

func loadFullPlan() (fullBackupPlan, []byte, error) {
	var plan fullBackupPlan
	info, err := os.Lstat(fullPlanPath())
	if err != nil {
		return plan, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return plan, nil, errors.New("full-backup.json must be a private regular file")
	}
	data, err := os.ReadFile(fullPlanPath()) //nolint:gosec // fixed private operator-owned plan
	if err != nil {
		return plan, nil, err
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err = d.Decode(&plan); err != nil {
		return plan, nil, err
	}
	if plan.Format != 1 || len(plan.Sources) == 0 {
		return plan, nil, errors.New("invalid full backup plan")
	}
	names := make([]string, 0, len(plan.Sources)+len(plan.SQLite)+len(plan.Prefixes))
	for name, source := range plan.Sources {
		if !filepath.IsAbs(source) {
			return plan, nil, errors.New("full source must be absolute")
		}
		names = append(names, name)
	}
	for name, source := range plan.SQLite {
		if !filepath.IsAbs(source) {
			return plan, nil, errors.New("SQLite source must be absolute")
		}
		names = append(names, name)
	}
	for name, source := range plan.Prefixes {
		if !filepath.IsAbs(source) {
			return plan, nil, errors.New("prefix source must be absolute")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for i, name := range names {
		reserved := name == "manifest.json" || name == "plan.json" || name == "snapshots" || name == "recovery.json"
		reserved = reserved || (plan.Clients && (name == "client" || strings.HasPrefix(name, "client/")))
		if !fs.ValidPath(name) || name == "." || reserved || strings.HasPrefix(name, "snapshots/") {
			return plan, nil, errors.New("invalid or reserved full backup destination")
		}
		for _, prior := range names[:i] {
			if strings.EqualFold(name, prior) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prior)+"/") {
				return plan, nil, errors.New("overlapping full backup destinations")
			}
		}
	}
	for _, name := range append(append([]string(nil), plan.AuditPaths...), plan.GitRoots...) {
		if !fs.ValidPath(name) || name == "." {
			return plan, nil, errors.New("invalid full verification path")
		}
	}
	if plan.ModelsRoot != "" && (!fs.ValidPath(plan.ModelsRoot) || plan.ModelsRoot == ".") {
		return plan, nil, errors.New("invalid full model path")
	}
	missingModelCheck := !fs.ValidPath(plan.OllamaBinary) || plan.OllamaBinary == "." ||
		plan.EmbeddingModel == "" || plan.EmbeddingDimensions <= 0
	if plan.ModelsRoot != "" && missingModelCheck {
		return plan, nil, errors.New("restored model runtime and embedding check required")
	}
	return plan, data, nil
}

func fullBackupCmd() int {
	if err := runFullBackup(); err != nil {
		fmt.Fprintln(os.Stderr, "full backup:", err)
		return 1
	}
	return 0
}

func runFullBackup() error {
	plan, data, err := loadFullPlan()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	installation, err := backupretention.Lock(filepath.Join(home, ".ariadne"), "install")
	if err != nil {
		return err
	}
	defer installation()
	root := managedBackupsDir()
	unlock, err := backupretention.Lock(root, "operation-full")
	if err != nil {
		return err
	}
	defer unlock()
	started := time.Now().UTC()
	if code := managedBackupCmd(); code != 0 {
		return errors.New("fresh verified collection backups required")
	}
	stage, err := backupretention.NewStage(root, "full")
	if err != nil {
		return err
	}
	if err = writeFullJSON(filepath.Join(stage, "plan.json"), json.RawMessage(data)); err != nil {
		return err
	}
	fmt.Println("full backup: capturing declared sources, online SQLite and audit prefixes")
	for _, name := range sortedFullNames(plan.Sources) {
		if err = backupretention.CopyInto(plan.Sources[name], filepath.Join(stage, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
	}
	for _, name := range sortedFullNames(plan.SQLite) {
		if err = copyFullSQLite(plan.SQLite[name], filepath.Join(stage, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	for _, name := range sortedFullNames(plan.Prefixes) {
		if err = copyFullPrefix(plan.Prefixes[name], filepath.Join(stage, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	if plan.Clients {
		if err = captureFullClients(stage); err != nil {
			return err
		}
	}
	collections, err := backupCollections()
	if err != nil {
		return err
	}
	proofs := map[string]backupretention.Manifest{}
	for _, name := range collections {
		// Hold the collection operation lock across verification and independent copy.
		if err = copyFullSnapshot(root, stage, name, proofs); err != nil {
			return err
		}
	}
	fmt.Println("full backup: testing independent file, database and model recovery")
	if err = backupretention.CheckRecoveryCopy(stage, filepath.Join(backupsDir(), "full-recovery-checks"), func(restored string) error {
		return checkFullRecovery(restored, plan, proofs)
	}); err != nil {
		return fmt.Errorf("full recovery failed; previous full copies retained: %w", err)
	}
	metadata := map[string]string{
		"restore_verified": backupProofVerified, "restore_verified_at": time.Now().UTC().Format(time.RFC3339Nano),
		"kind": "component-consistent-full",
	}
	recovery := map[string]any{"status": "PASS", "metadata": metadata, "collections": proofs}
	if err = writeFullJSON(filepath.Join(stage, "recovery.json"), recovery); err != nil {
		return err
	}
	// Preserve prior full-set audit before Publish can retire that generation.
	previous := filepath.Join(root, "full")
	if _, statErr := os.Lstat(previous); statErr == nil {
		m, verifyErr := backupretention.Verify(previous)
		if verifyErr != nil {
			return verifyErr
		}
		if m.Object != "full" || m.Metadata["restore_verified"] != backupProofVerified {
			return errors.New("previous full recovery receipt required")
		}
		sha, hashErr := backupretention.HashFile(filepath.Join(previous, "manifest.json"))
		if hashErr != nil {
			return hashErr
		}
		var oldPlan fullBackupPlan
		b, readErr := os.ReadFile(filepath.Join(previous, "plan.json")) //nolint:gosec // verified prior full generation
		if readErr != nil {
			return readErr
		}
		if err = json.Unmarshal(b, &oldPlan); err != nil {
			return err
		}
		paths := append([]string{"plan.json", "recovery.json", "manifest.json", "audit", "runtime/state", "runtime/logs", "runtime/metrics.db"},
			oldPlan.AuditPaths...)
		if _, err = backupretention.ArchiveFullAudit(filepath.Join(backupsDir(), "full-audit"), sha, previous, paths); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	dest, err := backupretention.Publish(root, "full", stage, started, metadata)
	if err != nil {
		return err
	}
	for _, legacy := range plan.Legacy {
		if err = backupretention.PruneLegacyFull(backupsDir(), dest, legacy); err != nil {
			return fmt.Errorf("new full retained; legacy cleanup: %w", err)
		}
	}
	fmt.Println("full backup ok: independent recovery PASS · one verified full copy · audit retained")
	return nil
}

func sortedFullNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func copyFullSnapshot(root, stage, name string, proofs map[string]backupretention.Manifest) error {
	unlock, err := backupretention.Lock(root, "operation-snapshot-"+name)
	if err != nil {
		return err
	}
	defer unlock()
	source := filepath.Join(root, "snapshot-"+name)
	m, err := backupretention.Verify(source)
	if err != nil {
		return err
	}
	if err = checkSnapshotManifest(m, name); err != nil {
		return err
	}
	if err = backupretention.CopyInto(source, filepath.Join(stage, "snapshots", name)); err != nil {
		return err
	}
	proofs[name] = m
	return nil
}

func writeFullJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // exclusive private stage
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func fullSQLite(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

func copyFullSQLite(source, dest string) error {
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("SQLite destination must be absent")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	db, err := fullSQLite(source)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return err
	}
	if err = os.Chmod(dest, 0o600); err != nil {
		return err
	}
	f, err := os.Open(dest) //nolint:gosec // newly created private SQLite recovery copy
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(syncErr, closeErr)
}

func checkFullRecovery(restored string, plan fullBackupPlan, proofs map[string]backupretention.Manifest) error {
	for name := range plan.SQLite {
		db, err := fullSQLite(filepath.Join(restored, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		var integrity string
		err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
		cancel()
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if integrity != "ok" {
			return errors.New("restored SQLite integrity failed")
		}
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	bin := filepath.Join(restored, "runtime", "bin")
	if _, err := fullReadCommand(filepath.Join(bin, "ariadnectl"+ext), []string{"version"}, restored); err != nil {
		return err
	}
	if runtime.GOOS == macOS {
		app := filepath.Join(restored, "applications", "Ariadne.app")
		if _, statErr := os.Lstat(app); statErr == nil {
			if _, verifyErr := fullReadCommand("/usr/bin/codesign", []string{"--verify", "--deep", "--strict", app}, restored); verifyErr != nil {
				return verifyErr
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for name, m := range proofs {
		snapshot := filepath.Join(restored, "snapshots", name, "snapshot.snapshot")
		proof, verifyErr := verifySnapshotWithBinary(snapshot, home, filepath.Join(bin, "qdrant"+ext))
		if verifyErr != nil {
			return verifyErr
		}
		if fmt.Sprint(proof.Points) != m.Metadata["points"] || proof.PointsSHA256 != m.Metadata["points_sha256"] ||
			proof.SchemaSHA256 != m.Metadata["schema_sha256"] {
			return errors.New("restored full snapshot proof differs")
		}
	}
	for _, name := range plan.GitRoots {
		args := []string{"-C", filepath.Join(restored, filepath.FromSlash(name)), "fsck", "--full", "--no-dangling"}
		if _, err = fullReadCommand("git", args, restored); err != nil {
			return err
		}
	}
	if plan.ModelsRoot != "" {
		if err = checkFullModels(filepath.Join(restored, filepath.FromSlash(plan.ModelsRoot))); err != nil {
			return err
		}
		if err = checkFullOllama(restored, plan); err != nil {
			return err
		}
	}
	return nil
}

func fullReadCommand(binary string, args []string, home string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...) //nolint:gosec // explicit restored runtime or fixed read-only Git invocation
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	if root := os.Getenv("SystemRoot"); root != "" {
		command.Env = append(command.Env, "SystemRoot="+root)
	}
	out, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("full recovery command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func checkFullModels(root string) error {
	count := 0
	err := filepath.WalkDir(filepath.Join(root, "manifests"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("model manifest must be regular")
		}
		b, err := os.ReadFile(path) //nolint:gosec // restored model manifest
		if err != nil {
			return err
		}
		var manifest struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
			Layers []struct {
				Digest string `json:"digest"`
			} `json:"layers"`
		}
		if err = json.Unmarshal(b, &manifest); err != nil {
			return err
		}
		digests := []string{manifest.Config.Digest}
		for _, layer := range manifest.Layers {
			digests = append(digests, layer.Digest)
		}
		for _, digest := range digests {
			if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 || strings.Trim(digest[7:], "0123456789abcdef") != "" {
				return errors.New("invalid model digest")
			}
			sha, hashErr := backupretention.HashFile(filepath.Join(root, "blobs", strings.ReplaceAll(digest, ":", "-")))
			if hashErr != nil {
				return hashErr
			}
			if sha != digest[7:] {
				return errors.New("restored model blob digest mismatch")
			}
		}
		count++
		return nil
	})
	if err == nil && count == 0 {
		return errors.New("no model manifests in full recovery")
	}
	return err
}
