// ariadne-tray is the system-tray monitor for the ariadne stack (Qdrant + Ollama
// + the ariadne MCP server) on macOS, Linux and Windows. Thin viewer: it shells
// `ariadnectl status -json` and renders; all logic lives in the Go core.
// Cross-platform via fyne.io/systray. The UI is localized (internal/i18n) with a
// live Language switcher.
package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"ariadne/internal/metrics"
	"ariadne/internal/version"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
)

const (
	pollEvery                = 5 * time.Second
	manualMaintenanceTimeout = 4 * time.Hour
	trayRestartExitCode      = 75
	osDarwin                 = "darwin"
	osLinux                  = "linux"
	osWindows                = "windows"
)

func ctlPath() string {
	if configured := os.Getenv("ARIADNE_CTL_PATH"); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "bin", "ariadnectl")
}

func runtimeDir(sub string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", sub)
}

// mirrors the JSON printed by `ariadnectl status -json`.
type svc struct {
	Up      bool   `json:"up"`
	PID     int    `json:"pid"`
	RSSMB   int64  `json:"rss_mb"`
	Version string `json:"version"`
}

type coll struct {
	Points int64  `json:"points"`
	Status string `json:"status"`
}

type status struct {
	reachable    bool
	OK           bool                      `json:"ok"`
	Qdrant       svc                       `json:"qdrant"`
	Ollama       svc                       `json:"ollama"`
	Collection   coll                      `json:"collection"`
	TokenMetrics metrics.Summary           `json:"token_metrics"`
	Maintenance  map[string]activity.Event `json:"maintenance"`
	DataMB       int64                     `json:"data_mb"`
	BackupsMB    int64                     `json:"backups_mb"`
	LogsMB       int64                     `json:"logs_mb"`
	RuntimeMB    int64                     `json:"runtime_mb"`
	FreeGB       int64                     `json:"free_gb"`
	Issues       []string                  `json:"issues"`
}

var (
	mu         sync.Mutex // serializes UI updates across poll/switch goroutines
	lang       i18n.Lang
	lastIssues []string

	rowVersion, rowHealth, rowQdrant, rowOllama, rowPoints                                       *systray.MenuItem
	rowTokens, rowCoverage, rowUnattributed, rowDisk, rowMaintenance                             *systray.MenuItem
	rowApproval, rowApprovalDetail                                                               *systray.MenuItem
	mUpdate, mStart, mStop, mRestart, mMaintenance, mBackup, mExport, mData, mLogs, mLang, mQuit *systray.MenuItem
	mApprove, mDeny                                                                              *systray.MenuItem
	langItems                                                                                    map[i18n.Lang]*systray.MenuItem
	maintenanceRunning                                                                           bool
	serviceActionRunning                                                                         bool
	approvalManager                                                                              = approval.New("")
	currentApproval                                                                              approval.Request
	hasCurrentApproval                                                                           bool
	currentApprovalCount                                                                         int
	lastNotifiedApprovalID                                                                       string
	approvalPrompts                                                                              approvalPromptGate
	trayExitMu                                                                                   sync.Mutex
	trayExitReason                                                                               = "desktop session ended"
	trayRestartRequested                                                                         bool
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--apply-update" {
		os.Exit(applyUpdate(os.Args[2], os.Args[3]))
	}
	log.Printf("Ariadne %s tray starting", version.Tag)
	systray.Run(onReady, onExit)
	trayExitMu.Lock()
	restart := trayRestartRequested
	trayExitMu.Unlock()
	if restart {
		os.Exit(trayRestartExitCode)
	}
}

func onExit() {
	trayExitMu.Lock()
	reason := trayExitReason
	trayExitMu.Unlock()
	log.Printf("Ariadne %s tray exiting: %s", version.Tag, reason)
}

func quitTray(reason string) {
	trayExitMu.Lock()
	trayExitReason = reason
	trayExitMu.Unlock()
	systray.Quit()
}

