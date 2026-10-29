//go:build !windows

package backupretention

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func Lock(root, object string) (func(), error) {
	if !validObject(object) {
		return nil, fmt.Errorf("invalid backup lock object")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, "."+object+".lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // managed lock file
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) //nolint:gosec // Unix kernel descriptors are C int values.
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("backup operation already running: %w", err)
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = f.Close() }, nil
}
