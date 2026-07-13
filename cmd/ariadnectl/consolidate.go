package main

import (
	"ariadne/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type diaryPoint struct {
	ID   uint64
	Text string
	Wing string
	TS   int64
}

type consolidatedMemory struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

const consolidatePrompt = "You curate long-term software-project memory. " +
	"Convert the supplied diary entries into only durable, critical memories. " +
	`Return a JSON array and nothing else. Each item must be {"room":"decisions|gotchas|reference","text":"..."}. ` +
	"Keep decisions with their rationale, verified root causes and fixes, durable constraints, and important unfinished risks. " +
	"Drop chronology, routine progress, code/log dumps, repository-derivable details, social chatter, and duplicates. " +
	"Each text must be self-contained, concise, and written in the diary's language. " +
	"Return [] when nothing deserves long-term retention. Never reproduce credentials or secrets."

func consolidateCmd(args []string) int {
	fs := flag.NewFlagSet("consolidate", flag.ContinueOnError)
	before := fs.Duration("before", 24*time.Hour, "only consolidate diary entries older than this age")
	dryRun := fs.Bool("dry-run", false, "show the plan without saving or deleting")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *before < time.Hour {
		fmt.Fprintln(os.Stderr, "consolidate: --before must be at least 1h")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	points, err := loadDiary(ctx, time.Now().Add(-*before).Unix())
	if err != nil {
		fmt.Fprintln(os.Stderr, "consolidate: list diary:", err)
		return 1
	}
	groups := groupDiary(points)
	if len(groups) == 0 {
		fmt.Println("consolidate: no eligible diary entries")
		return 0
	}
	if !*dryRun && backupCmd() != 0 {
		fmt.Fprintln(os.Stderr, "consolidate: backup failed; refusing to modify diary")
		return 1
	}
	st, err := consolidationStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "consolidate: store:", err)
		return 1
	}
	for _, key := range sortedKeys(groups) {
		memories, err := consolidateGroup(ctx, groups[key])
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", key, err)
			continue
		}
		fmt.Printf("%s: %d diary -> %d durable memories\n", key, len(groups[key]), len(memories))
		for _, memory := range memories {
			fmt.Printf("  %s: %s\n", memory.Room, memory.Text)
		}
		if *dryRun {
			continue
		}
		if err := replaceDiaryGroup(ctx, st, groups[key], memories); err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", key, err)
		}
	}
	return 0
}

func consolidationStore() (*store.Store, error) {
	port, err := strconv.Atoi(envOr("ARIADNE_QDRANT_PORT", "6334"))
	if err != nil {
		return nil, fmt.Errorf("bad ARIADNE_QDRANT_PORT: %w", err)
	}
	return store.New(envOr("ARIADNE_QDRANT_HOST", "localhost"), port,
		envOr("ARIADNE_OLLAMA", "http://localhost:11434"),
		envOr("ARIADNE_MODEL", "bge-m3"), collection)
}

func loadDiary(ctx context.Context, cutoff int64) ([]diaryPoint, error) {
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{"must": []any{
			map[string]any{"key": "room", "match": map[string]any{"value": "diary"}},
			map[string]any{"key": "ts", "range": map[string]any{"lte": cutoff}},
		}},
		"limit": 10000, "with_payload": true, "with_vector": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(qdrantREST, "/")+"/collections/"+url.PathEscape(collection)+"/points/scroll",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant HTTP %s", resp.Status)
	}
	var out struct {
		Result struct {
			Points []struct {
				ID      uint64         `json:"id"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	points := make([]diaryPoint, 0, len(out.Result.Points))
	for _, p := range out.Result.Points {
		text, _ := p.Payload["text"].(string)
		wing, _ := p.Payload["wing"].(string)
		ts, _ := p.Payload["ts"].(float64)
		if text != "" && wing != "" && ts > 0 {
			points = append(points, diaryPoint{ID: p.ID, Text: text, Wing: wing, TS: int64(ts)})
		}
	}
	return points, nil
}

func groupDiary(points []diaryPoint) map[string][]diaryPoint {
	out := map[string][]diaryPoint{}
	for _, p := range points {
		day := time.Unix(p.TS, 0).Local().Format("2006-01-02")
		key := p.Wing + "/" + day
		out[key] = append(out[key], p)
	}
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func consolidateGroup(ctx context.Context, points []diaryPoint) ([]consolidatedMemory, error) {
	var input strings.Builder
	for i, p := range points {
		fmt.Fprintf(&input, "DIARY %d:\n%s\n\n", i+1, p.Text)
	}
	payload, _ := json.Marshal(map[string]any{
		"model":    envOr("ARIADNE_SUMMARY_MODEL", "qwen2.5:7b"),
		"messages": []map[string]string{{"role": "system", "content": consolidatePrompt}, {"role": "user", "content": input.String()}},
		"stream":   false, "format": "json", "keep_alive": 0,
		"options": map[string]any{"temperature": 0.1, "num_ctx": 8192},
	})
	base := strings.TrimRight(envOr("ARIADNE_SUMMARY_OLLAMA", envOr("ARIADNE_OLLAMA", "http://localhost:11434")), "/")
	if !localSummaryEndpoint(base) && envOr("ARIADNE_CAPTURE_REMOTE", "0") != "1" {
		return nil, fmt.Errorf("remote summary endpoint blocked; set ARIADNE_CAPTURE_REMOTE=1 to allow")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(out.Message.Content)
	var memories []consolidatedMemory
	if err := json.Unmarshal([]byte(content), &memories); err != nil {
		var wrapped struct {
			Memories []consolidatedMemory `json:"memories"`
		}
		if err2 := json.Unmarshal([]byte(content), &wrapped); err2 != nil {
			return nil, fmt.Errorf("invalid model JSON: %w", err)
		}
		memories = wrapped.Memories
	}
	for i := range memories {
		memories[i].Room = strings.TrimSpace(memories[i].Room)
		memories[i].Text = strings.TrimSpace(memories[i].Text)
		if !validConsolidated(memories[i]) {
			return nil, fmt.Errorf("invalid memory %d", i+1)
		}
	}
	return memories, nil
}

func validConsolidated(m consolidatedMemory) bool {
	return m.Text != "" && (m.Room == "decisions" || m.Room == "gotchas" || m.Room == "reference")
}

func localSummaryEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func replaceDiaryGroup(ctx context.Context, st *store.Store, points []diaryPoint, memories []consolidatedMemory) error {
	wing := points[0].Wing
	latest := points[0].TS
	for _, point := range points[1:] {
		if point.TS > latest {
			latest = point.TS
		}
	}
	ts := strconv.FormatInt(latest, 10)
	for _, memory := range memories {
		if _, err := st.Save(ctx, memory.Text, map[string]string{"wing": wing, "room": memory.Room, "ts": ts}); err != nil {
			return fmt.Errorf("save %s: %w", memory.Room, err)
		}
	}
	for _, point := range points {
		if err := st.DeleteByID(ctx, point.ID); err != nil {
			return fmt.Errorf("delete diary %d: %w", point.ID, err)
		}
	}
	return nil
}
