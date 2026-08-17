package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/metrics"
	"ariadne/internal/secretguard"
	"ariadne/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	ConsolidationDeferredKey  string
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
	"Drop local filesystem paths, process IDs, and terminal, tmux, screen, watcher, or detached-job identifiers; retain the " +
	"verified outcome or unfinished risk without its ephemeral runtime name. " +
	"Write one cohesive subject per memory and never merge unrelated facts. A report may include its scope, method, key " +
	"measurements, caveats, and verification; an incident may include its symptom, cause, fix, and verification. Multiple " +
	"sentences are allowed only when every sentence supports the same reusable subject. Independent actions require separate " +
	"objects; prefer several small memories over one broad memory and never squeeze facts together to reduce object count. " +
	"Each text must name its subject, be self-contained rather than a fragment, contain 80-1200 characters, " +
	"and use the diary's language. Never invent a resolution for an explicitly unknown cause. " +
	"If the diary is trivial or contains no durable content, return an empty memories array; " +
	"never create a memory merely stating that nothing happened. " +
	`Return {"memories":[]} when nothing deserves long-term retention. Never reproduce credentials or secrets.`

const (
	defaultConsolidationTokenBudget = int64(6000)
	defaultEmptyGrace               = 7 * 24 * time.Hour
	consolidateDeferredExitCode     = 3
	consolidationPipelineRevision   = "atomic-v2"
	roomDecisions                   = "decisions"
	roomGotchas                     = "gotchas"
	roomReference                   = "reference"
)

type consolidationOutputError struct{ err error }

func (e *consolidationOutputError) Error() string { return e.err.Error() }
func (e *consolidationOutputError) Unwrap() error { return e.err }

type consolidationDeferredError struct{ err error }

func (e *consolidationDeferredError) Error() string { return e.err.Error() }
func (e *consolidationDeferredError) Unwrap() error { return e.err }

var unstablePathPattern = regexp.MustCompile(
	`(?i)(?:~/(?:\.codex|\.claude)/|/(?:users|home|data|tmp|var|mnt|private|volumes|workspace)/|` +
		`c:\\users\\|(?:^|[[:space:]])outputs/)[^[:space:]<>"']*`,
)

var unstableJobPattern = regexp.MustCompile(
	`(?i)(?:detached[[:space:]]+)?(?:screen|watcher|tmux[[:space:]]+session)[[:space:]]+` + "`?" +
		`[[:alnum:]_.:-]+` + "`?",
)

type consolidationBatch struct {
	key      string
	groupKey string
	points   []diaryPoint
}

type consolidationResult struct {
	batch    consolidationBatch
	memories []consolidatedMemory
}

type deferredConsolidation struct {
	batch consolidationBatch
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
	defer func() { _ = st.Close() }()
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
	req, err := newQdrantRequest(ctx, http.MethodPost,
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
	pipelineKey := consolidationDeferredKey()
	points, skippedDeferred := eligibleDiaryPoints(points, pipelineKey)
	rawGroups := groupDiary(points)
	if len(rawGroups) == 0 {
		fmt.Printf("consolidate: no eligible diary entries (deferred unchanged=%d)\n", skippedDeferred)
		return 0
	}
	batches := buildConsolidationBatches(rawGroups, *maxSourceTokens)
	results := make([]consolidationResult, 0, len(batches))
	deferred := make([]deferredConsolidation, 0)
	failures := int64(0)
	retryableFailures := int64(0)
	deferredFailures := int64(0)
	for i, batch := range batches {
		keepAlive := any("5m")
		if i == len(batches)-1 {
			keepAlive = 0
		}
		memories, err := consolidateGroupWithKeepAlive(ctx, batch.points, keepAlive)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", batch.key, err)
			failures++
			if isDeferredConsolidationError(err) {
				deferredFailures++
				deferred = append(deferred, deferredConsolidation{batch: batch})
			} else {
				retryableFailures++
			}
			continue
		}
		results = append(results, consolidationResult{batch: batch, memories: memories})
	}
	results = coalesceConsolidationResults(results)
	for _, result := range results {
		fmt.Printf("%s: %d diary -> %d durable memories\n",
			result.batch.key, len(result.batch.points), len(result.memories))
		for _, memory := range result.memories {
			fmt.Printf("  %s: %s\n", memory.Room, memory.Text)
		}
	}
	if *dryRun {
		status := "dry_run"
		exitCode := 0
		if failures > 0 {
			status, exitCode = consolidationFailureStatus(retryableFailures, deferredFailures)
		}
		fmt.Printf("consolidate dry-run summary: status=%s batches=%d failures=%d retryable=%d deferred=%d skipped=%d\n",
			status, len(batches), failures, retryableFailures, deferredFailures, skippedDeferred)
		return exitCode
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
	defer func() { _ = st.Close() }()
	totals := consolidationOutcome{}
	now := time.Now()
	for _, item := range deferred {
		if err := markDeferredConsolidation(ctx, st, item.batch.points, now, pipelineKey); err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: persist deferred marker: %v\n", item.batch.key, err)
			retryableFailures++
		}
	}
	for _, result := range results {
		outcome, err := applyConsolidatedGroup(ctx, st, result.batch.points, result.memories, now, *emptyGrace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidate: %s: %v; source diary kept\n", result.batch.key, err)
			failures++
			retryableFailures++
			continue
		}
		totals.Created += outcome.Created
		totals.Archived += outcome.Archived
		totals.Candidates += outcome.Candidates
	}
	status, exitCode := consolidationFailureStatus(retryableFailures, deferredFailures)
	_ = activity.Append(activity.Event{Operation: "consolidate", Status: status, Counters: map[string]int64{
		"batches": int64(len(batches)), "failures": failures, "created": totals.Created,
		"archived": totals.Archived, "empty_candidates": totals.Candidates,
		"retryable_failures": retryableFailures, "deferred_failures": deferredFailures,
		"skipped_deferred": skippedDeferred,
	}})
	fmt.Printf("consolidate summary: created=%d archived=%d empty_candidates=%d failures=%d\n",
		totals.Created, totals.Archived, totals.Candidates, failures)
	return exitCode
}

