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

func TestApprovalKindTitleIsLocalized(t *testing.T) {
	if got := approvalKindTitle(i18n.UK, approval.KindCrossWing); got != "Міжпроєктна пам’ять" {
		t.Fatalf("cross-wing title = %q", got)
	}
	if got := approvalKindTitle(i18n.UK, approval.KindCredential); got != "Доступ до захищеного ресурсу" {
		t.Fatalf("credential title = %q", got)
	}
}
