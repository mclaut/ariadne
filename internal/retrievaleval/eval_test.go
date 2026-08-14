package retrievaleval

import (
	"math"
	"testing"
)

func TestEvaluateComparesRankedRuns(t *testing.T) {
	benchmark := Benchmark{
		Cutoffs: []int{3, 1},
		Queries: []QueryCase{
			{Name: "q1", Query: "first", Relevant: []string{"a", "b"}, Runs: map[string][]string{
				"bm25": {"x", "a", "b"}, "learned_sparse": {"a", "b", "x"},
			}},
			{Name: "q2", Query: "second", Relevant: []string{"d"}, Runs: map[string][]string{
				"bm25": {"x", "d"}, "learned_sparse": {"d", "x"},
			}},
		},
	}
	scores, err := Evaluate(benchmark)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 4 || scores[0].Run != "bm25" || scores[0].Cutoff != 1 ||
		scores[2].Run != "learned_sparse" || scores[2].Cutoff != 1 {
		t.Fatalf("score order = %#v", scores)
	}
	if scores[0].Recall != 0 || scores[2].Recall != 0.75 || scores[2].MRR != 1 || scores[2].NDCG != 1 {
		t.Fatalf("scores = %#v", scores)
	}
	if math.Abs(scores[1].Recall-1) > 1e-9 || math.Abs(scores[1].MRR-0.5) > 1e-9 {
		t.Fatalf("bm25@3 = %#v", scores[1])
	}
}

func TestValidateRejectsUnfairRunSetsAndDuplicates(t *testing.T) {
	benchmark := Benchmark{Cutoffs: []int{5}, Queries: []QueryCase{
		{Name: "q1", Query: "first", Relevant: []string{"a"}, Runs: map[string][]string{
			"bm25": {"a", "a"}, "learned_sparse": {"a"},
		}},
	}}
	if err := Validate(benchmark); err == nil {
		t.Fatal("duplicate results accepted")
	}
	benchmark.Queries[0].Runs["bm25"] = []string{"a"}
	benchmark.Queries = append(benchmark.Queries, QueryCase{
		Name: "q2", Query: "second", Relevant: []string{"b"},
		Runs: map[string][]string{"bm25": {"b"}, "other": {"b"}},
	})
	if err := Validate(benchmark); err == nil {
		t.Fatal("mismatched run set accepted")
	}
}
