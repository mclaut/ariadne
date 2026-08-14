// Package store is ariadne's storage core: bge-m3 dense embedding (via Ollama),
// BM25 sparse (pure Go; Qdrant computes IDF), and hybrid save/recall over a
// Qdrant server. It holds no MCP concerns — the server and import tool both use it.
package store

import (
	"ariadne/internal/qdrantauth"
	"ariadne/internal/secretguard"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/qdrant/go-client/qdrant"
)

const denseDim = 1024

// Store talks to a Qdrant server and an Ollama embedding endpoint.
type Store struct {
	qc         *qdrant.Client
	http       *http.Client
	ollamaURL  string
	model      string
	collection string
}

// Result is one recall hit.
type Result struct {
	ID               uint64  `json:"id"`
	Score            float32 `json:"score"`
	Text             string  `json:"text"`
	Wing             string  `json:"wing,omitempty"`
	Room             string  `json:"room,omitempty"`
	SourceTokens     int64   `json:"source_tokens,omitempty"`
	MemoryTokens     int64   `json:"memory_tokens,omitempty"`
	Provenance       string  `json:"provenance,omitempty"`
	SourceID         string  `json:"source_id,omitempty"`
	Status           string  `json:"status,omitempty"`
	MemoryType       string  `json:"memory_type,omitempty"`
	TS               int64   `json:"ts,omitempty"`
	ObservedAt       int64   `json:"observed_at,omitempty"`
	OccurredAt       int64   `json:"occurred_at,omitempty"`
	LastSeenAt       int64   `json:"last_seen_at,omitempty"`
	SourceModifiedAt int64   `json:"source_modified_at,omitempty"`
	ContentHash      string  `json:"content_hash,omitempty"`
	SourceKind       string  `json:"source_kind,omitempty"`
	SourceKey        string  `json:"source_key,omitempty"`
	SourceRevision   string  `json:"source_revision,omitempty"`
	IdentityVer      string  `json:"identity_version,omitempty"`
	SupersededBy     string  `json:"superseded_by,omitempty"`
}

// SensitivePoint is metadata-only output from the deterministic credential
// audit. Findings names rules, never matched values or memory text.
type SensitivePoint struct {
	ID       uint64
	Wing     string
	Room     string
	Status   string
	Findings []string
}

// ClearedQuarantinePoint identifies a record quarantined by one detector
// revision that no longer matches the current rules. No payload text leaves
// the store layer.
type ClearedQuarantinePoint struct {
	ID             uint64
	PreviousStatus string
}

const (
	StatusActive      = "active"
	StatusArchived    = "archived"
	StatusSuperseded  = "superseded"
	StatusOrphaned    = "orphaned"
	StatusQuarantined = "quarantined"
)

