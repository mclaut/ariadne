//go:build !windows

package main

import (
	"path/filepath"
	"testing"
)

func TestTrayInstanceLockRejectsDuplicateAndRecoversAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.lock")
	releaseFirst, acquired, err := acquireTrayInstanceAt(path)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %v), want acquired", acquired, err)
	}

	releaseDuplicate, duplicateAcquired, err := acquireTrayInstanceAt(path)
	if err != nil {
		t.Fatalf("duplicate acquire: %v", err)
	}
	defer releaseDuplicate()
	if duplicateAcquired {
		t.Fatal("duplicate tray instance acquired the lock")
	}

	releaseFirst()
	releaseReplacement, replacementAcquired, err := acquireTrayInstanceAt(path)
	if err != nil || !replacementAcquired {
		t.Fatalf("replacement acquire = (%v, %v), want acquired", replacementAcquired, err)
	}
	releaseReplacement()
}
