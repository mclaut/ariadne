// Package store is ariadne's storage core: bge-m3 dense embedding (via Ollama),
// BM25 sparse (pure Go; Qdrant computes IDF), and hybrid save/recall over a
// Qdrant server. It holds no MCP concerns — the server and import tool both use it.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
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
	ID    uint64  `json:"id"`
	Score float32 `json:"score"`
	Text  string  `json:"text"`
	Wing  string  `json:"wing,omitempty"`
	Room  string  `json:"room,omitempty"`
}

// New connects to Qdrant (gRPC) and prepares the Ollama client.
func New(qHost string, qPort int, ollamaURL, model, collection string) (*Store, error) {
	qc, err := qdrant.NewClient(&qdrant.Config{Host: qHost, Port: qPort})
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

// EnsureCollection creates the hybrid collection (dense + IDF sparse) if absent
// and makes sure the "wing" payload index exists (used for filtered recall).
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
	// idempotent; an "already exists" error is fine
	_, _ = s.qc.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: s.collection,
		FieldName:      "wing",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	})
	// "ts" (unix seconds) — range index so diary can be ordered/filtered by time
	_, _ = s.qc.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: s.collection,
		FieldName:      "ts",
		FieldType:      qdrant.FieldType_FieldTypeInteger.Enum(),
	})
	return nil
}

// Save embeds text (dense+sparse) and upserts one point. The id is a content
// hash, so saving identical text twice is idempotent (natural dedup).
func (s *Store) Save(ctx context.Context, text string, meta map[string]string) (uint64, error) {
	dense, err := s.embed(ctx, text)
	if err != nil {
		return 0, err
	}
	sIdx, sVal := sparseVec(text)
	id := contentID(text)
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
	return id, err
}

// SaveItem is one text plus its metadata, for SaveBatch.
type SaveItem struct {
	Text string
	Meta map[string]string
}

// SaveBatch embeds all items in one Ollama call and upserts them in one Qdrant
// call — far fewer round trips than looping Save, which is what makes bulk
// import fast. Same content-hash id, so it stays idempotent.
func (s *Store) SaveBatch(ctx context.Context, items []SaveItem) error {
	if len(items) == 0 {
		return nil
	}
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.Text
	}
	dense, err := s.embedBatch(ctx, texts)
	if err != nil {
		return err
	}
	points := make([]*qdrant.PointStruct, len(items))
	for i, it := range items {
		sIdx, sVal := sparseVec(it.Text)
		points[i] = &qdrant.PointStruct{
			Id: qdrant.NewIDNum(contentID(it.Text)),
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				"dense":  qdrant.NewVectorDense(dense[i]),
				"sparse": qdrant.NewVectorSparse(sIdx, sVal),
			}),
			Payload: qdrant.NewValueMap(buildPayload(it.Text, it.Meta)),
		}
	}
	_, err = s.qc.Upsert(ctx, &qdrant.UpsertPoints{CollectionName: s.collection, Points: points})
	return err
}

// buildPayload assembles a point payload: text plus non-empty metadata. "ts" is
// stored as a number so its range index (see EnsureCollection) can order/filter
// by time; every other key stays a string.
func buildPayload(text string, meta map[string]string) map[string]any {
	payload := map[string]any{"text": text}
	for k, v := range meta {
		if v == "" {
			continue
		}
		if k == "ts" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				payload[k] = n
				continue
			}
		}
		payload[k] = v
	}
	return payload
}

// Recall runs a hybrid dense+sparse query fused with RRF, server-side.
// A non-empty wing narrows the search to that project/namespace; a non-empty
// collection overrides the default one (e.g. a separate "sessions" archive).
func (s *Store) Recall(ctx context.Context, query string, limit int, wing, collection string) ([]Result, error) {
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
	pre := uint64(limit * 4)
	var filter *qdrant.Filter
	if wing != "" {
		filter = &qdrant.Filter{Must: []*qdrant.Condition{qdrant.NewMatch("wing", wing)}}
	}
	res, err := s.qc.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrant.PrefetchQuery{
			{Query: qdrant.NewQueryDense(dense), Using: qdrant.PtrOf("dense"), Limit: &pre, Filter: filter},
			{Query: qdrant.NewQuerySparse(sIdx, sVal), Using: qdrant.PtrOf("sparse"), Limit: &pre, Filter: filter},
		},
		Query:       qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Filter:      filter,
		Limit:       qdrant.PtrOf(uint64(limit)),
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(res))
	for _, p := range res {
		pl := p.GetPayload()
		out = append(out, Result{
			ID:    p.GetId().GetNum(),
			Score: p.GetScore(),
			Text:  pl["text"].GetStringValue(),
			Wing:  pl["wing"].GetStringValue(),
			Room:  pl["room"].GetStringValue(),
		})
	}
	return out, nil
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
			payload[k] = v
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

// WingRoomPairs returns point counts per (wing, room) pair. One payload-only
// scroll — fine at local scale (revisit past ~100k points); used by
// import -sync to find orphaned chunks of deleted/renamed files.
func (s *Store) WingRoomPairs(ctx context.Context) (map[[2]string]int, error) {
	res, err := s.qc.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collection,
		Limit:          qdrant.PtrOf(uint32(200_000)),
		WithPayload:    qdrant.NewWithPayloadInclude("wing", "room"),
	})
	if err != nil {
		return nil, err
	}
	out := map[[2]string]int{}
	for _, p := range res {
		pl := p.GetPayload()
		out[[2]string{pl["wing"].GetStringValue(), pl["room"].GetStringValue()}]++
	}
	return out, nil
}

// SanitizeUTF8 replaces broken UTF-8 (source docs sometimes carry it).
func SanitizeUTF8(s string) string { return strings.ToValidUTF8(s, "") }
