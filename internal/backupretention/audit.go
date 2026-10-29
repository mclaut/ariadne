package backupretention

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

func appendAudit(root string, event map[string]any) error {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	const name = ".retention-audit.jsonl"
	if info, statErr := directory.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("retention audit must be a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	event["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}
