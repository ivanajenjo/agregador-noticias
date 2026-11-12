// Package digest builds the daily summary ("Resumen diario"): headline stats,
// top stories, rising terms, one distinctive headline per outlet and the
// front-page screenshots of the day. All of it is deterministic; an optional,
// strictly grounded LLM narrative can be added on top (see narrative.go) and
// is dropped silently when the model is unavailable or misbehaves.
package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Deps are the collaborators of the Builder. Store, Trends and Digests are
// required; Chat is optional (no narrative without it).
type Deps struct {
	Store   domain.Store
	Trends  domain.TrendProvider
	Digests domain.DigestStore
	Chat    domain.Chat // may be nil
	// Revisions is optional: anything implementing domain.RevisionCounter.
	// When nil, Store is tried through a type assertion.
	Revisions any
	Log       *slog.Logger
}

// Options configure the Builder. Zero values get defaults.
type Options struct {
	UseLLM     bool
	Location   *time.Location
	Now        func() time.Time
	Window     time.Duration // default 24h
	TopStories int           // stories shown (default 6)
	LLMStories int           // stories that get a narrative (default 3)
	Terms      int           // rising terms shown (default 8)
	MaxShots   int           // default 12
	LLMTimeout time.Duration // default 2 min
}

// Builder implements domain.DigestGenerator.
type Builder struct {
	d   Deps
	o   Options
	log *slog.Logger
}

var _ domain.DigestGenerator = (*Builder)(nil)

