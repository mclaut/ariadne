package main

import (
	"testing"
	"time"
)

func TestGroupDiaryByWingAndLocalDay(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 7, 10, 10, 0, 0, 0, time.Local).Unix()
	t2 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.Local).Unix()
	got := groupDiary([]diaryPoint{
		{Wing: "api", TS: t1},
		{Wing: "api", TS: t1 + 60},
		{Wing: "api", TS: t2},
		{Wing: "web", TS: t1},
	})
	if len(got) != 3 || len(got["api/2026-07-10"]) != 2 {
		t.Fatalf("unexpected groups: %#v", got)
	}
}

func TestValidConsolidated(t *testing.T) {
	t.Parallel()
	for _, room := range []string{"decisions", "gotchas", "reference"} {
		if !validConsolidated(consolidatedMemory{Room: room, Text: "durable fact"}) {
			t.Fatalf("room %q should be valid", room)
		}
	}
	if validConsolidated(consolidatedMemory{Room: "diary", Text: "chronology"}) ||
		validConsolidated(consolidatedMemory{Room: "reference"}) {
		t.Fatal("invalid consolidated memory accepted")
	}
}

func TestLocalSummaryEndpoint(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://localhost:11434", "http://127.0.0.1:11434", "http://[::1]:11434"} {
		if !localSummaryEndpoint(raw) {
			t.Fatalf("%q should be local", raw)
		}
	}
	if localSummaryEndpoint("https://ollama.example") {
		t.Fatal("remote endpoint accepted")
	}
}
