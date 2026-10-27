package approval

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatusLifecycleReadOnly(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	m := New(t.TempDir())
	m.now = func() time.Time { return now }
	r, err := m.Create(Request{
		Kind: KindCredential, ClientSession: "client", SourceWing: "source", TargetWing: "target",
		Resource: "/private/key", Purpose: "one release",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertState := func(want string) {
		t.Helper()
		before := approvalFiles(t, m.root)
		s, err := m.Status(r.ID)
		if err != nil || s.State != want {
			t.Fatalf("state=%s err=%v want=%s", s.State, err, want)
		}
		if !reflect.DeepEqual(before, approvalFiles(t, m.root)) {
			t.Fatal("Status mutated approval records")
		}
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"/private/key", "one release", "client_session", "source_wing", "target_wing"} {
			if strings.Contains(string(b), private) {
				t.Fatalf("status exposes %q", private)
			}
		}
	}
	assertState("pending")
	if _, err := m.Decide(r.ID, true); err != nil {
		t.Fatal(err)
	}
	pending, err := m.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	assertState("approved") // approved requests intentionally leave the pending queue
	now = now.Add(credentialTTL)
	assertState("expired") // equality is already expired
	now = now.Add(-time.Second)
	if _, err := m.AuthorizeCredential(r.ID, CredentialScope{
		ClientSession: "client", SourceWing: "source", TargetWing: "target", Resource: "/private/key", Purpose: "one release",
	}); err != nil {
		t.Fatal(err)
	}
	assertState("consumed")
	now = now.Add(time.Hour)
	assertState("consumed")
}

func TestStatusDeniedPendingExpiryAndUnknown(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending expiry", true: "denied"}[deny], func(t *testing.T) {
			now := time.Now().UTC()
			m := New(t.TempDir())
			m.now = func() time.Time { return now }
			r, err := m.Create(Request{Kind: KindCrossWing, ClientSession: "client", ActiveWing: "project", Query: "query", Purpose: "purpose"})
			if err != nil {
				t.Fatal(err)
			}
			if deny {
				if _, err := m.Decide(r.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			now = r.PendingUntil
			s, err := m.Status(r.ID)
			want := "expired"
			if deny {
				want = "denied"
			}
			if err != nil || s.State != want {
				t.Fatalf("%+v %v", s, err)
			}
			if _, err := m.Status("../invalid"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid id: %v", err)
			}
			if _, err := m.Status(strings.Repeat("0", 32)); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown id: %v", err)
			}
		})
	}
}

func TestStatusRejectsCorruptRecords(t *testing.T) {
	for _, scenario := range []string{
		"decision-json", "consumption-json", "decision-id", "consumption-id",
		"consumed-without-decision", "consumed-denied", "request-id",
	} {
		t.Run(scenario, func(t *testing.T) {
			m := New(t.TempDir())
			r, err := m.Create(Request{
				Kind: KindCredential, ClientSession: "client", SourceWing: "source", TargetWing: "target",
				Resource: "resource", Purpose: "purpose",
			})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "decision-json":
				err = os.WriteFile(m.decisionPath(r.ID), []byte("{"), 0o600)
			case "consumption-json":
				err = os.WriteFile(m.consumptionPath(r.ID), []byte("{"), 0o600)
			case "decision-id":
				err = writeExclusiveJSON(m.decisionPath(r.ID), Decision{RequestID: "wrong", Approved: true})
			case "consumed-without-decision":
				err = writeExclusiveJSON(m.consumptionPath(r.ID), Consumption{RequestID: r.ID})
			case "consumed-denied":
				_, err = m.Decide(r.ID, false)
				if err == nil {
					err = writeExclusiveJSON(m.consumptionPath(r.ID), Consumption{RequestID: r.ID})
				}
			case "consumption-id":
				_, err = m.Decide(r.ID, true)
				if err == nil {
					err = writeExclusiveJSON(m.consumptionPath(r.ID), Consumption{RequestID: "wrong"})
				}
			case "request-id":
				other := strings.Repeat("f", 32)
				err = writeExclusiveJSON(m.requestPath(other), r)
				r.ID = other
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Status(r.ID); err == nil {
				t.Fatal("corrupt records accepted")
			}
		})
	}
}

func approvalFiles(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // path comes only from walking this test's private temporary root
		if err != nil {
			return err
		}
		out[path] = sha256.Sum256(b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
