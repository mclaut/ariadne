package backupretention

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func pruneAdjacentConfigCopies(root, path string, before time.Time) error {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := filepath.Base(path) + ".bak-ariadne"
	type obsolete struct{ path, sha string }
	var planned []obsolete
	for _, entry := range entries {
		if entry.Name() != prefix {
			if !strings.HasPrefix(entry.Name(), prefix+"-") {
				continue
			}
			date := strings.TrimPrefix(entry.Name(), prefix+"-")
			created, parseErr := time.ParseInLocation("20060102-150405", date, time.Local)
			if parseErr != nil || created.After(before) {
				continue
			}
		}
		source := filepath.Join(dir, entry.Name())
		info, statErr := os.Lstat(source)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return errors.New("legacy client backup must be a regular file")
		}
		if info.ModTime().After(before) {
			continue
		}
		busy, useErr := InUse(source)
		if useErr != nil {
			return useErr
		}
		if busy {
			return errors.New("legacy client backup is in use")
		}
		sha, hashErr := HashFile(source)
		if hashErr != nil {
			return hashErr
		}
		planned = append(planned, obsolete{source, sha})
	}
	for _, copy := range planned {
		event := map[string]any{"action": "remove_legacy_config", "status": "started", "path": copy.path, "sha256": copy.sha}
		if err = appendAudit(root, event); err != nil {
			return err
		}
		if err = RemoveFile(copy.path, copy.sha); err != nil {
			return err
		}
		event["status"] = "complete"
		if err = appendAudit(root, event); err != nil {
			return err
		}
	}
	return nil
}