func consolidationFailureStatus(retryable, deferred int64) (string, int) {
	if retryable > 0 {
		return "partial", 1
	}
	if deferred > 0 {
		return "deferred", consolidateDeferredExitCode
	}
	return "complete", 0
}

func eligibleDiaryPoints(points []diaryPoint, pipelineKey string) ([]diaryPoint, int64) {
	eligible := make([]diaryPoint, 0, len(points))
	skipped := int64(0)
	for _, point := range points {
		if point.ConsolidationDeferredKey == pipelineKey {
			skipped++
			continue
		}
		eligible = append(eligible, point)
	}
	return eligible, skipped
}

func buildConsolidationBatches(rawGroups map[string][]diaryPoint, maxSourceTokens int64) []consolidationBatch {
	total := 0
	for _, points := range rawGroups {
		total += len(points)
	}
	batches := make([]consolidationBatch, 0, total)
	for _, key := range sortedKeys(rawGroups) {
		groupPoints := rawGroups[key]
		for i, point := range groupPoints {
			batchKey := key
			if len(groupPoints) > 1 {
				batchKey += fmt.Sprintf("#%d", i+1)
			}
			for _, part := range splitDiaryByTokenBudget([]diaryPoint{point}, maxSourceTokens) {
				batches = append(batches, consolidationBatch{key: batchKey, groupKey: key, points: part})
			}
		}
	}
	return batches
}

func coalesceConsolidationResults(results []consolidationResult) []consolidationResult {
	coalesced := make([]consolidationResult, 0, len(results))
	byGroup := map[string]int{}
	for _, result := range results {
		if len(result.memories) == 0 {
			coalesced = append(coalesced, result)
			continue
		}
		groupKey := result.batch.groupKey
		if groupKey == "" {
			groupKey = result.batch.key
		}
		index, ok := byGroup[groupKey]
		if !ok {
			result.batch.key = groupKey
			result.batch.groupKey = groupKey
			result.memories = deduplicateConsolidatedMemories(result.memories)
			byGroup[groupKey] = len(coalesced)
			coalesced = append(coalesced, result)
			continue
		}
		coalesced[index].batch.points = append(coalesced[index].batch.points, result.batch.points...)
		coalesced[index].memories = deduplicateConsolidatedMemories(append(
			coalesced[index].memories, result.memories...,
		))
	}
	return coalesced
}

func deduplicateConsolidatedMemories(memories []consolidatedMemory) []consolidatedMemory {
	unique := make([]consolidatedMemory, 0, len(memories))
	for _, candidate := range memories {
		duplicate := -1
		for i, existing := range unique {
			if nearDuplicateMemory(existing.Text, candidate.Text) {
				duplicate = i
				break
			}
		}
		if duplicate < 0 {
			unique = append(unique, candidate)
			continue
		}
		if len([]rune(candidate.Text)) > len([]rune(unique[duplicate].Text)) {
			unique[duplicate] = candidate
		}
	}
	return unique
}

