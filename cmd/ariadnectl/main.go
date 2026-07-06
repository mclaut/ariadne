// Command ariadnectl is the control/health core for the ariadne stack.
// The Swift menu-bar app is a thin viewer that shells these subcommands; all
// logic lives here in Go.
//
//	status [-json]   health of Qdrant + Ollama + the collection, and any issues.
//	start | stop | restart   manage the native services (Qdrant LaunchAgent,
//	                         Ollama brew service).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	qdrantLabel = "com.ariadne.qdrant"
	qdrantData  = ".ariadne/qdrant-data" // runtime home is ~/.ariadne (outside any TCC-protected folder)
	diskWarnMB  = 2048                   // warn if the machine's free space drops under this
)

// Overridable for reused/remote Qdrant setups (the installer's -qdrant-* flags).
var (
	qdrantREST = envOr("ARIADNE_QDRANT_REST", "http://localhost:6333")
	ollamaURL  = envOr("ARIADNE_OLLAMA", "http://localhost:11434")
	collection = envOr("ARIADNE_COLLECTION", "ariadne")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type svc struct {
	Up      bool   `json:"up"`
	RSSMB   int64  `json:"rss_mb,omitempty"`
	Version string `json:"version,omitempty"`
}

type coll struct {
	Points int64  `json:"points"`
	Status string `json:"status"`
}

type status struct {
	TS         string   `json:"ts"`
	OK         bool     `json:"ok"`
	Qdrant     svc      `json:"qdrant"`
	Ollama     svc      `json:"ollama"`
	Collection coll     `json:"collection"`
	DataMB     int64    `json:"data_mb"`
	FreeGB     int64    `json:"free_gb"`
	Issues     []string `json:"issues"`
}

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "status":
		printStatus(hasFlag("-json"))
	case "start":
		control("start")
	case "stop":
		control("stop")
	case "restart":
		control("stop")
		time.Sleep(2 * time.Second)
		control("start")
	case "backup":
		os.Exit(backupCmd())
	case "restore":
		os.Exit(restoreCmd(arg(2), hasFlag("--yes")))
	case "export":
		os.Exit(exportCmd(arg(2)))
	default:
		fmt.Fprintln(os.Stderr, "usage: ariadnectl {status [-json] | start | stop | restart | backup | restore <file> | export [file]}")
		os.Exit(2)
	}
}

func hasFlag(f string) bool {
	for _, a := range os.Args[1:] {
		if a == f {
			return true
		}
	}
	return false
}

// arg returns positional os.Args[i] (skipping flags) or "".
func arg(i int) string {
	if i < len(os.Args) && !strings.HasPrefix(os.Args[i], "-") {
		return os.Args[i]
	}
	return ""
}

func gather() status {
	s := status{TS: time.Now().Format("2006-01-02T15:04:05-07:00")}

	// Qdrant
	if getOK(qdrantREST + "/healthz") {
		s.Qdrant.Up = true
		s.Qdrant.RSSMB = rss(".ariadne/bin/qdrant") // the installed runtime binary
		if s.Qdrant.RSSMB == 0 {
			s.Qdrant.RSSMB = rss("/bin/qdrant") // dev runs from a repo checkout
		}
		if body, ok := getJSON(qdrantREST + "/collections/" + collection); ok {
			if r, k := body["result"].(map[string]any); k {
				s.Collection.Points = toInt(r["points_count"])
				s.Collection.Status, _ = r["status"].(string)
			}
		}
	}

	// Ollama
	if body, ok := getJSON(ollamaURL + "/api/version"); ok {
		s.Ollama.Up = true
		s.Ollama.Version, _ = body["version"].(string)
		s.Ollama.RSSMB = rss("ollama") + rss("llama-server")
	}

	home, _ := os.UserHomeDir()
	s.DataMB = dirSizeMB(filepath.Join(home, qdrantData))
	s.FreeGB = freeGB(home)

	// issues
	if !s.Qdrant.Up {
		s.Issues = append(s.Issues, "Qdrant DOWN")
	}
	if !s.Ollama.Up {
		s.Issues = append(s.Issues, "Ollama DOWN")
	}
	if s.Qdrant.Up && s.Collection.Status != "" && s.Collection.Status != "green" {
		s.Issues = append(s.Issues, "collection status: "+s.Collection.Status)
	}
	// NB: an empty collection is NOT an issue — a fresh install legitimately has
	// 0 memories until the user saves some; flagging it made the tray cry orange.
	if s.FreeGB*1024 < diskWarnMB {
		s.Issues = append(s.Issues, fmt.Sprintf("low disk: %dGB free", s.FreeGB))
	}
	s.OK = len(s.Issues) == 0
	return s
}

