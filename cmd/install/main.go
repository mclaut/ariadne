//go:build !windows

// Command install sets up the Ariadne runtime on macOS or Linux (console).
//
// It is deliberately careful with EXISTING infrastructure:
//   - an already-running Qdrant is REUSED, never stopped or reconfigured —
//     Ariadne only adds its own collection (see -qdrant-* flags for remotes);
//   - a busy port that is NOT Qdrant aborts the install;
//   - GPU / RAM / disk are checked up front, and insufficiencies are stated
//     plainly (hard FAILs abort unless -force).
//
// Run from the repo root:
//
//	go run ./cmd/install -dry-run     # preflight + plan only, changes nothing
//	go run ./cmd/install -yes         # do it
package main

import (
	"ariadne/internal/version"
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

type opts struct {
	yes, dryRun, force bool
	qdrantHost         string
	qdrantREST         int
	qdrantGRPC         int
	qdrantVersion      string
	ollamaURL          string
	model              string
	summaryModel       string
	collection         string
	skipModel          bool
	skipHooks          bool
	skipDeps           bool
	strictSupplyChain  bool
}

// remoteOllama reports whether -ollama points at a non-local box.
func (o opts) remoteOllama() bool {
	return !strings.Contains(o.ollamaURL, "localhost") && !strings.Contains(o.ollamaURL, "127.0.0.1")
}

func parseFlags() opts {
	var o opts
	flag.BoolVar(&o.yes, "yes", false, "no confirmation prompt")
	flag.BoolVar(&o.dryRun, "dry-run", false, "preflight + plan only, change nothing")
	flag.BoolVar(&o.force, "force", false, "proceed despite FAIL verdicts (not recommended)")
	flag.StringVar(&o.qdrantHost, "qdrant-host", "127.0.0.1", "Qdrant host (set to reuse a remote instance)")
	flag.IntVar(&o.qdrantREST, "qdrant-rest", 6333, "Qdrant REST port")
	flag.IntVar(&o.qdrantGRPC, "qdrant-grpc", 6334, "Qdrant gRPC port")
	flag.StringVar(&o.qdrantVersion, "qdrant-version", "v1.18.2", "pinned Qdrant release tag to install")
	flag.StringVar(&o.ollamaURL, "ollama", "http://localhost:11434", "Ollama URL (set to reuse a remote/GPU box)")
	flag.StringVar(&o.model, "model", "bge-m3", "embedding model")
	flag.StringVar(&o.summaryModel, "summary-model", "qwen2.5:7b",
		"local chat model for session-capture summaries (e.g. qwen2.5:3b ~2GB for less RAM, lower quality)")
	flag.StringVar(&o.collection, "collection", "ariadne", "Qdrant collection name")
	flag.BoolVar(&o.skipModel, "skip-model-pull", false, "do not pull models")
	flag.BoolVar(&o.skipHooks, "skip-hooks", false, "do not register Claude Code session hooks (auto-recall/auto-capture)")
	flag.BoolVar(&o.skipDeps, "skip-deps", false, "do not auto-install OS prerequisites (Linux: tray libs + Ollama)")
	flag.BoolVar(&o.strictSupplyChain, "strict-supply-chain", false, "avoid curl|sh installers; require manual deps where needed")
	flag.Parse()
	return o
}

func main() {
	o := parseFlags()
	fmt.Printf("== Ariadne installer %s ==\n", version.Tag)

	ensureDeps(o) // Linux: auto-install tray libs + Ollama BEFORE preflight sees them

	pf := preflight(o)
	printReport(pf)
	if pf.fatal() && !o.force {
		fmt.Println("\nABORTED: fix the ✗ items above (or re-run with -force if you accept the consequences).")
		os.Exit(1)
	}

	plan := makePlan(pf, o)
	fmt.Println("\n[plan]")
	for _, a := range plan {
		fmt.Printf("  %s %s\n", pick(a.skip, "SKIP", "  DO"), a.title)
	}
	if o.dryRun {
		fmt.Println("\n(dry-run: nothing changed)")
		return
	}
	if !o.yes && !confirm("Proceed?") {
		fmt.Println("aborted")
		return
	}

	fmt.Println("\n[install]")
	for _, a := range plan {
		if a.skip {
			continue
		}
		fmt.Printf("  → %s\n", a.title)
		if err := a.run(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", a.title, err)
			os.Exit(1)
		}
	}

	fmt.Println("\n[verify]")
	if !verify(o) {
		fmt.Println("\nINSTALL FINISHED WITH PROBLEMS — see ✗ above.")
		os.Exit(1)
	}
	fmt.Println("\nINSTALL OK ✓  (restart your Claude Code session to pick up the MCP tools + skill)")
	if pf.qdrant.state == qdForeign {
		fmt.Println("note: reusing a foreign Qdrant — `ariadnectl start/stop` will NOT manage it (by design).")
	}
}

type action struct {
	title string
	skip  bool
	run   func() error
}

func confirm(msg string) bool {
	fmt.Printf("%s [y/N] ", msg)
	sc := bufio.NewScanner(os.Stdin)
	return sc.Scan() && strings.EqualFold(strings.TrimSpace(sc.Text()), "y")
}

func pick(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}
