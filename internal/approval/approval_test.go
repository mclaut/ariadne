package approval

import (
	"errors"
	"os"
	"path/filepath"
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

func TestTrustedCredentialBindingIsExactAuditedAndRevocable(t *testing.T) {
	root := t.TempDir()
	manager := New(root)
	manager.now = func() time.Time { return time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC) }
	policyScope := CredentialScope{
		SourceWing: "keys", TargetWing: "ariadne", Resource: "/safe/hf_key.txt", Purpose: "publish one release",
	}
	trust, err := manager.SetCredentialTrust(policyScope, true, "ariadnectl")
	if err != nil || trust.Action != CredentialTrust {
		t.Fatalf("trust = %#v err=%v", trust, err)
	}
	duplicate, err := manager.SetCredentialTrust(policyScope, true, "ariadnectl")
	if err != nil || duplicate.ID != trust.ID {
		t.Fatalf("duplicate trust = %#v err=%v", duplicate, err)
	}
	useScope := policyScope
	useScope.ClientSession = "session-a"
	if _, err := manager.AuthorizeTrustedCredential(useScope); err != nil {
		t.Fatalf("trusted authorization = %v", err)
	}
	other := useScope
	other.TargetWing = "other-project"
	if _, err := manager.AuthorizeTrustedCredential(other); !errors.Is(err, ErrNotTrusted) {
		t.Fatalf("other target authorization = %v", err)
	}
	other = useScope
	other.Purpose = "publish a different release"
	if _, err := manager.AuthorizeTrustedCredential(other); !errors.Is(err, ErrNotTrusted) {
		t.Fatalf("other purpose authorization = %v", err)
	}
	uses, err := os.ReadDir(filepath.Join(root, "credential-uses"))
	if err != nil || len(uses) != 1 {
		t.Fatalf("uses = %d err=%v", len(uses), err)
	}
	revoke, err := manager.SetCredentialTrust(policyScope, false, "ariadnectl")
	if err != nil || revoke.Action != CredentialRevoke {
		t.Fatalf("revoke = %#v err=%v", revoke, err)
	}
	if _, err := manager.AuthorizeTrustedCredential(useScope); !errors.Is(err, ErrNotTrusted) {
		t.Fatalf("authorization after revoke = %v", err)
	}
	events, err := os.ReadDir(filepath.Join(root, "credential-trust"))
	if err != nil || len(events) != 2 {
		t.Fatalf("trust events = %d err=%v", len(events), err)
	}
}

func TestMalformedCredentialTrustEventFailsClosed(t *testing.T) {
	root := t.TempDir()
	manager := New(root)
	if err := manager.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "credential-trust", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.AuthorizeTrustedCredential(CredentialScope{
		ClientSession: "session-a", SourceWing: "keys", TargetWing: "ariadne",
		Resource: "/safe/hf_key.txt", Purpose: "publish",
	})
	if err == nil || errors.Is(err, ErrNotTrusted) {
		t.Fatalf("malformed event must fail closed with an integrity error, got %v", err)
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
