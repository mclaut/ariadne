package backupretention

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
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
	overlap := &windows.Overlapped{}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("backup operation already running: %w", err)
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlap); _ = f.Close() }, nil
}
