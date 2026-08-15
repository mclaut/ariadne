package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/metrics"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Attribution classes explain the coverage number instead of leaving it as one
// opaque percentage: "attributed" memories carry measured source metadata,
// "gap" memories were written by a mechanism that supports it (captures,
// consolidations, manual saves, diaries) but lack the metadata — fixable by
// backfill and save-time discipline — and "sourceless" memories (file-note
// chunks, legacy imports) have no recoverable source by nature.
const (
	classAttributed = "attributed"
	classGap        = "gap"
	classSourceless = "sourceless"

	attributionEstimateVersion = "v1"
)

func attributionClass(room, provenance string, sourceTokens int64) string {
	if sourceTokens > 0 {
		return classAttributed
	}
	switch provenance {
	case "capture", "consolidate", "manual", "manual-measured":
		return classGap
	}
	if room == "diary" {
		return classGap
	}
	return classSourceless
}

type attributionBucket struct {
	Memories int64 `json:"memories"`
	Tokens   int64 `json:"memory_tokens"`
}

type attributionProfile struct {
	ScannedPoints               int64             `json:"scanned_points"`
	Attributed                  attributionBucket `json:"attributed"`
	AttributableGap             attributionBucket `json:"attributable_gap"`
	GapDiaryMemories            int64             `json:"gap_diary_memories"`
	GapManualMemories           int64             `json:"gap_manual_memories"`
	Sourceless                  attributionBucket `json:"sourceless"`
	AttributableCoveragePercent float64           `json:"attributable_coverage_percent"`
}

type attributionPoint struct {
	ID           uint64
	Room         string
	Provenance   string
	Status       string
	SourceTokens int64
	MemoryTokens int64
	Text         string
}

// pointTokens is the delivery mass of one memory: the stored estimate when
// present, otherwise re-estimated from the text.
func pointTokens(p attributionPoint) int64 {
	if p.MemoryTokens > 0 {
		return p.MemoryTokens
	}
	return metrics.EstimateTokens(p.Text)
}

func computeAttributionProfile(points []attributionPoint) attributionProfile {
	var out attributionProfile
	for _, p := range points {
		out.ScannedPoints++
		tokens := pointTokens(p)
		switch attributionClass(p.Room, p.Provenance, p.SourceTokens) {
		case classAttributed:
			out.Attributed.Memories++
			out.Attributed.Tokens += tokens
		case classGap:
			out.AttributableGap.Memories++
			out.AttributableGap.Tokens += tokens
			if p.Room == "diary" || p.Provenance == "capture" || p.Provenance == "consolidate" {
				out.GapDiaryMemories++
			} else {
				out.GapManualMemories++
			}
		default:
			out.Sourceless.Memories++
			out.Sourceless.Tokens += tokens
		}
	}
	if denom := out.Attributed.Tokens + out.AttributableGap.Tokens; denom > 0 {
		out.AttributableCoveragePercent = float64(out.Attributed.Tokens) * 100 / float64(denom)
	}
	return out
}

