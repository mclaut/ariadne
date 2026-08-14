// Package retrievaleval compares ranked retrieval runs against explicit
// relevance judgments. It is deterministic and has no storage or model access.
package retrievaleval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// Benchmark contains relevance judgments and ordered result IDs for each run.
type Benchmark struct {
	Cutoffs []int       `json:"cutoffs"`
	Queries []QueryCase `json:"queries"`
}

// QueryCase is one judged query and the results returned by each retrieval run.
type QueryCase struct {
	Name     string              `json:"name"`
	Query    string              `json:"query"`
	Relevant []string            `json:"relevant"`
	Runs     map[string][]string `json:"runs"`
}

// Score contains macro-averaged binary-relevance metrics at one cutoff.
type Score struct {
	Run     string  `json:"run"`
	Cutoff  int     `json:"cutoff"`
	Queries int     `json:"queries"`
	Recall  float64 `json:"recall"`
	MRR     float64 `json:"mrr"`
	NDCG    float64 `json:"ndcg"`
}

// Load reads a benchmark from an explicit local path.
func Load(path string) (Benchmark, error) {
	body, err := os.ReadFile(path) //nolint:gosec // user-selected local evaluation fixture
	if err != nil {
		return Benchmark{}, err
	}
	var benchmark Benchmark
	if err := json.Unmarshal(body, &benchmark); err != nil {
		return Benchmark{}, err
	}
	if err := Validate(benchmark); err != nil {
		return Benchmark{}, fmt.Errorf("invalid benchmark %s: %w", path, err)
	}
	return benchmark, nil
}

// Validate rejects ambiguous or incomplete comparisons before scoring them.
func Validate(benchmark Benchmark) error {
	if len(benchmark.Cutoffs) == 0 {
		return errors.New("at least one cutoff is required")
	}
	seenCutoffs := map[int]bool{}
	for _, cutoff := range benchmark.Cutoffs {
		if cutoff <= 0 {
			return fmt.Errorf("cutoff must be positive: %d", cutoff)
		}
		if seenCutoffs[cutoff] {
			return fmt.Errorf("duplicate cutoff: %d", cutoff)
		}
		seenCutoffs[cutoff] = true
	}
	if len(benchmark.Queries) == 0 {
		return errors.New("at least one query is required")
	}
	var runNames map[string]bool
	seenNames := map[string]bool{}
	for i, query := range benchmark.Queries {
		if strings.TrimSpace(query.Name) == "" {
			return fmt.Errorf("query %d has no name", i+1)
		}
		if seenNames[query.Name] {
			return fmt.Errorf("duplicate query name: %s", query.Name)
		}
		seenNames[query.Name] = true
		if strings.TrimSpace(query.Query) == "" {
			return fmt.Errorf("query %q has no text", query.Name)
		}
		if len(unique(query.Relevant)) != len(query.Relevant) || len(query.Relevant) == 0 {
			return fmt.Errorf("query %q needs unique relevance judgments", query.Name)
		}
		currentRuns := map[string]bool{}
		for name, ids := range query.Runs {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("query %q has an unnamed run", query.Name)
			}
			if len(unique(ids)) != len(ids) {
				return fmt.Errorf("query %q run %q contains duplicate result IDs", query.Name, name)
			}
			currentRuns[name] = true
		}
		if len(currentRuns) < 2 {
			return fmt.Errorf("query %q needs at least two runs", query.Name)
		}
		if runNames == nil {
			runNames = currentRuns
			continue
		}
		if !sameSet(runNames, currentRuns) {
			return fmt.Errorf("query %q has a different run set", query.Name)
		}
	}
	return nil
}

// Evaluate returns stable run/cutoff ordering for reproducible reports.
func Evaluate(benchmark Benchmark) ([]Score, error) {
	if err := Validate(benchmark); err != nil {
		return nil, err
	}
	runs := make([]string, 0, len(benchmark.Queries[0].Runs))
	for run := range benchmark.Queries[0].Runs {
		runs = append(runs, run)
	}
	sort.Strings(runs)
	cutoffs := append([]int(nil), benchmark.Cutoffs...)
	sort.Ints(cutoffs)
	scores := make([]Score, 0, len(runs)*len(cutoffs))
	for _, run := range runs {
		for _, cutoff := range cutoffs {
			score := Score{Run: run, Cutoff: cutoff, Queries: len(benchmark.Queries)}
			for _, query := range benchmark.Queries {
				recall, reciprocalRank, ndcg := queryMetrics(query.Relevant, query.Runs[run], cutoff)
				score.Recall += recall
				score.MRR += reciprocalRank
				score.NDCG += ndcg
			}
			count := float64(score.Queries)
			score.Recall /= count
			score.MRR /= count
			score.NDCG /= count
			scores = append(scores, score)
		}
	}
	return scores, nil
}

func queryMetrics(relevantIDs, rankedIDs []string, cutoff int) (recall, reciprocalRank, ndcg float64) {
	relevant := map[string]bool{}
	for _, id := range relevantIDs {
		relevant[id] = true
	}
	limit := min(cutoff, len(rankedIDs))
	hits := 0
	for i, id := range rankedIDs[:limit] {
		if !relevant[id] {
			continue
		}
		hits++
		if reciprocalRank == 0 {
			reciprocalRank = 1 / float64(i+1)
		}
		ndcg += 1 / math.Log2(float64(i+2))
	}
	recall = float64(hits) / float64(len(relevant))
	idealHits := min(cutoff, len(relevant))
	ideal := 0.0
	for i := range idealHits {
		ideal += 1 / math.Log2(float64(i+2))
	}
	if ideal > 0 {
		ndcg /= ideal
	}
	return recall, reciprocalRank, ndcg
}

func unique(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out[value] = true
		}
	}
	return out
}

func sameSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}
