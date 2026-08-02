package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateBackupsArchivesWithoutDiscarding(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < keepBackup+2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%s-202607%02d.snapshot", collection, i+1))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("snapshot-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recent, archived := rotateBackups(dir)
	if recent != keepBackup || archived != 2 {
		t.Fatalf("rotation = recent %d archived %d", recent, archived)
	}
	root, err := filepath.Glob(filepath.Join(dir, collection+"-*.snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := filepath.Glob(filepath.Join(dir, "archive", collection+"-*.snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != keepBackup || len(archive) != 2 {
		t.Fatalf("files = root %d archive %d", len(root), len(archive))
	}
	for _, path := range archive {
		if body, err := os.ReadFile(path); err != nil || len(body) == 0 { //nolint:gosec // path comes from t.TempDir glob
			t.Fatalf("archived snapshot %s: %q, %v", path, body, err)
		}
	}
}

func TestExportOffsetJSONNumberKeepsUint64Precision(t *testing.T) {
	const pointID = "18446744073709551614"
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"result":{"next_page_offset":` + pointID + `}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	result, _ := decoded["result"].(map[string]any)
	offset, ok := result["next_page_offset"].(json.Number)
	if !ok || offset.String() != pointID {
		t.Fatalf("offset = %#v", result["next_page_offset"])
	}
	body, err := json.Marshal(map[string]any{"offset": offset})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"offset":`+pointID+`}` {
		t.Fatalf("re-encoded offset = %s", body)
	}
}