// scrollAttributionPoints pages through the collection with a payload-only
// scroll. filter may be nil for a full scan.
func scrollAttributionPoints(ctx context.Context, filter map[string]any) ([]attributionPoint, error) {
	var points []attributionPoint
	var offset any
	for {
		reqBody := map[string]any{
			"limit":        2048,
			"with_payload": []string{"room", "provenance", "status", "source_tokens", "memory_tokens", "text"},
			"with_vector":  false,
		}
		if filter != nil {
			reqBody["filter"] = filter
		}
		if offset != nil {
			reqBody["offset"] = offset
		}
		body, _ := json.Marshal(reqBody)
		req, err := newQdrantRequest(ctx, http.MethodPost,
			strings.TrimRight(qdrantREST, "/")+"/collections/"+url.PathEscape(collection)+"/points/scroll",
			bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Result struct {
				Points []struct {
					ID      uint64         `json:"id"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("qdrant HTTP %s", resp.Status)
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, p := range out.Result.Points {
			point := attributionPoint{ID: p.ID}
			point.Room, _ = p.Payload["room"].(string)
			point.Provenance, _ = p.Payload["provenance"].(string)
			point.Status, _ = p.Payload["status"].(string)
			if v, ok := p.Payload["source_tokens"].(float64); ok {
				point.SourceTokens = int64(v)
			}
			if v, ok := p.Payload["memory_tokens"].(float64); ok {
				point.MemoryTokens = int64(v)
			}
			point.Text, _ = p.Payload["text"].(string)
			points = append(points, point)
		}
		if out.Result.NextPageOffset == nil {
			return points, nil
		}
		offset = out.Result.NextPageOffset
	}
}

func loadAttributionProfile(ctx context.Context) (attributionProfile, error) {
	points, err := scrollAttributionPoints(ctx, nil)
	if err != nil {
		return attributionProfile{}, err
	}
	return computeAttributionProfile(points), nil
}

func printAttributionProfile(p attributionProfile) {
	fmt.Printf("Corpus attribution (%d memories)\n", p.ScannedPoints)
	fmt.Printf("  attributed:       %d memories · %d tokens\n", p.Attributed.Memories, p.Attributed.Tokens)
	fmt.Printf("  attributable gap: %d memories · %d tokens\n", p.AttributableGap.Memories, p.AttributableGap.Tokens)
	fmt.Printf("      fixable: %d diary (backfill-attribution) · %d manual saves (pass source_tokens when saving)\n",
		p.GapDiaryMemories, p.GapManualMemories)
	fmt.Printf("  sourceless:       %d memories · %d tokens (file notes / legacy imports — no recoverable source)\n",
		p.Sourceless.Memories, p.Sourceless.Tokens)
	fmt.Printf("  coverage of attributable corpus: %.1f%%\n", p.AttributableCoveragePercent)
}

// backfillSourceTokens returns the source/memory token pair a diary point
// receives: the memory mass times a conservative measured compression ratio.
func backfillSourceTokens(p attributionPoint, multiplier int64) (source, memory int64) {
	memory = pointTokens(p)
	if memory <= 0 {
		return 0, 0
	}
	return memory * multiplier, memory
}

// backfillAttributionCmd stamps estimated source attribution onto legacy diary
// entries that predate the metrics feature. Only room=diary is eligible: diary
// text is a measured ~8x compression of a real session, so a conservative
// multiplier is defensible there and nowhere else. The estimate is labelled in
// the payload (attribution_estimate) so audits can tell measured from inferred.
func backfillAttributionCmd(args []string) int {
	fs := flag.NewFlagSet("backfill-attribution", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	multiplier := fs.Int64("multiplier", 8, "conservative source-to-memory compression ratio (2..32)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *multiplier < 2 || *multiplier > 32 {
		fmt.Fprintln(os.Stderr, "backfill-attribution: --multiplier must be between 2 and 32")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	filter := map[string]any{
		"must": []any{
			map[string]any{"key": "room", "match": map[string]any{"value": "diary"}},
		},
		"must_not": []any{
			map[string]any{"key": "source_tokens", "range": map[string]any{"gt": 0}},
		},
	}
	points, err := scrollAttributionPoints(ctx, filter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-attribution: scroll:", err)
		return 1
	}
	if len(points) == 0 {
		fmt.Println("backfill-attribution: nothing to do — every diary entry already carries source metadata")
		return 0
	}
	var claimable, skippedEmpty int64
	eligible := make([]attributionPoint, 0, len(points))
	for _, p := range points {
		source, _ := backfillSourceTokens(p, *multiplier)
		if source <= 0 {
			skippedEmpty++
			continue
		}
		claimable += source
		eligible = append(eligible, p)
	}
	fmt.Printf("backfill-attribution: %d legacy diary entries lack source metadata (%d empty skipped)\n",
		len(eligible), skippedEmpty)
	fmt.Printf("  would stamp source_tokens = memory_tokens x%d (estimated ~%d source tokens total, marker %q)\n",
		*multiplier, claimable, estimateMarker(*multiplier))
	if *dryRun || len(eligible) == 0 {
		return 0
	}
	st, err := consolidationStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-attribution: store:", err)
		return 1
	}
	defer func() { _ = st.Close() }()
	updated := int64(0)
	for _, p := range eligible {
		source, memory := backfillSourceTokens(p, *multiplier)
		if err := st.SetMetaByIDs(ctx, []uint64{p.ID}, map[string]string{
			"source_tokens":        strconv.FormatInt(source, 10),
			"memory_tokens":        strconv.FormatInt(memory, 10),
			"attribution_estimate": estimateMarker(*multiplier),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "backfill-attribution: %d: %v\n", p.ID, err)
			return 1
		}
		updated++
	}
	_ = activity.Append(activity.Event{Operation: "backfill_attribution", Status: "complete", Counters: map[string]int64{
		"records":                 updated,
		"estimated_source_tokens": claimable,
	}})
	fmt.Printf("backfill-attribution: stamped %d diary entries\n", updated)
	return 0
}

func estimateMarker(multiplier int64) string {
	return fmt.Sprintf("x%d-%s", multiplier, attributionEstimateVersion)
}
