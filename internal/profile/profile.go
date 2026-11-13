// Package profile computes the "ficha de medio": per-outlet metrics derived
// only from stored articles, the story clusters of domain.TrendProvider and
// (optionally) revision / annotation stores. Everything is aggregated from
// bounded windows and cached in memory, so repeated page views are cheap.
package profile

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ErrNotFound is returned for an unknown or disabled outlet.
var ErrNotFound = errors.New("outlet not found")

const (
	// DailyDays is the length of the volume chart.
	DailyDays = 30
	// SparkDays is the length of the overview sparkline.
	SparkDays = 14
	// TermDays is the window of the distinctive-terms analysis.
	TermDays = 14
	// HeadlineN is the number of latest headlines kept per outlet.
	HeadlineN = 20
	// MinStorySources is the minimum number of outlets for a story to count.
	MinStorySources = 3

	defaultTTL = 10 * time.Minute
	chunkDays  = 5
	maxStories = 1000
)

// Options configure an Engine.
type Options struct {
	Location *time.Location // zone for days/hours (default UTC)
	Now      func() time.Time
	TTL      time.Duration // cache lifetime (default 10 min)
}

// Engine builds profiles. Safe for concurrent use.
type Engine struct {
	st  domain.Store
	tp  domain.TrendProvider // may be nil: lag/coverage metrics unavailable
	loc *time.Location
	now func() time.Time
	ttl time.Duration

	mu      sync.Mutex // serialises (re)computation
	snap    *snapshot
	stories *storySnap
	profs   map[string]*profEntry
}