func restartTrayThroughSupervisor(reason string) {
	trayExitMu.Lock()
	trayExitReason = reason
	trayRestartRequested = true
	trayExitMu.Unlock()
	systray.Quit()
}

func onReady() {
	lang = i18n.Current()
	systray.SetIcon(dotIcon(gray))
	systray.SetTitle("") // dot only — no text label
	systray.SetTooltip("Ariadne " + version.Tag)

	rowVersion = infoRow("Ariadne " + version.Tag)
	rowHealth = infoRow("…")
	rowQdrant = infoRow("")
	rowOllama = infoRow("")
	rowPoints = infoRow("")
	rowTokens = infoRow("")
	rowCoverage = infoRow("")
	rowUnattributed = infoRow("")
	rowDisk = infoRow("")
	rowMaintenance = infoRow("")
	rowApproval = infoRow("")
	rowApprovalDetail = infoRow("")
	mApprove = systray.AddMenuItem("", "")
	mDeny = systray.AddMenuItem("", "")
	systray.AddSeparator()
	mUpdate = systray.AddMenuItem("", "")
	systray.AddSeparator()
	mStart = systray.AddMenuItem("", "")
	mStop = systray.AddMenuItem("", "")
	mRestart = systray.AddMenuItem("", "")
	mMaintenance = systray.AddMenuItem("", "")
	systray.AddSeparator()
	mBackup = systray.AddMenuItem("", "")
	mExport = systray.AddMenuItem("", "")
	mData = systray.AddMenuItem("", "")
	mLogs = systray.AddMenuItem("", "")
	systray.AddSeparator()
	mLang = systray.AddMenuItem("", "")
	langItems = make(map[i18n.Lang]*systray.MenuItem, len(i18n.Available))
	for _, l := range i18n.Available {
		langItems[l] = mLang.AddSubMenuItem(i18n.Flag[l]+"  "+i18n.Name[l], "")
	}
	systray.AddSeparator()
	mQuit = systray.AddMenuItem("", "")

	relabel()
	// one click-listener goroutine per language item — extensible: new languages
	// in i18n.Available get their own listener automatically.
	for l, it := range langItems {
		go func(l i18n.Lang, it *systray.MenuItem) {
			for range it.ClickedCh {
				switchLang(l)
			}
		}(l, it)
	}
	go poll()
	go loop()
	go reportUpdateResult()
	go reportPendingServiceNotification()
	go checkForUpdates(false)
	go updateLoop()
}

func loop() {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			poll()
		case <-mStart.ClickedCh:
			serviceActionClicked(serviceStart)
		case <-mStop.ClickedCh:
			serviceActionClicked(serviceStop)
		case <-mRestart.ClickedCh:
			serviceActionClicked(serviceRestart)
		case <-mMaintenance.ClickedCh:
			go maintenanceClicked()
		case <-mApprove.ClickedCh:
			go decideApproval(true)
		case <-mDeny.ClickedCh:
			go decideApproval(false)
		case <-mUpdate.ClickedCh:
			go updateClicked()
		case <-mBackup.ClickedCh:
			_ = ctl("backup", i18n.T(lang, "notify.backup"))
		case <-mExport.ClickedCh:
			_ = ctl("export", i18n.T(lang, "notify.export"))
		case <-mData.ClickedCh:
			openPath(runtimeDir("backups"))
		case <-mLogs.ClickedCh:
			openPath(runtimeDir("logs"))
		case <-mQuit.ClickedCh:
			quitTray("user selected Quit")
			return
		}
	}
}

func switchLang(l i18n.Lang) {
	mu.Lock()
	lang = l
	_ = i18n.Set(l)
	mu.Unlock()
	relabel()
	poll() // re-render rows + re-fetch (ariadnectl now emits issues in the new lang)
}

