package approval

import (
	"errors"
	"testing"
	"time"
)

func TestCrossWingApprovalIsScopedAndExpires(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	manager := New(t.TempDir())
	manager.now = func() time.Time { return now }
	request, err := manager.Create(Request{
		Kind: KindCrossWing, ClientSession: "session-a", ActiveWing: "ariadne",
		Query: "shared approval design", Purpose: "compare reusable approaches",
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := manager.Create(Request{
		Kind: KindCrossWing, ClientSession: "session-a", ActiveWing: "ariadne",
		Query: "shared approval design", Purpose: "compare reusable approaches",
	})
	if err != nil || duplicate.ID != request.ID {
		t.Fatalf("duplicate = %#v err=%v", duplicate, err)
	}
	if _, err := manager.AuthorizeCrossWing(request.ID, "session-a", "ariadne", ""); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("authorization before decision = %v", err)
	}
	if _, err := manager.Decide(request.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AuthorizeCrossWing(request.ID, "session-a", "ariadne", ""); err != nil {
		t.Fatalf("authorization = %v", err)
	}
	if _, err := manager.AuthorizeCrossWing(request.ID, "session-b", "ariadne", ""); !errors.Is(err, ErrScope) {
		t.Fatalf("other session authorization = %v", err)
	}
	if _, err := manager.AuthorizeCrossWing(request.ID, "session-a", "ariadne", "sessions"); !errors.Is(err, ErrScope) {
		t.Fatalf("other collection authorization = %v", err)
	}
	now = now.Add(crossWingTTL + time.Second)
	if _, err := manager.AuthorizeCrossWing(request.ID, "session-a", "ariadne", ""); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired authorization = %v", err)
	}
}

func TestCredentialApprovalIsExactAndOneShot(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	manager := New(t.TempDir())
	manager.now = func() time.Time { return now }
	request, err := manager.Create(Request{
		Kind: KindCredential, ClientSession: "session-a", SourceWing: "service-a", TargetWing: "service-b",
		Resource: "deployment credential file", Purpose: "perform one deployment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Decide(request.ID, true); err != nil {
		t.Fatal(err)
	}
	scope := CredentialScope{
		ClientSession: "session-a", SourceWing: "service-a", TargetWing: "service-b",
		Resource: "deployment credential file", Purpose: "perform one deployment",
	}
	if _, err := manager.AuthorizeCredential(request.ID, scope); err != nil {
		t.Fatalf("authorization = %v", err)
	}
	if _, err := manager.AuthorizeCredential(request.ID, scope); !errors.Is(err, ErrConsumed) {
		t.Fatalf("second authorization = %v", err)
	}
}

func TestDeniedAndExpiredRequestsStayOutOfPending(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	manager := New(t.TempDir())
	manager.now = func() time.Time { return now }
	denied, err := manager.Create(Request{
		Kind: KindCrossWing, ClientSession: "session-a", ActiveWing: "ariadne",
		Query: "query", Purpose: "purpose",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Decide(denied.ID, false); err != nil {
		t.Fatal(err)
	}
	if pending, err := manager.Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("pending after denial = %#v err=%v", pending, err)
	}
	expiring, err := manager.Create(Request{
		Kind: KindCrossWing, ClientSession: "session-b", ActiveWing: "ariadne",
		Query: "query", Purpose: "purpose",
	})
	if err != nil || expiring.ID == "" {
		t.Fatalf("expiring request = %#v err=%v", expiring, err)
	}
	now = now.Add(pendingTTL + time.Second)
	if pending, err := manager.Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("pending after expiry = %#v err=%v", pending, err)
	}
}
