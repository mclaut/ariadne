//go:build windows

package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const qdrantTask = `\Ariadne Qdrant`

func loadedAriadneQdrantAgents() []string { return nil }

func control(action string) error {
	switch action {
	case "start":
		if err := run("schtasks.exe", "/Run", "/TN", qdrantTask); err != nil {
			return fmt.Errorf("start Ariadne Qdrant task: %w", err)
		}
	case "stop":
		if err := run("schtasks.exe", "/End", "/TN", qdrantTask); err != nil {
			return fmt.Errorf("stop Ariadne Qdrant task: %w", err)
		}
	}
	fmt.Println(action, "issued (Ariadne Qdrant task; Ollama is managed by its Windows app)")
	return nil
}

func rss(marker string) int64 {
	out, err := exec.CommandContext(context.Background(), "tasklist.exe", "/FO", "CSV", "/NH").Output() //nolint:gosec // fixed command
	if err != nil {
		return 0
	}
	parts := strings.FieldsFunc(marker, func(r rune) bool { return r == '/' || r == '\\' })
	needle := strings.ToLower(parts[len(parts)-1])
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.FieldsPerRecord = -1
	var totalKB int64
	for {
		record, readErr := reader.Read()
		if readErr != nil {
			break
		}
		if len(record) < 5 || !strings.Contains(strings.ToLower(record[0]), needle) {
			continue
		}
		memory := strings.NewReplacer(",", "", ".", "", " ", "", "K", "", "k", "").Replace(record[4])
		if kb, parseErr := strconv.ParseInt(memory, 10, 64); parseErr == nil {
			totalKB += kb
		}
	}
	return totalKB / 1024
}

func freeGB(path string) int64 {
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1
	}
	root := filepath.VolumeName(abs) + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return -1
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &available, nil, nil); err != nil {
		return -1
	}
	return int64(available / (1024 * 1024 * 1024))
}

func run(bin string, args ...string) error {
	return exec.CommandContext(context.Background(), bin, args...).Run() //nolint:gosec // fixed task controls
}
