package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeConsolidationWriter struct {
	saved    []map[string]string
	archived []uint64
	archive  map[string]string
}

func (f *fakeConsolidationWriter) Save(_ context.Context, _ string, meta map[string]string) (uint64, error) {
	f.saved = append(f.saved, meta)
	return uint64(len(f.saved)), nil
}

func (f *fakeConsolidationWriter) SetMetaByIDs(_ context.Context, ids []uint64, meta map[string]string) error {
	f.archived = append(f.archived, ids...)
	f.archive = meta
	return nil
}

func TestGroupDiaryByWingAndLocalDay(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 7, 10, 10, 0, 0, 0, time.Local).Unix()
	t2 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.Local).Unix()
	got := groupDiary([]diaryPoint{
		{Wing: "api", TS: t1},
		{Wing: "api", TS: t1 + 60},
		{Wing: "api", TS: t2},
		{Wing: "web", TS: t1},
	})
	if len(got) != 3 || len(got["api/2026-07-10"]) != 2 {
		t.Fatalf("unexpected groups: %#v", got)
	}
}

func TestBuildConsolidationBatchesKeepsSessionsAtomic(t *testing.T) {
	t.Parallel()
	groups := map[string][]diaryPoint{
		"api/2026-07-10": {
			{ID: 10, Text: "first session"},
			{ID: 20, Text: "second session"},
		},
	}
	batches := buildConsolidationBatches(groups, defaultConsolidationTokenBudget)
	if len(batches) != 2 || len(batches[0].points) != 1 || len(batches[1].points) != 1 ||
		batches[0].points[0].ID != 10 || batches[1].points[0].ID != 20 {
		t.Fatalf("batches = %#v", batches)
	}
}

func TestEligibleDiaryPointsSkipsOnlyMatchingDeferredPipeline(t *testing.T) {
	t.Parallel()
	points := []diaryPoint{
		{ID: 1, ConsolidationDeferredKey: "atomic-v1|old|old"},
		{ID: 2, ConsolidationDeferredKey: "atomic-v2|current|current"},
		{ID: 3},
	}
	eligible, skipped := eligibleDiaryPoints(points, "atomic-v2|current|current")
	if skipped != 1 || len(eligible) != 2 || eligible[0].ID != 1 || eligible[1].ID != 3 {
		t.Fatalf("eligible=%#v skipped=%d", eligible, skipped)
	}
}

func TestCoalesceConsolidationResultsDeduplicatesWithinWingDay(t *testing.T) {
	t.Parallel()
	results := []consolidationResult{
		{
			batch: consolidationBatch{key: "app/2026-08-03#1", groupKey: "app/2026-08-03", points: []diaryPoint{{ID: 1}}},
			memories: []consolidatedMemory{{
				Room: roomDecisions,
				Text: "Вирішено додати SSH-доступ для відновлення підключення до бази білінгу через систему безпеки.",
			}},
		},
		{
			batch: consolidationBatch{key: "app/2026-08-03#2", groupKey: "app/2026-08-03", points: []diaryPoint{{ID: 2}}},
			memories: []consolidatedMemory{{
				Room: roomGotchas,
				Text: "Доступ до бази білінгу зламався через систему безпеки; додавання SSH-доступу відновило підключення.",
			}},
		},
	}
	got := coalesceConsolidationResults(results)
	if len(got) != 1 || len(got[0].batch.points) != 2 || len(got[0].memories) != 1 {
		t.Fatalf("coalesced = %#v", got)
	}
}

func TestNearDuplicateMemoryKeepsDistinctReportingPeriods(t *testing.T) {
	t.Parallel()
	a := "Verified cash-flow report for July: income 45 million, expenses 48 million, and net flow minus 3 million."
	b := "Verified cash-flow report for the first half: income 404 million, expenses 460 million, and net flow minus 56 million."
	if nearDuplicateMemory(a, b) {
		t.Fatal("reports with different periods and measurements should remain distinct")
	}
}

