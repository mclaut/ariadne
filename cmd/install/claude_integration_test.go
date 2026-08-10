//go:build !windows

package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestUpsertClaudeHookUpdatesExistingAriadneEntry(t *testing.T) {
	t.Parallel()
	hooks := map[string]any{
		"SessionStart": []any{map[string]any{
			"matcher": "startup|resume|clear",
			"hooks": []any{map[string]any{
				"type": "command", "command": "/old/runtime/ariadne-hook session-start", "timeout": float64(15),
			}},
		}},
	}
	command := "/new/runtime/ariadne-hook session-start"
	upsertClaudeHook(hooks, "SessionStart", claudeSessionStartMatcher, command, 15)

	entries := hooks["SessionStart"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want one updated entry", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["matcher"] != claudeSessionStartMatcher {
		t.Fatalf("matcher = %v", entry["matcher"])
	}
	handler := entry["hooks"].([]any)[0].(map[string]any)
	if handler["command"] != command {
		t.Fatalf("command = %v", handler["command"])
	}
}

func TestHooksConfigCurrentRequiresEveryCurrentHook(t *testing.T) {
	t.Parallel()
	home := "/home/tester"
	bin := filepath.Join(home, ".ariadne", "bin", "ariadne-hook")
	hooks := map[string]any{}
	upsertClaudeHook(hooks, "SessionStart", claudeSessionStartMatcher, bin+" session-start", 15)
	upsertClaudeHook(hooks, "SessionEnd", "", bin+" session-end", 10)
	upsertClaudeHook(hooks, "PreCompact", "manual|auto", bin+" session-end", 10)
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if !hooksConfigCurrent(data, home) {
		t.Fatal("current hooks were not recognized")
	}

	upsertClaudeHook(hooks, "SessionStart", "startup", bin+" session-start", 15)
	data, err = json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if hooksConfigCurrent(data, home) {
		t.Fatal("stale SessionStart matcher was accepted")
	}
}
