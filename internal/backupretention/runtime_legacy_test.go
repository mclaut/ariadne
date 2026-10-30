package backupretention

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runtimeFixture(t *testing.T) (string, []string) {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, ".ariadne")
	root := filepath.Join(base, "backups", "previous")
	keeper := filepath.Join(t.TempDir(), "keeper")
	if err := os.WriteFile(keeper, []byte("newest verified CLI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(root, "runtime", map[string]string{"bin/" + runtimeCTLName(): keeper}); err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, 2)
	for _, release := range []string{"v0.8.17-old", "v0.8.18-old"} {
		dir := filepath.Join(base, "releases", release, "previous-runtime")
		dirs = append(dirs, dir)
		files := map[string]string{
			runtimeCTLName(): "older CLI", manifestName: `{"legacy":"retained metadata"}`,
			"audit/events.jsonl": "original event\n", "audit-extra/note": "unique evidence", "Ariadne.app/Contents/Info.plist": "old app metadata",
		}
		for name, data := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		active := filepath.Join(filepath.Dir(dir), "bin", "active-client")
		if err := os.MkdirAll(filepath.Dir(active), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(active, []byte("active image must survive"), 0o600); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != runtimeWindows {
			if err := os.Mkdir(filepath.Join(dir, "originals"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(active, filepath.Join(dir, "originals", "ariadne")); err != nil {
				t.Fatal(err)
			}
		}
	}
	stage, err := NewStage(root, "full")
	if err != nil {
		t.Fatal(err)
	}
	if err = CopyInto(filepath.Join(base, "releases"), filepath.Join(stage, "runtime", "releases")); err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(root, "full", stage, time.Now(), map[string]string{"restore_verified": "true"}); err != nil {
		t.Fatal(err)
	}
	old := probeRuntimeVersion
	probeRuntimeVersion = func(path string) ([]int, error) {
		if !strings.Contains(filepath.ToSlash(path), "previous-runtime/") {
			return []int{0, 8, 20}, nil
		}
		return []int{0, 8, 16}, nil
	}
	t.Cleanup(func() { probeRuntimeVersion = old })
	return home, dirs
}

func TestEqualVersionIdenticalRuntimeCopiesExpire(t *testing.T) {
	home, dirs := runtimeFixture(t)
	root := filepath.Join(home, ".ariadne/backups/previous")
	keeper := filepath.Join(root, "runtime")
	//nolint:gosec // exact canonical binary fixture
	data, err := os.ReadFile(filepath.Join(keeper, "bin", runtimeCTLName()))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		//nolint:gosec // directory is an exact fixture returned by runtimeFixture(t), never external input
		if err = os.WriteFile(filepath.Join(dir, runtimeCTLName()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := NewStage(root, "full")
	if err != nil {
		t.Fatal(err)
	}
	if err = CopyInto(filepath.Join(home, ".ariadne/releases"), filepath.Join(stage, "runtime/releases")); err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(root, "full", stage, time.Now(), map[string]string{"restore_verified": "true"}); err != nil {
		t.Fatal(err)
	}
	probeRuntimeVersion = func(string) ([]int, error) { return []int{0, 8, 20}, nil }
	before, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if err = PruneLegacyRuntimes(home); err != nil {
		t.Fatal(err)
	}
	after, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil || after != before {
		t.Fatal("canonical keeper changed")
	}
	for _, dir := range dirs {
		if _, err = os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("identical duplicate retained")
		}
	}
}

func TestLegacyRuntimeMigrationPreservesAuditAndActiveImages(t *testing.T) {
	home, dirs := runtimeFixture(t)
	keeper := filepath.Join(home, ".ariadne/backups/previous/runtime")
	before, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if err = PruneLegacyRuntimes(home); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if _, err = os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatal("obsolete runtime remains")
		}
		p := filepath.Join(filepath.Dir(dir), "bin", "active-client")
		//nolint:gosec // fixed test sentinel outside the obsolete backup
		if b, readErr := os.ReadFile(p); readErr != nil || string(b) != "active image must survive" {
			t.Fatal("active image changed")
		}
	}
	after, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil || before != after {
		t.Fatal("canonical keeper changed")
	}
	archives, err := os.ReadDir(filepath.Join(home, ".ariadne/backups/runtime-audit"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range archives {
		if !entry.IsDir() {
			continue
		}
		count++
		p := filepath.Join(home, ".ariadne/backups/runtime-audit", entry.Name())
		if _, err = Verify(p); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(p, "provenance.json")); err != nil {
			t.Fatal("retired file inventory missing")
		}
		for _, name := range []string{manifestName, "audit/events.jsonl", "audit-extra/note", "Ariadne.app/Contents/Info.plist"} {
			if _, err = os.Stat(filepath.Join(p, "evidence", filepath.FromSlash(name))); err != nil {
				t.Fatal("audit missing:", name)
			}
		}
		if _, err = os.Stat(filepath.Join(p, "evidence", runtimeCTLName())); !os.IsNotExist(err) {
			t.Fatal("program payload hidden in audit archive")
		}
	}
	// Identical audit inventories can share one immutable archive.
	if count < 1 || count > 2 {
		t.Fatal("unexpected archive count", count)
	}
	if err = PruneLegacyRuntimes(home); err != nil {
		t.Fatal("cleanup not idempotent:", err)
	}
}

func TestLegacyRuntimeBlocksAllDeletionOnDriftMissingProofOrNewerVersion(t *testing.T) {
	for _, kind := range []string{"drift", "missing-proof", "corrupt-keeper", "newer", "equal-different", "restore-failed"} {
		t.Run(kind, func(t *testing.T) {
			home, dirs := runtimeFixture(t)
			base := filepath.Join(home, ".ariadne")
			switch kind {
			case "drift":
				if err := os.WriteFile(filepath.Join(dirs[1], "unique-new-file"), []byte("must survive"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-proof":
				if err := os.Rename(filepath.Join(base, "backups/previous/full"), filepath.Join(base, "preserved-full")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-keeper":
				if err := os.WriteFile(filepath.Join(base, "backups/previous/runtime/bin", runtimeCTLName()), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "newer":
				probeRuntimeVersion = func(path string) ([]int, error) {
					if !strings.Contains(filepath.ToSlash(path), "previous-runtime/") {
						return []int{0, 8, 20}, nil
					}
					return []int{0, 8, 21}, nil
				}
			case "equal-different":
				probeRuntimeVersion = func(string) ([]int, error) { return []int{0, 8, 20}, nil }
			case "restore-failed":
				probeRuntimeVersion = func(path string) ([]int, error) {
					if strings.Contains(path, "runtime-recovery") {
						return nil, errors.New("failed restored version")
					}
					if !strings.Contains(filepath.ToSlash(path), "previous-runtime/") {
						return []int{0, 8, 20}, nil
					}
					return []int{0, 8, 16}, nil
				}
			}
			if err := PruneLegacyRuntimes(home); err == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			for _, dir := range dirs {
				if _, err := os.Stat(dir); err != nil {
					t.Fatal("candidate lost on failed preflight")
				}
			}
		})
	}
}

func TestLegacyRuntimeInUseBlocksDeletion(t *testing.T) {
	home, dirs := runtimeFixture(t)
	f, err := os.Open(filepath.Join(dirs[1], runtimeCTLName()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err = PruneLegacyRuntimes(home); err == nil {
		t.Fatal("active backup was deleted")
	}
	for _, dir := range dirs {
		if _, err = os.Stat(dir); err != nil {
			t.Fatal("candidate deleted while another is active")
		}
	}
}

func TestRuntimeReleaseSelectionAndStrictVersions(t *testing.T) {
	for _, name := range []string{"v1.2.3-local", "v0.8.17-access-queue"} {
		if !legacyRuntimeRelease(name) {
			t.Fatal("valid release rejected", name)
		}
	}
	for _, name := range []string{"", "v1.2-old", "v1.2.3-", "not-runtime", "v1.2.3-x/../../outside", "v1.2.3-x\\outside", "v1.2.3-\x00"} {
		if legacyRuntimeRelease(name) {
			t.Fatal("invalid release accepted", name)
		}
	}
	for _, version := range []string{"v1.2.3-extra", "1.2.3", "v1.2.-3", "v1.2.999999999999999999999999999"} {
		if _, err := parseRuntimeVersion(version); err == nil {
			t.Fatal("unknown runtime accepted", version)
		}
	}
	if _, err := parseRuntimeVersion("v0.8.20"); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeVersionFailurePreservesCandidates(t *testing.T) {
	home, dirs := runtimeFixture(t)
	probeRuntimeVersion = func(string) ([]int, error) { return nil, errors.New("unavailable version") }
	if err := PruneLegacyRuntimes(home); err == nil {
		t.Fatal("version failure ignored")
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Fatal("runtime lost")
		}
	}
}

func TestRuntimeCaptureAlsoMigratesLegacyCopies(t *testing.T) {
	home, dirs := runtimeFixture(t)
	bin := filepath.Join(home, ".ariadne/bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, runtimeCTLName()), []byte("current runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureRuntimeWithApp(home, ""); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("runtime capture omitted migration")
		}
	}
}

func TestSameVersionReinstallMigratesWithoutReplacingPreviousRuntime(t *testing.T) {
	home, dirs := runtimeFixture(t)
	keeper := filepath.Join(home, ".ariadne/backups/previous/runtime")
	before, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	probeRuntimeVersion = func(path string) ([]int, error) {
		p := filepath.ToSlash(path)
		if strings.Contains(p, "previous-runtime/") {
			return []int{0, 8, 16}, nil
		}
		if strings.Contains(p, "backups/previous/") {
			return []int{0, 8, 20}, nil
		}
		return []int{0, 8, 23}, nil
	}
	got, err := CaptureRuntimeBeforeUpgrade(home, "", "v0.8.23")
	if err != nil || got != keeper {
		t.Fatal("reinstall failed", err)
	}
	after, err := HashFile(filepath.Join(keeper, manifestName))
	if err != nil || after != before {
		t.Fatal("same-version reinstall replaced previous runtime")
	}
	for _, dir := range dirs {
		if _, err = os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("reinstall did not migrate legacy copies")
		}
	}
}
