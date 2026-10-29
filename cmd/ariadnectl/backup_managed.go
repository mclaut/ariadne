package main

import (
	"archive/tar"
	"ariadne/internal/backupretention"
	"ariadne/internal/version"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	backupStatusComplete = "complete"
	backupProofVerified  = "true"
)

var verifyBackupSnapshot = verifySnapshotRestore

func managedBackupsDir() string { return filepath.Join(backupsDir(), "previous") }

func managedBackupCmd() int {
	collections, err := backupCollections()
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup collections:", err)
		return 1
	}
	for _, name := range collections {
		if err := managedCollectionBackup(name); err != nil {
			fmt.Fprintln(os.Stderr, "backup:", err)
			return 1
		}
	}
	return 0
}

func managedCollectionBackup(name string) error {
	if name == "" || strings.ContainsAny(name, "/\\\x00") {
		return errors.New("invalid collection name")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := managedBackupsDir()
	object := "snapshot-" + name
	// Serialize creation as well as cleanup; Publish has a separate object lock.
	unlock, err := backupretention.Lock(root, "operation-"+object)
	if err != nil {
		return err
	}
	defer unlock()
	stage, err := backupretention.NewStage(root, object)
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	endpoint := qdrantREST + "/collections/" + url.PathEscape(name)
	beforeCount, beforeSchema, err := snapshotSourceState(endpoint)
	if err != nil {
		return err
	}
	var response struct {
		Result struct {
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			Checksum string `json:"checksum"`
		} `json:"result"`
	}
	if err = httpDo(http.MethodPost, endpoint+"/snapshots", nil, &response); err != nil {
		return err
	}
	afterCount, afterSchema, err := snapshotSourceState(endpoint)
	if err != nil {
		return err
	}
	if beforeSchema != afterSchema {
		return errors.New("collection schema changed during snapshot creation")
	}
	snapshot := response.Result
	if !strings.HasPrefix(snapshot.Name, name+"-") || filepath.Base(snapshot.Name) != snapshot.Name ||
		strings.ContainsAny(snapshot.Name, "/\\\x00") {
		return errors.New("invalid server snapshot name")
	}
	if len(snapshot.Checksum) != 64 || snapshot.Size <= 0 {
		return errors.New("qdrant snapshot checksum and size required; older backups retained")
	}
	path := filepath.Join(stage, "snapshot.snapshot")
	if err = download(endpoint+"/snapshots/"+url.PathEscape(snapshot.Name), path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	sha, err := backupretention.HashFile(path)
	if err != nil {
		return err
	}
	if info.Size() != snapshot.Size || sha != strings.ToLower(snapshot.Checksum) {
		return errors.New("snapshot size/checksum mismatch; older backups retained")
	}
	if err = validateSnapshotArchive(path); err != nil {
		return err
	}
	proof, verifyErr := verifyBackupSnapshot(path, home)
	if err = os.WriteFile(filepath.Join(stage, "restore.log"), proof.Log, 0o600); err != nil {
		return err
	}
	if verifyErr != nil {
		return fmt.Errorf("recovery verification failed; older backups retained: %w", verifyErr)
	}
	afterSHA, hashErr := backupretention.HashFile(path)
	if hashErr != nil {
		return hashErr
	}
	if afterSHA != sha {
		return errors.New("snapshot changed after recovery verification")
	}
	if proof.SchemaSHA256 != beforeSchema || proof.Points < min(beforeCount, afterCount) || proof.Points > max(beforeCount, afterCount) {
		return errors.New("restored snapshot does not match source count or vector schema")
	}

	metadata := map[string]string{
		"collection": name, "snapshot_sha256": sha,
		"snapshot_bytes": strconv.FormatInt(snapshot.Size, 10), "restore_verified": backupProofVerified,
		"points": strconv.FormatInt(proof.Points, 10), "points_sha256": proof.PointsSHA256,
		"schema_sha256": proof.SchemaSHA256, "restore_verified_at": time.Now().UTC().Format(time.RFC3339Nano),
		"native_source": snapshot.Name,
	}

	dest, err := backupretention.Publish(root, object, stage, started, metadata)
	if err != nil {
		return err
	}
	// Old paths are considered only after the new generation is durable and readable.
	removed, err := pruneLegacySnapshots(backupsDir(), name, started, dest)
	if err != nil {
		return fmt.Errorf("new verified backup retained at %s; cleanup: %w", dest, err)
	}
	nativeRemoved, err := pruneNativeSnapshots(name, snapshot.Name, started, dest)
	if err != nil {
		return fmt.Errorf("new verified backup retained; native cleanup: %w", err)
	}
	fmt.Printf("backup ok: %s · %d points · one verified copy · %d old files and %d native copies removed\n",
		name, proof.Points, removed, nativeRemoved)
	return nil
}

func validateSnapshotArchive(path string) error {
	f, err := os.Open(path) //nolint:gosec // explicit snapshot path in managed storage
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	markers := map[string]bool{}
	if err = validateSnapshotTar(f, info.Size(), 0, markers); err != nil {
		return err
	}
	if !markers["config.json"] || !markers["version.info"] {
		return errors.New("incomplete native snapshot archive")
	}
	return nil
}

func validateSnapshotTar(input io.Reader, maxSize int64, depth int, markers map[string]bool) error {
	if depth > 8 {
		return errors.New("snapshot archive nesting limit")
	}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.HasPrefix(header.Name, "/") || strings.ContainsAny(header.Name, "\\:") || header.Size > maxSize {
			return errors.New("unsafe snapshot archive path or size")
		}
		for _, part := range strings.Split(header.Name, "/") {
			if part == ".." {
				return errors.New("unsafe snapshot archive traversal")
			}
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("snapshot archive links or special files are not supported")
		}
		if depth == 0 {
			markers[header.Name] = true
		}
		if strings.HasSuffix(header.Name, ".tar") {
			if err = validateSnapshotTar(reader, header.Size, depth+1, markers); err != nil {
				return err
			}
		}
		if _, err = io.Copy(io.Discard, io.LimitReader(reader, header.Size)); err != nil {
			return err
		}
	}
}

func legacySnapshotTime(name, coll string) (time.Time, bool) {
	if !strings.HasPrefix(name, coll+"-") || !strings.HasSuffix(name, ".snapshot") {
		return time.Time{}, false
	}
	ts := strings.TrimSuffix(strings.TrimPrefix(name, coll+"-"), ".snapshot")
	value, err := time.ParseInLocation("20060102-150405", ts, time.Local)
	return value, err == nil
}

type legacySnapshot struct {
	path    string
	sha     string
	created time.Time
	info    os.FileInfo
}

func pruneLegacySnapshots(root, coll string, before time.Time, keeper string) (int, error) {
	m, err := backupretention.Verify(keeper)
	if err != nil {
		return 0, err
	}
	if err = checkSnapshotManifest(m, coll); err != nil {
		return 0, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return 0, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("legacy backup root is not a physical directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return 0, err
	}
	var candidates []legacySnapshot
	for _, dir := range []string{root, filepath.Join(root, "archive")} {
		info, statErr := os.Lstat(dir)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return 0, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, errors.New("legacy archive must be a physical directory")
		}
		files, readErr := os.ReadDir(dir)
		if readErr != nil {
			return 0, readErr
		}
		for _, entry := range files {
			created, ok := legacySnapshotTime(entry.Name(), coll)
			if !ok || created.After(before) {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			info, statErr := os.Lstat(source)
			if statErr != nil {
				return 0, statErr
			}
			if !info.Mode().IsRegular() {
				return 0, errors.New("legacy backup is not a regular file")
			}
			if err = validateSnapshotArchive(source); err != nil {
				return 0, fmt.Errorf("legacy snapshot not recognized: %w", err)
			}
			sha, hashErr := backupretention.HashFile(source)
			if hashErr != nil {
				return 0, hashErr
			}
			busy, useErr := backupretention.InUse(source)
			if useErr != nil {
				return 0, useErr
			}
			if busy {
				return 0, errors.New("legacy backup is in use")
			}
			candidates = append(candidates, legacySnapshot{path: source, sha: sha, created: created, info: info})
		}
	}
	copies := []backupretention.Copy{{
		Path: keeper, Object: coll, SHA256: m.Metadata["snapshot_sha256"], CreatedAt: m.CreatedAt, Verified: true,
	}}
	byPath := make(map[string]legacySnapshot, len(candidates))
	for _, c := range candidates {
		copies = append(copies, backupretention.Copy{Path: c.path, Object: coll, SHA256: c.sha, CreatedAt: c.created, Verified: true})
		byPath[c.path] = c
	}
	plan := backupretention.OnePrevious(copies)
	if len(plan.Keep) != 1 || plan.Keep[0].Path != keeper || len(plan.Blocked) != 0 {
		return 0, errors.New("legacy backup chronology is ambiguous")
	}
	removed := 0
	for _, copy := range plan.Remove {
		c := byPath[copy.Path]
		current, statErr := os.Lstat(c.path)
		if statErr != nil {
			return removed, statErr
		}
		if !os.SameFile(c.info, current) || c.info.Size() != current.Size() || !c.info.ModTime().Equal(current.ModTime()) {
			return removed, errors.New("legacy snapshot changed after planning")
		}
		event := map[string]any{"action": "remove_legacy_snapshot", "status": "started", "path": c.path, "sha256": c.sha, "collection": coll}
		if err = appendBackupRetentionAudit(event); err != nil {
			return removed, err
		}
		if err = backupretention.RemoveFile(c.path, c.sha); err != nil {
			return removed, err
		}
		event["status"] = backupStatusComplete
		if err = appendBackupRetentionAudit(event); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

var nativeSnapshotTimestamp = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}-\d{2}-\d{2}-\d{2})\.snapshot$`)

func pruneNativeSnapshots(coll, source string, before time.Time, keeper string) (int, error) {
	if err := verifiedSnapshotKeeper(keeper, coll); err != nil {
		return 0, err
	}
	endpoint := qdrantREST + "/collections/" + url.PathEscape(coll) + "/snapshots"
	var response struct {
		Result []struct {
			Name         string `json:"name"`
			CreationTime string `json:"creation_time"`
		} `json:"result"`
	}
	if err := httpDo(http.MethodGet, endpoint, nil, &response); err != nil {
		return 0, err
	}
	removed := 0
	for _, item := range response.Result {
		if !strings.HasPrefix(item.Name, coll+"-") || strings.ContainsAny(item.Name, "/\\\x00") {
			continue
		}
		match := nativeSnapshotTimestamp.FindStringSubmatch(item.Name)
		if len(match) != 2 {
			continue
		}
		created, err := time.Parse("2006-01-02-15-04-05", match[1])
		if err != nil {
			continue
		}
		if item.Name != source {
			precise, parseErr := time.Parse(time.RFC3339Nano, item.CreationTime)
			if parseErr == nil {
				if precise.After(before) {
					continue
				}
			} else if !created.Add(time.Second).Before(before) {
				continue
			}
		}
		if err = appendBackupRetentionAudit(map[string]any{
			"action": "remove_native_snapshot", "collection": coll, "name": item.Name,
		}); err != nil {
			return removed, err
		}
		var result struct {
			Status string `json:"status"`
			Result bool   `json:"result"`
		}
		if err = httpDo(http.MethodDelete, endpoint+"/"+url.PathEscape(item.Name)+"?wait=true", nil, &result); err != nil {
			return removed, err
		}
		if result.Status != "ok" || !result.Result {
			return removed, errors.New("native snapshot cleanup did not complete")
		}
		if err = appendBackupRetentionAudit(map[string]any{
			"action": "remove_native_snapshot", "status": backupStatusComplete, "collection": coll, "name": item.Name,
		}); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func appendBackupRetentionAudit(event map[string]any) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".ariadne", "state")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	event["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	//nolint:gosec // append-only operation audit under the explicit runtime state directory
	f, err := os.OpenFile(filepath.Join(dir, "backup-retention.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func backupRetentionCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ariadnectl backup-retention {runtime|claude-mcp|claude-hooks|codex-mcp}")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var dest string
	switch args[0] {
	case "runtime":
		app := ""
		if runtime.GOOS == "darwin" {
			app = "/Applications/Ariadne.app"
			if _, statErr := os.Stat(app); os.IsNotExist(statErr) {
				app = filepath.Join(home, "Applications", "Ariadne.app")
			}
		}
		dest, err = backupretention.CaptureRuntimeBeforeUpgrade(home, app, version.Tag)
	case "claude-mcp":
		dest, err = backupretention.CaptureConfig(home, args[0], filepath.Join(home, ".claude.json"))
	case "claude-hooks":
		dest, err = backupretention.CaptureConfig(home, args[0], filepath.Join(home, ".claude", "settings.json"))
	case "codex-mcp":
		dest, err = backupretention.CaptureConfig(home, args[0], backupretention.ClientConfigPath(home, args[0]))
	default:
		fmt.Fprintln(os.Stderr, "unknown backup-retention component")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "previous copy:", err)
		return 1
	}
	fmt.Println("one verified previous copy:", dest)
	return 0
}

func verifiedSnapshotKeeper(path, coll string) error {
	m, err := backupretention.Verify(path)
	if err != nil {
		return err
	}
	return checkSnapshotManifest(m, coll)
}

func checkSnapshotManifest(m backupretention.Manifest, coll string) error {
	if m.Object != "snapshot-"+coll || m.Metadata["collection"] != coll || m.Metadata["restore_verified"] != backupProofVerified {
		return errors.New("verified recovery copy for this collection required")
	}
	for _, key := range []string{"snapshot_sha256", "points_sha256", "schema_sha256"} {
		value := m.Metadata[key]
		if len(value) != 64 {
			return errors.New("incomplete snapshot verification proof")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return errors.New("invalid verification hash")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, m.Metadata["restore_verified_at"]); err != nil {
		return errors.New("missing recovery verification time")
	}
	points, err := strconv.ParseInt(m.Metadata["points"], 10, 64)
	if err != nil || points < 0 {
		return errors.New("invalid verified point count")
	}
	size, err := strconv.ParseInt(m.Metadata["snapshot_bytes"], 10, 64)
	if err != nil || size <= 0 {
		return errors.New("invalid verified snapshot size")
	}
	for _, entry := range m.Entries {
		if entry.Path == "snapshot.snapshot" && entry.Kind == "file" && entry.SHA256 == m.Metadata["snapshot_sha256"] && entry.Bytes == size {
			return nil
		}
	}
	return errors.New("snapshot verification is not bound to the retained file")
}

func snapshotSourceState(endpoint string) (int64, string, error) {
	var count map[string]any
	if err := httpDo(http.MethodPost, endpoint+"/points/count", strings.NewReader(`{"exact":true}`), &count); err != nil {
		return 0, "", err
	}
	value, ok := mapPathNumber(count, "result", "count")
	if !ok {
		return 0, "", errors.New("missing exact source count")
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, "", err
	}
	var info map[string]any
	if err = httpDo(http.MethodGet, endpoint, nil, &info); err != nil {
		return 0, "", err
	}
	schema, err := vectorSchemaDigest(info)
	return number, schema, err
}

func backupCollections() ([]string, error) {
	raw := os.Getenv("ARIADNE_BACKUP_COLLECTIONS")
	if raw == "" {
		return []string{collection}, nil
	}
	names := strings.Split(raw, ",")
	seen := map[string]bool{}
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "/\\\x00") {
			return nil, errors.New("invalid explicitly configured backup collection")
		}
		if !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	return result, nil
}
