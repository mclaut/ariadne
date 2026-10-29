//go:build windows

package processgroup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Configure cancels only the new child and its Windows descendant process tree.
func Configure(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tool := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
		//nolint:gosec // fixed OS tool; PID belongs to this exec.Cmd, never a service lookup
		err := exec.CommandContext(ctx, tool, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		if err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