// GetByID retrieves one memory exactly, without embedding or semantic search.
// A non-empty collection overrides the default collection.
func (s *Store) GetByID(ctx context.Context, id uint64, collection string) (Result, bool, error) {
	if collection == "" {
		collection = s.collection
	}
	points, err := s.qc.Get(ctx, &qdrant.GetPoints{
		CollectionName: collection,
		Ids:            []*qdrant.PointId{qdrant.NewIDNum(id)},
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return Result{}, false, err
	}
	if len(points) == 0 {
		return Result{}, false, nil
	}
	p := points[0]
	pl := p.GetPayload()
	return Result{
		ID:               p.GetId().GetNum(),
		Score:            1,
		Text:             pl["text"].GetStringValue(),
		Wing:             pl["wing"].GetStringValue(),
		Room:             pl["room"].GetStringValue(),
		SourceTokens:     pl["source_tokens"].GetIntegerValue(),
		MemoryTokens:     pl["memory_tokens"].GetIntegerValue(),
		Provenance:       pl["provenance"].GetStringValue(),
		SourceID:         pl["source_id"].GetStringValue(),
		Status:           pl["status"].GetStringValue(),
		MemoryType:       pl["memory_type"].GetStringValue(),
		TS:               pl["ts"].GetIntegerValue(),
		ObservedAt:       pl["observed_at"].GetIntegerValue(),
		OccurredAt:       pl["occurred_at"].GetIntegerValue(),
		LastSeenAt:       pl["last_seen_at"].GetIntegerValue(),
		SourceModifiedAt: pl["source_modified_at"].GetIntegerValue(),
		ContentHash:      pl["content_hash"].GetStringValue(),
		SourceKind:       pl["source_kind"].GetStringValue(),
		SourceKey:        pl["source_key"].GetStringValue(),
		SourceRevision:   pl["source_revision"].GetStringValue(),
		IdentityVer:      pl["identity_version"].GetStringValue(),
		SupersededBy:     pl["superseded_by"].GetStringValue(),
	}, true, nil
}

// New connects to Qdrant (gRPC) and prepares the Ollama client.
func New(qHost string, qPort int, ollamaURL, model, collection string) (*Store, error) {
	auth, err := qdrantauth.FromEnv()
	if err != nil {
		return nil, fmt.Errorf("qdrant auth config: %w", err)
	}
	if err := auth.ValidateGRPC(qHost); err != nil {
		return nil, fmt.Errorf("qdrant remote policy: %w", err)
	}
	qc, err := qdrant.NewClient(qdrantClientConfig(qHost, qPort, auth.APIKey, auth.UseTLS))
	if err != nil {
		return nil, fmt.Errorf("qdrant connect: %w", err)
	}
	return &Store{
		qc:         qc,
		http:       &http.Client{Timeout: 60 * time.Second},
		ollamaURL:  strings.TrimRight(ollamaURL, "/"),
		model:      model,
		collection: collection,
	}, nil
}

func qdrantClientConfig(host string, port int, apiKey string, useTLS bool) *qdrant.Config {
	return &qdrant.Config{
		Host: host, Port: port, APIKey: apiKey, UseTLS: useTLS,
		// MCP clients are long-lived but issue one tool call at a time. The
		// qdrant client defaults to a pool of three persistent gRPC sockets,
		// which multiplied the connection count across desktop sessions and
		// exhausted macOS launchd's file-descriptor limit.
		PoolSize: 1,
	}
}

// Close releases the Qdrant gRPC connection owned by the store.
func (s *Store) Close() error {
	if s == nil || s.qc == nil {
		return nil
	}
	return s.qc.Close()
}

type payloadIndex struct {
	field     string
	fieldType qdrant.FieldType
}

func desiredPayloadIndexes() []payloadIndex {
	indexes := make([]payloadIndex, 0, 35)
	indexes = append(indexes,
		payloadIndex{field: "wing", fieldType: qdrant.FieldType_FieldTypeKeyword},
		payloadIndex{field: "ts", fieldType: qdrant.FieldType_FieldTypeInteger},
	)
	for _, field := range []string{
		"room", "status", "provenance", "memory_type", "consolidation_status", "source_revision",
		"source_kind", "source_key", "content_hash", "identity_version", "superseded_by", "superseded_reason",
		"consolidation_deferred_key", "consolidation_deferred_reason", "quarantine_reason",
		"pre_quarantine_status", "quarantine_state", "quarantine_clear_reason", "secret_redacted", "redaction_rules",
	} {
		indexes = append(indexes, payloadIndex{field: field, fieldType: qdrant.FieldType_FieldTypeKeyword})
	}
	for _, field := range []string{
		"observed_at", "occurred_at", "last_seen_at", "source_modified_at",
		"consolidated_at", "consolidation_checked_at", "consolidation_first_empty_at",
		"consolidation_deferred_at", "consolidation_attempts", "superseded_at", "orphaned_at", "quarantined_at",
		"quarantine_cleared_at",
	} {
		indexes = append(indexes, payloadIndex{field: field, fieldType: qdrant.FieldType_FieldTypeInteger})
	}
	return indexes
}

func missingPayloadIndexes(
	desired []payloadIndex,
	existing map[string]*qdrant.PayloadSchemaInfo,
) []payloadIndex {
	missing := make([]payloadIndex, 0, len(desired))
	for _, index := range desired {
		if _, ok := existing[index.field]; !ok {
			missing = append(missing, index)
		}
	}
	return missing
}

// EnsureCollection creates the hybrid collection (dense + IDF sparse) if absent
// and creates only payload indexes that are not already present.
func (s *Store) EnsureCollection(ctx context.Context) error {
	ok, err := s.qc.CollectionExists(ctx, s.collection)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.qc.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: s.collection,
			VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
				"dense": {Size: denseDim, Distance: qdrant.Distance_Cosine},
			}),
			SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
				"sparse": {Modifier: qdrant.Modifier_Idf.Enum()},
			}),
		}); err != nil {
			return err
		}
	}
	info, err := s.qc.GetCollectionInfo(ctx, s.collection)
	if err != nil {
		return fmt.Errorf("inspect payload indexes: %w", err)
	}
	for _, index := range missingPayloadIndexes(desiredPayloadIndexes(), info.GetPayloadSchema()) {
		_, createErr := s.qc.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: s.collection,
			FieldName:      index.field,
			FieldType:      index.fieldType.Enum(),
		})
		if createErr == nil {
			continue
		}
		// Another concurrently starting MCP process may have created the same
		// index after our collection-info read. Confirm that race explicitly;
		// every other storage error must fail startup instead of being hidden.
		refreshed, refreshErr := s.qc.GetCollectionInfo(ctx, s.collection)
		if refreshErr == nil {
			if _, exists := refreshed.GetPayloadSchema()[index.field]; exists {
				continue
			}
		}
		return fmt.Errorf("create payload index %q: %w", index.field, createErr)
	}
	return nil
}

