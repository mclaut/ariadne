package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// This opt-in test reads an explicit backup but copies it before verification.
func TestIntegrationSnapshotRecovery(t *testing.T) {
	source := os.Getenv("ARIADNE_INTEGRATION_SNAPSHOT")
	if source == "" {
		t.Skip("set an explicit snapshot path for local native recovery verification")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // opt-in test reads the exact operator-supplied snapshot and writes only a private copy
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "input.snapshot")
	if err = copyVerificationInput(source, private, info); err != nil {
		t.Fatal(err)
	}
	proof, err := verifySnapshotRestore(private, home)
	if err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("ARIADNE_INTEGRATION_EXPECT_POINTS"); expected != "" {
		count, parseErr := strconv.ParseInt(expected, 10, 64)
		if parseErr != nil || count != proof.Points {
			t.Fatalf("count=%d expected=%s err=%v", proof.Points, expected, parseErr)
		}
	}
	if len(proof.PointsSHA256) != 64 || len(proof.SchemaSHA256) != 64 {
		t.Fatal("incomplete native recovery proof")
	}
	t.Logf("native recovery passed: points=%d point_digest=%s schema_digest=%s", proof.Points, proof.PointsSHA256, proof.SchemaSHA256)
}
