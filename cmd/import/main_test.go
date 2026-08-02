package main

import (
	"ariadne/internal/store"
	"context"
	"os"
	"path/filepath"
	"strconv"
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

func (f *fakeMemfileSyncStore) SetMetaBySourceKey(
	_ context.Context, key, except string, meta map[string]string,
) error {
	f.calls = append(f.calls, syncMetaCall{wing: "source:" + key, except: except, meta: meta})
	return nil
}

func (f *fakeMemfileSyncStore) SetMetaByWingRoomLegacy(
	_ context.Context, wing, room string, meta map[string]string,
) error {
	f.calls = append(f.calls, syncMetaCall{wing: wing, room: room, except: "legacy", meta: meta})
	return nil
}

func (f *fakeMemfileSyncStore) TouchActiveMemfiles(_ context.Context, ts int64) error {
	f.calls = append(f.calls, syncMetaCall{wing: "touch", except: strconv.FormatInt(ts, 10)})
	return nil
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

func TestMemfileIdentityUsesRelativePathAndPrivateSourceHash(t *testing.T) {
	path := "/home/me/.claude/projects/-Users-me-Projects-Ariadne/memory/notes/design.md"
	wing, room, legacyRoom, sourceKey, ok := memfileIdentity(path)
	if !ok || wing != "Ariadne" || room != "memory:notes/design.md" || legacyRoom != "memory:design.md" {
		t.Fatalf("identity = %q %q %q %q %v", wing, room, legacyRoom, sourceKey, ok)
	}
	if sourceKey == "" || strings.Contains(sourceKey, "me") || strings.Contains(sourceKey, "Ariadne") {
		t.Fatalf("source key exposes source path: %q", sourceKey)
	}
}

func TestMemfileSyncSkipsKnownUnchangedRevisionBeforeEmbedding(t *testing.T) {
	root := filepath.Join("testdata", "memfiles", "projects")
	path := filepath.Join(root, "-Users-example-Projects-Demo", "memory", "notes", "design.md")
	body, err := os.ReadFile(path) //nolint:gosec // fixed checked-in test fixture
	if err != nil {
		t.Fatal(err)
	}
	wing, room, _, sourceKey, ok := memfileIdentity(path)
	if !ok {
		t.Fatal("fixture did not produce a memfile identity")
	}
	revision := memfileRevision(body)
	jobs := make(chan doc, 4)
	fed, plan, err := feedMemFilesFromRoot(jobs, root, true, false, map[string]store.MemfileSourceState{
		sourceKey: {Wing: wing, Room: room, Revisions: map[string]bool{revision: true}},
	}, time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if fed != 0 || plan.skipped != 1 || len(jobs) != 0 {
		t.Fatalf("fed=%d skipped=%d queued=%d", fed, plan.skipped, len(jobs))
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
	fake := &fakeMemfileSyncStore{pairs: map[[2]string]int{
		{"ariadne", "memory:live.md"}:  4,
		{"ariadne", "memory:gone.md"}:  2,
		{"another", "memory:other.md"}: 3,
	}}
	plan := &memfileSyncPlan{
		sources: map[string]memfileSource{"key-live": {
			wing: "ariadne", room: "memory:live.md", legacyRoom: "memory:live.md", revision: "revision-new",
		}},
		known: map[string]store.MemfileSourceState{
			"key-live": {Wing: "ariadne", Room: "memory:live.md"},
		},
		wings: map[string]bool{"ariadne": true},
	}
	if err := finalizeMemfileSync(context.Background(), fake, plan,
		time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 4 {
		t.Fatalf("calls = %#v", fake.calls)
	}
	if fake.calls[0].wing != "source:key-live" || fake.calls[0].except != "revision-new" ||
		fake.calls[0].meta["status"] != "superseded" {
		t.Fatalf("revision archive = %#v", fake.calls[0])
	}
	if fake.calls[1].except != "legacy" || fake.calls[1].room != "memory:live.md" {
		t.Fatalf("legacy archive = %#v", fake.calls[1])
	}
	if fake.calls[2].room != "memory:gone.md" || fake.calls[2].except != "" ||
		fake.calls[2].meta["status"] != "orphaned" {
		t.Fatalf("orphan archive = %#v", fake.calls[2])
	}
	if fake.calls[3].wing != "touch" {
		t.Fatalf("touch = %#v", fake.calls[3])
	}
}
