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
	"ariadne/internal/store"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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

	s := server.NewMCPServer("ariadne", "0.1.0",
		server.WithToolCapabilities(false))

	s.AddTool(mcp.NewTool("memory_recall",
		mcp.WithDescription("Semantically recall past memories (hybrid dense+keyword, "+
			"multilingual). Use when the user asks about earlier decisions, prior context, "+
			"project history, or 'what did we decide about X'."),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("What to recall — keywords or a question, any language.")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 5).")),
		mcp.WithString("wing", mcp.Description("Optional: narrow to one project/namespace.")),
		mcp.WithString("collection", mcp.Description("Optional: search a non-default collection, "+
			"e.g. 'sessions' for the raw session archive.")),
	), recallHandler(st))

	s.AddTool(mcp.NewTool("memory_save",
		mcp.WithDescription("Save a memory (verbatim fact, decision, or context) for future recall. "+
			"Content is embedded and stored; identical text is deduplicated."),
		mcp.WithString("text", mcp.Required(), mcp.Description("The memory content, verbatim.")),
		mcp.WithString("wing", mcp.Description("Project/namespace, e.g. 'myapp'.")),
		mcp.WithString("room", mcp.Description("Aspect, e.g. 'decisions', 'diary'.")),
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

func recallHandler(st *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		limit := req.GetInt("limit", 5)
		hits, err := st.Recall(ctx, query, limit, req.GetString("wing", ""), req.GetString("collection", ""))
		if err != nil {
			return mcp.NewToolResultError("recall failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
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
		return mcp.NewToolResultText(strings.TrimSpace(b.String())), nil
	}
}

func saveHandler(st *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := req.RequireString("text")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		id, err := st.Save(ctx, store.SanitizeUTF8(text), map[string]string{
			"wing": req.GetString("wing", ""),
			"room": req.GetString("room", ""),
		})
		if err != nil {
			return mcp.NewToolResultError("save failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		return mcp.NewToolResultText(fmt.Sprintf("saved (id=%d)", id)), nil
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
		return mcp.NewToolResultText(fmt.Sprintf("moved (id=%d wing=%q room=%q)", id, wing, room)), nil
	}
}
