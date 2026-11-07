package main

import (
	"ariadne/internal/activity"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFullBackupFailureRecordedForBothCommands(t *testing.T) {
	for _, command := range []struct {
		name string
		run  func() int
	}{{"backup", backupCmd}, {"backup-full", fullBackupCmd}} {
		t.Run(command.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			root := filepath.Join(home, ".ariadne")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "full-backup.json"), []byte("invalid plan"), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := command.run(); code == 0 {
				t.Fatal("invalid plan reported success")
			}
			events, err := activity.Latest()
			if err != nil || events["backup"].Status != maintenanceStatusFailed {
				t.Fatalf("backup failure missing: events=%v err=%v", events, err)
			}
			stream, err := os.ReadFile(activity.Path()) //nolint:gosec // private activity stream under this test's HOME
			if err != nil || bytes.Count(stream, []byte("\n")) != 1 {
				t.Fatalf("expected exactly one outcome event: %q err=%v", stream, err)
			}
		})
	}
}
