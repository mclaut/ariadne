// Command ariadnectl is the control/health core for the ariadne stack.
// The tray monitor is a thin viewer that shells these subcommands; all
// logic lives here in Go.
//
//	status [-json]   health of Qdrant + Ollama + the collection, and any issues.
//	metrics [-json]  local estimates of context delivered and reused.
//	start | stop | restart   manage the native services (Qdrant LaunchAgent,
//	                         Ollama brew service).
package main

import (
	"ariadne/internal/i18n"
	"ariadne/internal/metrics"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	qdrantData = ".ariadne/qdrant-data" // runtime home is ~/.ariadne (outside any TCC-protected folder)
	diskWarnMB = 2048                   // warn if the machine's free space drops under this
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
	TS           string          `json:"ts"`
	OK           bool            `json:"ok"`
	Qdrant       svc             `json:"qdrant"`
	Ollama       svc             `json:"ollama"`
	Collection   coll            `json:"collection"`
	TokenMetrics metrics.Summary `json:"token_metrics"`
	DataMB       int64           `json:"data_mb"`
	FreeGB       int64           `json:"free_gb"`
	Issues       []string        `json:"issues"`
}

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "status":
		printStatus(hasFlag("-json"))
	case "metrics":
		printMetrics(hasFlag("-json"))
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
		fmt.Fprintln(os.Stderr, "usage: ariadnectl {status [-json] | metrics [-json] | "+
			"start | stop | restart | backup | restore <file> | export [file]}")
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
	metricsCtx, metricsCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	s.TokenMetrics, _ = metrics.Read(metricsCtx)
	metricsCancel()

	// issues (localized — the tray surfaces these verbatim)
	lang := i18n.Current()
	if !s.Qdrant.Up {
		s.Issues = append(s.Issues, i18n.T(lang, "issue.qdrant_down"))
	}
	if !s.Ollama.Up {
		s.Issues = append(s.Issues, i18n.T(lang, "issue.ollama_down"))
	}
	if s.Qdrant.Up && s.Collection.Status != "" && s.Collection.Status != "green" {
		s.Issues = append(s.Issues, fmt.Sprintf(i18n.T(lang, "issue.coll_status"), s.Collection.Status))
	}
	// NB: an empty collection is NOT an issue — a fresh install legitimately has
	// 0 memories until the user saves some; flagging it made the tray cry orange.
	if s.FreeGB*1024 < diskWarnMB {
		s.Issues = append(s.Issues, fmt.Sprintf(i18n.T(lang, "issue.low_disk"), s.FreeGB))
	}
	s.OK = len(s.Issues) == 0
	return s
}

func printMetrics(asJSON bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := metrics.Read(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metrics:", err)
		return
	}
	if asJSON {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
		return
	}
	fmt.Printf("Estimated token reuse (%s)\n", s.Estimator)
	printMetricWindow("All time", s.AllTime)
	printMetricWindow("Last 30 days", s.Last30Days)
}

func printMetricWindow(label string, t metrics.Totals) {
	fmt.Printf("  %-12s ~%d net avoided (%d represented - %d delivered), %d recalls / %d memories\n",
		label+":", t.NetAvoidedTokens, t.RepresentedTokens, t.DeliveredTokens, t.Recalls, t.Memories)
}

func printStatus(asJSON bool) {
	s := gather()
	if asJSON {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
		return
	}
	lang := i18n.Current()
	if s.OK {
		fmt.Println(i18n.T(lang, "status.ok"))
	} else {
		fmt.Println(i18n.T(lang, "status.issues"))
	}
	fmt.Printf("  Qdrant : %s  (%dMB RSS)\n", upStr(lang, s.Qdrant.Up), s.Qdrant.RSSMB)
	fmt.Printf("  Ollama : %s  %s  (%dMB RSS)\n", upStr(lang, s.Ollama.Up), s.Ollama.Version, s.Ollama.RSSMB)
	fmt.Printf("  %s : %d  (%s)\n", i18n.T(lang, "row.records"), s.Collection.Points, s.Collection.Status)
	fmt.Printf("  %s : %dMB · %s %dGB\n", i18n.T(lang, "row.data"), s.DataMB, i18n.T(lang, "row.free"), s.FreeGB)
	for _, i := range s.Issues {
		fmt.Printf("  ! %s\n", i)
	}
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

func toInt(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func upStr(lang i18n.Lang, b bool) string {
	if b {
		return i18n.T(lang, "status.up")
	}
	return i18n.T(lang, "status.down")
}
