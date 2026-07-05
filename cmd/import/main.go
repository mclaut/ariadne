// Command import backfills ariadne from an archived chromadb sqlite,
// re-embedding each stored document with bge-m3 into the Qdrant hybrid
// collection. Existing chroma chunks are imported as-is (already chunked);
// better chunking is a concern for NEW captures, not this backfill.
//
// Embedding is the bottleneck (~130 ms/doc), so documents are embedded by a
// worker pool. Content-hash ids make the whole import idempotent/resumable.
//
//	import [-db PATH] [-n LIMIT] [-workers N] [-skip-sessions]
package main

import (
	"ariadne/internal/store"
	"bufio"
	"context"
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
}

func main() {
	source := flag.String("source", "chroma", "chroma | memfiles | jsonl")
	db := flag.String("db", "", "chromadb sqlite path (required for -source chroma)")
	file := flag.String("file", "", "JSONL export file (for -source jsonl)")
	limit := flag.Int("n", 0, "max docs (0 = all)")
	workers := flag.Int("workers", 8, "parallel embed workers")
	skipSessions := flag.Bool("skip-sessions", false, "skip the raw-transcript 'sessions' wing")
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
	var done, failed atomic.Int64
	start := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				if _, err := st.Save(ctx, store.SanitizeUTF8(d.text),
					map[string]string{"wing": d.wing, "room": d.room}); err != nil {
					failed.Add(1)
					continue
				}
				if n := done.Add(1); n%500 == 0 {
					rate := float64(n) / time.Since(start).Seconds()
					fmt.Printf("  %d saved · %.0f/s · fail=%d\n", n, rate, failed.Load())
				}
			}
		}()
	}

	var feed int
	switch *source {
	case "memfiles":
		feed = feedMemFiles(jobs)
	case "jsonl":
		feed = feedJSONL(jobs, *file)
	default:
		feed = feedChroma(jobs, *db, *skipSessions, *limit)
	}
	close(jobs)
	wg.Wait()

	fmt.Printf("\n=== IMPORT DONE ===\n  fed=%d saved=%d failed=%d\n  wall=%s (%.0f docs/s)\n",
		feed, done.Load(), failed.Load(), time.Since(start).Round(time.Second),
		float64(done.Load())/time.Since(start).Seconds())
}

// feedChroma reads documents from the archived chromadb sqlite.
func feedChroma(jobs chan<- doc, dbPath string, skipSessions bool, limit int) int {
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
	if skipSessions {
		q += ` AND w.string_value NOT IN ('sessions')`
	}
	if limit > 0 {
		q += ` LIMIT ` + strconv.Itoa(limit) //nolint:gosec // limit is an int flag, not user text
	}
	rows, err := sq.QueryContext(context.Background(), q)
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var d struct{ Text, Wing, Room string }
		if err := json.Unmarshal([]byte(line), &d); err != nil || d.Text == "" {
			continue
		}
		jobs <- doc{text: d.Text, wing: d.Wing, room: d.Room}
		n++
	}
	return n
}

// feedMemFiles walks ~/.claude/projects/*/memory/*.md — the user's curated
// per-project native memory — chunking each file on paragraph boundaries.
func feedMemFiles(jobs chan<- doc) int {
	root := os.Getenv("HOME") + "/.claude/projects"
	n := 0
	//nolint:gosec // walks the user's own $HOME/.claude tree
	_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil //nolint:nilerr
		}
		if !strings.Contains(path, "/memory/") {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // under $HOME
		if err != nil {
			return nil //nolint:nilerr // skip unreadable files, keep walking
		}
		wing := wingFromMemPath(path)
		fname := filepath.Base(path)
		for _, chunk := range chunkMarkdown(string(b), 1200) {
			jobs <- doc{text: chunk, wing: wing, room: "memory:" + fname}
			n++
		}
		return nil
	})
	return n
}

// wingFromMemPath turns …/projects/-Users-…-Projects-MyApp/memory/x.md → "MyApp".
func wingFromMemPath(path string) string {
	i := strings.Index(path, "/projects/")
	if i < 0 {
		return "memory"
	}
	rest := path[i+len("/projects/"):]
	end := strings.Index(rest, "/")
	if end < 0 {
		return "memory"
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
