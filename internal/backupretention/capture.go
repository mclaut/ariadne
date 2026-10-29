package backupretention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Capture keeps one independent, checksummed previous copy of a component.
func Capture(root, object string, sources map[string]string) (string, error) {
	stage, err := NewStage(root, object)
	if err != nil {
		return "", err
	}
	created := time.Now().UTC()
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	count := 0
	for _, name := range names {
		if !fs.ValidPath(name) || name == "." || name == manifestName {
			return "", errors.New("invalid backup source name")
		}
		source := sources[name]
		if _, statErr := os.Lstat(source); os.IsNotExist(statErr) {
			continue
		} else if statErr != nil {
			return "", statErr
		}
		resolved, resolveErr := filepath.EvalSymlinks(source)
		if resolveErr != nil {
			return "", resolveErr
		}
		if err = copyItem(resolved, filepath.Join(stage, filepath.FromSlash(name))); err != nil {
			return "", err
		}
		count++
	}
	if count == 0 {
		if err = os.Remove(stage); err != nil {
			return "", err
		}
		return "", nil
	}
	return Publish(root, object, stage, created, map[string]string{"verification": "independent-copy-sha256"})
}

func copyItem(source, destination string) error {
	before, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	switch {
	case before.Mode()&os.ModeSymlink != 0:
		target, linkErr := os.Readlink(source)
		if linkErr != nil {
			return linkErr
		}
		return os.Symlink(target, destination)
	case before.IsDir():
		if err = os.Mkdir(destination, before.Mode().Perm()); err != nil {
			return err
		}
		children, readErr := os.ReadDir(source)
		if readErr != nil {
			return readErr
		}
		for _, child := range children {
			if err = copyItem(filepath.Join(source, child.Name()), filepath.Join(destination, child.Name())); err != nil {
				return err
			}
		}
		after, statErr := os.Lstat(source)
		if statErr != nil {
			return statErr
		}
		if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
			return errors.New("source directory changed during backup")
		}
		return nil
	case !before.Mode().IsRegular():
		return errors.New("unsupported runtime backup source")
	}
	input, err := os.Open(source) //nolint:gosec // fixed component source; identity checked before reading
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return errors.New("source replaced before backup")
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // exclusive managed stage
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, h), input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if n != before.Size() || !os.SameFile(before, current) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return errors.New("source changed during backup")
	}
	if err = os.Chmod(destination, before.Mode().Perm()); err != nil {
		return err
	}
	if err = os.Chtimes(destination, before.ModTime(), before.ModTime()); err != nil {
		return err
	}
	digest, err := HashFile(destination)
	if err != nil {
		return err
	}
	if digest != hex.EncodeToString(h.Sum(nil)) {
		return errors.New("backup copy checksum mismatch")
	}
	return nil
}

// CaptureRuntime preserves installed binaries and the native app before upgrade.
func CaptureRuntime(home string) (string, error) {
	return CaptureRuntimeWithApp(home, "")
}

func CaptureRuntimeWithApp(home, app string) (string, error) {
	names := []string{"ariadne", "ariadnectl", "import", "ariadne-hook", "ariadne-launcher", "ariadne-tray", "qdrant"}
	sources := make(map[string]string, len(names)+1)
	for _, name := range names {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		sources["bin/"+name] = filepath.Join(home, ".ariadne", "bin", name)
	}
	for _, name := range []string{"ariadne-hook-env", "ariadne-hook-env.ps1", "start-tray.ps1"} {
		path := filepath.Join(home, ".ariadne", name)
		if name == "ariadne-hook-env" {
			path = filepath.Join(home, ".ariadne", "bin", name)
		}
		sources["wrappers/"+name] = path
	}
	if app != "" {
		sources["Ariadne.app"] = app
	}
	sources["version"] = filepath.Join(home, ".ariadne", "version")
	for _, label := range []string{"com.ariadne.qdrant", "com.ariadne.tray", "com.ariadne.sync"} {
		sources["services/"+label+".plist"] = filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	}
	for _, unit := range []string{"ariadne-qdrant.service", "ariadne-sync.service", "ariadne-sync.timer"} {
		sources["services/"+unit] = filepath.Join(home, ".config", "systemd", "user", unit)
	}
	sources["services/ariadne-tray.desktop"] = filepath.Join(home, ".config", "autostart", "ariadne-tray.desktop")
	return Capture(filepath.Join(home, ".ariadne", "backups", "previous"), "runtime", sources)
}

// CaptureRuntimeBeforeUpgrade keeps the last different runtime on idempotent installs.
func CaptureRuntimeBeforeUpgrade(home, app, nextVersion string) (string, error) {
	ctl := filepath.Join(home, ".ariadne", "bin", "ariadnectl")
	if runtime.GOOS == "windows" {
		ctl += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ctl, "version") //nolint:gosec // fixed installed control binary; read-only version command
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		command.Env = append(command.Env, "SystemRoot="+systemRoot)
	}
	out, err := command.Output()
	previous := filepath.Join(home, ".ariadne", "backups", "previous", "runtime")
	if err == nil && strings.TrimSpace(string(out)) == nextVersion {
		if manifest, verifyErr := Verify(previous); verifyErr == nil && manifest.Object == "runtime" {
			return previous, nil
		}
	}
	return CaptureRuntimeWithApp(home, app)
}

// CaptureConfig snapshots only the explicitly named client configuration.
func CaptureConfig(home, object, path string) (string, error) {
	if object != "claude-mcp" && object != "claude-hooks" && object != "codex-mcp" {
		return "", fmt.Errorf("unknown client backup object %q", object)
	}
	expected := ClientConfigPath(home, object)
	if filepath.Clean(path) != expected {
		return "", errors.New("client backup path does not match its component")
	}
	objectID := object
	if object == "codex-mcp" && expected != filepath.Join(home, ".codex", "config.toml") {
		digest := sha256.Sum256([]byte(expected))
		objectID += "-" + hex.EncodeToString(digest[:8])
	}
	root := filepath.Join(home, ".ariadne", "backups", "previous")
	destination, err := Capture(root, "config-"+objectID, map[string]string{"config": path})
	if err != nil || destination == "" {
		return destination, err
	}
	m, err := Verify(destination)
	if err != nil {
		return destination, err
	}
	if err = pruneAdjacentConfigCopies(root, path, m.CreatedAt); err != nil {
		return destination, err
	}
	return destination, nil
}

// ClientConfigPath follows the same explicit Codex profile selection as its CLI.
func ClientConfigPath(home, object string) string {
	switch object {
	case "claude-mcp":
		return filepath.Join(home, ".claude.json")
	case "claude-hooks":
		return filepath.Join(home, ".claude", "settings.json")
	case "codex-mcp":
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return ""
		}
		return filepath.Join(absolute, "config.toml")
	default:
		return ""
	}
}
