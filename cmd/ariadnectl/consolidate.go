package main

import (
	"ariadne/internal/metrics"
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
	ID           uint64
	Text         string
	Wing         string
	TS           int64
	OccurredAt   int64
	SourceTokens int64
	SourceID     string
}

type consolidatedMemory struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

type consolidationWriter interface {
	Save(context.Context, string, map[string]string) (uint64, error)
	SetMetaByIDs(context.Context, []uint64, map[string]string) error
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
	dryRun := fs.Bool("dry-run", false, "show the plan without saving or changing metadata")
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
		fmt.Fprintln(os.Stderr, "consolidate: backup failed; refusing to modify diary metadata")
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
		if err := saveConsolidatedGroup(ctx, st, groups[key], memories, time.Now()); err != nil {
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
		}, "must_not": []any{
			map[string]any{"key": "status", "match": map[string]any{"value": store.StatusArchived}},
			map[string]any{"key": "consolidation_status", "match": map[string]any{"value": "completed"}},
			map[string]any{"key": "consolidation_status", "match": map[string]any{"value": "empty"}},
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
		occurredAt, _ := p.Payload["occurred_at"].(float64)
		sourceTokens, _ := p.Payload["source_tokens"].(float64)
		sourceID, _ := p.Payload["source_id"].(string)
		if text != "" && wing != "" && ts > 0 {
			points = append(points, diaryPoint{
				ID: p.ID, Text: text, Wing: wing, TS: int64(ts), OccurredAt: int64(occurredAt),
				SourceTokens: int64(sourceTokens), SourceID: sourceID,
			})
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

func saveConsolidatedGroup(
	ctx context.Context, st consolidationWriter, points []diaryPoint, memories []consolidatedMemory, now time.Time,
) error {
	if len(points) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	wing := points[0].Wing
	latest := pointEventTime(points[0])
	sourceTokens := int64(0)
	for _, point := range points[1:] {
		if eventTime := pointEventTime(point); eventTime > latest {
			latest = eventTime
		}
	}
	for _, point := range points {
		sourceTokens += point.SourceTokens
	}
	ts := strconv.FormatInt(latest, 10)
	observedAt := strconv.FormatInt(now.Unix(), 10)
	sourceGroup := consolidationSourceGroup(points)
	shares := distributeSourceTokens(sourceTokens, memories)
	for i, memory := range memories {
		meta := map[string]string{
			"wing":          wing,
			"room":          memory.Room,
			"ts":            ts,
			"observed_at":   observedAt,
			"occurred_at":   ts,
			"memory_tokens": strconv.FormatInt(metrics.EstimateTokens(memory.Text), 10),
			"provenance":    "consolidate",
			"source_id":     sourceGroup + ":" + strconv.Itoa(i+1),
			"status":        store.StatusActive,
			"memory_type":   memoryTypeForRoom(memory.Room),
		}
		if shares[i] > 0 {
			meta["source_tokens"] = strconv.FormatInt(shares[i], 10)
		}
		if _, err := st.Save(ctx, memory.Text, meta); err != nil {
			return fmt.Errorf("save %s: %w", memory.Room, err)
		}
	}
	ids := make([]uint64, len(points))
	for i, point := range points {
		ids[i] = point.ID
	}
	consolidationStatus := "completed"
	if len(memories) == 0 {
		consolidationStatus = "empty"
	}
	if err := st.SetMetaByIDs(ctx, ids, map[string]string{
		"status":               store.StatusArchived,
		"consolidation_status": consolidationStatus,
		"consolidated_at":      observedAt,
	}); err != nil {
		return fmt.Errorf("archive source diary: %w", err)
	}
	return nil
}

func pointEventTime(point diaryPoint) int64 {
	if point.OccurredAt > 0 {
		return point.OccurredAt
	}
	return point.TS
}

func consolidationSourceGroup(points []diaryPoint) string {
	parts := make([]string, len(points))
	for i, point := range points {
		if point.SourceID != "" {
			parts[i] = point.SourceID
		} else {
			parts[i] = strconv.FormatUint(point.ID, 10)
		}
	}
	sort.Strings(parts)
	return metrics.SessionEventID("consolidation-source", strings.Join(parts, "\x00"))
}

func memoryTypeForRoom(room string) string {
	switch room {
	case "decisions":
		return "decision"
	case "gotchas":
		return "gotcha"
	default:
		return "reference"
	}
}

func distributeSourceTokens(sourceTokens int64, memories []consolidatedMemory) []int64 {
	shares := make([]int64, len(memories))
	if sourceTokens <= 0 || len(memories) == 0 {
		return shares
	}
	totalMemoryTokens := int64(0)
	for _, memory := range memories {
		totalMemoryTokens += metrics.EstimateTokens(memory.Text)
	}
	if totalMemoryTokens == 0 {
		return shares
	}
	remaining := sourceTokens
	for i, memory := range memories {
		if i == len(memories)-1 {
			shares[i] = remaining
			break
		}
		shares[i] = sourceTokens * metrics.EstimateTokens(memory.Text) / totalMemoryTokens
		remaining -= shares[i]
	}
	return shares
}