// Save embeds text (dense+sparse) and upserts one point. Identity v2 scopes the
// content hash by wing+room, so identical text can safely exist in more than
// one project. A matching legacy text-only point is retained as superseded.
func (s *Store) Save(ctx context.Context, text string, meta map[string]string) (uint64, error) {
	if findings := secretguard.Findings(text); len(findings) > 0 {
		return 0, fmt.Errorf("refusing to store credential material (%s)", strings.Join(findings, ","))
	}
	if strings.TrimSpace(meta["wing"]) == "" {
		return 0, errors.New("wing is required")
	}
	meta = identityMetadata(text, meta)
	id := scopedContentID(text, meta["wing"], meta["room"])
	legacyID := contentID(text)
	existing, err := s.sourceMetadata(ctx, uniqueIDs(id, legacyID))
	if err != nil {
		return 0, err
	}
	base := existing[id]
	legacyReplacement := false
	legacy, legacyMatch := existing[legacyID]
	legacyMatch = legacyMatch && sameScope(legacy, meta)
	if _, ok := existing[id]; !ok {
		if legacyMatch {
			base = legacy
			legacyReplacement = legacyID != id
		}
	}
	meta = preserveSourceMetadata(meta, base)
	if legacyMatch && meta["source_kind"] == "memfile" {
		meta = preserveLegacyTimestamps(meta, legacy)
	}
	dense, err := s.embed(ctx, text)
	if err != nil {
		return 0, err
	}
	sIdx, sVal := sparseVec(text)
	_, err = s.qc.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.collection,
		Points: []*qdrant.PointStruct{{
			Id: qdrant.NewIDNum(id),
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				"dense":  qdrant.NewVectorDense(dense),
				"sparse": qdrant.NewVectorSparse(sIdx, sVal),
			}),
			Payload: qdrant.NewValueMap(buildPayload(text, meta)),
		}},
	})
	if err != nil || !legacyReplacement {
		return id, err
	}
	err = s.SetMeta(ctx, legacyID, map[string]string{
		"status":            StatusSuperseded,
		"superseded_at":     strconv.FormatInt(time.Now().Unix(), 10),
		"superseded_by":     strconv.FormatUint(id, 10),
		"superseded_reason": "scoped-identity-v2",
	})
	return id, err
}

// SaveItem is one text plus its metadata, for SaveBatch.
type SaveItem struct {
	Text string
	Meta map[string]string
}

// SaveBatch embeds all items in one Ollama call and upserts them in one Qdrant
// call — far fewer round trips than looping Save, which is what makes bulk
// import fast. Scoped identity keeps each wing+room association independently
// addressable while remaining idempotent inside that scope.
func (s *Store) SaveBatch(ctx context.Context, items []SaveItem) error {
	if len(items) == 0 {
		return nil
	}
	texts := make([]string, len(items))
	ids := make([]uint64, len(items))
	legacyIDs := make([]uint64, len(items))
	allIDs := make([]uint64, 0, len(items)*2)
	for i, it := range items {
		if findings := secretguard.Findings(it.Text); len(findings) > 0 {
			return fmt.Errorf("item %d contains credential material (%s)", i+1, strings.Join(findings, ","))
		}
		if strings.TrimSpace(it.Meta["wing"]) == "" {
			return fmt.Errorf("item %d: wing is required", i+1)
		}
		items[i].Meta = identityMetadata(it.Text, it.Meta)
		texts[i] = it.Text
		ids[i] = scopedContentID(it.Text, items[i].Meta["wing"], items[i].Meta["room"])
		legacyIDs[i] = contentID(it.Text)
		allIDs = append(allIDs, ids[i], legacyIDs[i])
	}
	existing, err := s.sourceMetadata(ctx, uniqueIDs(allIDs...))
	if err != nil {
		return err
	}
	dense, err := s.embedBatch(ctx, texts)
	if err != nil {
		return err
	}
	points := make([]*qdrant.PointStruct, len(items))
	legacyReplaced := map[uint64]bool{}
	for i, it := range items {
		sIdx, sVal := sparseVec(it.Text)
		base := existing[ids[i]]
		legacy, legacyMatch := existing[legacyIDs[i]]
		legacyMatch = legacyMatch && sameScope(legacy, it.Meta)
		if _, ok := existing[ids[i]]; !ok {
			if legacyMatch {
				base = legacy
				if legacyIDs[i] != ids[i] {
					legacyReplaced[legacyIDs[i]] = true
				}
			}
		}
		meta := preserveSourceMetadata(it.Meta, base)
		if legacyMatch && meta["source_kind"] == "memfile" {
			meta = preserveLegacyTimestamps(meta, legacy)
		}
		points[i] = &qdrant.PointStruct{
			Id: qdrant.NewIDNum(ids[i]),
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				"dense":  qdrant.NewVectorDense(dense[i]),
				"sparse": qdrant.NewVectorSparse(sIdx, sVal),
			}),
			Payload: qdrant.NewValueMap(buildPayload(it.Text, meta)),
		}
	}
	_, err = s.qc.Upsert(ctx, &qdrant.UpsertPoints{CollectionName: s.collection, Points: points})
	if err != nil || len(legacyReplaced) == 0 {
		return err
	}
	old := make([]uint64, 0, len(legacyReplaced))
	for legacyID := range legacyReplaced {
		old = append(old, legacyID)
	}
	return s.SetMetaByIDs(ctx, old, map[string]string{
		"status":            StatusSuperseded,
		"superseded_at":     strconv.FormatInt(time.Now().Unix(), 10),
		"superseded_reason": "scoped-identity-v2",
	})
}

