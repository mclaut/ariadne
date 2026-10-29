package backupretention

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitCodexProfilesHaveSeparatePreviousCopies(t *testing.T) {
	home := t.TempDir()
	destinations := make([]string, 0, 2)
	for _, name := range []string{"first", "second"} {
		profile := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(profile, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CODEX_HOME", profile)
		source := ClientConfigPath(home, "codex-mcp")
		if err := os.WriteFile(source, []byte("profile="+name), 0o600); err != nil {
			t.Fatal(err)
		}
		dest, err := CaptureConfig(home, "codex-mcp", source)
		if err != nil {
			t.Fatal(err)
		}
		destinations = append(destinations, dest)
	}
	if destinations[0] == destinations[1] {
		t.Fatal("different Codex profiles share one backup object")
	}
	for _, dest := range destinations {
		if _, err := Verify(dest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CaptureConfig(home, "codex-mcp", filepath.Join(home, "unrelated")); err == nil {
		t.Fatal("unselected configuration path accepted")
	}
}
