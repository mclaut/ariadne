package main

// The Ariadne panel is a small local page served by the tray: why the dot has
// its color, service state, recent maintenance activity, log tails and the same
// controls as the menu. It listens on loopback only, checks the Host header
// (DNS rebinding) and requires a per-run random token on every API call. The
// token travels in the URL fragment, so it never reaches the server log or the
// browser's request history; the page sends it back in a header that other
// origins cannot set without a CORS preflight, which the server never grants.

import (
	"ariadne/internal/activity"
	"ariadne/internal/i18n"
	"ariadne/internal/secretguard"
	"ariadne/internal/version"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed panel.html
var panelPage []byte

const (
	panelTokenHeader  = "X-Ariadne-Token" //nolint:gosec // header name, not a credential
	panelActivityRows = 60
	panelLogLines     = 200
	panelLogReadBytes = 256 << 10
)

// panelLogs lists the log files the panel may show; nothing else is readable.
var panelLogs = []string{"maintenance.log", "tray.log", "update.log", "capture.log", "qdrant.err.log"}

type panelServer struct {
	token string
	addr  string
	state func() panelState
	act   func(string) bool
	home  string
}

type panelState struct {
	Version   string            `json:"version"`
	Lang      string            `json:"lang"`
	Color     string            `json:"color"`
	Health    string            `json:"health"`
	Why       string            `json:"why"`
	Issues    []string          `json:"issues"`
	Services  []panelService    `json:"services"`
	Facts     []panelFact       `json:"facts"`
	Activity  []activity.Event  `json:"activity"`
	Busy      bool              `json:"busy"`
	Labels    map[string]string `json:"labels"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type panelService struct {
	Name   string `json:"name"`
	Up     bool   `json:"up"`
	Detail string `json:"detail"`
}

type panelFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

var (
	panelMu  sync.Mutex
	panelURL string
)

// healthColor is the single source of the dot color for the icon and the panel.
func healthColor(s status) string {
	switch {
	case !s.reachable:
		return "gray"
	case !s.Qdrant.Up || !s.Ollama.Up:
		return "red"
	case len(s.Issues) > 0:
		return "orange"
	default:
		return "green"
	}
}

func colorExplanation(l i18n.Lang, color string) string {
	return i18n.T(l, "panel.color."+color)
}

func newPanelState(s status, l i18n.Lang, busy bool, events []activity.Event) panelState {
	color := healthColor(s)
	issues := append([]string(nil), s.Issues...)
	if color == "red" && len(issues) == 0 {
		issues = append(issues, i18n.T(l, "health.down"))
	}
	state := panelState{
		Version: "Ariadne " + version.Tag, Lang: string(l), Color: color,
		Health: healthWord(l, color), Why: colorExplanation(l, color), Issues: issues,
		Services: []panelService{
			{Name: "Qdrant", Up: s.Qdrant.Up, Detail: serviceDetail(s.Qdrant)},
			{Name: "Ollama", Up: s.Ollama.Up, Detail: serviceDetail(s.Ollama)},
		},
		Facts: []panelFact{
			{Label: i18n.T(l, "row.records"), Value: fmt.Sprintf("%s (%s)", grouped(s.Collection.Points), s.Collection.Status)},
			{Label: i18n.T(l, "row.data"), Value: fmt.Sprintf("qdrant %d MB · backups %d MB · logs %d MB · %s %d GB",
				s.DataMB, s.BackupsMB, s.LogsMB, i18n.T(l, "row.free"), s.FreeGB)},
		},
		Activity: events, Busy: busy, Labels: panelLabels(l), UpdatedAt: time.Now(),
	}
	return state
}

func healthWord(l i18n.Lang, color string) string {
	switch color {
	case "gray":
		return i18n.T(l, "health.unreachable")
	case "red":
		return i18n.T(l, "health.down")
	case "orange":
		return i18n.T(l, "health.warn")
	default:
		return i18n.T(l, "health.ok")
	}
}

func serviceDetail(s svc) string {
	if !s.Up {
		return "—"
	}
	parts := []string{}
	if s.Version != "" {
		parts = append(parts, s.Version)
	}
	if s.PID > 0 {
		parts = append(parts, fmt.Sprintf("PID %d", s.PID))
	}
	if s.RSSMB > 0 {
		parts = append(parts, fmt.Sprintf("%d MB", s.RSSMB))
	}
	return strings.Join(parts, " · ")
}

func panelLabels(l i18n.Lang) map[string]string {
	labels := map[string]string{}
	for _, key := range []string{
		"panel.title", "panel.services", "panel.activity", "panel.logs", "panel.controls",
		"panel.updated", "panel.sent", "panel.busy", "panel.offline",
		"menu.start", "menu.stop", "menu.restart", "menu.maintenance", "menu.backup", "menu.logs", "menu.data",
	} {
		labels[key] = i18n.T(l, key)
	}
	return labels
}

// recentActivity returns the newest events first, bounded to the last rows.
func recentActivity(path string, limit int) []activity.Event {
	data, err := readTail(path, panelLogReadBytes)
	if err != nil {
		return nil
	}
	events := make([]activity.Event, 0, limit)
	scanner := bufio.NewScanner(strings.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var event activity.Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Operation != "" {
			event.Message = secretguard.Redact(event.Message)
			events = append(events, event)
		}
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

// readTail reads at most max bytes from the end of a file, starting at a line.
func readTail(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path) //nolint:gosec // fixed runtime file chosen from an allowlist
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := int64(0)
	if info.Size() > maxBytes {
		offset = info.Size() - maxBytes
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return "", err
	}
	text := string(data)
	if offset > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return text, nil
}

func tailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func startPanel(home string, state func() panelState, act func(string) bool) (*panelServer, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		_ = listener.Close()
		return nil, err
	}
	p := &panelServer{token: hex.EncodeToString(raw), addr: listener.Addr().String(), state: state, act: act, home: home}
	server := &http.Server{Handler: p.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("panel server stopped: %v", serveErr)
		}
	}()
	panelMu.Lock()
	panelURL = p.url()
	panelMu.Unlock()
	if err = p.writeHandle(); err != nil {
		log.Printf("panel handle not written: %v", err)
	}
	return p, nil
}

func (p *panelServer) url() string { return "http://" + p.addr + "/#t=" + p.token }

// writeHandle records the panel URL for the launcher (Ariadne.app double-click).
func (p *panelServer) writeHandle() error {
	path := filepath.Join(p.home, ".ariadne", "state", "panel.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"url": p.url(), "pid": os.Getpid()})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p *panelServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", p.page)
	mux.HandleFunc("GET /api/state", p.authorized(p.handleState))
	mux.HandleFunc("GET /api/log", p.authorized(p.handleLog))
	mux.HandleFunc("POST /api/action", p.authorized(p.handleAction))
	return p.sameHost(mux)
}

// sameHost rejects requests addressed to any other host name, which blocks
// DNS-rebinding pages from reaching the loopback listener.
func (p *panelServer) sameHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != p.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (p *panelServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(panelTokenHeader)), []byte(p.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (p *panelServer) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	_, _ = w.Write(panelPage)
}

func (p *panelServer) handleState(w http.ResponseWriter, _ *http.Request) {
	writePanelJSON(w, p.state())
}

func (p *panelServer) handleLog(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	allowed := false
	for _, candidate := range panelLogs {
		allowed = allowed || candidate == name
	}
	if !allowed {
		http.Error(w, "unknown log", http.StatusNotFound)
		return
	}
	text, err := readTail(filepath.Join(p.home, ".ariadne", "logs", name), panelLogReadBytes)
	if err != nil {
		text = ""
	}
	writePanelJSON(w, map[string]string{"name": name, "text": secretguard.Redact(tailLines(text, panelLogLines))})
}

func (p *panelServer) handleAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !p.act(body.Action) {
		http.Error(w, "unknown or busy", http.StatusConflict)
		return
	}
	writePanelJSON(w, map[string]bool{"accepted": true})
}

func writePanelJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func openPanel() {
	panelMu.Lock()
	url := panelURL
	panelMu.Unlock()
	if url != "" {
		openPath(url)
	}
}

var dotColors = map[string]color.RGBA{"gray": gray, "red": red, "orange": orange, "green": green}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// healthReasons lists what made the dot leave green, in the log's words.
func healthReasons(s status) []string {
	if !s.reachable {
		return []string{"ariadnectl status unreachable"}
	}
	reasons := []string{}
	if !s.Qdrant.Up {
		reasons = append(reasons, "Qdrant down")
	}
	if !s.Ollama.Up {
		reasons = append(reasons, "Ollama down")
	}
	return append(reasons, s.Issues...)
}

func panelSnapshot() panelState {
	mu.Lock()
	s, l := lastStatus, lang
	busy := serviceActionRunning || maintenanceRunning || updates.installing
	mu.Unlock()
	return newPanelState(s, l, busy, recentActivity(activity.Path(), panelActivityRows))
}

// panelAction runs a panel button exactly like the matching menu item.
func panelAction(action string) bool {
	mu.Lock()
	busy := serviceActionRunning || maintenanceRunning || updates.installing
	l := lang
	mu.Unlock()
	switch action {
	case "logs":
		openPath(runtimeDir("logs"))
		return true
	case "data":
		openPath(runtimeDir("backups"))
		return true
	}
	if busy {
		return false
	}
	switch action {
	case "start":
		go serviceActionClicked(serviceStart)
	case "stop":
		go serviceActionClicked(serviceStop)
	case "restart":
		go serviceActionClicked(serviceRestart)
	case "maintenance":
		go maintenanceClicked()
	case "backup":
		go func() { _ = ctl("backup", i18n.T(l, "notify.backup")) }()
	default:
		return false
	}
	log.Printf("panel action: %s", action)
	return true
}
