//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseTrayLabelsKeepsVersionedAgentsNewestFirst(t *testing.T) {
	output := `-	0	com.ariadne.tray.v0812
-	0	com.other.service
212	0	com.ariadne.tray.v0814
-	0	com.ariadne.tray
`
	want := []string{"com.ariadne.tray.v0814", "com.ariadne.tray.v0812"}
	if got := parseTrayLabels(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

func TestAppleScriptQuoteEscapesUserVisibleErrors(t *testing.T) {
	got := appleScriptQuote(`path "quoted" \\ failed`)
	want := `"path \"quoted\" \\\\ failed"`
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func TestPanelURLFromHandleAcceptsOnlyLoopback(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, "panel.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if url, ok := panelURLFromHandle(write(`{"url":"http://127.0.0.1:5000/#t=abc"}`)); !ok || url != "http://127.0.0.1:5000/#t=abc" {
		t.Fatalf("url=%q ok=%v", url, ok)
	}
	for _, body := range []string{`{"url":"https://evil.example/"}`, `{"url":"file:///etc/passwd"}`, `not json`} {
		if _, ok := panelURLFromHandle(write(body)); ok {
			t.Fatalf("accepted %s", body)
		}
	}
}
