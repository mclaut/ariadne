package backupretention

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveFileKeepsIdentityAcrossRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot")
	if err := os.WriteFile(path, []byte("known backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = RemoveFile(path, sha); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("obsolete backup remains")
	}
}

func TestRemoveFileWrongDigestRestoresOriginal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot")
	if err := os.WriteFile(path, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFile(path, "wrong"); err == nil {
		t.Fatal("wrong digest accepted")
	}
	data, err := os.ReadFile(path) //nolint:gosec // exact test fixture in t.TempDir
	if err != nil || string(data) != "must survive" {
		t.Fatal("original was not restored")
	}
	extra, err := filepath.Glob(filepath.Join(root, ".retention-*"))
	if err != nil || len(extra) != 0 {
		t.Fatal("unexpected staged residual")
	}
}
