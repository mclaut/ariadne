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
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// Estimator identifies the deliberately model-independent approximation.
	Estimator       = "utf8-bytes/4-v2"
	month           = 30 * 24 * time.Hour
	dbSchemaVersion = 2
)

// Representation is one source-backed memory portion returned by a recall.
// ID is an opaque session+memory hash used to credit that source only once per
// client session; no memory text or raw session identifier is persisted.
type Representation struct {
	ID     string
	Tokens int64
}

// Event is one recall response delivered to an MCP client or session hook.
type Event struct {
	ID                   string
	RepresentedID        string // legacy aggregate dedupe; prefer Representations
	At                   time.Time
	Source               string
	DeliveredTokens      int64
	RepresentedTokens    int64
	AttributedTokens     int64
	UnattributedTokens   int64
	Memories             int64
	AttributedMemories   int64
	UnattributedMemories int64
	Representations      []Representation
}

// Totals is an aggregate over a time window.
type Totals struct {
	Recalls              int64 `json:"recalls"`
	Memories             int64 `json:"memories"`
	AttributedMemories   int64 `json:"attributed_memories"`
	UnattributedMemories int64 `json:"unattributed_memories"`
	DeliveredTokens      int64 `json:"delivered_tokens"`
	RepresentedTokens    int64 `json:"represented_tokens"`
	AttributedTokens     int64 `json:"attributed_tokens"`
	UnattributedTokens   int64 `json:"unattributed_tokens"`
	ConfirmedSavedTokens int64 `json:"confirmed_saved_tokens"`
	RecallOverheadTokens int64 `json:"recall_overhead_tokens"`
	NetAvoidedTokens     int64 `json:"net_avoided_tokens"`
	AttributionPercent   int64 `json:"attribution_percent"`
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

// SplitAttribution allocates the complete observed recall cost (query plus
// response) in proportion to source-backed versus unknown memory text. This
// keeps wrapper/query cost visible without pretending that legacy memories
// have a measurable source benefit.
func SplitAttribution(totalCost, totalMemoryTokens, attributedMemoryTokens int64) (attributed, unknown int64) {
	if totalCost <= 0 {
		return 0, 0
	}
	if totalMemoryTokens <= 0 || attributedMemoryTokens <= 0 {
		return 0, totalCost
	}
	if attributedMemoryTokens >= totalMemoryTokens {
		return totalCost, 0
	}
	attributed = (totalCost*attributedMemoryTokens + totalMemoryTokens/2) / totalMemoryTokens
	if attributed > totalCost {
		attributed = totalCost
	}
	return attributed, totalCost - attributed
}

// SessionEventID returns a non-reversible identifier, allowing represented
// context to be counted once while repeated delivery overhead is still counted.
func SessionEventID(source, sessionID string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + sessionID))
	return hex.EncodeToString(sum[:16])
}

// SessionMemoryID identifies one recalled memory within one client session.
// It lets direct and automatic recalls share the same honest rule: represented
// source context is credited once, while every delivery still costs tokens.
func SessionMemoryID(source, sessionID string, memoryID uint64) string {
	return SessionEventID(source, sessionID+"\x00"+strconv.FormatUint(memoryID, 10))
}

