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

// Approval prompts intentionally expose exactly two choices and designate
// neither as the default. Return/Enter and Escape must never decide a security
// request or create a misleading retry cooldown.
const darwinApprovalScript = `on run argv
set dialogTitle to item 1 of argv
set dialogBody to item 2 of argv
set approveLabel to item 3 of argv
set denyLabel to item 4 of argv
tell current application to activate
set answer to display dialog dialogBody ¬
  with title dialogTitle ¬
  buttons {denyLabel, approveLabel} ¬
  with icon caution
return button returned of answer
end run`

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

	switch platform {
	case osDarwin:
		return runDarwinApprovalPrompt(ctx, title, body, approve, deny)
	case osWindows:
		return runWindowsApprovalPrompt(ctx, title, body, approve, deny)
	case osLinux:
		return runLinuxApprovalPrompt(ctx, title, body, approve, deny)
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
	ctx context.Context, title, body, approve, deny string,
) (approvalPromptAction, error) {
	out, err := exec.CommandContext( //nolint:gosec // fixed AppleScript; all user-facing values are isolated argv items
		ctx, "osascript", "-e", darwinApprovalScript, "--", title, body, approve, deny,
	).Output()
	if err != nil {
		return approvalPromptLater, fmt.Errorf("macOS approval prompt: %w", err)
	}
	return parseApprovalPromptLabel(string(out), approve, deny), nil
}

func runWindowsApprovalPrompt(
	ctx context.Context, title, body, approve, deny string,
) (approvalPromptAction, error) {
	command := windowsApprovalCommand(title, body, approve, deny)
	out, err := exec.CommandContext( //nolint:gosec // encoded fixed dialog command with localized UI-only text
		ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(command),
	).Output()
	if err != nil {
		return approvalPromptLater, fmt.Errorf("windows approval prompt: %w", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "approve":
		return approvalPromptApprove, nil
	case "deny":
		return approvalPromptDeny, nil
	default:
		return approvalPromptLater, nil
	}
}

func windowsApprovalCommand(title, body, approve, deny string) string {
	return "Add-Type -AssemblyName System.Windows.Forms;" +
		"Add-Type -AssemblyName System.Drawing;" +
		"$f=New-Object System.Windows.Forms.Form;" +
		"$f.Text=" + psQuote(title) + ";" +
		"$f.StartPosition='CenterScreen';$f.FormBorderStyle='FixedDialog';" +
		"$f.ClientSize=New-Object System.Drawing.Size(620,260);" +
		"$f.MaximizeBox=$false;$f.MinimizeBox=$false;$f.TopMost=$true;$f.KeyPreview=$true;" +
		"$f.AcceptButton=$null;$f.CancelButton=$null;" +
		"$l=New-Object System.Windows.Forms.Label;" +
		"$l.Text=" + psQuote(body) + ";$l.Location=New-Object System.Drawing.Point(24,22);" +
		"$l.Size=New-Object System.Drawing.Size(572,170);" +
		"$a=New-Object System.Windows.Forms.Button;$a.Text=" + psQuote(approve) + ";" +
		"$a.Location=New-Object System.Drawing.Point(476,210);$a.Size=New-Object System.Drawing.Size(120,32);" +
		"$a.TabStop=$false;" +
		"$d=New-Object System.Windows.Forms.Button;$d.Text=" + psQuote(deny) + ";" +
		"$d.Location=New-Object System.Drawing.Point(342,210);$d.Size=New-Object System.Drawing.Size(120,32);" +
		"$d.TabStop=$false;" +
		"$a.Add_Click({$f.Tag='approve';$f.Close()});" +
		"$d.Add_Click({$f.Tag='deny';$f.Close()});" +
		"$f.Add_KeyDown({param($sender,$e)if($e.KeyCode -eq [System.Windows.Forms.Keys]::Enter -or " +
		"$e.KeyCode -eq [System.Windows.Forms.Keys]::Escape){$e.Handled=$true;$e.SuppressKeyPress=$true}});" +
		"$f.Add_Shown({$f.Activate();$f.ActiveControl=$null});" +
		"$f.Controls.Add($l);$f.Controls.Add($d);$f.Controls.Add($a);" +
		"[void]$f.ShowDialog();Write-Output ([string]$f.Tag)"
}

func runLinuxApprovalPrompt(
	ctx context.Context, title, body, approve, deny string,
) (approvalPromptAction, error) {
	if _, err := exec.LookPath("xmessage"); err == nil {
		// xmessage documents that omitting -default leaves every button
		// non-default, so Return cannot approve or deny the request.
		err := exec.CommandContext( //nolint:gosec // fixed dialog binary and localized UI-only arguments
			ctx, "xmessage", linuxApprovalArgs(title, body, approve, deny)...,
		).Run()
		if err == nil {
			return approvalPromptLater, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case 6:
				return approvalPromptApprove, nil
			case 7:
				return approvalPromptDeny, nil
			default:
				return approvalPromptLater, nil
			}
		}
		return approvalPromptLater, fmt.Errorf("linux approval prompt: %w", err)
	}
	return approvalPromptLater, errApprovalPromptUnavailable
}

func linuxApprovalArgs(title, body, approve, deny string) []string {
	return []string{
		"-center", "-title", title,
		"-buttons", deny + ":7," + approve + ":6",
		body,
	}
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
