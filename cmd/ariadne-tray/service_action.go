package main

import (
	"ariadne/internal/i18n"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	serviceActionTimeout       = 90 * time.Second
	serviceVerificationTimeout = 20 * time.Second
	serviceVerificationPoll    = 500 * time.Millisecond
)

type serviceAction string

const (
	serviceStart   serviceAction = "start"
	serviceStop    serviceAction = "stop"
	serviceRestart serviceAction = "restart"
)

type serviceActionResult struct {
	Action        serviceAction
	Before        status
	After         status
	BeforeError   error
	CommandOutput string
	Err           error
	Duration      time.Duration
}

type (
	serviceCommand  func(context.Context, serviceAction) (string, error)
	serviceObserver func(context.Context) (status, error)
)

func serviceActionClicked(action serviceAction) {
	activeLang, ok := beginServiceAction()
	if !ok {
		return
	}
	title := i18n.T(activeLang, "notify.services")
	label := serviceActionLabel(activeLang, action)
	notify(title, label+": "+i18n.T(activeLang, "notify.in_progress"))

	ctx, cancel := context.WithTimeout(context.Background(), serviceActionTimeout)
	result := executeServiceAction(ctx, action, runtime.GOOS, runServiceCommand, observeServices)
	cancel()
	logServiceAction(result)

	if result.Err != nil {
		finishServiceAction()
		message := label + ": " + i18n.T(activeLang, "notify.failed") + " · " +
			shortServiceError(result.Err) + " · " + i18n.T(activeLang, "notify.see_logs")
		notify(title, message)
		poll()
		return
	}

	successMessage := label + ": " + i18n.T(activeLang, "notify.done") + " · " +
		i18n.T(activeLang, "notify.verified") + " · " + serviceStatusSummary(activeLang, result.After)
	if action == serviceRestart {
		if err := queueRestartNotification(activeLang, result.After); err != nil {
			log.Printf("queue restart notification: error=%q", err)
			if notifyErr := notifySync(title, successMessage); notifyErr != nil {
				log.Printf("fallback restart notification: error=%q", notifyErr)
			}
		}
		if err := relaunchTray(); err == nil {
			return
		} else {
			log.Printf("service action tray relaunch failed: action=%s error=%q", action, err)
			finishServiceAction()
			notify(title, label+": "+i18n.T(activeLang, "notify.failed")+" · "+
				shortServiceError(err)+" · "+i18n.T(activeLang, "notify.see_logs"))
			poll()
			return
		}
	}
	notify(title, successMessage)
	finishServiceAction()
	poll()
}

func beginServiceAction() (i18n.Lang, bool) {
	mu.Lock()
	defer mu.Unlock()
	if serviceActionRunning || maintenanceRunning || updates.installing {
		return lang, false
	}
	serviceActionRunning = true
	activeLang := lang
	refreshServiceMenusLocked()
	refreshMaintenanceMenuLocked()
	refreshUpdateMenuLocked()
	return activeLang, true
}

func finishServiceAction() {
	mu.Lock()
	serviceActionRunning = false
	refreshServiceMenusLocked()
	refreshMaintenanceMenuLocked()
	refreshUpdateMenuLocked()
	mu.Unlock()
}

func executeServiceAction(
	ctx context.Context,
	action serviceAction,
	platform string,
	run serviceCommand,
	observe serviceObserver,
) serviceActionResult {
	started := time.Now()
	result := serviceActionResult{Action: action}
	result.Before, result.BeforeError = observe(ctx)
	result.CommandOutput, result.Err = run(ctx, action)
	if result.Err != nil {
		result.Duration = time.Since(started)
		return result
	}

	verifyCtx, cancel := context.WithTimeout(ctx, serviceVerificationTimeout)
	defer cancel()
	result.After, result.Err = waitForServiceAction(
		verifyCtx, action, platform, result.Before, observe, serviceVerificationPoll,
	)
	result.Duration = time.Since(started)
	return result
}

