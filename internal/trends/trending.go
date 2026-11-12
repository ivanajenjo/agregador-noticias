package trends

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Burst scoring
//
// For a term with a articles in the recent window (n1 articles in total) and
// b articles in the baseline (n2 articles in total) we compute Dunning's
// log-likelihood ratio (G²) of the 2x2 table {term, not term} x {window,
// baseline}:
//
//	E1 = n1(a+b)/(n1+n2)      E2 = n2(a+b)/(n1+n2)
//	G² = 2 [ a ln(a/E1) + b ln(b/E2) ]        (0 ln 0 = 0)
//
// Because expectations are proportional to the number of ARTICLES in each
// period (not to its duration), differences in ingestion volume (night vs.
// day, a feed that was down) do not create fake trends. Only terms that are
// more frequent in the window than in the baseline (a/n1 > b/n2) and whose
// smoothed lift ((a+.5)/n1) / ((b+.5)/n2) is >= minLift are kept.
//
// The final score weights the statistic by how many outlets actually carry
// the term, using the "effective number of outlets" exp(H) where H is the
// Shannon entropy of the per-outlet article distribution (so 20 articles from
// one outlet and 1 from another count as ~1.2 outlets, not 2):
//
//	Score = G² * ln(1 + effOutlets)
//
// A term is New when it appears in at most 1 baseline article.
const minLift = 1.5

// subsumeRatio is the co-occurrence above which a sub-term is collapsed into
// the larger phrase/entity that contains it.
const subsumeRatio = 0.8

type tcand struct {
	id      int32
	win     int32
	base    int32
	sources int
	eff     float64
	g2      float64
	score   float64
}

func dunning(a, b, n1, n2 float64) float64 {
	if a <= 0 || n1 <= 0 {
		return 0
	}
	if n2 <= 0 { // empty baseline: everything is new; monotone in a
		return 2 * a * math.Log(1+a)
	}
	if a/n1 <= b/n2 {
		return 0
	}
	tot, n := a+b, n1+n2
	e1, e2 := n1*tot/n, n2*tot/n
	g := a * math.Log(a/e1)
	if b > 0 {
		g += b * math.Log(b/e2)
	}
	return 2 * g
}

// Trending implements domain.TrendProvider.
func (e *Engine) Trending(ctx context.Context, o domain.TrendOptions) (*domain.TrendReport, error) {
	o = normalize(o, false)
	v, err := e.flightDo(ctx, optKey("T", o, ""), true, func() (any, error) { return e.computeTrending(ctx, o) })
	if err != nil {
		return nil, err
	}
	r := *v.(*domain.TrendReport)
	r.Terms = append([]domain.Term(nil), r.Terms...)
	return &r, nil
}

