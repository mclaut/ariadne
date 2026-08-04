// Package approval implements local, append-only human approval requests for
// cross-project memory and credential access. The MCP server creates requests;
// only the desktop tray writes decisions. Request, decision, and consumption
// records are separate immutable files so the complete audit trail remains.
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Kind string

const (
	KindCrossWing  Kind = "cross_wing"
	KindCredential Kind = "credential"

	pendingTTL    = 10 * time.Minute
	crossWingTTL  = 15 * time.Minute
	credentialTTL = 5 * time.Minute
)

var (
	ErrNotFound    = errors.New("approval request not found")
	ErrNotApproved = errors.New("approval request is not approved")
	ErrExpired     = errors.New("approval request or grant expired")
	ErrScope       = errors.New("approval scope does not match")
	ErrConsumed    = errors.New("approval grant was already consumed")
)

type Request struct {
	ID            string    `json:"id"`
	Kind          Kind      `json:"kind"`
	ClientSession string    `json:"client_session"`
	ActiveWing    string    `json:"active_wing,omitempty"`
	SourceWing    string    `json:"source_wing,omitempty"`
	TargetWing    string    `json:"target_wing,omitempty"`
	Resource      string    `json:"resource,omitempty"`
	Query         string    `json:"query,omitempty"`
	Purpose       string    `json:"purpose"`
	Collection    string    `json:"collection,omitempty"`
	Room          string    `json:"room,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	PendingUntil  time.Time `json:"pending_until"`
}

type Decision struct {
	RequestID      string    `json:"request_id"`
	Approved       bool      `json:"approved"`
	DecidedAt      time.Time `json:"decided_at"`
	GrantExpiresAt time.Time `json:"grant_expires_at,omitempty"`
	DecidedBy      string    `json:"decided_by"`
}

type Consumption struct {
	RequestID string    `json:"request_id"`
	UsedAt    time.Time `json:"used_at"`
	UsedBy    string    `json:"used_by"`
}

type CredentialScope struct {
	ClientSession string
	SourceWing    string
	TargetWing    string
	Resource      string
	Purpose       string
}

type Manager struct {
	root string
	now  func() time.Time
}

func New(root string) *Manager {
	if strings.TrimSpace(root) == "" {
		root = defaultRoot()
	}
	return &Manager{root: root, now: time.Now}
}

func defaultRoot() string {
	if configured := strings.TrimSpace(os.Getenv("ARIADNE_APPROVAL_DIR")); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "state", "approvals")
}

func (m *Manager) Create(input Request) (Request, error) {
	if err := validateRequest(input); err != nil {
		return Request{}, err
	}
	if err := m.ensureDirs(); err != nil {
		return Request{}, err
	}
	if existing, ok := m.matchingPending(input); ok {
		return existing, nil
	}
	id, err := randomID()
	if err != nil {
		return Request{}, err
	}
	now := m.now().UTC()
	input.ID = id
	input.CreatedAt = now
	input.PendingUntil = now.Add(pendingTTL)
	if err := writeExclusiveJSON(m.requestPath(id), input); err != nil {
		return Request{}, err
	}
	return input, nil
}

func (m *Manager) Pending() ([]Request, error) {
	if err := m.ensureDirs(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.dir("requests"))
	if err != nil {
		return nil, err
	}
	now := m.now().UTC()
	out := make([]Request, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var request Request
		if err := readJSON(filepath.Join(m.dir("requests"), entry.Name()), &request); err != nil {
			continue
		}
		if !request.PendingUntil.After(now) || fileExists(m.decisionPath(request.ID)) {
			continue
		}
		out = append(out, request)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Manager) Decide(id string, approved bool) (Decision, error) {
	request, err := m.loadRequest(id)
	if err != nil {
		return Decision{}, err
	}
	now := m.now().UTC()
	if !request.PendingUntil.After(now) {
		return Decision{}, ErrExpired
	}
	ttl := crossWingTTL
	if request.Kind == KindCredential {
		ttl = credentialTTL
	}
	decision := Decision{
		RequestID: id, Approved: approved, DecidedAt: now, DecidedBy: "ariadne-tray",
	}
	if approved {
		decision.GrantExpiresAt = now.Add(ttl)
	}
	if err := writeExclusiveJSON(m.decisionPath(id), decision); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Decision{}, fmt.Errorf("request already decided: %w", err)
		}
		return Decision{}, err
	}
	return decision, nil
}

func (m *Manager) AuthorizeCrossWing(id, clientSession, activeWing, collection string) (Request, error) {
	request, decision, err := m.authorized(id, KindCrossWing, clientSession)
	if err != nil {
		return Request{}, err
	}
	if strings.TrimSpace(request.ActiveWing) != strings.TrimSpace(activeWing) ||
		strings.TrimSpace(request.Collection) != strings.TrimSpace(collection) {
		return Request{}, ErrScope
	}
	if !decision.GrantExpiresAt.After(m.now().UTC()) {
		return Request{}, ErrExpired
	}
	return request, nil
}

func (m *Manager) AuthorizeCredential(id string, scope CredentialScope) (Request, error) {
	request, decision, err := m.authorized(id, KindCredential, scope.ClientSession)
	if err != nil {
		return Request{}, err
	}
	if request.SourceWing != strings.TrimSpace(scope.SourceWing) ||
		request.TargetWing != strings.TrimSpace(scope.TargetWing) ||
		request.Resource != strings.TrimSpace(scope.Resource) || request.Purpose != strings.TrimSpace(scope.Purpose) {
		return Request{}, ErrScope
	}
	if !decision.GrantExpiresAt.After(m.now().UTC()) {
		return Request{}, ErrExpired
	}
	consumption := Consumption{RequestID: id, UsedAt: m.now().UTC(), UsedBy: scope.ClientSession}
	if err := writeExclusiveJSON(m.consumptionPath(id), consumption); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Request{}, ErrConsumed
		}
		return Request{}, err
	}
	return request, nil
}

func (m *Manager) authorized(id string, kind Kind, clientSession string) (Request, Decision, error) {
	request, err := m.loadRequest(id)
	if err != nil {
		return Request{}, Decision{}, err
	}
	if request.Kind != kind || request.ClientSession != strings.TrimSpace(clientSession) {
		return Request{}, Decision{}, ErrScope
	}
	var decision Decision
	if err := readJSON(m.decisionPath(id), &decision); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Request{}, Decision{}, ErrNotApproved
		}
		return Request{}, Decision{}, err
	}
	if !decision.Approved {
		return Request{}, Decision{}, ErrNotApproved
	}
	return request, decision, nil
}

func (m *Manager) matchingPending(input Request) (Request, bool) {
	pending, err := m.Pending()
	if err != nil {
		return Request{}, false
	}
	for _, request := range pending {
		if request.Kind == input.Kind && request.ClientSession == input.ClientSession &&
			request.ActiveWing == input.ActiveWing && request.SourceWing == input.SourceWing &&
			request.TargetWing == input.TargetWing && request.Resource == input.Resource &&
			request.Query == input.Query && request.Purpose == input.Purpose &&
			request.Collection == input.Collection && request.Room == input.Room {
			return request, true
		}
	}
	return Request{}, false
}

func validateRequest(request Request) error {
	if request.Kind != KindCrossWing && request.Kind != KindCredential {
		return fmt.Errorf("unsupported approval kind %q", request.Kind)
	}
	for name, value := range map[string]string{
		"client_session": request.ClientSession, "purpose": request.Purpose,
	} {
		if err := validateText(name, value, 512); err != nil {
			return err
		}
	}
	if request.Kind == KindCrossWing {
		if err := validateText("active_wing", request.ActiveWing, 128); err != nil {
			return err
		}
		if err := validateText("query", request.Query, 2_000); err != nil {
			return err
		}
		return nil
	}
	for name, value := range map[string]string{
		"source_wing": request.SourceWing, "target_wing": request.TargetWing,
		"resource": request.Resource,
	} {
		if err := validateText(name, value, 512); err != nil {
			return err
		}
	}
	if request.SourceWing == request.TargetWing {
		return errors.New("credential approval requires different source and target wings")
	}
	return nil
}

func validateText(name, value string, limit int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len([]rune(value)) > limit || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func (m *Manager) loadRequest(id string) (Request, error) {
	if !validID(id) {
		return Request{}, ErrNotFound
	}
	var request Request
	if err := readJSON(m.requestPath(id), &request); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	return request, nil
}

func (m *Manager) ensureDirs() error {
	for _, name := range []string{"requests", "decisions", "consumptions"} {
		if err := os.MkdirAll(m.dir(name), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) dir(name string) string       { return filepath.Join(m.root, name) }
func (m *Manager) requestPath(id string) string { return filepath.Join(m.dir("requests"), id+".json") }
func (m *Manager) decisionPath(id string) string {
	return filepath.Join(m.dir("decisions"), id+".json")
}

func (m *Manager) consumptionPath(id string) string {
	return filepath.Join(m.dir("consumptions"), id+".json")
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func writeExclusiveJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // fixed private runtime state
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func readJSON(path string, value any) error {
	b, err := os.ReadFile(path) //nolint:gosec // fixed private runtime state
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
