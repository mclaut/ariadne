// ariadne POC — native Go + Qdrant + bge-m3, now HYBRID (dense + BM25 sparse).
//
// Dense  : bge-m3 via Ollama (Metal), 1024-dim, cosine.
// Sparse : BM25 — Unicode tokenizer in pure Go, FNV-hashed token ids, term
//
//	frequency as value; Qdrant computes IDF server-side (Modifier_Idf).
//
// Fusion : Qdrant Query API, dense+sparse prefetch → RRF, fully server-side.
//
// Subcommands:
//
//	load  -n N     read N curated project docs, embed dense+sparse, upsert.
//	query "text"   hybrid search (dense+sparse RRF); prints top hits.
//	dense "text"   dense-only search (to compare against hybrid).
//	mltest         cross-lingual cosine of one sentence in 15 languages.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/qdrant/go-client/qdrant"
	_ "modernc.org/sqlite"
)

const (
	ollamaEmbed = "http://localhost:11434/api/embed"
	embModel    = "bge-m3"
	dim         = 1024
	collection  = "ariadne_poc"
)

// brokenDB: the archived chromadb sqlite to load test docs from (POC_CHROMA_DB env).
var brokenDB = os.Getenv("POC_CHROMA_DB")

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: poc {load -n N | query \"text\" | dense \"text\" | mltest}")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "load":
		n := 1000
		if len(os.Args) > 3 && os.Args[2] == "-n" {
			n, _ = strconv.Atoi(os.Args[3])
		}
		cmdLoad(n)
	case "query":
		cmdSearch(os.Args[2], true)
	case "dense":
		cmdSearch(os.Args[2], false)
	case "mltest":
		cmdMLTest()
	default:
		fmt.Println("unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

// --- dense embedding via Ollama ---

func embed(text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": embModel, "input": text})
	resp, err := http.Post(ollamaEmbed, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding for %.40q", text)
	}
	return out.Embeddings[0], nil
}

// --- BM25 sparse (pure Go; Qdrant does IDF) ---

// tokenize lowercases and splits on non-letter/number runes — Unicode-aware,
// so Cyrillic (uk/ru), Latin (all EU langs) and Arabic tokenize correctly.
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// sparseVec builds (indices, term-frequency values). Token id = FNV-1a hash.
func sparseVec(text string) ([]uint32, []float32) {
	tf := map[uint32]float32{}
	for _, tok := range tokenize(text) {
		if len([]rune(tok)) < 2 {
			continue // drop single chars
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

func qClient() *qdrant.Client {
	c, err := qdrant.NewClient(&qdrant.Config{Host: "localhost", Port: 6334})
	if err != nil {
		panic(err)
	}
	return c
}

// --- load ---

func cmdLoad(n int) {
	if brokenDB == "" {
		fmt.Fprintln(os.Stderr, "poc load: set POC_CHROMA_DB to an archived chromadb sqlite")
		os.Exit(2)
	}
	ctx := context.Background()
	c := qClient()

	_ = c.DeleteCollection(ctx, collection)
	if err := c.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: collection,
		VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
			"dense": {Size: dim, Distance: qdrant.Distance_Cosine},
		}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			"sparse": {Modifier: qdrant.Modifier_Idf.Enum()},
		}),
	}); err != nil {
		panic(err)
	}

	db, err := sql.Open("sqlite", "file:"+brokenDB+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT d.id, d.string_value, w.string_value,
		  (SELECT string_value FROM embedding_metadata WHERE id=d.id AND key='room')
		FROM embedding_metadata d
		JOIN embedding_metadata w ON w.id=d.id AND w.key='wing'
		WHERE d.key='chroma:document' AND length(d.string_value) > 120
		  AND w.string_value NOT IN ('sessions')
		ORDER BY RANDOM() LIMIT ?`, n)
	if err != nil {
		panic(err)
	}
	defer func() { _ = rows.Close() }()

	var points []*qdrant.PointStruct
	start := time.Now()
	var embTime time.Duration
	count := 0
	for rows.Next() {
		var id int64
		var doc string
		var wing, room sql.NullString
		if err := rows.Scan(&id, &doc, &wing, &room); err != nil {
			panic(err)
		}
		t0 := time.Now()
		vec, err := embed(doc)
		embTime += time.Since(t0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "embed error:", err)
			continue
		}
		sIdx, sVal := sparseVec(doc)
		points = append(points, &qdrant.PointStruct{
			Id: qdrant.NewIDNum(uint64(id)),
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				"dense":  qdrant.NewVectorDense(vec),
				"sparse": qdrant.NewVectorSparse(sIdx, sVal),
			}),
			Payload: qdrant.NewValueMap(map[string]any{
				"wing": wing.String, "room": room.String, "text": truncate(doc, 240),
			}),
		})
		count++
		if count%200 == 0 {
			fmt.Printf("  embedded %d…\n", count)
		}
	}

	for i := 0; i < len(points); i += 256 {
		end := min(i+256, len(points))
		if _, err := c.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: collection, Points: points[i:end],
		}); err != nil {
			panic(err)
		}
	}
	info, _ := c.GetCollectionInfo(ctx, collection)
	fmt.Printf("\n=== LOAD DONE (hybrid) ===\n")
	fmt.Printf("  docs: %d · points: %d\n", count, info.GetPointsCount())
	fmt.Printf("  embed: %s (%.1f ms/doc) · wall: %s\n",
		embTime.Round(time.Millisecond), float64(embTime.Milliseconds())/float64(max(count, 1)),
		time.Since(start).Round(time.Millisecond))
}

// --- search (hybrid or dense-only) ---

func cmdSearch(q string, hybrid bool) {
	ctx := context.Background()
	c := qClient()
	dense, err := embed(q)
	if err != nil {
		panic(err)
	}
	t1 := time.Now()
	var res []*qdrant.ScoredPoint
	if hybrid {
		sIdx, sVal := sparseVec(q)
		res, err = c.Query(ctx, &qdrant.QueryPoints{
			CollectionName: collection,
			Prefetch: []*qdrant.PrefetchQuery{
				{Query: qdrant.NewQueryDense(dense), Using: qdrant.PtrOf("dense"), Limit: qdrant.PtrOf(uint64(20))},
				{Query: qdrant.NewQuerySparse(sIdx, sVal), Using: qdrant.PtrOf("sparse"), Limit: qdrant.PtrOf(uint64(20))},
			},
			Query:       qdrant.NewQueryFusion(qdrant.Fusion_RRF),
			Limit:       qdrant.PtrOf(uint64(5)),
			WithPayload: qdrant.NewWithPayload(true),
		})
	} else {
		res, err = c.Query(ctx, &qdrant.QueryPoints{
			CollectionName: collection,
			Query:          qdrant.NewQueryDense(dense),
			Using:          qdrant.PtrOf("dense"),
			Limit:          qdrant.PtrOf(uint64(5)),
			WithPayload:    qdrant.NewWithPayload(true),
		})
	}
	if err != nil {
		panic(err)
	}
	mode := "HYBRID"
	if !hybrid {
		mode = "DENSE"
	}
	fmt.Printf("\n=== %s: %q  (search %s) ===\n", mode, q, time.Since(t1).Round(time.Millisecond))
	for i, p := range res {
		pl := p.GetPayload()
		fmt.Printf("  [%d] score=%.4f  wing=%s\n      %s\n", i+1, p.GetScore(),
			pl["wing"].GetStringValue(), truncate(pl["text"].GetStringValue(), 120))
	}
}

// --- multilingual test ---

func cmdMLTest() {
	sents := []struct{ lang, text string }{
		{"en", "The memory server stores conversations and finds them by meaning."},
		{"uk", "Сервер памʼяті зберігає розмови і знаходить їх за змістом."},
		{"ru", "Сервер памяти хранит разговоры и находит их по смыслу."},
		{"es", "El servidor de memoria almacena conversaciones y las encuentra por significado."},
		{"de", "Der Speicherserver speichert Gespräche und findet sie nach Bedeutung."},
		{"it", "Il server di memoria memorizza le conversazioni e le trova per significato."},
		{"pl", "Serwer pamięci przechowuje rozmowy i znajduje je według znaczenia."},
		{"ro", "Serverul de memorie stochează conversații și le găsește după sens."},
		{"hu", "A memóriaszerver tárolja a beszélgetéseket és jelentés alapján találja meg őket."},
		{"lt", "Atminties serveris saugo pokalbius ir randa juos pagal prasmę."},
		{"lv", "Atmiņas serveris glabā sarunas un atrod tās pēc nozīmes."},
		{"et", "Mäluserver salvestab vestlusi ja leiab need tähenduse järgi."},
		{"fi", "Muistipalvelin tallentaa keskustelut ja löytää ne merkityksen perusteella."},
		{"fr", "Le serveur de mémoire stocke les conversations et les retrouve par sens."},
		{"ar", "خادم الذاكرة يخزن المحادثات ويعثر عليها حسب المعنى."},
	}
	distractor := "The bakery on the corner sells fresh bread every morning."
	vecs := make([][]float32, len(sents))
	for i, s := range sents {
		v, err := embed(s.text)
		if err != nil {
			panic(err)
		}
		vecs[i] = v
	}
	dv, _ := embed(distractor)
	fmt.Println("\n=== MULTILINGUAL: cosine vs EN (same meaning) ===")
	for i, s := range sents {
		fmt.Printf("  %-5s cos=%.4f\n", s.lang, cosine(vecs[0], vecs[i]))
	}
	fmt.Printf("  %-5s cos=%.4f  <-- distractor\n", "en-x", cosine(vecs[0], dv))
}

func cosine(a, b []float32) float32 {
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (fsqrt(na) * fsqrt(nb))
}

func fsqrt(x float32) float32 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func truncate(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
