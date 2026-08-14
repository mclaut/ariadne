// Command import backfills ariadne from an archived chromadb sqlite,
// re-embedding each stored document with bge-m3 into the Qdrant hybrid
// collection. Existing chroma chunks are imported as-is (already chunked);
// better chunking is a concern for NEW captures, not this backfill.
//
// Embedding is the bottleneck (~130 ms/doc), so documents are embedded by a
// worker pool. Content-hash ids make the whole import idempotent/resumable.
//
//	import [-source chroma|memfiles|jsonl] [-db PATH] [-file PATH] [-n LIMIT] [-workers N] [-skip-sessions] [-sync]
package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/secretguard"
	"ariadne/internal/store"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type doc struct {
	text, wing, room string
	meta             map[string]string
}

type memfileSyncPlan struct {
	sources map[string]memfileSource
	known   map[string]store.MemfileSourceState
	wings   map[string]bool
	skipped int
}

type memfileSource struct {
	wing, room, legacyRoom, revision string
}

const (
	nativeMemoryDir   = "memory"
	memfileRoomPrefix = nativeMemoryDir + ":"
)

type memfileSyncStore interface {
	SetMetaBySourceKey(context.Context, string, string, map[string]string) error
	SetMetaByWingRoomLegacy(context.Context, string, string, map[string]string) error
	SetMetaByWingRoom(context.Context, string, string, string, map[string]string) error
	TouchActiveMemfiles(context.Context, int64) error
	WingRoomPairs(context.Context) (map[[2]string]int, error)
}

func main() {
	source := flag.String("source", "chroma", "chroma | memfiles | jsonl")
	db := flag.String("db", "", "chromadb sqlite path (required for -source chroma)")
	file := flag.String("file", "", "JSONL export file (for -source jsonl)")
	limit := flag.Int("n", 0, "max docs (0 = all)")
	workers := flag.Int("workers", 8, "concurrent embed+upsert workers")
	batchSize := flag.Int("batch", 64, "docs embedded+upserted per round trip")
	skipSessions := flag.Bool("skip-sessions", false, "skip the raw-transcript 'sessions' wing")
	onlyWing := flag.String("only-wing", "", "chroma only: import just this one wing")
	syncMode := flag.Bool("sync", false,
		"memfiles only: append new revisions, archive stale chunks, and mark vanished sources as orphaned")
	forceMemfiles := flag.Bool("force", false, "memfiles only: re-embed unchanged revisions (migration/repair)")
	flag.Parse()

	st, err := store.New(env("ARIADNE_QDRANT_HOST", "localhost"), atoiOr(env("ARIADNE_QDRANT_PORT", "6334"), 6334),
		env("ARIADNE_OLLAMA", "http://localhost:11434"), env("ARIADNE_MODEL", "bge-m3"),
		env("ARIADNE_COLLECTION", "ariadne"))
	if err != nil {
		fatal("store:", err)
	}
	ctx := context.Background()
	if err := st.EnsureCollection(ctx); err != nil {
		fatal("ensure collection:", err)
	}

	jobs := make(chan doc, 1024)
	batches := make(chan []store.SaveItem, *workers*2)
	var done, failed, redacted atomic.Int64
	start := time.Now()

	// batcher: group docs into fixed-size batches, one round trip each.
	go func() {
		buf := make([]store.SaveItem, 0, *batchSize)
		for d := range jobs {
			item, wasRedacted := prepareSaveItem(d)
			if wasRedacted {
				redacted.Add(1)
			}
			buf = append(buf, item)
			if len(buf) == *batchSize {
				batches <- buf
				buf = make([]store.SaveItem, 0, *batchSize)
			}
		}
		if len(buf) > 0 {
			batches <- buf
		}
		close(batches)
	}()

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range batches {
				if err := st.SaveBatch(ctx, b); err != nil {
					// one bad doc shouldn't sink the whole batch — retry each alone
					for _, it := range b {
						if _, e := st.Save(ctx, it.Text, it.Meta); e != nil {
							failed.Add(1)
						} else {
							done.Add(1)
						}
					}
					continue
				}
				n := done.Add(int64(len(b)))
				if prev := n - int64(len(b)); n/2000 != prev/2000 {
					rate := float64(n) / time.Since(start).Seconds()
					fmt.Printf("  %d saved · %.0f/s · fail=%d\n", n, rate, failed.Load())
				}
			}
		}()
	}

	var feed int
	var feedErr error
	var syncPlan *memfileSyncPlan
	switch *source {
	case "memfiles":
		var known map[string]store.MemfileSourceState
		if *syncMode {
			known, err = st.MemfileSourceStates(ctx)
			if err != nil {
				fatal("sync state:", err)
			}
		}
		feed, syncPlan, feedErr = feedMemFiles(jobs, *syncMode, *forceMemfiles, known, time.Now())
	case "jsonl":
		feed = feedJSONL(jobs, *file)
	default:
		feed = feedChroma(jobs, *db, *skipSessions, *onlyWing, *limit)
	}
	close(jobs)
	wg.Wait()
	if feedErr != nil {
		_ = activity.Append(activity.Event{Operation: "memfile_sync", Status: "failed", Message: feedErr.Error()})
		fatal("memfile scan incomplete; lifecycle finalization skipped:", feedErr)
	}
	if syncPlan != nil {
		if failed.Load() > 0 {
			fmt.Fprintln(os.Stderr, "sync: import failures detected; existing revisions left active")
			_ = activity.Append(activity.Event{Operation: "memfile_sync", Status: "failed", Counters: map[string]int64{
				"embedded": done.Load(), "failed": failed.Load(), "redacted": redacted.Load(),
				"unchanged": int64(syncPlan.skipped),
			}})
		} else if err := finalizeMemfileSync(ctx, st, syncPlan, time.Now()); err != nil {
			_ = activity.Append(activity.Event{Operation: "memfile_sync", Status: "failed", Message: err.Error()})
			fatal("sync finalize:", err)
		} else {
			_ = activity.Append(activity.Event{Operation: "memfile_sync", Status: "complete", Counters: map[string]int64{
				"embedded": done.Load(), "failed": failed.Load(), "redacted": redacted.Load(),
				"unchanged": int64(syncPlan.skipped),
				"sources":   int64(len(syncPlan.sources)),
			}})
		}
		fmt.Printf("  unchanged=%d embedded=%d\n", syncPlan.skipped, feed)
	}

	fmt.Printf("\n=== IMPORT DONE ===\n  fed=%d saved=%d failed=%d redacted=%d\n  wall=%s (%.0f docs/s)\n",
		feed, done.Load(), failed.Load(), redacted.Load(), time.Since(start).Round(time.Second),
		float64(done.Load())/time.Since(start).Seconds())
	exitCode := importResultCode(failed.Load())
	if closeErr := st.Close(); closeErr != nil {
		fmt.Fprintln(os.Stderr, "import: close store:", closeErr)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	if exitCode != 0 {
		os.Exit(1)
	}
}

