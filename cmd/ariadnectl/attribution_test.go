package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAttributionClass(t *testing.T) {
	cases := []struct {
		room, provenance string
		sourceTokens     int64
		want             string
	}{
		{"diary", "capture", 3389, classAttributed},
		{"decisions", "manual-measured", 120, classAttributed},
		{"diary", "capture", 0, classGap},         // legacy capture without metadata
		{"diary", "", 0, classGap},                // pre-provenance diary
		{"decisions", "manual", 0, classGap},      // explicit save missing source_tokens
		{"reference", "consolidate", 0, classGap}, // consolidation output
		{"memory:MEMORY.md", "import", 0, classSourceless},
		{"general", "", 0, classSourceless}, // legacy chroma import
		{"mybrain", "import", 0, classSourceless},
	}
	for _, c := range cases {
		if got := attributionClass(c.room, c.provenance, c.sourceTokens); got != c.want {
			t.Fatalf("attributionClass(%q, %q, %d) = %q, want %q", c.room, c.provenance, c.sourceTokens, got, c.want)
		}
	}
}

func TestComputeAttributionProfile(t *testing.T) {
	points := []attributionPoint{
		{Room: "diary", Provenance: "capture", SourceTokens: 800, MemoryTokens: 100},
		{Room: "diary", Provenance: "capture", SourceTokens: 160, MemoryTokens: 20, Estimate: "x8-v1"},
		{Room: "diary", MemoryTokens: 50},                           // gap, diary
		{Room: "decisions", Provenance: "manual", Text: "abcdefgh"}, // gap, manual, 2 tokens from text
		{Room: "memory:notes.md", Provenance: "import", MemoryTokens: 300},
		{Room: "reference", Provenance: "consolidate", Status: "archived", MemoryTokens: 30},
	}
	p := computeAttributionProfile(points)
	if p.ScannedPoints != 6 || p.Recallable.Memories != 5 || p.Inactive.Memories != 1 {
		t.Fatalf("profile sizes = scanned %d, recallable %d, inactive %d",
			p.ScannedPoints, p.Recallable.Memories, p.Inactive.Memories)
	}
	if p.Recallable.Attributed.Memories != 2 || p.Recallable.Attributed.Tokens != 120 ||
		p.Recallable.AttributedMeasured.Memories != 1 || p.Recallable.AttributedEstimated.Memories != 1 {
		t.Fatalf("recallable attribution = %+v / measured %+v / estimated %+v",
			p.Recallable.Attributed, p.Recallable.AttributedMeasured, p.Recallable.AttributedEstimated)
	}
	if p.Recallable.AttributableGap.Memories != 2 || p.Recallable.AttributableGap.Tokens != 52 {
		t.Fatalf("gap = %+v", p.Recallable.AttributableGap)
	}
	if p.Recallable.GapDiaryMemories != 1 || p.Recallable.GapConsolidatedMemories != 0 ||
		p.Recallable.GapManualMemories != 1 {
		t.Fatalf("recallable gap split = diary %d / consolidated %d / manual %d",
			p.Recallable.GapDiaryMemories, p.Recallable.GapConsolidatedMemories, p.Recallable.GapManualMemories)
	}
	if p.Recallable.Sourceless.Memories != 1 || p.Recallable.Sourceless.Tokens != 300 {
		t.Fatalf("sourceless = %+v", p.Recallable.Sourceless)
	}
	want := float64(120) * 100 / float64(172)
	if diff := p.Recallable.AttributableCoveragePercent - want; diff > 0.01 || diff < -0.01 {
		t.Fatalf("coverage = %.3f, want %.3f", p.Recallable.AttributableCoveragePercent, want)
	}
	if p.Inactive.GapConsolidatedMemories != 1 || p.Inactive.GapDiaryMemories != 0 {
		t.Fatalf("inactive gap split = diary %d / consolidated %d",
			p.Inactive.GapDiaryMemories, p.Inactive.GapConsolidatedMemories)
	}
}

func TestAttributionStatusRecallable(t *testing.T) {
	for _, status := range []string{"", "active", "future-state"} {
		if !attributionStatusRecallable(status) {
			t.Fatalf("status %q should be recallable", status)
		}
	}
	for _, status := range []string{"archived", "superseded", "orphaned", "quarantined"} {
		if attributionStatusRecallable(status) {
			t.Fatalf("status %q should be excluded", status)
		}
	}
}

func TestAttributionScrollOffsetPreservesUint64Precision(t *testing.T) {
	const pointID = "3584067001156640090"
	var page attributionScrollResponse
	if err := json.Unmarshal([]byte(`{"result":{"next_page_offset":`+pointID+`}}`), &page); err != nil {
		t.Fatal(err)
	}
	next := bytes.TrimSpace(page.Result.NextPageOffset)
	body, err := json.Marshal(map[string]any{"offset": json.RawMessage(next)})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"offset":`+pointID+`}` {
		t.Fatalf("offset lost precision: %s", body)
	}
}

func TestBackfillRejectsConflictingModes(t *testing.T) {
	if got := backfillAttributionCmd([]string{"--apply", "--dry-run"}); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestBackfillSourceTokens(t *testing.T) {
	source, memory := backfillSourceTokens(attributionPoint{MemoryTokens: 405}, 8)
	if source != 3240 || memory != 405 {
		t.Fatalf("stored memory_tokens: got %d/%d", source, memory)
	}
	source, memory = backfillSourceTokens(attributionPoint{Text: "0123456789ab"}, 8)
	if memory != 3 || source != 24 {
		t.Fatalf("estimated from text: got %d/%d", source, memory)
	}
	if source, _ := backfillSourceTokens(attributionPoint{}, 8); source != 0 {
		t.Fatalf("empty point must not claim tokens, got %d", source)
	}
}

func TestEstimateMarker(t *testing.T) {
	if got := estimateMarker(8); got != "x8-v1" {
		t.Fatalf("marker = %q", got)
	}
}
