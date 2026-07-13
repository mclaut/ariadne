// ariadne-tray is the system-tray monitor for the ariadne stack (Qdrant + Ollama
// + the ariadne MCP server) on macOS, Linux and Windows. Thin viewer: it shells
// `ariadnectl status -json` and renders; all logic lives in the Go core.
// Cross-platform via fyne.io/systray. The UI is localized (internal/i18n) with a
// live Language switcher.
package main

import (
	"ariadne/internal/i18n"
	"ariadne/internal/metrics"
	"ariadne/internal/version"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	pollEvery = 5 * time.Second
	osDarwin  = "darwin"
	osLinux   = "linux"
	osWindows = "windows"
)

func ctlPath() string {
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
	RSSMB   int64  `json:"rss_mb"`
	Version string `json:"version"`
}

type coll struct {
	Points int64  `json:"points"`
	Status string `json:"status"`
}

type status struct {
	reachable    bool
	OK           bool            `json:"ok"`
	Qdrant       svc             `json:"qdrant"`
	Ollama       svc             `json:"ollama"`
	Collection   coll            `json:"collection"`
	TokenMetrics metrics.Summary `json:"token_metrics"`
	DataMB       int64           `json:"data_mb"`
	FreeGB       int64           `json:"free_gb"`
	Issues       []string        `json:"issues"`
}

var (
	mu         sync.Mutex // serializes UI updates across poll/switch goroutines
	lang       i18n.Lang
	lastIssues []string

	rowVersion, rowHealth, rowQdrant, rowOllama, rowPoints, rowTokens, rowDisk *systray.MenuItem
	mUpdate, mStart, mStop, mRestart, mBackup, mExport, mData, mLogs, mLang    *systray.MenuItem
	mQuit                                                                      *systray.MenuItem
	langItems                                                                  map[i18n.Lang]*systray.MenuItem
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--apply-update" {
		os.Exit(applyUpdate(os.Args[2], os.Args[3]))
	}
	systray.Run(onReady, func() {})
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
	rowDisk = infoRow("")
	mUpdate = systray.AddMenuItem("", "")
	systray.AddSeparator()
	mStart = systray.AddMenuItem("", "")
	mStop = systray.AddMenuItem("", "")
	mRestart = systray.AddMenuItem("", "")
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
			ctl("start", "")
		case <-mStop.ClickedCh:
			ctl("stop", "")
		case <-mRestart.ClickedCh:
			ctl("restart", "")
		case <-mUpdate.ClickedCh:
			go updateClicked()
		case <-mBackup.ClickedCh:
			ctl("backup", i18n.T(lang, "notify.backup"))
		case <-mExport.ClickedCh:
			ctl("export", i18n.T(lang, "notify.export"))
		case <-mData.ClickedCh:
			openPath(runtimeDir("backups"))
		case <-mLogs.ClickedCh:
			openPath(runtimeDir("logs"))
		case <-mQuit.ClickedCh:
			systray.Quit()
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
	mStart.SetTitle(i18n.T(lang, "menu.start"))
	mStop.SetTitle(i18n.T(lang, "menu.stop"))
	mRestart.SetTitle(i18n.T(lang, "menu.restart"))
	mBackup.SetTitle(i18n.T(lang, "menu.backup"))
	mExport.SetTitle(i18n.T(lang, "menu.export"))
	mData.SetTitle(i18n.T(lang, "menu.data"))
	mLogs.SetTitle(i18n.T(lang, "menu.logs"))
	mLang.SetTitle("🌐 " + i18n.T(lang, "menu.language") + ": " + i18n.Name[lang])
	mQuit.SetTitle(i18n.T(lang, "menu.quit"))
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
	mu.Lock()
	defer mu.Unlock()
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
	rowQdrant.SetTitle(fmt.Sprintf("Qdrant: %s · %dMB", upWord(s.Qdrant.Up), s.Qdrant.RSSMB))
	rowOllama.SetTitle(fmt.Sprintf("Ollama: %s · %dMB", upVer(s.Ollama), s.Ollama.RSSMB))
	rowPoints.SetTitle(fmt.Sprintf("%s: %s (%s)", i18n.T(lang, "row.records"), grouped(s.Collection.Points), s.Collection.Status))
	savedTokens := s.TokenMetrics.AllTime.ConfirmedSavedTokens
	rowTokens.SetTitle(fmt.Sprintf("%s: ~%s", i18n.T(lang, "row.context_saved"), grouped(savedTokens)))
	rowDisk.SetTitle(fmt.Sprintf("%s: %dMB · %s %dGB", i18n.T(lang, "row.data"), s.DataMB, i18n.T(lang, "row.free"), s.FreeGB))

	// notify only when a NEW issue appears (or a service just dropped)
	if s.reachable && len(s.Issues) > 0 && !slices.Equal(s.Issues, lastIssues) {
		notify("⚠️ ariadne", strings.Join(s.Issues, " · "))
	}
	lastIssues = s.Issues
}

func fetch() status {
	var s status
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ctlPath(), "status", "-json").Output() //nolint:gosec // our own binary under $HOME
	if err != nil {
		return s
	}
	if json.Unmarshal(out, &s) != nil {
		return s
	}
	s.reachable = true
	return s
}

// ctl runs an ariadnectl action; a non-empty banner posts a completion notice.
func ctl(action, banner string) {
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
	ctx := context.Background()
	if runtime.GOOS == "darwin" {
		script := "display notification " + qq(msg) + " with title " + qq(title)
		_ = exec.CommandContext(ctx, "osascript", "-e", script).Start() //nolint:gosec // fixed argv
		return
	}
	_ = exec.CommandContext(ctx, "notify-send", title, msg).Start() //nolint:gosec // fixed argv, our own text
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
