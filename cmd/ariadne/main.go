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
	"ariadne/internal/approval"
	"ariadne/internal/metrics"
	"ariadne/internal/secretguard"
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
	approvals := approval.New("")

	s.AddTool(mcp.NewTool("memory_recall",
		mcp.WithDescription("Recall memories semantically (hybrid dense+keyword, multilingual) "+
			"or retrieve one exact memory by id. Provide query or id."),
		mcp.WithString("query",
			mcp.Description("What to recall — keywords or a question, any language. Omit when id is given.")),
		mcp.WithString("id", mcp.Description("Exact memory id; bypasses semantic search.")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 5).")),
		mcp.WithString("wing", mcp.Description("Required active project/namespace for semantic and exact recall.")),
		mcp.WithBoolean("all_wings", mcp.Description("Explicit opt-in to search every project. "+
			"Requires a human-approved Ariadne system warning or tray fallback request.")),
		mcp.WithString("purpose", mcp.Description("Required reason shown to the human when all_wings is requested.")),
		mcp.WithString("approval_id", mcp.Description("Approval request id returned by a prior all_wings call.")),
		mcp.WithString("room", mcp.Description("Optional: narrow to one category, e.g. "+
			"'decisions', 'gotchas', 'reference', or 'diary'.")),
		mcp.WithBoolean("include_archived", mcp.Description("Include archived, superseded, and orphaned "+
			"records for history/audit searches (default false).")),
		mcp.WithString("collection", mcp.Description("Optional: search a non-default collection, "+
			"e.g. 'sessions' for the raw session archive.")),
	), recallHandler(st, metricsSession, approvals))

	s.AddTool(mcp.NewTool("memory_save",
		mcp.WithDescription("Save a memory (verbatim fact, decision, or context) for future recall. "+
			"Content is embedded and stored; identical text is deduplicated."),
		mcp.WithString("text", mcp.Required(), mcp.Description("The memory content, verbatim.")),
		mcp.WithString("wing", mcp.Required(), mcp.Description("Required project/namespace, e.g. 'myapp'.")),
		mcp.WithString("room", mcp.Description("Aspect, e.g. 'decisions', 'diary'.")),
		mcp.WithNumber("source_tokens", mcp.Description("Optional measured or conservative size of the bounded "+
			"source context condensed into this memory. Omit rather than guess; never send the raw source.")),
		mcp.WithNumber("occurred_at", mcp.Description("Optional Unix timestamp for when a historical fact or event occurred. "+
			"Observation time remains the current save time.")),
	), saveHandler(st))

	s.AddTool(mcp.NewTool("credential_access",
		mcp.WithDescription("Request or consume one human-approved, one-time permission to use a credential "+
			"from another project. This tool never reads or returns the credential itself."),
		mcp.WithString("source_wing", mcp.Required(), mcp.Description("Project that owns the credential.")),
		mcp.WithString("target_wing", mcp.Required(), mcp.Description("Active project that would use it.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Exact credential name or file path; never its value.")),
		mcp.WithString("purpose", mcp.Required(), mcp.Description("Exact one-time purpose shown in the system warning and tray.")),
		mcp.WithString("approval_id", mcp.Description("Approval request id returned by the first call.")),
	), credentialAccessHandler(approvals, metricsSession))

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

