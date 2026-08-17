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
	ErrNotTrusted  = errors.New("credential resource is not locally trusted")
)

type CredentialTrustAction string

const (
	CredentialTrust  CredentialTrustAction = "trust"
	CredentialRevoke CredentialTrustAction = "revoke"
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

// CredentialTrustEvent is an append-only local policy decision. It stores only
// the exact credential path/name and wing scope, never the credential value.
type CredentialTrustEvent struct {
	ID         string                `json:"id"`
	Action     CredentialTrustAction `json:"action"`
	SourceWing string                `json:"source_wing"`
	TargetWing string                `json:"target_wing"`
	Resource   string                `json:"resource"`
	Purpose    string                `json:"purpose"`
	CreatedAt  time.Time             `json:"created_at"`
	CreatedBy  string                `json:"created_by"`
}

// TrustedCredentialUse is an immutable audit record for a use allowed by a
// persistent local credential binding.
type TrustedCredentialUse struct {
	ID            string    `json:"id"`
	TrustEventID  string    `json:"trust_event_id"`
	ClientSession string    `json:"client_session"`
	Purpose       string    `json:"purpose"`
	UsedAt        time.Time `json:"used_at"`
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

// SetCredentialTrust appends a trust or revoke decision for one exact
// source-wing, target-wing, resource, and purpose tuple. Repeating the same effective
// decision is idempotent and returns the existing event.
func (m *Manager) SetCredentialTrust(scope CredentialScope, trusted bool, actor string) (CredentialTrustEvent, error) {
	scope = normalizeCredentialScope(scope)
	if err := validateCredentialScope(scope, false); err != nil {
		return CredentialTrustEvent{}, err
	}
	if err := validateText("created_by", actor, 128); err != nil {
		return CredentialTrustEvent{}, err
	}
	if err := m.ensureDirs(); err != nil {
		return CredentialTrustEvent{}, err
	}
	events, err := m.credentialTrustEvents()
	if err != nil {
		return CredentialTrustEvent{}, err
	}
	action := CredentialRevoke
	if trusted {
		action = CredentialTrust
	}
	var previous CredentialTrustEvent
	for i := len(events) - 1; i >= 0; i-- {
		if trustEventMatches(events[i], scope) {
			previous = events[i]
			if events[i].Action == action {
				return events[i], nil
			}
			break
		}
	}
	id, err := randomID()
	if err != nil {
		return CredentialTrustEvent{}, err
	}
	createdAt := m.now().UTC()
	if previous.ID != "" && !createdAt.After(previous.CreatedAt) {
		createdAt = previous.CreatedAt.Add(time.Nanosecond)
	}
	event := CredentialTrustEvent{
		ID: id, Action: action, SourceWing: scope.SourceWing, TargetWing: scope.TargetWing,
		Resource: scope.Resource, Purpose: scope.Purpose, CreatedAt: createdAt, CreatedBy: strings.TrimSpace(actor),
	}
	if err := writeExclusiveJSON(filepath.Join(m.dir("credential-trust"), id+".json"), event); err != nil {
		return CredentialTrustEvent{}, err
	}
	return event, nil
}

// AuthorizeTrustedCredential accepts a previously registered exact local
// binding and appends an immutable use record. The purpose must match the
// binding exactly; the client session is audited per use.
func (m *Manager) AuthorizeTrustedCredential(scope CredentialScope) (CredentialTrustEvent, error) {
	scope = normalizeCredentialScope(scope)
	if err := validateCredentialScope(scope, true); err != nil {
		return CredentialTrustEvent{}, err
	}
	events, err := m.credentialTrustEvents()
	if err != nil {
		return CredentialTrustEvent{}, err
	}
	var effective CredentialTrustEvent
	for _, event := range events {
		if trustEventMatches(event, scope) {
			effective = event
		}
	}
	if effective.ID == "" || effective.Action != CredentialTrust {
		return CredentialTrustEvent{}, ErrNotTrusted
	}
	id, err := randomID()
	if err != nil {
		return CredentialTrustEvent{}, err
	}
	use := TrustedCredentialUse{
		ID: id, TrustEventID: effective.ID, ClientSession: scope.ClientSession,
		Purpose: scope.Purpose, UsedAt: m.now().UTC(),
	}
	if err := writeExclusiveJSON(filepath.Join(m.dir("credential-uses"), id+".json"), use); err != nil {
		return CredentialTrustEvent{}, err
	}
	return effective, nil
}

func (m *Manager) credentialTrustEvents() ([]CredentialTrustEvent, error) {
	if err := m.ensureDirs(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.dir("credential-trust"))
	if err != nil {
		return nil, err
	}
	events := make([]CredentialTrustEvent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var event CredentialTrustEvent
		if err := readJSON(filepath.Join(m.dir("credential-trust"), entry.Name()), &event); err != nil {
			return nil, fmt.Errorf("read credential trust event %s: %w", entry.Name(), err)
		}
		if event.ID+".json" != entry.Name() ||
			(event.Action != CredentialTrust && event.Action != CredentialRevoke) {
			return nil, fmt.Errorf("invalid credential trust event %s", entry.Name())
		}
		if err := validateCredentialScope(CredentialScope{
			SourceWing: event.SourceWing, TargetWing: event.TargetWing, Resource: event.Resource, Purpose: event.Purpose,
		}, false); err != nil {
			return nil, fmt.Errorf("invalid credential trust event %s: %w", entry.Name(), err)
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	return events, nil
}

func normalizeCredentialScope(scope CredentialScope) CredentialScope {
	scope.ClientSession = strings.TrimSpace(scope.ClientSession)
	scope.SourceWing = strings.TrimSpace(scope.SourceWing)
	scope.TargetWing = strings.TrimSpace(scope.TargetWing)
	scope.Resource = strings.TrimSpace(scope.Resource)
	scope.Purpose = strings.TrimSpace(scope.Purpose)
	return scope
}

func validateCredentialScope(scope CredentialScope, requireUseFields bool) error {
	for name, value := range map[string]string{
		"source_wing": scope.SourceWing, "target_wing": scope.TargetWing, "resource": scope.Resource,
	} {
		if err := validateText(name, value, 512); err != nil {
			return err
		}
	}
	if scope.SourceWing == scope.TargetWing {
		return errors.New("credential trust requires different source and target wings")
	}
	if err := validateText("purpose", scope.Purpose, 512); err != nil {
		return err
	}
	if requireUseFields {
		if err := validateText("client_session", scope.ClientSession, 512); err != nil {
			return err
		}
	}
	return nil
}

func trustEventMatches(event CredentialTrustEvent, scope CredentialScope) bool {
	return event.SourceWing == scope.SourceWing && event.TargetWing == scope.TargetWing &&
		event.Resource == scope.Resource && event.Purpose == scope.Purpose
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
	for _, name := range []string{"requests", "decisions", "consumptions", "credential-trust", "credential-uses"} {
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