type sourceMeta struct {
	Wing         string
	Room         string
	SourceTokens int64
	MemoryTokens int64
	Provenance   string
	SourceID     string
	Status       string
	MemoryType   string
	ObservedAt   int64
	OccurredAt   int64
	TS           int64
	LastSeenAt   int64
	IdentityVer  string
}

// MemfileSourceState is the active, source-keyed state of one native memory
// file. Revisions is a set because an interrupted earlier sync can leave more
// than one active revision; the next successful finalization resolves it.
type MemfileSourceState struct {
	Wing      string
	Room      string
	Revisions map[string]bool
	Points    int
}

func (s *Store) sourceMetadata(ctx context.Context, ids []uint64) (map[uint64]sourceMeta, error) {
	pointIDs := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		pointIDs[i] = qdrant.NewIDNum(id)
	}
	points, err := s.qc.Get(ctx, &qdrant.GetPoints{
		CollectionName: s.collection,
		Ids:            pointIDs,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[uint64]sourceMeta, len(points))
	for _, point := range points {
		payload := point.GetPayload()
		out[point.GetId().GetNum()] = sourceMeta{
			Wing:         payload["wing"].GetStringValue(),
			Room:         payload["room"].GetStringValue(),
			SourceTokens: payload["source_tokens"].GetIntegerValue(),
			MemoryTokens: payload["memory_tokens"].GetIntegerValue(),
			Provenance:   payload["provenance"].GetStringValue(),
			SourceID:     payload["source_id"].GetStringValue(),
			Status:       payload["status"].GetStringValue(),
			MemoryType:   payload["memory_type"].GetStringValue(),
			ObservedAt:   payload["observed_at"].GetIntegerValue(),
			OccurredAt:   payload["occurred_at"].GetIntegerValue(),
			TS:           payload["ts"].GetIntegerValue(),
			LastSeenAt:   payload["last_seen_at"].GetIntegerValue(),
			IdentityVer:  payload["identity_version"].GetStringValue(),
		}
	}
	return out, nil
}

func preserveSourceMetadata(meta map[string]string, existing sourceMeta) map[string]string {
	out := make(map[string]string, len(meta)+8)
	for key, value := range meta {
		out[key] = value
	}
	if out["source_tokens"] == "" && existing.SourceTokens > 0 {
		out["source_tokens"] = strconv.FormatInt(existing.SourceTokens, 10)
		if existing.MemoryTokens > 0 {
			out["memory_tokens"] = strconv.FormatInt(existing.MemoryTokens, 10)
		}
		if existing.Provenance != "" {
			out["provenance"] = existing.Provenance
		}
	}
	for key, value := range map[string]string{
		"source_id":   existing.SourceID,
		"status":      existing.Status,
		"memory_type": existing.MemoryType,
	} {
		if out[key] == "" && value != "" {
			out[key] = value
		}
	}
	// Event/observation time is immutable for the same logical record. A sync
	// heartbeat belongs in last_seen_at and must not make old knowledge recent.
	for key, value := range map[string]int64{
		"ts":          existing.TS,
		"observed_at": existing.ObservedAt,
		"occurred_at": existing.OccurredAt,
	} {
		if value > 0 {
			out[key] = strconv.FormatInt(value, 10)
		}
	}
	return out
}

func preserveLegacyTimestamps(meta map[string]string, legacy sourceMeta) map[string]string {
	for key, value := range map[string]int64{
		"ts": legacy.TS, "observed_at": legacy.ObservedAt, "occurred_at": legacy.OccurredAt,
	} {
		if value > 0 {
			meta[key] = strconv.FormatInt(value, 10)
		}
	}
	return meta
}

func identityMetadata(text string, meta map[string]string) map[string]string {
	out := make(map[string]string, len(meta)+2)
	for key, value := range meta {
		out[key] = value
	}
	out["identity_version"] = "2"
	out["content_hash"] = contentHash(text)
	return out
}

func sameScope(existing sourceMeta, meta map[string]string) bool {
	return existing.IdentityVer != "2" && existing.Wing == meta["wing"] &&
		(existing.Room == meta["room"] || existing.Room == meta["_legacy_room"])
}

