package main

import (
	"ariadne/internal/metrics"
	"ariadne/internal/store"
	"testing"
)

func TestFormatMoveResultShowsKeptFields(t *testing.T) {
	got := formatMoveResult(42, "ariadne", "")
	want := `moved (id=42 wing="ariadne" room=<kept>)`
	if got != want {
		t.Fatalf("formatMoveResult = %q, want %q", got, want)
	}
}

func TestRecallMetricsEventSeparatesUnknownCost(t *testing.T) {
	hits := []store.Result{
		{ID: 1, Text: "known memory", SourceTokens: 1_000, MemoryTokens: metrics.EstimateTokens("known memory")},
		{ID: 2, Text: "unknown memory"},
	}
	event := recallMetricsEvent(hits, "query=context", "response with wrappers and both memories", "session", "")
	if event.DeliveredTokens != event.AttributedTokens+event.UnattributedTokens {
		t.Fatalf("cost split = %+v", event)
	}
	if event.AttributedTokens == 0 || event.UnattributedTokens == 0 ||
		event.AttributedMemories != 1 || event.UnattributedMemories != 1 {
		t.Fatalf("attribution = %+v", event)
	}
	if len(event.Representations) != 1 || event.Representations[0].Tokens != 1_000 {
		t.Fatalf("representations = %+v", event.Representations)
	}
}

func TestRecallMetricsEventUsesStableSourceLineage(t *testing.T) {
	a := recallMetricsEvent([]store.Result{{
		ID: 1, Text: "first wording", SourceTokens: 100, MemoryTokens: metrics.EstimateTokens("first wording"), SourceID: "source-a",
	}}, "query", "response", "session", "")
	b := recallMetricsEvent([]store.Result{{
		ID: 2, Text: "second wording", SourceTokens: 100, MemoryTokens: metrics.EstimateTokens("second wording"), SourceID: "source-a",
	}}, "query", "response", "session", "")
	if len(a.Representations) != 1 || len(b.Representations) != 1 ||
		a.Representations[0].ID != b.Representations[0].ID {
		t.Fatalf("lineage ids = %+v / %+v", a.Representations, b.Representations)
	}
}

func TestRecallMetricsEventDoesNotCreditArchivedSourceAgain(t *testing.T) {
	event := recallMetricsEvent([]store.Result{{
		ID: 1, Text: "archived source", SourceTokens: 1_000,
		MemoryTokens: metrics.EstimateTokens("archived source"), SourceID: "source-a", Status: store.StatusArchived,
	}}, "history query", "archived response", "session", "")
	if event.AttributedMemories != 0 || event.UnattributedMemories != 1 || len(event.Representations) != 0 ||
		event.AttributedTokens != 0 || event.UnattributedTokens != event.DeliveredTokens {
		t.Fatalf("archived attribution = %+v", event)
	}
}

func TestManualMemoryType(t *testing.T) {
	for room, want := range map[string]string{
		"decisions": "decision", "gotchas": "gotcha", "diary": "event", "reference": "reference", "": "reference",
	} {
		if got := manualMemoryType(room); got != want {
			t.Fatalf("manualMemoryType(%q) = %q, want %q", room, got, want)
		}
	}
}
