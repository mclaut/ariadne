// ariadne-tray is a system-tray monitor for the ariadne stack (Qdrant + Ollama
// + the ariadne MCP server) — the Linux/Windows counterpart of the macOS Swift
// menu-bar app. Thin viewer: it shells `ariadnectl status -json` and renders;
// all logic lives in the Go core. Cross-platform via fyne.io/systray.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"fyne.io/systray"
)

const pollEvery = 5 * time.Second

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
	reachable  bool
	OK         bool     `json:"ok"`
	Qdrant     svc      `json:"qdrant"`
	Ollama     svc      `json:"ollama"`
	Collection coll     `json:"collection"`
	DataMB     int64    `json:"data_mb"`
	FreeGB     int64    `json:"free_gb"`
	Issues     []string `json:"issues"`
}

var (
	rowHealth, rowQdrant, rowOllama, rowPoints, rowDisk *systray.MenuItem
	lastIssues                                          []string
)

func main() {
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetIcon(dotIcon(gray))
	systray.SetTitle("ariadne")
	systray.SetTooltip("ariadne monitor")

	rowHealth = infoRow("…")
	rowQdrant = infoRow("")
	rowOllama = infoRow("")
	rowPoints = infoRow("")
	rowDisk = infoRow("")
	systray.AddSeparator()
	mStart := systray.AddMenuItem("▶ Старт", "start the stack")
	mStop := systray.AddMenuItem("■ Стоп", "stop the stack")
	mRestart := systray.AddMenuItem("⟳ Рестарт", "restart the stack")
	systray.AddSeparator()
	mBackup := systray.AddMenuItem("💾 Бекап зараз", "snapshot the collection")
	mExport := systray.AddMenuItem("⬇ Експорт (JSONL)", "portable export")
	mData := systray.AddMenuItem("Показати бекапи/дані", "open the data dir")
	mLogs := systray.AddMenuItem("Показати логи", "open the logs dir")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Вийти", "quit the monitor")

	go poll()
	go func() {
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
			case <-mBackup.ClickedCh:
				ctl("backup", "Бекап")
			case <-mExport.ClickedCh:
				ctl("export", "Експорт")
			case <-mData.ClickedCh:
				openPath(runtimeDir("backups"))
			case <-mLogs.ClickedCh:
				openPath(runtimeDir("logs"))
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func infoRow(title string) *systray.MenuItem {
	it := systray.AddMenuItem(title, "")
	it.Disable()
	return it
}

func poll() {
	s := fetch()
	var icon []byte
	var word string
	switch {
	case !s.reachable:
		icon, word = dotIcon(gray), "ariadnectl недоступний"
	case !s.Qdrant.Up || !s.Ollama.Up:
		icon, word = dotIcon(red), "сервіс впав"
	case len(s.Issues) > 0:
		icon, word = dotIcon(orange), "увага"
	default:
		icon, word = dotIcon(green), "OK"
	}
	systray.SetIcon(icon)
	systray.SetTooltip("ariadne — " + word)
	rowHealth.SetTitle("ariadne — " + word)
	rowQdrant.SetTitle(fmt.Sprintf("Qdrant: %s · %dMB", upWord(s.Qdrant.Up), s.Qdrant.RSSMB))
	rowOllama.SetTitle(fmt.Sprintf("Ollama: %s · %dMB", upVer(s.Ollama), s.Ollama.RSSMB))
	rowPoints.SetTitle(fmt.Sprintf("Записів: %s (%s)", grouped(s.Collection.Points), s.Collection.Status))
	rowDisk.SetTitle(fmt.Sprintf("Дані: %dMB · вільно %dGB", s.DataMB, s.FreeGB))

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
		if err == nil {
			notify("ariadne", banner+": готово ✅")
		} else {
			notify("ariadne", banner+": помилка")
		}
	}
	poll()
}

func openPath(p string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
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

// dotIcon draws a filled circle PNG of the given colour — no asset files.
func dotIcon(c color.RGBA) []byte {
	const n = 64
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cx, cy, r2 := float64(n)/2, float64(n)/2, float64(n/2-4)*float64(n/2-4)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r2 {
				img.Set(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func upWord(up bool) string {
	if up {
		return "up"
	}
	return "DOWN"
}

func upVer(o svc) string {
	if !o.Up {
		return "DOWN"
	}
	if o.Version != "" {
		return "up " + o.Version
	}
	return "up"
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
