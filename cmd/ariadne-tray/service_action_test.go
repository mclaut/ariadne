package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecuteServiceActionRestartUsesOneAtomicCommand(t *testing.T) {
	t.Parallel()
	before := healthyServiceStatus(101, 201)
	after := healthyServiceStatus(102, 202)
	observations := []status{before, after}
	observeCalls := 0
	commandCalls := make([]serviceAction, 0, 1)

	result := executeServiceAction(
		context.Background(),
		serviceRestart,
		osDarwin,
		func(_ context.Context, action serviceAction) (string, error) {
			commandCalls = append(commandCalls, action)
			return "restart issued", nil
		},
		func(context.Context) (status, error) {
			observation := observations[observeCalls]
			observeCalls++
			return observation, nil
		},
	)
	if result.Err != nil {
		t.Fatalf("execute restart: %v", result.Err)
	}
	if len(commandCalls) != 1 || commandCalls[0] != serviceRestart {
		t.Fatalf("commands = %#v, want one restart", commandCalls)
	}
	if observeCalls != 2 {
		t.Fatalf("observe calls = %d, want before and after", observeCalls)
	}
	if result.Before.Qdrant.PID != 101 || result.After.Qdrant.PID != 102 {
		t.Fatalf("qdrant transition = %d -> %d, want 101 -> 102",
			result.Before.Qdrant.PID, result.After.Qdrant.PID)
	}
}

func TestExecuteServiceActionStopsAfterCommandFailure(t *testing.T) {
	t.Parallel()
	observeCalls := 0
	runErr := errors.New("service command failed")
	result := executeServiceAction(
		context.Background(),
		serviceRestart,
		osDarwin,
		func(context.Context, serviceAction) (string, error) { return "details", runErr },
		func(context.Context) (status, error) {
			observeCalls++
			return healthyServiceStatus(101, 201), nil
		},
	)
	if !errors.Is(result.Err, runErr) {
		t.Fatalf("error = %v, want %v", result.Err, runErr)
	}
	if observeCalls != 1 {
		t.Fatalf("observe calls = %d, want only the before snapshot", observeCalls)
	}
}

func TestVerifyServiceAction(t *testing.T) {
	t.Parallel()
	before := healthyServiceStatus(101, 201)
	tests := []struct {
		name     string
		action   serviceAction
		platform string
		after    status
		want     string
	}{
		{name: "darwin restart verified", action: serviceRestart, platform: osDarwin, after: healthyServiceStatus(102, 202)},
		{
			name: "qdrant pid unchanged", action: serviceRestart, platform: osDarwin,
			after: healthyServiceStatus(101, 202), want: "qdrant PID did not change",
		},
		{
			name: "darwin ollama pid unchanged", action: serviceRestart, platform: osDarwin,
			after: healthyServiceStatus(102, 201), want: "ollama PID did not change",
		},
		{name: "linux leaves ollama alone", action: serviceRestart, platform: osLinux, after: healthyServiceStatus(102, 201)},
		{name: "darwin stop verified", action: serviceStop, platform: osDarwin, after: stoppedServiceStatus()},
		{
			name: "darwin stop leaves ollama up", action: serviceStop, platform: osDarwin,
			after: status{Ollama: svc{Up: true, PID: 201}}, want: "ollama is still running",
		},
		{name: "linux stop leaves ollama alone", action: serviceStop, platform: osLinux, after: status{Ollama: svc{Up: true, PID: 201}}},
		{name: "darwin start verified", action: serviceStart, platform: osDarwin, after: healthyServiceStatus(101, 201)},
		{
			name: "darwin start missing ollama", action: serviceStart, platform: osDarwin,
			after: status{Qdrant: svc{Up: true, PID: 101}}, want: "ollama is not running",
		},
		{
			name: "collection not green", action: serviceRestart, platform: osDarwin,
			after: unhealthyCollectionStatus(102, 202), want: "collection status is yellow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := verifyServiceAction(test.action, test.platform, before, test.after)
			if test.want == "" {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func healthyServiceStatus(qdrantPID, ollamaPID int) status {
	return status{
		reachable:  true,
		Qdrant:     svc{Up: true, PID: qdrantPID},
		Ollama:     svc{Up: true, PID: ollamaPID},
		Collection: coll{Status: "green"},
	}
}

func stoppedServiceStatus() status {
	return status{reachable: true}
}

func unhealthyCollectionStatus(qdrantPID, ollamaPID int) status {
	s := healthyServiceStatus(qdrantPID, ollamaPID)
	s.Collection.Status = "yellow"
	return s
}
