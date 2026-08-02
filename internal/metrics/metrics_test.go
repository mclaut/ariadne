package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEstimateTokensIsDeterministicAndMultilingual(t *testing.T) {
	if got := EstimateTokens("    "); got != 0 {
		t.Fatalf("blank estimate = %d", got)
	}
	if got := EstimateTokens("1234"); got != 1 {
		t.Fatalf("ASCII estimate = %d", got)
	}
	if got := EstimateTokens("пам"); got != 2 {
		t.Fatalf("UTF-8 estimate = %d", got)
	}
}

func TestRecordRecallSupportsConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	if err := RecordRecallAt(ctx, path, Event{ID: "initial", Source: "test"}); err != nil {
		t.Fatal(err)
	}

	const writers = 12
	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- RecordRecallAt(ctx, path, Event{
				ID: fmt.Sprintf("writer-%d", i), Source: "test", DeliveredTokens: 10,
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := ReadAt(ctx, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.Recalls != writers+1 || got.AllTime.DeliveredTokens != writers*10 {
		t.Fatalf("concurrent totals = %+v", got.AllTime)
	}
}

func TestRepresentedShareRequiresSourceMetadata(t *testing.T) {
	if got := RepresentedShare(0, 100, 50); got != 0 {
		t.Fatalf("legacy share = %d", got)
	}
	if got := RepresentedShare(1_000, 100, 25); got != 250 {
		t.Fatalf("partial share = %d", got)
	}
	if got := RepresentedShare(1_000, 100, 120); got != 1_000 {
		t.Fatalf("full share = %d", got)
	}
}

func TestSplitAttributionIncludesSharedRecallCost(t *testing.T) {
	if attributed, unknown := SplitAttribution(120, 100, 25); attributed != 30 || unknown != 90 {
		t.Fatalf("mixed attribution = %d/%d", attributed, unknown)
	}
	if attributed, unknown := SplitAttribution(120, 100, 0); attributed != 0 || unknown != 120 {
		t.Fatalf("unknown attribution = %d/%d", attributed, unknown)
	}
	if attributed, unknown := SplitAttribution(120, 100, 100); attributed != 120 || unknown != 0 {
		t.Fatalf("full attribution = %d/%d", attributed, unknown)
	}
}

func TestRecordRecallAggregatesAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	event := Event{
		ID: "first-recall", RepresentedID: "same-session", At: now, Source: "auto",
		DeliveredTokens: 100, RepresentedTokens: 900, Memories: 3,
	}
	if err := RecordRecallAt(ctx, path, event); err != nil {
		t.Fatal(err)
	}
	event.ID = "repeated-recall"
	if err := RecordRecallAt(ctx, path, event); err != nil {
		t.Fatal(err)
	}
	if err := RecordRecallAt(ctx, path, Event{
		ID: "old", At: now.Add(-31 * 24 * time.Hour), Source: "mcp",
		DeliveredTokens: 50, RepresentedTokens: 250, Memories: 1,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadAt(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.Recalls != 3 || got.AllTime.NetAvoidedTokens != 900 {
		t.Fatalf("all time = %+v", got.AllTime)
	}
	if got.AllTime.ConfirmedSavedTokens != 1_000 || got.AllTime.RecallOverheadTokens != 100 {
		t.Fatalf("all-time saved/overhead = %+v", got.AllTime)
	}
	if got.Last30Days.Recalls != 2 || got.Last30Days.NetAvoidedTokens != 700 {
		t.Fatalf("last 30 days = %+v", got.Last30Days)
	}
	if got.Last30Days.ConfirmedSavedTokens != 800 || got.Last30Days.RecallOverheadTokens != 100 {
		t.Fatalf("recent saved/overhead = %+v", got.Last30Days)
	}
}

func TestMetricsNeverReportNegativeSavings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	if err := RecordRecallAt(ctx, path, Event{
		ID: "legacy", DeliveredTokens: 120, RepresentedTokens: 0, Memories: 1,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAt(ctx, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.ConfirmedSavedTokens != 0 || got.AllTime.RecallOverheadTokens != 0 ||
		got.AllTime.NetAvoidedTokens != 0 || got.AllTime.UnattributedTokens != 120 ||
		got.AllTime.AttributionPercent != 0 {
		t.Fatalf("legacy-only metrics = %+v", got.AllTime)
	}
}

func TestRepresentationsDeduplicatePerMemory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	first := Event{
		ID: "first", Source: "mcp", DeliveredTokens: 100, AttributedTokens: 100, Memories: 2,
		AttributedMemories: 2,
		Representations:    []Representation{{ID: "session-memory-a", Tokens: 900}, {ID: "session-memory-b", Tokens: 100}},
	}
	if err := RecordRecallAt(ctx, path, first); err != nil {
		t.Fatal(err)
	}
	if err := RecordRecallAt(ctx, path, Event{
		ID: "second", Source: "mcp", DeliveredTokens: 50, AttributedTokens: 50, Memories: 1,
		AttributedMemories: 1, Representations: []Representation{{ID: "session-memory-a", Tokens: 900}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAt(ctx, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.RepresentedTokens != 1_000 || got.AllTime.ConfirmedSavedTokens != 900 ||
		got.AllTime.RecallOverheadTokens != 50 || got.AllTime.NetAvoidedTokens != 850 {
		t.Fatalf("deduplicated totals = %+v", got.AllTime)
	}
}

func TestSessionSourceIDTracksLineageNotPointID(t *testing.T) {
	a := SessionSourceID("mcp", "session", "source-a")
	b := SessionSourceID("mcp", "session", "source-a")
	if a == "" || a != b {
		t.Fatalf("source ids = %q/%q", a, b)
	}
	if a == SessionSourceID("mcp", "session", "source-b") ||
		a == SessionSourceID("mcp", "other-session", "source-a") {
		t.Fatal("source id did not include lineage and session scope")
	}
}

func TestReadMigratesV1UnknownDelivery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE recall_events (
		id TEXT PRIMARY KEY, ts INTEGER NOT NULL, source TEXT NOT NULL,
		delivered_tokens INTEGER NOT NULL, represented_tokens INTEGER NOT NULL,
		memories INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO recall_events VALUES ('legacy', 1, 'mcp', 120, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ReadAt(ctx, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.UnattributedTokens != 120 || got.AllTime.UnattributedMemories != 1 ||
		got.AllTime.RecallOverheadTokens != 0 || got.AllTime.NetAvoidedTokens != 0 {
		t.Fatalf("migrated totals = %+v", got.AllTime)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO recall_events
		(id, ts, source, delivered_tokens, represented_tokens, memories)
		VALUES ('old-client', 2, 'mcp', 80, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = ReadAt(ctx, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.AllTime.UnattributedTokens != 200 || got.AllTime.UnattributedMemories != 2 {
		t.Fatalf("old-client totals = %+v", got.AllTime)
	}
}
