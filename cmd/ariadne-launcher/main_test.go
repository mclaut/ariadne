package main

import (
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
