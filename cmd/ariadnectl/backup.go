package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/backupretention"
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
	"time"
)

const (
	backupsSub = ".ariadne/backups"
)

func backupsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, backupsSub)
}

// backupCmd creates a Qdrant snapshot and downloads it OUTSIDE qdrant-data so
// it survives loss of the data dir. One verified generation is retained per
// collection, and legacy/native copies rotate only after recovery verification.
func backupCmd() (code int) {
	status, message := "failed", ""
	defer func() {
		counters := map[string]int64{"copies_per_object": 1}
		_ = activity.Append(activity.Event{
			Operation: "backup", Status: status, Message: message, Counters: counters,
		})
	}()
	if _, err := os.Lstat(fullPlanPath()); err == nil {
		code = fullBackupCmd()
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "full backup plan:", err)
		code = 1
	} else {
		code = managedBackupCmd()
	}
	if code != 0 {
		message = "verified backup or retention failed"
		return code
	}
	status = backupStatusComplete
	return 0
}

// backupIfDue runs the automatic snapshot path at most once per interval.
// Manual `ariadnectl backup` remains unconditional.
func backupIfDue(now time.Time, interval time.Duration) int {
	if interval <= 0 {
		interval = 7 * 24 * time.Hour
	}
	if _, err := os.Lstat(fullPlanPath()); err == nil {
		m, verifyErr := backupretention.Verify(filepath.Join(managedBackupsDir(), "full"))
		if verifyErr != nil && !os.IsNotExist(verifyErr) {
			fmt.Fprintln(os.Stderr, "full backup cadence:", verifyErr)
			return 1
		}
		if verifyErr == nil && m.Object == "full" && m.Metadata["restore_verified"] == backupProofVerified && now.Sub(m.CreatedAt) < interval {
			return 0
		}
		return backupCmd()
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "full backup cadence:", err)
		return 1
	}
	names, err := backupCollections()
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup cadence:", err)
		return 1
	}
	allFresh := true
	for _, name := range names {
		latest, timeErr := latestManagedBackupTime(backupsDir(), name)
		if timeErr != nil {
			fmt.Fprintln(os.Stderr, "backup cadence:", timeErr)
			return 1
		}
		if latest.IsZero() || now.Sub(latest) >= interval {
			allFresh = false
		}
	}
	if allFresh {
		return 0
	}
	return backupCmd()
}

func latestBackupTime(dir string) (time.Time, error) { return latestManagedBackupTime(dir, collection) }

func latestManagedBackupTime(dir, name string) (time.Time, error) {
	m, err := backupretention.Verify(filepath.Join(dir, "previous", "snapshot-"+name))
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if err = checkSnapshotManifest(m, name); err != nil {
		return time.Time{}, err
	}
	return m.CreatedAt, nil
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
	resp, err := (&http.Client{Timeout: 120 * time.Second, CheckRedirect: noQdrantRedirect}).Do(req)
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
	resp, err := (&http.Client{Timeout: 300 * time.Second, CheckRedirect: noQdrantRedirect}).Do(req)
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
	if err != nil {
		return err
	}
	return f.Sync()
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
	resp, err := (&http.Client{Timeout: 300 * time.Second, CheckRedirect: noQdrantRedirect}).Do(req)
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

// Qdrant requests are scoped to exact resources; redirects must never retarget a
// snapshot deletion or forward the api-key header to another host.
func noQdrantRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
