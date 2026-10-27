package main

import (
	"ariadne/internal/approval"
	"testing"
	"time"
)

func TestPromptCooldownDoesNotStarveNextRequest(t *testing.T) {
	var gate approvalPromptGate
	now := time.Now()
	if !gate.begin("first", now) {
		t.Fatal("first not shown")
	}
	if gate.begin("second", now) {
		t.Fatal("overlapping dialogs")
	}
	gate.finish("first", now)
	if gate.begin("first", now) {
		t.Fatal("first bypassed cooldown")
	}
	if !gate.begin("second", now) {
		t.Fatal("second starved")
	}
	gate.finish("second", now)
	if gate.begin("first", now) {
		t.Fatal("second erased first cooldown")
	}
	if !gate.begin("first", now.Add(approvalPromptRetryEvery)) {
		t.Fatal("retry not allowed")
	}
}

func TestPromptRotationVisitsEntireQueue(t *testing.T) {
	var gate approvalPromptGate
	now := time.Now()
	pending := make([]approval.Request, 0, 5)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		pending = append(pending, approval.Request{ID: id, PendingUntil: now.Add(10 * time.Minute)})
	}
	for _, expected := range []string{"a", "b", "c", "d", "e"} {
		request, ok := gate.next(pending, now)
		if !ok || request.ID != expected {
			t.Fatalf("expected %s, got %s", expected, request.ID)
		}
		now = now.Add(30 * time.Second)
		gate.finish(request.ID, now)
	}
}
