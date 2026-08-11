package main

import (
	"ariadne/internal/i18n"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceNotificationQueueIsAppendOnlyAndAcknowledged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "service-notifications.jsonl")
	pending := serviceNotificationEvent{
		ID: "restart-1", At: time.Now(), Status: serviceNotificationPending,
		Lang: i18n.UK, QdrantPID: 101, OllamaPID: 201,
	}
	if err := appendServiceNotification(path, pending); err != nil {
		t.Fatalf("append pending: %v", err)
	}
	got, ok, err := latestPendingServiceNotification(path)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	if !ok || got.ID != pending.ID || got.QdrantPID != 101 || got.OllamaPID != 201 {
		t.Fatalf("pending = %#v, %t", got, ok)
	}
	if err := appendServiceNotification(path, serviceNotificationEvent{
		ID: pending.ID, At: time.Now(), Status: serviceNotificationDelivered,
	}); err != nil {
		t.Fatalf("append delivered: %v", err)
	}
	if got, ok, err := latestPendingServiceNotification(path); err != nil || ok {
		t.Fatalf("pending after delivery = %#v, %t, %v", got, ok, err)
	}
}

func TestLatestPendingServiceNotificationReturnsNewestUndelivered(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "service-notifications.jsonl")
	for _, event := range []serviceNotificationEvent{
		{ID: "old", Status: serviceNotificationPending},
		{ID: "old", Status: serviceNotificationDelivered},
		{ID: "new", Status: serviceNotificationPending, QdrantPID: 303},
	} {
		if err := appendServiceNotification(path, event); err != nil {
			t.Fatalf("append %#v: %v", event, err)
		}
	}
	got, ok, err := latestPendingServiceNotification(path)
	if err != nil || !ok || got.ID != "new" || got.QdrantPID != 303 {
		t.Fatalf("latest pending = %#v, %t, %v", got, ok, err)
	}
}

func TestLatestPendingServiceNotificationDoesNotReplaySupersededPending(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "service-notifications.jsonl")
	for _, event := range []serviceNotificationEvent{
		{ID: "old", Status: serviceNotificationPending},
		{ID: "new", Status: serviceNotificationPending},
		{ID: "new", Status: serviceNotificationDelivered},
	} {
		if err := appendServiceNotification(path, event); err != nil {
			t.Fatalf("append %#v: %v", event, err)
		}
	}
	if got, ok, err := latestPendingServiceNotification(path); err != nil || ok {
		t.Fatalf("superseded pending = %#v, %t, %v", got, ok, err)
	}
}
