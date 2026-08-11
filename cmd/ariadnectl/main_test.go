package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRestartServicesAlwaysAttemptsRecoveryStart(t *testing.T) {
	t.Parallel()
	stopErr := errors.New("stop failed")
	steps := make([]string, 0, 3)
	err := restartServices(
		func() error {
			steps = append(steps, "stop")
			return stopErr
		},
		func() error {
			steps = append(steps, "start")
			return nil
		},
		func() { steps = append(steps, "pause") },
	)
	if !errors.Is(err, stopErr) {
		t.Fatalf("error = %v, want %v", err, stopErr)
	}
	want := []string{"stop", "pause", "start"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %#v, want %#v", steps, want)
	}
}

func TestQdrantRequestLoadsKeyWithoutPuttingItInURL(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "qdrant.key")
	if err := os.WriteFile(keyFile, []byte("request-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARIADNE_QDRANT_API_KEY_FILE", keyFile)
	t.Setenv("ARIADNE_QDRANT_API_KEY", "")
	t.Setenv("ARIADNE_QDRANT_TLS", "1")
	t.Setenv("ARIADNE_QDRANT_ALLOW_INSECURE_REMOTE", "")
	req, err := newQdrantRequest(t.Context(), "GET", "https://qdrant.example/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("api-key"); got != "request-secret" {
		t.Fatalf("api-key header = %q", got)
	}
	if got := req.URL.String(); got != "https://qdrant.example/healthz" {
		t.Fatalf("request URL = %q", got)
	}
}