func nearDuplicateMemory(a, b string) bool {
	aTokens := memoryTokenSet(a)
	bTokens := memoryTokenSet(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return false
	}
	aNumbers := numericTokens(aTokens)
	bNumbers := numericTokens(bTokens)
	if len(aNumbers) > 0 && len(bNumbers) > 0 {
		sharedNumbers := 0
		for token := range aNumbers {
			if _, ok := bNumbers[token]; ok {
				sharedNumbers++
			}
		}
		if float64(sharedNumbers)/float64(min(len(aNumbers), len(bNumbers))) < 0.5 {
			return false
		}
	}
	intersection := 0
	for token := range aTokens {
		if _, ok := bTokens[token]; ok {
			intersection++
		}
	}
	minimum := min(len(aTokens), len(bTokens))
	return intersection >= 4 && float64(intersection)/float64(minimum) >= 0.4
}

func numericTokens(tokens map[string]struct{}) map[string]struct{} {
	numbers := map[string]struct{}{}
	for token := range tokens {
		if allDigitToken(token) {
			numbers[token] = struct{}{}
		}
	}
	return numbers
}

func allDigitToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func memoryTokenSet(text string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len([]rune(field)) >= 3 || allDigitToken(field) {
			tokens[field] = struct{}{}
		}
	}
	return tokens
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
	req, err := newQdrantRequest(ctx, http.MethodPost,
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
		consolidationDeferredKey, _ := p.Payload["consolidation_deferred_key"].(string)
		if text != "" && wing != "" && ts > 0 {
			points = append(points, diaryPoint{
				ID: p.ID, Text: text, Wing: wing, TS: int64(ts), OccurredAt: int64(occurredAt),
				SourceTokens: int64(sourceTokens), SourceID: sourceID,
				ConsolidationStatus: consolidationStatus, ConsolidationAttempts: int64(consolidationAttempts),
				ConsolidationFirstEmptyAt: int64(consolidationFirstEmptyAt),
				ConsolidationDeferredKey:  consolidationDeferredKey,
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
	for key := range out {
		sort.Slice(out[key], func(i, j int) bool {
			if out[key][i].TS != out[key][j].TS {
				return out[key][i].TS < out[key][j].TS
			}
			return out[key][i].ID < out[key][j].ID
		})
	}
	return out
}

func consolidationModel() string {
	return envOr("ARIADNE_CONSOLIDATION_MODEL", envOr("ARIADNE_SUMMARY_MODEL", "qwen2.5:7b"))
}

func consolidationJudgeModel() string {
	return envOr("ARIADNE_CONSOLIDATION_JUDGE_MODEL", consolidationModel())
}

func consolidationDeferredKey() string {
	return strings.Join([]string{
		consolidationPipelineRevision,
		consolidationModel(),
		consolidationJudgeModel(),
	}, "|")
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
		fmt.Fprintf(&input, "DIARY %d:\n%s\n\n", i+1, secretguard.Redact(p.Text))
	}
	requestInput := input.String()
	var lastErr error
	guidance := sourceLanguageGuidance(points)
	correction := ""
	for attempt := 0; attempt < 2; attempt++ {
		memories, err := requestConsolidatedMemories(ctx, points, requestInput, keepAlive, guidance, correction)
		if err == nil {
			return memories, nil
		}
		lastErr = err
		var outputErr *consolidationOutputError
		if attempt == 0 && errors.As(err, &outputErr) {
			correction = consolidationRepairGuidance(points, outputErr)
			if strings.Contains(outputErr.Error(), "unstable local artifact") {
				requestInput = redactUnstableArtifacts(requestInput)
			}
			continue
		}
		break
	}
	return nil, lastErr
}

func consolidationRepairGuidance(points []diaryPoint, err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "output language differs from source"):
		source := make([]string, len(points))
		for i, point := range points {
			source[i] = point.Text
		}
		sourceText := strings.Join(source, "\n")
		language := sourceLanguageHint(sourceText)
		if language == "" {
			language, _ = dominantScript(sourceText)
			language += " script source language"
		}
		return "The previous response used the wrong language. Write every memory in " + language +
			" exactly as the source does; do not translate it."
	case strings.Contains(message, "unstable local artifact"):
		return "Remove every local filesystem path and every terminal, tmux, screen, watcher, or detached-job identifier. " +
			"State only the durable verified outcome or unfinished risk; do not copy or rename the ephemeral reference."
	case strings.Contains(message, "credential material"):
		return "Remove every credential value, password, token, private key, credential URI, and secret assignment. " +
			"Retain only the non-sensitive operational conclusion and, when needed, the variable name without its value."
	default:
		return "The previous response failed this validation rule: " + message + ". Correct that rule."
	}
}

