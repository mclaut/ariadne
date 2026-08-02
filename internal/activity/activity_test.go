package activity

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndLatestPreserveHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	first := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	for _, event := range []Event{
		{At: first, Operation: "memfile_sync", Status: "complete", Counters: map[string]int64{"embedded": 4}},
		{At: second, Operation: "memfile_sync", Status: "complete", Counters: map[string]int64{"skipped": 4}},
		{At: first, Operation: "consolidate", Status: "failed"},
	} {
		if err := AppendAt(path, event); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := LatestAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !latest["memfile_sync"].At.Equal(second) || latest["memfile_sync"].Counters["skipped"] != 4 ||
		latest["consolidate"].Status != "failed" {
		t.Fatalf("latest = %#v", latest)
	}
}
