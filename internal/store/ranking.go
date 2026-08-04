package store

import (
	"ariadne/internal/metrics"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const CrossWingWeight = 0.70

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

// RerankCrossWing applies an origin penalty after normal semantic/historical
// ranking and reserves a bounded share of the response for approved external
// projects. The permission check happens before this function; weighting is a
// relevance policy, never an access-control mechanism.
func RerankCrossWing(candidates []Result, homeWing string, limit int) []Result {
	if limit <= 0 {
		limit = 5
	}
	local := make([]Result, 0, len(candidates))
	remote := make([]Result, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Wing == homeWing {
			local = append(local, candidate)
			continue
		}
		candidate.Score *= CrossWingWeight
		remote = append(remote, candidate)
	}
	scoreSort := func(items []Result) {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	}
	scoreSort(local)
	scoreSort(remote)
	remoteSlots := min(2, (limit+1)/2)
	localSlots := limit - remoteSlots
	selected := make([]Result, 0, min(limit, len(candidates)))
	localTake := min(localSlots, len(local))
	remoteTake := min(remoteSlots, len(remote))
	selected = append(selected, local[:localTake]...)
	selected = append(selected, remote[:remoteTake]...)
	leftovers := append(slices.Clone(local[localTake:]), remote[remoteTake:]...)
	scoreSort(leftovers)
	for _, candidate := range leftovers {
		if len(selected) == limit {
			break
		}
		selected = append(selected, candidate)
	}
	scoreSort(selected)
	return selected
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
