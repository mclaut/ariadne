package metrics

import (
	"context"
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
	if got.AllTime.ConfirmedSavedTokens != 0 || got.AllTime.RecallOverheadTokens != 120 ||
		got.AllTime.NetAvoidedTokens != -120 {
		t.Fatalf("legacy-only metrics = %+v", got.AllTime)
	}
}
