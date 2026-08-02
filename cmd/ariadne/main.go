// Command ariadne is a Model Context Protocol server that gives Claude Code a
// long-lived, native, hybrid-search memory backed by Qdrant + bge-m3.
//
// It is a SERVER — the whole single-writer/lock-starvation class of embedded
// vector stores is gone: Qdrant itself handles concurrent access.
// Tools: memory_save, memory_recall, memory_delete, memory_move.
//
// Config via env (defaults match the local native POC):
//
//	ARIADNE_QDRANT_HOST  localhost
//	ARIADNE_QDRANT_PORT  6334
//	ARIADNE_OLLAMA       http://localhost:11434
//	ARIADNE_MODEL        bge-m3
//	ARIADNE_COLLECTION   ariadne
package main

import (
	"ariadne/internal/metrics"
	"ariadne/internal/store"
	"ariadne/internal/version"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	port, _ := strconv.Atoi(env("ARIADNE_QDRANT_PORT", "6334"))
	st, err := store.New(
		env("ARIADNE_QDRANT_HOST", "localhost"), port,
		env("ARIADNE_OLLAMA", "http://localhost:11434"),
		env("ARIADNE_MODEL", "bge-m3"),
		env("ARIADNE_COLLECTION", "ariadne"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ariadne: store init:", err)
		os.Exit(1)
	}
	if err := st.EnsureCollection(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "ariadne: ensure collection:", err)
		os.Exit(1)
	}

	s := server.NewMCPServer("ariadne", version.Current,
		server.WithToolCapabilities(false))
	metricsSession := metrics.UniqueEventID()

	s.AddTool(mcp.NewTool("memory_recall",
		mcp.WithDescription("Recall memories semantically (hybrid dense+keyword, multilingual) "+
			"or retrieve one exact memory by id. Provide query or id."),
		mcp.WithString("query",
			mcp.Description("What to recall — keywords or a question, any language. Omit when id is given.")),
		mcp.WithString("id", mcp.Description("Exact memory id; bypasses semantic search.")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 5).")),
		mcp.WithString("wing", mcp.Description("Optional: narrow to one project/namespace.")),
		mcp.WithString("room", mcp.Description("Optional: narrow to one category, e.g. "+
			"'decisions', 'gotchas', 'reference', or 'diary'.")),
		mcp.WithBoolean("include_archived", mcp.Description("Include archived, superseded, and orphaned "+
			"records for history/audit searches (default false).")),
		mcp.WithString("collection", mcp.Description("Optional: search a non-default collection, "+
			"e.g. 'sessions' for the raw session archive.")),
	), recallHandler(st, metricsSession))

	s.AddTool(mcp.NewTool("memory_save",
		mcp.WithDescription("Save a memory (verbatim fact, decision, or context) for future recall. "+
			"Content is embedded and stored; identical text is deduplicated."),
		mcp.WithString("text", mcp.Required(), mcp.Description("The memory content, verbatim.")),
		mcp.WithString("wing", mcp.Description("Project/namespace, e.g. 'myapp'.")),
		mcp.WithString("room", mcp.Description("Aspect, e.g. 'decisions', 'diary'.")),
		mcp.WithNumber("source_tokens", mcp.Description("Optional measured or conservative size of the bounded "+
			"source context condensed into this memory. Omit rather than guess; never send the raw source.")),
	), saveHandler(st))

	s.AddTool(mcp.NewTool("memory_delete",
		mcp.WithDescription("Delete ONE memory by its id (from memory_recall). Irreversible — "+
			"use only for a memory the user asked to remove, or one that is clearly wrong or "+
			"superseded. Recall first to get the exact id and confirm it's the right one."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The memory id shown by memory_recall.")),
	), deleteHandler(st))

	s.AddTool(mcp.NewTool("memory_move",
		mcp.WithDescription("Re-home or re-tag ONE memory without changing its text: set a new "+
			"wing (project/namespace) and/or room (aspect). Get the id from memory_recall. "+
			"At least one of wing/room must be given."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The memory id shown by memory_recall.")),
		mcp.WithString("wing", mcp.Description("New project/namespace (omit to keep the current one).")),
		mcp.WithString("room", mcp.Description("New aspect/room (omit to keep the current one).")),
	), moveHandler(st))

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "ariadne: serve:", err)
		os.Exit(1)
	}
}

func recallHandler(st *store.Store, metricsSession string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		collection := req.GetString("collection", "")
		requestParts := make([]string, 0, 4)
		var hits []store.Result
		if rawID := strings.TrimSpace(req.GetString("id", "")); rawID != "" {
			requestParts = append(requestParts, "id="+rawID)
			id, err := strconv.ParseUint(rawID, 10, 64)
			if err != nil {
				return mcp.NewToolResultError("bad id: " + err.Error()), nil //nolint:nilerr
			}
			hit, ok, err := st.GetByID(ctx, id, collection)
			if err != nil {
				return mcp.NewToolResultError("recall by id failed: " + err.Error()), nil //nolint:nilerr
			}
			if ok {
				hits = []store.Result{hit}
			}
		} else {
			query := strings.TrimSpace(req.GetString("query", ""))
			if query == "" {
				return mcp.NewToolResultError("query or id is required"), nil
			}
			limit := req.GetInt("limit", 5)
			requestParts = append(requestParts, "query="+query, fmt.Sprintf("limit=%d", limit))
			var err error
			hits, err = st.Recall(ctx, query, limit, req.GetString("wing", ""),
				req.GetString("room", ""), collection, req.GetBool("include_archived", false))
			if err != nil {
				return mcp.NewToolResultError("recall failed: " + err.Error()), nil //nolint:nilerr
			}
		}
		if req.GetBool("include_archived", false) {
			requestParts = append(requestParts, "include_archived=true")
		}
		for _, scope := range []struct{ name, value string }{
			{"wing", req.GetString("wing", "")},
			{"room", req.GetString("room", "")},
			{"collection", collection},
		} {
			if scope.value != "" {
				requestParts = append(requestParts, scope.name+"="+scope.value)
			}
		}
		if len(hits) == 0 {
			return mcp.NewToolResultText("(no memories found)"), nil
		}
		var b strings.Builder
		for i, h := range hits {
			loc := h.Wing
			if h.Room != "" {
				loc += "/" + h.Room
			}
			fmt.Fprintf(&b, "[%d] id=%d score=%.3f %s\n%s\n\n", i+1, h.ID, h.Score, loc, store.SanitizeUTF8(h.Text))
		}
		text := strings.TrimSpace(b.String())
		event := recallMetricsEvent(hits, strings.Join(requestParts, " "), text, metricsSession, collection)
		metricsCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_ = metrics.RecordRecall(metricsCtx, event)
		cancel()
		return mcp.NewToolResultText(text), nil
	}
}

