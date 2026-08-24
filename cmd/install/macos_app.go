//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type commandRunner func(string, ...string) error

func macApplicationPath(home string) string {
	if directoryWritableByCurrentUser("/Applications") {
		return "/Applications/Ariadne.app"
	}
	return filepath.Join(home, "Applications", "Ariadne.app")
}

func directoryWritableByCurrentUser(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	mode := info.Mode().Perm()
	if uint32(os.Geteuid()) == stat.Uid { //nolint:gosec // Unix effective IDs fit the platform Stat_t field
		return mode&0o300 == 0o300
	}
	groups, _ := os.Getgroups()
	for _, group := range groups {
		if uint32(group) == stat.Gid { //nolint:gosec // Unix group IDs fit the platform Stat_t field
			return mode&0o030 == 0o030
		}
	}
	return mode&0o003 == 0o003
}

func writeMacApplicationBundle(app, launcher, release string, run commandRunner) error {
	macOSDir := filepath.Join(app, "Contents", "MacOS")
	resources := filepath.Join(app, "Contents", "Resources")
	for _, dir := range []string{macOSDir, resources} {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-owned application bundle
			return err
		}
	}

	if err := copyExecutable(launcher, filepath.Join(macOSDir, "Ariadne")); err != nil {
		return fmt.Errorf("install Ariadne.app launcher: %w", err)
	}
	if err := os.WriteFile( //nolint:gosec // user-owned application metadata
		filepath.Join(app, "Contents", "Info.plist"), []byte(macApplicationPlist(release)), 0o644,
	); err != nil {
		return err
	}
	if err := writeMacApplicationIcon(resources); err != nil {
		return fmt.Errorf("install Ariadne.app icon: %w", err)
	}
	if err := run("codesign", "--force", "--deep", "--sign", "-", app); err != nil {
		return fmt.Errorf("sign Ariadne.app: %w", err)
	}

	// Ask LaunchServices to notice the user-level application immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "touch", app).Run() //nolint:gosec // fixed system command and installer-owned path
	return nil
}

func copyExecutable(source, destination string) error {
	src, err := os.Open(source) //nolint:gosec // installer-owned runtime binary
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // app executable
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func macApplicationPlist(release string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleDisplayName</key><string>Ariadne</string>
  <key>CFBundleExecutable</key><string>Ariadne</string>
  <key>CFBundleIconFile</key><string>Ariadne.icns</string>
  <key>CFBundleIdentifier</key><string>io.github.mclaut.ariadne</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Ariadne</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>` + release + `</string>
  <key>CFBundleVersion</key><string>` + release + `</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
`
}

func writeMacApplicationIcon(resources string) error {
	// Modern macOS still reads the classic ICNS container, but iconutil no
	// longer reliably compiles a complete legacy iconset. ICNS PNG elements are
	// simple and stable, so emit them directly without an Xcode dependency.
	elements := []struct {
		kind string
		size int
	}{
		{kind: "icp4", size: 16},
		{kind: "icp5", size: 32},
		{kind: "icp6", size: 64},
		{kind: "ic07", size: 128},
		{kind: "ic08", size: 256},
		{kind: "ic09", size: 512},
		{kind: "ic10", size: 1024},
	}
	var body bytes.Buffer
	for _, element := range elements {
		var payload bytes.Buffer
		if err := png.Encode(&payload, renderAriadneIcon(element.size)); err != nil {
			return err
		}
		body.WriteString(element.kind)
		if err := binary.Write(&body, binary.BigEndian, uint32(payload.Len()+8)); err != nil { //nolint:gosec // ICNS format length
			return err
		}
		_, _ = body.Write(payload.Bytes())
	}
	var icon bytes.Buffer
	icon.WriteString("icns")
	if err := binary.Write(&icon, binary.BigEndian, uint32(body.Len()+8)); err != nil { //nolint:gosec // ICNS format length
		return err
	}
	_, _ = icon.Write(body.Bytes())
	return os.WriteFile(filepath.Join(resources, "Ariadne.icns"), icon.Bytes(), 0o644) //nolint:gosec // app icon resource
}

func renderAriadneIcon(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	dark := color.NRGBA{R: 0x11, G: 0x12, B: 0x0f, A: 0xff}
	green := color.NRGBA{R: 0x8c, G: 0xff, B: 0x3f, A: 0xff}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	round := float64(size) / 8
	center := float64(size) / 2
	outer := float64(size) * 16 / 64
	inner := float64(size) * 12 / 64
	for y := range size {
		for x := range size {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if insideRoundedSquare(fx, fy, float64(size), round) {
				img.SetNRGBA(x, y, dark)
			}
			dx, dy := fx-center, fy-center
			distance2 := dx*dx + dy*dy
			if distance2 <= outer*outer {
				img.SetNRGBA(x, y, white)
			}
			if distance2 <= inner*inner {
				img.SetNRGBA(x, y, green)
			}
		}
	}
	return img
}

func insideRoundedSquare(x, y, size, radius float64) bool {
	cx := min(max(x, radius), size-radius)
	cy := min(max(y, radius), size-radius)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}
