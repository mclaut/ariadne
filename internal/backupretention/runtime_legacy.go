package backupretention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	runtimeBackupObject  = "runtime"
	runtimeDirectoryKind = "directory"
	runtimeWindows       = "windows"
)

type legacyRuntime struct {
	path     string
	release  string
	entries  []Entry
	identity string
	version  []int
}

var probeRuntimeVersion = readRuntimeVersion

// PruneLegacyRuntimes migrates the older release-local backup layout. It never
// removes release/bin or an active executable: only exact previous-runtime trees
// already captured by a restored full set, older than the canonical runtime.
func PruneLegacyRuntimes(home string) error {
	base := filepath.Join(home, ".ariadne")
	releases := filepath.Join(base, "releases")
	if _, err := os.Lstat(releases); os.IsNotExist(err) {
		return nil
	}
	physical, err := physicalDirectory(releases)
	if err != nil {
		return err
	}
	releases = physical
	children, err := os.ReadDir(releases)
	if err != nil {
		return err
	}
	var candidates []legacyRuntime
	for _, child := range children {
		if !legacyRuntimeRelease(child.Name()) {
			continue
		}
		dir := filepath.Join(releases, child.Name(), "previous-runtime")
		if _, statErr := os.Lstat(dir); os.IsNotExist(statErr) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if child.Type()&os.ModeSymlink != 0 || !child.IsDir() {
			return errors.New("legacy release must be a physical directory")
		}
		if _, err = physicalDirectory(dir); err != nil {
			return err
		}
		candidates = append(candidates, legacyRuntime{path: dir, release: child.Name()})
	}
	if len(candidates) == 0 {
		return nil
	}
	root := filepath.Join(base, "backups", "previous")
	unlock, err := Lock(root, runtimeBackupObject)
	if err != nil {
		return err
	}
	defer unlock()
	fullUnlock, err := Lock(root, "full")
	if err != nil {
		return err
	}
	defer fullUnlock()
	keeper := filepath.Join(root, runtimeBackupObject)
	km, err := Verify(keeper)
	if err != nil {
		return fmt.Errorf("canonical runtime: %w", err)
	}
	if km.Object != runtimeBackupObject {
		return errors.New("canonical runtime keeper required")
	}
	keeperVersion, err := probeRuntimeVersion(filepath.Join(keeper, "bin", runtimeCTLName()))
	if err != nil {
		return err
	}
	proof, err := Verify(filepath.Join(root, "full"))
	if err != nil {
		return fmt.Errorf("legacy runtime full proof: %w", err)
	}
	if proof.Object != "full" || proof.Metadata["restore_verified"] != "true" {
		return errors.New("restored full proof required for legacy runtime migration")
	}
	fullSHA, err := HashFile(filepath.Join(root, "full", manifestName))
	if err != nil {
		return err
	}
	keeperSHA, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil {
		return err
	}
	// Validate every candidate before deleting any; mtime alone is never age proof.
	for i := range candidates {
		c := &candidates[i]
		prefix := "runtime/releases/" + c.release + "/previous-runtime"
		rootMode := uint32(0)
		found := false
		for _, e := range proof.Entries {
			if e.Path == prefix && e.Kind == runtimeDirectoryKind {
				rootMode = e.Mode
				found = true
			}
			if strings.HasPrefix(e.Path, prefix+"/") {
				e.Path = strings.TrimPrefix(e.Path, prefix+"/")
				c.entries = append(c.entries, e)
			}
		}
		info, statErr := os.Lstat(c.path)
		if statErr != nil {
			return statErr
		}
		if !found || len(c.entries) == 0 || uint32(info.Mode().Perm()) != rootMode {
			return errors.New("legacy runtime is absent from restored full proof")
		}
		sortRuntimeEntries(c.entries)
		actual, readErr := runtimeInventory(c.path)
		if readErr != nil {
			return readErr
		}
		if !slices.Equal(actual, c.entries) {
			return errors.New("legacy runtime changed since verified full backup")
		}
		ctl, findErr := legacyRuntimeCTL(c.path, c.entries)
		if findErr != nil {
			return findErr
		}
		c.version, err = probeRuntimeVersion(ctl)
		if err != nil {
			return err
		}
		comparison := slices.Compare(c.version, keeperVersion)
		if comparison > 0 || (comparison == 0 && !runtimePayloadEqual(c.entries, km.Entries)) {
			return errors.New("legacy runtime is not older than canonical keeper")
		}
		busy, useErr := InUse(c.path)
		if useErr != nil {
			return useErr
		}
		if busy {
			return errors.New("legacy runtime in use; cleanup deferred")
		}
		data, marshalErr := json.Marshal(c.entries)
		if marshalErr != nil {
			return marshalErr
		}
		h := sha256.Sum256(data)
		c.identity = hex.EncodeToString(h[:])
	}
	if err = verifyRuntimeRecovery(root, keeper, keeperVersion); err != nil {
		return err
	}
	for _, c := range candidates {
		paths := runtimeAuditPaths(c.entries)
		auditRoot := filepath.Join(base, "backups", "runtime-audit")
		provenance := map[string]any{
			"legacy_entries": c.entries, "release": c.release, "version": c.version,
			"keeper_manifest_sha256": keeperSHA, "full_manifest_sha256": fullSHA,
		}
		if _, err = archiveBackupAudit(auditRoot, runtimeBackupObject, c.identity, c.path, paths, provenance); err != nil {
			return err
		}
		if _, err = Verify(keeper); err != nil {
			return err
		}
		if digest, hashErr := HashFile(filepath.Join(root, "full", manifestName)); hashErr != nil || digest != fullSHA {
			return errors.New("full proof changed during runtime cleanup")
		}
		actual, readErr := runtimeInventory(c.path)
		if readErr != nil {
			return readErr
		}
		if !slices.Equal(actual, c.entries) {
			return errors.New("legacy runtime changed after planning")
		}
		busy, useErr := InUse(c.path)
		if useErr != nil {
			return useErr
		}
		if busy {
			return errors.New("legacy runtime became active")
		}
		event := map[string]any{
			"action": "remove_legacy_runtime", "status": "started", "release": c.release, "version": c.version,
			"inventory_sha256": c.identity, "keeper_manifest_sha256": keeperSHA, "full_manifest_sha256": fullSHA,
		}
		if err = appendAudit(root, event); err != nil {
			return err
		}
		container, mkdirErr := os.MkdirTemp(filepath.Dir(c.path), ".retired-runtime-")
		if mkdirErr != nil {
			return mkdirErr
		}
		target := filepath.Join(container, "generation")
		if err = renameExclusive(c.path, target); err != nil {
			return err
		}
		actual, err = runtimeInventory(target)
		if err == nil && !slices.Equal(actual, c.entries) {
			err = errors.New("retired runtime changed")
		}
		if err == nil {
			busy, useErr := InUse(target)
			if useErr != nil {
				err = useErr
			} else if busy {
				err = errors.New("retired runtime became active")
			}
		}
		if err != nil {
			restoreErr := renameExclusive(target, c.path)
			if restoreErr == nil {
				restoreErr = os.Remove(container)
			}
			event["status"] = "deferred"
			return errors.Join(err, restoreErr, appendAudit(root, event))
		}
		if err = removeEntries(target, c.entries, false); err != nil {
			return err
		}
		if err = os.Remove(container); err != nil {
			return err
		}
		event["status"] = "complete"
		if err = appendAudit(root, event); err != nil {
			return err
		}
	}
	return nil
}

