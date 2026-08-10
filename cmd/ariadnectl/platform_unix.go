//go:build !windows

package main

import (
	"context"
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
		labels := loadedAriadneQdrantAgents()
		for _, label := range labels {
			if label != qdrantLabel {
				if err := run("launchctl", "bootout", "gui/"+uid+"/"+label); err != nil {
					return fmt.Errorf("bootout obsolete Qdrant job %s: %w", label, err)
				}
			}
		}
		if !slices.Contains(labels, qdrantLabel) {
			if err := run("launchctl", "bootstrap", "gui/"+uid, plist); err != nil {
				return fmt.Errorf("bootstrap Qdrant job: %w", err)
			}
		}
		if err := run("launchctl", "kickstart", "gui/"+uid+"/"+qdrantLabel); err != nil {
			return fmt.Errorf("kickstart Qdrant job: %w", err)
		}
		if err := run("brew", "services", "start", "ollama"); err != nil {
			return fmt.Errorf("start Ollama service: %w", err)
		}
	case "stop":
		for _, label := range loadedAriadneQdrantAgents() {
			if err := run("launchctl", "bootout", "gui/"+uid+"/"+label); err != nil {
				return fmt.Errorf("bootout Qdrant job %s: %w", label, err)
			}
		}
		if err := run("brew", "services", "stop", "ollama"); err != nil {
			return fmt.Errorf("stop Ollama service: %w", err)
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
	return exec.CommandContext(context.Background(), bin, args...).Run() //nolint:gosec // fixed service controls
}
