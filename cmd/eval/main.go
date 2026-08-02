// Command eval runs deterministic coding-memory ranking regressions. It does
// not contact Qdrant, Ollama, or any remote service and never mutates memory.
package main

import (
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
	flag.Parse()
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