func runtimePayloadEqual(old, keeper []Entry) bool {
	canonical := make(map[string]Entry, len(keeper))
	for _, e := range keeper {
		canonical[e.Path] = e
	}
	audit := runtimeAuditPaths(old)
	for _, e := range old {
		if e.Kind != legacyRegularFile {
			continue
		}
		preserved := false
		for _, p := range audit {
			if e.Path == p || strings.HasPrefix(e.Path, p+"/") {
				preserved = true
				break
			}
		}
		if preserved {
			continue
		}
		path := e.Path
		if !strings.HasPrefix(path, "Ariadne.app/") {
			path = "bin/" + strings.TrimPrefix(strings.TrimPrefix(path, "originals/"), "bin/")
		}
		match, ok := canonical[path]
		if !ok || match.Kind != legacyRegularFile || match.SHA256 != e.SHA256 || match.Bytes != e.Bytes {
			return false
		}
	}
	return true
}

func verifyRuntimeRecovery(root, keeper string, version []int) error {
	work, err := NewStage(root, "runtime-recovery")
	if err != nil {
		return err
	}
	restored := filepath.Join(work, "runtime")
	if err = CopyInto(keeper, restored); err != nil {
		return err
	}
	if _, err = Verify(restored); err != nil {
		return err
	}
	actual, err := probeRuntimeVersion(filepath.Join(restored, "bin", runtimeCTLName()))
	if err != nil {
		return err
	}
	if !slices.Equal(actual, version) {
		return errors.New("restored runtime version differs; legacy copies retained")
	}
	busy, err := InUse(restored)
	if err != nil {
		return err
	}
	if busy {
		return errors.New("restored runtime still in use")
	}
	if err = RemoveVerified(restored); err != nil {
		return err
	}
	if err = os.Remove(work); err != nil {
		return err
	}
	return appendAudit(root, map[string]any{"action": "verify_runtime_recovery", "status": "complete", "version": version})
}

