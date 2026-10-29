package backupretention

import (
	"reflect"
	"testing"
	"time"
)

func TestOnePreviousSeparatesObjectsAndProtectsUnverifiedCopies(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	copies := []Copy{
		{Path: "old", Object: "memories", SHA256: "a", CreatedAt: now.Add(-time.Hour), Verified: true},
		{Path: "new", Object: "memories", SHA256: "b", CreatedAt: now, Verified: true},
		{Path: "broken", Object: "memories", CreatedAt: now.Add(time.Hour)},
		{Path: "sessions", Object: "sessions", SHA256: "c", CreatedAt: now, Verified: true},
		{Path: "busy", Object: "memories", SHA256: "d", CreatedAt: now.Add(-2 * time.Hour), Verified: true, InUse: true},
	}
	before := append([]Copy(nil), copies...)
	p := OnePrevious(copies)
	if len(p.Keep) != 2 || len(p.Remove) != 1 || p.Remove[0].Path != "old" || len(p.Blocked) != 2 {
		t.Fatalf("plan = %#v", p)
	}
	if !reflect.DeepEqual(copies, before) {
		t.Fatal("mutated input")
	}
}

func TestOnePreviousDuplicatePathsAndEqualTimes(t *testing.T) {
	now := time.Now()
	c := func(path string) Copy {
		return Copy{Path: path, Object: "runtime", SHA256: "ok", CreatedAt: now, Verified: true}
	}
	p := OnePrevious([]Copy{c("b"), c("a"), c("duplicate"), c("duplicate")})
	if len(p.Keep) != 1 || p.Keep[0].Path != "a" || len(p.Remove) != 1 || p.Remove[0].Path != "b" || len(p.Blocked) != 2 {
		t.Fatalf("plan = %#v", p)
	}
}
