// Package activity records append-only maintenance outcomes for local status
// and diagnostics. Events contain counters and lifecycle state, never memory
// text or private source paths.
package activity

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event is one completed or failed maintenance operation.
type Event struct {
	At        time.Time        `json:"at"`
	Operation string           `json:"operation"`
	Status    string           `json:"status"`
	Counters  map[string]int64 `json:"counters,omitempty"`
	Message   string           `json:"message,omitempty"`
}

// Path returns the local append-only activity stream path.
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "state", "activity.jsonl")
}

// Append writes one event with O_APPEND. Existing history is never rewritten.
func Append(event Event) error { return AppendAt(Path(), event) }

// AppendAt is Append with an explicit path for tests and embedded callers.
func AppendAt(path string, event Event) error {
	if event.Operation == "" || event.Status == "" {
		return errors.New("activity operation and status are required")
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // user-owned state directory
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // caller-selected local state path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Latest returns the newest valid event for every operation. A missing stream
// is a normal fresh-install state.
func Latest() (map[string]Event, error) { return LatestAt(Path()) }

// LatestAt is Latest with an explicit path.
func LatestAt(path string) (map[string]Event, error) {
	f, err := os.Open(path) //nolint:gosec // caller-selected local state path
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]Event{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("activity line %d: %w", line, err)
		}
		if event.Operation != "" && (out[event.Operation].At.Before(event.At) || out[event.Operation].At.IsZero()) {
			out[event.Operation] = event
		}
	}
	return out, scanner.Err()
}