func sourceLanguageGuidance(points []diaryPoint) string {
	source := make([]string, len(points))
	for i, point := range points {
		source[i] = point.Text
	}
	if language := sourceLanguageHint(strings.Join(source, "\n")); language != "" {
		return "The source language is " + language + ". Write every memory only in " + language + "; do not translate it."
	}
	return ""
}

func redactUnstableArtifacts(text string) string {
	text = unstablePathPattern.ReplaceAllString(text, " [local artifact omitted]")
	return unstableJobPattern.ReplaceAllString(text, " [ephemeral job omitted]")
}

func requestConsolidatedMemories(
	ctx context.Context, points []diaryPoint, input string, keepAlive any, guidance, correction string,
) ([]consolidatedMemory, error) {
	systemPrompt := consolidatePrompt
	if guidance != "" {
		systemPrompt += " " + guidance
	}
	if correction != "" {
		systemPrompt += " " + correction +
			" Regenerate from the diary; do not mention the rejected response or these repair instructions."
	}
	payload, _ := json.Marshal(map[string]any{
		"model":    consolidationModel(),
		"messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": input}},
		// Consolidation requires schema-valid JSON, not a reasoning trace. Ollama
		// returns thinking separately for capable models; disable it to reduce
		// latency and keep the response contract deterministic.
		"stream": false, "think": false, "keep_alive": keepAlive,
		"format": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"memories"},
			"properties": map[string]any{"memories": map[string]any{
				"type": "array", "maxItems": 12, "items": map[string]any{
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
		return nil, &consolidationDeferredError{err: errors.New(
			"remote summary endpoint blocked; set ARIADNE_CAPTURE_REMOTE=1 to allow")}
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
		err = fmt.Errorf("ollama HTTP %s", resp.Status)
		if resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
			return nil, &consolidationDeferredError{err: err}
		}
		return nil, err
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
		return nil, &consolidationOutputError{err: fmt.Errorf("invalid model JSON: %w", err)}
	}
	memories, err := validateConsolidatedMemories(points, wrapped.Memories)
	if err != nil {
		return nil, err
	}
	if err := validateConsolidatedQuality(ctx, base, keepAlive, memories); err != nil {
		return nil, err
	}
	return memories, nil
}

func validateConsolidatedQuality(
	ctx context.Context, base string, finalKeepAlive any, memories []consolidatedMemory,
) error {
	if len(memories) == 0 {
		return nil
	}
	var candidates strings.Builder
	for i, memory := range memories {
		fmt.Fprintf(&candidates, "CANDIDATE %d [%s]:\n%s\n\n", i+1, memory.Room, memory.Text)
	}
	valid, reason, err := requestConsolidationVerdict(ctx, base, finalKeepAlive,
		"Review the complete candidate set. Each candidate must cover one cohesive reusable subject. A report may include "+
			"its scope, method, key measurements, caveats, and verification. An incident may include its symptom, cause, "+
			"fix, and verification. A release may include related changes and checks. Reject a candidate only when it joins "+
			"facts that are useful independently and neither fact explains or supports the other. Also reject duplicate "+
			"candidates that express the same durable fact. Related but distinct candidates are valid.",
		candidates.String())
	if err != nil {
		return err
	}
	if !valid {
		return &consolidationOutputError{err: fmt.Errorf("candidate set failed quality review: %s", reason)}
	}
	return nil
}

