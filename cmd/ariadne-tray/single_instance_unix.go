//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireTrayInstance() (func(), bool, error) {
	return acquireTrayInstanceAt(runtimeDir(filepath.Join("state", "tray.lock")))
}

func acquireTrayInstanceAt(path string) (func(), bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("create lock directory: %w", err)
	}
	lockFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed user-owned runtime path
	if err != nil {
		return nil, false, fmt.Errorf("open tray lock: %w", err)
	}
	lockFD := int(lockFile.Fd()) //nolint:gosec // file descriptors are OS ints; Go exposes them as uintptr
	if err := unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return func() {}, false, nil
		}
		return nil, false, fmt.Errorf("lock tray instance: %w", err)
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = unix.Flock(lockFD, unix.LOCK_UN)
		_ = lockFile.Close()
	}, true, nil
}
