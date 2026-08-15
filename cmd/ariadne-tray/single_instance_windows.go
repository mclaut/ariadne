//go:build windows

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func acquireTrayInstance() (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(`Local\AriadneTray`)
	if err != nil {
		return nil, false, fmt.Errorf("encode tray mutex name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return func() {}, false, nil
	}
	if err != nil {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, false, fmt.Errorf("create tray mutex: %w", err)
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = windows.CloseHandle(handle)
	}, true, nil
}
