package main

import (
	"ariadne/internal/metrics"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sessionEnd detaches the real work and returns immediately so quitting a
// session never waits for the summarizer.
func sessionEnd() {
	if env("ARIADNE_CAPTURE", "1") == "0" {
		return
	}
	in, ok := readHookInput()
	if !ok || in.TranscriptPath == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	home, _ := os.UserHomeDir()
	_ = os.MkdirAll(filepath.Join(home, ".ariadne", "logs"), 0o755)                  //nolint:gosec // user-owned
	logf, err := os.OpenFile(filepath.Join(home, ".ariadne", "logs", "capture.log"), //nolint:gosec // fixed path under $HOME
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.CommandContext(context.Background(), self, //nolint:gosec // re-exec self
		"capture-run", "-transcript", in.TranscriptPath, "-cwd", in.CWD, "-session", in.SessionID)
	cmd.Stdout, cmd.Stderr = logf, logf
	detachProcess(cmd)
	_ = cmd.Start()
}

// captureRun is the detached worker: transcript → guards → local-LLM summary →
// ONE diary memory. Curated, never raw.
func captureRun(args []string) {
	fs := flag.NewFlagSet("capture-run", flag.ExitOnError)
	transcript := fs.String("transcript", "", "session transcript JSONL")
	cwd := fs.String("cwd", "", "session working directory")
	session := fs.String("session", "", "session id")
	dry := fs.Bool("dry", false, "print the memory instead of saving")
	_ = fs.Parse(args)

	log.SetFlags(0)
	log.SetPrefix(time.Now().Format("2006-01-02 15:04:05") + " ")

	turns, first, last := parseTranscript(*transcript)
	minTurns, _ := strconv.Atoi(env("ARIADNE_CAPTURE_MIN_TURNS", "3"))
	userTurns := 0
	for _, t := range turns {
		if t.role == "user" {
			userTurns++
		}
	}
	if userTurns < minTurns {
		log.Printf("skip %s: %d user turns < %d", short(*session), userTurns, minTurns)
		return
	}

	project := filepath.Base(*cwd)
	branch, commits := gitFacts(*cwd, first, last)

	summaryURL, ok := summaryOllamaURL()
	if !ok {
		log.Printf("skip %s: summary endpoint %q is remote; set ARIADNE_CAPTURE_REMOTE=1 to allow it",
			short(*session), env("ARIADNE_SUMMARY_OLLAMA", env("ARIADNE_OLLAMA", "http://localhost:11434")))
		return
	}
	condensed := condense(turns)
	summary := summarize(condensed, summaryURL)
	if summary == "" {
		log.Printf("FAIL %s: empty summary (model down or not pulled? ollama pull %s)",
			short(*session), env("ARIADNE_SUMMARY_MODEL", "qwen2.5:7b"))
		return
	}
	if strings.Contains(summary, "SKIP") && len(summary) < 40 {
		log.Printf("skip %s: summarizer judged the session not worth keeping", short(*session))
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📅 %s · сесія у %s · %s", first.Format("2006-01-02"), project, duration(first, last))
	if branch != "" {
		fmt.Fprintf(&b, " · гілка %s", branch)
	}
	if len(commits) > 0 {
		fmt.Fprintf(&b, " · коміти: %s", strings.Join(commits, "; "))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(summary))
	text := b.String()

	if *dry {
		fmt.Println("---- DRY RUN (не зберігаю) ----")
		fmt.Println(text)
		return
	}
	st, err := newStore()
	if err != nil {
		log.Printf("FAIL %s: store: %v", short(*session), err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	meta := map[string]string{
		"wing":          project,
		"room":          "diary",
		"source_tokens": strconv.FormatInt(metrics.EstimateTokens(condensed), 10),
		"memory_tokens": strconv.FormatInt(metrics.EstimateTokens(text), 10),
	}
	if t := first; !t.IsZero() {
		meta["ts"] = strconv.FormatInt(t.Unix(), 10) // session start, unix seconds
	} else if !last.IsZero() {
		meta["ts"] = strconv.FormatInt(last.Unix(), 10)
	}
	id, err := st.Save(ctx, text, meta)
	if err != nil {
		log.Printf("FAIL %s: save: %v", short(*session), err)
		return
	}
	log.Printf("captured %s → %s/diary, %d chars, id=%d", short(*session), project, len(text), id)
}

// --- transcript ---

type turn struct {
	role, text string
}

type transcriptLine struct {
	Type      string `json:"type"`
	IsMeta    bool   `json:"isMeta"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// parseTranscript extracts plain user/assistant text turns and the session's
// time bounds. Tool traffic, meta lines and command tags are dropped.
func parseTranscript(path string) (turns []turn, first, last time.Time) {
	f, err := os.Open(path) //nolint:gosec // path comes from the hook payload
	if err != nil {
		return nil, first, last
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024) // transcript lines get huge

	for sc.Scan() {
		var l transcriptLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.IsMeta {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, l.Timestamp); err == nil {
			if first.IsZero() {
				first = ts
			}
			last = ts
		}
		if l.Type != "user" && l.Type != "assistant" {
			continue
		}
		txt := contentText(l.Message.Content)
		if txt == "" || strings.HasPrefix(txt, "<") { // command/caveat/system tags
			continue
		}
		turns = append(turns, turn{role: l.Type, text: txt})
	}
	return turns, first, last
}

// contentText handles both string content and []{type,text} blocks.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// condense builds the summarizer input: every turn clipped, the final
// assistant message kept longer (it usually carries the outcome), the middle
// dropped if the total still overflows.
func condense(turns []turn) string {
	lines := make([]string, 0, len(turns))
	for i, t := range turns {
		limit := 500
		if t.role == "assistant" {
			limit = 400
			if i == len(turns)-1 {
				limit = 2500
			}
		}
		tag := "U"
		if t.role == "assistant" {
			tag = "A"
		}
		lines = append(lines, tag+": "+oneLine(t.text, limit))
	}
	body := strings.Join(lines, "\n")
	const budget = 14000
	if len(body) > budget {
		body = body[:2000] + "\n…(середину сесії пропущено)…\n" + body[len(body)-11500:]
	}
	return body
}

// --- summarizer (local Ollama chat model) ---

const summaryPrompt = "Ти — архіваріус сесій розробки. На вході стенограма сесії (U: користувач, A: асистент). " +
	"Стисни її українською в 4–8 речень: (1) що зроблено і які рішення ухвалено — з ПРИЧИНАМИ; " +
	"(2) що зламалося і як полагоджено; (3) що свідомо відкладено чи лишилось відкритим. " +
	"Пиши щільні факти без води, без заголовків і списків. " +
	"Якщо сесія беззмістовна (привітання, проби), відповідай рівно одним словом: SKIP"

func summaryOllamaURL() (string, bool) {
	u := env("ARIADNE_SUMMARY_OLLAMA", env("ARIADNE_OLLAMA", "http://localhost:11434"))
	u = strings.TrimRight(u, "/")
	if isLocalEndpoint(u) || env("ARIADNE_CAPTURE_REMOTE", "0") == "1" {
		return u, true
	}
	return u, false
}

func isLocalEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func summarize(body, ollamaURL string) string {
	if body == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"model": env("ARIADNE_SUMMARY_MODEL", "qwen2.5:7b"),
		"messages": []map[string]string{
			{"role": "system", "content": summaryPrompt},
			{"role": "user", "content": body},
		},
		"stream": false,
		// unload the ~4.7GB summary model right after summarizing — it's used
		// only here and capture runs detached, so the reload cost is invisible.
		"keep_alive": 0,
		"options":    map[string]any{"temperature": 0.2, "num_ctx": 8192},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return strings.TrimSpace(out.Message.Content)
}

// --- deterministic facts ---

func gitFacts(cwd string, first, last time.Time) (branch string, commits []string) {
	if cwd == "" || first.IsZero() {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", cwd, //nolint:gosec // fixed argv
		"rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "log", "--oneline", //nolint:gosec // fixed argv
		"--since="+first.Format(time.RFC3339), "--until="+last.Add(time.Minute).Format(time.RFC3339)).Output()
	if err != nil {
		return branch, nil
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			commits = append(commits, l)
		}
		if len(commits) == 8 {
			break
		}
	}
	return branch, commits
}

func duration(first, last time.Time) string {
	if first.IsZero() || last.IsZero() {
		return "?"
	}
	d := last.Sub(first).Round(time.Minute)
	if d < time.Minute {
		return "<1хв"
	}
	return strings.ReplaceAll(strings.ReplaceAll(d.String(), "h", "г"), "m0s", "хв")
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
