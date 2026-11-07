package main

import (
	"ariadne/internal/activity"
	"log"
	"time"
)

const backupOperation = "backup"

// Distinct failed attempts must notify even while the issue text stays unchanged.
// Completed maintenance resolves older child failures, but never a failed backup.
func newFailureNotices(events map[string]activity.Event, seen map[string]string) []activity.Event {
	var notices []activity.Event
	maintenance := events["maintenance"]
	for _, operation := range []string{backupOperation, "maintenance", "memfile_sync", "consolidate"} {
		event, ok := events[operation]
		if !ok || event.At.IsZero() {
			continue
		}
		if event.Status != "failed" && event.Status != "partial" && event.Status != "retrying" {
			continue
		}
		if operation != backupOperation && (maintenance.Status == "complete" || maintenance.Status == "complete_with_deferred") &&
			!maintenance.At.Before(event.At) {
			continue
		}
		key := event.At.Format(time.RFC3339Nano) + ":" + event.Status
		if seen[operation] != key {
			seen[operation] = key
			notices = append(notices, event)
		}
	}
	return notices
}

func failureNoticeKey(event activity.Event) string {
	return event.At.Format(time.RFC3339Nano) + ":" + event.Status
}

func deliverFailureNotice(event activity.Event, message string) {
	if err := notifySync("⚠ Ariadne", message); err != nil {
		log.Printf("failure notification failed: operation=%s status=%s error=%q", event.Operation, event.Status, err)
		// Retain the menu warning and retry delivery on the next poll.
		mu.Lock()
		if lastFailures[event.Operation] == failureNoticeKey(event) {
			delete(lastFailures, event.Operation)
		}
		mu.Unlock()
		return
	}
	log.Printf("failure notification command completed: operation=%s status=%s at=%s",
		event.Operation, event.Status, event.At.Format(time.RFC3339Nano))
}