func uniqueIDs(ids ...uint64) []uint64 {
	seen := make(map[uint64]bool, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// buildPayload assembles a point payload: text plus non-empty metadata. Numeric
// fields stay numeric in Qdrant; every other key stays a string.
func buildPayload(text string, meta map[string]string) map[string]any {
	payload := map[string]any{"text": text}
	for k, v := range meta {
		if v == "" || strings.HasPrefix(k, "_") {
			continue
		}
		payload[k] = metadataValue(k, v)
	}
	return payload
}

func metadataValue(key, value string) any {
	switch key {
	case "ts", "source_tokens", "memory_tokens", "observed_at", "occurred_at",
		"session_started_at", "session_ended_at", "last_seen_at", "source_modified_at",
		"consolidated_at", "consolidation_checked_at", "consolidation_first_empty_at",
		"consolidation_deferred_at", "superseded_at", "orphaned_at", "quarantined_at", "quarantine_cleared_at",
		"consolidation_attempts":
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return value
}

// Recall runs a hybrid dense+sparse query fused with RRF, server-side.
// Non-empty wing and room values narrow the search to that project/namespace
// and category. A non-empty collection overrides the default one.
func (s *Store) Recall(
	ctx context.Context, query string, limit int, wing, room, collection string, includeArchived bool,
) ([]Result, error) {
	if limit <= 0 {
		limit = 5
	}
	if collection == "" {
		collection = s.collection
	}
	dense, err := s.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	sIdx, sVal := sparseVec(query)
	candidateLimit := limit * 4
	if candidateLimit < 20 {
		candidateLimit = 20
	}
	pre := uint64(candidateLimit * 2)
	filter := recallFilter(wing, room, includeArchived)
	res, err := s.qc.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrant.PrefetchQuery{
			{Query: qdrant.NewQueryDense(dense), Using: qdrant.PtrOf("dense"), Limit: &pre, Filter: filter},
			{Query: qdrant.NewQuerySparse(sIdx, sVal), Using: qdrant.PtrOf("sparse"), Limit: &pre, Filter: filter},
		},
		Query:       qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Filter:      filter,
		Limit:       qdrant.PtrOf(uint64(candidateLimit)),
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(res))
	for _, p := range res {
		pl := p.GetPayload()
		text := pl["text"].GetStringValue()
		if secretguard.Contains(text) {
			continue
		}
		out = append(out, Result{
			ID:               p.GetId().GetNum(),
			Score:            p.GetScore(),
			Text:             text,
			Wing:             pl["wing"].GetStringValue(),
			Room:             pl["room"].GetStringValue(),
			SourceTokens:     pl["source_tokens"].GetIntegerValue(),
			MemoryTokens:     pl["memory_tokens"].GetIntegerValue(),
			Provenance:       pl["provenance"].GetStringValue(),
			SourceID:         pl["source_id"].GetStringValue(),
			Status:           pl["status"].GetStringValue(),
			MemoryType:       pl["memory_type"].GetStringValue(),
			TS:               pl["ts"].GetIntegerValue(),
			ObservedAt:       pl["observed_at"].GetIntegerValue(),
			OccurredAt:       pl["occurred_at"].GetIntegerValue(),
			LastSeenAt:       pl["last_seen_at"].GetIntegerValue(),
			SourceModifiedAt: pl["source_modified_at"].GetIntegerValue(),
			ContentHash:      pl["content_hash"].GetStringValue(),
			SourceKind:       pl["source_kind"].GetStringValue(),
			SourceKey:        pl["source_key"].GetStringValue(),
			SourceRevision:   pl["source_revision"].GetStringValue(),
			IdentityVer:      pl["identity_version"].GetStringValue(),
			SupersededBy:     pl["superseded_by"].GetStringValue(),
		})
	}
	return Rerank(query, out, limit, time.Now()), nil
}

func recallFilter(wing, room string, includeArchived bool) *qdrant.Filter {
	conditions := make([]*qdrant.Condition, 0, 2)
	if wing != "" {
		conditions = append(conditions, qdrant.NewMatch("wing", wing))
	}
	if room != "" {
		conditions = append(conditions, qdrant.NewMatch("room", room))
	}
	filter := &qdrant.Filter{Must: conditions}
	// Quarantine is a security boundary, not ordinary lifecycle history. It is
	// excluded even from includeArchived semantic search. Exact-id retrieval is
	// still available to the MCP layer, which redacts values before returning it.
	filter.MustNot = append(filter.MustNot, qdrant.NewMatch("status", StatusQuarantined))
	// Consolidated and superseded records remain stored and are available by
	// exact id or an explicit includeArchived recall. Normal recall only sees
	// active or legacy records.
	if !includeArchived {
		for _, status := range []string{StatusArchived, StatusSuperseded, StatusOrphaned} {
			filter.MustNot = append(filter.MustNot, qdrant.NewMatch("status", status))
		}
	}
	return filter
}

// --- embedding + sparse ---

// embedBatch embeds many texts in a single Ollama call — bge-m3's /api/embed
// accepts an array input, so this is one round trip instead of len(texts).
func (s *Store) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": s.model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ollamaURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}

func (s *Store) embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := s.embedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// tokenize: lowercase, split on non-letter/number — Unicode-aware (Cyrillic,
// Latin EU langs, Arabic all tokenize correctly).
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// sparseVec: BM25 term-frequency sparse vector; token id = FNV-1a hash.
func sparseVec(text string) ([]uint32, []float32) {
	tf := map[uint32]float32{}
	for _, tok := range tokenize(text) {
		if len([]rune(tok)) < 2 {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		tf[h.Sum32()]++
	}
	idx := make([]uint32, 0, len(tf))
	val := make([]float32, 0, len(tf))
	for k, v := range tf {
		idx = append(idx, k)
		val = append(val, v)
	}
	return idx, val
}

func contentID(text string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return h.Sum64()
}

func scopedContentID(text, wing, room string) uint64 {
	sum := sha256.Sum256([]byte("ariadne-id-v2\x00" + wing + "\x00" + room + "\x00" + text))
	return binary.BigEndian.Uint64(sum[:8])
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:16])
}

// DeleteByWingRoom removes every point of one (wing, room) pair — i.e. all
// chunks that came from a single source file. Used by import -sync.
func (s *Store) DeleteByWingRoom(ctx context.Context, wing, room string) error {
	_, err := s.qc.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: s.collection,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{Must: []*qdrant.Condition{
			qdrant.NewMatch("wing", wing),
			qdrant.NewMatch("room", room),
		}}),
	})
	return err
}

