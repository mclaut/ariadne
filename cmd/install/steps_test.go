//go:build !windows

package main

import (
	"reflect"
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