// relabel sets every static menu title in the active language + ticks the
// current language in the switcher.
func relabel() {
	mu.Lock()
	defer mu.Unlock()
	refreshServiceMenusLocked()
	refreshMaintenanceMenuLocked()
	mBackup.SetTitle(i18n.T(lang, "menu.backup"))
	mExport.SetTitle(i18n.T(lang, "menu.export"))
	mData.SetTitle(i18n.T(lang, "menu.data"))
	mLogs.SetTitle(i18n.T(lang, "menu.logs"))
	mLang.SetTitle("🌐 " + i18n.T(lang, "menu.language") + ": " + i18n.Name[lang])
	mQuit.SetTitle(i18n.T(lang, "menu.quit"))
	refreshApprovalMenuLocked()
	refreshUpdateMenuLocked()
	for l, it := range langItems {
		if l == lang {
			it.Check()
		} else {
			it.Uncheck()
		}
	}
}

func infoRow(title string) *systray.MenuItem {
	it := systray.AddMenuItem(title, "")
	it.Disable()
	return it
}

func poll() {
	s := fetch()
	pending, approvalErr := approvalManager.Pending()
	mu.Lock()
	var icon []byte
	var word string
	switch {
	case !s.reachable:
		icon, word = dotIcon(gray), i18n.T(lang, "health.unreachable")
	case !s.Qdrant.Up || !s.Ollama.Up:
		icon, word = dotIcon(red), i18n.T(lang, "health.down")
	case len(s.Issues) > 0:
		icon, word = dotIcon(orange), i18n.T(lang, "health.warn")
	default:
		icon, word = dotIcon(green), i18n.T(lang, "health.ok")
	}
	systray.SetIcon(icon)
	systray.SetTooltip("Ariadne " + version.Tag + " — " + word)
	rowVersion.SetTitle("Ariadne " + version.Tag)
	rowHealth.SetTitle("ariadne — " + word)
	rowQdrant.SetTitle(serviceRow("Qdrant", upWord(s.Qdrant.Up), s.Qdrant.PID, s.Qdrant.RSSMB))
	rowOllama.SetTitle(serviceRow("Ollama", upVer(s.Ollama), s.Ollama.PID, s.Ollama.RSSMB))
	rowPoints.SetTitle(fmt.Sprintf("%s: %s (%s)", i18n.T(lang, "row.records"), grouped(s.Collection.Points), s.Collection.Status))
	totals := s.TokenMetrics.AllTime
	rowTokens.SetTitle(fmt.Sprintf("%s: ~%s", i18n.T(lang, "row.context_saved"), grouped(totals.ConfirmedSavedTokens)))
	rowCoverage.SetTitle(fmt.Sprintf("%s: %.1f%% · %s: %s", i18n.T(lang, "row.metrics_coverage"),
		totals.AttributionPercent, i18n.T(lang, "row.recalls"), grouped(totals.Recalls)))
	rowUnattributed.SetTitle(fmt.Sprintf("%s: ~%s", i18n.T(lang, "row.unattributed"), grouped(totals.UnattributedTokens)))
	rowDisk.SetTitle(fmt.Sprintf("%s: %dMB · backups %dMB · logs %dMB · %s %dGB",
		i18n.T(lang, "row.data"), s.DataMB, s.BackupsMB, s.LogsMB, i18n.T(lang, "row.free"), s.FreeGB))
	if event, ok := latestTrayMaintenanceEvent(s.Maintenance); ok {
		rowMaintenance.SetTitle(fmt.Sprintf("%s: %s · %s", i18n.T(lang, "row.maintenance"),
			event.At.Local().Format("2006-01-02 15:04"), event.Status))
	} else {
		rowMaintenance.SetTitle(fmt.Sprintf("%s: %s", i18n.T(lang, "row.maintenance"), i18n.T(lang, "row.never")))
	}
	var approvalNotice, approvalPrompt *approval.Request
	if approvalErr == nil && len(pending) > 0 {
		currentApproval = pending[0]
		hasCurrentApproval = true
		currentApprovalCount = len(pending)
		if currentApproval.ID != lastNotifiedApprovalID {
			request := currentApproval
			approvalNotice = &request
			lastNotifiedApprovalID = currentApproval.ID
		}
		if approvalPrompts.begin(currentApproval.ID, time.Now()) {
			request := currentApproval
			approvalPrompt = &request
		}
	} else {
		currentApproval = approval.Request{}
		hasCurrentApproval = false
		currentApprovalCount = 0
	}
	refreshApprovalMenuLocked()

	// notify only when a NEW issue appears (or a service just dropped)
	if s.reachable && len(s.Issues) > 0 && !slices.Equal(s.Issues, lastIssues) {
		notify("⚠️ ariadne", strings.Join(s.Issues, " · "))
	}
	lastIssues = s.Issues
	activeLang := lang
	mu.Unlock()
	if approvalNotice != nil {
		notify(i18n.T(activeLang, "notify.approval"), approvalNotification(activeLang, *approvalNotice))
	}
	if approvalPrompt != nil {
		go runApprovalSystemPrompt(*approvalPrompt, activeLang)
	}
}

