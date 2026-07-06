package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	osDarwin = "darwin"
	osLinux  = "linux"
)

type qdrantState int

const (
	qdNone        qdrantState = iota // nothing on the ports → install our own
	qdOurs                           // healthy Qdrant run from ~/.ariadne
	qdForeign                        // healthy Qdrant we did NOT install → reuse, never touch
	qdBusyNot                        // port busy by something that is not Qdrant → abort
	qdUnreachable                    // remote host given but not answering → abort
)

type report struct {
	os, arch  string
	ramGB     int64
	freeGB    int64
	gpu       string // human verdict line
	gpuOK     bool   // false = CPU-only warning
	gpuFatal  bool
	ramFatal  bool
	diskFatal bool

	qdrant struct {
		state    qdrantState
		version  string
		colls    []string
		points   int64 // points in our collection, -1 if absent
		remote   bool
		grpcOK   bool
		bindWide bool // foreign instance listens beyond loopback
	}
	ollama struct {
		up         bool
		version    string
		hasModel   bool
		hasSummary bool
		remote     bool
	}
	repoRoot     string
	goOK         bool
	svcInstalled bool // our service unit/agent file exists
	mcpOK        bool // ~/.claude.json points at ~/.ariadne/bin/ariadne
	skillOK      bool
	hooksOK      bool // session hooks registered in ~/.claude/settings.json
	home         string
}

func (r *report) fatal() bool {
	return r.ramFatal || r.diskFatal || r.gpuFatal ||
		r.qdrant.state == qdBusyNot || r.qdrant.state == qdUnreachable ||
		!r.ollama.up || r.repoRoot == "" || !r.goOK
}

func preflight(o opts) *report {
	r := &report{}
	r.os, r.arch = runtime.GOOS, runtime.GOARCH
	r.home, _ = os.UserHomeDir()
	r.ramGB = totalRAMGB()
	r.freeGB = freeDiskGB(r.home)
	r.gpu, r.gpuOK, r.gpuFatal = gpuVerdict()
	r.ramFatal = r.ramGB > 0 && r.ramGB < 6
	r.diskFatal = r.freeGB >= 0 && r.freeGB < 5

	detectQdrant(r, o)
	detectOllama(r, o)

	r.repoRoot = findRepoRoot()
	r.goOK = which("go")
	r.svcInstalled = fileExists(servicePath(r))
	r.mcpOK = mcpRegistered(r.home)
	r.skillOK = fileExists(filepath.Join(r.home, ".claude", "skills", "ariadne", "SKILL.md"))
	if b, err := os.ReadFile(filepath.Join(r.home, ".claude", "settings.json")); err == nil { //nolint:gosec // own config
		r.hooksOK = strings.Contains(string(b), "ariadne-hook")
	}
	return r
}

func printReport(r *report) {
	fmt.Println("\n[preflight]")
	line := func(ok bool, fatal bool, s string) {
		mark := "✓"
		if !ok {
			mark = "!"
		}
		if fatal {
			mark = "✗"
		}
		fmt.Printf("  %s %s\n", mark, s)
	}
	fmt.Printf("  · platform: %s/%s\n", r.os, r.arch)
	line(!r.ramFatal && r.ramGB >= 12, r.ramFatal,
		fmt.Sprintf("RAM %dGiB %s", r.ramGB, pick(r.ramFatal,
			"— INSUFFICIENT: bge-m3 + Qdrant need ≥6GiB. This machine cannot run Ariadne well.",
			pick(r.ramGB < 12, "(tight but workable)", ""))))
	line(!r.diskFatal, r.diskFatal,
		fmt.Sprintf("disk %dGiB free %s", r.freeGB, pick(r.diskFatal, "— need ≥5GiB (model ~1.3GiB + data)", "")))
	line(r.gpuOK, r.gpuFatal, r.gpu)

	switch r.qdrant.state {
	case qdOurs:
		line(true, false, fmt.Sprintf("Qdrant: OURS, healthy (v%s), collections: %s", r.qdrant.version, strings.Join(r.qdrant.colls, ", ")))
	case qdForeign:
		line(true, false, fmt.Sprintf("Qdrant: EXISTING %s instance (v%s) → will REUSE, never restart/reconfigure it",
			pick(r.qdrant.remote, "remote", "local"), r.qdrant.version))
		if r.qdrant.bindWide {
			line(false, false, "  that Qdrant listens beyond loopback — your memories would sit on an exposed instance (no auth by default)")
		}
	case qdNone:
		line(true, false, "Qdrant: none found → will install our own (loopback-only) into ~/.ariadne")
	case qdBusyNot:
		line(false, true, fmt.Sprintf("port %d is busy but it is NOT Qdrant — refusing to touch it; "+
			"free the port or pass -qdrant-rest/-qdrant-grpc", 6333))
	case qdUnreachable:
		line(false, true, "the Qdrant host you passed does not answer /healthz — check host/port")
	}
	if r.qdrant.state == qdOurs || r.qdrant.state == qdForeign {
		if r.qdrant.points >= 0 {
			line(true, false, fmt.Sprintf("  collection present: %d points (will be kept as-is)", r.qdrant.points))
		}
		line(r.qdrant.grpcOK, !r.qdrant.grpcOK, pick(r.qdrant.grpcOK,
			"  gRPC port reachable", "  gRPC port NOT reachable — the MCP server needs it"))
	}

	line(r.ollama.up, !r.ollama.up, pick(r.ollama.up,
		fmt.Sprintf("Ollama up (v%s)%s", r.ollama.version, pick(r.ollama.remote, " [remote]", "")),
		"Ollama NOT running — install it first: macOS `brew install ollama && brew services start ollama`, "+
			"Linux `curl -fsSL https://ollama.com/install.sh | sh`"))
	if r.ollama.up {
		line(r.ollama.hasModel, false, pick(r.ollama.hasModel, "bge-m3 present", "bge-m3 missing → will pull (~1.3GiB)"))
		line(r.ollama.hasSummary, false, pick(r.ollama.hasSummary,
			"summary model present", "summary model missing → will pull (~4.7GiB; used by session auto-capture)"))
	}
	line(r.repoRoot != "", r.repoRoot == "", pick(r.repoRoot != "", "repo root: "+r.repoRoot,
		"run me from the ariadne repo root (go.mod with `module ariadne` not found)"))
	line(r.goOK, !r.goOK, pick(r.goOK, "go toolchain present", "go toolchain missing — install Go first"))
}

