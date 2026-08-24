//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachUpdateProcess keeps the updater alive after the tray exits. On macOS,
// a LaunchAgent-managed tray and its children otherwise share a lifecycle,
// which can terminate the updater before it invokes the downloaded installer.
func detachUpdateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
