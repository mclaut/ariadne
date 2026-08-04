package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const wingMarker = ".ariadne-wing"

// projectWing resolves subdirectories to their repository root and supports a
// stable explicit slug in .ariadne-wing. This avoids capturing a nested cwd as
// a different project. Repositories with the same basename should add distinct
// markers instead of sharing one wing accidentally.
func projectWing(cwd string) string {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "" || cwd == "." {
		return ""
	}
	root := nearestProjectRoot(cwd)
	if marker := readWingMarker(root); marker != "" {
		return marker
	}
	return filepath.Base(root)
}

func nearestProjectRoot(cwd string) string {
	current := cwd
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return cwd
		}
		current = parent
	}
}

func readWingMarker(root string) string {
	b, err := os.ReadFile(filepath.Join(root, wingMarker)) //nolint:gosec // fixed marker under the project root
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(b))
	if value == "" || len([]rune(value)) > 128 || strings.ContainsAny(value, `/\\`) {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ""
		}
	}
	return value
}
