//go:build !windows

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
)

const qdrantLabel = "com.ariadne.qdrant"

// control starts/stops the native Qdrant service. Ollama remains owned by its
// platform installer on Linux and by Homebrew on macOS.
func control(action string) error {
	if runtime.GOOS == "linux" {
		if err := run("systemctl", "--user", action, "ariadne-qdrant"); err != nil {
			return fmt.Errorf("systemctl %s ariadne-qdrant: %w", action, err)
		}
		fmt.Println(action, "issued (ariadne-qdrant user unit; Ollama is a system service, left alone)")
		return nil
	}
	home, _ := os.UserHomeDir()
	uid := strconv.Itoa(os.Getuid())
	plist := filepath.Join(home, "Library", "LaunchAgents", qdrantLabel+".plist")
	switch action {
	case "start":
		var errs []error
		labels := loadedAriadneQdrantAgents()
		for _, label := range labels {
			if label != qdrantLabel {
				if err := run("launchctl", "bootout", "gui/"+uid+"/"+label); err != nil {
					errs = append(errs, fmt.Errorf("bootout obsolete Qdrant job %s: %w", label, err))
				}
			}
		}
		if !slices.Contains(labels, qdrantLabel) {
			if err := run("launchctl", "bootstrap", "gui/"+uid, plist); err != nil {
				errs = append(errs, fmt.Errorf("bootstrap Qdrant job: %w", err))
			}
		}
		if err := run("launchctl", "kickstart", "gui/"+uid+"/"+qdrantLabel); err != nil {
			errs = append(errs, fmt.Errorf("kickstart Qdrant job: %w", err))
		}
		if err := run("brew", "services", "start", "ollama"); err != nil {
			errs = append(errs, fmt.Errorf("start Ollama service: %w", err))
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
	case "stop":
		var errs []error
		for _, label := range loadedAriadneQdrantAgents() {
			if err := run("launchctl", "bootout", "gui/"+uid+"/"+label); err != nil {
				errs = append(errs, fmt.Errorf("bootout Qdrant job %s: %w", label, err))
			}
		}
		if err := run("brew", "services", "stop", "ollama"); err != nil {
			errs = append(errs, fmt.Errorf("stop Ollama service: %w", err))
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}
	fmt.Println(action, "issued (qdrant agent + ollama brew service)")
	return nil
}

func loadedAriadneQdrantAgents() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	out, err := exec.CommandContext(context.Background(), "launchctl", "list").Output() //nolint:gosec // fixed command
	if err != nil {
		return nil
	}
	return parseAriadneQdrantAgents(string(out))
}

func parseAriadneQdrantAgents(output string) []string {
	labels := make([]string, 0, 1)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		label := fields[len(fields)-1]
		if label == qdrantLabel || strings.HasPrefix(label, qdrantLabel+".") {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	return slices.Compact(labels)
}

func rss(marker string) int64 {
	out, err := exec.CommandContext(context.Background(), "ps", "axo", "rss,args").Output() //nolint:gosec // fixed command
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, marker) || strings.Contains(line, "ariadnectl") {
			continue
		}
		fields := strings.Fields(line)
		if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			total += kb
		}
	}
	return total / 1024
}

func pid(markers ...string) int {
	out, err := exec.CommandContext(context.Background(), "ps", "axo", "pid=,args=").Output() //nolint:gosec // fixed command
	if err != nil {
		return 0
	}
	return parseProcessPID(string(out), markers...)
}

func processFDUsage(processID int) (int, int) {
	if processID <= 0 {
		return 0, 0
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.CommandContext(context.Background(), "lsof", "-nP", "-a", "-p", //nolint:gosec // fixed diagnostic
			strconv.Itoa(processID), "-Ff").Output()
		if err != nil {
			return 0, 0
		}
		open := parseLsofFDCount(string(out))
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", qdrantLabel+".plist")
		limitOut, limitErr := exec.CommandContext(context.Background(), "plutil", "-extract", //nolint:gosec // fixed diagnostic
			"SoftResourceLimits.NumberOfFiles", "raw", "-o", "-", plist).Output()
		if limitErr != nil {
			return open, 0
		}
		limit, _ := strconv.Atoi(strings.TrimSpace(string(limitOut)))
		return open, limit
	}

	entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(processID), "fd"))
	if err != nil {
		return 0, 0
	}
	limits, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(processID), "limits")) //nolint:gosec // fixed proc path
	if err != nil {
		return len(entries), 0
	}
	return len(entries), parseProcOpenFilesLimit(string(limits))
}

func parseLsofFDCount(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 || line[0] != 'f' {
			continue
		}
		if _, err := strconv.Atoi(line[1:]); err == nil {
			count++
		}
	}
	return count
}

func parseProcOpenFilesLimit(output string) int {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Max open files") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return 0
		}
		limit, _ := strconv.Atoi(fields[3])
		return limit
	}
	return 0
}

func parseProcessPID(output string, markers ...string) int {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "ariadnectl") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		matched := false
		for _, marker := range markers {
			if marker != "" && strings.Contains(line, marker) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		value, parseErr := strconv.Atoi(fields[0])
		if parseErr == nil {
			return value
		}
	}
	return 0
}

func freeGB(path string) int64 {
	out, err := exec.CommandContext(context.Background(), "df", "-Pk", path).Output() //nolint:gosec // fixed command
	if err != nil {
		return -1
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return -1
	}
	availableKB, _ := strconv.ParseInt(fields[3], 10, 64)
	return availableKB / (1024 * 1024)
}

func run(bin string, args ...string) error {
	resolved := resolveServiceBinary(bin, exec.LookPath, isExecutableFile)
	cmd := exec.CommandContext(context.Background(), resolved, args...) //nolint:gosec // fixed service controls
	if bin == "brew" {
		cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

func resolveServiceBinary(
	bin string,
	lookPath func(string) (string, error),
	executable func(string) bool,
) string {
	if path, err := lookPath(bin); err == nil {
		return path
	}
	if bin != "brew" {
		return bin
	}
	for _, candidate := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if executable(candidate) {
			return candidate
		}
	}
	return bin
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