func (e *Engine) computeTrending(ctx context.Context, o domain.TrendOptions) (*domain.TrendReport, error) {
	c, now, err := e.getCorpus(ctx, o, func(now time.Time) time.Time {
		f := now.Add(-o.Window - o.Baseline)
		if d := startOfDay(now).AddDate(0, 0, -(sparkDays - 1)); d.Before(f) {
			f = d
		}
		return f
	})
	if err != nil {
		return nil, err
	}
	iw := c.firstAtOrAfter(now.Add(-o.Window).Unix())
	ib := c.firstAtOrAfter(now.Add(-o.Window - o.Baseline).Unix())
	end := len(c.arts)
	rep := &domain.TrendReport{GeneratedAt: now, Window: o.Window, Baseline: o.Baseline, Articles: end - iw}
	n1, n2 := float64(end-iw), float64(iw-ib)
	if n1 == 0 {
		return rep, nil
	}
	nT := len(c.dict.terms)
	win := make([]int32, nT)
	base := make([]int32, nT)
	for a := ib; a < iw; a++ {
		for _, t := range c.terms(a) {
			base[t]++
		}
	}
	for a := iw; a < end; a++ {
		for _, t := range c.terms(a) {
			win[t]++
		}
	}
	srcCnt := make([]int32, len(c.srcs))
	var cands []tcand
	for t := 0; t < nT; t++ {
		a := win[t]
		if int(a) < o.MinCount {
			continue
		}
		b := base[t]
		g2 := dunning(float64(a), float64(b), n1, n2)
		if g2 <= 0 {
			continue
		}
		if n2 > 0 && (float64(a)+.5)/n1/((float64(b)+.5)/n2) < minLift {
			continue
		}
		p := c.post(int32(t))
		wp := p[lowerBound(p, int32(iw)):]
		distinct := 0
		for _, a := range wp {
			s := c.src[a]
			if srcCnt[s] == 0 {
				distinct++
			}
			srcCnt[s]++
		}
		eff := 0.0
		if distinct >= o.MinSources {
			h := 0.0
			tot := float64(len(wp))
			for _, a := range wp {
				s := c.src[a]
				if k := srcCnt[s]; k > 0 {
					pr := float64(k) / tot
					h -= pr * math.Log(pr)
					srcCnt[s] = -k // mark as already counted
				}
			}
			eff = math.Exp(h)
		}
		for _, a := range wp {
			srcCnt[c.src[a]] = 0
		}
		if distinct < o.MinSources {
			continue
		}
		cands = append(cands, tcand{id: int32(t), win: a, base: b, sources: distinct, eff: eff, g2: g2, score: g2 * math.Log(1+eff)})
	}
	key := func(i int) string { return c.dict.terms[cands[i].id].key }
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].win != cands[j].win {
			return cands[i].win > cands[j].win
		}
		return key(i) < key(j)
	})
	pool := o.Limit * 4
	if pool < 60 {
		pool = 60
	}
	if pool > len(cands) {
		pool = len(cands)
	}
	cands = e.collapse(c, cands[:pool], iw)
	if len(cands) > o.Limit {
		cands = cands[:o.Limit]
	}

	ids := make([]int32, len(cands))
	for i, cd := range cands {
		ids[i] = cd.id
	}
	disp := c.display(ids)
	days := o.Baseline.Hours() / 24
	loc := now.Location()
	today := startOfDay(now.In(loc))
	var dayStart [sparkDays]int64
	for k := 0; k < sparkDays; k++ {
		dayStart[k] = today.AddDate(0, 0, k-(sparkDays-1)).Unix()
	}
	for _, cd := range cands {
		t := domain.Term{
			Term: disp[cd.id], Key: c.dict.terms[cd.id].key, Kind: c.kindName(cd.id),
			Count: int(cd.win), Sources: cd.sources, Score: cd.score, New: cd.base <= 1,
			Leanings: map[string]int{}, Spark: make([]int, sparkDays),
		}
		if days > 0 {
			t.BaselineRate = float64(cd.base) / days
		}
		p := c.post(cd.id)
		for _, a := range p {
			at := c.at[a]
			if k := sort.Search(sparkDays, func(i int) bool { return dayStart[i] > at }) - 1; k >= 0 {
				t.Spark[k]++
			}
			if int(a) >= iw {
				t.Leanings[leaningOf(c.srcs[c.src[a]])]++
				if t.FirstAt.IsZero() {
					t.FirstAt = c.arts[a].At
				}
			}
		}
		rep.Terms = append(rep.Terms, t)
	}
	return rep, nil
}

// collapse removes sub-terms subsumed by a larger phrase/entity that appears
// in >= subsumeRatio of their window articles. cands must be sorted by score.
// Between a sub-term A and its container B: B wins when it is an entity or
// scores at least 85% of A; otherwise B (a random bigram of a good unigram)
// is dropped instead.
func (e *Engine) collapse(c *corpus, cands []tcand, iw int) []tcand {
	idx := make(map[int32]int, len(cands))
	for i, cd := range cands {
		idx[cd.id] = i
	}
	dropped := make([]bool, len(cands))
	winPost := func(id int32) []int32 {
		p := c.post(id)
		return p[lowerBound(p, int32(iw)):]
	}
	for bi, b := range cands {
		key := c.dict.terms[b.id].key
		if !strings.Contains(key, " ") {
			continue
		}
		toks := strings.Split(key, " ")
		for l := 1; l < len(toks); l++ {
			for s := 0; s+l <= len(toks); s++ {
				sub := strings.Join(toks[s:s+l], " ")
				aid, ok := c.dict.ids[sub]
				if !ok {
					continue
				}
				ai, ok := idx[aid]
				if !ok || dropped[ai] || dropped[bi] {
					continue
				}
				a := cands[ai]
				if float64(intersect(winPost(a.id), winPost(b.id))) < subsumeRatio*float64(a.win) {
					continue
				}
				if c.kindName(b.id) == "entity" || b.score >= 0.85*a.score {
					dropped[ai] = true
				} else {
					dropped[bi] = true
				}
			}
		}
	}
	out := cands[:0:0]
	for i, cd := range cands {
		if !dropped[i] {
			out = append(out, cd)
		}
	}
	return out
}

func intersect(a, b []int32) int {
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			n++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return n
}
