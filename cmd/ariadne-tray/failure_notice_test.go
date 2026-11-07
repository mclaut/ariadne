package main

import (
	"ariadne/internal/activity"
	"testing"
	"time"
)

const testFailureNoticeStatus = "failed"

func TestFailureNotificationCommandErrorAllowsRetry(t *testing.T) {
	// An empty executable search directory makes notification delivery fail
	// without sending a real desktop notification from the test.
	t.Setenv("PATH", t.TempDir())
	event := activity.Event{At: time.Now().UTC(), Operation: backupOperation, Status: testFailureNoticeStatus}
	mu.Lock()
	previous := lastFailures
	lastFailures = map[string]string{backupOperation: failureNoticeKey(event)}
	mu.Unlock()
	defer func() {
		mu.Lock()
		lastFailures = previous
		mu.Unlock()
	}()
	deliverFailureNotice(event, "test-only notification")
	mu.Lock()
	defer mu.Unlock()
	if len(lastFailures) != 0 {
		t.Fatalf("failed delivery was marked as delivered: %v", lastFailures)
	}
	if got := newFailureNotices(map[string]activity.Event{backupOperation: event}, lastFailures); len(got) != 1 {
		t.Fatalf("failed command did not schedule a retry: %v", got)
	}
}

func TestFailureNoticeNewAttemptsAndRecovery(t *testing.T) {
	start := time.Now().UTC()
	seen := map[string]string{}
	events := map[string]activity.Event{
		backupOperation: {At: start, Operation: backupOperation, Status: testFailureNoticeStatus},
	}
	if got := newFailureNotices(events, seen); len(got) != 1 {
		t.Fatalf("first failure: %v", got)
	}
	if got := newFailureNotices(events, seen); len(got) != 0 {
		t.Fatalf("repeated poll: %v", got)
	}
	events[backupOperation] = activity.Event{At: start.Add(time.Minute), Operation: backupOperation, Status: testFailureNoticeStatus}
	if got := newFailureNotices(events, seen); len(got) != 1 {
		t.Fatalf("new attempt with identical issue text: %v", got)
	}
	events["consolidate"] = activity.Event{At: start, Operation: "consolidate", Status: testFailureNoticeStatus}
	events["maintenance"] = activity.Event{At: start.Add(time.Hour), Operation: "maintenance", Status: "complete"}
	seen = map[string]string{}
	got := newFailureNotices(events, seen)
	if len(got) != 1 || got[0].Operation != backupOperation {
		t.Fatalf("maintenance must resolve child failures but not backup: %v", got)
	}
	events[backupOperation] = activity.Event{At: start.Add(2 * time.Hour), Operation: backupOperation, Status: "complete"}
	if got := newFailureNotices(events, map[string]string{}); len(got) != 0 {
		t.Fatalf("resolved failures: %v", got)
	}
}