func prepareSaveItem(d doc) (store.SaveItem, bool) {
	meta := map[string]string{"wing": d.wing, "room": d.room, "provenance": "import"}
	for key, value := range d.meta {
		meta[key] = value
	}
	text := store.SanitizeUTF8(d.text)
	findings := secretguard.Findings(text)
	if len(findings) == 0 {
		return store.SaveItem{Text: text, Meta: meta}, false
	}
	meta["secret_redacted"] = "true"
	meta["redaction_rules"] = strings.Join(findings, ",")
	return store.SaveItem{Text: secretguard.Redact(text), Meta: meta}, true
}

func importResultCode(failed int64) int {
	if failed > 0 {
		return 1
	}
	return 0
}

// feedChroma reads documents from the archived chromadb sqlite.
func feedChroma(jobs chan<- doc, dbPath string, skipSessions bool, onlyWing string, limit int) int {
	if dbPath == "" {
		fatal("chroma source needs -db <path to the archived chromadb sqlite>")
	}
	sq, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		fatal("open sqlite:", err)
	}
	defer func() { _ = sq.Close() }()
	q := `SELECT d.string_value, w.string_value,
	        (SELECT string_value FROM embedding_metadata WHERE id=d.id AND key='room')
	      FROM embedding_metadata d
	      JOIN embedding_metadata w ON w.id=d.id AND w.key='wing'
	      WHERE d.key='chroma:document' AND length(d.string_value) > 120`
	var args []any
	if onlyWing != "" {
		q += ` AND w.string_value = ?` // parameterized — no injection
		args = append(args, onlyWing)
	} else if skipSessions {
		q += ` AND w.string_value NOT IN ('sessions')`
	}
	if limit > 0 {
		q += ` LIMIT ` + strconv.Itoa(limit) //nolint:gosec // limit is an int flag, not user text
	}
	rows, err := sq.QueryContext(context.Background(), q, args...)
	if err != nil {
		fatal("query:", err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var d doc
		var wing, room sql.NullString
		if err := rows.Scan(&d.text, &wing, &room); err != nil {
			fatal("scan:", err)
		}
		d.wing, d.room = wing.String, room.String
		jobs <- d
		n++
	}
	if err := rows.Err(); err != nil {
		fatal("rows:", err)
	}
	return n
}