func latestTrayMaintenanceEvent(events map[string]activity.Event) (activity.Event, bool) {
	var latest activity.Event
	found := false
	for _, operation := range []string{"maintenance", "memfile_sync", "consolidate"} {
		event, ok := events[operation]
		if !ok || event.At.IsZero() {
			continue
		}
		if !found || event.At.After(latest.At) {
			latest = event
			found = true
		}
	}
	return latest, found
}

func refreshMaintenanceMenuLocked() {
	if mMaintenance == nil {
		return
	}
	if maintenanceRunning {
		mMaintenance.SetTitle(i18n.T(lang, "menu.maintenance_running"))
		mMaintenance.Disable()
		return
	}
	mMaintenance.SetTitle(i18n.T(lang, "menu.maintenance"))
	if serviceActionRunning {
		mMaintenance.Disable()
		return
	}
	mMaintenance.Enable()
}

func refreshServiceMenusLocked() {
	if mStart == nil || mStop == nil || mRestart == nil {
		return
	}
	mStart.SetTitle(i18n.T(lang, "menu.start"))
	mStop.SetTitle(i18n.T(lang, "menu.stop"))
	mRestart.SetTitle(i18n.T(lang, "menu.restart"))
	busy := serviceActionRunning || maintenanceRunning || updates.installing
	items := []*systray.MenuItem{mStart, mStop, mRestart, mBackup, mExport}
	for _, item := range items {
		if item == nil {
			continue
		}
		if busy {
			item.Disable()
		} else {
			item.Enable()
		}
	}
}

func refreshApprovalMenuLocked() {
	if rowApproval == nil || mApprove == nil || mDeny == nil {
		return
	}
	if !hasCurrentApproval {
		rowApproval.SetTitle(i18n.T(lang, "row.approvals") + ": " + i18n.T(lang, "approval.none"))
		rowApprovalDetail.SetTitle("")
		mApprove.SetTitle(i18n.T(lang, "menu.approve"))
		mDeny.SetTitle(i18n.T(lang, "menu.deny"))
		mApprove.Disable()
		mDeny.Disable()
		return
	}
	kind := approvalKindTitle(lang, currentApproval.Kind)
	count := ""
	if currentApprovalCount > 1 {
		count = fmt.Sprintf(" (+%d)", currentApprovalCount-1)
	}
	rowApproval.SetTitle(i18n.T(lang, "row.approvals") + ": " + kind + count)
	rowApprovalDetail.SetTitle(shortApprovalDetail(currentApproval, 96))
	mApprove.SetTitle(i18n.T(lang, "menu.approve"))
	mDeny.SetTitle(i18n.T(lang, "menu.deny"))
	mApprove.Enable()
	mDeny.Enable()
}

