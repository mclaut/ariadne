// Command ariadne-hook wires Ariadne into Claude Code lifecycle hooks.
//
//	session-start  stdin: SessionStart hook JSON → stdout: additionalContext
//	               with the project's top memories. Recall-only (never writes);
//	               stays silent when the project has no memories, and NEVER
//	               blocks the session (all failures → empty output, exit 0).
//	session-end    stdin: SessionEnd hook JSON → detaches capture-run and
//	               returns immediately (session exit is never blocked).
//	capture-run    the detached worker: summarize the transcript with a local
//	               Ollama chat model and save ONE curated diary memory —
//	               never the raw transcript.
//
// Config via env (defaults in brackets):
//
//	ARIADNE_CAPTURE            "0" disables capture [on]
//	ARIADNE_SUMMARY_MODEL      Ollama chat model for summaries [qwen2.5:7b]
//	ARIADNE_CAPTURE_MIN_TURNS  min user turns worth capturing [3]
//	ARIADNE_QDRANT_HOST/PORT, ARIADNE_OLLAMA, ARIADNE_MODEL, ARIADNE_COLLECTION
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ariadne-hook {session-start | session-end | capture-run}")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "session-start":
		sessionStart() // never fails the session: errors → exit 0, empty stdout
	case "session-end":
		sessionEnd()
	case "capture-run":
		captureRun(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
