// Package rag implements domain.Retriever: hybrid search (vector + FTS fused
// with Reciprocal Rank Fusion) and retrieval-augmented answers.
package rag

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ErrLLMUnavailable is returned (wrapped) by Ask when the chat model could not
// produce an answer before the first token. Sources have already been
// delivered through onSources.
var ErrLLMUnavailable = errors.New("rag: modelo de lenguaje no disponible")

// Options tunes the Service. Zero values get sensible defaults.
type Options struct {
	K            int           // default results/sources (default 8)
	RRFK         float64       // RRF constant (default 60)
	WeightVector float64       // default 1
	WeightText   float64       // default 1
	Candidates   int           // per-method candidate pool (default max(3*k, 30))
	EmbedTimeout time.Duration // query embedding timeout (default 5s)
	QueryPrefix  string        // used only if the embedder has no EmbedQuery method

	ContextChars int // total prompt budget for sources (default 12000)
	SnippetChars int // max chars per source (default 1200)
	PerLeaning   int // sources per leaning in compare mode (default 3)
	MaxLeanings  int // max leanings compared at once (default 6)

	Now func() time.Time // for tests
}

func (o *Options) defaults() {
	if o.K <= 0 {
		o.K = 8
	}
	if o.RRFK <= 0 {
		o.RRFK = 60
	}
	if o.WeightVector <= 0 {
		o.WeightVector = 1
	}
	if o.WeightText <= 0 {
		o.WeightText = 1
	}
	if o.EmbedTimeout <= 0 {
		o.EmbedTimeout = 5 * time.Second
	}
	if o.ContextChars <= 0 {
		o.ContextChars = 12000
	}
	if o.SnippetChars <= 0 {
		o.SnippetChars = 1200
	}
	if o.PerLeaning <= 0 {
		o.PerLeaning = 3
	}
	if o.MaxLeanings <= 0 {
		o.MaxLeanings = 6
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Service implements domain.Retriever.
type Service struct {
	st   domain.Store
	emb  domain.Embedder
	chat domain.Chat
	opts Options
	log  *slog.Logger
}

var _ domain.Retriever = (*Service)(nil)

// New creates a Service. emb and chat may be nil (text-only / no answers).
func New(st domain.Store, emb domain.Embedder, chat domain.Chat, opts Options, log *slog.Logger) *Service {
	opts.defaults()
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, emb: emb, chat: chat, opts: opts, log: log.With("component", "rag")}
}

type queryEmbedder interface {
	EmbedQuery(ctx context.Context, q string) ([]float32, error)
}

// embedQuery embeds q with a short timeout; any error means "text only".
func (s *Service) embedQuery(ctx context.Context, q string) ([]float32, error) {
	if s.emb == nil {
		return nil, errors.New("sin embedder")
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.EmbedTimeout)
	defer cancel()
	if qe, ok := s.emb.(queryEmbedder); ok {
		return qe.EmbedQuery(ctx, q)
	}
	v, err := s.emb.Embed(ctx, []string{s.opts.QueryPrefix + q})
	if err != nil {
		return nil, err
	}
	if len(v) != 1 || len(v[0]) == 0 {
		return nil, errors.New("embedding de consulta inválido")
	}
	return v[0], nil
}

// Search runs hybrid search, falling back to text-only (ModeText) whenever
// embeddings are unavailable. It only errors if the text search itself fails.
func (s *Service) Search(ctx context.Context, query string, f domain.Filter, k int) ([]domain.Hit, domain.SearchMode, error) {
	if k <= 0 {
		k = s.opts.K
	}
	var vec []float32
	var embErr error
	var wg sync.WaitGroup
	var textHits []domain.Hit
	var textErr error
	wg.Add(1)
	go func() { // FTS runs while the query is being embedded
		defer wg.Done()
		textHits, textErr = s.searchText(ctx, query, f, k)
	}()
	vec, embErr = s.embedQuery(ctx, query)
	if embErr != nil && ctx.Err() == nil {
		s.log.Debug("embedding de consulta no disponible; búsqueda solo textual", "err", embErr)
	}
	vecHits, vecErr := []domain.Hit(nil), error(nil)
	if embErr == nil {
		vecHits, vecErr = s.searchVector(ctx, vec, f, k)
	}
	wg.Wait()
	return s.finish(ctx, textHits, textErr, vecHits, vecErr, k)
}

func (s *Service) cand(k int) int { return max(s.opts.Candidates, 3*k, 30) }

func (s *Service) searchText(ctx context.Context, q string, f domain.Filter, k int) ([]domain.Hit, error) {
	f.Limit, f.Offset = 0, 0
	return s.st.SearchText(ctx, q, f, s.cand(k))
}

func (s *Service) searchVector(ctx context.Context, v []float32, f domain.Filter, k int) ([]domain.Hit, error) {
	f.Limit, f.Offset = 0, 0
	return s.st.SearchVector(ctx, v, s.emb.Model(), f, s.cand(k))
}

func (s *Service) finish(ctx context.Context, textHits []domain.Hit, textErr error, vecHits []domain.Hit, vecErr error, k int) ([]domain.Hit, domain.SearchMode, error) {
	if vecErr != nil && ctx.Err() == nil {
		s.log.Warn("búsqueda vectorial fallida; solo texto", "err", vecErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.ModeText, err
	}
	if vecErr != nil || len(vecHits) == 0 {
		if textErr != nil {
			return nil, domain.ModeText, textErr
		}
		return dedupe(textHits, k), domain.ModeText, nil
	}
	if textErr != nil {
		s.log.Warn("búsqueda textual fallida; solo vectores", "err", textErr)
		textHits = nil
	}
	return Fuse(s.opts.RRFK, vecHits, textHits, s.opts.WeightVector, s.opts.WeightText, k), domain.ModeHybrid, nil
}

// dedupe keeps the first (best) hit per article, preserving order.
func dedupe(hits []domain.Hit, limit int) []domain.Hit {
	seen := map[int64]bool{}
	var out []domain.Hit
	for _, h := range hits {
		if seen[h.Article.ID] {
			continue
		}
		seen[h.Article.ID] = true
		out = append(out, h)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Fuse merges a vector ranking and a text ranking with weighted Reciprocal
// Rank Fusion: score(article) = sum_i w_i / (rrfK + rank_i), rank starting at 1
// and only the best-ranked hit per article counted in each list. The result
// is de-duplicated per article, sorted by fused score (ties by article ID)
// and cut to limit (<=0 means no cut). Hit.Score holds the fused score; the
// vector hit supplies Chunk, the text hit supplies Snippet when present.
func Fuse(rrfK float64, vec, txt []domain.Hit, wVec, wTxt float64, limit int) []domain.Hit {
	type acc struct {
		hit   domain.Hit
		score float64
	}
	m := map[int64]*acc{}
	add := func(hits []domain.Hit, w float64, isVec bool) {
		seen := map[int64]bool{}
		rank := 0
		for _, h := range hits {
			if seen[h.Article.ID] {
				continue
			}
			seen[h.Article.ID] = true
			rank++
			a := m[h.Article.ID]
			if a == nil {
				a = &acc{hit: h}
				m[h.Article.ID] = a
			}
			a.score += w / (rrfK + float64(rank))
			if isVec {
				a.hit.Chunk = h.Chunk
				if a.hit.Snippet == "" {
					a.hit.Snippet = h.Snippet
				}
			} else {
				if h.Snippet != "" {
					a.hit.Snippet = h.Snippet
				}
				if a.hit.Chunk == 0 && !isVec && len(vec) == 0 {
					a.hit.Chunk = h.Chunk
				}
			}
		}
	}
	add(vec, wVec, true)
	add(txt, wTxt, false)
	out := make([]domain.Hit, 0, len(m))
	for _, a := range m {
		a.hit.Score = a.score
		out = append(out, a.hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Article.ID < out[j].Article.ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func leaningKey(l string) string {
	l = strings.TrimSpace(l)
	if l == "" {
		return "sin clasificar"
	}
	return l
}
