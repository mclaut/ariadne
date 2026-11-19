package main

import "testing"

func TestHealthGateIgnoresOnePollBlips(t *testing.T) {
	var g healthGate
	if shown, changed, notice := g.observe(healthGreen, nil, true); shown != healthGreen || !changed || notice != nil {
		t.Fatalf("first poll = %s %v %v", shown, changed, notice)
	}
	blip := []string{"metrics unavailable"}
	if shown, changed, notice := g.observe(healthOrange, blip, true); shown != healthGreen || changed || notice != nil {
		t.Fatalf("one-poll blip = %s %v %v", shown, changed, notice)
	}
	if shown, changed, notice := g.observe(healthGreen, nil, true); shown != healthGreen || changed || notice != nil {
		t.Fatalf("after blip = %s %v %v", shown, changed, notice)
	}
}

func TestHealthGateSettlesAndNotifiesOnce(t *testing.T) {
	var g healthGate
	g.observe(healthGreen, nil, true)
	down := []string{"Qdrant down"}
	g.observe(healthRed, down, true)
	shown, changed, notice := g.observe(healthRed, down, true)
	if shown != healthRed || !changed || len(notice) != 1 {
		t.Fatalf("settled red = %s %v %v", shown, changed, notice)
	}
	if _, changed, notice := g.observe(healthRed, down, true); changed || notice != nil {
		t.Fatalf("repeat notified again: %v %v", changed, notice)
	}
	// A different issue set that persists while still red is announced once.
	more := []string{"Qdrant down", "backup failed"}
	g.observe(healthRed, more, true)
	if _, _, notice := g.observe(healthRed, more, true); len(notice) != 2 {
		t.Fatalf("new persisted issue not announced: %v", notice)
	}
	// Recovery clears the memory, so the same failure later notifies again.
	g.observe(healthGreen, nil, true)
	g.observe(healthGreen, nil, true)
	g.observe(healthRed, down, true)
	if _, _, notice := g.observe(healthRed, down, true); len(notice) != 1 {
		t.Fatalf("recurrence after recovery not announced: %v", notice)
	}
}

func TestHealthGateStartsWithExistingProblem(t *testing.T) {
	var g healthGate
	issues := []string{"backup failed: partial"}
	if shown, _, notice := g.observe(healthOrange, issues, true); shown != healthOrange || notice != nil {
		t.Fatalf("first poll = %s %v", shown, notice)
	}
	if _, _, notice := g.observe(healthOrange, issues, true); len(notice) != 1 {
		t.Fatalf("persisting startup problem not announced: %v", notice)
	}
}
