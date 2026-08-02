package main

import (
	"ariadne/internal/activity"
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
	"unicode"
)

type diaryPoint struct {
	ID                        uint64
	Text                      string
	Wing                      string
	TS                        int64
	OccurredAt                int64
	SourceTokens              int64
	SourceID                  string
	ConsolidationStatus       string
	ConsolidationAttempts     int64
	ConsolidationFirstEmptyAt int64
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
	`Return one JSON object and nothing else: {"memories":[{"room":"decisions|gotchas|reference","text":"..."}]}. ` +
	"Keep decisions with their rationale, verified root causes and fixes, durable constraints, and important unfinished risks. " +
	"Use decisions only for an explicit choice and its rationale; use gotchas for a verified root cause, failure mode, or fix; " +
	"use reference for verified reports, releases, measurements, and current status. " +
	"Drop chronology, routine progress, code/log dumps, repository-derivable details, social chatter, and duplicates. " +
	"Write one concern per memory and never merge unrelated facts. " +
	"Each text must name its subject, be self-contained rather than a fragment, contain 80-1200 characters, " +
	"and use the diary's language. Never invent a resolution for an explicitly unknown cause. " +
	"If the diary is trivial or contains no durable content, return an empty memories array; " +
	"never create a memory merely stating that nothing happened. " +
	`Return {"memories":[]} when nothing deserves long-term retention. Never reproduce credentials or secrets.`

const (
	defaultConsolidationTokenBudget = int64(6000)
	defaultEmptyGrace               = 7 * 24 * time.Hour
	roomDecisions                   = "decisions"
	roomGotchas                     = "gotchas"
	roomReference                   = "reference"
)

type consolidationBatch struct {
	key    string
	points []diaryPoint
}

type consolidationResult struct {
	batch    consolidationBatch
	memories []consolidatedMemory
}

type consolidationOutcome struct {
	Created, Archived, Candidates int64
}

type legacyEmptyPoint struct {
	ID             uint64
	ConsolidatedAt int64
}

// requeueEmptyCmd migrates the old one-pass "empty" lifecycle into the new
// two-pass review policy. Records remain intact; only their searchable status
// changes so the next normal maintenance run can evaluate them again.
func requeueEmptyCmd(args []string) int {
	fs := flag.NewFlagSet("requeue-empty", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "count eligible legacy empty records without changing metadata")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	points, err := loadLegacyEmpty(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "requeue-empty:", err)
		return 1
	}
	fmt.Printf("requeue-empty: %d legacy reviewed-empty records\n", len(points))
	if *dryRun || len(points) == 0 {
		return 0
	}
	st, err := consolidationStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "requeue-empty: store:", err)
		return 1
	}
	now := time.Now().Unix()
	for _, point := range points {
		firstEmptyAt := point.ConsolidatedAt
		if firstEmptyAt <= 0 {
			firstEmptyAt = now
		}
		if err := st.SetMetaByIDs(ctx, []uint64{point.ID}, map[string]string{
			"status":                       store.StatusActive,
			"consolidation_status":         "candidate_empty",
			"consolidation_attempts":       "1",
			"consolidation_first_empty_at": strconv.FormatInt(firstEmptyAt, 10),
			"consolidation_checked_at":     strconv.FormatInt(now, 10),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "requeue-empty: %d: %v\n", point.ID, err)
			return 1
		}
	}
	_ = activity.Append(activity.Event{Operation: "requeue_empty", Status: "complete", Counters: map[string]int64{
		"records": int64(len(points)),
	}})
	fmt.Printf("requeue-empty: %d records active for a confirming pass\n", len(points))
	return 0
}