// SessionSourceID identifies one bounded source lineage within a client
// session. It remains stable when the same source-backed memory is reworded or
// re-embedded, so representation credit follows provenance rather than point
// identity.
func SessionSourceID(source, sessionID, sourceID string) string {
	return SessionEventID(source, sessionID+"\x00source\x00"+sourceID)
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
	normalizeAttribution(&event)
	represented := event.RepresentedTokens
	if len(event.Representations) > 0 {
		represented = 0
		for _, item := range event.Representations {
			if item.ID == "" || item.Tokens <= 0 {
				continue
			}
			result, insertErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO represented_events (id) VALUES (?)`, item.ID)
			if insertErr != nil {
				return insertErr
			}
			if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
				return rowsErr
			} else if rows > 0 {
				represented += item.Tokens
			}
		}
	} else if event.RepresentedID != "" {
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
		(id, ts, source, delivered_tokens, represented_tokens, memories,
		 attributed_tokens, unattributed_tokens, attributed_memories, unattributed_memories)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.At.Unix(), event.Source,
		event.DeliveredTokens, represented, event.Memories, event.AttributedTokens,
		event.UnattributedTokens, event.AttributedMemories, event.UnattributedMemories)
	if err == nil {
		err = tx.Commit()
	}
	secureFiles(path)
	return err
}

func normalizeAttribution(event *Event) {
	if event.DeliveredTokens < 0 {
		event.DeliveredTokens = 0
	}
	if event.AttributedTokens < 0 {
		event.AttributedTokens = 0
	}
	if event.AttributedTokens > event.DeliveredTokens {
		event.AttributedTokens = event.DeliveredTokens
	}
	if event.AttributedTokens == 0 && event.UnattributedTokens == 0 {
		if event.RepresentedTokens > 0 || len(event.Representations) > 0 {
			event.AttributedTokens = event.DeliveredTokens
		} else {
			event.UnattributedTokens = event.DeliveredTokens
		}
	} else {
		event.UnattributedTokens = event.DeliveredTokens - event.AttributedTokens
	}

	if event.Memories < 0 {
		event.Memories = 0
	}
	if event.AttributedMemories < 0 {
		event.AttributedMemories = 0
	}
	if event.AttributedMemories > event.Memories {
		event.AttributedMemories = event.Memories
	}
	if event.AttributedMemories == 0 && event.UnattributedMemories == 0 {
		if event.RepresentedTokens > 0 || len(event.Representations) > 0 {
			event.AttributedMemories = event.Memories
		} else {
			event.UnattributedMemories = event.Memories
		}
	} else {
		event.UnattributedMemories = event.Memories - event.AttributedMemories
	}
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
	version, err := schemaVersion(ctx, db)
	if err != nil {
		_ = db.Close()
		return out, err
	}
	if version < dbSchemaVersion {
		_ = db.Close()
		db, err = open(ctx, path, true)
		if err != nil {
			return out, err
		}
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
		var version int
		if version, err = schemaVersion(ctx, db); err == nil && version < dbSchemaVersion {
			if _, err = db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err == nil {
				err = migrateSchema(ctx, db)
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

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	return version, err
}

func migrateSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS recall_events (
				id TEXT PRIMARY KEY,
				ts INTEGER NOT NULL,
				source TEXT NOT NULL,
				delivered_tokens INTEGER NOT NULL,
				represented_tokens INTEGER NOT NULL,
				memories INTEGER NOT NULL,
				attributed_tokens INTEGER NOT NULL DEFAULT 0,
				unattributed_tokens INTEGER NOT NULL DEFAULT 0,
				attributed_memories INTEGER NOT NULL DEFAULT 0,
				unattributed_memories INTEGER NOT NULL DEFAULT 0
			)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS represented_events (
		id TEXT PRIMARY KEY
	)`); err != nil {
		return err
	}

	migrated := false
	columns := []struct{ name, definition string }{
		{"attributed_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"unattributed_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"attributed_memories", "INTEGER NOT NULL DEFAULT 0"},
		{"unattributed_memories", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		has, columnErr := hasColumn(ctx, tx, "recall_events", column.name)
		if columnErr != nil {
			return columnErr
		}
		if has {
			continue
		}
		if _, columnErr = tx.ExecContext(ctx, "ALTER TABLE recall_events ADD COLUMN "+column.name+" "+column.definition); columnErr != nil {
			return columnErr
		}
		migrated = true
	}
	if migrated {
		_, err = tx.ExecContext(ctx, `UPDATE recall_events SET
			attributed_tokens = CASE WHEN represented_tokens > 0 THEN delivered_tokens ELSE 0 END,
			unattributed_tokens = CASE WHEN represented_tokens > 0 THEN 0 ELSE delivered_tokens END,
			attributed_memories = CASE WHEN represented_tokens > 0 THEN memories ELSE 0 END,
			unattributed_memories = CASE WHEN represented_tokens > 0 THEN 0 ELSE memories END`)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS classify_legacy_recall
		AFTER INSERT ON recall_events
		WHEN NEW.delivered_tokens > 0 AND NEW.attributed_tokens = 0 AND NEW.unattributed_tokens = 0
		BEGIN
			UPDATE recall_events SET
				attributed_tokens = CASE WHEN NEW.represented_tokens > 0 THEN NEW.delivered_tokens ELSE 0 END,
				unattributed_tokens = CASE WHEN NEW.represented_tokens > 0 THEN 0 ELSE NEW.delivered_tokens END,
				attributed_memories = CASE WHEN NEW.represented_tokens > 0 THEN NEW.memories ELSE 0 END,
				unattributed_memories = CASE WHEN NEW.represented_tokens > 0 THEN 0 ELSE NEW.memories END
			WHERE id = NEW.id;
		END`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", dbSchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func hasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func secureFiles(path string) {
	_ = os.Chmod(path, 0o600)
	_ = os.Chmod(path+"-wal", 0o600)
	_ = os.Chmod(path+"-shm", 0o600)
}

func totals(ctx context.Context, db *sql.DB, since int64) (Totals, error) {
	var out Totals
	row := db.QueryRowContext(ctx, `WITH classified AS (
		SELECT *,
			CASE WHEN delivered_tokens > 0 AND attributed_tokens = 0 AND unattributed_tokens = 0
				THEN CASE WHEN represented_tokens > 0 THEN delivered_tokens ELSE 0 END
				ELSE attributed_tokens END AS measured_tokens,
			CASE WHEN delivered_tokens > 0 AND attributed_tokens = 0 AND unattributed_tokens = 0
				THEN CASE WHEN represented_tokens > 0 THEN 0 ELSE delivered_tokens END
				ELSE unattributed_tokens END AS unknown_tokens,
			CASE WHEN memories > 0 AND attributed_memories = 0 AND unattributed_memories = 0
				THEN CASE WHEN represented_tokens > 0 THEN memories ELSE 0 END
				ELSE attributed_memories END AS measured_memories,
			CASE WHEN memories > 0 AND attributed_memories = 0 AND unattributed_memories = 0
				THEN CASE WHEN represented_tokens > 0 THEN 0 ELSE memories END
				ELSE unattributed_memories END AS unknown_memories
		FROM recall_events WHERE ts >= ?
	)
	SELECT COUNT(*), COALESCE(SUM(memories), 0),
		COALESCE(SUM(measured_memories), 0), COALESCE(SUM(unknown_memories), 0),
		COALESCE(SUM(delivered_tokens), 0), COALESCE(SUM(represented_tokens), 0),
		COALESCE(SUM(measured_tokens), 0), COALESCE(SUM(unknown_tokens), 0),
		COALESCE(SUM(MAX(represented_tokens - measured_tokens, 0)), 0),
		COALESCE(SUM(MAX(measured_tokens - represented_tokens, 0)), 0)
	FROM classified`, since)
	if err := row.Scan(&out.Recalls, &out.Memories, &out.AttributedMemories, &out.UnattributedMemories,
		&out.DeliveredTokens, &out.RepresentedTokens, &out.AttributedTokens, &out.UnattributedTokens,
		&out.ConfirmedSavedTokens, &out.RecallOverheadTokens); err != nil {
		return out, err
	}
	out.NetAvoidedTokens = out.RepresentedTokens - out.AttributedTokens
	if out.DeliveredTokens > 0 {
		out.AttributionPercent = (out.AttributedTokens*100 + out.DeliveredTokens/2) / out.DeliveredTokens
	}
	return out, nil
}