// feedJSONL reads a portable export ({text,wing,room} per line) — the import
// side of `ariadnectl export`. Re-embeds each memory with the current model.
func feedJSONL(jobs chan<- doc, path string) int {
	if path == "" {
		fatal("jsonl source needs -file")
	}
	f, err := os.Open(path) //nolint:gosec // user-provided path
	if err != nil {
		fatal("open jsonl:", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	observedAt := strconv.FormatInt(time.Now().Unix(), 10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		text, _ := raw["text"].(string)
		if text == "" {
			continue
		}
		wing, _ := raw["wing"].(string)
		room, _ := raw["room"].(string)
		meta := map[string]string{
			"status":      store.StatusActive,
			"memory_type": "reference",
			"observed_at": observedAt,
			"ts":          observedAt,
		}
		for _, field := range []string{
			"ts", "observed_at", "occurred_at", "session_started_at", "session_ended_at",
			"last_seen_at", "source_modified_at", "consolidated_at", "consolidation_checked_at",
			"consolidation_first_empty_at", "superseded_at", "orphaned_at", "source_tokens", "memory_tokens",
			"consolidation_attempts", "provenance", "source_id", "source_kind", "source_key", "source_revision",
			"content_hash", "identity_version", "superseded_by", "superseded_reason", "status", "memory_type",
			"consolidation_status",
		} {
			if value := jsonMetadataString(raw[field]); value != "" {
				meta[field] = value
			}
		}
		jobs <- doc{text: text, wing: wing, room: room, meta: meta}
		n++
	}
	return n
}

func jsonMetadataString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

// feedMemFiles walks ~/.claude/projects/*/memory/*.md — the user's curated
// per-project native memory — chunking each file on paragraph boundaries.
// With sync, every file revision is imported first. Only after all new chunks
// are safely stored are older revisions marked superseded and vanished source
// files marked orphaned. No memory record is deleted.
func feedMemFiles(
	jobs chan<- doc, syncMode, force bool, known map[string]store.MemfileSourceState, now time.Time,
) (int, *memfileSyncPlan, error) {
	root := filepath.Join(os.Getenv("HOME"), ".claude", "projects")
	return feedMemFilesFromRoot(jobs, root, syncMode, force, known, now)
}

func feedMemFilesFromRoot(
	jobs chan<- doc, root string, syncMode, force bool, known map[string]store.MemfileSourceState, now time.Time,
) (int, *memfileSyncPlan, error) {
	n := 0
	var plan *memfileSyncPlan
	if syncMode {
		plan = &memfileSyncPlan{
			sources: map[string]memfileSource{}, known: known, wings: map[string]bool{},
		}
	}
	observedAt := strconv.FormatInt(now.Unix(), 10)
	//nolint:gosec // walks the user's own $HOME/.claude tree
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		wing, room, legacyRoom, sourceKey, ok := memfileIdentity(path)
		if !ok {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // under $HOME
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		revision := memfileRevision(b)
		if plan != nil {
			plan.sources[sourceKey] = memfileSource{
				wing: wing, room: room, legacyRoom: legacyRoom, revision: revision,
			}
			plan.wings[wing] = true
			if state, exists := known[sourceKey]; !force && exists && state.Wing == wing && state.Room == room &&
				state.Revisions[revision] {
				plan.skipped++
				return nil
			}
		}
		for _, chunk := range chunkMarkdown(string(b), 1200) {
			jobs <- doc{text: chunk, wing: wing, room: room, meta: map[string]string{
				"_legacy_room":       legacyRoom,
				"source_kind":        "memfile",
				"source_key":         sourceKey,
				"source_revision":    revision,
				"source_modified_at": strconv.FormatInt(info.ModTime().Unix(), 10),
				"last_seen_at":       observedAt,
				"status":             store.StatusActive,
				"memory_type":        "reference",
				"observed_at":        observedAt,
				"ts":                 observedAt,
			}}
			n++
		}
		return nil
	})
	return n, plan, err
}

func memfileIdentity(path string) (wing, room, legacyRoom, sourceKey string, ok bool) {
	normalized := filepath.ToSlash(path)
	const marker = "/projects/"
	i := strings.Index(normalized, marker)
	if i < 0 {
		return "", "", "", "", false
	}
	rest := normalized[i+len(marker):]
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[1] != nativeMemoryDir {
		return "", "", "", "", false
	}
	projectSlug := parts[0]
	relative := strings.Join(parts[2:], "/")
	if relative == "" {
		return "", "", "", "", false
	}
	wing = wingFromMemPath(path)
	room = memfileRoomPrefix + relative
	legacyRoom = memfileRoomPrefix + filepath.Base(path)
	sum := sha256.Sum256([]byte("ariadne-memfile-v2\x00" + projectSlug + "\x00" + relative))
	sourceKey = fmt.Sprintf("%x", sum[:16])
	return wing, room, legacyRoom, sourceKey, true
}

func memfileRevision(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:16])
}

