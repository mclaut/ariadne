package main

import "testing"

func TestAutoRecallSourceIncludesContextTransitions(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"", "startup", "resume", "clear", "compact", "fork"} {
		if !autoRecallSource(source) {
			t.Errorf("source %q must trigger auto-recall", source)
		}
	}
	if autoRecallSource("unknown") {
		t.Fatal("unknown source must fail closed")
	}
}
