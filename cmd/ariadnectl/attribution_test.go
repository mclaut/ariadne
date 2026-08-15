package main

import "testing"

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
		{Room: "diary", MemoryTokens: 50},                           // gap, diary
		{Room: "decisions", Provenance: "manual", Text: "abcdefgh"}, // gap, manual, 2 tokens from text
		{Room: "memory:notes.md", Provenance: "import", MemoryTokens: 300},
	}
	p := computeAttributionProfile(points)
	if p.ScannedPoints != 4 {
		t.Fatalf("scanned = %d, want 4", p.ScannedPoints)
	}
	if p.Attributed.Memories != 1 || p.Attributed.Tokens != 100 {
		t.Fatalf("attributed = %+v", p.Attributed)
	}
	if p.AttributableGap.Memories != 2 || p.AttributableGap.Tokens != 52 {
		t.Fatalf("gap = %+v", p.AttributableGap)
	}
	if p.GapDiaryMemories != 1 || p.GapManualMemories != 1 {
		t.Fatalf("gap split = diary %d / manual %d", p.GapDiaryMemories, p.GapManualMemories)
	}
	if p.Sourceless.Memories != 1 || p.Sourceless.Tokens != 300 {
		t.Fatalf("sourceless = %+v", p.Sourceless)
	}
	want := float64(100) * 100 / float64(152)
	if diff := p.AttributableCoveragePercent - want; diff > 0.01 || diff < -0.01 {
		t.Fatalf("coverage = %.3f, want %.3f", p.AttributableCoveragePercent, want)
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
