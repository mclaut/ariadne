//go:build !windows

package main

import (
	"ariadne/internal/qdrantauth"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseInstallQdrantAgents(t *testing.T) {
	t.Parallel()
	output := `- 101 com.ariadne.qdrant
2003 0 com.ariadne.qdrant.scoped-v2
2004 0 com.example.qdrant`
	want := []string{"com.ariadne.qdrant", "com.ariadne.qdrant.scoped-v2"}
	if got := parseInstallQdrantAgents(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}
}

func TestParseInstallLaunchAgentsScopesByCanonicalPrefix(t *testing.T) {
	t.Parallel()
	output := `101 0 com.ariadne.tray.v0-8-7
102 0 com.ariadne.tray
103 0 com.ariadne.tray-helper
104 0 com.ariadne.sync.v0-8-7
101 0 com.ariadne.tray.v0-8-7`
	want := []string{"com.ariadne.tray", "com.ariadne.tray.v0-8-7"}
	if got := parseInstallLaunchAgents(output, "com.ariadne.tray"); !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}
}

func TestArchiveLegacyLaunchAgentPlistsPreservesCanonicalAndCollisions(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	archive := filepath.Join(home, ".ariadne", "archive", "launchagents")
	if err := os.MkdirAll(agents, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"com.ariadne.tray.plist":        "canonical",
		"com.ariadne.tray.v0-8-9.plist": "legacy-current",
		"com.example.tray.plist":        "unrelated",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(agents, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(archive, "com.ariadne.tray.v0-8-9.plist"), []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := archiveLegacyLaunchAgentPlists(home, "com.ariadne.tray"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"com.ariadne.tray.plist", "com.example.tray.plist"} {
		if _, err := os.Stat(filepath.Join(agents, name)); err != nil {
			t.Fatalf("preserved %s: %v", name, err)
		}
	}
	for name, want := range map[string]string{
		"com.ariadne.tray.v0-8-9.plist":   "older",
		"com.ariadne.tray.v0-8-9.1.plist": "legacy-current",
	} {
		got, err := os.ReadFile(filepath.Join(archive, name)) //nolint:gosec // test fixture
		if err != nil || string(got) != want {
			t.Fatalf("archive %s = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestQdrantLaunchdTemplateRaisesDescriptorLimit(t *testing.T) {
	t.Parallel()
	template, err := os.ReadFile(filepath.Join("..", "..", "deploy", "com.ariadne.qdrant.plist")) //nolint:gosec // repo fixture
	if err != nil {
		t.Fatal(err)
	}
	text := string(template)
	if !strings.Contains(text, "<key>SoftResourceLimits</key>") ||
		!strings.Contains(text, "<key>NumberOfFiles</key><integer>8192</integer>") {
		t.Fatal("Qdrant launchd template does not set the required descriptor limit")
	}
}

func TestClientRuntimeEnvPropagatesRemoteQdrantWithoutKeyValue(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "qdrant key")
	if err := os.WriteFile(keyFile, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qdrantauth.EnvAPIKeyFile, keyFile)
	t.Setenv(qdrantauth.EnvAPIKey, "")
	t.Setenv(qdrantauth.EnvTLS, "1")
	t.Setenv(qdrantauth.EnvAllowInsecureRemote, "")
	env := clientRuntimeEnv(opts{
		qdrantHost: "qdrant.example", qdrantREST: 7443, qdrantGRPC: 7444,
		ollamaURL: "http://localhost:11434", model: "bge-m3", collection: "ariadne",
	})
	if env[qdrantauth.EnvAPIKeyFile] != keyFile || env[qdrantauth.EnvTLS] != "1" ||
		env["ARIADNE_QDRANT_REST"] != "https://qdrant.example:7443" ||
		env["ARIADNE_QDRANT_PORT"] != "7444" {
		t.Fatalf("runtime env = %#v", env)
	}
	for _, value := range env {
		if value == "test-key" {
			t.Fatal("resolved API key leaked into runtime environment")
		}
	}

	plist := renderPlistTemplate(`<string>__HOME__</string><!-- __ARIADNE_ENV__ -->`, "/home/a&b", env)
	if !strings.Contains(plist, "/home/a&amp;b") || !strings.Contains(plist, "ARIADNE_QDRANT_API_KEY_FILE") ||
		strings.Contains(plist, ">test-key<") {
		t.Fatalf("rendered plist = %s", plist)
	}
	if got := systemdEnvironment(env); !strings.Contains(got, `Environment="ARIADNE_QDRANT_TLS=1"`) {
		t.Fatalf("systemd environment = %s", got)
	}
	if got := desktopEnvironment(env); !strings.HasPrefix(got, `env "ARIADNE_QDRANT_API_KEY_FILE=`) {
		t.Fatalf("desktop environment = %s", got)
	}
}

func TestQdrantBaseURLSupportsTLSAndIPv6Loopback(t *testing.T) {
	t.Setenv("ARIADNE_QDRANT_TLS", "1")
	t.Setenv("ARIADNE_QDRANT_API_KEY", "")
	t.Setenv("ARIADNE_QDRANT_API_KEY_FILE", "")
	o := opts{qdrantHost: "::1", qdrantREST: 6333}
	if got := qdrantBaseURL(o); got != "https://[::1]:6333" {
		t.Fatalf("base URL = %q", got)
	}
}
