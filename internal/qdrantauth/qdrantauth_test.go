package qdrantauth

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteRequiresKeyAndTLS(t *testing.T) {
	if err := (Config{}).ValidateGRPC("qdrant.example"); err == nil || !strings.Contains(err.Error(), EnvAPIKey) {
		t.Fatalf("missing key error = %v", err)
	}
	withKey := Config{APIKey: "secret"}
	if err := withKey.ValidateGRPC("qdrant.example"); err == nil || !strings.Contains(err.Error(), EnvTLS) {
		t.Fatalf("missing TLS error = %v", err)
	}
	if err := (Config{APIKey: "secret", UseTLS: true}).ValidateGRPC("qdrant.example"); err != nil {
		t.Fatalf("secure remote rejected: %v", err)
	}
}

func TestLoopbackAndExplicitOverrideRemainAvailable(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := (Config{}).ValidateGRPC(host); err != nil {
			t.Fatalf("loopback %q rejected: %v", host, err)
		}
	}
	if err := (Config{AllowInsecureRemote: true}).ValidateGRPC("qdrant.internal"); err != nil {
		t.Fatalf("explicit override rejected: %v", err)
	}
}

func TestRESTRequiresHTTPSForRemoteKey(t *testing.T) {
	cfg := Config{APIKey: "secret", UseTLS: true}
	if err := cfg.ValidateURL("http://qdrant.example:6333/healthz"); err == nil {
		t.Fatal("plaintext remote REST accepted")
	}
	if err := cfg.ValidateURL("https://qdrant.example:6333/healthz"); err != nil {
		t.Fatalf("HTTPS remote rejected: %v", err)
	}
}

func TestKeyFileAndHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qdrant.key")
	if err := os.WriteFile(path, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromValues("", path, "true", "false")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://qdrant.example", nil)
	cfg.Apply(req)
	if got := req.Header.Get("api-key"); got != "secret-value" {
		t.Fatalf("api-key header = %q", got)
	}
}

func TestRejectsAmbiguousOrMultilineKeys(t *testing.T) {
	if _, err := FromValues("one", "key.txt", "", ""); err == nil {
		t.Fatal("raw key plus file accepted")
	}
	if _, err := FromValues("one\ntwo", "", "", ""); err == nil {
		t.Fatal("multiline key accepted")
	}
}

func TestRejectsLooseKeyFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not authoritative on Windows")
	}
	path := filepath.Join(t.TempDir(), "qdrant.key")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil { //nolint:gosec // deliberately insecure negative test
		t.Fatal(err)
	}
	if _, err := FromValues("", path, "", ""); err == nil {
		t.Fatal("loose key file permissions accepted")
	}
}
