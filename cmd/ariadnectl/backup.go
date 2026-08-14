package main

import (
	"ariadne/internal/activity"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	backupsSub = ".ariadne/backups"
	keepBackup = 10
)

func backupsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, backupsSub)
}

// backupCmd creates a Qdrant snapshot and downloads it OUTSIDE qdrant-data so
// it survives loss of the data dir. Both copies are preserved; older downloaded
// snapshots move into an append-only archive rather than being discarded.
func backupCmd() (code int) {
	status, message := "failed", ""
	var sizeMB int64
	defer func() {
		counters := map[string]int64{}
		if sizeMB > 0 {
			counters["snapshot_mb"] = sizeMB
		}
		_ = activity.Append(activity.Event{
			Operation: "backup", Status: status, Message: message, Counters: counters,
		})
	}()
	dir := backupsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-owned backups dir
		fmt.Fprintln(os.Stderr, "mkdir backups:", err)
		message = err.Error()
		return 1
	}
	// 1. create snapshot
	body, ok := postJSON(qdrantREST+"/collections/"+collection+"/snapshots", nil)
	if !ok {
		fmt.Fprintln(os.Stderr, "snapshot create failed (is Qdrant up?)")
		message = "snapshot create failed"
		return 1
	}
	name, _ := mapPath(body, "result", "name")
	if name == "" {
		fmt.Fprintln(os.Stderr, "snapshot: no name in response")
		message = "snapshot response had no name"
		return 1
	}
	// 2. download it
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(dir, collection+"-"+ts+".snapshot")
	if err := download(qdrantREST+"/collections/"+collection+"/snapshots/"+name, dest); err != nil {
		fmt.Fprintln(os.Stderr, "download snapshot:", err)
		message = err.Error()
		return 1
	}
	// 3. keep a small recent set in the root and move older snapshots into an
	// append-only archive. Nothing is discarded.
	n, archived := rotateBackups(dir)
	fi, _ := os.Stat(dest)
	if fi != nil {
		sizeMB = fi.Size() / (1024 * 1024)
	}
	fmt.Printf("backup ok: %s (%dMB) · %d recent · %d archived now\n",
		filepath.Base(dest), sizeMB, n, archived)
	status = "complete"
	return 0
}

// backupIfDue runs the automatic snapshot path at most once per interval.
// Manual `ariadnectl backup` remains unconditional.
func backupIfDue(now time.Time, interval time.Duration) int {
	if interval <= 0 {
		interval = 7 * 24 * time.Hour
	}
	latest, err := latestBackupTime(backupsDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup cadence:", err)
		return 1
	}
	if !latest.IsZero() && now.Sub(latest) < interval {
		return 0
	}
	return backupCmd()
}

func latestBackupTime(dir string) (time.Time, error) {
	var latest time.Time
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), collection+"-") ||
			!strings.HasSuffix(entry.Name(), ".snapshot") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, err
}

func rotateBackups(dir string) (recent, archived int) {
	m, _ := filepath.Glob(filepath.Join(dir, collection+"-*.snapshot"))
	sort.Strings(m)
	archiveDir := filepath.Join(dir, "archive")
	if len(m) <= keepBackup {
		return len(m), 0
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil { //nolint:gosec // user-owned backup archive
		return len(m), 0
	}
	for _, source := range m[:len(m)-keepBackup] {
		destination := filepath.Join(archiveDir, filepath.Base(source))
		if _, err := os.Stat(destination); err == nil {
			continue // preserve both copies rather than overwrite either one
		}
		if err := os.Rename(source, destination); err == nil {
			archived++
		}
	}
	return len(m) - archived, archived
}

// restoreCmd uploads a snapshot file and recovers the collection from it
// (REPLACES current data). Destructive — asks unless --yes.
func restoreCmd(path string, yes bool) int {
	if path == "" {
		fmt.Fprintln(os.Stderr, "usage: ariadnectl restore <snapshot> [--yes]")
		return 2
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintln(os.Stderr, "no such snapshot:", path)
		return 2
	}
	if !yes {
		fmt.Printf("Restore REPLACES collection %q with %s. Continue? [y/N] ", collection, filepath.Base(path))
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() || sc.Text() != "y" {
			fmt.Println("aborted")
			return 0
		}
	}
	url := qdrantREST + "/collections/" + collection + "/snapshots/upload?priority=snapshot"
	if err := uploadMultipart(url, "snapshot", path); err != nil {
		fmt.Fprintln(os.Stderr, "restore:", err)
		return 1
	}
	fmt.Println("restore ok:", filepath.Base(path))
	return 0
}

// exportCmd scrolls all points and writes portable JSONL (text + metadata,
// no vectors — re-embeddable by any model on import).
func exportCmd(path string) int {
	if path == "" {
		path = filepath.Join(backupsDir(), collection+"-"+time.Now().Format("20060102-150405")+".jsonl")
		_ = os.MkdirAll(backupsDir(), 0o755) //nolint:gosec // user-owned
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // user-provided path
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		return 1
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	var offset any
	total := 0
	for {
		req := map[string]any{"limit": 256, "with_payload": true, "with_vector": false}
		if offset != nil {
			req["offset"] = offset
		}
		body, ok := postJSON(qdrantREST+"/collections/"+collection+"/points/scroll", req)
		if !ok {
			fmt.Fprintln(os.Stderr, "scroll failed")
			return 1
		}
		res, _ := body["result"].(map[string]any)
		pts, _ := res["points"].([]any)
		for _, p := range pts {
			pm, _ := p.(map[string]any)
			pl, _ := pm["payload"].(map[string]any)
			line := map[string]any{
				"text": strOf(pl["text"]), "wing": strOf(pl["wing"]), "room": strOf(pl["room"]),
			}
			for _, field := range []string{
				"ts", "observed_at", "occurred_at", "session_started_at", "session_ended_at",
				"last_seen_at", "source_modified_at", "consolidated_at", "consolidation_checked_at",
				"consolidation_first_empty_at", "superseded_at", "orphaned_at", "source_tokens", "memory_tokens",
				"consolidation_attempts", "provenance", "source_id", "source_kind", "source_key", "source_revision",
				"content_hash", "identity_version", "superseded_by", "superseded_reason", "status", "memory_type",
				"consolidation_status",
			} {
				if value, exists := pl[field]; exists {
					line[field] = value
				}
			}
			b, _ := json.Marshal(line)
			_, _ = w.Write(append(b, '\n'))
			total++
		}
		offset = res["next_page_offset"]
		if offset == nil || len(pts) == 0 {
			break
		}
	}
	fmt.Printf("export ok: %d memories → %s\n", total, path)
	return 0
}

// --- HTTP helpers ---

func postJSON(url string, payload any) (map[string]any, bool) {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = bytes.NewReader(b)
	}
	var out map[string]any
	if err := httpDo(http.MethodPost, url, rdr, &out); err != nil {
		return nil, false
	}
	return out, true
}

func httpDo(method, url string, body io.Reader, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := newQdrantRequest(ctx, method, url, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber() // preserve uint64 point offsets across paginated exports
		return decoder.Decode(out)
	}
	return nil
}

func download(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, err := newQdrantRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // user-owned backups path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, resp.Body)
	return err
}

func uploadMultipart(url, field, path string) error {
	f, err := os.Open(path) //nolint:gosec // user-provided path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return err
	}
	_ = mw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, err := newQdrantRequest(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func mapPath(m map[string]any, keys ...string) (string, bool) {
	cur := any(m)
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = mm[k]
	}
	s, ok := cur.(string)
	return s, ok
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
