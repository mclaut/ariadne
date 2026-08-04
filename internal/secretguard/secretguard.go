// Package secretguard detects and redacts high-confidence credential material
// before it can enter or leave Ariadne. It deliberately ignores plain variable
// names and .env file references: those are useful operational facts and are
// not secrets without an assigned value.
package secretguard

import (
	"regexp"
	"sort"
	"strings"
)

const redacted = "[REDACTED]"

type rule struct {
	name    string
	pattern *regexp.Regexp
	replace string
}

var rules = []rule{
	{
		name: "private-key",
		pattern: regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----.*?` +
			`-----END (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
		replace: "[PRIVATE KEY REDACTED]",
	},
	{
		name:    "credential-uri",
		pattern: regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`),
		replace: `${1}[REDACTED]@`,
	},
	{
		name: "known-token",
		pattern: regexp.MustCompile(`(?i)\b(?:sk-[a-z0-9_-]{20,}|sk-proj-[a-z0-9_-]{20,}|` +
			`github_pat_[a-z0-9_]{20,}|gh[pousr]_[a-z0-9]{20,}|hf_[a-z0-9]{20,}|` +
			`xox[baprs]-[a-z0-9-]{20,}|AIza[0-9A-Za-z_-]{20,}|` +
			`eyJ[a-z0-9_-]{10,}\.[a-z0-9_-]{10,}\.[a-z0-9_-]{10,})\b`),
		replace: redacted,
	},
}

var assignmentPattern = regexp.MustCompile(
	`(?m)(^|[\s,{;])(["']?)([A-Za-z][A-Za-z0-9_]{2,})(["']?\s*[:=]\s*)(["']?)([^\s,;"'}]+)(["']?)`,
)

var secretNamePattern = regexp.MustCompile(
	`(?i)(?:(?:^|_)(?:API_?KEY|KEY|TOKEN|SECRET|PASSWORD|PASSWD|PWD|CREDENTIAL|PRIVATE_?KEY|SIGN_?KEY)(?:$|_)|` +
		`(?:APIKEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)$)`,
)

var nonSecretNamePattern = regexp.MustCompile(
	`(?i)(?:_PATH|_FILE|_FILENAME|_ID|_LEN|_LENGTH|_ALGORITHM|_ALGORITHMS|_CHECKS)$`,
)

var structuredSecretNamePattern = regexp.MustCompile(
	`(?i)^(?:api_?key|access_?token|auth_?token|bearer_?token|client_?secret|private_?key|` +
		`sign_?key|token|secret|password|passwd|credential)$`,
)

var placeholderPattern = regexp.MustCompile(
	`(?i)^(?:\$|\$\{|<|\[?redacted\]?|example|placeholder|changeme|your[_-]|x{3,}|\*{3,}|\.{3,}|` +
		`string$|value$|sample$|dummy$|none$|null$|true$|false$)`,
)

// Findings returns stable rule names without ever returning the matched value.
func Findings(text string) []string {
	found := map[string]bool{}
	for _, candidate := range rules {
		if candidate.pattern.MatchString(text) {
			found[candidate.name] = true
		}
	}
	for _, match := range assignmentPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 7 || !sensitiveAssignment(match[3], match[6], match[2] != "") {
			continue
		}
		found["secret-assignment"] = true
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func Contains(text string) bool { return len(Findings(text)) > 0 }

// Redact preserves surrounding context and variable names while replacing only
// credential values. Calling Redact repeatedly is idempotent.
func Redact(text string) string {
	for _, candidate := range rules {
		text = candidate.pattern.ReplaceAllString(text, candidate.replace)
	}
	return assignmentPattern.ReplaceAllStringFunc(text, func(raw string) string {
		match := assignmentPattern.FindStringSubmatch(raw)
		if len(match) < 8 || !sensitiveAssignment(match[3], match[6], match[2] != "") {
			return raw
		}
		return match[1] + match[2] + match[3] + match[4] + match[5] + redacted + match[7]
	})
}

func sensitiveAssignment(name, value string, quotedName bool) bool {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if nonSecretNamePattern.MatchString(name) {
		return false
	}
	if name != strings.ToUpper(name) {
		if !quotedName || !structuredSecretNamePattern.MatchString(name) {
			return false
		}
	} else if !secretNamePattern.MatchString(name) {
		return false
	}
	if value == "" || value == redacted || placeholderPattern.MatchString(value) {
		return false
	}
	// Very short values are normally booleans, modes, or examples. Requiring a
	// little entropy avoids quarantining prose such as AUTH=required.
	return len([]rune(value)) >= 6
}
