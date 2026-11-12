package annotate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// WorkerOptions configures the Worker.
type WorkerOptions struct {
	Model          string        // required: stored with every row
	BatchSize      int           // articles per cycle (default 8)
	Interval       time.Duration // pause between cycles (default 2m)
	MaxBackoff     time.Duration // outage backoff cap (default 10m)
	ArticleTimeout time.Duration // per article (default 90s)
	Retries        int           // extra attempts on unusable output (default 1)
	Seed           *int
	Log            *slog.Logger
}

// Prober is optionally implemented by the Completer (*Client does): it is
// called before every batch so nothing runs while the model host is down.
type Prober interface {
	Probe(ctx context.Context) error
}

// Worker backfills annotations for the newest articles lacking them.
type Worker struct {
	st   domain.AnnotationStore
	c    Completer
	an   *Annotator
	opts WorkerOptions
	log  *slog.Logger

	mu     sync.Mutex
	failed map[int64]int
	kick   chan struct{}
}

const maxArticleFailures = 3

// NewWorker creates a worker.
func NewWorker(st domain.AnnotationStore, c Completer, o WorkerOptions) *Worker {
	if o.BatchSize <= 0 {
		o.BatchSize = 8
	}
	if o.Interval <= 0 {
		o.Interval = 2 * time.Minute
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 10 * time.Minute
	}
	if o.ArticleTimeout <= 0 {
		o.ArticleTimeout = 90 * time.Second
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	return &Worker{st: st, c: c, an: &Annotator{C: c, Seed: o.Seed, Retries: o.Retries}, opts: o,
		log: log.With("component", "annotate"), failed: map[int64]int{}, kick: make(chan struct{}, 1)}
}

// Kick wakes Run immediately.
func (w *Worker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Model is the model name rows are stored under.
func (w *Worker) Model() string { return w.opts.Model }

// RunOnce annotates up to BatchSize articles sequentially and returns how
// many were saved. An outage aborts the cycle with an error.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p, ok := w.c.(Prober); ok {
		if err := p.Probe(ctx); err != nil {
			return 0, fmt.Errorf("servidor de anotaciones inaccesible: %w", err)
		}
	}
	skipped := 0
	for _, n := range w.failed {
		if n >= maxArticleFailures {
			skipped++
		}
	}
	arts, err := w.st.ArticlesNeedingAnnotation(ctx, domain.AnnoTone, w.opts.Model, w.opts.BatchSize+skipped)
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
		actx, cancel := context.WithTimeout(ctx, w.opts.ArticleTimeout)
		res, err := w.an.Annotate(actx, InputFor(a))
		timedOut := actx.Err() != nil && ctx.Err() == nil
		cancel()
		if err != nil {
			switch {
			case ctx.Err() != nil:
				return done, ctx.Err()
			case timedOut:
				w.failed[a.ID]++
				w.log.Warn("anotación: tiempo agotado", "article", a.ID, "attempt", w.failed[a.ID])
				continue
			case IsOutage(err):
				return done, err
			default:
				w.failed[a.ID]++
				w.log.Warn("anotación descartada", "article", a.ID, "attempt", w.failed[a.ID], "err", err)
				continue
			}
		}
		if err := w.save(ctx, a.ID, res); err != nil {
			return done, err
		}
		if res.Tone == "" {
			// partial answer: keep what is valid but retry the tone a few times
			w.failed[a.ID]++
			continue
		}
		delete(w.failed, a.ID)
		done++
	}
	return done, nil
}

type annoData struct {
	Rationale string `json:"rationale,omitempty"`
	IsOpinion *bool  `json:"is_opinion,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Prompt    int    `json:"prompt"`
}

func (w *Worker) save(ctx context.Context, id int64, r Result) error {
	d, _ := json.Marshal(annoData{Rationale: r.Rationale, IsOpinion: r.IsOpinion, Raw: truncRunes(r.Raw, 500), Prompt: PromptVersion})
	now := time.Now().UTC()
	put := func(kind, label string) error {
		if label == "" {
			return nil
		}
		return w.st.SaveAnnotation(ctx, domain.Annotation{ArticleID: id, Kind: kind, Model: w.opts.Model,
			Label: label, Score: r.Confidence, Data: string(d), CreatedAt: now})
	}
	op := ""
	if r.IsOpinion != nil {
		op = "no"
		if *r.IsOpinion {
			op = "sí"
		}
	}
	for _, kv := range [][2]string{{domain.AnnoTone, r.Tone}, {domain.AnnoIntent, r.Intent}, {domain.AnnoFrame, r.Frame}, {domain.AnnoOpinion, op}} {
		if err := put(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// Job is the sched.Job-style entry point: one batch, a human message.
func (w *Worker) Job(ctx context.Context) (string, error) {
	n, err := w.RunOnce(ctx)
	return fmt.Sprintf("%d artículos anotados", n), err
}

// Run loops until ctx is done: one batch per Interval (back-to-back never),
// exponential backoff on outages, one log line per outage.
func (w *Worker) Run(ctx context.Context) {
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
				w.log.Warn("modelo de anotaciones no disponible; reintentando con backoff", "err", err, "next_retry", backoff)
			}
			wait = backoff
		default:
			if down {
				down = false
				w.log.Info("modelo de anotaciones operativo de nuevo")
			}
			backoff = 0
			if n > 0 {
				w.log.Debug("artículos anotados", "articles", n)
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