func runtimeInventory(dir string) ([]Entry, error) {
	entries, err := inventory(dir)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, manifestName)
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	sha, err := HashFile(p)
	if err != nil {
		return nil, err
	}
	entry := Entry{Path: manifestName, Kind: legacyRegularFile, Mode: uint32(info.Mode().Perm()), Bytes: info.Size(), SHA256: sha}
	entries = append(entries, entry)
	sortRuntimeEntries(entries)
	return entries, nil
}

func sortRuntimeEntries(entries []Entry) {
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
}

func runtimeCTLName() string {
	if runtime.GOOS == runtimeWindows {
		return "ariadnectl.exe"
	}
	return "ariadnectl"
}

func legacyRuntimeCTL(dir string, entries []Entry) (string, error) {
	for _, name := range []string{runtimeCTLName(), "originals/" + runtimeCTLName(), "bin/" + runtimeCTLName()} {
		for _, e := range entries {
			if e.Path == name && e.Kind == legacyRegularFile {
				return filepath.Join(dir, filepath.FromSlash(name)), nil
			}
		}
	}
	return "", errors.New("regular versioned legacy CLI required")
}

func readRuntimeVersion(path string) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "version") //nolint:gosec // exact checksum-verified own runtime, fixed read-only argument
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	for _, name := range []string{"HOME", "USERPROFILE", "SystemRoot"} {
		if value := os.Getenv(name); value != "" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("runtime version probe: %w", err)
	}
	return parseRuntimeVersion(strings.TrimSpace(string(out)))
}

func parseRuntimeVersion(version string) ([]int, error) {
	if !strings.HasPrefix(version, "v") {
		return nil, errors.New("unrecognized runtime version")
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return nil, errors.New("unrecognized runtime version")
	}
	values := make([]int, 0, 3)
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return nil, errors.New("unrecognized runtime version")
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		values = append(values, n)
	}
	return values, nil
}

func runtimeAuditPaths(entries []Entry) []string {
	var paths []string
	selected := map[string]bool{}
	for _, e := range entries {
		p := e.Path
		parts := strings.Split(p, "/")
		if parts[0] == "Ariadne.app" {
			if p == "Ariadne.app/Contents/Info.plist" || p == "Ariadne.app/Contents/_CodeSignature/CodeResources" {
				paths = append(paths, p)
			}
			continue
		}
		if p == "originals" || p == "bin" {
			continue
		}
		name := p
		if len(parts) == 2 && (parts[0] == "originals" || parts[0] == "bin") {
			name = parts[1]
		}
		name = strings.TrimSuffix(name, ".exe")
		payload := false
		switch name {
		case "ariadne", "ariadnectl", "ariadne-hook", "ariadne-launcher", "ariadne-tray", "import", "qdrant":
			payload = e.Kind != runtimeDirectoryKind
		}
		if payload {
			continue
		}
		covered := false
		for parent := p; parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if selected[parent] {
				covered = true
				break
			}
		}
		if !covered {
			paths = append(paths, p)
			selected[p] = true
		}
	}
	return paths
}
