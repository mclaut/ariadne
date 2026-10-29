package main

import (
	"archive/tar"
	"ariadne/internal/backupretention"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeArchiveFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, name := range []string{"config.json", "version.info", "0/segments/fixture.tar"} {
		body := []byte("{}")
		if strings.HasSuffix(name, ".tar") {
			var nested bytes.Buffer
			inner := tar.NewWriter(&nested)
			if err := inner.Close(); err != nil {
				t.Fatal(err)
			}
			body = nested.Bytes()
		}
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type backupFixture struct {
	Home        string
	Archive     []byte
	Deleted     []string
	BadChecksum bool
	FailDelete  bool
}

func setupManagedBackup(t *testing.T) *backupFixture {
	t.Helper()
	f := &backupFixture{Home: t.TempDir(), Archive: nativeArchiveFixture(t)}
	t.Setenv("HOME", f.Home)
	t.Setenv("USERPROFILE", f.Home)
	if err := os.MkdirAll(backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	oldURL, oldCollection, oldVerifier := qdrantREST, collection, verifyBackupSnapshot
	t.Cleanup(func() { qdrantREST, collection, verifyBackupSnapshot = oldURL, oldCollection, oldVerifier })
	collection = "ariadne"
	verifyBackupSnapshot = func(_, _ string) (snapshotProof, error) {
		schema, _ := vectorSchemaDigest(fixtureVectorConfig())
		return snapshotProof{Points: 2, PointsSHA256: strings.Repeat("a", 64), SchemaSHA256: schema}, nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/collections/ariadne/points/count":
			_, _ = w.Write([]byte(`{"result":{"count":2}}`))
		case r.URL.Path == "/collections/ariadne":
			_ = json.NewEncoder(w).Encode(fixtureVectorConfig())
		case r.Method == http.MethodPost && r.URL.Path == "/collections/ariadne/snapshots":
			sum := sha256.Sum256(f.Archive)
			checksum := hex.EncodeToString(sum[:])
			if f.BadChecksum {
				checksum = strings.Repeat("0", 64)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
				"name": "ariadne-peer-2026-01-01-00-00-00.snapshot", "size": len(f.Archive), "checksum": checksum,
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/collections/ariadne/snapshots/ariadne-peer-2026-01-01-00-00-00.snapshot":
			_, _ = w.Write(f.Archive)
		case r.Method == http.MethodGet && r.URL.Path == "/collections/ariadne/snapshots":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{
				{"name": "ariadne-peer-2026-01-01-00-00-00.snapshot"},
				{"name": "ariadne-peer-2099-01-01-00-00-00.snapshot"},
				{"name": "unowned"},
			}})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/collections/ariadne/snapshots/"):
			if f.FailDelete {
				http.Error(w, "failure", 500)
				return
			}
			f.Deleted = append(f.Deleted, r.URL.Path)
			_, _ = w.Write([]byte(`{"status":"ok","result":true}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	t.Cleanup(server.Close)
	qdrantREST = server.URL
	return f
}

func TestManagedBackupKeepsOneAndCleansOnlyMatchingLegacy(t *testing.T) {
	f := setupManagedBackup(t)
	root := backupsDir()
	archive := filepath.Join(root, "archive")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	old := []string{filepath.Join(root, "ariadne-20200101-000000.snapshot"), filepath.Join(archive, "ariadne-20200102-000000.snapshot")}
	for _, p := range old {
		if err := os.WriteFile(p, f.Archive, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(root, "sessions-20200101-000000.snapshot")
	if err := os.WriteFile(foreign, f.Archive, 0o600); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(root, "full-existing")
	if err := os.Mkdir(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "record"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := managedCollectionBackup("ariadne"); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(managedBackupsDir(), "snapshot-ariadne")
	m, err := backupretention.Verify(dest)
	if err != nil || m.Metadata["restore_verified"] != "true" {
		t.Fatalf("backup=%#v err=%v", m, err)
	}
	for _, p := range old {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("old copy remains: %s", p)
		}
	}
	if _, err = os.Stat(foreign); err != nil {
		t.Fatal("other collection was removed")
	}
	if _, err = os.Stat(filepath.Join(evidence, "record")); err != nil {
		t.Fatal("full backup/evidence was removed")
	}
	if len(f.Deleted) != 2 {
		t.Fatalf("native removals=%v", f.Deleted)
	}
	entries, err := os.ReadDir(managedBackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pending") || strings.HasPrefix(e.Name(), ".retired") {
			t.Fatalf("obsolete generation remains: %s", e.Name())
		}
	}
}

func TestFailedVerificationNeverPrunesOldBackup(t *testing.T) {
	for _, failure := range []string{"checksum", "restore", "mutation", "wrong-count", "wrong-schema"} {
		t.Run(failure, func(t *testing.T) {
			f := setupManagedBackup(t)
			if err := managedCollectionBackup("ariadne"); err != nil {
				t.Fatal(err)
			}
			keeper := filepath.Join(managedBackupsDir(), "snapshot-ariadne")
			original, err := os.ReadFile(filepath.Join(keeper, "manifest.json")) //nolint:gosec // generated fixture under t.TempDir
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "wrong-count", "wrong-schema":
				verifyBackupSnapshot = func(_, _ string) (snapshotProof, error) {
					schema, _ := vectorSchemaDigest(fixtureVectorConfig())
					n := int64(2)
					if failure == "wrong-count" {
						n = 0
					} else {
						schema = strings.Repeat("0", 64)
					}
					return snapshotProof{Points: n, PointsSHA256: strings.Repeat("a", 64), SchemaSHA256: schema}, nil
				}
			case "checksum":
				f.BadChecksum = true
			case "restore":
				verifyBackupSnapshot = func(_, _ string) (snapshotProof, error) { return snapshotProof{}, errors.New("failed restore") }
			case "mutation":
				verifyBackupSnapshot = func(path, _ string) (snapshotProof, error) {
					if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
						t.Fatal(err)
					}
					return snapshotProof{Points: 2}, nil
				}
			}
			if err = managedCollectionBackup("ariadne"); err == nil {
				t.Fatal("failure accepted")
			}
			current, err := os.ReadFile(filepath.Join(keeper, "manifest.json")) //nolint:gosec // generated fixture under t.TempDir
			if err != nil || !bytes.Equal(original, current) {
				t.Fatal("previous keeper changed after failed verification")
			}
			if len(f.Deleted) != 1 {
				t.Fatal("native backup deleted after failed verification")
			}
		})
	}
}

func TestNativeCleanupFailureRetainsVerifiedExternalCopy(t *testing.T) {
	f := setupManagedBackup(t)
	f.FailDelete = true
	if err := managedCollectionBackup("ariadne"); err == nil {
		t.Fatal("native cleanup failure hidden")
	}
	if _, err := backupretention.Verify(filepath.Join(managedBackupsDir(), "snapshot-ariadne")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupCadenceIgnoresPartialAndMutableMtime(t *testing.T) {
	f := setupManagedBackup(t)
	partial := filepath.Join(backupsDir(), "ariadne-20990101-000000.snapshot")
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	latest, err := latestBackupTime(backupsDir())
	if err != nil || !latest.IsZero() {
		t.Fatalf("partial counted as verified: %v %v", latest, err)
	}
	if err = os.Remove(partial); err != nil {
		t.Fatal(err)
	}
	if err = managedCollectionBackup("ariadne"); err != nil {
		t.Fatal(err)
	}
	expected, err := latestBackupTime(backupsDir())
	if err != nil || expected.IsZero() {
		t.Fatal(err)
	}
	path := filepath.Join(managedBackupsDir(), "snapshot-ariadne", "snapshot.snapshot")
	future := time.Now().AddDate(10, 0, 0)
	if err = os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	got, err := latestBackupTime(backupsDir())
	if err != nil || !got.Equal(expected) {
		t.Fatalf("mtime changed cadence: %v %v", got, err)
	}
	_ = f
}

func TestRestoredPointVerificationRejectsIncompleteAndDuplicatePages(t *testing.T) {
	for _, kind := range []string{"valid", "missing-vector", "duplicate", "wrong-count"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/count") {
					count := 1
					if kind == "wrong-count" {
						count = 2
					}
					_, _ = fmt.Fprintf(w, `{"result":{"count":%d}}`, count)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/scroll") {
					point := `{"id":18446744073709551614,"payload":{"text":"fixture"},"vector":[0.1]}`
					if kind == "missing-vector" {
						point = `{"id":1,"payload":{}}`
					}
					if kind == "duplicate" {
						point += "," + point
					}
					_, _ = fmt.Fprintf(w, `{"result":{"points":[%s],"next_page_offset":null}}`, point)
					return
				}
				_, _ = w.Write([]byte(`{"result":{"status":"green"}}`))
			}))
			defer server.Close()
			n, sha, err := verifyRestoredPoints(context.Background(), server.Client(), server.URL)
			if kind == "valid" {
				if err != nil || n != 1 || len(sha) != 64 {
					t.Fatalf("valid=%d %s %v", n, sha, err)
				}
			} else if err == nil {
				t.Fatal("invalid restore accepted")
			}
		})
	}
}

func fixtureVectorConfig() map[string]any {
	return map[string]any{"result": map[string]any{"config": map[string]any{"params": map[string]any{
		"vectors": map[string]any{"dense": map[string]any{"size": 1, "distance": "Cosine"}}, "sparse_vectors": nil,
	}}}}
}

func TestQdrantRedirectCannotRetargetDeletionOrForwardCredentials(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collections/working", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	old := qdrantREST
	qdrantREST = source.URL
	t.Cleanup(func() { qdrantREST = old })
	t.Setenv("ARIADNE_QDRANT_API_KEY", "fixture-api-key")
	if err := httpDo(http.MethodDelete, source.URL+"/collections/ariadne/snapshots/old", nil, nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalls != 0 {
		t.Fatal("snapshot deletion or credentials were redirected to another resource")
	}
}
