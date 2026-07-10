package main

import (
	"strings"
	"testing"
)

func TestWingFromMemPathProjectsSlug(t *testing.T) {
	path := "/home/me/.claude/projects/-Users-me-Projects-Ariadne/memory/notes.md"
	if got := wingFromMemPath(path); got != "Ariadne" {
		t.Fatalf("wing = %q", got)
	}
}

func TestWingFromMemPathFallback(t *testing.T) {
	if got := wingFromMemPath("/tmp/notes.md"); got != "memory" {
		t.Fatalf("wing = %q", got)
	}
}

func TestChunkMarkdownGroupsParagraphsAndDropsTinyChunks(t *testing.T) {
	text := strings.Join([]string{
		"short",
		"This paragraph is long enough to survive chunk filtering.",
		"Another useful paragraph that should also survive filtering.",
	}, "\n\n")
	chunks := chunkMarkdown(text, 70)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %#v", chunks)
	}
	if !strings.Contains(chunks[0], "This paragraph") || !strings.Contains(chunks[1], "Another useful") {
		t.Fatalf("chunks = %#v", chunks)
	}
	if got := chunkMarkdown("tiny", 70); len(got) != 0 {
		t.Fatalf("tiny standalone paragraph should be dropped: %#v", got)
	}
}
