// Package trends implements domain.TrendProvider: trending terms, multi-outlet
// stories and per-term coverage, computed on demand from article titles and
// summaries with no LLM and no external dependencies.
//
// Pipeline (see the individual files):
//
//	tokenize.go / extract.go  Spanish tokenisation, normalisation, candidates
//	engine.go                 corpus construction, caches, singleflight
//	trending.go               burst scoring (Dunning log-likelihood)
//	stories.go                multi-outlet clustering
//	term.go                   per-term coverage detail
package trends

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.TrendProvider = (*Engine)(nil)

// Options configures an Engine.
type Options struct {
	CacheTTL       time.Duration    // result/corpus cache lifetime (default 5 min)
	ExtraStopwords []string         // extra words ignored as terms (outlet names, boilerplate...)
	Now            func() time.Time // clock hook for tests (default time.Now)
}

// Defaults for domain.TrendOptions zero values.
const (
	DefaultWindow          = 24 * time.Hour
	DefaultBaseline        = 14 * 24 * time.Hour
	DefaultMinCount        = 3
	DefaultMinSources      = 2 // trending terms
	DefaultStoryMinSources = 3 // stories
	DefaultLimit           = 30
	DefaultTermDays        = 7
	sparkDays              = 14
	maxTermDays            = 120
)

// Engine implements domain.TrendProvider. Results are cached in memory and
// must be treated as read-only by callers.
type Engine struct {
	st   domain.TrendStore
	opt  Options
	stop map[string]struct{}

	mu      sync.Mutex
	results map[string]*cacheEntry
	corpora []*corpus
	flights map[string]*flight
}

type cacheEntry struct {
	val any
	at  time.Time
}

type flight struct {
	done chan struct{}
	val  any
	err  error
}

