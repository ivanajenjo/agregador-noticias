package store

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Vector search performance (brute force cosine, float32, see BenchmarkSearchVector50k):
// measured on the dev container (linux/amd64, GOMAXPROCS=see bench output):
//   50k articles x 768 dims, 1 chunk each: BENCH_NUMBERS
// Memory per 50k x 768 float32 vectors: 50_000*768*4 = 153.6 MB (+ ~4 MB of
// per-entry overhead). int8 quantisation would be ~38 MB (+ 4 B scale per
// vector) at a small recall cost; not implemented: vectorIndex is the seam
// where it (or sqlite-vec/Qdrant) can be swapped in.

// ---- blob codec -------------------------------------------------------------

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= inv
	}
}

// ---- in-memory index --------------------------------------------------------

// vecEntry holds all chunks of one article for one model: n chunks of dim
// floats, L2-normalised, contiguous.
type vecEntry struct {
	article int64
	dim     int
	idxs    []int32
	vecs    []float32
}

type modelIndex struct {
	entries []*vecEntry
	pos     map[int64]int // article -> position in entries
}

// vectorIndex is an in-memory cache of normalised vectors, per model, loaded
// lazily from the DB and kept up to date by replace(). Safe for concurrent use.
type vectorIndex struct {
	mu     sync.RWMutex
	models map[string]*modelIndex
}

func newVectorIndex() *vectorIndex { return &vectorIndex{models: map[string]*modelIndex{}} }

// ensure loads model from the DB if it is not cached yet.
func (v *vectorIndex) ensure(ctx context.Context, db *sql.DB, model string) error {
	v.mu.RLock()
	_, ok := v.models[model]
	v.mu.RUnlock()
	if ok {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.models[model]; ok {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT article_id, idx, vec FROM article_chunks WHERE model=? ORDER BY article_id, idx`, model)
	if err != nil {
		return err
	}
	defer rows.Close()
	mi := &modelIndex{pos: map[int64]int{}}
	var cur *vecEntry
	for rows.Next() {
		var id int64
		var idx int32
		var blob []byte
		if err := rows.Scan(&id, &idx, &blob); err != nil {
			return err
		}
		vec := decodeVec(blob)
		normalize(vec)
		if cur == nil || cur.article != id {
			cur = &vecEntry{article: id, dim: len(vec)}
			mi.pos[id] = len(mi.entries)
			mi.entries = append(mi.entries, cur)
		}
		if len(vec) != cur.dim {
			continue // inconsistent chunk; ignore
		}
		cur.idxs = append(cur.idxs, idx)
		cur.vecs = append(cur.vecs, vec...)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	v.models[model] = mi
	return nil
}

// replace sets the vectors of (article, model) in the cache if that model is loaded.
func (v *vectorIndex) replace(model string, article int64, chunks []domain.ChunkVec) {
	v.mu.Lock()
	defer v.mu.Unlock()
	mi, ok := v.models[model]
	if !ok {
		return // will be read from the DB when first loaded
	}
	if p, ok := mi.pos[article]; ok { // swap-delete
		last := len(mi.entries) - 1
		mi.entries[p] = mi.entries[last]
		mi.pos[mi.entries[p].article] = p
		mi.entries[last] = nil
		mi.entries = mi.entries[:last]
		delete(mi.pos, article)
	}
	if len(chunks) == 0 {
		return
	}
	e := &vecEntry{article: article, dim: len(chunks[0].Vec)}
	for _, c := range chunks {
		vec := append([]float32(nil), c.Vec...)
		normalize(vec)
		e.idxs = append(e.idxs, int32(c.Index))
		e.vecs = append(e.vecs, vec...)
	}
	mi.pos[article] = len(mi.entries)
	mi.entries = append(mi.entries, e)
}

// invalidate drops the cached model (next search reloads from the DB).
func (v *vectorIndex) invalidate(model string) {
	v.mu.Lock()
	delete(v.models, model)
	v.mu.Unlock()
}

type scored struct {
	article int64
	chunk   int32
	score   float32
}

type minHeap []scored

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(scored)) }
func (h *minHeap) Pop() any {
	o := *h
	x := o[len(o)-1]
	*h = o[:len(o)-1]
	return x
}

func dot(a, b []float32) float32 {
	var s0, s1, s2, s3 float32
	n := len(a)
	b = b[:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < n; i++ {
		s0 += a[i] * b[i]
	}
	return s0 + s1 + s2 + s3
}

// search returns the best chunk per article, top-k by score desc. allowed==nil
// means all articles. q must be normalised.
func (v *vectorIndex) search(model string, q []float32, allowed map[int64]struct{}, k int) []scored {
	v.mu.RLock()
	defer v.mu.RUnlock()
	mi := v.models[model]
	if mi == nil || len(mi.entries) == 0 {
		return nil
	}
	ents := mi.entries
	workers := min(runtime.GOMAXPROCS(0), 4)
	if len(ents) < 2000 {
		workers = 1
	}
	parts := make([]minHeap, workers)
	var wg sync.WaitGroup
	step := (len(ents) + workers - 1) / workers
	for w := 0; w < workers; w++ {
		lo, hi := w*step, min((w+1)*step, len(ents))
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			h := minHeap{}
			for _, e := range ents[lo:hi] {
				if e.dim != len(q) {
					continue
				}
				if allowed != nil {
					if _, ok := allowed[e.article]; !ok {
						continue
					}
				}
				best, bi := float32(-2), int32(0)
				for c := range e.idxs {
					if s := dot(q, e.vecs[c*e.dim:(c+1)*e.dim]); s > best {
						best, bi = s, e.idxs[c]
					}
				}
				if len(h) < k {
					heap.Push(&h, scored{e.article, bi, best})
				} else if best > h[0].score {
					h[0] = scored{e.article, bi, best}
					heap.Fix(&h, 0)
				}
			}
			parts[w] = h
		}(w, lo, hi)
	}
	wg.Wait()
	var all []scored
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].article > all[j].article
	})
	if len(all) > k {
		all = all[:k]
	}
	return all
}

// ---- Store methods ----------------------------------------------------------

// ArticlesNeedingEmbedding returns articles (newest first) not yet embedded
// with model. An article counts as embedded once SaveEmbeddings ran for it,
// even with zero chunks (so empty articles are not retried forever).
func (s *SQLite) ArticlesNeedingEmbedding(ctx context.Context, model string, limit int) ([]domain.Article, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.r.QueryContext(ctx, `SELECT `+articleCols+` FROM articles a
WHERE NOT EXISTS (SELECT 1 FROM article_embeds e WHERE e.article_id=a.id AND e.model=?)
ORDER BY `+dateExpr+` DESC, a.id DESC LIMIT ?`, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveEmbeddings atomically replaces all chunks of (article, model).
func (s *SQLite) SaveEmbeddings(ctx context.Context, articleID int64, model string, chunks []domain.ChunkVec) error {
	if model == "" {
		return errors.New("store: empty model")
	}
	for _, c := range chunks {
		if len(c.Vec) == 0 || len(c.Vec) != len(chunks[0].Vec) {
			return fmt.Errorf("store: chunk %d has inconsistent/empty vector", c.Index)
		}
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM article_chunks WHERE article_id=? AND model=?`, articleID, model); err != nil {
			return err
		}
		for _, c := range chunks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO article_chunks (article_id,model,idx,text,dim,vec) VALUES (?,?,?,?,?,?)`,
				articleID, model, c.Index, c.Text, len(c.Vec), encodeVec(c.Vec)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO article_embeds (article_id,model,chunks,created_at) VALUES (?,?,?,?)
ON CONFLICT(article_id,model) DO UPDATE SET chunks=excluded.chunks, created_at=excluded.created_at`,
			articleID, model, len(chunks), time.Now().Unix())
		return err
	})
	if err != nil {
		return err
	}
	s.vecs.replace(model, articleID, chunks)
	return nil
}

