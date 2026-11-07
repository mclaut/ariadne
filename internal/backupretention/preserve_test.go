package backupretention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishPreservingKeepsVerifiedPredecessor(t *testing.T) {
	root := t.TempDir()
	start := time.Now()
	for i, body := range []string{"previous", "current"} {
		stage, err := NewStage(root, "full")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(stage, "file"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = PublishPreserving(root, "full", stage, start.Add(time.Duration(i)*time.Second), nil); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join(root, "full", "file")) //nolint:gosec // fixed leaf in a generated test backup root
	if err != nil || string(current) != "current" {
		t.Fatalf("current=%q err=%v", current, err)
	}
	retired, err := filepath.Glob(filepath.Join(root, ".retired-full-*", "generation"))
	if err != nil || len(retired) != 1 {
		t.Fatalf("preserved previous=%v err=%v", retired, err)
	}
	if _, err = Verify(retired[0]); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(filepath.Join(retired[0], "file")) //nolint:gosec // generated verified fixture path
	if err != nil || string(previous) != "previous" {
		t.Fatalf("previous=%q err=%v", previous, err)
	}
}

func TestRecoveryPreservingKeepsIndependentCheckedCopy(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("recoverable"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	called := false
	err := CheckRecoveryCopyPreserving(source, root, func(restored string) error {
		called = true
		if restored == source {
			t.Fatal("recovery was not independent")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("check=%v called=%v", err, called)
	}
	checked, err := filepath.Glob(filepath.Join(root, ".pending-full-restore-*", "checked"))
	if err != nil || len(checked) != 1 {
		t.Fatalf("checked=%v err=%v", checked, err)
	}
	if _, err = Verify(checked[0]); err != nil {
		t.Fatal(err)
	}
}
