package approval

import (
	"testing"
	"time"
)

func TestHistoryRetainsExpiredAndConsumed(t *testing.T) {
	m := New(t.TempDir())
	now := time.Now()
	m.now = func() time.Time { return now }
	request := Request{
		Kind: KindCredential, ClientSession: "client", SourceWing: "source", TargetWing: "target",
		Resource: "test-resource", Purpose: "test",
	}
	a, err := m.Create(request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	b, err := m.Create(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Decide(b.ID, true); err != nil {
		t.Fatal(err)
	}
	scope := CredentialScope{
		ClientSession: "client", SourceWing: "source", TargetWing: "target", Resource: "test-resource", Purpose: "test",
	}
	if _, err := m.AuthorizeCredential(b.ID, scope); err != nil {
		t.Fatal(err)
	}
	entries, err := m.History()
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, entry := range entries {
		states[entry.Request.ID] = entry.Status.State
	}
	if len(entries) != 2 || states[a.ID] != stateExpired || states[b.ID] != "consumed" {
		t.Fatalf("history: %+v", entries)
	}
}
