package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/i18n"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPanel(t *testing.T) (*panelServer, *[]string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ariadne", "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	var actions []string
	p := &panelServer{
		token: "secret-token", addr: "127.0.0.1:4242", home: home,
		state: func() panelState { return panelState{Color: "orange", Issues: []string{"backup failed"}} },
		act: func(action string) bool {
			actions = append(actions, action)
			return action == "maintenance"
		},
	}
	return p, &actions
}

func panelRequest(p *panelServer, method, target, host, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set(panelTokenHeader, token)
	}
	rec := httptest.NewRecorder()
	p.routes().ServeHTTP(rec, req)
	return rec
}

func TestPanelRejectsForeignHostsAndMissingToken(t *testing.T) {
	p, _ := testPanel(t)
	if rec := panelRequest(p, http.MethodGet, "/api/state", "attacker.example:4242", p.token, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host status = %d", rec.Code)
	}
	if rec := panelRequest(p, http.MethodGet, "/api/state", p.addr, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", rec.Code)
	}
	if rec := panelRequest(p, http.MethodGet, "/api/state", p.addr, "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", rec.Code)
	}
	rec := panelRequest(p, http.MethodGet, "/api/state", p.addr, p.token, "")
	var state panelState
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &state) != nil || state.Color != "orange" {
		t.Fatalf("state status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPanelPageCarriesNoDataAndStrictPolicy(t *testing.T) {
	p, _ := testPanel(t)
	rec := panelRequest(p, http.MethodGet, "/", p.addr, "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("page status=%d csp=%q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(rec.Body.String(), p.token) {
		t.Fatal("the page must not embed the token")
	}
}

func TestPanelLogsAreAllowlistedAndRedacted(t *testing.T) {
	p, _ := testPanel(t)
	logPath := filepath.Join(p.home, ".ariadne", "logs", "maintenance.log")
	if err := os.WriteFile(logPath, []byte("first\nAPI_KEY=sk-live-0123456789abcdef0123456789abcdef\nlast\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := panelRequest(p, http.MethodGet, "/api/log?name=maintenance.log", p.addr, p.token, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "last") ||
		strings.Contains(rec.Body.String(), "0123456789abcdef0123456789abcdef") {
		t.Fatalf("log status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"../../.ssh/id_rsa", "activity.jsonl", ""} {
		if rec := panelRequest(p, http.MethodGet, "/api/log?name="+name, p.addr, p.token, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%q status = %d", name, rec.Code)
		}
	}
}

func TestPanelActionsGoThroughTheTrayHandler(t *testing.T) {
	p, actions := testPanel(t)
	if rec := panelRequest(p, http.MethodPost, "/api/action", p.addr, p.token, `{"action":"maintenance"}`); rec.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d", rec.Code)
	}
	if rec := panelRequest(p, http.MethodPost, "/api/action", p.addr, p.token, `{"action":"rm -rf"}`); rec.Code != http.StatusConflict {
		t.Fatalf("unknown action status = %d", rec.Code)
	}
	if rec := panelRequest(p, http.MethodGet, "/api/action", p.addr, p.token, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET action status = %d", rec.Code)
	}
	if strings.Join(*actions, ",") != "maintenance,rm -rf" {
		t.Fatalf("actions = %v", *actions)
	}
}

func TestHealthColorAndExplanation(t *testing.T) {
	up := svc{Up: true}
	cases := map[string]status{
		"gray":   {},
		"red":    {reachable: true, Qdrant: svc{}, Ollama: up},
		"orange": {reachable: true, Qdrant: up, Ollama: up, Issues: []string{"maintenance: failed"}},
		"green":  {reachable: true, Qdrant: up, Ollama: up},
	}
	for want, s := range cases {
		if got := healthColor(s); got != want {
			t.Fatalf("color = %q, want %q", got, want)
		}
		state := newPanelState(s, i18n.UK, false, nil)
		if state.Why == "" || state.Why == "panel.color."+want {
			t.Fatalf("%s has no explanation: %q", want, state.Why)
		}
	}
	if reasons := healthReasons(cases["red"]); len(reasons) == 0 || reasons[0] != "Qdrant down" {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestRecentActivityNewestFirstAndBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	base := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := activity.AppendAt(path, activity.Event{
			At:        base.Add(time.Duration(i) * time.Minute),
			Operation: "consolidate", Status: "running",
		}); err != nil {
			t.Fatal(err)
		}
	}
	events := recentActivity(path, 3)
	if len(events) != 3 || !events[0].At.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("events = %#v", events)
	}
}
