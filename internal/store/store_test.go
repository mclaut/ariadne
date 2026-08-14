package store

import (
	"context"
	"strings"
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

func TestMissingPayloadIndexesSkipsExistingFields(t *testing.T) {
	desired := []payloadIndex{
		{field: "wing", fieldType: qdrant.FieldType_FieldTypeKeyword},
		{field: "ts", fieldType: qdrant.FieldType_FieldTypeInteger},
		{field: "status", fieldType: qdrant.FieldType_FieldTypeKeyword},
	}
	existing := map[string]*qdrant.PayloadSchemaInfo{
		"wing": {DataType: qdrant.PayloadSchemaType_Keyword},
		"ts":   {DataType: qdrant.PayloadSchemaType_Integer},
	}
	got := missingPayloadIndexes(desired, existing)
	if len(got) != 1 || got[0].field != "status" {
		t.Fatalf("missing indexes = %#v", got)
	}
}

func TestQdrantClientUsesSingleConnection(t *testing.T) {
	t.Parallel()
	config := qdrantClientConfig("localhost", 6334, "", false)
	if config.PoolSize != 1 {
		t.Fatalf("Qdrant pool size = %d, want 1", config.PoolSize)
	}
}

func TestDesiredPayloadIndexesAreUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for _, index := range desiredPayloadIndexes() {
		if _, exists := seen[index.field]; exists {
			t.Fatalf("duplicate payload index %q", index.field)
		}
		seen[index.field] = struct{}{}
	}
	if len(seen) < 30 {
		t.Fatalf("payload index plan unexpectedly small: %d", len(seen))
	}
}

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

func TestScopedContentIDIsStableWithinScopeAndDistinctAcrossScopes(t *testing.T) {
	a := scopedContentID("same text", "project-a", "reference")
	if a != scopedContentID("same text", "project-a", "reference") {
		t.Fatal("same scoped content produced different ids")
	}
	if a == scopedContentID("same text", "project-b", "reference") ||
		a == scopedContentID("same text", "project-a", "decisions") {
		t.Fatal("different scopes collapsed to one id")
	}
	hash := contentHash("same text")
	if hash == "" || hash == contentHash("different text") {
		t.Fatal("content hash is not stable and content-sensitive")
	}
}

func TestPreserveSourceMetadataKeepsOriginalTimestamps(t *testing.T) {
	got := preserveSourceMetadata(map[string]string{
		"ts": "999", "observed_at": "999", "occurred_at": "999", "last_seen_at": "1000",
	}, sourceMeta{TS: 100, ObservedAt: 110, OccurredAt: 90})
	if got["ts"] != "100" || got["observed_at"] != "110" || got["occurred_at"] != "90" ||
		got["last_seen_at"] != "1000" {
		t.Fatalf("timestamps = %#v", got)
	}
}

func TestLegacyRoomAliasAndTimestampsSupportMemfileMigration(t *testing.T) {
	legacy := sourceMeta{Wing: "project", Room: "memory:design.md", TS: 100, ObservedAt: 110, OccurredAt: 90}
	meta := map[string]string{
		"wing": "project", "room": "memory:notes/design.md", "_legacy_room": "memory:design.md",
	}
	if !sameScope(legacy, meta) {
		t.Fatal("legacy basename room should match the migration alias")
	}
	got := preserveLegacyTimestamps(map[string]string{"observed_at": "999"}, legacy)
	if got["ts"] != "100" || got["observed_at"] != "110" || got["occurred_at"] != "90" {
		t.Fatalf("legacy timestamps = %#v", got)
	}
	payload := buildPayload("text", meta)
	if _, exists := payload["_legacy_room"]; exists {
		t.Fatal("internal migration alias leaked into payload")
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
	if got := recallFilter("", "", false); got == nil || len(got.Must) != 0 || len(got.MustNot) != 4 {
		t.Fatalf("active-only filter = %#v", got)
	}
	if got := recallFilter("", "", true); got == nil || len(got.MustNot) != 1 {
		t.Fatalf("history recall must retain quarantine filter: %#v", got)
	}
	if got := recallFilter("ariadne", "", false); got == nil || len(got.Must) != 1 || len(got.MustNot) != 4 {
		t.Fatalf("wing-only filter = %#v", got)
	}
	if got := recallFilter("", "reference", false); got == nil || len(got.Must) != 1 {
		t.Fatalf("room-only filter = %#v", got)
	}
	if got := recallFilter("ariadne", "reference", false); got == nil || len(got.Must) != 2 {
		t.Fatalf("wing+room filter = %#v", got)
	}
	if got := recallFilter("ariadne", "reference", true); got == nil || len(got.MustNot) != 1 {
		t.Fatalf("history filter = %#v", got)
	}
}

func TestSaveRejectsCredentialMaterialBeforeNetworkAccess(t *testing.T) {
	_, err := (&Store{}).Save(context.Background(), "API_TOKEN=actual-secret-value", map[string]string{"wing": "app"})
	if err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("Save error = %v", err)
	}
	err = (&Store{}).SaveBatch(context.Background(), []SaveItem{{
		Text: "DB_PASSWORD=actual-secret-value", Meta: map[string]string{"wing": "app"},
	}})
	if err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("SaveBatch error = %v", err)
	}
}

func TestSaveRequiresWingBeforeNetworkAccess(t *testing.T) {
	if _, err := (&Store{}).Save(context.Background(), "safe durable fact", nil); err == nil || err.Error() != "wing is required" {
		t.Fatalf("Save error = %v", err)
	}
	err := (&Store{}).SaveBatch(context.Background(), []SaveItem{{Text: "safe durable fact"}})
	if err == nil || err.Error() != "item 1: wing is required" {
		t.Fatalf("SaveBatch error = %v", err)
	}
}
