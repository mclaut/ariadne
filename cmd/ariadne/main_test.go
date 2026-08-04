package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/metrics"
	"ariadne/internal/store"
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestSemanticRecallScopeIsDefaultDeny(t *testing.T) {
	if _, err := semanticRecallScope("", false); err == nil {
		t.Fatal("unscoped semantic recall was accepted")
	}
	if got, err := semanticRecallScope(" ariadne ", false); err != nil || got != "ariadne" {
		t.Fatalf("scoped recall = %q, %v", got, err)
	}
	if _, err := semanticRecallScope("", true); err == nil {
		t.Fatal("all-wings recall without a home wing was accepted")
	}
	if got, err := semanticRecallScope("ariadne", true); err != nil || got != "" {
		t.Fatalf("approved all-wings scope = %q, %v", got, err)
	}
}

func TestFormatMoveResultShowsKeptFields(t *testing.T) {
	got := formatMoveResult(42, 84, "ariadne", "")
	want := `moved (source_id=42 new_id=84 wing="ariadne" room=<kept>)`
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

func TestCrossWingApprovalHelperRequiresTrayDecision(t *testing.T) {
	manager := approval.New(t.TempDir())
	result := requireCrossWingApproval(
		manager, "session", "ariadne", "query", "compare patterns", "", "", "",
	)
	if result == nil || !result.IsError {
		t.Fatalf("initial result = %#v", result)
	}
	pending, err := manager.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %#v err=%v", pending, err)
	}
	if _, err := manager.Decide(pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if result := requireCrossWingApproval(manager, "session", "ariadne", "new query", "same task", "", "", pending[0].ID); result != nil {
		t.Fatalf("approved result = %#v", result)
	}
	result = requireCrossWingApproval(
		manager, "session", "ariadne", "query", "purpose", "sessions", "", pending[0].ID,
	)
	if result == nil || !result.IsError {
		t.Fatalf("cross-collection result = %#v", result)
	}
}

func TestCredentialAccessHandlerConsumesOneTimeApproval(t *testing.T) {
	manager := approval.New(t.TempDir())
	handler := credentialAccessHandler(manager, "session")
	arguments := map[string]any{
		"source_wing": "service-a", "target_wing": "service-b",
		"resource": "deployment credential file", "purpose": "one deployment",
	}
	result, err := handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: arguments}})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("initial result = %#v err=%v", result, err)
	}
	pending, err := manager.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %#v err=%v", pending, err)
	}
	if _, err := manager.Decide(pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	arguments["approval_id"] = pending[0].ID
	result, err = handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: arguments}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("approved result = %#v err=%v", result, err)
	}
	result, err = handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: arguments}})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("reused result = %#v err=%v", result, err)
	}
}
