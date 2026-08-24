//go:build !windows

// Ariadne Launcher restores the managed macOS menu-bar tray from Finder,
// Spotlight, or Launchpad. The actual long-running process remains owned by
// launchd, so launching the app never creates an unmanaged duplicate.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const trayLabel = "com.ariadne.tray"

func main() {
	if err := launchTray(); err != nil {
		showLaunchError(err)
		os.Exit(1)
	}
}

func launchTray() error {
	if runtime.GOOS != "darwin" {
		return errors.New("Ariadne.app launcher is only available on macOS") //nolint:staticcheck // product name begins the user-facing error
	}
	uid := strconv.Itoa(os.Getuid())
	domain := "gui/" + uid

	if runLaunchctl("kickstart", domain+"/"+trayLabel) == nil {
		return nil
	}
	for _, label := range installedTrayLabels() {
		if runLaunchctl("kickstart", domain+"/"+label) == nil {
			return nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", trayLabel+".plist")
	if _, err := os.Stat(plist); err != nil {
		return fmt.Errorf("tray LaunchAgent is not installed: %w", err)
	}
	if err := runLaunchctl("bootstrap", domain, plist); err != nil &&
		!strings.Contains(err.Error(), "service already loaded") {
		return fmt.Errorf("register tray LaunchAgent: %w", err)
	}
	if err := runLaunchctl("kickstart", domain+"/"+trayLabel); err != nil {
		return fmt.Errorf("start tray LaunchAgent: %w", err)
	}
	return nil
}

func installedTrayLabels() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", "list").Output() //nolint:gosec // fixed command
	if err != nil {
		return nil
	}
	return parseTrayLabels(string(out))
}

func parseTrayLabels(output string) []string {
	labels := make([]string, 0, 1)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		label := fields[len(fields)-1]
		if strings.HasPrefix(label, trayLabel+".") {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	slices.Reverse(labels)
	return slices.Compact(labels)
}

func runLaunchctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput() //nolint:gosec // fixed binary, bounded argv
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(msg)
}

func showLaunchError(err error) {
	if runtime.GOOS != "darwin" {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	message := "Could not start the Ariadne menu-bar monitor. " +
		"Run ~/.ariadne/bin/ariadnectl status or check ~/.ariadne/logs/tray.log.\n\n" + err.Error()
	script := "display alert " + appleScriptQuote("Ariadne") +
		" message " + appleScriptQuote(message) + " as critical"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "osascript", "-e", script).Run() //nolint:gosec // fixed AppleScript with escaped text
}

func appleScriptQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
