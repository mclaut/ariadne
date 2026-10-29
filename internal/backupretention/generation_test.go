package backupretention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureKeepsOneVerifiedPreviousGeneration(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "config.json")
	for _, body := range []string{"first", "second", "third"} {
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		destination, err := Capture(root, "config", map[string]string{"config": source})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Verify(destination); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(destination, "config")) //nolint:gosec // generated test backup in t.TempDir
		if err != nil || string(got) != body {
			t.Fatalf("copy=%q err=%v", got, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "config" && entry.Name() != ".config.lock" && entry.Name() != ".retention-audit.jsonl" {
			t.Fatalf("obsolete generation remains: %s", entry.Name())
		}
	}
}

func TestCapturePreservesModifiedPreviousInsteadOfDeletingIt(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(source, []byte("original"), 0o600)
	destination, err := Capture(root, "runtime", map[string]string{"file": source})
	if err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(destination, "unique")
	_ = os.WriteFile(extra, []byte("must survive"), 0o600)
	_ = os.WriteFile(source, []byte("new"), 0o600)
	if _, err = Capture(root, "runtime", map[string]string{"file": source}); err == nil {
		t.Fatal("modified prior generation was accepted")
	}
	//nolint:gosec // explicit fixture inside t.TempDir
	if data, readErr := os.ReadFile(extra); readErr != nil || string(data) != "must survive" {
		t.Fatal("unknown data lost")
	}
}

func TestPublishRejectsOlderAndForeignStages(t *testing.T) {
	root := t.TempDir()
	stage, err := NewStage(root, "object")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(stage, "file"), []byte("valid"), 0o600)
	now := time.Now()
	dest, err := Publish(root, "object", stage, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := NewStage(root, "object")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(old, "file"), []byte("old"), 0o600)
	if _, err = Publish(root, "object", old, now.Add(-time.Hour), nil); err == nil {
		t.Fatal("older stage replaced newest backup")
	}
	if _, err = Verify(dest); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if _, err = Publish(root, "object", foreign, now.Add(time.Hour), nil); err == nil {
		t.Fatal("foreign stage accepted")
	}
}

func TestGenerationSymlinkCannotDeleteExternalDirectory(t *testing.T) {
	outside := t.TempDir()
	body := []byte(`{"format":1,"object":"x","verified":true,"created_at":"2026-01-01T00:00:00Z"}`)
	_ = os.WriteFile(filepath.Join(outside, "manifest.json"), body, 0o600)
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := RemoveVerified(link); err == nil {
		t.Fatal("symlink generation accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "manifest.json")); err != nil {
		t.Fatal("external data lost")
	}
}
