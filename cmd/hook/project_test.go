package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectWingUsesRepositoryRootFromNestedCWD(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	nested := filepath.Join(root, "internal", "worker")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if got := projectWing(nested); got != "project" {
		t.Fatalf("projectWing = %q", got)
	}
}

func TestProjectWingPrefersStableMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "duplicate-name")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, wingMarker), []byte("stable-project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := projectWing(root); got != "stable-project" {
		t.Fatalf("projectWing = %q", got)
	}
}

func TestProjectWingRejectsUnsafeMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fallback")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, wingMarker), []byte("other/project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := projectWing(root); got != "fallback" {
		t.Fatalf("projectWing = %q", got)
	}
}