// --- detection helpers ---

func detectQdrant(r *report, o opts) {
	base := fmt.Sprintf("http://%s:%d", o.qdrantHost, o.qdrantREST)
	r.qdrant.remote = o.qdrantHost != "127.0.0.1" && o.qdrantHost != "localhost"
	r.qdrant.points = -1

	if !getOK(base + "/healthz") {
		if r.qdrant.remote {
			r.qdrant.state = qdUnreachable
			return
		}
		if tcpOpen(o.qdrantHost, o.qdrantREST) {
			r.qdrant.state = qdBusyNot
			return
		}
		r.qdrant.state = qdNone
		return
	}
	// healthy Qdrant answered
	if root, ok := getJSON(base + "/"); ok {
		r.qdrant.version, _ = root["version"].(string)
	}
	if cl, ok := getJSON(base + "/collections"); ok {
		if res, k := cl["result"].(map[string]any); k {
			if arr, k2 := res["collections"].([]any); k2 {
				for _, c := range arr {
					if m, k3 := c.(map[string]any); k3 {
						if n, k4 := m["name"].(string); k4 {
							r.qdrant.colls = append(r.qdrant.colls, n)
						}
					}
				}
			}
		}
	}
	if info, ok := getJSON(base + "/collections/" + o.collection); ok {
		if res, k := info["result"].(map[string]any); k {
			if f, k2 := res["points_count"].(float64); k2 {
				r.qdrant.points = int64(f)
			}
		}
	}
	r.qdrant.grpcOK = tcpOpen(o.qdrantHost, o.qdrantGRPC)

	if r.qdrant.remote {
		r.qdrant.state = qdForeign
		return
	}
	if oursOnPort(o.qdrantREST) {
		r.qdrant.state = qdOurs
	} else {
		r.qdrant.state = qdForeign
		r.qdrant.bindWide = listensBeyondLoopback(o.qdrantREST)
	}
}

func detectOllama(r *report, o opts) {
	r.ollama.remote = !strings.Contains(o.ollamaURL, "localhost") && !strings.Contains(o.ollamaURL, "127.0.0.1")
	if v, ok := getJSON(strings.TrimRight(o.ollamaURL, "/") + "/api/version"); ok {
		r.ollama.up = true
		r.ollama.version, _ = v["version"].(string)
	}
	if !r.ollama.up {
		return
	}
	if tags, ok := getJSON(strings.TrimRight(o.ollamaURL, "/") + "/api/tags"); ok {
		b, _ := json.Marshal(tags)
		r.ollama.hasModel = strings.Contains(string(b), `"`+o.model)
		r.ollama.hasSummary = strings.Contains(string(b), `"`+o.summaryModel)
	}
}

func gpuVerdict() (msg string, ok, fatal bool) {
	switch runtime.GOOS {
	case osDarwin:
		if runtime.GOARCH == "arm64" {
			return "GPU: Apple Silicon (Metal) — embeddings run on GPU", true, false
		}
		return "GPU: Intel Mac — NO Metal acceleration for Ollama: embeddings on CPU, ~10x slower. " +
			"Interactive use OK, bulk imports will crawl.", false, false
	case "linux":
		out, err := exec.CommandContext(context.Background(),
			"nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader").Output()
		if err == nil && len(out) > 0 {
			gpu := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			return "GPU: " + gpu + " (CUDA) — embeddings run on GPU", true, false
		}
		return "GPU: none found — embeddings will run on CPU: ~10x slower than GPU. " +
			"Interactive recall/save is fine; bulk backfills will be slow. " +
			"For heavy imports point -ollama at a GPU box instead.", false, false
	default:
		return "GPU: unsupported OS " + runtime.GOOS, false, true
	}
}

