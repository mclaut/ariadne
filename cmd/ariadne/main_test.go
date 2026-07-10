package main

import "testing"

func TestFormatMoveResultShowsKeptFields(t *testing.T) {
	got := formatMoveResult(42, "ariadne", "")
	want := `moved (id=42 wing="ariadne" room=<kept>)`
	if got != want {
		t.Fatalf("formatMoveResult = %q, want %q", got, want)
	}
}