// New returns a Builder.
func New(d Deps, o Options) *Builder {
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Window <= 0 {
		o.Window = 24 * time.Hour
	}
	if o.TopStories <= 0 {
		o.TopStories = 6
	}
	if o.LLMStories <= 0 {
		o.LLMStories = 3
	}
	if o.Terms <= 0 {
		o.Terms = 8
	}
	if o.MaxShots <= 0 {
		o.MaxShots = 12
	}
	if o.LLMTimeout <= 0 {
		o.LLMTimeout = 2 * time.Minute
	}
	l := d.Log
	if l == nil {
		l = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Builder{d: d, o: o, log: l}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

const DayLayout = "2006-01-02"

// RunOnce builds and stores today's digest. It is the scheduler job entry point.
func (b *Builder) RunOnce(ctx context.Context) (string, error) {
	d, err := b.Build(ctx, b.o.Now())
	if err != nil {
		return "", err
	}
	narr := "sin narrativa"
	if d.Model != "" {
		narr = "narrativa de " + d.Model
	}
	return fmt.Sprintf("resumen %s: %d artículos, %d historias, %s", d.Day, d.Articles, len(d.Stories), narr), nil
}

// Build builds the digest of the local day containing `day` and stores it,
// overwriting any previous digest of that day. For today the window is the 24h
// ending now; for a past day it is the 24h ending at the end of that day.
func (b *Builder) Build(ctx context.Context, day time.Time) (*domain.Digest, error) {
	if b.d.Store == nil || b.d.Trends == nil || b.d.Digests == nil {
		return nil, errors.New("digest: faltan dependencias (store, trends, digests)")
	}
	loc := b.o.Location
	now := b.o.Now()
	dl := day.In(loc)
	start := time.Date(dl.Year(), dl.Month(), dl.Day(), 0, 0, 0, 0, loc)
	to := start.AddDate(0, 0, 1)
	if now.Before(to) {
		to = now
	}
	if to.Before(start) {
		to = start
	}
	from := to.Add(-b.o.Window)

	dg := &domain.Digest{Day: start.Format(DayLayout), GeneratedAt: now.UTC(), WindowFrom: from.UTC(), WindowTo: to.UTC()}

	srcs, err := b.d.Store.ListSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("digest: fuentes: %w", err)
	}
	names := map[string]domain.Source{}
	var enabled []domain.Source
	for _, s := range srcs {
		names[s.ID] = s
		if s.IsEnabled() {
			enabled = append(enabled, s)
		}
	}
	name := func(id string) string {
		if s, ok := names[id]; ok && s.Name != "" {
			return s.Name
		}
		return id
	}

	// Per-outlet articles: stats + distinctive headline.
	perOutlet := map[string][]domain.Article{}
	for _, s := range enabled {
		arts, err := b.d.Store.ListArticles(ctx, domain.Filter{SourceIDs: []string{s.ID}, From: from, To: to, Limit: 200})
		if err != nil {
			b.log.Warn("digest: artículos", "source", s.ID, "err", err)
			continue
		}
		if len(arts) > 0 {
			perOutlet[s.ID] = arts
			n, err := b.d.Store.CountArticles(ctx, domain.Filter{SourceIDs: []string{s.ID}, From: from, To: to})
			if err != nil || n < int64(len(arts)) {
				n = int64(len(arts))
			}
			dg.Articles += int(n)
			dg.Outlets++
		}
	}
	dg.Distinctive = distinctive(perOutlet, enabled, names)

	topts := domain.TrendOptions{Window: b.o.Window, Now: to}

	// Stories.
	so := topts
	so.Limit = b.o.TopStories
	stories, err := b.d.Trends.Stories(ctx, so)
	if err != nil {
		b.log.Warn("digest: historias", "err", err)
	}
	n := 0
	for i, st := range stories {
		if i >= b.o.TopStories {
			break
		}
		ds := domain.DigestStory{ID: st.ID, Title: strings.TrimSpace(st.Title), Sources: st.Sources, Articles: st.Articles,
			Leanings: st.Leanings, FirstAt: st.FirstAt, Terms: st.Terms}
		for _, c := range st.Coverage {
			if c.SourceID == st.FirstBy {
				ds.FirstBy = covName(c)
			}
		}
		if ds.FirstBy == "" && st.FirstBy != "" {
			ds.FirstBy = name(st.FirstBy)
		}
		for _, c := range st.Missing {
			ds.Missing = append(ds.Missing, covName(c))
		}
		for _, c := range st.Coverage {
			if len(c.Headlines) == 0 || len(ds.Headlines) >= 8 {
				continue
			}
			h := c.Headlines[0]
			n++
			ds.Headlines = append(ds.Headlines, domain.DigestHeadline{N: n, SourceID: c.SourceID, Outlet: covName(c),
				Leaning: c.Leaning, Title: strings.TrimSpace(h.Title), URL: h.URL, At: h.At})
		}
		dg.Stories = append(dg.Stories, ds)
	}

	// Rising terms.
	to2 := topts
	to2.Limit = b.o.Terms
	if rep, err := b.d.Trends.Trending(ctx, to2); err != nil {
		b.log.Warn("digest: términos", "err", err)
	} else if rep != nil {
		for i, t := range rep.Terms {
			if i >= b.o.Terms {
				break
			}
			dg.Rising = append(dg.Rising, domain.DigestTerm{Term: t.Term, Key: t.Key, Count: t.Count, Sources: t.Sources, New: t.New})
		}
		if dg.Articles == 0 {
			dg.Articles = rep.Articles
		}
	}

	// Modified headlines (optional).
	var rc domain.RevisionCounter
	if r, ok := b.d.Revisions.(domain.RevisionCounter); ok {
		rc = r
	} else if r, ok := b.d.Store.(domain.RevisionCounter); ok {
		rc = r
	}
	if rc != nil {
		if c, err := rc.CountHeadlineChanges(ctx, from, to); err == nil {
			dg.Changed = &c
		} else {
			b.log.Warn("digest: revisiones", "err", err)
		}
	}

	// Screenshots: latest per outlet inside the window.
	if shots, err := b.d.Store.ListScreenshots(ctx, "", from, to); err != nil {
		b.log.Warn("digest: capturas", "err", err)
	} else {
		latest := map[string]domain.Screenshot{}
		for _, s := range shots {
			if p, ok := latest[s.SourceID]; !ok || s.TakenAt.After(p.TakenAt) {
				latest[s.SourceID] = s
			}
		}
		for _, s := range enabled { // config order is stable
			if sh, ok := latest[s.ID]; ok && len(dg.Shots) < b.o.MaxShots {
				dg.Shots = append(dg.Shots, domain.DigestShot{SourceID: s.ID, Outlet: name(s.ID), TakenAt: sh.TakenAt.UTC(), Path: sh.Path})
			}
		}
	}

	dg.Lead = lead(dg)

	if b.o.UseLLM && b.d.Chat != nil && len(dg.Stories) > 0 {
		b.narrate(ctx, dg, loc)
	}

	if err := b.d.Digests.SaveDigest(ctx, dg); err != nil {
		return nil, fmt.Errorf("digest: guardar: %w", err)
	}
	return dg, nil
}

func covName(c domain.SourceCoverage) string {
	if c.Name != "" {
		return c.Name
	}
	return c.SourceID
}

// lead is the deterministic intro paragraph.
func lead(d *domain.Digest) string {
	if d.Articles == 0 && len(d.Stories) == 0 {
		return "No hay artículos recogidos en este periodo."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "En las últimas %d horas se han recogido %d artículos de %d medios.", int(d.WindowTo.Sub(d.WindowFrom).Round(time.Hour).Hours()), d.Articles, d.Outlets)
	if len(d.Stories) > 0 {
		s := d.Stories[0]
		fmt.Fprintf(&sb, " La historia con más cobertura es «%s», en %d medios.", s.Title, s.Sources)
	}
	var terms []string
	for _, t := range d.Rising {
		if len(terms) < 3 {
			terms = append(terms, t.Term)
		}
	}
	if len(terms) > 0 {
		sb.WriteString(" Términos en alza: " + strings.Join(terms, ", ") + ".")
	}
	if d.Changed != nil && *d.Changed > 0 {
		fmt.Fprintf(&sb, " Se han modificado %d titulares ya publicados.", *d.Changed)
	}
	return sb.String()
}

// ---- distinctive headline per outlet ----

func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, w := range f {
		if len([]rune(w)) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

// distinctive picks, for each outlet, the headline whose words are the rarest
// across all headlines of the window (mean IDF), i.e. what only that outlet
// is talking about. Deterministic; ties broken by recency then title.
func distinctive(per map[string][]domain.Article, order []domain.Source, names map[string]domain.Source) []domain.DigestHeadline {
	df := map[string]int{}
	total := 0
	for _, arts := range per {
		for _, a := range arts {
			total++
			seen := map[string]bool{}
			for _, w := range tokens(a.Title) {
				if !seen[w] {
					seen[w] = true
					df[w]++
				}
			}
		}
	}
	var out []domain.DigestHeadline
	for _, s := range order {
		arts := per[s.ID]
		bestScore, best := -1.0, -1
		for i, a := range arts {
			tk := tokens(a.Title)
			if len(tk) < 3 {
				continue
			}
			sum := 0.0
			for _, w := range tk {
				sum += math.Log(1 + float64(total)/float64(df[w]))
			}
			sc := sum / float64(len(tk))
			if sc > bestScore+1e-9 || (math.Abs(sc-bestScore) <= 1e-9 && better(a, arts[best])) {
				bestScore, best = sc, i
			}
		}
		if best < 0 {
			continue
		}
		a := arts[best]
		at := a.PublishedAt
		if at.IsZero() {
			at = a.FirstSeenAt
		}
		out = append(out, domain.DigestHeadline{SourceID: s.ID, Outlet: firstNonEmpty(names[s.ID].Name, s.ID),
			Leaning: s.Leaning, Title: strings.TrimSpace(a.Title), URL: a.URL, At: at})
	}
	return out
}

func better(a, b domain.Article) bool {
	at, bt := a.PublishedAt, b.PublishedAt
	if at.IsZero() {
		at = a.FirstSeenAt
	}
	if bt.IsZero() {
		bt = b.FirstSeenAt
	}
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.Title < b.Title
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