func recallHandler(st *store.Store, metricsSession string, approvals *approval.Manager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		collection := req.GetString("collection", "")
		homeWing := strings.TrimSpace(req.GetString("wing", ""))
		allWings := req.GetBool("all_wings", false)
		purpose := strings.TrimSpace(req.GetString("purpose", ""))
		approvalID := strings.TrimSpace(req.GetString("approval_id", ""))
		requestParts := make([]string, 0, 4)
		var hits []store.Result
		if rawID := strings.TrimSpace(req.GetString("id", "")); rawID != "" {
			if _, scopeErr := semanticRecallScope(homeWing, allWings); scopeErr != nil {
				return mcp.NewToolResultError(scopeErr.Error()), nil
			}
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
				if hit.Wing != homeWing {
					if !allWings {
						return mcp.NewToolResultError("exact memory is outside the active wing; retry with all_wings, purpose, and human approval"), nil
					}
					if result := requireCrossWingApproval(approvals, metricsSession, homeWing,
						"exact memory "+rawID, purpose, collection, req.GetString("room", ""), approvalID); result != nil {
						return result, nil
					}
				}
				hits = []store.Result{hit}
			}
		} else {
			query := strings.TrimSpace(req.GetString("query", ""))
			if query == "" {
				return mcp.NewToolResultError("query or id is required"), nil
			}
			limit := req.GetInt("limit", 5)
			wing, scopeErr := semanticRecallScope(homeWing, allWings)
			if scopeErr != nil {
				return mcp.NewToolResultError(scopeErr.Error()), nil
			}
			requestParts = append(requestParts, "query="+query, fmt.Sprintf("limit=%d", limit))
			if allWings {
				requestParts = append(requestParts, "all_wings=true")
				if result := requireCrossWingApproval(approvals, metricsSession, homeWing,
					query, purpose, collection, req.GetString("room", ""), approvalID); result != nil {
					return result, nil
				}
			}
			var err error
			queryLimit := limit
			if allWings {
				queryLimit = max(20, limit*4)
			}
			hits, err = st.Recall(ctx, query, queryLimit, wing, req.GetString("room", ""),
				collection, req.GetBool("include_archived", false))
			if err != nil {
				return mcp.NewToolResultError("recall failed: " + err.Error()), nil //nolint:nilerr
			}
			if allWings {
				hits = store.RerankCrossWing(hits, homeWing, limit)
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
			memoryText := store.SanitizeUTF8(h.Text)
			if secretguard.Contains(memoryText) {
				memoryText = "[sensitive values redacted]\n" + secretguard.Redact(memoryText)
			}
			origin := "local"
			if allWings && h.Wing != homeWing {
				origin = fmt.Sprintf("cross-wing weight=%.2f", store.CrossWingWeight)
			}
			fmt.Fprintf(&b, "[%d] id=%d score=%.3f origin=%s %s\n%s\n\n",
				i+1, h.ID, h.Score, origin, loc, memoryText)
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
			hit.Status == store.StatusOrphaned || hit.Status == store.StatusQuarantined {
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
		wing := strings.TrimSpace(req.GetString("wing", ""))
		if wing == "" {
			return mcp.NewToolResultError("wing is required for memory_save"), nil
		}
		if findings := secretguard.Findings(text); len(findings) > 0 {
			return mcp.NewToolResultError("memory rejected: credential material detected (" +
				strings.Join(findings, ",") + ")"), nil
		}
		now := strconv.FormatInt(time.Now().Unix(), 10)
		eventTime := now
		if occurredAt := req.GetInt("occurred_at", 0); occurredAt > 0 {
			eventTime = strconv.Itoa(occurredAt)
		}
		room := req.GetString("room", "")
		meta := map[string]string{
			"wing":          wing,
			"room":          room,
			"ts":            eventTime,
			"observed_at":   now,
			"occurred_at":   eventTime,
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

func semanticRecallScope(wing string, allWings bool) (string, error) {
	wing = strings.TrimSpace(wing)
	if wing == "" {
		return "", fmt.Errorf("wing is required; all_wings expands from the active wing only after human approval")
	}
	if allWings {
		return "", nil
	}
	return wing, nil
}

func requireCrossWingApproval(
	approvals *approval.Manager, clientSession, activeWing, query, purpose, collection, room, approvalID string,
) *mcp.CallToolResult {
	if purpose == "" {
		return mcp.NewToolResultError("purpose is required for all_wings human approval")
	}
	if approvalID != "" {
		if _, err := approvals.AuthorizeCrossWing(approvalID, clientSession, activeWing, collection); err != nil {
			return mcp.NewToolResultError("cross-wing approval is not usable: " + err.Error())
		}
		return nil
	}
	request, err := approvals.Create(approval.Request{
		Kind: approval.KindCrossWing, ClientSession: clientSession, ActiveWing: activeWing,
		Query: secretguard.Redact(query), Purpose: secretguard.Redact(purpose),
		Collection: collection, Room: room,
	})
	if err != nil {
		return mcp.NewToolResultError("create cross-wing approval: " + err.Error())
	}
	return mcp.NewToolResultError("human approval required: request_id=" + request.ID +
		". Approve it in the Ariadne system warning or tray fallback, then retry with the same wing, " +
		"all_wings=true, purpose, and approval_id")
}

func credentialAccessHandler(approvals *approval.Manager, clientSession string) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sourceWing, err := req.RequireString("source_wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		targetWing, err := req.RequireString("target_wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resource, err := req.RequireString("resource")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		purpose, err := req.RequireString("purpose")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sourceWing, targetWing = strings.TrimSpace(sourceWing), strings.TrimSpace(targetWing)
		resource, purpose = strings.TrimSpace(resource), strings.TrimSpace(purpose)
		if findings := secretguard.Findings(resource + "\n" + purpose); len(findings) > 0 {
			return mcp.NewToolResultError("credential request contains a credential value; provide only its name/path and purpose"), nil
		}
		scope := approval.CredentialScope{
			ClientSession: clientSession, SourceWing: sourceWing, TargetWing: targetWing,
			Resource: resource, Purpose: purpose,
		}
		if approvalID := strings.TrimSpace(req.GetString("approval_id", "")); approvalID != "" {
			if _, err := approvals.AuthorizeCredential(approvalID, scope); err != nil {
				return mcp.NewToolResultError("credential approval is not usable: " + err.Error()), nil //nolint:nilerr // MCP errors are in-band
			}
			return mcp.NewToolResultText("human-approved one-time credential access consumed; " +
				"use only the exact resource and purpose, and never store its value in Ariadne"), nil
		}
		request, err := approvals.Create(approval.Request{
			Kind: approval.KindCredential, ClientSession: clientSession,
			SourceWing: sourceWing, TargetWing: targetWing, Resource: resource, Purpose: purpose,
		})
		if err != nil {
			return mcp.NewToolResultError("create credential approval: " + err.Error()), nil //nolint:nilerr // MCP errors are in-band
		}
		return mcp.NewToolResultError("separate human credential approval required: request_id=" + request.ID +
			". Approve it in the Ariadne system warning or tray fallback, then retry this exact call with approval_id"), nil
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
		newID, err := st.MoveAppendOnly(ctx, id, wing, room)
		if err != nil {
			return mcp.NewToolResultError("move failed: " + err.Error()), nil //nolint:nilerr // MCP tool errors go in-band
		}
		return mcp.NewToolResultText(formatMoveResult(id, newID, wing, room)), nil
	}
}

func formatMoveResult(id, newID uint64, wing, room string) string {
	part := func(name, value string) string {
		if value == "" {
			return name + "=<kept>"
		}
		return fmt.Sprintf("%s=%q", name, value)
	}
	return fmt.Sprintf("moved (source_id=%d new_id=%d %s %s)", id, newID, part("wing", wing), part("room", room))
}