func printStatus(asJSON bool) {
	s := gather()
	if asJSON {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
		return
	}
	fmt.Printf("ariadne %s\n", pick(s.OK, "OK", "ISSUES"))
	fmt.Printf("  Qdrant : %s  (%dMB RSS)\n", upStr(s.Qdrant.Up), s.Qdrant.RSSMB)
	fmt.Printf("  Ollama : %s  %s  (%dMB RSS)\n", upStr(s.Ollama.Up), s.Ollama.Version, s.Ollama.RSSMB)
	fmt.Printf("  Points : %d  (%s)\n", s.Collection.Points, s.Collection.Status)
	fmt.Printf("  Data   : %dMB · free %dGB\n", s.DataMB, s.FreeGB)
	for _, i := range s.Issues {
		fmt.Printf("  ! %s\n", i)
	}
}

// control starts/stops the native services. action is "start" or "stop" (main
// decomposes "restart" into stop+start).
func control(action string) {
	if runtime.GOOS == "linux" {
		// Ariadne owns only the Qdrant user unit; Ollama on Linux is a SYSTEM
		// service we reuse (like a foreign Qdrant) — leave it to the OS.
		run("systemctl", "--user", action, "ariadne-qdrant")
		fmt.Println(action, "issued (ariadne-qdrant user unit; Ollama is a system service, left alone)")
		return
	}
	home, _ := os.UserHomeDir()
	uid := strconv.Itoa(os.Getuid())
	plist := filepath.Join(home, "Library", "LaunchAgents", qdrantLabel+".plist")
	switch action {
	case "start":
		run("launchctl", "bootstrap", "gui/"+uid, plist)
		run("brew", "services", "start", "ollama")
	case "stop":
		run("launchctl", "bootout", "gui/"+uid+"/"+qdrantLabel)
		run("brew", "services", "stop", "ollama")
	}
	fmt.Println(action, "issued (qdrant agent + ollama brew service)")
}

// --- helpers ---

func httpClient() *http.Client { return &http.Client{Timeout: 3 * time.Second} }

func getOK(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := httpClient().Do(req)
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
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, false
	}
	return m, true
}

// rss returns the summed RSS (MB) of processes whose args contain marker.
func rss(marker string) int64 {
	out, err := exec.CommandContext(context.Background(), "ps", "axo", "rss,args").Output() //nolint:gosec // fixed command
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, marker) || strings.Contains(line, "ariadnectl") {
			continue
		}
		fields := strings.Fields(line)
		if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			total += kb
		}
	}
	return total / 1024
}

func dirSizeMB(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil //nolint:nilerr
		}
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total / (1024 * 1024)
}

func freeGB(path string) int64 {
	// -Pk is POSIX-portable (Linux + macOS/BSD); -g is BSD-only and errors on
	// Linux. Available (KB) is column 4 of the POSIX single-line data row.
	out, err := exec.CommandContext(context.Background(), "df", "-Pk", path).Output() //nolint:gosec // fixed command
	if err != nil {
		return -1
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return -1
	}
	availKB, _ := strconv.ParseInt(f[3], 10, 64)
	return availKB / (1024 * 1024) // KB → GB
}

func run(bin string, args ...string) {
	_ = exec.CommandContext(context.Background(), bin, args...).Run() //nolint:gosec,errcheck // fixed service controls
}

func toInt(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}
func upStr(b bool) string { return pick(b, "up", "DOWN") }
func pick(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}
