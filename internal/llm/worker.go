package llm

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// WorkerOptions configures the EmbedWorker.
type WorkerOptions struct {
	BatchSize     int           // articles per cycle (default 32)
	MaxChunkChars int           // default 1500
	Overlap       int           // default 150
	Interval      time.Duration // idle poll interval (default 1m)
	MaxBackoff    time.Duration // outage backoff cap (default 5m)
	Log           *slog.Logger
}

type docEmbedder interface {
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbedWorker backfills embeddings for articles that lack them.
type EmbedWorker struct {
	st   domain.Store
	emb  domain.Embedder
	opts WorkerOptions
	log  *slog.Logger

	mu     sync.Mutex // serializes RunOnce
	failed map[int64]int
	kick   chan struct{}
}

const maxArticleFailures = 3

// NewEmbedWorker creates a worker. If emb implements EmbedDocs (as
// *OpenAIEmbedder does) the document prefix is applied.
func NewEmbedWorker(st domain.Store, emb domain.Embedder, opts WorkerOptions) *EmbedWorker {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 32
	}
	if opts.MaxChunkChars <= 0 {
		opts.MaxChunkChars = 1500
	}
	if opts.Overlap < 0 {
		opts.Overlap = 0
	} else if opts.Overlap == 0 {
		opts.Overlap = 150
	}
	opts.Interval = orDur(opts.Interval, time.Minute)
	opts.MaxBackoff = orDur(opts.MaxBackoff, 5*time.Minute)
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &EmbedWorker{st: st, emb: emb, opts: opts, log: log.With("component", "embed"),
		failed: map[int64]int{}, kick: make(chan struct{}, 1)}
}

// Kick wakes the Run loop immediately (e.g. after an ingest run).
func (w *EmbedWorker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// chunksFor builds the chunks to embed for an article. The first chunk always
// starts with the title. An article without body text is embedded by title
// only (and by URL if even the title is empty), because the store cannot mark
// an article as done with zero chunks.
func (w *EmbedWorker) chunksFor(a domain.Article) []string {
	title := strings.TrimSpace(a.Title)
	if r := []rune(title); len(r) > w.opts.MaxChunkChars/2 {
		title = string(r[:w.opts.MaxChunkChars/2])
	}
	body := strings.TrimSpace(a.Content)
	if body == "" {
		body = strings.TrimSpace(a.Summary)
	}
	if title == "" && body == "" {
		return []string{a.URL}
	}
	if body == "" {
		return []string{title}
	}
	if title == "" {
		return ChunkText(body, w.opts.MaxChunkChars, w.opts.Overlap)
	}
	return ChunkText(title+"\n\n"+body, w.opts.MaxChunkChars, w.opts.Overlap)
}

func (w *EmbedWorker) embedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if de, ok := w.emb.(docEmbedder); ok {
		return de.EmbedDocs(ctx, texts)
	}
	return w.emb.Embed(ctx, texts)
}

// RunOnce processes up to BatchSize pending articles and returns how many
// were stored. A transient embedder/store error aborts the cycle and is
// returned (articles already saved stay saved). Articles the server
// permanently rejects are skipped after a few attempts.
func (w *EmbedWorker) RunOnce(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	skipped := 0
	for _, n := range w.failed {
		if n >= maxArticleFailures {
			skipped++
		}
	}
	arts, err := w.st.ArticlesNeedingEmbedding(ctx, w.emb.Model(), w.opts.BatchSize+skipped)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, a := range arts {
		if done >= w.opts.BatchSize {
			break
		}
		if w.failed[a.ID] >= maxArticleFailures {
			continue
		}
		if err := ctx.Err(); err != nil {
			return done, err
		}
		texts := w.chunksFor(a)
		vecs, err := w.embedTexts(ctx, texts)
		if err != nil {
			if IsPermanent(err) {
				w.failed[a.ID]++
				w.log.Warn("artículo rechazado por el servidor de embeddings", "article", a.ID, "attempt", w.failed[a.ID], "err", err)
				continue
			}
			return done, err
		}
		cv := make([]domain.ChunkVec, len(texts))
		for i := range texts {
			cv[i] = domain.ChunkVec{Index: i, Text: texts[i], Vec: vecs[i]}
		}
		if err := w.st.SaveEmbeddings(ctx, a.ID, w.emb.Model(), cv); err != nil {
			return done, err
		}
		delete(w.failed, a.ID)
		done++
	}
	return done, nil
}

// Run loops until ctx is done. On errors it backs off exponentially (up to
// MaxBackoff), logs once per outage and resumes automatically.
func (w *EmbedWorker) Run(ctx context.Context) {
	var backoff time.Duration
	down := false
	for ctx.Err() == nil {
		n, err := w.RunOnce(ctx)
		var wait time.Duration
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			if backoff == 0 {
				backoff = w.opts.Interval
			} else {
				backoff = min(backoff*2, w.opts.MaxBackoff)
			}
			if !down {
				down = true
				w.log.Warn("embeddings no disponibles; reintentando con backoff", "err", err, "next_retry", backoff)
			}
			wait = backoff
		default:
			if down {
				down = false
				w.log.Info("embeddings operativos de nuevo")
			}
			backoff = 0
			if n > 0 {
				w.log.Debug("embeddings guardados", "articles", n)
			}
			if n >= w.opts.BatchSize {
				continue // more pending
			}
			wait = w.opts.Interval
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.kick:
			t.Stop()
		case <-t.C:
		}
	}
}
