// Command eval runs deterministic coding-memory ranking regressions. It does
// not contact Qdrant, Ollama, or any remote service and never mutates memory.
package main

import (
	"ariadne/internal/retrievaleval"
	"ariadne/internal/store"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

type evalCase struct {
	Name       string         `json:"name"`
	Query      string         `json:"query"`
	Now        string         `json:"now"`
	Limit      int            `json:"limit"`
	WantFirst  uint64         `json:"want_first"`
	Candidates []store.Result `json:"candidates"`
}

func main() {
	path := flag.String("cases", "evaluation/coding-memory.json", "ranking evaluation cases")
	retrievalRuns := flag.String("retrieval-runs", "", "judged ranked runs for retrieval comparison")
	baseline := flag.String("baseline", "bm25", "baseline run name for retrieval comparison")
	asJSON := flag.Bool("json", false, "emit retrieval scores as JSON")
	flag.Parse()
	if *retrievalRuns != "" {
		if err := runRetrieval(*retrievalRuns, *baseline, *asJSON); err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
			os.Exit(2)
		}
		return
	}
	cases, err := loadCases(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(2)
	}
	passed := 0
	for _, item := range cases {
		now, err := time.Parse(time.RFC3339, item.Now)
		if err != nil {
			fmt.Printf("FAIL %-56s invalid now: %v\n", item.Name, err)
			continue
		}
		got := store.Rerank(item.Query, item.Candidates, item.Limit, now)
		if len(got) > 0 && got[0].ID == item.WantFirst {
			passed++
			fmt.Printf("PASS %s\n", item.Name)
			continue
		}
		var gotID uint64
		if len(got) > 0 {
			gotID = got[0].ID
		}
		fmt.Printf("FAIL %-56s want=%d got=%d\n", item.Name, item.WantFirst, gotID)
	}
	fmt.Printf("\n%d/%d ranking cases passed\n", passed, len(cases))
	if passed != len(cases) {
		os.Exit(1)
	}
}

func runRetrieval(path, baseline string, asJSON bool) error {
	benchmark, err := retrievaleval.Load(path)
	if err != nil {
		return err
	}
	scores, err := retrievaleval.Evaluate(benchmark)
	if err != nil {
		return err
	}
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(scores)
	}
	baselines := map[int]retrievaleval.Score{}
	for _, score := range scores {
		if score.Run == baseline {
			baselines[score.Cutoff] = score
		}
	}
	if len(baselines) == 0 {
		return fmt.Errorf("baseline run %q is absent", baseline)
	}
	fmt.Printf("%-20s %4s %8s %8s %8s %9s\n", "RUN", "K", "RECALL", "MRR", "NDCG", "ΔNDCG")
	for _, score := range scores {
		base, ok := baselines[score.Cutoff]
		if !ok {
			return fmt.Errorf("baseline run %q has no score at k=%d", baseline, score.Cutoff)
		}
		delta := score.NDCG - base.NDCG
		fmt.Printf("%-20s %4d %7.2f%% %7.2f%% %7.2f%% %+8.2f%%\n",
			score.Run, score.Cutoff, score.Recall*100, score.MRR*100, score.NDCG*100, delta*100)
	}
	fmt.Printf("\n%d judged queries; binary relevance; macro averages\n", len(benchmark.Queries))
	return nil
}

func loadCases(path string) ([]evalCase, error) {
	body, err := os.ReadFile(path) //nolint:gosec // explicit local fixture path
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	if err := json.Unmarshal(body, &cases); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases in %s", path)
	}
	return cases, nil
}
