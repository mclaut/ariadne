//go:build !windows

package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseAriadneQdrantAgents(t *testing.T) {
	t.Parallel()
	output := `- 0 com.ariadne.tray.v0-8-0
- 101 com.ariadne.qdrant
2003 0 com.ariadne.qdrant.scoped-v2
2004 0 com.example.qdrant
2003 0 com.ariadne.qdrant.scoped-v2`
	want := []string{"com.ariadne.qdrant", "com.ariadne.qdrant.scoped-v2"}
	if got := parseAriadneQdrantAgents(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}
}

func TestControlReturnsServiceCommandFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := control("start"); err == nil {
		t.Fatal("control reported success when the platform service command was unavailable")
	}
}

func TestParseProcessPID(t *testing.T) {
	t.Parallel()
	output := `  120 /usr/bin/unrelated
  321 /runtime/.ariadne/bin/qdrant
  654 /opt/homebrew/opt/ollama/bin/ollama serve
  999 ariadnectl status -json`
	if got := parseProcessPID(output, ".ariadne/bin/qdrant", "/bin/qdrant"); got != 321 {
		t.Fatalf("qdrant pid = %d, want 321", got)
	}
	if got := parseProcessPID(output, "/ollama serve"); got != 654 {
		t.Fatalf("ollama pid = %d, want 654", got)
	}
	if got := parseProcessPID(output, "missing"); got != 0 {
		t.Fatalf("missing pid = %d, want 0", got)
	}
}

func TestResolveServiceBinaryUsesHomebrewFallbackOutsideShellPATH(t *testing.T) {
	t.Parallel()
	notFound := func(string) (string, error) { return "", errors.New("not found") }
	executable := func(path string) bool { return path == "/opt/homebrew/bin/brew" }
	if got := resolveServiceBinary("brew", notFound, executable); got != "/opt/homebrew/bin/brew" {
		t.Fatalf("resolved brew = %q, want Apple Silicon Homebrew path", got)
	}
}

func TestResolveServiceBinaryPrefersPATH(t *testing.T) {
	t.Parallel()
	lookPath := func(string) (string, error) { return "/custom/bin/brew", nil }
	if got := resolveServiceBinary("brew", lookPath, func(string) bool { return false }); got != "/custom/bin/brew" {
		t.Fatalf("resolved brew = %q, want PATH result", got)
	}
}