func loadLegacyEmpty(ctx context.Context) ([]legacyEmptyPoint, error) {
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{"must": []any{
			map[string]any{"key": "room", "match": map[string]any{"value": "diary"}},
			map[string]any{"key": "status", "match": map[string]any{"value": store.StatusArchived}},
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
	points := make([]legacyEmptyPoint, 0, len(out.Result.Points))
	for _, point := range out.Result.Points {
		consolidatedAt, _ := point.Payload["consolidated_at"].(float64)
		points = append(points, legacyEmptyPoint{ID: point.ID, ConsolidatedAt: int64(consolidatedAt)})
	}
	return points, nil
}

func consolidateCmd(args []string) int {
	fs := flag.NewFlagSet("consolidate", flag.ContinueOnError)
	before := fs.Duration("before", 24*time.Hour, "only consolidate diary entries older than this age")
	dryRun := fs.Bool("dry-run", false, "show the plan without saving or changing metadata")
	emptyGrace := fs.Duration("empty-grace", defaultEmptyGrace,
		"minimum delay between first and confirming empty passes")
	maxSourceTokens := fs.Int64("max-source-tokens", defaultConsolidationTokenBudget,
		"maximum estimated diary tokens per model request")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *before < time.Hour {
		fmt.Fprintln(os.Stderr, "consolidate: --before must be at least 1h")
		return 2
	}
	if *emptyGrace < 0 || *maxSourceTokens < 256 {
		fmt.Fprintln(os.Stderr, "consolidate: --empty-grace must be non-negative and --max-source-tokens at least 256")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	points, err := loadDiary(ctx, time.Now().Add(-*before).Unix())
	if err != nil {
		fmt.Fprintln(os.Stderr, "consolidate: list diary:", err)
		return 1
	}
	rawGroups := groupDiary(points)
	if len(rawGroups) == 0 {
		fmt.Println("consolidate: no eligible diary entries")
		return 0
	}
	batches := make([]consolidationBatch, 0, len(rawGroups))
	for _, key := range sortedKeys(rawGroups) {
		parts := splitDiaryByTokenBudget(rawGroups[key], *maxSourceTokens)
		for i, part := range parts {
			batchKey := key
			if len(parts) > 1 {
				batchKey += fmt.Sprintf("#%d", i+1)
			}
			batches = append(batches, consolidationBatch{key: batchKey, points: part})
		}
	}
	results := make([]consolidationResult, 0, len(batches))
	failures := int64(0)
	for i, batch := range batches {
		keepAlive := any("5m")
		if i == len(batches)-1 {
			keepAlive = 0
		}
		memories, err := consolidateGroupWithKeepAlive(ctx, batch.points, keepAlive)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", batch.key, err)
			failures++
			continue
		}
		results = append(results, consolidationResult{batch: batch, memories: memories})
		fmt.Printf("%s: %d diary -> %d durable memories\n", batch.key, len(batch.points), len(memories))
		for _, memory := range memories {
			fmt.Printf("  %s: %s\n", memory.Room, memory.Text)
		}
	}
	if *dryRun {
		status := "dry_run"
		if failures > 0 {
			status = "partial"
		}
		_ = activity.Append(activity.Event{Operation: "consolidate", Status: status, Counters: map[string]int64{
			"batches": int64(len(batches)), "failures": failures,
		}})
		if failures > 0 {
			return 1
		}
		return 0
	}
	needsBackup := false
	for _, result := range results {
		if len(result.memories) > 0 || emptyWouldArchive(result.batch.points, time.Now(), *emptyGrace) {
			needsBackup = true
			break
		}
	}
	if needsBackup {
		interval := durationEnv("ARIADNE_BACKUP_MIN_INTERVAL", 7*24*time.Hour)
		if backupIfDue(time.Now(), interval) != 0 {
			fmt.Fprintln(os.Stderr, "consolidate: due backup failed; refusing lifecycle changes")
			_ = activity.Append(activity.Event{Operation: "consolidate", Status: "failed", Message: "due backup failed"})
			return 1
		}
	}
	st, err := consolidationStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "consolidate: store:", err)
		return 1
	}
	totals := consolidationOutcome{}
	for _, result := range results {
		outcome, err := applyConsolidatedGroup(ctx, st, result.batch.points, result.memories, time.Now(), *emptyGrace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", result.batch.key, err)
			failures++
			continue
		}
		totals.Created += outcome.Created
		totals.Archived += outcome.Archived
		totals.Candidates += outcome.Candidates
	}
	status := "complete"
	if failures > 0 {
		status = "partial"
	}
	_ = activity.Append(activity.Event{Operation: "consolidate", Status: status, Counters: map[string]int64{
		"batches": int64(len(batches)), "failures": failures, "created": totals.Created,
		"archived": totals.Archived, "empty_candidates": totals.Candidates,
	}})
	fmt.Printf("consolidate summary: created=%d archived=%d empty_candidates=%d failures=%d\n",
		totals.Created, totals.Archived, totals.Candidates, failures)
	if failures > 0 {
		return 1
	}
	return 0
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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
			map[string]any{"key": "consolidation_status", "match": map[string]any{"value": "empty_confirmed"}},
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
		consolidationStatus, _ := p.Payload["consolidation_status"].(string)
		consolidationAttempts, _ := p.Payload["consolidation_attempts"].(float64)
		consolidationFirstEmptyAt, _ := p.Payload["consolidation_first_empty_at"].(float64)
		if text != "" && wing != "" && ts > 0 {
			points = append(points, diaryPoint{
				ID: p.ID, Text: text, Wing: wing, TS: int64(ts), OccurredAt: int64(occurredAt),
				SourceTokens: int64(sourceTokens), SourceID: sourceID,
				ConsolidationStatus: consolidationStatus, ConsolidationAttempts: int64(consolidationAttempts),
				ConsolidationFirstEmptyAt: int64(consolidationFirstEmptyAt),
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

func splitDiaryByTokenBudget(points []diaryPoint, budget int64) [][]diaryPoint {
	if len(points) == 0 {
		return nil
	}
	if budget <= 0 {
		budget = defaultConsolidationTokenBudget
	}
	var groups [][]diaryPoint
	current := make([]diaryPoint, 0, len(points))
	currentTokens := int64(0)
	for _, point := range points {
		tokens := metrics.EstimateTokens(point.Text) + 12
		if len(current) > 0 && currentTokens+tokens > budget {
			groups = append(groups, current)
			current = make([]diaryPoint, 0, len(points))
			currentTokens = 0
		}
		current = append(current, point)
		currentTokens += tokens
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func consolidateGroupWithKeepAlive(
	ctx context.Context, points []diaryPoint, keepAlive any,
) ([]consolidatedMemory, error) {
	var input strings.Builder
	if len(points) > 0 {
		fmt.Fprintf(&input, "PROJECT/WING: %s\n\n", points[0].Wing)
	}
	for i, p := range points {
		fmt.Fprintf(&input, "DIARY %d:\n%s\n\n", i+1, p.Text)
	}
	payload, _ := json.Marshal(map[string]any{
		"model":    envOr("ARIADNE_SUMMARY_MODEL", "qwen2.5:7b"),
		"messages": []map[string]string{{"role": "system", "content": consolidatePrompt}, {"role": "user", "content": input.String()}},
		"stream":   false, "keep_alive": keepAlive,
		"format": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"memories"},
			"properties": map[string]any{"memories": map[string]any{
				"type": "array", "maxItems": 6, "items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"room", "text"},
					"properties": map[string]any{
						"room": map[string]any{"type": "string", "enum": []string{roomDecisions, roomGotchas, roomReference}},
						"text": map[string]any{"type": "string", "minLength": 80, "maxLength": 1200},
					},
				},
			}},
		},
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
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama HTTP %s", resp.Status)
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(out.Message.Content)
	var wrapped struct {
		Memories []consolidatedMemory `json:"memories"`
	}
	if err := json.Unmarshal([]byte(content), &wrapped); err != nil {
		return nil, fmt.Errorf("invalid model JSON: %w", err)
	}
	return validateConsolidatedMemories(points, wrapped.Memories)
}

func validateConsolidatedMemories(
	points []diaryPoint, memories []consolidatedMemory,
) ([]consolidatedMemory, error) {
	source := make([]string, len(points))
	for i, point := range points {
		source[i] = point.Text
	}
	sourceText := strings.Join(source, "\n")
	validated := make([]consolidatedMemory, 0, len(memories))
	for i := range memories {
		memories[i].Room = strings.TrimSpace(memories[i].Room)
		memories[i].Text = strings.TrimSpace(memories[i].Text)
		if trivialConsolidatedSummary(memories[i].Text) {
			continue
		}
		if unstableArtifactReference(memories[i].Text) {
			return nil, fmt.Errorf("invalid memory %d: contains an unstable local artifact path", i+1)
		}
		if !sameDominantScript(sourceText, memories[i].Text) {
			return nil, fmt.Errorf("invalid memory %d: output language differs from source", i+1)
		}
		memories[i].Room = conservativeRoom(memories[i].Room, memories[i].Text)
		if !validConsolidated(memories[i]) {
			return nil, fmt.Errorf("invalid memory %d", i+1)
		}
		validated = append(validated, memories[i])
	}
	return validated, nil
}

func conservativeRoom(room, text string) string {
	lower := strings.ToLower(text)
	decisionSignals := []string{
		"decid", "decision", "chose", "chosen", "selected",
		"виріш", "рішен", "обра", "залишено",
		"entschied", "decisione", "decidió", "décid", "decyz",
	}
	gotchaSignals := []string{
		"root cause", "caused", "due to", "failed", "failure", "error", "problem", "fixed", "resolved",
		"blocks", "mismatch", "причин", "помил", "проблем", "виправ", "блоку", "невідповід", "збій",
		"ursache", "fehler", "causa", "errore", "problème", "błąd",
	}
	hasDecision := containsAny(lower, decisionSignals)
	hasGotcha := containsAny(lower, gotchaSignals)
	switch room {
	case roomDecisions:
		if hasGotcha && !hasDecision {
			return roomGotchas
		}
		if !hasDecision {
			return roomReference
		}
	case roomGotchas:
		if !hasGotcha {
			return roomReference
		}
	}
	return room
}

func containsAny(text string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func trivialConsolidatedSummary(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"nothing durable", "no durable content", "no concrete issues", "routine progress only",
		"беззмістов", "нічого важливого", "не вирішувалися конкретні", "немає довготривалої",
	})
}

func unstableArtifactReference(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"/users/", "c:\\users\\", "~/.codex/", "~/.claude/", "outputs/",
	})
}

func sameDominantScript(source, output string) bool {
	sourceScript, sourceCount := dominantScript(source)
	outputScript, outputCount := dominantScript(output)
	if sourceCount < 20 || outputCount < 20 {
		return true
	}
	return sourceScript == outputScript
}

func dominantScript(text string) (string, int) {
	counts := map[string]int{"latin": 0, "cyrillic": 0, "han": 0}
	for _, char := range text {
		switch {
		case unicode.In(char, unicode.Cyrillic):
			counts["cyrillic"]++
		case unicode.In(char, unicode.Han):
			counts["han"]++
		case unicode.In(char, unicode.Latin):
			counts["latin"]++
		}
	}
	name, count := "", 0
	for candidate, candidateCount := range counts {
		if candidateCount > count {
			name, count = candidate, candidateCount
		}
	}
	return name, count
}

func validConsolidated(m consolidatedMemory) bool {
	length := len([]rune(m.Text))
	return length >= 80 && length <= 1200 &&
		(m.Room == roomDecisions || m.Room == roomGotchas || m.Room == roomReference)
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
	_, err := applyConsolidatedGroup(ctx, st, points, memories, now, defaultEmptyGrace)
	return err
}

func applyConsolidatedGroup(
	ctx context.Context,
	st consolidationWriter,
	points []diaryPoint,
	memories []consolidatedMemory,
	now time.Time,
	emptyGrace time.Duration,
) (consolidationOutcome, error) {
	var outcome consolidationOutcome
	if len(points) == 0 {
		return outcome, nil
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
			return outcome, fmt.Errorf("save %s: %w", memory.Room, err)
		}
		outcome.Created++
	}
	for _, point := range points {
		attempts := point.ConsolidationAttempts + 1
		meta := map[string]string{
			"consolidation_attempts":   strconv.FormatInt(attempts, 10),
			"consolidation_checked_at": observedAt,
		}
		if len(memories) > 0 {
			meta["status"] = store.StatusArchived
			meta["consolidation_status"] = "completed"
			meta["consolidated_at"] = observedAt
			outcome.Archived++
		} else {
			firstEmptyAt := point.ConsolidationFirstEmptyAt
			if firstEmptyAt <= 0 {
				firstEmptyAt = now.Unix()
			}
			meta["consolidation_first_empty_at"] = strconv.FormatInt(firstEmptyAt, 10)
			if attempts >= 2 && now.Sub(time.Unix(firstEmptyAt, 0)) >= emptyGrace {
				meta["status"] = store.StatusArchived
				meta["consolidation_status"] = "empty_confirmed"
				meta["consolidated_at"] = observedAt
				outcome.Archived++
			} else {
				meta["status"] = store.StatusActive
				meta["consolidation_status"] = "candidate_empty"
				outcome.Candidates++
			}
		}
		if err := st.SetMetaByIDs(ctx, []uint64{point.ID}, meta); err != nil {
			return outcome, fmt.Errorf("update source diary %d: %w", point.ID, err)
		}
	}
	return outcome, nil
}

func emptyWouldArchive(points []diaryPoint, now time.Time, grace time.Duration) bool {
	for _, point := range points {
		if point.ConsolidationAttempts+1 >= 2 && point.ConsolidationFirstEmptyAt > 0 &&
			now.Sub(time.Unix(point.ConsolidationFirstEmptyAt, 0)) >= grace {
			return true
		}
	}
	return false
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
	case roomDecisions:
		return "decision"
	case roomGotchas:
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