// New returns an Engine. tp may be nil.
func New(st domain.Store, tp domain.TrendProvider, o Options) *Engine {
	e := &Engine{st: st, tp: tp, loc: o.Location, now: o.Now, ttl: o.TTL, profs: map[string]*profEntry{}}
	if e.loc == nil {
		e.loc = time.UTC
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.ttl <= 0 {
		e.ttl = defaultTTL
	}
	return e
}

// ---- result types ----

// Count is a labelled counter.
type Count struct {
	Label string
	N     int
}

// DayN is the article count of one local day.
type DayN struct {
	Day time.Time
	N   int
}

// FeedHealth is the health of one feed of the outlet.
type FeedHealth struct {
	URL, Section string
	Status       string // ok | warn | bad | unknown
	LastOK       time.Time
	LastError    string
	LastCount    int
}

// Summary is one outlet in the overview grid.
type Summary struct {
	Source  domain.Source
	Total30 int
	Total14 int
	PerDay  float64 // average over the last 14 days
	Last    time.Time
	Spark   []int // SparkDays values, oldest to today
	Health  string
}

// Overview is the data of /medios.
type Overview struct {
	GeneratedAt time.Time
	Outlets     []Summary
	Articles30  int
}

// TermScore is a distinctive term of an outlet.
type TermScore struct {
	Term  string
	Count int // outlet articles containing it
	Score float64
}

// SideStat compares the outlet with the outlets of one leaning.
type SideStat struct {
	Label   string
	Covered int // stories (>=3 outlets) covered by at least one outlet of this side
	Omitted int // of those, stories this outlet did not cover
}

// OmittedStory is a story the outlet did not cover.
type OmittedStory struct {
	ID, Title string
	Outlets   int
	FirstAt   time.Time
}

// LagStats are the story-based metrics for a period.
type LagStats struct {
	Available    bool
	Days         int
	Stories      int // stories with >= MinStorySources outlets in the period
	Covered      int
	Omitted      int
	FirstN       int
	LagSamples   int
	MedianLagMin float64 // median minutes behind the first outlet, over covered stories (0 when first)
	P75LagMin    float64
	Sides        []SideStat
	OmittedTop   []OmittedStory
}

// CoveragePct is covered/stories in percent.
func (l LagStats) CoveragePct() float64 { return pct(l.Covered, l.Stories) }

// SharePct is the share of covered stories where the outlet was first.
func (l LagStats) SharePct() float64 { return pct(l.FirstN, l.Covered) }

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

// RevisionStat is the headline edit rate (optional store).
type RevisionStat struct {
	Edits    int64
	Articles int
	Days     int
}

// Rate is edits per 100 articles.
func (r RevisionStat) Rate() float64 {
	if r.Articles == 0 {
		return 0
	}
	return float64(r.Edits) * 100 / float64(r.Articles)
}

// ToneStat is the model-estimated tone distribution (optional store).
type ToneStat struct {
	Model  string
	Total  int
	Labels []Count
}

// Profile is the complete outlet sheet.
type Profile struct {
	Source      domain.Source
	GeneratedAt time.Time
	Feeds       []FeedHealth
	Health      string
	Daily       []DayN // DailyDays, oldest to today
	Total30     int
	Avg30       float64
	Last        time.Time
	Heat        [7][24]int // [weekday Mon..Sun][hour] over 30 days
	Sections    []Count
	SectionsN   int // articles sampled for sections
	SectionDays int
	Terms       []TermScore
	TermsN      int // outlet articles analysed
	Lag         LagStats
	Headlines   []domain.ArticleLite
	Shot        *domain.Screenshot
	Rev         *RevisionStat
	Tone        *ToneStat
}

// ---- snapshot of the last 30 days ----

type agg struct {
	total    int
	daily    [DailyDays]int
	heat     [7][24]int
	last     time.Time
	heads    []domain.ArticleLite // last HeadlineN, oldest first
	termN    int                  // articles in the term window
	termCnt  map[string]int
	recent14 int
}

type snapshot struct {
	at       time.Time
	by       map[string]*agg
	termTot  map[string]int // global article counts per term
	termN    int
	sources  []domain.Source
	feeds    []domain.FeedState
	total    int
	ignoreTk map[string]bool
}

type profEntry struct {
	at time.Time
	p  *Profile
}

type storySnap struct {
	at  time.Time
	st  []domain.Story
	err error
}

func (e *Engine) dayStart(t time.Time) time.Time {
	t = t.In(e.loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, e.loc)
}

func (e *Engine) loadSnapshot(ctx context.Context) (*snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked(ctx)
}

func (e *Engine) snapshotLocked(ctx context.Context) (*snapshot, error) {
	now := e.now()
	if e.snap != nil && now.Sub(e.snap.at) < e.ttl {
		return e.snap, nil
	}
	srcs, err := e.st.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	sn := &snapshot{at: now, by: map[string]*agg{}, termTot: map[string]int{}, ignoreTk: map[string]bool{}}
	for _, s := range srcs {
		if !s.IsEnabled() {
			continue
		}
		sn.sources = append(sn.sources, s)
		sn.by[s.ID] = &agg{termCnt: map[string]int{}}
		for _, t := range tokens(s.Name + " " + s.ID) {
			sn.ignoreTk[t] = true
		}
	}
	if fs, err := e.st.ListFeedStates(ctx); err == nil {
		sn.feeds = fs
	}
	today := e.dayStart(now)
	first := today.AddDate(0, 0, -(DailyDays - 1))
	termFrom := today.AddDate(0, 0, -(TermDays - 1))
	for from := first; from.Before(now); from = from.AddDate(0, 0, chunkDays) {
		to := from.AddDate(0, 0, chunkDays)
		if to.After(now) {
			to = now.Add(time.Second)
		}
		arts, err := e.articlesBetween(ctx, from, to)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			g := sn.by[a.SourceID]
			if g == nil || a.At.After(now) {
				continue
			}
			sn.total++
			g.total++
			lt := a.At.In(e.loc)
			di := DailyDays - 1 - int(today.Sub(e.dayStart(a.At)).Round(24*time.Hour)/(24*time.Hour))
			if di >= 0 && di < DailyDays {
				g.daily[di]++
			}
			g.heat[(int(lt.Weekday())+6)%7][lt.Hour()]++
			if a.At.After(g.last) {
				g.last = a.At
			}
			if !a.At.Before(termFrom) {
				g.recent14++
				g.termN++
				sn.termN++
				for t := range uniq(tokens(a.Title)) {
					g.termCnt[t]++
					sn.termTot[t]++
				}
			}
			a.Summary = ""
			g.heads = append(g.heads, a)
			if len(g.heads) > 2*HeadlineN {
				g.heads = append(g.heads[:0], g.heads[len(g.heads)-HeadlineN:]...)
			}
		}
	}
	e.snap = sn
	return sn, nil
}