func decideApproval(approved bool) {
	mu.Lock()
	if !hasCurrentApproval {
		mu.Unlock()
		return
	}
	id := currentApproval.ID
	activeLang := lang
	mu.Unlock()
	decideApprovalByID(id, approved, activeLang)
}

func decideApprovalByID(id string, approved bool, activeLang i18n.Lang) {
	_, err := approvalManager.Decide(id, approved)
	message := i18n.T(activeLang, "notify.done")
	if !approved {
		message = i18n.T(activeLang, "menu.deny")
	}
	if err != nil {
		message = i18n.T(activeLang, "notify.failed") + ": " + err.Error()
	}
	notify(i18n.T(activeLang, "notify.approval"), message)
	poll()
}

func approvalKindTitle(l i18n.Lang, kind approval.Kind) string {
	if kind == approval.KindCredential {
		return i18n.T(l, "approval.protected")
	}
	return i18n.T(l, "approval.cross")
}

func approvalNotification(l i18n.Lang, request approval.Request) string {
	return approvalKindTitle(l, request.Kind) + ": " + shortApprovalDetail(request, 140)
}

func shortApprovalDetail(request approval.Request, limit int) string {
	var detail string
	if request.Kind == approval.KindCredential {
		detail = request.SourceWing + " → " + request.TargetWing + " · " + request.Resource + " · " + request.Purpose
	} else {
		detail = request.ActiveWing + " → * · " + request.Purpose + " · " + request.Query
	}
	detail = strings.Join(strings.Fields(detail), " ")
	runes := []rune(detail)
	if len(runes) <= limit {
		return detail
	}
	return string(runes[:limit-1]) + "…"
}

func maintenanceClicked() {
	mu.Lock()
	if maintenanceRunning || serviceActionRunning {
		mu.Unlock()
		return
	}
	maintenanceRunning = true
	activeLang := lang
	refreshMaintenanceMenuLocked()
	refreshServiceMenusLocked()
	mu.Unlock()

	notify("ariadne", i18n.T(activeLang, "notify.maintenance")+": "+i18n.T(activeLang, "notify.started"))
	ctx, cancel := context.WithTimeout(context.Background(), manualMaintenanceTimeout)
	err := runTrayMaintenance(ctx)
	cancel()

	mu.Lock()
	maintenanceRunning = false
	activeLang = lang
	refreshMaintenanceMenuLocked()
	refreshServiceMenusLocked()
	mu.Unlock()
	result := i18n.T(activeLang, "notify.done")
	if err != nil {
		result = i18n.T(activeLang, "notify.failed")
	}
	notify("ariadne", i18n.T(activeLang, "notify.maintenance")+": "+result)
	poll()
}

func runTrayMaintenance(ctx context.Context) error {
	logDir := runtimeDir("logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil { //nolint:gosec // user-owned runtime directory
		return err
	}
	logFile, err := os.OpenFile( //nolint:gosec // fixed user-owned runtime log
		filepath.Join(logDir, "maintenance.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600,
	)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	cmd := exec.CommandContext(ctx, ctlPath(), "maintenance") //nolint:gosec // our own binary, fixed action
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd.Run()
}

func fetch() status {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _ := fetchStatus(ctx)
	return s
}

// ctl runs an ariadnectl action; a non-empty banner posts a completion notice.
func ctl(action, banner string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, ctlPath(), action).Run() //nolint:gosec // our own binary, fixed action set
	if banner != "" {
		result := i18n.T(lang, "notify.done")
		if err != nil {
			result = i18n.T(lang, "notify.failed")
		}
		notify("ariadne", banner+": "+result)
	}
	poll()
	return err
}

