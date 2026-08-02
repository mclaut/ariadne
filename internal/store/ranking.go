package store

import (
	"ariadne/internal/metrics"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Rerank applies small, explainable historical-quality adjustments after
// Qdrant's dense+BM25 RRF. The bounded adjustment keeps semantic relevance in
// control while letting an explicitly temporal query prefer the right dated
// record and discouraging oversized legacy payloads.
func Rerank(query string, candidates []Result, limit int, now time.Time) []Result {
	if limit <= 0 {
		limit = 5
	}
	out := slices.Clone(candidates)
	temporal := temporalQuery(query)
	for i := range out {
		adjustment := provenanceBoost(out[i].Provenance)
		if temporal {
			adjustment += recencyBoost(memoryTimestamp(out[i]), now)
		}
		adjustment -= sizePenalty(out[i])
		// The combined metadata adjustment is deliberately capped. A result
		// with materially stronger hybrid relevance must remain ahead.
		adjustment = math.Max(-0.02, math.Min(0.04, adjustment))
		out[i].Score += float32(adjustment)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func temporalQuery(query string) bool {
	q := strings.ToLower(query)
	for _, marker := range []string{
		"latest", "current", "recent", "today", "yesterday", "tomorrow", "when ", "last ", "newest",
		"зараз", "поточ", "остан", "свіж", "сьогод", "вчора", "завтра", "коли ", "нещодав",
		"aktuell", "letzte", "heute", "gestern", "quando ", "oggi", "recente",
		"actual", "últim", "hoy", "ayer", "récent", "aujourd", "hier ", "ostatn", "dzisiaj", "wczoraj",
	} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	return false
}

func memoryTimestamp(result Result) int64 {
	if result.OccurredAt > 0 {
		return result.OccurredAt
	}
	return result.ObservedAt
}

func recencyBoost(ts int64, now time.Time) float64 {
	if ts <= 0 || now.IsZero() {
		return 0
	}
	age := now.Sub(time.Unix(ts, 0))
	if age < 0 {
		age = 0
	}
	days := age.Hours() / 24
	// 0.025 now, 0.0125 after 30 days, and progressively less after
	// that. There is no general age decay: this applies only when the query
	// itself asks for current or historical ordering.
	return 0.025 / (1 + days/30)
}

func provenanceBoost(provenance string) float64 {
	switch provenance {
	case "capture", "consolidate", "manual-measured":
		return 0.010
	case "manual":
		return 0.006
	case "import":
		return 0.002
	default:
		return 0
	}
}

func sizePenalty(result Result) float64 {
	tokens := result.MemoryTokens
	if tokens <= 0 {
		tokens = metrics.EstimateTokens(result.Text)
	}
	if tokens <= 600 {
		return 0
	}
	penalty := float64(tokens-600) / 100_000
	return math.Min(0.015, penalty)
}
