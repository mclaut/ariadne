package secretguard

import (
	"strings"
	"testing"
)

func TestFindingsAndRedactAssignments(t *testing.T) {
	input := "API_TOKEN=super-secret-value\nDB_PASSWORD: hunter42\nAPNS_KEY_PATH=/tmp/key.p8\nAUTH=required"
	findings := Findings(input)
	if len(findings) != 1 || findings[0] != "secret-assignment" {
		t.Fatalf("findings = %#v", findings)
	}
	got := Redact(input)
	if strings.Contains(got, "super-secret-value") || strings.Contains(got, "hunter42") {
		t.Fatalf("secret survived redaction: %q", got)
	}
	for _, safe := range []string{"API_TOKEN=[REDACTED]", "DB_PASSWORD: [REDACTED]", "APNS_KEY_PATH=/tmp/key.p8", "AUTH=required"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("redaction lost %q: %q", safe, got)
		}
	}
	if Redact(got) != got {
		t.Fatal("redaction is not idempotent")
	}
}

func TestReferencesAndPlaceholdersAreSafe(t *testing.T) {
	for _, input := range []string{
		"Credentials are stored in access.env with mode 0600.",
		"Use API_TOKEN from the environment.",
		"API_TOKEN=${API_TOKEN}",
		"PASSWORD=[REDACTED]",
		"PRIVATE_KEY_FILE=/run/keys/service.pem",
		`{"key":"ordinary-map-value"}`,
		"password=descriptive-config-value",
		`{"password":"placeholder"}`,
	} {
		if Contains(input) {
			t.Fatalf("safe reference flagged: %q", input)
		}
	}
}

func TestStructuredLowercaseAssignment(t *testing.T) {
	input := `{"password":"synthetic-lowercase-value","key":"ordinary-map-value"}`
	if !Contains(input) {
		t.Fatal("lowercase structured password was not detected")
	}
	got := Redact(input)
	if strings.Contains(got, "synthetic-lowercase-value") || !strings.Contains(got, "ordinary-map-value") {
		t.Fatalf("structured redaction = %q", got)
	}
}

func TestStructuredSecrets(t *testing.T) {
	inputs := []string{
		"postgres://user:secret-password@db.internal/app",
		"-----BEGIN " + "PRIVATE KEY-----\nopaque\n-----END " + "PRIVATE KEY-----",
		"github_" + "pat_" + "abcdefghijklmnopqrstuvwxyz123456",
	}
	for _, input := range inputs {
		if !Contains(input) {
			t.Fatalf("secret not detected: %q", input)
		}
		if Contains(Redact(input)) {
			t.Fatalf("redacted text still sensitive: %q", Redact(input))
		}
	}
}