func waitForServiceAction(
	ctx context.Context,
	action serviceAction,
	platform string,
	before status,
	observe serviceObserver,
	interval time.Duration,
) (status, error) {
	var last status
	var lastErr error
	for {
		after, err := observe(ctx)
		if err == nil {
			last = after
			lastErr = verifyServiceAction(action, platform, before, after)
			if lastErr == nil {
				return after, nil
			}
		} else {
			lastErr = err
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return last, fmt.Errorf("verification timed out: %w", lastErr)
			}
			return last, fmt.Errorf("verification timed out: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func verifyServiceAction(action serviceAction, platform string, before, after status) error {
	managedOllama := platform == osDarwin
	switch action {
	case serviceStart:
		if !after.Qdrant.Up {
			return fmt.Errorf("qdrant is not running")
		}
		if managedOllama && !after.Ollama.Up {
			return fmt.Errorf("ollama is not running")
		}
	case serviceStop:
		if after.Qdrant.Up {
			return fmt.Errorf("qdrant is still running with PID %d", after.Qdrant.PID)
		}
		if managedOllama && after.Ollama.Up {
			return fmt.Errorf("ollama is still running with PID %d", after.Ollama.PID)
		}
	case serviceRestart:
		if !after.Qdrant.Up {
			return fmt.Errorf("qdrant did not return")
		}
		if before.Qdrant.PID > 0 && after.Qdrant.PID > 0 && before.Qdrant.PID == after.Qdrant.PID {
			return fmt.Errorf("qdrant PID did not change (%d)", after.Qdrant.PID)
		}
		if managedOllama {
			if !after.Ollama.Up {
				return fmt.Errorf("ollama did not return")
			}
			if before.Ollama.PID > 0 && after.Ollama.PID > 0 && before.Ollama.PID == after.Ollama.PID {
				return fmt.Errorf("ollama PID did not change (%d)", after.Ollama.PID)
			}
		}
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
	if after.Qdrant.Up && after.Collection.Status != "" && after.Collection.Status != "green" {
		return fmt.Errorf("collection status is %s", after.Collection.Status)
	}
	return nil
}

func runServiceCommand(ctx context.Context, action serviceAction) (string, error) {
	output, err := exec.CommandContext(ctx, ctlPath(), string(action)).CombinedOutput() //nolint:gosec // fixed action set and our binary
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("ariadnectl %s: %w: %s", action, err, text)
		}
		return text, fmt.Errorf("ariadnectl %s: %w", action, err)
	}
	return text, nil
}

func observeServices(parent context.Context) (status, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return fetchStatus(ctx)
}

func fetchStatus(ctx context.Context) (status, error) {
	var s status
	output, err := exec.CommandContext(ctx, ctlPath(), "status", "-json").CombinedOutput() //nolint:gosec // our binary
	if err != nil {
		return s, fmt.Errorf("ariadnectl status: %w", err)
	}
	if err := json.Unmarshal(output, &s); err != nil {
		return s, fmt.Errorf("decode ariadnectl status: %w", err)
	}
	s.reachable = true
	return s, nil
}

func logServiceAction(result serviceActionResult) {
	log.Printf(
		"service action: action=%s duration=%s before=%s after=%s before_error=%q error=%q output=%q",
		result.Action,
		result.Duration.Round(time.Millisecond),
		serviceLogSummary(result.Before),
		serviceLogSummary(result.After),
		shortServiceError(result.BeforeError),
		shortServiceError(result.Err),
		truncateServiceText(result.CommandOutput, 2048),
	)
}

func serviceLogSummary(s status) string {
	return fmt.Sprintf(
		"reachable=%t,qdrant_up=%t,qdrant_pid=%d,ollama_up=%t,ollama_pid=%d,collection=%s",
		s.reachable, s.Qdrant.Up, s.Qdrant.PID, s.Ollama.Up, s.Ollama.PID, s.Collection.Status,
	)
}

func serviceStatusSummary(activeLang i18n.Lang, s status) string {
	qdrant := "Qdrant " + i18n.T(activeLang, "status.down")
	if s.Qdrant.Up {
		qdrant = "Qdrant " + i18n.T(activeLang, "status.up")
		if s.Qdrant.PID > 0 {
			qdrant += fmt.Sprintf(" · PID %d", s.Qdrant.PID)
		}
	}
	ollama := "Ollama " + i18n.T(activeLang, "status.down")
	if s.Ollama.Up {
		ollama = "Ollama " + i18n.T(activeLang, "status.up")
		if s.Ollama.PID > 0 {
			ollama += fmt.Sprintf(" · PID %d", s.Ollama.PID)
		}
	}
	return qdrant + " · " + ollama
}

func serviceActionLabel(activeLang i18n.Lang, action serviceAction) string {
	key := "menu." + string(action)
	return i18n.T(activeLang, key)
}

func shortServiceError(err error) string {
	if err == nil {
		return ""
	}
	return truncateServiceText(strings.Join(strings.Fields(err.Error()), " "), 180)
}

func truncateServiceText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
