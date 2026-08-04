package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const approvalPromptRetryEvery = time.Minute

var errApprovalPromptUnavailable = errors.New("system approval prompt is unavailable")

type approvalPromptAction uint8

const (
	approvalPromptLater approvalPromptAction = iota
	approvalPromptDeny
	approvalPromptApprove
)

// approvalPromptGate guarantees that polling cannot open duplicate windows for
// one request. Dismissing a prompt leaves the request pending and permits a new
// reminder after a short cooldown.
type approvalPromptGate struct {
	inFlight string
	retryID  string
	retryAt  time.Time
}

func (g *approvalPromptGate) begin(id string, now time.Time) bool {
	if id == "" || g.inFlight != "" {
		return false
	}
	if g.retryID == id && now.Before(g.retryAt) {
		return false
	}
	g.inFlight = id
	return true
}

func (g *approvalPromptGate) finish(id string, now time.Time) {
	if g.inFlight == id {
		g.inFlight = ""
	}
	g.retryID = id
	g.retryAt = now.Add(approvalPromptRetryEvery)
}

func showApprovalSystemPrompt(
	ctx context.Context, platform string, activeLang i18n.Lang, request approval.Request,
) (approvalPromptAction, error) {
	title := i18n.T(activeLang, "approval.prompt_title")
	body := approvalPromptBody(activeLang, request)
	approve := i18n.T(activeLang, "approval.prompt_approve")
	deny := i18n.T(activeLang, "approval.prompt_deny")
	later := i18n.T(activeLang, "approval.prompt_later")

	switch platform {
	case osDarwin:
		return runDarwinApprovalPrompt(ctx, title, body, approve, deny, later)
	case osWindows:
		return runWindowsApprovalPrompt(ctx, title, body)
	case osLinux:
		return runLinuxApprovalPrompt(ctx, title, body, approve, deny, later)
	default:
		return approvalPromptLater, errApprovalPromptUnavailable
	}
}

func approvalPromptBody(activeLang i18n.Lang, request approval.Request) string {
	kind := approvalKindTitle(activeLang, request.Kind)
	detail := shortApprovalDetail(request, 400)
	requestID := request.ID
	if len(requestID) > 12 {
		requestID = requestID[:12]
	}
	return fmt.Sprintf(
		"%s\n\n%s\n\nID: %s\n\n%s",
		kind, detail, requestID, i18n.T(activeLang, "approval.prompt_help"),
	)
}

func runDarwinApprovalPrompt(
	ctx context.Context, title, body, approve, deny, later string,
) (approvalPromptAction, error) {
	const script = `on run argv
set dialogTitle to item 1 of argv
set dialogBody to item 2 of argv
set approveLabel to item 3 of argv
set denyLabel to item 4 of argv
set laterLabel to item 5 of argv
try
  set answer to display dialog dialogBody ¬
    with title dialogTitle ¬
    buttons {laterLabel, denyLabel, approveLabel} ¬
    default button laterLabel ¬
    cancel button laterLabel ¬
    with icon caution
  return button returned of answer
on error number -128
  return laterLabel
end try
end run`
	out, err := exec.CommandContext( //nolint:gosec // fixed AppleScript; all user-facing values are isolated argv items
		ctx, "osascript", "-e", script, "--", title, body, approve, deny, later,
	).Output()
	if err != nil {
		return approvalPromptLater, fmt.Errorf("macOS approval prompt: %w", err)
	}
	return parseApprovalPromptLabel(string(out), approve, deny), nil
}

func runWindowsApprovalPrompt(ctx context.Context, title, body string) (approvalPromptAction, error) {
	command := "$w=New-Object -ComObject WScript.Shell;" +
		"$r=$w.Popup(" + psQuote(body) + ",0," + psQuote(title) + ",51);" +
		"Write-Output $r"
	out, err := exec.CommandContext( //nolint:gosec // encoded fixed dialog command with localized UI-only text
		ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(command),
	).Output()
	if err != nil {
		return approvalPromptLater, fmt.Errorf("windows approval prompt: %w", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "6":
		return approvalPromptApprove, nil
	case "7":
		return approvalPromptDeny, nil
	default:
		return approvalPromptLater, nil
	}
}

func runLinuxApprovalPrompt(
	ctx context.Context, title, body, approve, deny, later string,
) (approvalPromptAction, error) {
	if _, err := exec.LookPath("kdialog"); err == nil {
		cmd := exec.CommandContext( //nolint:gosec // fixed dialog binary and localized UI-only arguments
			ctx, "kdialog", "--title", title, "--yesnocancel", body,
			"--yes-label", approve, "--no-label", deny, "--cancel-label", later,
		)
		err := cmd.Run()
		if err == nil {
			return approvalPromptApprove, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case 1:
				return approvalPromptDeny, nil
			case 2:
				return approvalPromptLater, nil
			}
		}
		return approvalPromptLater, fmt.Errorf("linux approval prompt: %w", err)
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		// Zenity cannot reliably distinguish its cancel button from closing the
		// window, so the safe fallback is Approve or Later; Deny remains in tray.
		err := exec.CommandContext( //nolint:gosec // fixed dialog binary and localized UI-only arguments
			ctx, "zenity", "--question", "--title", title, "--text", body,
			"--ok-label", approve, "--cancel-label", later,
		).Run()
		if err == nil {
			return approvalPromptApprove, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return approvalPromptLater, nil
		}
		return approvalPromptLater, fmt.Errorf("linux approval prompt: %w", err)
	}
	return approvalPromptLater, errApprovalPromptUnavailable
}

func parseApprovalPromptLabel(output, approve, deny string) approvalPromptAction {
	switch strings.TrimSpace(output) {
	case approve:
		return approvalPromptApprove
	case deny:
		return approvalPromptDeny
	default:
		return approvalPromptLater
	}
}

func runApprovalSystemPrompt(request approval.Request, activeLang i18n.Lang) {
	ctx, cancel := context.WithDeadline(context.Background(), request.PendingUntil)
	action, err := showApprovalSystemPrompt(ctx, runtime.GOOS, activeLang, request)
	cancel()

	mu.Lock()
	approvalPrompts.finish(request.ID, time.Now())
	mu.Unlock()

	if err != nil {
		if !errors.Is(err, errApprovalPromptUnavailable) {
			logApprovalPromptError(err)
		}
		return
	}
	switch action {
	case approvalPromptApprove:
		decideApprovalByID(request.ID, true, activeLang)
	case approvalPromptDeny:
		decideApprovalByID(request.ID, false, activeLang)
	case approvalPromptLater:
		// No decision is durable until the human explicitly approves or denies.
	}
}

func logApprovalPromptError(err error) {
	// Kept separate so tests can exercise prompt decisions without UI globals.
	log.Printf("system approval prompt: %v", err)
}
