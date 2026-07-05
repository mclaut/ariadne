package main

import (
	"ariadne/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// hookInput is the JSON Claude Code pipes to lifecycle hooks on stdin.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"` // SessionStart: startup|resume|clear|compact
	Reason         string `json:"reason"` // SessionEnd
}

func readHookInput() (hookInput, bool) {
	var in hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return in, false
	}
	return in, true
}

func newStore() (*store.Store, error) {
	port, _ := strconv.Atoi(env("ARIADNE_QDRANT_PORT", "6334"))
	return store.New(
		env("ARIADNE_QDRANT_HOST", "localhost"), port,
		env("ARIADNE_OLLAMA", "http://localhost:11434"),
		env("ARIADNE_MODEL", "bge-m3"),
		env("ARIADNE_COLLECTION", "ariadne"),
	)
}

// sessionStart injects the project's top memories as additionalContext.
// The wing is the cwd basename; a project with no memories yields NO output —
// sessions outside known projects start completely clean.
func sessionStart() {
	in, ok := readHookInput()
	if !ok || in.CWD == "" || in.Source == "compact" {
		return // after compact the summary already carries the context
	}
	wing := filepath.Base(in.CWD)

	st, err := newStore()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	hits, err := st.Recall(ctx,
		"поточний стан проекту "+wing+": останні рішення, застереження, наступні кроки",
		4, wing)
	if err != nil || len(hits) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🧵 Ariadne auto-recall — памʼять проекту «%s»:\n", wing)
	total := 0
	for _, h := range hits {
		t := oneLine(store.SanitizeUTF8(h.Text), 260)
		if total+len(t) > 1400 {
			break
		}
		fmt.Fprintf(&b, "• [%.2f%s] %s\n", h.Score, room(h.Room), t)
		total += len(t)
	}
	b.WriteString("(глибше: тул mcp__ariadne__memory_recall, параметр wing)")

	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": b.String(),
	}}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func room(r string) string {
	if r == "" {
		return ""
	}
	return " " + r
}

// oneLine flattens whitespace and truncates rune-safely.
func oneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
