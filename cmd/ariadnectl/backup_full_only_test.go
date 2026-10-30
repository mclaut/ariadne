package main

import (
	"ariadne/internal/backupretention"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupFullOnlyBackup(t *testing.T) (*backupFixture, string) {
	t.Helper()
	f := setupManagedBackup(t)
	t.Setenv("ARIADNE_BACKUP_COLLECTIONS", "ariadne")
	t.Setenv("CODEX_HOME", filepath.Join(f.Home, ".codex"))
	source := filepath.Join(f.Home, "owned-source")
	if err := os.WriteFile(source, []byte("owned runtime fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := fullBackupPlan{Format: 1, Sources: map[string]string{"runtime/bin/ariadnectl": source}}
	if err := writeFullJSON(fullPlanPath(), plan); err != nil {
		t.Fatal(err)
	}
	prior := verifyFullRecovery
	t.Cleanup(func() { verifyFullRecovery = prior })
	verifyFullRecovery = func(restored string, _ fullBackupPlan, proofs map[string]backupretention.Manifest) error {
		if len(f.Deleted) != 0 {
			t.Fatal("native source deleted before full recovery")
		}
		a, err := os.Stat(source)
		if err != nil {
			return err
		}
		b, err := os.Stat(filepath.Join(restored, "runtime/bin/ariadnectl"))
		if err != nil {
			return err
		}
		if os.SameFile(a, b) {
			t.Fatal("full restore shares source inode")
		}
		return checkSnapshotManifest(proofs["ariadne"], "ariadne")
	}
	return f, source
}

func TestFullOnlyRepeatedBackupHasNoStandaloneGenerations(t *testing.T) {
	f, source := setupFullOnlyBackup(t)
	for range 2 {
		f.Deleted = nil
		if err := runFullBackup(); err != nil {
			t.Fatal(err)
		}
		keeper := filepath.Join(managedBackupsDir(), backupObjectFull)
		if _, err := backupretention.Verify(keeper); err != nil {
			t.Fatal(err)
		}
		if _, enabled, err := backupretention.FullOnlyKeeper(f.Home); !enabled || err != nil {
			t.Fatalf("full-only keeper: enabled=%v err=%v", enabled, err)
		}
		entries, err := os.ReadDir(managedBackupsDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() && e.Name() != backupObjectFull {
				t.Fatalf("extra recovery generation remains: %s", e.Name())
			}
		}
		if len(f.Deleted) != 1 || !strings.HasSuffix(f.Deleted[0], "ariadne-peer-2026-01-01-00-00-00.snapshot") {
			t.Fatalf("deleted unrelated native snapshot: %v", f.Deleted)
		}
		if _, err = backupretention.CaptureRuntimeBeforeUpgrade(f.Home, "", "v0.8.25"); err != nil {
			t.Fatal(err)
		}
		for _, component := range []string{"codex-mcp", "runtime-cleanup"} {
			if code := backupRetentionCmd([]string{component}); code != 0 {
				t.Fatalf("component %s exit=%d", component, code)
			}
		}
		if err = managedCollectionBackup("ariadne"); err == nil {
			t.Fatal("standalone snapshot bypassed full-only policy")
		}
		if code := backupIfDue(time.Now(), 7*24*time.Hour); code != 0 {
			t.Fatalf("cadence exit=%d", code)
		}
	}
	// The previous full's manifest and collection restore log stay in audit.
	archives, err := os.ReadDir(filepath.Join(backupsDir(), "full-audit"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range archives {
		if !e.IsDir() {
			continue
		}
		archive := filepath.Join(backupsDir(), "full-audit", e.Name())
		if _, err = backupretention.Verify(archive); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(archive, "evidence/snapshots/snapshot-ariadne/restore.log")); err != nil {
			t.Fatal("snapshot audit lost:", err)
		}
		if _, err = os.Stat(filepath.Join(archive, "evidence/snapshots/.retention-audit.jsonl")); err != nil {
			t.Fatal("snapshot retention audit lost:", err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("expected one archived full audit, got %d", count)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("working source changed:", err)
	}
}

func TestFullOnlyFailedRecoveryPreservesFullAndExistingComponents(t *testing.T) {
	f, source := setupFullOnlyBackup(t)
	if err := runFullBackup(); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, object := range []string{"runtime", "snapshot-ariadne"} {
		path, err := backupretention.Capture(managedBackupsDir(), object, map[string]string{"unique": source})
		if err != nil {
			t.Fatal(err)
		}
		before[path], err = backupretention.HashFile(filepath.Join(path, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	full := filepath.Join(managedBackupsDir(), backupObjectFull)
	before[full], _ = backupretention.HashFile(filepath.Join(full, "manifest.json"))
	f.Deleted = nil
	verifyFullRecovery = func(string, fullBackupPlan, map[string]backupretention.Manifest) error {
		return errors.New("functional recovery failed")
	}
	if err := runFullBackup(); err == nil {
		t.Fatal("failed recovery accepted")
	}
	if len(f.Deleted) != 0 {
		t.Fatalf("native recovery source removed on failure: %v", f.Deleted)
	}
	for path, sha := range before {
		actual, err := backupretention.HashFile(filepath.Join(path, "manifest.json"))
		if err != nil || actual != sha {
			t.Fatalf("existing generation changed: %s, %v", path, err)
		}
	}
}

func TestFullOnlyNativeCleanupFailureKeepsPublishedFull(t *testing.T) {
	f, _ := setupFullOnlyBackup(t)
	f.FailDelete = true
	if err := runFullBackup(); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	full := filepath.Join(managedBackupsDir(), backupObjectFull)
	if _, err := backupretention.Verify(full); err != nil {
		t.Fatal("new verified full lost on cleanup failure:", err)
	}
	f.FailDelete = false
	if err := retryFullCleanup(); err != nil {
		t.Fatal(err)
	}
}
