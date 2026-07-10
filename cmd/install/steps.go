package main

import (
	"archive/tar"
	"ariadne/internal/store"
	"compress/gzip"
	"context"
	"crypto/sha256"
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
			title: fmt.Sprintf("pull summary model %q (session auto-capture)", o.summaryModel),
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
		{
			title: "register daily memfiles-sync agent (keeps memory notes true to the store)",
			skip:  fileExists(syncAgentPath(r)),
			run:   func() error { return installSyncAgent(r) },
		},
		{
			title: "install tray-monitor autostart (Linux: autostart entry; macOS: LaunchAgent)",
			skip:  fileExists(trayAutostartPath(r)),
			run:   func() error { return installTrayAutostart(r) },
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
		return "aarch64-unknown-linux-musl", nil
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
		asset := "qdrant-" + target + ".tar.gz"
		url := fmt.Sprintf("https://github.com/qdrant/qdrant/releases/download/%s/%s", o.qdrantVersion, asset)
		sum, err := qdrantAssetDigest(o.qdrantVersion, asset)
		if err != nil {
			if envSum := os.Getenv("ARIADNE_QDRANT_SHA256"); envSum != "" {
				sum = envSum
				err = nil
			}
		}
		if err != nil {
			return err
		}
		fmt.Printf("    downloading %s\n", url)
		if err := downloadQdrant(url, sum, dest); err != nil {
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

// installSyncAgent registers the daily memfiles-sync: a launchd agent on macOS,
// a systemd user timer+oneshot on Linux. Mirrors installService.
func installSyncAgent(r *report) error {
	dst := syncAgentPath(r)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // user-owned
		return err
	}
	if r.os == osDarwin {
		tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", "com.ariadne.sync.plist")) //nolint:gosec // repo file
		if err != nil {
			return err
		}
		rendered := strings.ReplaceAll(string(tpl), "__HOME__", r.home)
		if err := os.WriteFile(dst, []byte(rendered), 0o644); err != nil { //nolint:gosec // launchd reads it
			return err
		}
		uid := strconv.Itoa(os.Getuid())
		_ = runCmd("launchctl", "bootout", "gui/"+uid+"/com.ariadne.sync") // ignore: may not be loaded
		return runCmd("launchctl", "bootstrap", "gui/"+uid, dst)
	}
	// linux: oneshot service + daily timer (systemd user units use %h natively)
	unitDir := filepath.Dir(dst)
	for _, name := range []string{"ariadne-sync.service", "ariadne-sync.timer"} {
		tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", name)) //nolint:gosec // repo file
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(unitDir, name), tpl, 0o644); err != nil { //nolint:gosec // systemd reads it
			return err
		}
	}
	// A headless box has no user D-Bus session, so `systemctl --user` fails with
	// "Failed to connect to bus"; linger starts a persistent user manager. Even
	// then the sync agent is a nice-to-have, so never abort the install over it.
	enableLinger()
	if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
		fmt.Fprintf(os.Stderr, "    ⚠ systemd --user unavailable (%v) — sync agent skipped; "+
			"run `~/.ariadne/bin/import -source memfiles -sync` manually or from a system timer\n", err)
		return nil
	}
	if err := runCmd("systemctl", "--user", "enable", "--now", "ariadne-sync.timer"); err != nil {
		fmt.Fprintf(os.Stderr, "    ⚠ could not enable the sync timer (%v) — non-critical, run the sync by hand\n", err)
	}
	return nil
}

// enableLinger starts a persistent `systemctl --user` manager for the current
// user so user units work on a headless box (no login session / user D-Bus).
// Best-effort: loginctl uses the system bus, which exists even headless.
func enableLinger() {
	if u := os.Getenv("USER"); u != "" {
		_ = runCmd("loginctl", "enable-linger", u)
	}
}

// ensureDeps auto-installs OS prerequisites BEFORE preflight inspects them, so a
// clean machine bootstraps to a working stack. Best-effort — failures warn and
// continue; skip with -skip-deps. (Windows deps belong to the future Win branch.)
func ensureDeps(o opts) {
	if o.skipDeps {
		return
	}
	switch runtime.GOOS {
	case osLinux:
		ensureDepsLinux(o)
	case osDarwin:
		ensureDepsDarwin(o)
	}
}