func requestConsolidationVerdict(
	ctx context.Context, base string, keepAlive any, instruction, input string,
) (bool, string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": consolidationJudgeModel(),
		"messages": []map[string]string{
			{"role": "system", "content": "You are a strict durable-memory quality gate. " + instruction +
				" Return one JSON object and nothing else."},
			{"role": "user", "content": input},
		},
		"stream": false, "think": false, "keep_alive": keepAlive,
		"format": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"valid", "reason"},
			"properties": map[string]any{
				"valid":  map[string]any{"type": "boolean"},
				"reason": map[string]any{"type": "string", "maxLength": 400},
			},
		},
		"options": map[string]any{"temperature": 0, "num_ctx": 4096},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		err = fmt.Errorf("ollama quality gate HTTP %s", resp.Status)
		if resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
			return false, "", &consolidationDeferredError{err: err}
		}
		return false, "", err
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, "", err
	}
	var verdict struct {
		Valid  bool   `json:"valid"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.Message.Content)), &verdict); err != nil {
		return false, "", &consolidationDeferredError{err: fmt.Errorf("invalid quality-gate JSON: %w", err)}
	}
	reason := strings.TrimSpace(verdict.Reason)
	if reason == "" {
		reason = "quality gate rejected the candidate"
	}
	return verdict.Valid, reason, nil
}

func isDeferredConsolidationError(err error) bool {
	var outputErr *consolidationOutputError
	var deferredErr *consolidationDeferredError
	return errors.As(err, &outputErr) || errors.As(err, &deferredErr)
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
			return nil, &consolidationOutputError{err: fmt.Errorf(
				"invalid memory %d: contains an unstable local artifact reference", i+1)}
		}
		if findings := secretguard.Findings(memories[i].Text); len(findings) > 0 {
			return nil, &consolidationOutputError{err: fmt.Errorf(
				"invalid memory %d: contains credential material (%s)", i+1, strings.Join(findings, ","))}
		}
		if !sameDominantScript(sourceText, memories[i].Text) {
			return nil, &consolidationOutputError{err: fmt.Errorf(
				"invalid memory %d: output language differs from source", i+1)}
		}
		memories[i].Room = conservativeRoom(memories[i].Room, memories[i].Text)
		if !validConsolidated(memories[i]) {
			return nil, &consolidationOutputError{err: fmt.Errorf("invalid memory %d", i+1)}
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
		"/users/", "/home/", "/data/", "/tmp/", "/var/", "/mnt/", "/private/", "/volumes/", "/workspace/",
		"c:\\users\\", "~/.codex/", "~/.claude/", "outputs/", "detached screen ", "detached watcher ",
		"tmux session ", "[local artifact omitted]", "[ephemeral job omitted]",
	})
}

func sameDominantScript(source, output string) bool {
	sourceScript, sourceCount := dominantScript(source)
	outputScript, outputCount := dominantScript(output)
	if sourceCount < 20 || outputCount < 20 {
		return true
	}
	if sourceScript != outputScript {
		return false
	}
	sourceLanguage := sourceLanguageHint(source)
	outputLanguage := sourceLanguageHint(output)
	return sourceLanguage == "" || outputLanguage == "" || sourceLanguage == outputLanguage
}

func sourceLanguageHint(text string) string {
	ukrainian := strings.Count(text, "і") + strings.Count(text, "І") + strings.Count(text, "ї") +
		strings.Count(text, "Ї") + strings.Count(text, "є") + strings.Count(text, "Є") +
		strings.Count(text, "ґ") + strings.Count(text, "Ґ")
	russian := strings.Count(text, "ы") + strings.Count(text, "Ы") + strings.Count(text, "э") +
		strings.Count(text, "Э") + strings.Count(text, "ъ") + strings.Count(text, "Ъ") +
		strings.Count(text, "ё") + strings.Count(text, "Ё")
	switch {
	case ukrainian >= 2 && ukrainian > russian:
		return "Ukrainian"
	case russian >= 2 && russian > ukrainian:
		return "Russian"
	default:
		return ""
	}
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

func markDeferredConsolidation(
	ctx context.Context, st consolidationWriter, points []diaryPoint, now time.Time, pipelineKey string,
) error {
	if now.IsZero() {
		now = time.Now()
	}
	checkedAt := strconv.FormatInt(now.Unix(), 10)
	for _, point := range points {
		meta := map[string]string{
			"consolidation_attempts":        strconv.FormatInt(point.ConsolidationAttempts+1, 10),
			"consolidation_checked_at":      checkedAt,
			"consolidation_deferred_at":     checkedAt,
			"consolidation_deferred_key":    pipelineKey,
			"consolidation_deferred_reason": "quality_gate",
		}
		if err := st.SetMetaByIDs(ctx, []uint64{point.ID}, meta); err != nil {
			return fmt.Errorf("update source diary %d: %w", point.ID, err)
		}
	}
	return nil
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
