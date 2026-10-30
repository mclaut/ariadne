package backupretention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fullOnlyFixture(t *testing.T, receipt string, metadata map[string]string) (string, string) {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, ".ariadne")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := []byte(`{"format":1,"sources":{"runtime":"/explicit/source"}}`)
	if err := os.WriteFile(filepath.Join(base, "full-backup.json"), plan, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "backups", "previous")
	stage, err := NewStage(root, fullBackupObject)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "recovery.json"), []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(stage, "runtime/bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "runtime/bin", runtimeCTLName()), []byte("fixture binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	keeper, err := Publish(root, fullBackupObject, stage, time.Now(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	return home, keeper
}

func TestFullOnlyComponentPathsReuseFullAndPreserveExistingCopies(t *testing.T) {
	home, keeper := fullOnlyFixture(t, `{"status":"PASS"}`, map[string]string{"restore_verified": restoredFullProof})
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	legacy := filepath.Join(home, ".ariadne", "releases", "v0.8.17-old", "previous-runtime")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(legacy, "unique")
	if err := os.WriteFile(sentinel, []byte("retain pending exact approval"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, capture := range []func() (string, error){
		func() (string, error) { return CaptureRuntime(home) },
		func() (string, error) { return CaptureRuntimeBeforeUpgrade(home, "", "v0.8.25") },
		func() (string, error) { return CaptureConfig(home, "codex-mcp", ClientConfigPath(home, "codex-mcp")) },
		func() (string, error) { return CaptureConfig(home, "claude-mcp", ClientConfigPath(home, "claude-mcp")) },
	} {
		path, err := capture()
		if err != nil || path != keeper {
			t.Fatalf("path=%q err=%v", path, err)
		}
	}
	if err := PruneLegacyRuntimes(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("legacy copy changed without cleanup approval:", err)
	}
	for _, object := range []string{"runtime", "config-codex-mcp", "config-claude-mcp"} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(keeper), object)); !os.IsNotExist(err) {
			t.Fatalf("standalone %s created: %v", object, err)
		}
	}
}

func TestFullOnlyBlocksComponentCaptureWithoutSuccessfulRecovery(t *testing.T) {
	for _, failure := range []string{"missing", "changed", "failed-recovery", "missing-proof"} {
		t.Run(failure, func(t *testing.T) {
			receipt := `{"status":"PASS"}`
			metadata := map[string]string{"restore_verified": restoredFullProof}
			if failure == "failed-recovery" {
				receipt = `{"status":"FAIL"}`
			}
			if failure == "missing-proof" {
				metadata = nil
			}
			home, keeper := fullOnlyFixture(t, receipt, metadata)
			if failure == "missing" {
				if err := os.Rename(keeper, keeper+"-preserved"); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "changed" {
				if err := os.WriteFile(filepath.Join(keeper, "unknown"), []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CaptureRuntimeBeforeUpgrade(home, "", "v0.8.25"); err == nil {
				t.Fatal("unverified full accepted")
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(keeper), "runtime")); !os.IsNotExist(err) {
				t.Fatalf("silently fell back to standalone runtime: %v", err)
			}
		})
	}
}

func TestFullOnlyMalformedPlanDoesNotFallBack(t *testing.T) {
	home, _ := fullOnlyFixture(t, `{"status":"PASS"}`, map[string]string{"restore_verified": restoredFullProof})
	if err := os.WriteFile(filepath.Join(home, ".ariadne", "full-backup.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureRuntime(home); err == nil {
		t.Fatal("invalid plan fell back to component capture")
	}
}

func TestFullOnlyUpgradeRejectsStaleRuntimeInOtherwiseVerifiedFull(t *testing.T) {
	home, _ := fullOnlyFixture(t, `{"status":"PASS"}`, map[string]string{"restore_verified": restoredFullProof})
	bin := filepath.Join(home, ".ariadne/bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, runtimeCTLName())
	if err := os.WriteFile(path, []byte("fixture binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureRuntimeBeforeUpgrade(home, "", "v0.8.25"); err != nil {
		t.Fatal("current runtime not accepted:", err)
	}
	if err := os.WriteFile(path, []byte("newer installed binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureRuntimeBeforeUpgrade(home, "", "v0.8.25"); err == nil {
		t.Fatal("stale full used as pre-upgrade recovery")
	}
}
