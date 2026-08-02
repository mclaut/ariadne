package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestCaptureMetadataUsesCaptureTimeNotTranscriptStart(t *testing.T) {
	first := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	last := time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 20, 18, 1, 0, 0, time.UTC)
	got := captureMetadata(now, first, last, "session-1", "bounded source", "memory")
	if got["ts"] != strconv.FormatInt(now.Unix(), 10) || got["observed_at"] != got["ts"] {
		t.Fatalf("capture timestamp = %#v", got)
	}
	if got["occurred_at"] != strconv.FormatInt(last.Unix(), 10) ||
		got["session_started_at"] != strconv.FormatInt(first.Unix(), 10) {
		t.Fatalf("session bounds = %#v", got)
	}
	if got["source_id"] == "" || got["status"] != "active" || got["memory_type"] != "event" {
		t.Fatalf("capture provenance = %#v", got)
	}
}

func TestCaptureMetadataSourceIDIsStableForSameBoundedSource(t *testing.T) {
	now := time.Date(2026, 7, 20, 18, 1, 0, 0, time.UTC)
	last := now.Add(-time.Minute)
	a := captureMetadata(now, time.Time{}, last, "session-1", "same source", "memory")
	b := captureMetadata(now.Add(time.Hour), time.Time{}, last, "session-1", "same source", "memory")
	if a["source_id"] != b["source_id"] {
		t.Fatalf("source ids differ: %q != %q", a["source_id"], b["source_id"])
	}
}
