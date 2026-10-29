package backupretention

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyIntoRejectsOverlapAndExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyInto(source, filepath.Join(source, "nested", "dest")); err == nil {
		t.Fatal("recursive copy accepted")
	}
	if err := CopyInto(source, source); err == nil {
		t.Fatal("existing destination accepted")
	}
	dest := filepath.Join(root, "sibling", "dest")
	if err := CopyInto(source, dest); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(source, "file"))
	b, _ := os.Stat(filepath.Join(dest, "file"))
	if os.SameFile(a, b) {
		t.Fatal("full copy is not independent")
	}
}

func TestRecoveryCopyBlocksPublicationOnFailedCheck(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "payload")
	if err := os.WriteFile(file, []byte("unique"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sentinel := errors.New("functional recovery failed")
	if err := CheckRecoveryCopy(source, root, func(restored string) error {
		a, _ := os.Stat(file)
		b, _ := os.Stat(filepath.Join(restored, "payload"))
		if os.SameFile(a, b) {
			t.Fatal("hardlinked recovery test")
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
	//nolint:gosec // fixed temporary fixture
	if got, _ := os.ReadFile(file); string(got) != "unique" {
		t.Fatal("source changed")
	}
	if err := CheckRecoveryCopy(source, root, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func legacyFullFixture(t *testing.T, root string) LegacyFull {
	t.Helper()
	name := "full-20200101T000000Z"
	dir := filepath.Join(root, name)
	paths := []string{
		"runtime/state/approvals/decision.json", "runtime/logs/activity.log", "runtime-extra", "verification/RESTORE.json", "snapshot",
	}
	for _, path := range paths {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{"path": e.Path, "mode": e.Mode, "type": e.Kind, "bytes": e.Bytes, "sha256": e.SHA256, "target": e.Target}
		rows = append(rows, row)
	}
	b, err := json.Marshal(map[string]any{"id": name, "entries": rows, "self_excluded": []string{"MANIFEST-v2.json"}})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "MANIFEST-v2.json")
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	sha, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return LegacyFull{Name: name, Manifest: "MANIFEST-v2.json", SHA256: sha}
}

func fullKeeperFixture(t *testing.T, root string, verified bool) string {
	t.Helper()
	root = filepath.Join(root, "previous")
	stage, err := NewStage(root, "full")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "snapshot"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{}
	if verified {
		metadata["restore_verified"] = "true"
	}
	dest, err := Publish(root, "full", stage, time.Now(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestLegacyFullRotationPreservesAuditAndForeignDirectories(t *testing.T) {
	root := t.TempDir()
	old := legacyFullFixture(t, root)
	keeper := fullKeeperFixture(t, root, true)
	foreign := filepath.Join(root, "history-audit")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PruneLegacyFull(root, keeper, old); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, old.Name)); !os.IsNotExist(err) {
		t.Fatal("legacy full remains")
	}
	archived := filepath.Join(root, "full-audit", "full-"+old.SHA256)
	if _, err := Verify(archived); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(archived, "evidence/runtime/state/approvals/decision.json")
	//nolint:gosec // verified temporary audit fixture
	if b, err := os.ReadFile(path); err != nil || string(b) != "runtime/state/approvals/decision.json" {
		t.Fatal("audit lost")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("foreign audit removed")
	}
	if _, err := Verify(keeper); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyFullRotationRefusesUnverifiedOrModified(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "unverified", true: "modified"}[modified], func(t *testing.T) {
			root := t.TempDir()
			old := legacyFullFixture(t, root)
			keeper := fullKeeperFixture(t, root, modified)
			if modified {
				if err := os.WriteFile(filepath.Join(root, old.Name, "unique-new-file"), []byte("unique"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := PruneLegacyFull(root, keeper, old); err == nil {
				t.Fatal("unsafe rotation accepted")
			}
			if _, err := os.Stat(filepath.Join(root, old.Name, "snapshot")); err != nil {
				t.Fatal("old full lost")
			}
		})
	}
}