// DeleteByID removes one memory by its content-hash id (as returned by Recall).
func (s *Store) DeleteByID(ctx context.Context, id uint64) error {
	_, err := s.qc.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: s.collection,
		Points:         qdrant.NewPointsSelector(qdrant.NewIDNum(id)),
	})
	return err
}

// SetMeta updates the payload (e.g. wing/room) of one memory in place — its text
// and vectors are untouched, so the id is stable. This is how a memory is moved
// between wings or re-tagged. Empty values are ignored (that field is left as-is).
func (s *Store) SetMeta(ctx context.Context, id uint64, meta map[string]string) error {
	payload := map[string]any{}
	for k, v := range meta {
		if v != "" {
			payload[k] = metadataValue(k, v)
		}
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := s.qc.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: s.collection,
		Payload:        qdrant.NewValueMap(payload),
		PointsSelector: qdrant.NewPointsSelector(qdrant.NewIDNum(id)),
	})
	return err
}

// MoveAppendOnly re-homes one memory by creating its scoped destination record
// first, then retaining the old record as superseded history. The returned id
// is the destination id; no source point is deleted or rewritten in place.
func (s *Store) MoveAppendOnly(ctx context.Context, id uint64, wing, room string) (uint64, error) {
	hit, ok, err := s.GetByID(ctx, id, "")
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("memory %d not found", id)
	}
	destinationWing, destinationRoom := hit.Wing, hit.Room
	if wing != "" {
		destinationWing = wing
	}
	if room != "" {
		destinationRoom = room
	}
	if destinationWing == hit.Wing && destinationRoom == hit.Room {
		return id, nil
	}
	meta := map[string]string{
		"wing": destinationWing, "room": destinationRoom,
		"source_tokens": strconv.FormatInt(hit.SourceTokens, 10),
		"memory_tokens": strconv.FormatInt(hit.MemoryTokens, 10),
		"provenance":    hit.Provenance, "source_id": hit.SourceID,
		"status": StatusActive, "memory_type": hit.MemoryType,
		"ts": strconv.FormatInt(hit.TS, 10), "observed_at": strconv.FormatInt(hit.ObservedAt, 10),
		"occurred_at": strconv.FormatInt(hit.OccurredAt, 10), "last_seen_at": strconv.FormatInt(hit.LastSeenAt, 10),
		"source_modified_at": strconv.FormatInt(hit.SourceModifiedAt, 10),
		"source_kind":        hit.SourceKind, "source_key": hit.SourceKey, "source_revision": hit.SourceRevision,
	}
	newID, err := s.Save(ctx, hit.Text, meta)
	if err != nil {
		return 0, err
	}
	if newID == id {
		return id, nil
	}
	if err := s.SetMeta(ctx, id, map[string]string{
		"status": StatusSuperseded, "superseded_at": strconv.FormatInt(time.Now().Unix(), 10),
		"superseded_by": strconv.FormatUint(newID, 10), "superseded_reason": "memory-move",
	}); err != nil {
		return newID, fmt.Errorf("destination saved as %d but source lifecycle update failed: %w", newID, err)
	}
	return newID, nil
}

