package main

import (
	"ariadne/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodingMemoryEvaluation(t *testing.T) {
	cases, err := loadCases(filepath.Join("..", "..", "evaluation", "coding-memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		item := item
		t.Run(item.Name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, item.Now)
			if err != nil {
				t.Fatal(err)
			}
			got := store.Rerank(item.Query, item.Candidates, item.Limit, now)
			if len(got) == 0 || got[0].ID != item.WantFirst {
				t.Fatalf("want first id %d, got %#v", item.WantFirst, got)
			}
		})
	}
}

func TestRetrievalEvaluationFixture(t *testing.T) {
	path := filepath.Join("..", "..", "evaluation", "retrieval-runs.example.json")
	if err := runRetrieval(path, "bm25", false); err != nil {
		t.Fatal(err)
	}
	if err := runRetrieval(path, "missing", false); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
