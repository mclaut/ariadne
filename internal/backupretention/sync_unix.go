//go:build !windows

package backupretention

import "os"

func syncDirectory(path string) error {
	f, err := os.Open(path) //nolint:gosec // managed physical backup directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
