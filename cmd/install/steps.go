package main

import (
	"archive/tar"
	"ariadne/internal/store"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func makePlan(r *report, o opts) []action {
	home := r.home
	installOwnQdrant := r.qdrant.state == qdNone

	return []action{
		{
			title: "ensure ~/.ariadne/{bin,backups,logs,qdrant-data}",
			run:   func() error { return ensureDirs(home) },
		},
		{
			title: "install Qdrant binary + service (loopback-only)",
			skip:  !installOwnQdrant,
			run:   func() error { return installQdrant(r, o) },
		},
		{
			title: "build + install ariadne / ariadnectl / import → ~/.ariadne/bin",
			run:   func() error { return buildBinaries(r) },
		},
		{
			title: fmt.Sprintf("pull embedding model %q (~1.3GiB)", o.model),
			skip:  o.skipModel || !r.ollama.up || r.ollama.hasModel,
			run:   func() error { return pullModel(o, o.model) },
		},
		{
			title: fmt.Sprintf("pull summary model %q (~4.7GiB, session auto-capture)", o.summaryModel),
			skip:  o.skipModel || !r.ollama.up || r.ollama.hasSummary,
			run:   func() error { return pullModel(o, o.summaryModel) },
		},
		{
			title: fmt.Sprintf("create collection %q (existing data is never touched)", o.collection),
			skip:  r.qdrant.points >= 0, // already exists
			run:   func() error { return ensureCollection(o) },
		},
		{
			title: "register MCP server in ~/.claude.json (backup kept)",
			skip:  r.mcpOK && defaultsOnly(o),
			run:   func() error { return registerMCP(home, o) },
		},
		{
			title: "install Claude Code skill → ~/.claude/skills/ariadne (real copy, not symlink)",
			skip:  r.skillOK && !isSymlink(filepath.Join(home, ".claude", "skills", "ariadne")),
			run:   func() error { return installSkill(r) },
		},
		{
			title: "register session hooks in ~/.claude/settings.json (auto-recall + auto-capture; backup kept)",
			skip:  o.skipHooks || r.hooksOK,
			run:   func() error { return registerHooks(home) },
		},
	}
}

func defaultsOnly(o opts) bool {
	return (o.qdrantHost == "127.0.0.1" || o.qdrantHost == "localhost") &&
		o.qdrantGRPC == 6334 && o.ollamaURL == "http://localhost:11434" &&
		o.model == "bge-m3" && o.collection == "ariadne"
}

// --- steps ---

func ensureDirs(home string) error {
	for _, d := range []string{"bin", "backups", "logs", "qdrant-data"} {
		if err := os.MkdirAll(filepath.Join(home, ".ariadne", d), 0o755); err != nil { //nolint:gosec // user-owned
			return err
		}
	}
	return nil
}

func qdrantTarget() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu", nil
	}
	return "", fmt.Errorf("no Qdrant release for %s/%s — install Qdrant manually and re-run with -qdrant-host", runtime.GOOS, runtime.GOARCH)
}

func installQdrant(r *report, o opts) error {
	dest := filepath.Join(r.home, ".ariadne", "bin", "qdrant")
	if !fileExists(dest) {
		target, err := qdrantTarget()
		if err != nil {
			return err
		}
		url := "https://github.com/qdrant/qdrant/releases/latest/download/qdrant-" + target + ".tar.gz"
		fmt.Printf("    downloading %s\n", url)
		if err := downloadQdrant(url, dest); err != nil {
			return err
		}
	} else {
		fmt.Println("    binary already present, keeping it")
	}
	if err := installService(r); err != nil {
		return err
	}
	// wait for it
	base := fmt.Sprintf("http://%s:%d", o.qdrantHost, o.qdrantREST)
	for i := 0; i < 40; i++ {
		if getOK(base + "/healthz") {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("qdrant did not come up within 20s — check ~/.ariadne/logs/qdrant*.log")
}

func downloadQdrant(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download: HTTP %d (release asset for this platform may not exist)", resp.StatusCode)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("no `qdrant` binary inside the release archive")
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "qdrant" && hdr.Typeflag == tar.TypeReg {
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // executable
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr) //nolint:gosec // trusted GitHub release of qdrant
			cerr := f.Close()
			if err != nil {
				return err
			}
			return cerr
		}
	}
}

