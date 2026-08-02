package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

type syncMetaCall struct {
	wing, room, except string
	meta               map[string]string
}

type fakeMemfileSyncStore struct {
	pairs map[[2]string]int
	calls []syncMetaCall
}

func (f *fakeMemfileSyncStore) SetMetaByWingRoom(
	_ context.Context, wing, room, except string, meta map[string]string,
) error {
	f.calls = append(f.calls, syncMetaCall{wing: wing, room: room, except: except, meta: meta})
	return nil
}

func (f *fakeMemfileSyncStore) WingRoomPairs(context.Context) (map[[2]string]int, error) {
	return f.pairs, nil
}

func TestWingFromMemPathProjectsSlug(t *testing.T) {
	path := "/home/me/.claude/projects/-Users-me-Projects-Ariadne/memory/notes.md"
	if got := wingFromMemPath(path); got != "Ariadne" {
		t.Fatalf("wing = %q", got)
	}
}

func TestWingFromMemPathFallback(t *testing.T) {
	if got := wingFromMemPath("/tmp/notes.md"); got != "memory" {
		t.Fatalf("wing = %q", got)
	}
}

func TestChunkMarkdownGroupsParagraphsAndDropsTinyChunks(t *testing.T) {
	text := strings.Join([]string{
		"short",
		"This paragraph is long enough to survive chunk filtering.",
		"Another useful paragraph that should also survive filtering.",
	}, "\n\n")
	chunks := chunkMarkdown(text, 70)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %#v", chunks)
	}
	if !strings.Contains(chunks[0], "This paragraph") || !strings.Contains(chunks[1], "Another useful") {
		t.Fatalf("chunks = %#v", chunks)
	}
	if got := chunkMarkdown("tiny", 70); len(got) != 0 {
		t.Fatalf("tiny standalone paragraph should be dropped: %#v", got)
	}
}

func TestMemfileRevisionIsStableAndContentSensitive(t *testing.T) {
	a := memfileRevision([]byte("same"))
	b := memfileRevision([]byte("same"))
	c := memfileRevision([]byte("changed"))
	if a == "" || a != b || a == c {
		t.Fatalf("revisions = %q %q %q", a, b, c)
	}
}

func TestJSONMetadataStringPreservesPortableTypes(t *testing.T) {
	if got := jsonMetadataString("capture"); got != "capture" {
		t.Fatalf("string metadata = %q", got)
	}
	if got := jsonMetadataString(float64(123)); got != "123" {
		t.Fatalf("numeric metadata = %q", got)
	}
	if got := jsonMetadataString(true); got != "" {
		t.Fatalf("unsupported metadata = %q", got)
	}
}

func TestFinalizeMemfileSyncArchivesOldRevisionsAndOrphans(t *testing.T) {
	store := &fakeMemfileSyncStore{pairs: map[[2]string]int{
		{"ariadne", "memory:live.md"}:  4,
		{"ariadne", "memory:gone.md"}:  2,
		{"another", "memory:other.md"}: 3,
	}}
	plan := &memfileSyncPlan{
		revisions: map[[2]string]string{{"ariadne", "memory:live.md"}: "revision-new"},
		wings:     map[string]bool{"ariadne": true},
	}
	if err := finalizeMemfileSync(context.Background(), store, plan,
		time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 {
		t.Fatalf("calls = %#v", store.calls)
	}
	if store.calls[0].wing != "ariadne" || store.calls[0].room != "memory:live.md" ||
		store.calls[0].except != "revision-new" || store.calls[0].meta["status"] != "superseded" {
		t.Fatalf("revision archive = %#v", store.calls[0])
	}
	if store.calls[1].room != "memory:gone.md" || store.calls[1].except != "" ||
		store.calls[1].meta["status"] != "orphaned" {
		t.Fatalf("orphan archive = %#v", store.calls[1])
	}
}
