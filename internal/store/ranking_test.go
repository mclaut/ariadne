package store

import (
	"testing"
	"time"
)

func TestRerankTemporalQueryPrefersRecentComparableHit(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	got := Rerank("what is the latest deployment state?", []Result{
		{ID: 1, Score: 0.500, Text: "old deployment", OccurredAt: now.Add(-180 * 24 * time.Hour).Unix()},
		{ID: 2, Score: 0.495, Text: "current deployment", Provenance: "manual", OccurredAt: now.Add(-time.Hour).Unix()},
	}, 2, now)
	if got[0].ID != 2 {
		t.Fatalf("temporal ranking = %#v", got)
	}
}

func TestRerankDoesNotDecayOldDecisionWithoutTemporalIntent(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	got := Rerank("why do we bind Qdrant to loopback?", []Result{
		{ID: 1, Score: 0.60, Room: "decisions", Text: "old but exact decision", OccurredAt: now.AddDate(-2, 0, 0).Unix()},
		{ID: 2, Score: 0.55, Room: "reference", Text: "newer status", OccurredAt: now.Unix()},
	}, 2, now)
	if got[0].ID != 1 {
		t.Fatalf("non-temporal ranking = %#v", got)
	}
}

func TestRerankMetadataCannotOverrideMaterialSemanticLead(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	got := Rerank("latest decision", []Result{
		{ID: 1, Score: 0.70, Text: "strong semantic hit", OccurredAt: now.AddDate(-1, 0, 0).Unix()},
		{ID: 2, Score: 0.60, Text: "weak recent hit", Provenance: "capture", OccurredAt: now.Unix()},
	}, 2, now)
	if got[0].ID != 1 {
		t.Fatalf("metadata overrode semantic lead: %#v", got)
	}
}

func TestRerankPenalizesOversizedLegacyPayloadAtCloseScores(t *testing.T) {
	got := Rerank("metrics attribution", []Result{
		{ID: 1, Score: 0.500, Text: "legacy", MemoryTokens: 3_000},
		{ID: 2, Score: 0.495, Text: "concise measured result", MemoryTokens: 80, Provenance: "manual-measured"},
	}, 2, time.Now())
	if got[0].ID != 2 {
		t.Fatalf("size/provenance ranking = %#v", got)
	}
}

func TestTemporalQueryRecognizesSupportedLanguages(t *testing.T) {
	for _, query := range []string{
		"останні рішення", "current state", "aktuelle Entscheidung", "última decisión", "décision récente", "ostatnia decyzja",
	} {
		if !temporalQuery(query) {
			t.Fatalf("temporal query not recognized: %q", query)
		}
	}
	if temporalQuery("why is Qdrant local") {
		t.Fatal("non-temporal query classified as temporal")
	}
}

func TestRerankCrossWingPenalizesAndBoundsExternalResults(t *testing.T) {
	got := RerankCrossWing([]Result{
		{ID: 1, Wing: "home", Score: 0.90},
		{ID: 2, Wing: "home", Score: 0.80},
		{ID: 3, Wing: "home", Score: 0.70},
		{ID: 4, Wing: "other-a", Score: 0.95},
		{ID: 5, Wing: "other-b", Score: 0.85},
		{ID: 6, Wing: "other-c", Score: 0.75},
	}, "home", 5)
	if len(got) != 5 {
		t.Fatalf("results = %#v", got)
	}
	remote := 0
	for _, result := range got {
		if result.Wing != "home" {
			remote++
			if result.Score > 0.95*CrossWingWeight+0.0001 {
				t.Fatalf("remote score was not weighted: %#v", result)
			}
		}
	}
	if remote != 2 {
		t.Fatalf("remote result count = %d, results=%#v", remote, got)
	}
}

func TestRerankCrossWingFillsWhenHomeHasNoCandidates(t *testing.T) {
	got := RerankCrossWing([]Result{
		{ID: 1, Wing: "other-a", Score: 0.9},
		{ID: 2, Wing: "other-b", Score: 0.8},
		{ID: 3, Wing: "other-c", Score: 0.7},
	}, "home", 3)
	if len(got) != 3 {
		t.Fatalf("results = %#v", got)
	}
}
