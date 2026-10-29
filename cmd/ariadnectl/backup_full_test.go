package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFullSQLiteCapturesCommittedWALAndRestoresIndependently(t *testing.T) {
	source := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	statements := []string{"PRAGMA journal_mode=WAL", "CREATE TABLE evidence (value TEXT)", "INSERT INTO evidence VALUES ('preserved')"}
	for _, stmt := range statements {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), "restored.db")
	if err = copyFullSQLite(source, dest); err != nil {
		t.Fatal(err)
	}
	copyDB, err := fullSQLite(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var value string
	if err = copyDB.QueryRowContext(ctx, "SELECT value FROM evidence").Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if err = copyFullSQLite(source, dest); err == nil {
		t.Fatal("existing SQLite overwritten")
	}
}

func TestFullSQLiteDriveURIHasNoAuthority(t *testing.T) {
	u := fullSQLiteURL("C:/profile with spaces/runtime/metrics.db")
	if u.Host != "" || u.Path != "/C:/profile with spaces/runtime/metrics.db" || u.Query().Get("mode") != "ro" {
		t.Fatalf("invalid drive URI: %s", u.String())
	}
	if u.String() != "file:///C:/profile%20with%20spaces/runtime/metrics.db?_pragma=busy_timeout%285000%29&mode=ro" {
		t.Fatalf("unexpected SQLite URI: %s", u.String())
	}
}

func TestFullPlanRejectsOverlappingAndReservedPaths(t *testing.T) {
	for _, names := range [][]string{
		{"runtime", "runtime/bin"},
		{"runtime", "runtime-extra", "runtime/bin"},
		{"RUNTIME", "runtime/bin"},
		{"snapshots/data"},
		{"../escape"},
		{"plan.json"},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		p := fullBackupPlan{Format: 1, Sources: map[string]string{}}
		for _, name := range names {
			p.Sources[name] = t.TempDir()
		}
		if err := os.MkdirAll(filepath.Dir(fullPlanPath()), 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(fullPlanPath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err = loadFullPlan(); err == nil {
			t.Fatalf("invalid names accepted: %v", names)
		}
	}
}

func TestFullClientCaptureExcludesUnrelatedServersAndHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	for _, name := range []string{".codex", ".claude"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := "[mcp_servers.ariadne]\ncommand='owned'\n[mcp_servers.foreign]\ncommand='unrelated'\n"
	if err := os.WriteFile(filepath.Join(home, ".codex/config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := `{"mcpServers":{"ariadne":{"command":"owned"},"foreign":{"command":"unrelated"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := `{"hooks":{"SessionStart":[{"hooks":[{"command":"ariadne-hook session-start"},{"command":"unrelated"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, ".claude/settings.json"), []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := captureFullClients(stage); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // generated temporary scoped client fixture
	b, err := os.ReadFile(filepath.Join(stage, "client/ariadne-sections.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ client, field string }{{"codex", "mcp_servers"}, {"claude", "mcpServers"}} {
		client, ok := result[item.client].(map[string]any)
		if !ok {
			t.Fatal("client missing")
		}
		servers, ok := client[item.field].(map[string]any)
		if !ok || len(servers) != 1 || servers["ariadne"] == nil {
			t.Fatal("foreign server captured")
		}
	}
	settings := result["claude_settings"].(map[string]any)
	events := settings["hooks"].(map[string]any)
	groups := events["SessionStart"].([]any)
	handlers := groups[0].(map[string]any)["hooks"].([]any)
	if len(handlers) != 1 || handlers[0].(map[string]any)["command"] != "ariadne-hook session-start" {
		t.Fatal("unrelated hook captured")
	}
}