// SetMetaByIDs updates metadata for several records without changing their
// text or vectors. It is used to archive source diary records after an
// append-only consolidation pass.
func (s *Store) SetMetaByIDs(ctx context.Context, ids []uint64, meta map[string]string) error {
	return s.SetMetaByIDsInCollection(ctx, "", ids, meta)
}

// SetMetaByIDsInCollection applies lifecycle metadata without touching text or
// vectors. A non-empty collection overrides the configured default.
func (s *Store) SetMetaByIDsInCollection(
	ctx context.Context, collection string, ids []uint64, meta map[string]string,
) error {
	if len(ids) == 0 {
		return nil
	}
	if collection == "" {
		collection = s.collection
	}
	payload := map[string]any{}
	for key, value := range meta {
		if value != "" {
			payload[key] = metadataValue(key, value)
		}
	}
	if len(payload) == 0 {
		return nil
	}
	pointIDs := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		pointIDs[i] = qdrant.NewIDNum(id)
	}
	_, err := s.qc.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: collection,
		Payload:        qdrant.NewValueMap(payload),
		PointsSelector: qdrant.NewPointsSelector(pointIDs...),
	})
	return err
}

// SensitivePoints scans plaintext payloads locally and returns only ids and
// non-sensitive metadata for records matching high-confidence credential
// rules. The text itself never leaves this method.
func (s *Store) SensitivePoints(
	ctx context.Context, collection string,
) ([]SensitivePoint, bool, error) {
	if collection == "" {
		collection = s.collection
	}
	exists, err := s.qc.CollectionExists(ctx, collection)
	if err != nil || !exists {
		return nil, false, err
	}
	const pageSize = uint32(256)
	iterator := s.qc.ScrollAll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		Limit:          qdrant.PtrOf(pageSize),
		WithPayload:    qdrant.NewWithPayloadInclude("text", "wing", "room", "status"),
	})
	out := make([]SensitivePoint, 0)
	for {
		points, err := iterator.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		for _, point := range points {
			payload := point.GetPayload()
			findings := secretguard.Findings(payload["text"].GetStringValue())
			if len(findings) == 0 {
				continue
			}
			out = append(out, SensitivePoint{
				ID: point.GetId().GetNum(), Wing: payload["wing"].GetStringValue(),
				Room: payload["room"].GetStringValue(), Status: payload["status"].GetStringValue(),
				Findings: findings,
			})
		}
	}
	return out, false, nil
}

// ClearedQuarantinePoints returns records quarantined for reason whose text no
// longer matches the current detector. Callers can restore the previous status
// without deleting payloads, vectors, or quarantine audit metadata.
func (s *Store) ClearedQuarantinePoints(
	ctx context.Context, collection, reason string,
) ([]ClearedQuarantinePoint, error) {
	if collection == "" {
		collection = s.collection
	}
	exists, err := s.qc.CollectionExists(ctx, collection)
	if err != nil || !exists {
		return nil, err
	}
	filter := &qdrant.Filter{Must: []*qdrant.Condition{
		qdrant.NewMatch("status", StatusQuarantined), qdrant.NewMatch("quarantine_reason", reason),
	}}
	iterator := s.qc.ScrollAll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		Limit:          qdrant.PtrOf(uint32(256)),
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayloadInclude("text", "pre_quarantine_status"),
	})
	out := make([]ClearedQuarantinePoint, 0)
	for {
		points, nextErr := iterator.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		for _, point := range points {
			payload := point.GetPayload()
			if secretguard.Contains(payload["text"].GetStringValue()) {
				continue
			}
			out = append(out, ClearedQuarantinePoint{
				ID: point.GetId().GetNum(), PreviousStatus: payload["pre_quarantine_status"].GetStringValue(),
			})
		}
	}
	return out, nil
}

// SetMetaByWingRoom updates every record from one imported source. When
// exceptRevision is non-empty, records from that current revision are left
// active while older revisions receive the supplied archival metadata.
func (s *Store) SetMetaByWingRoom(
	ctx context.Context, wing, room, exceptRevision string, meta map[string]string,
) error {
	payload := map[string]any{}
	for key, value := range meta {
		if value != "" {
			payload[key] = metadataValue(key, value)
		}
	}
	if len(payload) == 0 {
		return nil
	}
	filter := &qdrant.Filter{Must: []*qdrant.Condition{
		qdrant.NewMatch("wing", wing), qdrant.NewMatch("room", room),
	}}
	if exceptRevision != "" {
		filter.MustNot = append(filter.MustNot, qdrant.NewMatch("source_revision", exceptRevision))
	}
	_, err := s.qc.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: s.collection,
		Payload:        qdrant.NewValueMap(payload),
		PointsSelector: qdrant.NewPointsSelectorFilter(filter),
	})
	return err
}