func recallMetricsEvent(hits []store.Result, requestText, responseText, sessionID, collection string) metrics.Event {
	totalMemoryTokens := int64(0)
	attributedMemoryTokens := int64(0)
	attributedMemories := int64(0)
	representations := make([]metrics.Representation, 0, len(hits))
	for _, hit := range hits {
		delivered := metrics.EstimateTokens(hit.Text)
		totalMemoryTokens += delivered
		if hit.Status == store.StatusArchived || hit.Status == store.StatusSuperseded ||
			hit.Status == store.StatusOrphaned {
			continue // history/audit delivery must not claim source reuse twice
		}
		represented := metrics.RepresentedShare(hit.SourceTokens, hit.MemoryTokens, delivered)
		if represented <= 0 {
			continue
		}
		attributedMemoryTokens += delivered
		attributedMemories++
		representationID := metrics.SessionMemoryID("mcp:"+collection, sessionID, hit.ID)
		if hit.SourceID != "" {
			representationID = metrics.SessionSourceID("mcp:"+collection, sessionID, hit.SourceID)
		}
		representations = append(representations, metrics.Representation{
			ID:     representationID,
			Tokens: represented,
		})
	}
	cost := metrics.EstimateTokens(requestText) + metrics.EstimateTokens(responseText)
	attributed, unknown := metrics.SplitAttribution(cost, totalMemoryTokens, attributedMemoryTokens)
	return metrics.Event{
		ID:                   metrics.UniqueEventID(),
		Source:               "mcp",
		DeliveredTokens:      cost,
		AttributedTokens:     attributed,
		UnattributedTokens:   unknown,
		Memories:             int64(len(hits)),
		AttributedMemories:   attributedMemories,
		UnattributedMemories: int64(len(hits)) - attributedMemories,
		Representations:      representations,
	}
}

func saveHandler(st *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := req.RequireString("text")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		text = store.SanitizeUTF8(text)
		now := strconv.FormatInt(time.Now().Unix(), 10)
		room := req.GetString("room", "")
		meta := map[string]string{
			"wing":          req.GetString("wing", ""),
			"room":          room,
			"ts":            now,
			"observed_at":   now,
			"memory_tokens": strconv.FormatInt(metrics.EstimateTokens(text), 10),
			"provenance":    "manual",
			"status":        store.StatusActive,
			"memory_type":   manualMemoryType(room),
		}
		if sourceTokens := req.GetInt("source_tokens", 0); sourceTokens > 0 {
			meta["source_tokens"] = strconv.Itoa(sourceTokens)
			meta["provenance"] = "manual-measured"
			meta["source_id"] = metrics.SessionEventID("manual-source", text)
		}
		id, err := st.Save(ctx, text, meta)
		if err != nil {
			return mcp.NewToolResultError("save failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		return mcp.NewToolResultText(fmt.Sprintf("saved (id=%d)", id)), nil
	}
}

func manualMemoryType(room string) string {
	switch room {
	case "decisions":
		return "decision"
	case "gotchas":
		return "gotcha"
	case "diary":
		return "event"
	default:
		return "reference"
	}
}

// parseID reads the memory id — a string so a big uint64 keeps full precision.
func parseID(req mcp.CallToolRequest) (uint64, error) {
	s, err := req.RequireString("id")
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}

func deleteHandler(st *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := parseID(req)
		if err != nil {
			return mcp.NewToolResultError("bad id: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		if err := st.DeleteByID(ctx, id); err != nil {
			return mcp.NewToolResultError("delete failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		return mcp.NewToolResultText(fmt.Sprintf("deleted (id=%d)", id)), nil
	}
}

func moveHandler(st *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := parseID(req)
		if err != nil {
			return mcp.NewToolResultError("bad id: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		wing, room := req.GetString("wing", ""), req.GetString("room", "")
		if wing == "" && room == "" {
			return mcp.NewToolResultError("nothing to change: give a new wing and/or room"), nil
		}
		if err := st.SetMeta(ctx, id, map[string]string{"wing": wing, "room": room}); err != nil {
			return mcp.NewToolResultError("move failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		return mcp.NewToolResultText(formatMoveResult(id, wing, room)), nil
	}
}

func formatMoveResult(id uint64, wing, room string) string {
	part := func(name, value string) string {
		if value == "" {
			return name + "=<kept>"
		}
		return fmt.Sprintf("%s=%q", name, value)
	}
	return fmt.Sprintf("moved (id=%d %s %s)", id, part("wing", wing), part("room", room))
}
