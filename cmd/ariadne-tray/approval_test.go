package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"strings"
	"testing"
	"time"
)

func TestShortApprovalDetailDoesNotExposeMoreThanLimit(t *testing.T) {
	request := approval.Request{
		Kind: approval.KindCredential, SourceWing: "source", TargetWing: "target",
		Resource: "credential file", Purpose: strings.Repeat("purpose ", 30),
	}
	got := shortApprovalDetail(request, 64)
	if len([]rune(got)) != 64 || !strings.HasSuffix(got, "…") {
		t.Fatalf("detail = %q (%d runes)", got, len([]rune(got)))
	}
}

func TestApprovalPromptBodyIsBoundedAndWarnsUser(t *testing.T) {
	request := approval.Request{
		ID: "1234567890abcdef", Kind: approval.KindCrossWing, ActiveWing: "ariadne",
		Purpose: "compare projects", Query: strings.Repeat("query ", 100),
	}
	body := approvalPromptBody(i18n.UK, request)
	if !strings.Contains(body, "ID: 1234567890ab") ||
		!strings.Contains(body, "Схвалюйте лише запити") {
		t.Fatalf("body = %q", body)
	}
	if len([]rune(body)) > 600 {
		t.Fatalf("body has %d runes", len([]rune(body)))
	}
}

func TestApprovalPromptHelpMatchesTwoChoiceUI(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.UK} {
		help := i18n.T(lang, "approval.prompt_help")
		if strings.Contains(help, "Later") || strings.Contains(help, "Пізніше") {
			t.Fatalf("approval help still describes a removed third choice: %q", help)
		}
	}
}

func TestApprovalPromptGatePreventsDuplicatesAndRetries(t *testing.T) {
	var gate approvalPromptGate
	now := time.Unix(100, 0)
	if !gate.begin("request", now) || gate.begin("request", now) {
		t.Fatal("gate did not prevent a duplicate in-flight prompt")
	}
	gate.finish("request", now)
	if gate.begin("request", now.Add(approvalPromptRetryEvery-time.Second)) {
		t.Fatal("gate retried before cooldown")
	}
	if !gate.begin("request", now.Add(approvalPromptRetryEvery)) {
		t.Fatal("gate did not retry after cooldown")
	}
}

func TestParseApprovalPromptLabelFailsClosed(t *testing.T) {
	if got := parseApprovalPromptLabel("Approve\n", "Approve", "Deny"); got != approvalPromptApprove {
		t.Fatalf("approve = %d", got)
	}
	if got := parseApprovalPromptLabel("Deny\n", "Approve", "Deny"); got != approvalPromptDeny {
		t.Fatalf("deny = %d", got)
	}
	if got := parseApprovalPromptLabel("unexpected", "Approve", "Deny"); got != approvalPromptLater {
		t.Fatalf("unexpected = %d", got)
	}
}

func TestDarwinApprovalPromptActivatesBeforeDisplaying(t *testing.T) {
	activate := strings.Index(darwinApprovalScript, "tell current application to activate")
	display := strings.Index(darwinApprovalScript, "display dialog")
	if activate < 0 || display < 0 || activate > display {
		t.Fatalf("darwin prompt must activate before display: %q", darwinApprovalScript)
	}
}

func TestDarwinApprovalPromptHasExactlyTwoNonDefaultButtons(t *testing.T) {
	if strings.Contains(darwinApprovalScript, "default button") {
		t.Fatalf("approval prompt must not let Return activate any button: %q", darwinApprovalScript)
	}
	if strings.Contains(darwinApprovalScript, "cancel button") ||
		strings.Contains(darwinApprovalScript, "laterLabel") {
		t.Fatalf("approval prompt must not expose a Later or cancel button: %q", darwinApprovalScript)
	}
	if !strings.Contains(darwinApprovalScript, "buttons {denyLabel, approveLabel}") {
		t.Fatalf("approval prompt must expose only Deny and Approve: %q", darwinApprovalScript)
	}
}

func TestWindowsApprovalPromptHasExactlyTwoNonDefaultButtons(t *testing.T) {
	command := windowsApprovalCommand("Title", "Body", "Approve", "Deny")
	for _, required := range []string{
		"$f.AcceptButton=$null", "$f.CancelButton=$null",
		"$a.TabStop=$false", "$d.TabStop=$false",
		"Keys]::Enter", "Keys]::Escape",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("Windows prompt is missing %q: %q", required, command)
		}
	}
	if strings.Contains(command, "WScript.Shell") || strings.Contains(command, "Later") {
		t.Fatalf("Windows prompt must expose only explicit Approve and Deny controls: %q", command)
	}
}

func TestLinuxApprovalPromptHasExactlyTwoNonDefaultButtons(t *testing.T) {
	args := linuxApprovalArgs("Title", "Body", "Approve", "Deny")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-default") || strings.Contains(joined, "Later") {
		t.Fatalf("Linux prompt must not expose or select a third choice: %q", joined)
	}
	if !strings.Contains(joined, "-buttons Deny:7,Approve:6") {
		t.Fatalf("Linux prompt must expose only Deny and Approve: %q", joined)
	}
}

func TestApprovalKindTitleIsLocalized(t *testing.T) {
	if got := approvalKindTitle(i18n.UK, approval.KindCrossWing); got != "Міжпроєктна пам’ять" {
		t.Fatalf("cross-wing title = %q", got)
	}
	if got := approvalKindTitle(i18n.UK, approval.KindCredential); got != "Доступ до захищеного ресурсу" {
		t.Fatalf("credential title = %q", got)
	}
}