func finalizeMemfileSync(ctx context.Context, st memfileSyncStore, plan *memfileSyncPlan, now time.Time) error {
	changedAt := strconv.FormatInt(now.Unix(), 10)
	for sourceKey, source := range plan.sources {
		if err := st.SetMetaBySourceKey(ctx, sourceKey, source.revision, map[string]string{
			"status":        store.StatusSuperseded,
			"superseded_at": changedAt,
		}); err != nil {
			return fmt.Errorf("archive old revision %s/%s: %w", source.wing, source.room, err)
		}
		for _, legacyRoom := range uniqueStrings(source.room, source.legacyRoom) {
			if err := st.SetMetaByWingRoomLegacy(ctx, source.wing, legacyRoom, map[string]string{
				"status":            store.StatusSuperseded,
				"superseded_at":     changedAt,
				"superseded_reason": "memfile-identity-v2",
			}); err != nil {
				return fmt.Errorf("archive legacy revision %s/%s: %w", source.wing, legacyRoom, err)
			}
		}
	}
	for sourceKey, state := range plan.known {
		if _, seen := plan.sources[sourceKey]; seen {
			continue
		}
		if err := st.SetMetaBySourceKey(ctx, sourceKey, "", map[string]string{
			"status": store.StatusOrphaned, "orphaned_at": changedAt,
		}); err != nil {
			return fmt.Errorf("archive orphan %s/%s: %w", state.Wing, state.Room, err)
		}
	}
	seenPairs := make(map[[2]string]string, len(plan.sources))
	for _, source := range plan.sources {
		seenPairs[[2]string{source.wing, source.room}] = source.revision
		seenPairs[[2]string{source.wing, source.legacyRoom}] = source.revision
	}
	if err := archiveOrphans(ctx, st, seenPairs, plan.wings, changedAt); err != nil {
		return err
	}
	if err := st.TouchActiveMemfiles(ctx, now.Unix()); err != nil {
		return fmt.Errorf("touch active memfiles: %w", err)
	}
	return nil
}

func uniqueStrings(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// archiveOrphans marks memory chunks whose source file vanished. Only wings
// that still have a live memory directory are touched; the records remain
// available through include_archived recall.
func archiveOrphans(
	ctx context.Context, st memfileSyncStore, seen map[[2]string]string, wings map[string]bool, changedAt string,
) error {
	pairs, err := st.WingRoomPairs(ctx)
	if err != nil {
		return fmt.Errorf("orphan scan: %w", err)
	}
	archived := 0
	for pair, cnt := range pairs {
		wing, room := pair[0], pair[1]
		if !strings.HasPrefix(room, memfileRoomPrefix) || !wings[wing] || seen[pair] != "" {
			continue
		}
		if err := st.SetMetaByWingRoom(ctx, wing, room, "", map[string]string{
			"status":      store.StatusOrphaned,
			"orphaned_at": changedAt,
		}); err != nil {
			return fmt.Errorf("archive orphan %s/%s: %w", wing, room, err)
		}
		fmt.Printf("  orphan archived: %s/%s (%d chunks)\n", wing, room, cnt)
		archived += cnt
	}
	if archived > 0 {
		fmt.Printf("  archived orphans total: %d chunks\n", archived)
	}
	return nil
}

// wingFromMemPath turns …/projects/-Users-…-Projects-MyApp/memory/x.md → "MyApp".
func wingFromMemPath(path string) string {
	i := strings.Index(path, "/projects/")
	if i < 0 {
		return nativeMemoryDir
	}
	rest := path[i+len("/projects/"):]
	end := strings.Index(rest, "/")
	if end < 0 {
		return nativeMemoryDir
	}
	slug := rest[:end]
	if j := strings.LastIndex(slug, "-Projects-"); j >= 0 {
		return slug[j+len("-Projects-"):]
	}
	return slug
}

// chunkMarkdown groups paragraphs into chunks up to ~max chars.
func chunkMarkdown(text string, max int) []string {
	paras := strings.Split(text, "\n\n")
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
		}
		cur.Reset()
	}
	for _, p := range paras {
		if cur.Len()+len(p) > max && cur.Len() > 0 {
			flush()
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
	}
	flush()
	// drop trivially short chunks
	out := chunks[:0]
	for _, c := range chunks {
		if len([]rune(c)) >= 40 {
			out = append(out, c)
		}
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
func fatal(a ...any) { fmt.Fprintln(os.Stderr, append([]any{"import:"}, a...)...); os.Exit(1) }