// SearchVector does a cosine search over cached vectors of model, returning the
// best chunk per article (Hit.Chunk, Hit.Snippet = chunk text), best first.
func (s *SQLite) SearchVector(ctx context.Context, query []float32, model string, f domain.Filter, k int) ([]domain.Hit, error) {
	if k <= 0 {
		k = f.Limit
	}
	if k <= 0 {
		k = 10
	}
	if len(query) == 0 {
		return nil, nil
	}
	if err := s.vecs.ensure(ctx, s.r, model); err != nil {
		return nil, err
	}
	var allowed map[int64]struct{}
	if !isTrivial(f) {
		w, args := where(f)
		rows, err := s.r.QueryContext(ctx, `SELECT a.id FROM articles a WHERE `+w, args...)
		if err != nil {
			return nil, err
		}
		allowed = map[int64]struct{}{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			allowed[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	q := append([]float32(nil), query...)
	normalize(q)
	res := s.vecs.search(model, q, allowed, k+max(f.Offset, 0))
	if f.Offset > 0 {
		if f.Offset >= len(res) {
			return nil, nil
		}
		res = res[f.Offset:]
	}
	out := make([]domain.Hit, 0, len(res))
	for _, r := range res {
		a, err := scanArticle(s.r.QueryRowContext(ctx, `SELECT `+articleCols+` FROM articles a WHERE a.id=?`, r.article))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var text string
		_ = s.r.QueryRowContext(ctx, `SELECT text FROM article_chunks WHERE article_id=? AND model=? AND idx=?`, r.article, model, r.chunk).Scan(&text)
		out = append(out, domain.Hit{Article: a, Score: float64(r.score), Chunk: int(r.chunk), Snippet: truncate(text, 300)})
	}
	return out, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
