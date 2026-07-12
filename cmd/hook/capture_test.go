package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContentTextString(t *testing.T) {
	raw, _ := json.Marshal(" hello ")
	if got := contentText(raw); got != "hello" {
		t.Fatalf("contentText = %q", got)
	}
}

func TestContentTextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"tool_use","text":"skip"},{"type":"text","text":"two"}]`)
	if got := contentText(raw); got != "one\ntwo" {
		t.Fatalf("contentText = %q", got)
	}
}

func TestCondenseKeepsRoleTags(t *testing.T) {
	got := condense([]turn{
		{role: "user", text: "please remember this"},
		{role: "assistant", text: "done"},
	})
	if !strings.Contains(got, "U: please remember this") || !strings.Contains(got, "A: done") {
		t.Fatalf("condensed body = %q", got)
	}
}

func TestIsLocalEndpoint(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:11434":      true,
		"http://127.0.0.1:11434":      true,
		"http://[::1]:11434":          true,
		"http://ollama.example:11434": false,
		"https://ollama.lan":          false,
		"not a url":                   false,
	}
	for raw, want := range cases {
		if got := isLocalEndpoint(raw); got != want {
			t.Fatalf("isLocalEndpoint(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestSummaryOllamaURLRequiresOptInForRemote(t *testing.T) {
	t.Setenv("ARIADNE_SUMMARY_OLLAMA", "http://ollama.example:11434")
	t.Setenv("ARIADNE_CAPTURE_REMOTE", "0")
	if _, ok := summaryOllamaURL(); ok {
		t.Fatal("remote summary endpoint should be blocked by default")
	}
	t.Setenv("ARIADNE_CAPTURE_REMOTE", "1")
	if got, ok := summaryOllamaURL(); !ok || got != "http://ollama.example:11434" {
		t.Fatalf("summaryOllamaURL = %q/%v", got, ok)
	}
}
