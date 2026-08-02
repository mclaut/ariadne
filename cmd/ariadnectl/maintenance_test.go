package main

import (
	"ariadne/internal/activity"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRunMaintenanceRetriesOnlyFailedStage(t *testing.T) {
	config := maintenanceConfig{
		attempts: 3, retryDelay: time.Second, maxRetryDelay: 10 * time.Second,
		commandTimeout: time.Minute, before: 24 * time.Hour,
		importPath: "import", ctlPath: "ariadnectl",
	}
	var commands []string
	var sleeps []time.Duration
	var events []activity.Event
	consolidateCalls := 0
	deps := maintenanceDeps{
		run: func(_ context.Context, path string, args ...string) error {
			commands = append(commands, path+" "+strings.Join(args, " "))
			if path == "ariadnectl" {
				consolidateCalls++
				if consolidateCalls < 3 {
					return errors.New("quality gate")
				}
			}
			return nil
		},
		sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
		append: func(event activity.Event) error {
			events = append(events, event)
			return nil
		},
		now: func() time.Time { return time.Date(2026, 8, 2, 4, 30, 0, 0, time.UTC) },
	}
	if err := runMaintenance(context.Background(), config, deps); err != nil {
		t.Fatal(err)
	}
	wantCommands := []string{
		"import -source memfiles -sync",
		"ariadnectl consolidate --before 24h0m0s",
		"ariadnectl consolidate --before 24h0m0s",
		"ariadnectl consolidate --before 24h0m0s",
	}
	if !slices.Equal(commands, wantCommands) {
		t.Fatalf("commands = %#v", commands)
	}
	if !slices.Equal(sleeps, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("sleeps = %#v", sleeps)
	}
	last := events[len(events)-1]
	if last.Status != "complete" || last.Counters["import_attempts"] != 1 ||
		last.Counters["consolidate_attempts"] != 3 {
		t.Fatalf("last event = %#v", last)
	}
}

func TestRunMaintenanceStopsBeforeConsolidationWhenImportExhaustsRetries(t *testing.T) {
	config := maintenanceConfig{
		attempts: 2, retryDelay: 0, maxRetryDelay: 0,
		commandTimeout: time.Minute, before: 24 * time.Hour,
		importPath: "import", ctlPath: "ariadnectl",
	}
	var commands []string
	var events []activity.Event
	deps := maintenanceDeps{
		run: func(_ context.Context, path string, _ ...string) error {
			commands = append(commands, path)
			return errors.New("unavailable")
		},
		sleep: func(context.Context, time.Duration) error { return nil },
		append: func(event activity.Event) error {
			events = append(events, event)
			return nil
		},
		now: time.Now,
	}
	if err := runMaintenance(context.Background(), config, deps); err == nil {
		t.Fatal("expected maintenance failure")
	}
	if !slices.Equal(commands, []string{"import", "import"}) {
		t.Fatalf("commands = %#v", commands)
	}
	last := events[len(events)-1]
	if last.Status != "failed" || last.Counters["import_attempts"] != 2 {
		t.Fatalf("last event = %#v", last)
	}
}

func TestRetryBackoffIsBounded(t *testing.T) {
	if got := retryBackoff(5*time.Minute, 12*time.Minute, 1); got != 5*time.Minute {
		t.Fatalf("first delay = %s", got)
	}
	if got := retryBackoff(5*time.Minute, 12*time.Minute, 2); got != 10*time.Minute {
		t.Fatalf("second delay = %s", got)
	}
	if got := retryBackoff(5*time.Minute, 12*time.Minute, 3); got != 12*time.Minute {
		t.Fatalf("bounded delay = %s", got)
	}
}

func TestValidateMaintenanceConfig(t *testing.T) {
	valid := maintenanceConfig{
		attempts: 3, retryDelay: time.Second, maxRetryDelay: time.Minute,
		commandTimeout: time.Minute, before: 24 * time.Hour, importPath: "import",
	}
	if err := validateMaintenanceConfig(valid); err != nil {
		t.Fatal(err)
	}
	valid.attempts = 0
	if err := validateMaintenanceConfig(valid); err == nil {
		t.Fatal("zero attempts accepted")
	}
}