// SetMetaBySourceKey updates one imported native-memory source. A non-empty
// exceptRevision protects the current revision while older ones are retained
// with the supplied lifecycle status.
func (s *Store) SetMetaBySourceKey(
	ctx context.Context, sourceKey, exceptRevision string, meta map[string]string,
) error {
	filter := &qdrant.Filter{Must: []*qdrant.Condition{qdrant.NewMatch("source_key", sourceKey)}}
	if exceptRevision != "" {
		filter.MustNot = append(filter.MustNot, qdrant.NewMatch("source_revision", exceptRevision))
	}
	return s.setMetaByFilter(ctx, filter, meta)
}

// SetMetaByWingRoomLegacy updates pre-v2 records from one native memory file.
// The v2 identity marker is deliberately excluded so current scoped records
// are never swept into a compatibility migration.
func (s *Store) SetMetaByWingRoomLegacy(
	ctx context.Context, wing, room string, meta map[string]string,
) error {
	return s.setMetaByFilter(ctx, &qdrant.Filter{
		Must:    []*qdrant.Condition{qdrant.NewMatch("wing", wing), qdrant.NewMatch("room", room)},
		MustNot: []*qdrant.Condition{qdrant.NewMatch("identity_version", "2")},
	}, meta)
}

// TouchActiveMemfiles records that a complete filesystem scan observed all
// currently active native-memory chunks. It changes freshness metadata only,
// never the original observation or event time.
func (s *Store) TouchActiveMemfiles(ctx context.Context, lastSeenAt int64) error {
	filter := &qdrant.Filter{Must: []*qdrant.Condition{qdrant.NewMatch("source_kind", "memfile")}}
	for _, status := range []string{StatusArchived, StatusSuperseded, StatusOrphaned, StatusQuarantined} {
		filter.MustNot = append(filter.MustNot, qdrant.NewMatch("status", status))
	}
	return s.setMetaByFilter(ctx, filter, map[string]string{
		"last_seen_at": strconv.FormatInt(lastSeenAt, 10),
	})
}

func (s *Store) setMetaByFilter(ctx context.Context, filter *qdrant.Filter, meta map[string]string) error {
	payload := map[string]any{}
	for key, value := range meta {
		if value != "" {
			payload[key] = metadataValue(key, value)
		}
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := s.qc.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: s.collection,
		Payload:        qdrant.NewValueMap(payload),
		PointsSelector: qdrant.NewPointsSelectorFilter(filter),
	})
	return err
}

// MemfileSourceStates returns active v2 native-memory sources keyed by their
// privacy-safe source hash. It is used to skip unchanged files before they
// reach the embedding queue.
func (s *Store) MemfileSourceStates(ctx context.Context) (map[string]MemfileSourceState, error) {
	res, err := s.qc.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collection,
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{qdrant.NewMatch("source_kind", "memfile")},
			MustNot: []*qdrant.Condition{
				qdrant.NewMatch("status", StatusArchived),
				qdrant.NewMatch("status", StatusSuperseded),
				qdrant.NewMatch("status", StatusOrphaned),
				qdrant.NewMatch("status", StatusQuarantined),
			},
		},
		Limit:       qdrant.PtrOf(uint32(200_000)),
		WithPayload: qdrant.NewWithPayloadInclude("source_key", "source_revision", "wing", "room"),
	})
	if err != nil {
		return nil, err
	}
	out := map[string]MemfileSourceState{}
	for _, point := range res {
		payload := point.GetPayload()
		key := payload["source_key"].GetStringValue()
		if key == "" {
			continue
		}
		state := out[key]
		if state.Revisions == nil {
			state.Revisions = map[string]bool{}
		}
		state.Wing = payload["wing"].GetStringValue()
		state.Room = payload["room"].GetStringValue()
		if revision := payload["source_revision"].GetStringValue(); revision != "" {
			state.Revisions[revision] = true
		}
		state.Points++
		out[key] = state
	}
	return out, nil
}

// WingRoomPairs returns point counts per (wing, room) pair. It pages through
// the complete collection with payload-only requests so import -sync remains
// correct for large archival collections as well as the curated default.
func (s *Store) WingRoomPairs(ctx context.Context) (map[[2]string]int, error) {
	const pageSize = uint32(512)
	iterator := s.qc.ScrollAll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collection,
		Limit:          qdrant.PtrOf(pageSize),
		WithPayload:    qdrant.NewWithPayloadInclude("wing", "room", "status"),
	})
	out := map[[2]string]int{}
	for {
		points, err := iterator.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, point := range points {
			payload := point.GetPayload()
			switch payload["status"].GetStringValue() {
			case StatusArchived, StatusSuperseded, StatusOrphaned, StatusQuarantined:
				continue
			}
			out[[2]string{payload["wing"].GetStringValue(), payload["room"].GetStringValue()}]++
		}
	}
	return out, nil
}

// SanitizeUTF8 replaces broken UTF-8 (source docs sometimes carry it).
func SanitizeUTF8(s string) string { return strings.ToValidUTF8(s, "") }
