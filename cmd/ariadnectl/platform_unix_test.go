//go:build !windows

package main

import (
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
