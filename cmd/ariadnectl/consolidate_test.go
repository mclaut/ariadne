package main

import (
	"bytes"
	"context"
	"encoding/json"
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