// ensureDepsLinux installs the tray's desktop libs (notifications, xdg-open, the
// GNOME AppIndicator extension) and Ollama (official script) if local + missing.
func ensureDepsLinux(o opts) {
	fmt.Println("\n[deps] Linux prerequisites (skip with -skip-deps)")
	switch {
	case which("apt-get"):
		if o.dryRun {
			fmt.Println("    would: sudo apt-get update && sudo apt-get install -y " +
				"libnotify-bin xdg-utils gnome-shell-extension-appindicator")
		} else {
			runVisible("sudo", "apt-get", "update")
			runVisible("sudo", "apt-get", "install", "-y",
				"libnotify-bin", "xdg-utils", "gnome-shell-extension-appindicator")
		}
	case which("dnf"):
		pkgInstall(o, "dnf", "install", "-y", "libnotify", "xdg-utils", "gnome-shell-extension-appindicator")
	case which("pacman"):
		pkgInstall(o, "pacman", "-S", "--needed", "--noconfirm", "libnotify", "xdg-utils")
	default:
		fmt.Println("    unknown package manager — install manually: libnotify-bin xdg-utils gnome-shell-extension-appindicator")
	}
	if !o.remoteOllama() && !which("ollama") {
		if o.dryRun {
			fmt.Println("    would install Ollama: curl -fsSL https://ollama.com/install.sh | sh")
		} else if o.strictSupplyChain {
			fmt.Println("    Ollama missing — strict supply-chain mode will not run curl|sh; install Ollama manually and re-run")
		} else {
			fmt.Println("    installing Ollama…")
			runVisible("sh", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
			waitOllama(o.ollamaURL) // let the service bind before preflight checks it
		}
	}
}

// ensureDepsDarwin installs Ollama via Homebrew if it's local and missing. macOS
// needs no extra desktop libs for the tray (those are Linux-only). Homebrew
// itself we don't auto-install (its own installer is interactive/sudo-heavy) —
// we guide instead, which is the one manual step on a brand-new Mac.
func ensureDepsDarwin(o opts) {
	if o.remoteOllama() || which("ollama") {
		return
	}
	fmt.Println("\n[deps] macOS: Ollama (skip with -skip-deps)")
	if !which("brew") {
		fmt.Println("    Homebrew not found — install it (https://brew.sh) or Ollama.app " +
			"(https://ollama.com/download), then re-run")
		return
	}
	if o.dryRun {
		fmt.Println("    would: brew install ollama && brew services start ollama")
		return
	}
	runVisible("brew", "install", "ollama")
	runVisible("brew", "services", "start", "ollama")
	waitOllama(o.ollamaURL)
}

// waitOllama blocks until the freshly-installed Ollama API answers (or ~30s
// elapse), so the just-started systemd service doesn't lose the race against the
// preflight check that immediately follows.
func waitOllama(url string) {
	base := strings.TrimRight(url, "/")
	for i := 0; i < 30; i++ {
		if getOK(base + "/api/version") {
			fmt.Println("    Ollama is up")
			return
		}
		time.Sleep(time.Second)
	}
	fmt.Fprintln(os.Stderr, "    ⚠ Ollama installed but not answering yet — if preflight aborts, just re-run")
}

func pkgInstall(o opts, mgr string, args ...string) {
	if o.dryRun {
		fmt.Printf("    would: sudo %s %s\n", mgr, strings.Join(args, " "))
		return
	}
	runVisible("sudo", append([]string{mgr}, args...)...)
}

// runVisible runs a command with the terminal attached (so sudo can prompt) and
// only warns on failure — prerequisites are best-effort, never fatal.
func runVisible(bin string, args ...string) {
	fmt.Printf("    $ %s %s\n", bin, strings.Join(args, " "))
	cmd := exec.CommandContext(context.Background(), bin, args...) //nolint:gosec // fixed package-manager / installer argv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "    ⚠ %s failed (%v) — install it manually and re-run\n", bin, err)
	}
}

