package approval

import (
	"errors"
	"fmt"
	"io/fs"
	"time"
)

const stateExpired = "expired"

// Status is a read-only snapshot, never an authorization token.
type Status struct {
	ID             string    `json:"id"`
	Kind           Kind      `json:"kind"`
	State          string    `json:"state"`
	PendingUntil   time.Time `json:"pending_until"`
	DecidedAt      time.Time `json:"decided_at,omitzero"`
	GrantExpiresAt time.Time `json:"grant_expires_at,omitzero"`
	UsedAt         time.Time `json:"used_at,omitzero"`
}

// Status reads one request's lifecycle without deciding or consuming it.
func (m *Manager) Status(id string) (Status, error) {
	request, err := m.loadRequest(id)
	if err != nil {
		return Status{}, err
	}
	if request.ID != id {
		return Status{}, fmt.Errorf("request id mismatch")
	}
	var decision Decision
	decisionErr := readJSON(m.decisionPath(id), &decision)
	if decisionErr != nil && !errors.Is(decisionErr, fs.ErrNotExist) {
		return Status{}, decisionErr
	}
	var consumption Consumption
	consumptionErr := readJSON(m.consumptionPath(id), &consumption)
	if consumptionErr != nil && !errors.Is(consumptionErr, fs.ErrNotExist) {
		return Status{}, consumptionErr
	}
	if decisionErr == nil && decision.RequestID != id {
		return Status{}, fmt.Errorf("decision id mismatch")
	}
	if consumptionErr == nil && (decisionErr != nil || !decision.Approved || consumption.RequestID != id) {
		return Status{}, fmt.Errorf("inconsistent approval consumption")
	}
	status := Status{ID: id, Kind: request.Kind, State: "pending", PendingUntil: request.PendingUntil}
	now := m.now().UTC()
	if decisionErr != nil {
		if !request.PendingUntil.After(now) {
			status.State = stateExpired
		}
		return status, nil
	}
	status.DecidedAt = decision.DecidedAt
	status.GrantExpiresAt = decision.GrantExpiresAt
	switch {
	case !decision.Approved:
		status.State = "denied"
	case consumptionErr == nil:
		status.State = "consumed"
		status.UsedAt = consumption.UsedAt
	case !decision.GrantExpiresAt.After(now):
		status.State = stateExpired
	default:
		status.State = "approved"
	}
	return status, nil
}