func TestConsolidationModelOverridesCaptureModel(t *testing.T) {
	t.Setenv("ARIADNE_SUMMARY_MODEL", "capture-small")
	t.Setenv("ARIADNE_CONSOLIDATION_MODEL", "curator-large")
	t.Setenv("ARIADNE_CONSOLIDATION_JUDGE_MODEL", "judge")
	if consolidationModel() != "curator-large" || consolidationJudgeModel() != "judge" ||
		!strings.Contains(consolidationDeferredKey(), "curator-large|judge") {
		t.Fatalf("models=%q/%q key=%q", consolidationModel(), consolidationJudgeModel(), consolidationDeferredKey())
	}
}

func TestDistributeSourceTokensPreservesTotal(t *testing.T) {
	memories := []consolidatedMemory{{Text: "short"}, {Text: "a much longer memory"}, {Text: "last"}}
	shares := distributeSourceTokens(1_003, memories)
	if len(shares) != len(memories) {
		t.Fatalf("shares = %#v", shares)
	}
	total := int64(0)
	for _, share := range shares {
		total += share
	}
	if total != 1_003 || shares[1] <= shares[0] {
		t.Fatalf("shares = %#v, total = %d", shares, total)
	}
}

func TestSaveConsolidatedGroupArchivesSourcesWithoutDeleting(t *testing.T) {
	now := time.Date(2026, 7, 21, 5, 0, 0, 0, time.UTC)
	writer := &fakeConsolidationWriter{}
	points := []diaryPoint{
		{ID: 10, Wing: "ariadne", TS: now.Add(-48 * time.Hour).Unix(), SourceTokens: 600, SourceID: "source-a"},
		{ID: 20, Wing: "ariadne", TS: now.Add(-24 * time.Hour).Unix(), SourceTokens: 400, SourceID: "source-b"},
	}
	memories := []consolidatedMemory{{Room: "decisions", Text: "Keep history append-only because source context matters."}}
	if err := saveConsolidatedGroup(context.Background(), writer, points, memories, now); err != nil {
		t.Fatal(err)
	}
	if len(writer.saved) != 1 || writer.saved[0]["status"] != "active" ||
		writer.saved[0]["source_tokens"] != "1000" || writer.saved[0]["source_id"] == "" {
		t.Fatalf("saved metadata = %#v", writer.saved)
	}
	if !reflect.DeepEqual(writer.archived, []uint64{10, 20}) || writer.archive["status"] != "archived" ||
		writer.archive["consolidation_status"] != "completed" {
		t.Fatalf("archive operation = ids %#v meta %#v", writer.archived, writer.archive)
	}
}

