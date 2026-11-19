package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/i18n"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceHealthIssueFailedAndPartial(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	for _, statusValue := range []string{maintenanceStatusFailed, maintenanceStatusPartial} {
		issue := maintenanceHealthIssue(now, map[string]activity.Event{
			"maintenance": {At: now.Add(-time.Minute), Operation: "maintenance", Status: statusValue},
		}, i18n.EN)
		if !strings.Contains(issue, statusValue) {
			t.Fatalf("status %q issue = %q", statusValue, issue)
		}
	}
}

func TestMaintenanceHealthIssueSafeDeferredCompletionIsHealthy(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	issue := maintenanceHealthIssue(now, map[string]activity.Event{
		"maintenance": {
			At: now.Add(-time.Minute), Operation: "maintenance", Status: "complete_with_deferred",
			Counters: map[string]int64{"deferred_stages": 1},
		},
	}, i18n.EN)
	if issue != "" {
		t.Fatalf("safe deferred completion issue = %q", issue)
	}
}

func TestMaintenanceHealthIssueMissing(t *testing.T) {
	issue := maintenanceHealthIssue(time.Now(), nil, i18n.EN)
	if !strings.Contains(issue, "never") {
		t.Fatalf("missing issue = %q", issue)
	}
}

func stubSystemBootTime(t *testing.T, boot time.Time, known bool) {
	t.Helper()
	previous := systemBootTime
	systemBootTime = func() (time.Time, bool) { return boot, known }
	t.Cleanup(func() { systemBootTime = previous })
}

func TestMaintenanceHealthIssueStaleAndStuck(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	stubSystemBootTime(t, now.Add(-24*time.Hour), true)
	t.Setenv("ARIADNE_MAINTENANCE_STALE_AFTER", "36h")
	t.Setenv("ARIADNE_MAINTENANCE_STUCK_AFTER", "2h")
	stale := maintenanceHealthIssue(now, map[string]activity.Event{
		"maintenance": {At: now.Add(-37 * time.Hour), Operation: "maintenance", Status: backupStatusComplete},
	}, i18n.EN)
	if !strings.Contains(stale, "stale") {
		t.Fatalf("stale issue = %q", stale)
	}
	stuck := maintenanceHealthIssue(now, map[string]activity.Event{
		"maintenance": {At: now.Add(-3 * time.Hour), Operation: "maintenance", Status: "retrying"},
	}, i18n.EN)
	if !strings.Contains(stuck, "stuck") {
		t.Fatalf("stuck issue = %q", stuck)
	}
}

func TestLatestMaintenanceEventUsesNewestRelevantOperation(t *testing.T) {
	start := time.Date(2026, 8, 2, 4, 30, 0, 0, time.UTC)
	event, ok := latestMaintenanceEvent(map[string]activity.Event{
		"maintenance":  {At: start, Operation: "maintenance", Status: "running"},
		"memfile_sync": {At: start.Add(time.Minute), Operation: "memfile_sync", Status: backupStatusComplete},
		"backup":       {At: start.Add(time.Hour), Operation: "backup", Status: backupStatusComplete},
	})
	if !ok || event.Operation != "memfile_sync" {
		t.Fatalf("latest = %#v, %v", event, ok)
	}
}

func TestMaintenanceFailuresAreNotHiddenByNewerProgress(t *testing.T) {
	now := time.Now()
	events := map[string]activity.Event{
		"consolidate":  {At: now.Add(-time.Minute), Operation: "consolidate", Status: maintenanceStatusFailed},
		"maintenance":  {At: now, Operation: "maintenance", Status: "retrying"},
		"memfile_sync": {At: now, Operation: "memfile_sync", Status: backupStatusComplete},
	}
	if issue := maintenanceHealthIssue(now, events, i18n.EN); issue == "" {
		t.Fatal("retry wait incorrectly healthy")
	}
	events["maintenance"] = activity.Event{At: now, Operation: "maintenance", Status: "running"}
	if issue := maintenanceHealthIssue(now, events, i18n.EN); !strings.Contains(issue, maintenanceStatusFailed) {
		t.Fatalf("newer running event hid child failure: %q", issue)
	}
	events["maintenance"] = activity.Event{At: now, Operation: "maintenance", Status: backupStatusComplete}
	if issue := maintenanceHealthIssue(now, events, i18n.EN); issue != "" {
		t.Fatalf("verified maintenance completion did not resolve failure: %q", issue)
	}
}

