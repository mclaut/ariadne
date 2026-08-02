package main

import (
	"context"
	"reflect"
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
	f.archived = append([]uint64(nil), ids...)
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

func TestSaveEmptyConsolidationArchivesSourceAsReviewedEmpty(t *testing.T) {
	writer := &fakeConsolidationWriter{}
	now := time.Date(2026, 7, 21, 5, 0, 0, 0, time.UTC)
	if err := saveConsolidatedGroup(context.Background(), writer, []diaryPoint{
		{ID: 10, Wing: "ariadne", TS: now.Add(-48 * time.Hour).Unix()},
	}, nil, now); err != nil {
		t.Fatal(err)
	}
	if len(writer.saved) != 0 || writer.archive["consolidation_status"] != "empty" ||
		writer.archive["status"] != "archived" {
		t.Fatalf("empty consolidation = saved %#v meta %#v", writer.saved, writer.archive)
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
	for _, room := range []string{"decisions", "gotchas", "reference"} {
		if !validConsolidated(consolidatedMemory{Room: room, Text: "durable fact"}) {
			t.Fatalf("room %q should be valid", room)
		}
	}
	if validConsolidated(consolidatedMemory{Room: "diary", Text: "chronology"}) ||
		validConsolidated(consolidatedMemory{Room: "reference"}) {
		t.Fatal("invalid consolidated memory accepted")
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