// articlesBetween uses ArticleTexts when the store provides it (cheap
// projection) and falls back to paged ListArticles.
func (e *Engine) articlesBetween(ctx context.Context, from, to time.Time) ([]domain.ArticleLite, error) {
	if ts, ok := e.st.(domain.TrendStore); ok {
		return ts.ArticleTexts(ctx, from, to)
	}
	var out []domain.ArticleLite
	const page = 2000
	for off := 0; ; off += page {
		as, err := e.st.ListArticles(ctx, domain.Filter{From: from, To: to.Add(-time.Second), Limit: page, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			at := a.PublishedAt
			if at.IsZero() {
				at = a.FirstSeenAt
			}
			if at.Before(from) || !at.Before(to) {
				continue
			}
			out = append(out, domain.ArticleLite{ID: a.ID, SourceID: a.SourceID, Title: a.Title, URL: a.URL, At: at})
		}
		if len(as) < page || off > 100000 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func uniq(ts []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		m[t] = struct{}{}
	}
	return m
}

var stop = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`para pero como esta este esto estos estas sobre entre desde hasta tras ante bajo cada todo toda todos todas
	otra otro otros otras mas menos muy sus sin son fue ser han has hay que con del los las una uno unos unas por nos
	tiene tienen puede pueden hace hacer segun donde cuando quien quienes cual cuales tambien aunque ademas ahora hoy ayer
	despues antes durante contra nuevo nueva nuevos nuevas primer primera años año dia dias video fotos foto directo
	será sera sido está están estan tras mientras sigue vuelve dice dicen`) {
		m[w] = true
	}
	return m
}()