// New creates an engine on top of a TrendStore.
func New(st domain.TrendStore, opt Options) *Engine {
	if opt.CacheTTL <= 0 {
		opt.CacheTTL = 5 * time.Minute
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Engine{
		st: st, opt: opt, stop: buildStopSet(opt.ExtraStopwords),
		results: map[string]*cacheEntry{}, flights: map[string]*flight{},
	}
}

func (e *Engine) now() time.Time { return e.opt.Now() }

// normalize fills defaults. stories selects the MinSources default.
func normalize(o domain.TrendOptions, stories bool) domain.TrendOptions {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.Baseline <= 0 {
		o.Baseline = DefaultBaseline
	}
	if o.MinCount <= 0 {
		o.MinCount = DefaultMinCount
	}
	if o.MinSources <= 0 {
		o.MinSources = DefaultMinSources
		if stories {
			o.MinSources = DefaultStoryMinSources
		}
	}
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if len(o.SourceIDs) > 0 {
		ids := append([]string(nil), o.SourceIDs...)
		sort.Strings(ids)
		o.SourceIDs = ids
	}
	return o
}

func srcKey(ids []string) string { return strings.Join(ids, ",") }

func optKey(kind string, o domain.TrendOptions, extra string) string {
	return fmt.Sprintf("%s|%d|%d|%d|%d|%d|%d|%s|%s", kind, o.Window, o.Baseline, o.MinCount, o.MinSources, o.Limit,
		o.Now.UnixNano(), srcKey(o.SourceIDs), extra)
}

func isCtxErr(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

// flightDo runs fn once per key among concurrent callers (singleflight).
// If store is true the result is cached for CacheTTL.
func (e *Engine) flightDo(ctx context.Context, key string, store bool, fn func() (any, error)) (any, error) {
	for attempt := 0; ; attempt++ {
		e.mu.Lock()
		if store {
			if ce, ok := e.results[key]; ok {
				if e.now().Sub(ce.at) < e.opt.CacheTTL {
					e.mu.Unlock()
					return ce.val, nil
				}
				delete(e.results, key)
			}
		}
		if f, ok := e.flights[key]; ok {
			e.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if f.err != nil && ctx.Err() == nil && attempt < 2 && isCtxErr(f.err) {
				continue // the leader was cancelled, not us: retry
			}
			return f.val, f.err
		}
		f := &flight{done: make(chan struct{})}
		e.flights[key] = f
		e.mu.Unlock()

		f.val, f.err = fn()

		e.mu.Lock()
		delete(e.flights, key)
		if store && f.err == nil {
			e.results[key] = &cacheEntry{val: f.val, at: e.now()}
			e.gcResults()
		}
		e.mu.Unlock()
		close(f.done)
		return f.val, f.err
	}
}

// gcResults drops expired entries (caller holds e.mu).
func (e *Engine) gcResults() {
	if len(e.results) < 64 {
		return
	}
	now := e.now()
	for k, ce := range e.results {
		if now.Sub(ce.at) >= e.opt.CacheTTL {
			delete(e.results, k)
		}
	}
	if len(e.results) > 256 { // pathological: keep memory bounded
		e.results = map[string]*cacheEntry{}
	}
}

// ---- corpus ----------------------------------------------------------------

type srcInfo struct {
	ID, Name, Leaning string
}

// corpus is the tokenised article set of [from, to) (oldest first), shared by
// Trending, Stories and Term calls.
type corpus struct {
	key      string // srcKey
	live     bool   // built for a zero Now
	from, to time.Time
	builtAt  time.Time

	arts []domain.ArticleLite
	src  []int16
	at   []int64
	off  []int32 // article -> terms CSR
	flat []int32

	dict     *dict
	srcs     []srcInfo // index = src id; selected outlets first, unknown ones after
	nOutlets int       // number of srcs that are enabled and selected

	postOnce sync.Once
	pOff     []int32 // term -> articles CSR (articles ascending)
	pArt     []int32

	stop map[string]struct{}
}

func (c *corpus) terms(i int) []int32 { return c.flat[c.off[i]:c.off[i+1]] }

func (c *corpus) postings() {
	c.postOnce.Do(func() {
		n := len(c.dict.terms)
		off := make([]int32, n+1)
		for _, t := range c.flat {
			off[t+1]++
		}
		for i := 0; i < n; i++ {
			off[i+1] += off[i]
		}
		art := make([]int32, len(c.flat))
		pos := append([]int32(nil), off[:n]...)
		for a := 0; a < len(c.arts); a++ {
			for _, t := range c.terms(a) {
				art[pos[t]] = int32(a)
				pos[t]++
			}
		}
		c.pOff, c.pArt = off, art
	})
}

func (c *corpus) post(t int32) []int32 {
	c.postings()
	return c.pArt[c.pOff[t]:c.pOff[t+1]]
}

// firstAtOrAfter returns the first article index with at >= t (unix seconds).
func (c *corpus) firstAtOrAfter(t int64) int {
	return sort.Search(len(c.at), func(i int) bool { return c.at[i] >= t })
}

// lowerBound: first position in the sorted slice with value >= x.
func lowerBound(p []int32, x int32) int {
	return sort.Search(len(p), func(i int) bool { return p[i] >= x })
}

// getCorpus returns a corpus covering [needFrom(now), now). o.Now is the
// caller's explicit Now (zero = live). It also returns the effective now.
func (e *Engine) getCorpus(ctx context.Context, o domain.TrendOptions, needFrom func(now time.Time) time.Time) (*corpus, time.Time, error) {
	sk := srcKey(o.SourceIDs)
	live := o.Now.IsZero()
	find := func() *corpus {
		for _, c := range e.corpora {
			if c.key != sk || c.live != live {
				continue
			}
			if live {
				if e.now().Sub(c.builtAt) >= e.opt.CacheTTL {
					continue
				}
			} else if !c.to.Equal(o.Now) {
				continue
			}
			if !c.from.After(needFrom(c.to)) {
				return c
			}
		}
		return nil
	}
	e.mu.Lock()
	if c := find(); c != nil {
		e.mu.Unlock()
		return c, c.to, nil
	}
	e.mu.Unlock()

	now := o.Now
	if live {
		now = e.now()
	}
	from := needFrom(now)
	// Share one corpus between the default Trending/Stories/Term calls.
	if floor := now.Add(-(DefaultWindow + DefaultBaseline)); floor.Before(from) {
		from = floor
	}
	fkey := fmt.Sprintf("C|%s|%v|%d|%d", sk, live, now.UnixNano(), from.UnixNano())
	v, err := e.flightDo(ctx, fkey, false, func() (any, error) {
		return e.buildCorpus(ctx, sk, o.SourceIDs, live, from, now)
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	c := v.(*corpus)
	e.mu.Lock()
	keep := e.corpora[:0]
	for _, x := range e.corpora {
		if x == c {
			continue
		}
		if x.live && e.now().Sub(x.builtAt) >= e.opt.CacheTTL {
			continue
		}
		if x.key == c.key && x.live == c.live && x.to.Equal(c.to) && !x.from.Before(c.from) {
			continue // superseded by a wider corpus
		}
		keep = append(keep, x)
	}
	e.corpora = append(keep, c)
	if len(e.corpora) > 3 {
		e.corpora = e.corpora[len(e.corpora)-3:]
	}
	e.mu.Unlock()
	return c, c.to, nil
}

func (e *Engine) buildCorpus(ctx context.Context, sk string, ids []string, live bool, from, to time.Time) (*corpus, error) {
	sources, err := e.st.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	arts, err := e.st.ArticleTexts(ctx, from, to)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	c := &corpus{key: sk, live: live, from: from, to: to, builtAt: e.now(), stop: e.stop}
	sidx := map[string]int16{}
	known := map[string]bool{}
	for _, s := range sources {
		known[s.ID] = true
		if !s.IsEnabled() || (len(want) > 0 && !want[s.ID]) {
			continue
		}
		sidx[s.ID] = int16(len(c.srcs))
		c.srcs = append(c.srcs, srcInfo{ID: s.ID, Name: s.Name, Leaning: s.Leaning})
	}
	c.nOutlets = len(c.srcs)
	c.arts = make([]domain.ArticleLite, 0, len(arts))
	c.src = make([]int16, 0, len(arts))
	c.at = make([]int64, 0, len(arts))
	for _, a := range arts {
		si, ok := sidx[a.SourceID]
		if !ok {
			if known[a.SourceID] || (len(want) > 0 && !want[a.SourceID]) || len(c.srcs) >= 30000 {
				continue // disabled or filtered out
			}
			// unknown outlet: analysed, but never listed as "missing"
			si = int16(len(c.srcs))
			sidx[a.SourceID] = si
			c.srcs = append(c.srcs, srcInfo{ID: a.SourceID, Name: a.SourceID})
		}
		c.arts = append(c.arts, a)
		c.src = append(c.src, si)
		c.at = append(c.at, a.At.Unix())
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.index(ctx, c)
	return c, ctx.Err()
}

// ---- dictionary ------------------------------------------------------------

type termInfo struct {
	key    string
	kind   uint8
	capMid int32
	lowMid int32
	stamp  int32
}

type dict struct {
	ids   map[string]int32
	terms []termInfo
}

func (d *dict) intern(b []byte) int32 {
	if id, ok := d.ids[string(b)]; ok {
		return id
	}
	s := string(b)
	id := int32(len(d.terms))
	d.ids[s] = id
	d.terms = append(d.terms, termInfo{key: s})
	return id
}

type pending struct {
	art               int32
	full, rest, first int32
}

// shard is the work unit of the (parallel) indexing passes: a contiguous
// range of articles with its own dictionary, merged afterwards.
type shard struct {
	lo, hi int
	d      *dict
	flat   []int32
	off    []int32 // len hi-lo+1
	pend   []pending
	ng     []uint64 // pass 1: hashes of title n-grams, per article
	ngOff  []int32
}

func (d *dict) internString(s string) int32 {
	if id, ok := d.ids[s]; ok {
		return id
	}
	id := int32(len(d.terms))
	d.ids[s] = id
	d.terms = append(d.terms, termInfo{key: s})
	return id
}

// index tokenises all articles into the dictionary and the article->terms
// CSR. Most title n-grams occur exactly once and can never trend or link
// articles, so they are not interned: pass 1 (parallel) interns unigrams and
// entities and only records n-gram hashes; after counting hashes, pass 2
// (parallel, title only, only articles that own a repeated n-gram) interns the
// n-grams seen more than once. This keeps the dictionary ~10x smaller.
func (e *Engine) index(ctx context.Context, c *corpus) {
	n := len(c.arts)
	d := &dict{ids: make(map[string]int32, 1<<12), terms: make([]termInfo, 0, 1<<12)}
	c.dict = d
	c.off = make([]int32, n+1)
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if n < 2000 || workers < 1 {
		workers = 1
	}
	shards := make([]*shard, workers)
	for w := range shards {
		shards[w] = &shard{lo: n * w / workers, hi: n * (w + 1) / workers}
	}
	run := func(fn func(s *shard)) {
		var wg sync.WaitGroup
		for _, s := range shards {
			wg.Add(1)
			go func(s *shard) { defer wg.Done(); fn(s) }(s)
		}
		wg.Wait()
	}

	// ---- pass 1
	run(func(s *shard) {
		x := newExtractor(e.stop)
		ld := &dict{ids: make(map[string]int32, 1<<12), terms: make([]termInfo, 0, 1<<12)}
		s.d = ld
		m := s.hi - s.lo
		s.off = make([]int32, m+1)
		s.ngOff = make([]int32, m+1)
		s.flat = make([]int32, 0, m*14)
		s.ng = make([]uint64, 0, m*10)
		var cur int32
		var art int32
		emit := func(cd *cand) {
			if cd.pend {
				p := pending{art: art, full: ld.intern(cd.key), rest: -1}
				if len(cd.restKey) > 0 {
					p.rest = ld.intern(cd.restKey)
				}
				p.first = ld.intern(cd.firstKey)
				s.pend = append(s.pend, p)
				return
			}
			if cd.kind == kindPhrase {
				s.ng = append(s.ng, cd.hash)
				return
			}
			id := ld.intern(cd.key)
			t := &ld.terms[id]
			if cd.kind > t.kind {
				t.kind = cd.kind
			}
			switch cd.stat {
			case statCapMid:
				t.capMid++
			case statLowMid:
				t.lowMid++
			}
			if t.stamp != cur {
				t.stamp = cur
				s.flat = append(s.flat, id)
			}
		}
		for i := s.lo; i < s.hi; i++ {
			if i&1023 == 0 && ctx.Err() != nil {
				return
			}
			art, cur = int32(i), int32(i+1)
			s.off[i-s.lo] = int32(len(s.flat))
			s.ngOff[i-s.lo] = int32(len(s.ng))
			a := &c.arts[i]
			x.extract(a.Title, a.Summary, emit)
		}
		s.off[m] = int32(len(s.flat))
		s.ngOff[m] = int32(len(s.ng))
	})
	if ctx.Err() != nil {
		return
	}
	// count n-gram hashes (saturating at 2) in a hashed counter table
	tbits := uint(16)
	for (1<<tbits) < n*200 && tbits < 23 {
		tbits++
	}
	table := make([]uint8, 1<<tbits)
	idx := func(h uint64) uint64 { return (h ^ h>>29) & (1<<tbits - 1) }
	for _, s := range shards {
		for _, h := range s.ng {
			if k := idx(h); table[k] < 2 {
				table[k]++
			}
		}
	}

	// merge pass 1
	var pend []pending
	c.flat = make([]int32, 0, n*14)
	for _, s := range shards {
		remap := make([]int32, len(s.d.terms))
		for lid, t := range s.d.terms {
			gid := d.internString(t.key)
			remap[lid] = gid
			g := &d.terms[gid]
			if t.kind > g.kind {
				g.kind = t.kind
			}
			g.capMid += t.capMid
			g.lowMid += t.lowMid
		}
		for i := 0; i < s.hi-s.lo; i++ {
			c.off[s.lo+i] = int32(len(c.flat))
			for _, lid := range s.flat[s.off[i]:s.off[i+1]] {
				c.flat = append(c.flat, remap[lid])
			}
		}
		for _, p := range s.pend {
			q := pending{art: p.art, full: remap[p.full], rest: -1, first: remap[p.first]}
			if p.rest >= 0 {
				q.rest = remap[p.rest]
			}
			pend = append(pend, q)
		}
	}
	c.off[n] = int32(len(c.flat))

	// ---- pass 2: repeated n-grams (title only)
	run(func(s *shard) {
		x := newExtractor(e.stop)
		x.ngramsOnly = true
		ld := &dict{ids: make(map[string]int32, 1<<10), terms: make([]termInfo, 0, 1<<10)}
		s.d = ld
		m := s.hi - s.lo
		s.flat = s.flat[:0]
		s.off = make([]int32, m+1)
		var cur int32
		emit := func(cd *cand) {
			if cd.kind != kindPhrase || table[idx(cd.hash)] < 2 {
				return
			}
			id := ld.intern(cd.key)
			if t := &ld.terms[id]; t.stamp != cur {
				t.stamp = cur
				s.flat = append(s.flat, id)
			}
		}
		for i := s.lo; i < s.hi; i++ {
			if i&1023 == 0 && ctx.Err() != nil {
				return
			}
			cur = int32(i + 1)
			s.off[i-s.lo] = int32(len(s.flat))
			rep := false
			for _, h := range s.ng[s.ngOff[i-s.lo]:s.ngOff[i-s.lo+1]] {
				if table[idx(h)] >= 2 {
					rep = true
					break
				}
			}
			if rep {
				x.extract(c.arts[i].Title, "", emit)
			}
		}
		s.off[m] = int32(len(s.flat))
	})
	if ctx.Err() != nil {
		return
	}
	extra := make([][]int32, n)
	nExtra := 0
	for _, s := range shards {
		remap := make([]int32, len(s.d.terms))
		for lid, t := range s.d.terms {
			gid := d.internString(t.key)
			remap[lid] = gid
			if d.terms[gid].kind < kindPhrase {
				d.terms[gid].kind = kindPhrase
			}
		}
		for i := 0; i < s.hi-s.lo; i++ {
			for _, lid := range s.flat[s.off[i]:s.off[i+1]] {
				extra[s.lo+i] = append(extra[s.lo+i], remap[lid])
				nExtra++
			}
		}
	}

	// Resolve sentence-initial capitalised runs now that the capitalisation
	// statistics of the whole corpus are known.
	for _, p := range pend {
		f := d.terms[p.first]
		use := p.rest
		if f.capMid >= 1 && f.capMid >= f.lowMid {
			use = p.full
		}
		if use < 0 {
			continue
		}
		d.terms[use].kind = kindEntity
		extra[p.art] = append(extra[p.art], use)
		nExtra++
	}
	if nExtra == 0 {
		return
	}
	flat := make([]int32, 0, len(c.flat)+nExtra)
	off := make([]int32, n+1)
	for i := 0; i < n; i++ {
		off[i] = int32(len(flat))
		flat = append(flat, c.flat[c.off[i]:c.off[i+1]]...)
	outer:
		for _, id := range extra[i] {
			for _, o := range flat[off[i]:] {
				if o == id {
					continue outer
				}
			}
			flat = append(flat, id)
		}
	}
	off[n] = int32(len(flat))
	c.flat, c.off = flat, off
}

// kindName returns the public kind of a term.
func (c *corpus) kindName(id int32) string {
	t := c.dict.terms[id]
	switch t.kind {
	case kindEntity:
		return "entity"
	case kindPhrase:
		return "phrase"
	}
	if t.capMid >= 1 && t.capMid >= 2*t.lowMid {
		return "entity"
	}
	return "word"
}

// display resolves the most frequent original form (casing and accents) of
// each wanted term by rescanning (a sample of) the articles that contain it.
func (c *corpus) display(ids []int32) map[int32]string {
	out := make(map[int32]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	want := make(map[int32]bool, len(ids))
	votes := make(map[int32]map[string]int, len(ids))
	scan := map[int32]bool{}
	for _, id := range ids {
		want[id] = true
		p := c.post(id)
		if len(p) > 60 { // sample: the most recent 60 articles
			p = p[len(p)-60:]
		}
		for _, a := range p {
			scan[a] = true
		}
	}
	arts := make([]int32, 0, len(scan))
	for a := range scan {
		arts = append(arts, a)
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i] < arts[j] })
	x := newExtractor(c.stop)
	vote := func(key []byte, disp string, w int) {
		id, ok := c.dict.ids[string(key)]
		if !ok || !want[id] {
			return
		}
		m := votes[id]
		if m == nil {
			m = map[string]int{}
			votes[id] = m
		}
		m[disp] += w
	}
	emit := func(cd *cand) {
		w := 4
		if cd.sent {
			w = 2
		}
		if cd.caps {
			w = 1
		}
		vote(cd.key, cd.disp, w)
		if cd.pend && len(cd.restKey) > 0 {
			vote(cd.restKey, cd.restDisp, w)
		}
	}
	for _, a := range arts {
		x.extract(c.arts[a].Title, c.arts[a].Summary, emit)
	}
	for _, id := range ids {
		best, bw := "", -1
		for d, w := range votes[id] {
			if w > bw || (w == bw && betterDisplay(d, best)) {
				best, bw = d, w
			}
		}
		if best == "" {
			best = c.dict.terms[id].key
		}
		out[id] = best
	}
	return out
}

func betterDisplay(a, b string) bool {
	ua, ub := upperCount(a), upperCount(b)
	if ua != ub {
		return ua > ub
	}
	return a < b
}

func upperCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsUpper(r) {
			n++
		}
	}
	return n
}

// ---- shared helpers --------------------------------------------------------

func leaningOf(s srcInfo) string {
	if s.Leaning == "" {
		return "desconocida"
	}
	return s.Leaning
}

var leaningRank = map[string]int{"izquierda": 0, "centro-izquierda": 1, "centro": 2, "centro-derecha": 3, "derecha": 4}

func rankOf(l string) int {
	if r, ok := leaningRank[l]; ok {
		return r
	}
	return 5
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// tokenSubterm reports whether the space-separated token sequence a occurs
// contiguously inside b (a != b).
func tokenSubterm(a, b string) bool {
	if len(a) >= len(b) {
		return false
	}
	for from := 0; ; {
		i := strings.Index(b[from:], a)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(a)
		if (i == 0 || b[i-1] == ' ') && (end == len(b) || b[end] == ' ') {
			return true
		}
		from = i + 1
	}
}

// missingFor lists the selected outlets not in covered, sorted by leaning.
func (c *corpus) missingFor(covered map[int16]bool) []domain.SourceCoverage {
	var out []domain.SourceCoverage
	for i := 0; i < c.nOutlets; i++ {
		if covered[int16(i)] {
			continue
		}
		s := c.srcs[i]
		out = append(out, domain.SourceCoverage{SourceID: s.ID, Name: s.Name, Leaning: s.Leaning})
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rankOf(out[i].Leaning), rankOf(out[j].Leaning)
		if ri != rj {
			return ri < rj
		}
		if out[i].Leaning != out[j].Leaning {
			return out[i].Leaning < out[j].Leaning
		}
		return out[i].SourceID < out[j].SourceID
	})
	return out
}

// coverage builds per-outlet coverage (earliest first) from article indexes.
func (c *corpus) coverage(arts []int32) []domain.SourceCoverage {
	by := map[int16]*domain.SourceCoverage{}
	for _, a := range arts {
		si := c.src[a]
		sc := by[si]
		if sc == nil {
			s := c.srcs[si]
			sc = &domain.SourceCoverage{SourceID: s.ID, Name: s.Name, Leaning: s.Leaning}
			by[si] = sc
		}
		sc.Count++
		at := c.arts[a].At
		if sc.FirstAt.IsZero() || at.Before(sc.FirstAt) {
			sc.FirstAt = at
		}
		sc.Headlines = append(sc.Headlines, refOf(c.arts[a]))
	}
	out := make([]domain.SourceCoverage, 0, len(by))
	for _, sc := range by {
		sort.SliceStable(sc.Headlines, func(i, j int) bool { // newest first
			if !sc.Headlines[i].At.Equal(sc.Headlines[j].At) {
				return sc.Headlines[i].At.After(sc.Headlines[j].At)
			}
			return sc.Headlines[i].ID > sc.Headlines[j].ID
		})
		if len(sc.Headlines) > maxHeadlines {
			sc.Headlines = sc.Headlines[:maxHeadlines]
		}
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstAt.Equal(out[j].FirstAt) {
			return out[i].FirstAt.Before(out[j].FirstAt)
		}
		return out[i].SourceID < out[j].SourceID
	})
	return out
}

const maxHeadlines = 3

func refOf(a domain.ArticleLite) domain.ArticleRef {
	return domain.ArticleRef{ID: a.ID, SourceID: a.SourceID, Title: a.Title, URL: a.URL, At: a.At}
}
