package main

import (
	"ariadne/internal/activity"
	"testing"
	"time"
)

func TestLatestTrayMaintenanceEvent(t *testing.T) {
	start := time.Date(2026, 8, 2, 4, 30, 0, 0, time.UTC)
	event, ok := latestTrayMaintenanceEvent(map[string]activity.Event{
		"maintenance":  {At: start, Operation: "maintenance", Status: "running"},
		"memfile_sync": {At: start.Add(time.Minute), Operation: "memfile_sync", Status: "complete"},
		"backup":       {At: start.Add(time.Hour), Operation: "backup", Status: "complete"},
	})
	if !ok || event.Operation != "memfile_sync" {
		t.Fatalf("latest = %#v, %v", event, ok)
	}
}