func installService(r *report) error {
	svc := servicePath(r)
	if err := os.MkdirAll(filepath.Dir(svc), 0o755); err != nil { //nolint:gosec // user-owned
		return err
	}
	if r.os == osDarwin {
		tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", "com.ariadne.qdrant.plist")) //nolint:gosec // repo file
		if err != nil {
			return err
		}
		rendered := strings.ReplaceAll(string(tpl), "__HOME__", r.home)
		if err := os.WriteFile(svc, []byte(rendered), 0o644); err != nil { //nolint:gosec // launchd reads it
			return err
		}
		uid := strconv.Itoa(os.Getuid())
		_ = runCmd("launchctl", "bootout", "gui/"+uid+"/com.ariadne.qdrant") // ignore: may not be loaded
		return runCmd("launchctl", "bootstrap", "gui/"+uid, svc)
	}
	// linux: systemd user unit (uses %h natively — copy as-is)
	tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", "ariadne-qdrant.service"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(svc, tpl, 0o644); err != nil { //nolint:gosec // systemd reads it
		return err
	}
	if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := runCmd("systemctl", "--user", "enable", "--now", "ariadne-qdrant"); err != nil {
		return err
	}
	fmt.Println("    hint: for start-on-boot without login run: loginctl enable-linger $USER")
	return nil
}

func buildBinaries(r *report) error {
	for bin, pkg := range map[string]string{
		"ariadne": "ariadne", "ariadnectl": "ariadnectl", "import": "import", "ariadne-hook": "hook",
	} {
		dest := filepath.Join(r.home, ".ariadne", "bin", bin)
		_ = os.Remove(dest)                                                                       // avoid overwriting a running binary in place
		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", dest, "./cmd/"+pkg) //nolint:gosec // fixed argv
		cmd.Dir = r.repoRoot
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build %s: %w", bin, err)
		}
	}
	return nil
}

func pullModel(o opts, model string) error {
	if which("ollama") {
		cmd := exec.CommandContext(context.Background(), "ollama", "pull", model) //nolint:gosec // fixed argv
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	// no CLI (remote ollama): use the API and wait
	body := strings.NewReader(fmt.Sprintf(`{"name":%q}`, model))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(o.ollamaURL, "/")+"/api/pull", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // stream until the pull finishes
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ollama pull: HTTP %d", resp.StatusCode)
	}
	return nil
}

func ensureCollection(o opts) error {
	st, err := store.New(o.qdrantHost, o.qdrantGRPC, o.ollamaURL, o.model, o.collection)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return st.EnsureCollection(ctx)
}