func TestBackupFailureSurvivesMaintenanceCompletion(t *testing.T) {
	now := time.Now()
	events := map[string]activity.Event{
		"backup":      {At: now.Add(-time.Minute), Operation: "backup", Status: maintenanceStatusFailed},
		"maintenance": {At: now, Operation: "maintenance", Status: backupStatusComplete},
	}
	if issue := backupHealthIssue(events, i18n.UK); !strings.Contains(issue, maintenanceStatusFailed) {
		t.Fatalf("backup failure hidden: %q", issue)
	}
	events["backup"] = activity.Event{At: now, Operation: "backup", Status: backupStatusComplete}
	if issue := backupHealthIssue(events, i18n.UK); issue != "" {
		t.Fatalf("successful backup did not clear warning: %q", issue)
	}
}

func TestMaintenanceHealthIssueInterruptedByRestart(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 9, 0, 0, time.UTC)
	stubSystemBootTime(t, now.Add(-25*time.Minute), true)
	t.Setenv("ARIADNE_MAINTENANCE_STUCK_AFTER", "2h")
	for _, status := range []string{"running", "retrying"} {
		issue := maintenanceHealthIssue(now, map[string]activity.Event{
			"maintenance": {At: now.Add(-3 * time.Hour), Operation: "maintenance", Status: status},
		}, i18n.EN)
		if !strings.Contains(issue, "interrupted by a system restart") {
			t.Fatalf("%s before boot: issue = %q", status, issue)
		}
	}

	// A run that started after boot is still live and keeps the stuck timeout.
	stubSystemBootTime(t, now.Add(-6*time.Hour), true)
	stuck := maintenanceHealthIssue(now, map[string]activity.Event{
		"maintenance": {At: now.Add(-3 * time.Hour), Operation: "maintenance", Status: "running"},
	}, i18n.EN)
	if !strings.Contains(stuck, "stuck") {
		t.Fatalf("running after boot: issue = %q", stuck)
	}

	// Unknown boot time falls back to the stuck timeout.
	stubSystemBootTime(t, time.Time{}, false)
	if fallback := maintenanceHealthIssue(now, map[string]activity.Event{
		"maintenance": {At: now.Add(-3 * time.Hour), Operation: "maintenance", Status: "running"},
	}, i18n.EN); !strings.Contains(fallback, "stuck") {
		t.Fatalf("unknown boot: issue = %q", fallback)
	}
}

func TestReadSystemBootTimeIsInThePast(t *testing.T) {
	boot, ok := readSystemBootTime()
	if !ok {
		t.Skip("boot time unavailable on this platform")
	}
	if !boot.Before(time.Now()) {
		t.Fatalf("boot time %v is not in the past", boot)
	}
}

func TestLongConsolidationDrainWithProgressIsHealthy(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	stubSystemBootTime(t, now.Add(-24*time.Hour), true)
	events := map[string]activity.Event{
		"maintenance": {At: now.Add(-4 * time.Hour), Operation: "maintenance", Status: "running"},
		"consolidate": {At: now.Add(-5 * time.Minute), Operation: "consolidate", Status: "running"},
	}
	if issue := maintenanceHealthIssue(now, events, i18n.EN); issue != "" {
		t.Fatalf("a drain that keeps reporting progress is not stuck: %q", issue)
	}
	events["consolidate"] = activity.Event{At: now.Add(-3 * time.Hour), Operation: "consolidate", Status: "running"}
	if issue := maintenanceHealthIssue(now, events, i18n.EN); issue == "" {
		t.Fatal("a drain without progress for hours must still be reported as stuck")
	}
}
