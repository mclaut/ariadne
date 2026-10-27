package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewPendingRequestNeverBuriedInHistory(t *testing.T) {
	now := time.Now()
	entries := make([]approval.Entry, 0, 157)
	for index := range 155 {
		entries = append(entries, approval.Entry{
			Request: approval.Request{ID: fmt.Sprintf("history-%03d", index), CreatedAt: now.Add(-time.Duration(index) * time.Minute)},
			Status:  approval.Status{State: "expired"},
		})
	}
	entries = append(entries,
		approval.Entry{Request: approval.Request{ID: "new", CreatedAt: now.Add(time.Second)}, Status: approval.Status{State: "pending"}},
		approval.Entry{Request: approval.Request{ID: "old", CreatedAt: now.Add(-time.Hour)}, Status: approval.Status{State: "pending"}},
	)
	before := append([]approval.Entry(nil), entries...)
	pending, pages := approvalQueueSections(entries)
	if len(pending) != 2 || pending[0].Request.ID != "old" || pending[1].Request.ID != "new" {
		t.Fatalf("pending requests mixed with history: %+v", pending)
	}
	if len(pages) != 16 || len(pages[0]) != 10 || len(pages[15]) != 5 {
		t.Fatalf("history pagination lost entries: %d pages", len(pages))
	}
	if pages[0][0].Request.ID != "history-000" || pages[15][4].Request.ID != "history-154" {
		t.Fatal("history is not newest first")
	}
	if !reflect.DeepEqual(entries, before) {
		t.Fatal("menu grouping changed its source history")
	}
	// A decision must move the record to history without losing it.
	entries[len(entries)-2].Status.State = "approved"
	pending, pages = approvalQueueSections(entries)
	if len(pending) != 1 || pages[0][0].Request.ID != "new" || pages[0][0].Status.State != "approved" {
		t.Fatal("newly decided request did not appear first in history")
	}
}

func TestReadableApprovalDetailsKeepFullScope(t *testing.T) {
	entry := approval.Entry{
		Request: approval.Request{
			ID: "1234567890abcdef1234567890abcdef", Kind: approval.KindCredential,
			SourceWing: "owner", TargetWing: "client", Resource: "/private/two  spaces/" + strings.Repeat("path/", 30) + "resource",
			Purpose: "Publish the project", CreatedAt: time.Now(),
		},
		Status: approval.Status{State: "approved"},
	}
	lines := approvalDetailLines(entry, i18n.UK)
	joined := strings.Join(lines, "")
	for _, expected := range []string{entry.Request.Resource, entry.Request.ID, "owner → client", entry.Request.Purpose} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("details lost %q", expected)
		}
	}
	for _, line := range lines {
		if len([]rune(line)) > 70 {
			t.Fatalf("unreadably long line: %q", line)
		}
	}
	if strings.Contains(joined, "approval_id") {
		t.Fatal("human status exposes protocol instructions")
	}
}

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
