package store

import "testing"

func TestBuildPayloadSkipsEmptyAndParsesTS(t *testing.T) {
	payload := buildPayload("hello", map[string]string{
		"wing":          "ariadne",
		"room":          "",
		"ts":            "123",
		"observed_at":   "124",
		"source_tokens": "456",
		"memory_tokens": "78",
	})
	if payload["text"] != "hello" {
		t.Fatalf("text = %v", payload["text"])
	}
	if payload["wing"] != "ariadne" {
		t.Fatalf("wing = %v", payload["wing"])
	}
	if _, ok := payload["room"]; ok {
		t.Fatal("empty room should be omitted")
	}
	if payload["ts"] != int64(123) {
		t.Fatalf("ts = %#v", payload["ts"])
	}
	if payload["observed_at"] != int64(124) {
		t.Fatalf("observed_at = %#v", payload["observed_at"])
	}
	if payload["source_tokens"] != int64(456) || payload["memory_tokens"] != int64(78) {
		t.Fatalf("token metadata = %#v/%#v", payload["source_tokens"], payload["memory_tokens"])
	}
}

func TestPreserveSourceMetadataKeepsMeasuredProvenance(t *testing.T) {
	got := preserveSourceMetadata(map[string]string{"wing": "ariadne", "provenance": "import"}, sourceMeta{
		SourceTokens: 456, MemoryTokens: 78, Provenance: "capture", SourceID: "source-a",
		Status: "active", MemoryType: "event", ObservedAt: 123, OccurredAt: 120,
	})
	if got["source_tokens"] != "456" || got["memory_tokens"] != "78" || got["provenance"] != "capture" ||
		got["source_id"] != "source-a" || got["status"] != "active" || got["memory_type"] != "event" ||
		got["observed_at"] != "123" || got["occurred_at"] != "120" {
		t.Fatalf("preserved metadata = %#v", got)
	}

	override := preserveSourceMetadata(map[string]string{
		"source_tokens": "900", "memory_tokens": "90", "provenance": "manual-measured",
	}, sourceMeta{SourceTokens: 456, MemoryTokens: 78, Provenance: "capture"})
	if override["source_tokens"] != "900" || override["provenance"] != "manual-measured" {
		t.Fatalf("override metadata = %#v", override)
	}
}

func TestContentIDIsStable(t *testing.T) {
	a := contentID("same text")
	if a != contentID("same text") {
		t.Fatal("same text produced different ids")
	}
	if a == contentID("different text") {
		t.Fatal("different text produced the same id")
	}
}

func TestTokenizeIsUnicodeAware(t *testing.T) {
	got := tokenize("Hello, пам'ять 42!")
	want := []string{"hello", "пам", "ять", "42"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSparseVecDropsSingleRuneTokens(t *testing.T) {
	idx, val := sparseVec("a bb bb c")
	if len(idx) != 1 || len(val) != 1 {
		t.Fatalf("sparse length = %d/%d", len(idx), len(val))
	}
	if val[0] != 2 {
		t.Fatalf("term frequency = %v, want 2", val[0])
	}
}

func TestRecallFilterScopesWingAndRoom(t *testing.T) {
	if got := recallFilter("", "", false); got == nil || len(got.Must) != 0 || len(got.MustNot) != 3 {
		t.Fatalf("active-only filter = %#v", got)
	}
	if recallFilter("", "", true) != nil {
		t.Fatal("unscoped history recall should not create a filter")
	}
	if got := recallFilter("ariadne", "", false); got == nil || len(got.Must) != 1 || len(got.MustNot) != 3 {
		t.Fatalf("wing-only filter = %#v", got)
	}
	if got := recallFilter("", "reference", false); got == nil || len(got.Must) != 1 {
		t.Fatalf("room-only filter = %#v", got)
	}
	if got := recallFilter("ariadne", "reference", false); got == nil || len(got.Must) != 2 {
		t.Fatalf("wing+room filter = %#v", got)
	}
	if got := recallFilter("ariadne", "reference", true); got == nil || len(got.MustNot) != 0 {
		t.Fatalf("history filter = %#v", got)
	}
}