func TestMarkDeferredConsolidationKeepsSourceActive(t *testing.T) {
	writer := &fakeConsolidationWriter{}
	now := time.Date(2026, 8, 3, 5, 0, 0, 0, time.UTC)
	if err := markDeferredConsolidation(context.Background(), writer, []diaryPoint{{
		ID: 10, ConsolidationAttempts: 2,
	}}, now, "atomic-v2|qwen|qwen"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(writer.archived, []uint64{10}) || writer.archive["status"] != "" ||
		writer.archive["consolidation_status"] != "" ||
		writer.archive["consolidation_attempts"] != "3" ||
		writer.archive["consolidation_deferred_key"] != "atomic-v2|qwen|qwen" {
		t.Fatalf("deferred metadata = ids %#v meta %#v", writer.archived, writer.archive)
	}
}

func TestFirstEmptyConsolidationKeepsSourceActiveForConfirmation(t *testing.T) {
	writer := &fakeConsolidationWriter{}
	now := time.Date(2026, 7, 21, 5, 0, 0, 0, time.UTC)
	if err := saveConsolidatedGroup(context.Background(), writer, []diaryPoint{
		{ID: 10, Wing: "ariadne", TS: now.Add(-48 * time.Hour).Unix()},
	}, nil, now); err != nil {
		t.Fatal(err)
	}
	if len(writer.saved) != 0 || writer.archive["consolidation_status"] != "candidate_empty" ||
		writer.archive["status"] != "active" || writer.archive["consolidation_attempts"] != "1" {
		t.Fatalf("empty consolidation = saved %#v meta %#v", writer.saved, writer.archive)
	}
}

func TestSecondEmptyAfterGraceArchivesSource(t *testing.T) {
	writer := &fakeConsolidationWriter{}
	now := time.Date(2026, 7, 21, 5, 0, 0, 0, time.UTC)
	point := diaryPoint{
		ID: 10, Wing: "ariadne", TS: now.Add(-48 * time.Hour).Unix(),
		ConsolidationStatus: "candidate_empty", ConsolidationAttempts: 1,
		ConsolidationFirstEmptyAt: now.Add(-8 * 24 * time.Hour).Unix(),
	}
	outcome, err := applyConsolidatedGroup(context.Background(), writer, []diaryPoint{point}, nil, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Archived != 1 || writer.archive["consolidation_status"] != "empty_confirmed" ||
		writer.archive["status"] != "archived" || writer.archive["consolidation_attempts"] != "2" {
		t.Fatalf("confirmed empty = outcome %#v meta %#v", outcome, writer.archive)
	}
}

func TestSplitDiaryByTokenBudgetNeverDropsEntries(t *testing.T) {
	points := []diaryPoint{
		{ID: 1, Text: strings.Repeat("a", 800)},
		{ID: 2, Text: strings.Repeat("b", 800)},
		{ID: 3, Text: strings.Repeat("c", 800)},
	}
	groups := splitDiaryByTokenBudget(points, 256)
	seen := 0
	for _, group := range groups {
		seen += len(group)
	}
	if len(groups) != 3 || seen != len(points) {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestSplitDiaryByTokenBudgetKeepsEntriesTogetherWithinBudget(t *testing.T) {
	points := []diaryPoint{
		{ID: 1, Text: "short"},
		{ID: 2, Text: "also short"},
	}
	groups := splitDiaryByTokenBudget(points, defaultConsolidationTokenBudget)
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestConsolidationSourceGroupIsOrderIndependent(t *testing.T) {
	a := consolidationSourceGroup([]diaryPoint{{ID: 1, SourceID: "a"}, {ID: 2, SourceID: "b"}})
	b := consolidationSourceGroup([]diaryPoint{{ID: 2, SourceID: "b"}, {ID: 1, SourceID: "a"}})
	if a == "" || a != b {
		t.Fatalf("source groups = %q/%q", a, b)
	}
}

func TestValidConsolidated(t *testing.T) {
	t.Parallel()
	text := "A durable, self-contained project fact includes its subject, verified outcome, and enough context for later recall."
	for _, room := range []string{"decisions", "gotchas", "reference"} {
		if !validConsolidated(consolidatedMemory{Room: room, Text: text}) {
			t.Fatalf("room %q should be valid", room)
		}
	}
	if validConsolidated(consolidatedMemory{Room: "diary", Text: text}) ||
		validConsolidated(consolidatedMemory{Room: "reference"}) ||
		validConsolidated(consolidatedMemory{Room: "reference", Text: "too short"}) {
		t.Fatal("invalid consolidated memory accepted")
	}
}

func TestConsolidationQualityGateRejectsLanguageDriftAndRoutesRooms(t *testing.T) {
	points := []diaryPoint{{
		Text: "The certificate renewal failed because certbot.timer was disabled; enabling the timer resolved the problem.",
	}}
	if _, err := validateConsolidatedMemories(points, []consolidatedMemory{{
		Room: roomDecisions, Text: "系统设置问题导致证书续订失败，必须启用定时器才能恢复自动续订，并确认后续运行正常。",
	}}); err == nil {
		t.Fatal("cross-script output should be rejected")
	}
	got, err := validateConsolidatedMemories(points, []consolidatedMemory{{
		Room: roomDecisions,
		Text: "Certificate renewal failed because certbot.timer was disabled; enabling the timer resolved automatic renewal.",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Room != roomGotchas {
		t.Fatalf("quality-routed memories = %#v", got)
	}
	got, err = validateConsolidatedMemories(
		[]diaryPoint{{Text: "A routine session contained no decisions or durable results."}},
		[]consolidatedMemory{{
			Room: roomReference,
			Text: "The session had no durable content and did not resolve any concrete issue or create reusable knowledge.",
		}},
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("trivial summary = %#v, %v", got, err)
	}
}

func TestConservativeRoomKeepsExplicitDecisionAndRoutesReport(t *testing.T) {
	if got := conservativeRoom(roomDecisions,
		"The team decided to keep Qdrant loopback-only because payloads are plaintext."); got != roomDecisions {
		t.Fatalf("explicit decision routed to %q", got)
	}
	if got := conservativeRoom(roomDecisions,
		"Production release 42 completed and all verification checks passed successfully."); got != roomReference {
		t.Fatalf("release report routed to %q", got)
	}
	if got := conservativeRoom(roomDecisions,
		"MaxSell=0 blocks grid export; the exact cause is unknown and requires an audit."); got != roomGotchas {
		t.Fatalf("unresolved failure routed to %q", got)
	}
}

func TestQualityGateRejectsUnstableLocalArtifactPath(t *testing.T) {
	_, err := validateConsolidatedMemories(
		[]diaryPoint{{Text: "The verified financial analysis completed with all reconciliation checks passing."}},
		[]consolidatedMemory{{
			Room: roomReference,
			Text: "The verified financial analysis completed with all checks passing; workbook is /data/runs/report.xlsx.",
		}},
	)
	if err == nil {
		t.Fatal("unstable local artifact path should be rejected")
	}
}

func TestQualityGateRejectsCredentialMaterial(t *testing.T) {
	_, err := validateConsolidatedMemories(
		[]diaryPoint{{Text: "The service deployment completed successfully after configuration was corrected."}},
		[]consolidatedMemory{{
			Room: roomReference,
			Text: "The service deployment completed successfully, but the generated report included " +
				"API_TOKEN=actual-secret-value and must be rejected.",
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestQualityGateRejectsEphemeralJobIdentifier(t *testing.T) {
	_, err := validateConsolidatedMemories(
		[]diaryPoint{{Text: "The QA run remains active and its final verified report is still pending."}},
		[]consolidatedMemory{{
			Room: roomReference,
			Text: "Detached screen qa-report-2026 runs the pending QA report and will trigger another model comparison.",
		}},
	)
	if err == nil {
		t.Fatal("ephemeral detached job identifier should be rejected")
	}
	redacted := redactUnstableArtifacts("Detached screen `qa-report-2026` runs /tmp/report.json")
	if strings.Contains(redacted, "qa-report-2026") || strings.Contains(redacted, "/tmp/report.json") {
		t.Fatalf("redacted = %q", redacted)
	}
	if !unstableArtifactReference("Verified workbook: [local artifact omitted]; all checks passed.") {
		t.Fatal("redaction placeholder should not become durable memory text")
	}
}

func TestQualityGateDistinguishesUkrainianAndRussian(t *testing.T) {
	ukrainian := "Завершено фінансовий аналіз підприємства: перевірені надходження, витрати й чистий потік за звітний період."
	russian := "Завершён финансовый анализ предприятия: проверены внешние доходы, расходы и чистый денежный поток за отчётный период."
	if sameDominantScript(ukrainian, russian) {
		t.Fatal("Russian output should not match a clearly Ukrainian source")
	}
	if hint := sourceLanguageGuidance([]diaryPoint{{Text: ukrainian}}); !strings.Contains(hint, "Ukrainian") {
		t.Fatalf("language guidance = %q", hint)
	}
}

func TestLocalSummaryEndpoint(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://localhost:11434", "http://127.0.0.1:11434", "http://[::1]:11434"} {
		if !localSummaryEndpoint(raw) {
			t.Fatalf("%q should be local", raw)
		}
	}
	if localSummaryEndpoint("https://ollama.example") {
		t.Fatal("remote endpoint accepted")
	}
}

func TestConsolidationRequestUsesSchemaAndWrappedResponse(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"message":{"content":"{\"memories\":[]}"}}`))
	}))
	defer server.Close()
	t.Setenv("ARIADNE_SUMMARY_OLLAMA", server.URL)
	memories, err := consolidateGroupWithKeepAlive(context.Background(), []diaryPoint{{Text: "Routine progress only."}}, "5m")
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 || request["keep_alive"] != "5m" {
		t.Fatalf("memories=%#v request=%#v", memories, request)
	}
	format, ok := request["format"].(map[string]any)
	if !ok || format["type"] != "object" {
		body, _ := json.Marshal(request["format"])
		t.Fatalf("format is not an object schema: %s", bytes.TrimSpace(body))
	}
}

func TestConsolidationRepairsRejectedModelOutputOnce(t *testing.T) {
	calls := 0
	var secondSystem string
	var secondInput string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.Contains(request.Messages[0]["content"], "durable-memory quality gate") {
			body, _ := json.Marshal(map[string]any{
				"message": map[string]string{"content": `{"valid":true,"reason":"one verified report"}`},
			})
			_, _ = w.Write(body)
			return
		}
		content := `{"memories":[{"room":"reference","text":"The verified report completed successfully ` +
			`and remains at /tmp/report.xlsx for later review."}]}`
		if calls == 2 {
			secondSystem = request.Messages[0]["content"]
			secondInput = request.Messages[1]["content"]
			content = `{"memories":[{"room":"reference","text":"The verified report completed successfully ` +
				`and all reconciliation checks passed without unresolved differences."}]}`
		}
		body, _ := json.Marshal(map[string]any{"message": map[string]string{"content": content}})
		_, _ = w.Write(body)
	}))
	defer server.Close()
	t.Setenv("ARIADNE_SUMMARY_OLLAMA", server.URL)
	memories, err := consolidateGroupWithKeepAlive(context.Background(), []diaryPoint{{
		Text: "The verified report at /tmp/source.xlsx completed successfully and all reconciliation checks passed.",
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(memories) != 1 || !strings.Contains(secondSystem, "Remove every local filesystem") ||
		strings.Contains(secondInput, "/tmp/source.xlsx") {
		t.Fatalf("calls=%d memories=%#v second system=%q second input=%q",
			calls, memories, secondSystem, secondInput)
	}
}

func TestConsolidationRepairsMixedConcernRejectedByQualityGate(t *testing.T) {
	generationCalls := 0
	qualityCalls := 0
	var repairSystem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.Contains(request.Messages[0]["content"], "durable-memory quality gate") {
			qualityCalls++
			valid := qualityCalls > 1
			body, _ := json.Marshal(map[string]any{"message": map[string]string{
				"content": fmt.Sprintf(`{"valid":%t,"reason":"two independent systems"}`, valid),
			}})
			_, _ = w.Write(body)
			return
		}
		generationCalls++
		repairSystem = request.Messages[0]["content"]
		content := `{"memories":[{"room":"reference","text":"The Qdrant owner was normalized successfully, ` +
			`while the tray lifecycle log now records every clean exit reason."}]}`
		if generationCalls == 2 {
			content = `{"memories":[{"room":"reference","text":"The Ariadne Qdrant owner was normalized ` +
				`to one canonical service without changing stored memory."}]}`
		}
		body, _ := json.Marshal(map[string]any{"message": map[string]string{"content": content}})
		_, _ = w.Write(body)
	}))
	defer server.Close()
	t.Setenv("ARIADNE_SUMMARY_OLLAMA", server.URL)
	memories, err := consolidateGroupWithKeepAlive(context.Background(), []diaryPoint{{
		Text: "The Ariadne Qdrant owner was normalized to one service. The tray now records clean exit reasons.",
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if generationCalls != 2 || qualityCalls != 2 || len(memories) != 1 ||
		!strings.Contains(repairSystem, "candidate set failed quality review") {
		t.Fatalf("generation=%d quality=%d memories=%#v repair=%q",
			generationCalls, qualityCalls, memories, repairSystem)
	}
}

func TestConsolidationRepairGuidanceNamesExpectedScript(t *testing.T) {
	err := &consolidationOutputError{err: errors.New("invalid memory 1: output language differs from source")}
	guidance := consolidationRepairGuidance([]diaryPoint{{
		Text: "The release completed successfully and all verification checks passed without errors.",
	}}, err)
	if !strings.Contains(guidance, "latin script") || !strings.Contains(guidance, "do not translate") {
		t.Fatalf("guidance = %q", guidance)
	}
}

func TestConsolidationFailureStatusPrefersRetryableFailures(t *testing.T) {
	if status, code := consolidationFailureStatus(0, 2); status != "deferred" || code != consolidateDeferredExitCode {
		t.Fatalf("deferred status=%q code=%d", status, code)
	}
	if status, code := consolidationFailureStatus(1, 2); status != "partial" || code != 1 {
		t.Fatalf("mixed status=%q code=%d", status, code)
	}
	if status, code := consolidationFailureStatus(0, 0); status != "complete" || code != 0 {
		t.Fatalf("complete status=%q code=%d", status, code)
	}
}
