package main

import (
	"ariadne/internal/approval"
	"errors"
	"testing"
)

func TestCredentialCmdRequiresConfirmationAndRecordsTrust(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ARIADNE_APPROVAL_DIR", root)
	args := make([]string, 0, 10)
	args = append(args,
		"trust", "--source-wing", "keys", "--target-wing", "ariadne",
		"--resource", "/safe/hf_key.txt", "--purpose", "publish release",
	)
	if code := credentialCmd(args); code != 2 {
		t.Fatalf("without --yes code = %d", code)
	}
	if code := credentialCmd(append(args, "--yes")); code != 0 {
		t.Fatalf("trust code = %d", code)
	}
	scope := approval.CredentialScope{
		ClientSession: "test-session", SourceWing: "keys", TargetWing: "ariadne",
		Resource: "/safe/hf_key.txt", Purpose: "publish release",
	}
	if _, err := approval.New("").AuthorizeTrustedCredential(scope); err != nil {
		t.Fatalf("trusted authorization = %v", err)
	}
	revoke := append([]string{"revoke"}, args[1:]...)
	if code := credentialCmd(append(revoke, "--yes")); code != 0 {
		t.Fatalf("revoke code = %d", code)
	}
	if _, err := approval.New("").AuthorizeTrustedCredential(scope); !errors.Is(err, approval.ErrNotTrusted) {
		t.Fatalf("authorization after revoke = %v", err)
	}
}