func totalRAMGB() int64 {
	switch runtime.GOOS {
	case osDarwin:
		out, err := exec.CommandContext(context.Background(), "sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return -1
		}
		b, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return b / (1 << 30)
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return -1
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "MemTotal:") {
				f := strings.Fields(l)
				if len(f) >= 2 {
					kb, _ := strconv.ParseInt(f[1], 10, 64)
					return kb / (1 << 20)
				}
			}
		}
	}
	return -1
}

func freeDiskGB(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) / (1 << 30) //nolint:gosec,unconvert // sizes fit int64; field types differ per OS
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for i := 0; i < 4; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // repo detection under cwd
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "module ariadne") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func servicePath(r *report) string {
	if r.os == osDarwin {
		return filepath.Join(r.home, "Library", "LaunchAgents", "com.ariadne.qdrant.plist")
	}
	return filepath.Join(r.home, ".config", "systemd", "user", "ariadne-qdrant.service")
}

func syncAgentPath(r *report) string {
	if r.os == osDarwin {
		return filepath.Join(r.home, "Library", "LaunchAgents", "com.ariadne.sync.plist")
	}
	return filepath.Join(r.home, ".config", "systemd", "user", "ariadne-sync.timer")
}

func trayAutostartPath(r *report) string {
	if r.os == osDarwin {
		return filepath.Join(r.home, "Library", "LaunchAgents", "com.ariadne.tray.plist")
	}
	return filepath.Join(r.home, ".config", "autostart", "ariadne-tray.desktop")
}

// swiftMonitorPresent reports whether the native macOS Swift monitor is already
// set up — if so, the installer leaves the Go tray autostart alone (no dupes).
func swiftMonitorPresent(r *report) bool {
	return r.os == osDarwin &&
		fileExists(filepath.Join(r.home, "Library", "LaunchAgents", "com.ariadne.monitor.plist"))
}

func mcpRegistered(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, ".claude.json")) //nolint:gosec // own config
	if err != nil {
		return false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	srv, _ := m["mcpServers"].(map[string]any)
	ar, _ := srv["ariadne"].(map[string]any)
	cmd, _ := ar["command"].(string)
	return strings.Contains(cmd, ".ariadne/bin/ariadne")
}

// --- tiny probes ---

func httpc() *http.Client { return &http.Client{Timeout: 3 * time.Second} }

func getOK(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := httpc().Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 300
}

func getJSON(url string) (map[string]any, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := httpc().Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return nil, false
	}
	return m, true
}

func tcpOpen(host string, port int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// oursOnPort reports whether the process LISTENING on the given port was
// started from ~/.ariadne — i.e. it is Ariadne's own Qdrant, not a foreign one.
// A global process-list match is not enough: our instance on 6333 must not make
// a second instance on another port look "ours".
func oursOnPort(port int) bool {
	for _, pid := range listenerPids(port) {
		out, err := exec.CommandContext(context.Background(), "ps", "-o", "args=", "-p", pid).Output() //nolint:gosec // pid from lsof/ss
		if err == nil && strings.Contains(string(out), ".ariadne/bin/qdrant") {
			return true
		}
	}
	return false
}

func listenerPids(port int) []string {
	if runtime.GOOS == osDarwin {
		out, err := exec.CommandContext(context.Background(), //nolint:gosec // fixed command, int-derived arg
			"lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
		if err != nil {
			return nil
		}
		return strings.Fields(string(out))
	}
	// linux: `ss -ltnp` lines look like: ... users:(("qdrant",pid=1234,fd=25))
	out, err := exec.CommandContext(context.Background(), "ss", "-ltnp").Output()
	if err != nil {
		return nil
	}
	var pids []string
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.Contains(l, ":"+strconv.Itoa(port)+" ") || !strings.Contains(l, "pid=") {
			continue
		}
		rest := l[strings.Index(l, "pid=")+4:]
		end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
		if end > 0 {
			pids = append(pids, rest[:end])
		}
	}
	return pids
}

// listensBeyondLoopback: best-effort check of the listener address (macOS lsof / Linux ss).
func listensBeyondLoopback(port int) bool {
	var out []byte
	var err error
	if runtime.GOOS == osDarwin {
		out, err = exec.CommandContext(context.Background(), //nolint:gosec // fixed command, int-derived arg
			"lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	} else {
		out, err = exec.CommandContext(context.Background(), "ss", "-ltn").Output()
	}
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.Contains(l, strconv.Itoa(port)) {
			continue
		}
		if strings.Contains(l, "*:"+strconv.Itoa(port)) || strings.Contains(l, "0.0.0.0:"+strconv.Itoa(port)) ||
			strings.Contains(l, "[::]:"+strconv.Itoa(port)) {
			return true
		}
	}
	return false
}

func which(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
