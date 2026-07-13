package store

import "testing"

func TestBuildPayloadSkipsEmptyAndParsesTS(t *testing.T) {
	payload := buildPayload("hello", map[string]string{
		"wing":          "ariadne",
		"room":          "",
		"ts":            "123",
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
	if payload["source_tokens"] != int64(456) || payload["memory_tokens"] != int64(78) {
		t.Fatalf("token metadata = %#v/%#v", payload["source_tokens"], payload["memory_tokens"])
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
	if recallFilter("", "") != nil {
		t.Fatal("empty scope should not create a filter")
	}
	if got := recallFilter("ariadne", ""); got == nil || len(got.Must) != 1 {
		t.Fatalf("wing-only filter = %#v", got)
	}
	if got := recallFilter("", "reference"); got == nil || len(got.Must) != 1 {
		t.Fatalf("room-only filter = %#v", got)
	}
	if got := recallFilter("ariadne", "reference"); got == nil || len(got.Must) != 2 {
		t.Fatalf("wing+room filter = %#v", got)
	}
}
