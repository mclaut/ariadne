package backupretention

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	fullBackupObject  = "full"
	restoredFullProof = "true"
)

// FullOnlyEnabled selects a single full recovery set when an operator has
// declared its sources. An invalid plan must never fall back to component copies.
func FullOnlyEnabled(home string) (bool, error) {
	path := filepath.Join(home, ".ariadne", "full-backup.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != runtimeWindows && info.Mode().Perm()&0o077 != 0) {
		return false, errors.New("full-backup.json must be a private regular file")
	}
	data, err := os.ReadFile(path) //nolint:gosec // fixed operator-owned plan, never logged
	if err != nil {
		return false, err
	}
	var plan struct {
		Format  int               `json:"format"`
		Sources map[string]string `json:"sources"`
	}
	if err = json.Unmarshal(data, &plan); err != nil {
		return false, err
	}
	if plan.Format != 1 || len(plan.Sources) == 0 {
		return false, errors.New("invalid full backup plan")
	}
	return true, nil
}

// FullOnlyKeeper verifies the sole full recovery set without creating or
// expiring any component copies. Existing copies require a separate cleanup plan.
func FullOnlyKeeper(home string) (string, bool, error) {
	enabled, err := FullOnlyEnabled(home)
	if err != nil || !enabled {
		return "", enabled, err
	}
	keeper := filepath.Join(home, ".ariadne", "backups", "previous", fullBackupObject)
	m, err := Verify(keeper)
	if err != nil {
		return "", true, errors.Join(errors.New("run backup-full to establish a verified full recovery set"), err)
	}
	if m.Object != fullBackupObject || m.Metadata["restore_verified"] != restoredFullProof {
		return "", true, errors.New("restored full recovery set required")
	}
	data, err := os.ReadFile(filepath.Join(keeper, "recovery.json")) //nolint:gosec // checksummed full receipt
	if err != nil {
		return "", true, err
	}
	var receipt struct {
		Status string `json:"status"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		return "", true, err
	}
	if receipt.Status != "PASS" {
		return "", true, errors.New("successful full recovery receipt required")
	}
	keeper, err = physicalDirectory(keeper)
	return keeper, true, err
}

// A valid but older full set must not silently replace a pre-upgrade capture.
// Require its runtime payload to cover the installed files about to change.
func verifyFullRuntime(home, app, keeper string) error {
	data, err := os.ReadFile(filepath.Join(keeper, manifestName)) //nolint:gosec // verified full manifest
	if err != nil {
		return err
	}
	var m Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	entries := make(map[string]Entry, len(m.Entries))
	for _, e := range m.Entries {
		entries[e.Path] = e
	}
	for _, name := range []string{"ariadne", "ariadnectl", "ariadne-hook", "ariadne-launcher", "ariadne-tray", "import", "qdrant"} {
		if runtime.GOOS == runtimeWindows {
			name += ".exe"
		}
		path := filepath.Join(home, ".ariadne", "bin", name)
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			continue
		} else if statErr != nil {
			return statErr
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return resolveErr
		}
		sha, hashErr := HashFile(resolved)
		if hashErr != nil {
			return hashErr
		}
		if e := entries["runtime/bin/"+name]; e.Kind != "file" || e.SHA256 != sha {
			return fmt.Errorf("run backup-full before upgrade: full does not cover installed %s", name)
		}
	}
	if app == "" {
		return nil
	}
	if _, err = os.Lstat(app); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	resolvedApp, err := filepath.EvalSymlinks(app)
	if err != nil {
		return err
	}
	actual, err := inventory(resolvedApp)
	if err != nil {
		return err
	}
	for _, e := range actual {
		e.Path = "applications/Ariadne.app/" + e.Path
		if entries[e.Path] != e {
			return errors.New("run backup-full before upgrade: full does not cover installed application")
		}
	}
	return nil
}
