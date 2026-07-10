package main

import (
	"ariadne/internal/i18n"
	"ariadne/internal/version"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"fyne.io/systray"
)

const (
	updateEvery          = 6 * time.Hour
	maxInstallScriptSize = 1 << 20
	latestReleaseURL     = "https://api.github.com/repos/" + version.Repository + "/releases/latest"
)

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type updateStatus struct {
	available  releaseInfo
	checking   bool
	installing bool
}

type updateResult struct {
	Version string `json:"version"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

var updates updateStatus

func updateLoop() {
	ticker := time.NewTicker(updateEvery)
	defer ticker.Stop()
	for range ticker.C {
		checkForUpdates(false)
	}
}

func updateClicked() {
	mu.Lock()
	release := updates.available
	busy := updates.checking || updates.installing
	mu.Unlock()
	if busy {
		return
	}
	if release.TagName == "" {
		checkForUpdates(true)
		return
	}
	startUpdate(release)
}

func checkForUpdates(manual bool) {
	mu.Lock()
	if updates.checking || updates.installing {
		mu.Unlock()
		return
	}
	updates.checking = true
	refreshUpdateMenuLocked()
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	release, err := fetchLatestRelease(ctx, &http.Client{Timeout: 15 * time.Second}, latestReleaseURL)
	newer := err == nil && isNewerVersion(version.Current, release.TagName)

	mu.Lock()
	updates.checking = false
	if newer {
		updates.available = release
	} else {
		updates.available = releaseInfo{}
	}
	activeLang := lang
	refreshUpdateMenuLocked()
	mu.Unlock()

	if err != nil {
		if manual {
			notify(i18n.T(activeLang, "notify.update_title"), i18n.T(activeLang, "notify.update_check_failed"))
		}
		return
	}
	if !newer {
		if manual {
			notify(i18n.T(activeLang, "notify.update_title"),
				fmt.Sprintf(i18n.T(activeLang, "notify.update_current"), version.Tag))
		}
		return
	}
	if markUpdateNotified(release.TagName) {
		notify(i18n.T(activeLang, "notify.update_title"),
			fmt.Sprintf(i18n.T(activeLang, "notify.update_available"), release.TagName))
	}
	if manual {
		startUpdate(release)
	}
}

func refreshUpdateMenuLocked() {
	if mUpdate == nil {
		return
	}
	switch {
	case updates.installing:
		mUpdate.SetTitle(fmt.Sprintf(i18n.T(lang, "menu.updating"), updates.available.TagName))
		mUpdate.Disable()
	case updates.checking:
		mUpdate.SetTitle(i18n.T(lang, "menu.checking_updates"))
		mUpdate.Disable()
	case updates.available.TagName != "":
		mUpdate.SetTitle(fmt.Sprintf(i18n.T(lang, "menu.update_to"), updates.available.TagName))
		mUpdate.Enable()
	default:
		mUpdate.SetTitle(i18n.T(lang, "menu.check_updates"))
		mUpdate.Enable()
	}
}

func startUpdate(release releaseInfo) {
	if !confirmUpdate(release.TagName) {
		return
	}

	mu.Lock()
	if updates.installing {
		mu.Unlock()
		return
	}
	updates.installing = true
	updates.available = release
	refreshUpdateMenuLocked()
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	installerName := "install.sh"
	if runtime.GOOS == osWindows {
		installerName = "install.ps1"
	}
	installURL := "https://raw.githubusercontent.com/" + version.Repository + "/" +
		url.PathEscape(release.TagName) + "/" + installerName
	scriptPath, err := downloadInstaller(
		ctx, &http.Client{Timeout: 30 * time.Second}, installURL, runtimeDir(""), installerName,
	)
	if err != nil {
		updateStartFailed(release.TagName, err)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		_ = os.Remove(scriptPath)
		updateStartFailed(release.TagName, err)
		return
	}
	helperExe, err := updateHelperExecutable(exe)
	if err != nil {
		_ = os.Remove(scriptPath)
		updateStartFailed(release.TagName, err)
		return
	}
	helper := exec.CommandContext( //nolint:gosec // our copied executable, validated release tag, and downloaded installer
		context.Background(), helperExe, "--apply-update", release.TagName, scriptPath,
	)
	if err := helper.Start(); err != nil {
		_ = os.Remove(scriptPath)
		if helperExe != exe {
			_ = os.Remove(helperExe)
		}
		updateStartFailed(release.TagName, err)
		return
	}
	_ = helper.Process.Release()
	systray.Quit()
}

func updateStartFailed(tag string, err error) {
	appendUpdateLog("could not start update to %s: %v\n", tag, err)
	mu.Lock()
	updates.installing = false
	activeLang := lang
	refreshUpdateMenuLocked()
	mu.Unlock()
	notify(i18n.T(activeLang, "notify.update_title"),
		fmt.Sprintf(i18n.T(activeLang, "notify.update_failed"), tag))
}

func confirmUpdate(tag string) bool {
	mu.Lock()
	activeLang := lang
	mu.Unlock()
	title := i18n.T(activeLang, "confirm.update_title")
	body := fmt.Sprintf(i18n.T(activeLang, "confirm.update_body"), tag)
	yes := i18n.T(activeLang, "confirm.update_yes")
	no := i18n.T(activeLang, "confirm.update_no")

	switch runtime.GOOS {
	case osDarwin:
		script := "display dialog " + qq(body) + " with title " + qq(title) +
			" buttons {" + qq(no) + ", " + qq(yes) + "} default button " + qq(yes) +
			" cancel button " + qq(no) + " with icon note"
		cmd := exec.CommandContext( //nolint:gosec // fixed AppleScript with escaped, localized UI text
			context.Background(), "osascript", "-e", script,
		)
		return cmd.Run() == nil
	case osLinux:
		if _, err := exec.LookPath("zenity"); err == nil {
			return exec.CommandContext( //nolint:gosec // fixed dialog binary and localized UI-only arguments
				context.Background(), "zenity", "--question", "--title", title,
				"--text", body, "--ok-label", yes, "--cancel-label", no).Run() == nil
		}
		if _, err := exec.LookPath("kdialog"); err == nil {
			return exec.CommandContext( //nolint:gosec // fixed dialog binary and localized UI-only arguments
				context.Background(), "kdialog", "--title", title, "--yesno", body,
				"--yes-label", yes, "--no-label", no).Run() == nil
		}
		// Choosing the explicit "Update to ..." tray action is the consent fallback
		// on minimal desktops without a dialog provider.
		return true
	case osWindows:
		command := "$w=New-Object -ComObject WScript.Shell;" +
			"$r=$w.Popup(" + psQuote(body) + ",0," + psQuote(title) + ",36);" +
			"if($r -eq 6){exit 0};exit 1"
		return exec.CommandContext( //nolint:gosec // encoded fixed dialog command with localized UI-only text
			context.Background(), "powershell.exe", "-NoProfile", "-NonInteractive",
			"-EncodedCommand", encodePowerShell(command),
		).Run() == nil
	default:
		return false
	}
}

func fetchLatestRelease(ctx context.Context, client *http.Client, endpoint string) (releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ariadne-tray/"+version.Current)
	resp, err := client.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("latest release: HTTP %d", resp.StatusCode)
	}
	var release releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return releaseInfo{}, err
	}
	if release.Draft || release.Prerelease {
		return releaseInfo{}, errors.New("latest release is not stable")
	}
	if _, ok := parseSemver(release.TagName); !ok {
		return releaseInfo{}, fmt.Errorf("invalid release tag %q", release.TagName)
	}
	if release.HTMLURL == "" {
		release.HTMLURL = "https://github.com/" + version.Repository + "/releases/tag/" + release.TagName
	}
	if !trustedReleaseURL(release.HTMLURL) {
		return releaseInfo{}, errors.New("release URL is outside the Ariadne repository")
	}
	return release, nil
}

func downloadInstaller(
	ctx context.Context, client *http.Client, sourceURL, dir, installerName string,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ariadne-tray/"+version.Current)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download installer: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInstallScriptSize+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxInstallScriptSize {
		return "", errors.New("installer is unexpectedly large")
	}
	valid := bytes.HasPrefix(body, []byte("#!/bin/sh")) &&
		bytes.Contains(body, []byte(`REPO="`+version.Repository+`"`))
	pattern := "update-install-*.sh"
	if installerName == "install.ps1" {
		valid = bytes.HasPrefix(body, []byte("# Ariadne Windows installer")) &&
			bytes.Contains(body, []byte(`$Repository = "`+version.Repository+`"`))
		pattern = "update-install-*.ps1"
	}
	if !valid {
		return "", errors.New("downloaded file is not the Ariadne installer")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-owned runtime directory
		return "", err
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(0o700); err != nil {
		return "", err
	}
	if _, err := f.Write(body); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func parseSemver(raw string) ([3]int, bool) {
	var parsed [3]int
	s := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	parts := strings.Split(s, ".")
	if len(parts) != len(parsed) {
		return parsed, false
	}
	for i, part := range parts {
		if part == "" {
			return parsed, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return parsed, false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return parsed, false
		}
		parsed[i] = n
	}
	return parsed, true
}

func isNewerVersion(current, candidate string) bool {
	a, okA := parseSemver(current)
	b, okB := parseSemver(candidate)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if b[i] != a[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func markUpdateNotified(tag string) bool {
	path := runtimeDir("update-notified")
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == tag { //nolint:gosec // own runtime state
		return false
	}
	_ = os.WriteFile(path, []byte(tag+"\n"), 0o644) //nolint:gosec // non-sensitive runtime state
	return true
}

func reportUpdateResult() {
	cleanupUpdateHelpers()
	path := runtimeDir("update-result.json")
	b, err := os.ReadFile(path) //nolint:gosec // own runtime state
	if err != nil {
		return
	}
	_ = os.Remove(path)
	var result updateResult
	if json.Unmarshal(b, &result) != nil || result.Version == "" {
		return
	}
	mu.Lock()
	activeLang := lang
	mu.Unlock()
	key := "notify.update_installed"
	if !result.Success {
		key = "notify.update_failed"
	}
	notify(i18n.T(activeLang, "notify.update_title"), fmt.Sprintf(i18n.T(activeLang, key), result.Version))
}

func applyUpdate(tag, scriptPath string) int {
	if _, ok := parseSemver(tag); !ok || !validUpdateScript(scriptPath) {
		return 2
	}
	_ = os.MkdirAll( //nolint:gosec // user-owned runtime directory
		runtimeDir("logs"), 0o755,
	)
	logf, err := os.OpenFile( //nolint:gosec // fixed update log under the user-owned runtime directory
		runtimeDir("logs/update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644,
	)
	if err != nil {
		return 1
	}
	defer func() { _ = logf.Close() }()
	defer func() { _ = os.Remove(scriptPath) }() //nolint:gosec // validated update-install file under ~/.ariadne

	_, _ = fmt.Fprintf(logf, "\n[%s] updating Ariadne to %s\n", time.Now().Format(time.RFC3339), tag)
	var cmd *exec.Cmd
	if runtime.GOOS == osWindows {
		cmd = exec.CommandContext( //nolint:gosec // validated PowerShell installer under ~/.ariadne
			context.Background(), "powershell.exe", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-File", scriptPath,
			"-Version", tag, "-Yes", "-Update",
		)
	} else {
		cmd = exec.CommandContext( //nolint:gosec // validated shell installer under ~/.ariadne
			context.Background(), "/bin/sh", scriptPath, "-skip-deps",
		)
		cmd.Env = envWith("ARIADNE_REF", tag)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	err = cmd.Run()
	result := updateResult{Version: tag, Success: err == nil}
	if err != nil {
		result.Error = truncateError(err.Error())
		_, _ = fmt.Fprintf(logf, "update failed: %v\n", err)
	} else {
		_, _ = fmt.Fprintln(logf, "update completed")
	}
	if writeErr := writeUpdateResult(result); writeErr != nil {
		_, _ = fmt.Fprintf(logf, "could not write update result: %v\n", writeErr)
	}

	trayName := "ariadne-tray"
	if runtime.GOOS == osWindows {
		trayName += ".exe"
	}
	tray := runtimeDir(filepath.Join("bin", trayName))
	restart := exec.CommandContext(context.Background(), tray) //nolint:gosec // fixed installed tray path
	restart.Stdout, restart.Stderr = logf, logf
	if startErr := restart.Start(); startErr != nil {
		_, _ = fmt.Fprintf(logf, "could not restart tray: %v\n", startErr)
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func updateHelperExecutable(exe string) (string, error) {
	if runtime.GOOS != osWindows {
		return exe, nil
	}
	dir := runtimeDir("updates")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-owned runtime directory
		return "", err
	}
	src, err := os.Open(exe) //nolint:gosec // current executable path from the OS
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.CreateTemp(dir, "ariadne-update-helper-*.exe")
	if err != nil {
		return "", err
	}
	path := dst.Name()
	ok := false
	defer func() {
		_ = dst.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func cleanupUpdateHelpers() {
	paths, _ := filepath.Glob(runtimeDir("updates/ariadne-update-helper-*.exe"))
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func encodePowerShell(command string) string {
	encoded := utf16.Encode([]rune(command))
	bytesLE := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(bytesLE[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(bytesLE)
}

func pathWithin(base, target string) bool {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validUpdateScript(path string) bool {
	if !pathWithin(runtimeDir(""), path) || !strings.HasPrefix(filepath.Base(path), "update-install-") {
		return false
	}
	info, err := os.Lstat(path) //nolint:gosec // path is constrained to ~/.ariadne/update-install-*
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func envWith(key, value string) []string {
	prefix := key + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, prefix) {
			env = append(env, item)
		}
	}
	return append(env, prefix+value)
}

func writeUpdateResult(result updateResult) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return os.WriteFile(runtimeDir("update-result.json"), b, 0o644) //nolint:gosec // non-sensitive runtime state
}

func truncateError(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func appendUpdateLog(format string, args ...any) {
	_ = os.MkdirAll( //nolint:gosec // user-owned runtime directory
		runtimeDir("logs"), 0o755,
	)
	f, err := os.OpenFile( //nolint:gosec // fixed update log under the user-owned runtime directory
		runtimeDir("logs/update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644,
	)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, format, args...)
}

func trustedReleaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return false
	}
	return strings.HasPrefix(u.EscapedPath(), "/"+version.Repository+"/releases/")
}