// tokens returns lower-cased words of at least 4 letters, without stopwords.
func tokens(s string) []string {
	var out []string
	var b []rune
	flush := func() {
		if len(b) >= 4 {
			w := string(b)
			if !stop[w] {
				out = append(out, w)
			}
		}
		b = b[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b = append(b, unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return out
}

// ---- health ----

func feedStatus(fs domain.FeedState, found bool) string {
	switch {
	case !found:
		return "unknown"
	case fs.LastError != "" && !fs.LastErrorAt.Before(fs.LastOK):
		return "bad"
	case fs.LastOK.IsZero():
		return "unknown"
	}
	return "ok"
}

func (sn *snapshot) feedsOf(s domain.Source) ([]FeedHealth, string) {
	idx := map[string]domain.FeedState{}
	for _, f := range sn.feeds {
		if f.SourceID == s.ID {
			idx[f.FeedURL] = f
		}
	}
	var out []FeedHealth
	bad, ok, unk := 0, 0, 0
	for _, f := range s.Feeds {
		st, found := idx[f.URL]
		h := FeedHealth{URL: f.URL, Section: f.Section, Status: feedStatus(st, found), LastOK: st.LastOK, LastError: st.LastError, LastCount: st.LastCount}
		switch h.Status {
		case "bad":
			bad++
		case "ok":
			ok++
		default:
			unk++
		}
		out = append(out, h)
	}
	switch {
	case len(out) == 0 || (ok == 0 && bad == 0):
		return out, "unknown"
	case bad == 0:
		return out, "ok"
	case ok == 0:
		return out, "bad"
	}
	return out, "warn"
}

// ---- overview ----

// Overview returns the summaries of all enabled outlets.
func (e *Engine) Overview(ctx context.Context) (*Overview, error) {
	sn, err := e.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	ov := &Overview{GeneratedAt: sn.at, Articles30: sn.total}
	for _, s := range sn.sources {
		g := sn.by[s.ID]
		sm := Summary{Source: s, Total30: g.total, Total14: g.recent14, PerDay: float64(g.recent14) / TermDays, Last: g.last,
			Spark: append([]int(nil), g.daily[DailyDays-SparkDays:]...)}
		_, sm.Health = sn.feedsOf(s)
		ov.Outlets = append(ov.Outlets, sm)
	}
	return ov, nil
}

// ---- stories ----

func (e *Engine) loadStories(ctx context.Context) ([]domain.Story, error) {
	if e.tp == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if e.stories != nil && now.Sub(e.stories.at) < e.ttl {
		return e.stories.st, e.stories.err
	}
	st, err := e.tp.Stories(ctx, domain.TrendOptions{Window: 14 * 24 * time.Hour, MinSources: MinStorySources, MinCount: MinStorySources, Limit: maxStories, Now: now})
	if err != nil {
		return nil, err // do not cache failures
	}
	e.stories = &storySnap{at: now, st: st}
	return st, nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[int(float64(len(s)-1)*q+0.5)]
}

// SideLabels are the canonical leaning buckets (see LeanBucket).
var SideLabels = []string{"Izquierda", "Centro-izquierda", "Centro", "Centro-derecha", "Derecha", "Sin clasificar"}

// LeanBucket maps a free leaning label to an index of SideLabels.
func LeanBucket(l string) int {
	l = strings.ToLower(l)
	switch {
	case strings.Contains(l, "centro") && strings.Contains(l, "izq"):
		return 1
	case strings.Contains(l, "centro") && strings.Contains(l, "der"):
		return 3
	case strings.Contains(l, "izq") || strings.Contains(l, "progres"):
		return 0
	case strings.Contains(l, "der") || strings.Contains(l, "conserv"):
		return 4
	case strings.Contains(l, "centro") || strings.Contains(l, "liberal"):
		return 2
	}
	return 5
}

// ComputeLag derives the story metrics of outlet id from stories, keeping
// only those that started within the last days days. Pure function.
func ComputeLag(stories []domain.Story, id string, days int, now time.Time) LagStats {
	ls := LagStats{Available: true, Days: days, Sides: make([]SideStat, len(SideLabels))}
	for i := range ls.Sides {
		ls.Sides[i].Label = SideLabels[i]
	}
	from := now.Add(-time.Duration(days) * 24 * time.Hour)
	var lags []float64
	for _, s := range stories {
		if len(s.Coverage) < MinStorySources || s.FirstAt.Before(from) {
			continue
		}
		ls.Stories++
		t0, mine, has := time.Time{}, time.Time{}, false
		var sides [6]bool
		for _, c := range s.Coverage {
			if !c.FirstAt.IsZero() && (t0.IsZero() || c.FirstAt.Before(t0)) {
				t0 = c.FirstAt
			}
			if c.SourceID == id {
				has, mine = true, c.FirstAt
			} else {
				sides[LeanBucket(c.Leaning)] = true
			}
		}
		for i, ok := range sides {
			if ok {
				ls.Sides[i].Covered++
				if !has {
					ls.Sides[i].Omitted++
				}
			}
		}
		if !has {
			ls.Omitted++
			ls.OmittedTop = append(ls.OmittedTop, OmittedStory{ID: s.ID, Title: s.Title, Outlets: len(s.Coverage), FirstAt: s.FirstAt})
			continue
		}
		ls.Covered++
		if s.FirstBy == id || (!mine.IsZero() && mine.Equal(t0)) {
			ls.FirstN++
		}
		if !mine.IsZero() && !t0.IsZero() {
			lags = append(lags, mine.Sub(t0).Minutes())
		}
	}
	ls.LagSamples = len(lags)
	ls.MedianLagMin = median(lags)
	ls.P75LagMin = quantile(lags, 0.75)
	sort.SliceStable(ls.OmittedTop, func(i, j int) bool { return ls.OmittedTop[i].Outlets > ls.OmittedTop[j].Outlets })
	if len(ls.OmittedTop) > 5 {
		ls.OmittedTop = ls.OmittedTop[:5]
	}
	return ls
}

// ---- profile ----

// Profile builds (or returns from cache) the sheet of one outlet. lagDays is
// 7 or 14 (anything else becomes 14).
func (e *Engine) Profile(ctx context.Context, id string, lagDays int) (*Profile, error) {
	if lagDays != 7 {
		lagDays = 14
	}
	key := id + "|" + string(rune('0'+lagDays/7))
	now := e.now()
	e.mu.Lock()
	if c := e.profs[key]; c != nil && now.Sub(c.at) < e.ttl {
		e.mu.Unlock()
		return c.p, nil
	}
	e.mu.Unlock()

	sn, err := e.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	var src *domain.Source
	for i := range sn.sources {
		if sn.sources[i].ID == id {
			src = &sn.sources[i]
		}
	}
	if src == nil {
		return nil, ErrNotFound
	}
	g := sn.by[id]
	p := &Profile{Source: *src, GeneratedAt: now, Total30: g.total, Avg30: float64(g.total) / DailyDays, Last: g.last, Heat: g.heat}
	p.Feeds, p.Health = sn.feedsOf(*src)
	today := e.dayStart(now)
	for i, n := range g.daily {
		p.Daily = append(p.Daily, DayN{Day: today.AddDate(0, 0, i-(DailyDays-1)), N: n})
	}
	hs := g.heads
	if len(hs) > HeadlineN {
		hs = hs[len(hs)-HeadlineN:]
	}
	for i := len(hs) - 1; i >= 0; i-- {
		p.Headlines = append(p.Headlines, hs[i])
	}
	p.Terms, p.TermsN = distinctive(sn, g)

	// sections from full articles over a bounded window
	p.SectionDays = TermDays
	if as, err := e.st.ListArticles(ctx, domain.Filter{SourceIDs: []string{id}, From: today.AddDate(0, 0, -(TermDays - 1)), Limit: 1500}); err == nil {
		m := map[string]int{}
		for _, a := range as {
			sec := strings.TrimSpace(a.Section)
			if sec == "" {
				sec = "(sin sección)"
			}
			m[sec]++
		}
		p.SectionsN = len(as)
		for k, n := range m {
			p.Sections = append(p.Sections, Count{k, n})
		}
		sort.Slice(p.Sections, func(i, j int) bool {
			if p.Sections[i].N != p.Sections[j].N {
				return p.Sections[i].N > p.Sections[j].N
			}
			return p.Sections[i].Label < p.Sections[j].Label
		})
		if len(p.Sections) > 12 {
			p.Sections = p.Sections[:12]
		}
	}

	if e.tp != nil {
		if st, err := e.loadStories(ctx); err == nil {
			p.Lag = ComputeLag(st, id, lagDays, now)
		}
	}
	if shots, err := e.st.ListScreenshots(ctx, id, now.Add(-72*time.Hour), now.Add(time.Minute)); err == nil && len(shots) > 0 {
		last := shots[0]
		for _, s := range shots {
			if s.TakenAt.After(last.TakenAt) {
				last = s
			}
		}
		p.Shot = &last
	}
	e.optional(ctx, p, g.recent14)

	e.mu.Lock()
	e.profs[key] = &profEntry{at: now, p: p}
	if len(e.profs) > 400 {
		e.profs = map[string]*profEntry{key: e.profs[key]}
	}
	e.mu.Unlock()
	return p, nil
}

// distinctive ranks terms over-represented in the outlet's titles compared
// with all the other outlets (smoothed log-ratio of document frequencies).
func distinctive(sn *snapshot, g *agg) ([]TermScore, int) {
	nS, nO := g.termN, sn.termN-g.termN
	if nS == 0 {
		return nil, 0
	}
	minC := 3
	if nS < 30 {
		minC = 2
	}
	var out []TermScore
	for t, c := range g.termCnt {
		if c < minC || sn.ignoreTk[t] {
			continue
		}
		o := sn.termTot[t] - c
		ps := (float64(c) + 0.5) / (float64(nS) + 1)
		po := (float64(o) + 0.5) / (float64(nO) + 1)
		if ps <= po {
			continue
		}
		out = append(out, TermScore{Term: t, Count: c, Score: logRatio(ps, po) * sqrtF(float64(c))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Term < out[j].Term
	})
	if len(out) > 15 {
		out = out[:15]
	}
	return out, nS
}

// optional fills metrics from optional stores; failures are silent.
func (e *Engine) optional(ctx context.Context, p *Profile, arts14 int) {
	to := e.now()
	from := to.Add(-TermDays * 24 * time.Hour)
	if rs, ok := e.st.(domain.RevisionStore); ok {
		f := domain.RevisionFilter{From: from, To: to, Kind: domain.RevisionTitle, SourceIDs: []string{p.Source.ID}}
		if n, err := rs.CountRevisions(ctx, f); err == nil {
			p.Rev = &RevisionStat{Edits: n, Articles: arts14, Days: TermDays}
		}
	}
	if ar, ok := e.st.(domain.AnnotationReader); ok {
		ms, err := ar.AnnotationModels(ctx, domain.AnnoTone)
		if err != nil || len(ms) == 0 {
			return
		}
		ds, err := ar.AnnotationDistribution(ctx, domain.AnnotationQuery{Kind: domain.AnnoTone, Model: ms[0].Model, SourceIDs: []string{p.Source.ID}, From: from, To: to})
		if err != nil {
			return
		}
		t := &ToneStat{Model: ms[0].Model}
		for _, d := range ds {
			if d.SourceID == p.Source.ID && d.N > 0 {
				t.Labels = append(t.Labels, Count{d.Label, int(d.N)})
				t.Total += int(d.N)
			}
		}
		sort.Slice(t.Labels, func(i, j int) bool { return t.Labels[i].N > t.Labels[j].N })
		if t.Total > 0 {
			p.Tone = t
		}
	}
}

func logRatio(a, b float64) float64 { return math.Log(a / b) }
func sqrtF(x float64) float64       { return math.Sqrt(x) }
