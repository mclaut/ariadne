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
