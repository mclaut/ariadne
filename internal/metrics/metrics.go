// Package metrics records local, content-free estimates of context reused by
// Ariadne recalls. It stores only counters and hashed event identifiers.
package metrics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// Estimator identifies the deliberately model-independent approximation.
	Estimator = "utf8-bytes/4-v1"
	month     = 30 * 24 * time.Hour
)

// Event is one recall response delivered to an MCP client or session hook.
type Event struct {
	ID                string
	RepresentedID     string
	At                time.Time
	Source            string
	DeliveredTokens   int64
	RepresentedTokens int64
	Memories          int64
}

// Totals is an aggregate over a time window.
type Totals struct {
	Recalls           int64 `json:"recalls"`
	Memories          int64 `json:"memories"`
	DeliveredTokens   int64 `json:"delivered_tokens"`
	RepresentedTokens int64 `json:"represented_tokens"`
	NetAvoidedTokens  int64 `json:"net_avoided_tokens"`
}

// Summary exposes both lifetime and recent token-efficiency estimates.
type Summary struct {
	Estimated  bool   `json:"estimated"`
	Estimator  string `json:"estimator"`
	AllTime    Totals `json:"all_time"`
	Last30Days Totals `json:"last_30_days"`
}

// EstimateTokens approximates model tokens from UTF-8 bytes. The result is
// intentionally labelled as an estimate because tokenizers vary by client and
// model; byte counting keeps the method deterministic and multilingual.
func EstimateTokens(text string) int64 {
	n := len([]byte(strings.TrimSpace(text)))
	if n == 0 {
		return 0
	}
	return int64((n + 3) / 4)
}

// RepresentedShare estimates how much original context a returned portion of a
// curated memory represents. Unknown legacy metadata produces no claimed gain.
func RepresentedShare(sourceTokens, memoryTokens, deliveredMemoryTokens int64) int64 {
	if sourceTokens <= 0 || memoryTokens <= 0 || deliveredMemoryTokens <= 0 {
		return 0
	}
	if deliveredMemoryTokens >= memoryTokens {
		return sourceTokens
	}
	return (sourceTokens*deliveredMemoryTokens + memoryTokens - 1) / memoryTokens
}

// SessionEventID returns a non-reversible identifier, allowing represented
// context to be counted once while repeated delivery overhead is still counted.
func SessionEventID(source, sessionID string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + sessionID))
	return hex.EncodeToString(sum[:16])
}

// UniqueEventID returns an opaque identifier for recalls without a client
// session id, such as direct MCP tool calls.
func UniqueEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return SessionEventID("fallback", fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()))
}

// DefaultPath is the local metrics database. ARIADNE_METRICS_DB is primarily
// useful for tests and custom runtime layouts.
func DefaultPath() string {
	if path := os.Getenv("ARIADNE_METRICS_DB"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "metrics.db")
}

// RecordRecall stores one content-free event. ARIADNE_METRICS=0 disables new
// records without affecting existing local totals.
func RecordRecall(ctx context.Context, event Event) error {
	if os.Getenv("ARIADNE_METRICS") == "0" {
		return nil
	}
	return RecordRecallAt(ctx, DefaultPath(), event)
}

// RecordRecallAt is RecordRecall with an explicit path, used by tests.
func RecordRecallAt(ctx context.Context, path string, event Event) error {
	if event.ID == "" {
		event.ID = UniqueEventID()
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	db, err := open(ctx, path, true)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	represented := event.RepresentedTokens
	if event.RepresentedID != "" {
		result, insertErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO represented_events (id) VALUES (?)`, event.RepresentedID)
		if insertErr != nil {
			return insertErr
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
			return rowsErr
		} else if rows == 0 {
			represented = 0
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO recall_events
		(id, ts, source, delivered_tokens, represented_tokens, memories)
		VALUES (?, ?, ?, ?, ?, ?)`, event.ID, event.At.Unix(), event.Source,
		event.DeliveredTokens, represented, event.Memories)
	if err == nil {
		err = tx.Commit()
	}
	secureFiles(path)
	return err
}

// Read returns empty totals before the first metric is recorded.
func Read(ctx context.Context) (Summary, error) {
	return ReadAt(ctx, DefaultPath(), time.Now())
}

// ReadAt aggregates one metrics database at the supplied clock time.
func ReadAt(ctx context.Context, path string, now time.Time) (Summary, error) {
	out := Summary{Estimated: true, Estimator: Estimator}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	db, err := open(ctx, path, false)
	if err != nil {
		return out, err
	}
	defer func() { _ = db.Close() }()
	if out.AllTime, err = totals(ctx, db, 0); err != nil {
		return out, err
	}
	out.Last30Days, err = totals(ctx, db, now.Add(-month).Unix())
	return out, err
}

func open(ctx context.Context, path string, migrate bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA busy_timeout = 2000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if migrate {
		if _, err = db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err == nil {
			_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS recall_events (
				id TEXT PRIMARY KEY,
				ts INTEGER NOT NULL,
				source TEXT NOT NULL,
				delivered_tokens INTEGER NOT NULL,
				represented_tokens INTEGER NOT NULL,
				memories INTEGER NOT NULL
			)`)
			if err == nil {
				_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS represented_events (
					id TEXT PRIMARY KEY
				)`)
			}
		}
		secureFiles(path)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func secureFiles(path string) {
	_ = os.Chmod(path, 0o600)
	_ = os.Chmod(path+"-wal", 0o600)
	_ = os.Chmod(path+"-shm", 0o600)
}

func totals(ctx context.Context, db *sql.DB, since int64) (Totals, error) {
	var out Totals
	row := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(memories), 0),
		COALESCE(SUM(delivered_tokens), 0), COALESCE(SUM(represented_tokens), 0)
		FROM recall_events WHERE ts >= ?`, since)
	if err := row.Scan(&out.Recalls, &out.Memories, &out.DeliveredTokens, &out.RepresentedTokens); err != nil {
		return out, err
	}
	out.NetAvoidedTokens = out.RepresentedTokens - out.DeliveredTokens
	return out, nil
}