// installTrayAutostart makes the Go tray start with the desktop session: a
// LaunchAgent on macOS (migrating off the legacy Swift monitor), a
// ~/.config/autostart entry on Linux.
func installTrayAutostart(r *report) error {
	dst := trayAutostartPath(r)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // user-owned
		return err
	}
	if r.os == osDarwin {
		uid := strconv.Itoa(os.Getuid())
		// migrate off the legacy Swift monitor so the menu bar shows one icon
		_ = runCmd("launchctl", "bootout", "gui/"+uid+"/com.ariadne.monitor")                        // ignore: may not be loaded
		_ = os.Remove(filepath.Join(r.home, "Library", "LaunchAgents", "com.ariadne.monitor.plist")) //nolint:errcheck // may not exist
		tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", "com.ariadne.tray.plist"))       //nolint:gosec // repo file
		if err != nil {
			return err
		}
		rendered := strings.ReplaceAll(string(tpl), "__HOME__", r.home)
		if err := os.WriteFile(dst, []byte(rendered), 0o644); err != nil { //nolint:gosec // launchd reads it
			return err
		}
		_ = runCmd("launchctl", "bootout", "gui/"+uid+"/com.ariadne.tray") // ignore: may not be loaded
		return runCmd("launchctl", "bootstrap", "gui/"+uid, dst)
	}
	tpl, err := os.ReadFile(filepath.Join(r.repoRoot, "deploy", "ariadne-tray.desktop")) //nolint:gosec // repo file
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(tpl), "__HOME__", r.home)
	return os.WriteFile(dst, []byte(rendered), 0o644) //nolint:gosec // desktop entry, not a secret
}

func qdrantAssetDigest(version, asset string) (string, error) {
	api := "https://api.github.com/repos/qdrant/qdrant/releases/tags/" + version
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("qdrant release metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("qdrant release metadata: HTTP %d", resp.StatusCode)
	}
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name != asset {
			continue
		}
		if strings.HasPrefix(a.Digest, "sha256:") {
			return strings.TrimPrefix(a.Digest, "sha256:"), nil
		}
		return "", fmt.Errorf("qdrant asset %s has no sha256 digest; set ARIADNE_QDRANT_SHA256", asset)
	}
	return "", fmt.Errorf("qdrant asset %s not found in %s", asset, version)
}

func downloadQdrant(url, wantSHA256, dest string) error {
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
	tmp, err := os.CreateTemp(filepath.Dir(dest), "qdrant-*.tar.gz")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("qdrant checksum mismatch: got %s, want %s", got, wantSHA256)
	}
	f, err := os.Open(tmpName) //nolint:gosec // temp file was created by us in the runtime bin dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
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
	enableLinger() // headless: make `systemctl --user` work + start Qdrant on boot without login
	if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := runCmd("systemctl", "--user", "enable", "--now", "ariadne-qdrant"); err != nil {
		return err
	}
	return nil
}

func buildBinaries(r *report) error {
	for bin, pkg := range map[string]string{
		"ariadne": "ariadne", "ariadnectl": "ariadnectl", "import": "import",
		"ariadne-hook": "hook", "ariadne-tray": "ariadne-tray",
	} {
		dest := filepath.Join(r.home, ".ariadne", "bin", bin)
		next := dest + ".new"
		_ = os.Remove(next)
		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", next, "./cmd/"+pkg) //nolint:gosec // fixed argv
		cmd.Dir = r.repoRoot
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			_ = os.Remove(next)
			if bin == "ariadne-tray" {
				// the tray needs a C toolchain on macOS (Cocoa); if that's absent
				// the monitor is skipped but the pure-Go core stack is unaffected.
				fmt.Fprintf(os.Stderr, "    ⚠ tray build failed (%v) — monitor unavailable, core stack fine\n", err)
				continue
			}
			return fmt.Errorf("go build %s: %w", bin, err)
		}
		if err := os.Rename(next, dest); err != nil {
			_ = os.Remove(next)
			return fmt.Errorf("install %s: %w", bin, err)
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
