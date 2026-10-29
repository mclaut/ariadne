//go:build !windows

package processgroup

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGroupHelper(t *testing.T) {
	role := os.Getenv("ARIADNE_TEST_PROCESS_ROLE")
	if role == "" {
		return
	}
	if role == "child" {
		time.Sleep(time.Minute)
		return
	}
	child := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestGroupHelper$") //nolint:gosec // current test binary
	child.Env = append(os.Environ(), "ARIADNE_TEST_PROCESS_ROLE=child")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	fmt.Println(child.Process.Pid)
	_ = child.Wait()
	os.Exit(0)
}

func TestCancellationStopsOnlyOwnedProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGroupHelper$") //nolint:gosec // current test helper binary
	cmd.Env = append(os.Environ(), "ARIADNE_TEST_PROCESS_ROLE=parent")
	Configure(cmd)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || child <= 0 {
		t.Fatalf("invalid helper child: %q", line)
	}
	pgid, err := syscall.Getpgid(child)
	if err != nil || pgid != cmd.Process.Pid {
		t.Fatalf("group %d parent %d err %v", pgid, cmd.Process.Pid, err)
	}
	cancel()
	if err = cmd.Wait(); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	// A reparented zombie has stopped; PID1 may reap it asynchronously in CI.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer checkCancel()
	for {
		//nolint:gosec // read-only ps query for the validated PID emitted by our own helper
		state, queryErr := exec.CommandContext(checkCtx, "ps", "-p", strconv.Itoa(child), "-o", "stat=").Output()
		if checkCtx.Err() != nil {
			t.Fatal("could not verify owned child termination")
		}
		text := strings.TrimSpace(string(state))
		if text == "" && queryErr != nil {
			return
		}
		if strings.HasPrefix(text, "Z") {
			return
		}
		select {
		case <-checkCtx.Done():
			t.Fatalf("owned child still running: %s", text)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
