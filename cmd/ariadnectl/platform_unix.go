//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const qdrantLabel = "com.ariadne.qdrant"

// control starts/stops the native Qdrant service. Ollama remains owned by its
// platform installer on Linux and by Homebrew on macOS.
func control(action string) {
	if runtime.GOOS == "linux" {
		run("systemctl", "--user", action, "ariadne-qdrant")
		fmt.Println(action, "issued (ariadne-qdrant user unit; Ollama is a system service, left alone)")
		return
	}
	home, _ := os.UserHomeDir()
	uid := strconv.Itoa(os.Getuid())
	plist := filepath.Join(home, "Library", "LaunchAgents", qdrantLabel+".plist")
	switch action {
	case "start":
		run("launchctl", "bootstrap", "gui/"+uid, plist)
		run("brew", "services", "start", "ollama")
	case "stop":
		run("launchctl", "bootout", "gui/"+uid+"/"+qdrantLabel)
		run("brew", "services", "stop", "ollama")
	}
	fmt.Println(action, "issued (qdrant agent + ollama brew service)")
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

func run(bin string, args ...string) {
	_ = exec.CommandContext(context.Background(), bin, args...).Run() //nolint:gosec,errcheck // fixed service controls
}