// relaunchTray starts the installed tray binary and closes this process. This
// matters after an update: restarting the managed services alone leaves the old
// UI code resident in memory until the tray itself is replaced.
func relaunchTray() error {
	if supervisedTray(runtime.GOOS, os.Getenv("XPC_SERVICE_NAME")) {
		// The launchd job has KeepAlive.SuccessfulExit=false. Exit with a
		// deliberate non-zero status so launchd starts one replacement only
		// after this status item has fully left the menu bar. Starting a second
		// Cocoa tray before quitting this one races and can leave no icon.
		restartTrayThroughSupervisor("launchd tray restart requested")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve tray executable: %w", err)
	}
	cmd := exec.CommandContext(context.Background(), exe) //nolint:gosec // current trusted tray executable
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tray: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach tray: %w", err)
	}
	quitTray("tray relaunched")
	return nil
}

func supervisedTray(platform, serviceName string) bool {
	return platform == osDarwin &&
		(serviceName == "com.ariadne.tray" || strings.HasPrefix(serviceName, "com.ariadne.tray."))
}

func openPath(p string) {
	opener := "xdg-open"
	switch runtime.GOOS {
	case osDarwin:
		opener = "open"
	case osWindows:
		opener = "explorer"
	}
	_ = exec.CommandContext(context.Background(), opener, p).Start() //nolint:gosec // fixed opener, our own path
}

func notify(title, msg string) {
	_ = notificationCommand(context.Background(), title, msg).Start()
}

func notifySync(title, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return notificationCommand(ctx, title, msg).Run()
}

func notificationCommand(ctx context.Context, title, msg string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		script := "display notification " + qq(msg) + " with title " + qq(title)
		return exec.CommandContext(ctx, "osascript", "-e", script) //nolint:gosec // fixed argv
	}
	return exec.CommandContext(ctx, "notify-send", title, msg) //nolint:gosec // fixed argv, our own text
}

// --- helpers ---

var (
	green  = color.RGBA{0x2e, 0xcc, 0x71, 0xff}
	orange = color.RGBA{0xf3, 0x9c, 0x12, 0xff}
	red    = color.RGBA{0xe7, 0x4c, 0x3c, 0xff}
	gray   = color.RGBA{0x95, 0xa5, 0xa6, 0xff}
)

// dotIcon draws an anti-aliased filled circle PNG of the given colour, with a
// faint top highlight for a bit of depth — no asset files.
func dotIcon(c color.RGBA) []byte {
	const n = 64
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cx, cy := float64(n-1)/2, float64(n-1)/2
	r := float64(n)/2 - 3
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			cov := r + 0.5 - math.Hypot(dx, dy) // edge coverage → smooth border
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			hi := 1 + 0.15*(-dy/r) // subtle brighten toward the top, like the emoji sheen
			img.SetRGBA(x, y, color.RGBA{shade(c.R, hi), shade(c.G, hi), shade(c.B, hi), uint8(cov * 255)})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// shade multiplies a colour channel by f, clamped to [0,255].
func shade(v uint8, f float64) uint8 {
	switch x := float64(v) * f; {
	case x > 255:
		return 255
	case x < 0:
		return 0
	default:
		return uint8(x)
	}
}

func upWord(up bool) string {
	if up {
		return i18n.T(lang, "status.up")
	}
	return i18n.T(lang, "status.down")
}

func upVer(o svc) string {
	if !o.Up {
		return i18n.T(lang, "status.down")
	}
	if o.Version != "" {
		return i18n.T(lang, "status.up") + " " + o.Version
	}
	return i18n.T(lang, "status.up")
}

func serviceRow(name, state string, processID int, rssMB int64) string {
	if processID > 0 {
		return fmt.Sprintf("%s: %s · PID %d · %dMB", name, state, processID, rssMB)
	}
	return fmt.Sprintf("%s: %s · %dMB", name, state, rssMB)
}

// grouped formats an int with thin-space thousands separators.
func grouped(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return s
	}
	var out []byte
	for i, d := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, d)
	}
	return string(out)
}

func qq(s string) string {
	out := []byte{'"'}
	for _, r := range []byte(s) {
		if r == '"' {
			r = '\''
		}
		out = append(out, r)
	}
	return string(append(out, '"'))
}