func registerMCP(home string, o opts) error {
	path := filepath.Join(home, ".claude.json")
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // own config
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("~/.claude.json is not valid JSON, refusing to touch it: %w", err)
		}
		backup := path + ".bak-ariadne-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, b, 0o600); err != nil { //nolint:gosec // backup of own config
			return err
		}
	}
	srv, _ := m["mcpServers"].(map[string]any)
	if srv == nil {
		srv = map[string]any{}
	}
	env := map[string]any{}
	if o.qdrantHost != "127.0.0.1" && o.qdrantHost != "localhost" {
		env["ARIADNE_QDRANT_HOST"] = o.qdrantHost
	}
	if o.qdrantGRPC != 6334 {
		env["ARIADNE_QDRANT_PORT"] = strconv.Itoa(o.qdrantGRPC)
	}
	if o.ollamaURL != "http://localhost:11434" {
		env["ARIADNE_OLLAMA"] = o.ollamaURL
	}
	if o.model != "bge-m3" {
		env["ARIADNE_MODEL"] = o.model
	}
	if o.collection != "ariadne" {
		env["ARIADNE_COLLECTION"] = o.collection
	}
	srv["ariadne"] = map[string]any{
		"type":    "stdio",
		"command": filepath.Join(home, ".ariadne", "bin", "ariadne"),
		"args":    []any{},
		"env":     env,
	}
	m["mcpServers"] = srv
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// registerHooks merges Ariadne's session hooks into ~/.claude/settings.json:
// SessionStart (startup|resume|clear) → auto-recall, SessionEnd → auto-capture.
func registerHooks(home string) error {
	path := filepath.Join(home, ".claude", "settings.json")
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // own config
		if strings.Contains(string(b), "ariadne-hook") {
			return nil
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("~/.claude/settings.json is not valid JSON, refusing to touch it: %w", err)
		}
		backup := path + ".bak-ariadne-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, b, 0o600); err != nil { //nolint:gosec // backup of own config
			return err
		}
	}
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	bin := filepath.Join(home, ".ariadne", "bin", "ariadne-hook")
	add := func(event, matcher, sub string, timeout int) {
		entry := map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": bin + " " + sub, "timeout": timeout}},
		}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		arr, _ := hooks[event].([]any)
		hooks[event] = append(arr, entry)
	}
	add("SessionStart", "startup|resume|clear", "session-start", 15)
	add("SessionEnd", "", "session-end", 10)
	m["hooks"] = hooks
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func installSkill(r *report) error {
	src := filepath.Join(r.repoRoot, "skills", "ariadne")
	dest := filepath.Join(r.home, ".claude", "skills", "ariadne")
	if isSymlink(dest) {
		_ = os.Remove(dest) // symlinked skills are not discovered at session start
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // user-owned
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // user-owned
		}
		b, err := os.ReadFile(p) //nolint:gosec // repo files
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, b, info.Mode().Perm()) //nolint:gosec // repo skill → ~/.claude/skills
	})
}

func verify(o opts) bool {
	ok := true
	check := func(good bool, s string) {
		mark := "✓"
		if !good {
			mark, ok = "✗", false
		}
		fmt.Printf("  %s %s\n", mark, s)
	}
	base := fmt.Sprintf("http://%s:%d", o.qdrantHost, o.qdrantREST)
	check(getOK(base+"/healthz"), "Qdrant answers /healthz")
	check(tcpOpen(o.qdrantHost, o.qdrantGRPC), "Qdrant gRPC reachable")
	pts := int64(-1)
	status := ""
	if info, k := getJSON(base + "/collections/" + o.collection); k {
		if res, k2 := info["result"].(map[string]any); k2 {
			if f, k3 := res["points_count"].(float64); k3 {
				pts = int64(f)
			}
			status, _ = res["status"].(string)
		}
	}
	check(pts >= 0 && status == "green", fmt.Sprintf("collection %q green (%d points)", o.collection, pts))
	tags, k := getJSON(strings.TrimRight(o.ollamaURL, "/") + "/api/tags")
	hasModel := false
	if k {
		b, _ := json.Marshal(tags)
		hasModel = strings.Contains(string(b), `"`+o.model)
	}
	check(hasModel, "embedding model present: "+o.model)
	home, _ := os.UserHomeDir()
	check(mcpRegistered(home), "MCP server registered in ~/.claude.json")
	check(fileExists(filepath.Join(home, ".claude", "skills", "ariadne", "SKILL.md")), "Claude Code skill installed")
	if !o.skipHooks {
		hooksOK := false
		if b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil { //nolint:gosec // own config
			hooksOK = strings.Contains(string(b), "ariadne-hook")
		}
		check(hooksOK, "session hooks registered (auto-recall + auto-capture)")
	}
	return ok
}

// --- helpers ---

func runCmd(bin string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), bin, args...) //nolint:gosec // fixed service controls
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
