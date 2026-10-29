package backupretention

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func InUse(dir string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, statErr := os.Lstat(dir)
	if statErr != nil {
		return false, statErr
	}
	args := []string{"-nP", "-Fn", "--", dir}
	if info.IsDir() {
		args = []string{"-nP", "-Fn", "+D", dir}
	}
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", args...) //nolint:gosec // fixed tool, managed backup path
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	err := cmd.Run()
	if output.Len() > 0 {
		return true, nil
	}
	if stderr.Len() > 0 {
		return false, fmt.Errorf("cannot verify backup use: %s", stderr.String())
	}
	if err == nil {
		return false, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("verify backup use: %w", err)
}
