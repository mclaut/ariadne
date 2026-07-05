package main

import (
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

// backupCmd creates a Qdrant snapshot, downloads it OUTSIDE qdrant-data (so it
// survives loss of the data dir), removes the in-engine copy, and rotates.
func backupCmd() int {
	dir := backupsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-owned backups dir
		fmt.Fprintln(os.Stderr, "mkdir backups:", err)
		return 1
	}
	// 1. create snapshot
	body, ok := postJSON(qdrantREST+"/collections/"+collection+"/snapshots", nil)
	if !ok {
		fmt.Fprintln(os.Stderr, "snapshot create failed (is Qdrant up?)")
		return 1
	}
	name, _ := mapPath(body, "result", "name")
	if name == "" {
		fmt.Fprintln(os.Stderr, "snapshot: no name in response")
		return 1
	}
	// 2. download it
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(dir, collection+"-"+ts+".snapshot")
	if err := download(qdrantREST+"/collections/"+collection+"/snapshots/"+name, dest); err != nil {
		fmt.Fprintln(os.Stderr, "download snapshot:", err)
		return 1
	}
	// 3. drop the in-engine snapshot (keep qdrant-data lean)
	_ = httpDo(http.MethodDelete, qdrantREST+"/collections/"+collection+"/snapshots/"+name, nil, nil)
	// 4. rotate
	n := rotateBackups(dir)
	fi, _ := os.Stat(dest)
	fmt.Printf("backup ok: %s (%dMB) · %d kept\n", filepath.Base(dest), fi.Size()/(1024*1024), n)
	return 0
}

func rotateBackups(dir string) int {
	m, _ := filepath.Glob(filepath.Join(dir, collection+"-*.snapshot"))
	sort.Strings(m)
	for len(m) > keepBackup {
		_ = os.Remove(m[0])
		m = m[1:]
	}
	return len(m)
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
	f, err := os.Create(path) //nolint:gosec // user-provided path
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
	req, err := http.NewRequestWithContext(ctx, method, url, body)
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
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func download(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest) //nolint:gosec // user-owned backups path
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
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
