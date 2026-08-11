package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestRestartServicesAlwaysAttemptsRecoveryStart(t *testing.T) {
	t.Parallel()
	stopErr := errors.New("stop failed")
	steps := make([]string, 0, 3)
	err := restartServices(
		func() error {
			steps = append(steps, "stop")
			return stopErr
		},
		func() error {
			steps = append(steps, "start")
			return nil
		},
		func() { steps = append(steps, "pause") },
	)
	if !errors.Is(err, stopErr) {
		t.Fatalf("error = %v, want %v", err, stopErr)
	}
	want := []string{"stop", "pause", "start"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %#v, want %#v", steps, want)
	}
}
