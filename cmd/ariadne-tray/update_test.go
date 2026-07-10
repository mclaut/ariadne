package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		current   string
		candidate string
		want      bool
	}{
		{"0.2.0", "v0.2.1", true},
		{"0.2.0", "v0.3.0", true},
		{"0.2.0", "v1.0.0", true},
		{"0.2.0", "v0.2.0", false},
		{"0.2.0", "v0.1.9", false},
		{"0.2.0", "not-a-version", false},
		{"0.2.0", "v0.2.1-beta.1", false},
	}
	for _, tc := range cases {
		if got := isNewerVersion(tc.current, tc.candidate); got != tc.want {
			t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("missing User-Agent")
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0","html_url":"https://github.com/mclaut/ariadne/releases/tag/v0.3.0"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := fetchLatestRelease(ctx, srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v0.3.0" || release.HTMLURL != "https://github.com/mclaut/ariadne/releases/tag/v0.3.0" {
		t.Fatalf("release = %#v", release)
	}
}

func TestDownloadInstallScript(t *testing.T) {
	script := "#!/bin/sh\nREPO=\"mclaut/ariadne\"\necho ok\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(script))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	path, err := downloadInstaller(ctx, srv.Client(), srv.URL, t.TempDir(), "install.sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	b, err := os.ReadFile(path) //nolint:gosec // path was created by the function under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != script {
		t.Fatalf("script = %q", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != osWindows && info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestDownloadInstallScriptRejectsUnexpectedFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not an installer"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := downloadInstaller(ctx, srv.Client(), srv.URL, t.TempDir(), "install.sh"); err == nil {
		t.Fatal("unexpected file was accepted")
	}
}

func TestDownloadPowerShellInstaller(t *testing.T) {
	script := "# Ariadne Windows installer\n$Repository = \"mclaut/ariadne\"\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(script))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	path, err := downloadInstaller(ctx, srv.Client(), srv.URL, t.TempDir(), "install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".ps1" {
		t.Fatalf("installer extension = %q", filepath.Ext(path))
	}
}

func TestPathWithin(t *testing.T) {
	base := t.TempDir()
	if !pathWithin(base, filepath.Join(base, "update-install-1.sh")) {
		t.Fatal("child path should be accepted")
	}
	if pathWithin(base, filepath.Join(base, "..", "outside.sh")) {
		t.Fatal("parent path should be rejected")
	}
}

func TestValidUpdateScriptRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	runtimeRoot := filepath.Join(home, ".ariadne")
	if err := os.MkdirAll(runtimeRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	realScript := filepath.Join(runtimeRoot, "update-install-real.sh")
	if err := os.WriteFile(realScript, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !validUpdateScript(realScript) {
		t.Fatal("regular update script should be accepted")
	}
	symlink := filepath.Join(runtimeRoot, "update-install-link.sh")
	if err := os.Symlink(realScript, symlink); err != nil {
		if runtime.GOOS == osWindows {
			t.Skipf("Windows symlink creation is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if validUpdateScript(symlink) {
		t.Fatal("symlinked update script should be rejected")
	}
}
