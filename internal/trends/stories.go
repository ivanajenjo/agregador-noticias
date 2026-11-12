package trends

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Story clustering
//
// Every term gets a global weight W(t) = idf * kind * burst where
//
//	idf   = ln((N+1)/(df+1)), df over window+baseline, N = articles there
//	kind  = 1.3 for entities, 0.8 for plain n-grams, 1 for words
//	burst = min(1, max(0.15, lift/3)), lift = smoothed window/baseline rate
//	        ratio, so background chatter ("Gobierno", "Pedro Sánchez") weighs
//	        little and event terms ("dana") full.
//
// Nested terms inside one article ("pedro" in "pedro sanchez") count once: only
// the longest is kept.
//
// Terms present in <2 window articles (they cannot link anything) or in more
// than max(40, 12% of the window) articles are ignored.
//
// Articles are processed in time order (single pass, single-link with a
// centroid guard). The evidence that two articles are about the same event
// is the summed weight of the terms they share:
//
//	ev(a,b) = sum_{t in a∩b} W(t)
//
// An article is linked to an earlier article within 36 h if ev >= E, with
// E = 0.9 ln(N+1) (two shared specific terms such as "dana"+"valencia"). It
// then joins that article's cluster provided the cluster is <72 h old in
// total, has <120 articles and the article is typical of the whole cluster:
// sum_t W(t) * (members containing t)/|cluster| >= E/2 (this stops a cluster
// from drifting through one shared name). If the article is linked to several
// clusters in which it is typical, they are merged.
//
// Clusters with >= MinSources distinct outlets are stories; their score is
//
//	Score = outlets * (1 + 0.25*(distinct leanings - 1)) * max(0.05, 0.5^(age/12h))
//
// where age is measured from the latest article of the cluster.
const (
	storyMaxSize     = 120
	storyGap         = 36 * time.Hour
	storySpan        = 72 * time.Hour
	storyMinIDF      = 1.2
	storyHalfLife    = 12 * time.Hour
	storyTopTerms    = 4
	storyMaxDFFloor  = 40
	storyMaxDFFrac   = 0.12
	storyMaxDFAbs    = 400
	storyFullLift    = 3.0  // window/baseline rate ratio at which a term gets full weight
	storyMinBurst    = 0.15 // weight floor for background terms
	storyEvidence    = 0.9  // E = storyEvidence * ln(N+1)
	storyScanPerTerm = 250  // max earlier articles examined per term
)

type svec struct {
	ids []int32
	w   []float32 // W(t)
}

type cluster struct {
	members     []int32         // window-relative article indexes
	cnt         map[int32]int32 // term -> members containing it
	first, last int64
	dead        bool
}

// Stories implements domain.TrendProvider.
func (e *Engine) Stories(ctx context.Context, o domain.TrendOptions) ([]domain.Story, error) {
	o = normalize(o, true)
	r, err := e.storyResult(ctx, o)
	if err != nil {
		return nil, err
	}
	return append([]domain.Story(nil), r.stories...), nil
}

// storiesResult is the cached output of the clustering: the stories plus the
// article texts behind them (see storydetail.go).
type storiesResult struct {
	stories []domain.Story
	arts    map[string][]domain.ArticleLite // story id -> members, oldest first
	window  []domain.ArticleLite            // every article of the window
}

func (e *Engine) storyResult(ctx context.Context, o domain.TrendOptions) (*storiesResult, error) {
	v, err := e.flightDo(ctx, optKey("S", o, ""), true, func() (any, error) { return e.computeStories(ctx, o) })
	if err != nil {
		return nil, err
	}
	return v.(*storiesResult), nil
}

func (e *Engine) computeStories(ctx context.Context, o domain.TrendOptions) (*storiesResult, error) {
	c, now, err := e.getCorpus(ctx, o, func(now time.Time) time.Time { return now.Add(-o.Window - o.Baseline) })
	if err != nil {
		return nil, err
	}
	iw := c.firstAtOrAfter(now.Add(-o.Window).Unix())
	ib := c.firstAtOrAfter(now.Add(-o.Window - o.Baseline).Unix())
	end := len(c.arts)
	nWin := end - iw
	if nWin == 0 {
		return &storiesResult{}, nil
	}
	nT := len(c.dict.terms)
	winDF := make([]int32, nT)
	allDF := make([]int32, nT)
	for a := ib; a < end; a++ {
		for _, t := range c.terms(a) {
			allDF[t]++
			if a >= iw {
				winDF[t]++
			}
		}
	}
	maxDF := int32(math.Min(storyMaxDFAbs, math.Max(storyMaxDFFloor, storyMaxDFFrac*float64(nWin))))
	N := float64(end - ib)
	E := storyEvidence * math.Log(N+1)
	n1, n2 := float64(nWin), float64(iw-ib)

	weight := make([]float32, nT) // W(t); 0 = ignored
	for t := 0; t < nT; t++ {
		if winDF[t] < 2 || winDF[t] > maxDF {
			continue
		}
		idf := math.Log((N + 1) / (float64(allDF[t]) + 1))
		if idf < storyMinIDF {
			continue
		}
		if n2 > 0 {
			lift := (float64(winDF[t]) + .5) / n1 / ((float64(allDF[t]-winDF[t]) + .5) / n2)
			idf *= math.Min(1, math.Max(storyMinBurst, lift/storyFullLift))
		}
		switch c.dict.terms[t].kind {
		case kindEntity:
			idf *= 1.3
		case kindPhrase:
			idf *= 0.8
		}
		weight[t] = float32(idf)
	}
	vecs := make([]svec, nWin)
	for i := 0; i < nWin; i++ {
		v := &vecs[i]
		for _, t := range c.terms(iw + i) {
			if weight[t] > 0 {
				v.ids = append(v.ids, t)
				v.w = append(v.w, weight[t])
			}
		}
		// nested terms ("pedro", "sanchez" inside "pedro sanchez") are one
		// piece of evidence, not several: keep only the longest.
		for _, u := range v.ids {
			uk := c.dict.terms[u].key
			if !strings.Contains(uk, " ") {
				continue
			}
			for k := 0; k < len(v.ids); k++ {
				if v.ids[k] != u && tokenSubterm(c.dict.terms[v.ids[k]].key, uk) {
					v.ids = append(v.ids[:k], v.ids[k+1:]...)
					v.w = append(v.w[:k], v.w[k+1:]...)
					k--
				}
			}
		}
	}

	// ---- clustering
	var clusters []*cluster
	of := make([]int32, nWin) // article -> cluster id
	for i := range of {
		of[i] = -1
	}
	ev := make([]float64, nWin)
	var touched []int32
	typical := func(v *svec, cl *cluster) bool {
		s := 0.0
		for k, t := range v.ids {
			s += float64(v.w[k]) * float64(cl.cnt[t])
		}
		return s/float64(len(cl.members)) >= E/2
	}
	add := func(cl *cluster, i int32) {
		cl.members = append(cl.members, i)
		for _, t := range vecs[i].ids {
			cl.cnt[t]++
		}
		at := c.at[iw+int(i)]
		if at < cl.first {
			cl.first = at
		}
		if at > cl.last {
			cl.last = at
		}
	}
	for i := 0; i < nWin; i++ {
		v := &vecs[i]
		if len(v.ids) == 0 {
			continue
		}
		if i&255 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		at := c.at[iw+i]
		touched = touched[:0]
		for k, t := range v.ids {
			p := c.post(t)
			pos := lowerBound(p, int32(iw+i)) // entries before this article are p[:pos]
			scanned := 0
			for q := pos - 1; q >= 0 && scanned < storyScanPerTerm; q-- {
				j := int(p[q]) - iw
				if j < 0 {
					break
				}
				if time.Duration(at-c.at[iw+j])*time.Second > storyGap {
					break
				}
				scanned++
				if ev[j] == 0 {
					touched = append(touched, int32(j))
				}
				ev[j] += float64(v.w[k])
			}
		}
		// best evidence per cluster
		type link struct {
			cid int32
			ev  float64
		}
		var links []link
		for _, j := range touched {
			if e := ev[j]; e >= E && of[j] >= 0 {
				found := false
				for k := range links {
					if links[k].cid == of[j] {
						if e > links[k].ev {
							links[k].ev = e
						}
						found = true
						break
					}
				}
				if !found {
					links = append(links, link{of[j], e})
				}
			}
			ev[j] = 0
		}
		sort.Slice(links, func(a, b int) bool {
			if links[a].ev != links[b].ev {
				return links[a].ev > links[b].ev
			}
			return links[a].cid < links[b].cid
		})
		var target *cluster
		tid := int32(-1)
		for _, l := range links {
			cl := clusters[l.cid]
			if cl.dead || !typical(v, cl) {
				continue
			}
			if target == nil {
				if len(cl.members) >= storyMaxSize || at-cl.first > int64(storySpan/time.Second) {
					continue
				}
				target, tid = cl, l.cid
				continue
			}
			// article is typical of two clusters: same story, merge
			if len(target.members)+len(cl.members) >= storyMaxSize ||
				maxI(target.last, cl.last)-minI(target.first, cl.first) > int64(storySpan/time.Second) {
				continue
			}
			for _, m := range cl.members {
				of[m] = tid
				target.members = append(target.members, m)
			}
			for t, n := range cl.cnt {
				target.cnt[t] += n
			}
			target.first, target.last = minI(target.first, cl.first), maxI(target.last, cl.last)
			cl.dead = true
			cl.members = nil
		}
		if target == nil {
			target = &cluster{cnt: map[int32]int32{}, first: at, last: at}
			tid = int32(len(clusters))
			clusters = append(clusters, target)
		}
		of[i] = tid
		add(target, int32(i))
	}

	// ---- build stories
	var stories []domain.Story
	var allIDs []int32
	type pre struct {
		cl    *cluster
		terms []int32
		cov   []int32
	}
	var pres []pre
	for _, cl := range clusters {
		if cl.dead || len(cl.members) < o.MinSources {
			continue
		}
		srcSet := map[int16]struct{}{}
		for _, m := range cl.members {
			srcSet[c.src[iw+int(m)]] = struct{}{}
		}
		if len(srcSet) < o.MinSources {
			continue
		}
		p := pre{cl: cl}
		p.terms = topClusterTerms(c, weight, cl)
		allIDs = append(allIDs, p.terms...)
		pres = append(pres, p)
	}
	disp := c.display(allIDs)
	loc := now.Location()
	members := map[string][]domain.ArticleLite{}
	for _, p := range pres {
		cl := p.cl
		arts := make([]int32, len(cl.members))
		for i, m := range cl.members {
			arts[i] = int32(iw) + m
		}
		sort.Slice(arts, func(i, j int) bool { return arts[i] < arts[j] })
		st := domain.Story{Articles: len(arts), Leanings: map[string]int{}}
		covered := map[int16]bool{}
		first := arts[0]
		for _, a := range arts {
			covered[c.src[a]] = true
			st.Leanings[leaningOf(c.srcs[c.src[a]])]++
			if c.arts[a].At.Before(c.arts[first].At) {
				first = a
			}
		}
		st.Sources = len(covered)
		st.FirstAt = c.arts[first].At
		st.FirstBy = c.arts[first].SourceID
		st.LastAt = c.arts[arts[0]].At
		for _, a := range arts {
			if c.arts[a].At.After(st.LastAt) {
				st.LastAt = c.arts[a].At
			}
		}
		st.Coverage = c.coverage(arts)
		st.Missing = c.missingFor(covered)
		st.Title = representative(c, vecs, cl, iw)
		keys := make([]string, 0, storyTopTerms)
		for _, t := range p.terms {
			st.Terms = append(st.Terms, disp[t])
			keys = append(keys, c.dict.terms[t].key)
		}
		sort.Strings(keys)
		h := sha1.Sum([]byte(strings.Join(keys, "|") + "@" + st.FirstAt.In(loc).Format("2006-01-02")))
		st.ID = hex.EncodeToString(h[:6])
		ms := make([]domain.ArticleLite, len(arts))
		for i, a := range arts {
			ms[i] = c.arts[a]
		}
		members[st.ID] = ms
		leans := map[string]bool{}
		for l := range st.Leanings {
			if l != "desconocida" {
				leans[l] = true
			}
		}
		div := 1.0
		if len(leans) > 1 {
			div += 0.25 * float64(len(leans)-1)
		}
		age := now.Sub(st.LastAt)
		if age < 0 {
			age = 0
		}
		fresh := math.Max(0.05, math.Pow(0.5, float64(age)/float64(storyHalfLife)))
		st.Score = float64(st.Sources) * div * fresh
		stories = append(stories, st)
	}
	sort.Slice(stories, func(i, j int) bool {
		a, b := stories[i], stories[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Articles != b.Articles {
			return a.Articles > b.Articles
		}
		if !a.FirstAt.Equal(b.FirstAt) {
			return a.FirstAt.Before(b.FirstAt)
		}
		return a.ID < b.ID
	})
	if len(stories) > o.Limit {
		stories = stories[:o.Limit]
	}
	res := &storiesResult{stories: stories, arts: make(map[string][]domain.ArticleLite, len(stories)), window: c.arts[iw:end]}
	for _, st := range stories {
		ms := members[st.ID]
		res.arts[st.ID] = ms
	}
	return res, nil
}

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// topClusterTerms picks up to storyTopTerms terms shared by several members,
// preferring specific (high weight), widely shared and multi-word terms,
// without listing a term next to one that contains it.
func topClusterTerms(c *corpus, w []float32, cl *cluster) []int32 {
	type sc struct {
		id    int32
		score float64
		cnt   int32
	}
	var all []sc
	for t, n := range cl.cnt {
		if n < 2 {
			continue
		}
		all = append(all, sc{t, float64(n) * float64(w[t]), n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return c.dict.terms[all[i].id].key < c.dict.terms[all[j].id].key
	})
	var chosen []sc
	for _, cd := range all {
		key := c.dict.terms[cd.id].key
		skip := false
		for i := 0; i < len(chosen); i++ {
			ck := c.dict.terms[chosen[i].id].key
			if tokenSubterm(key, ck) {
				skip = true
				break
			}
			if tokenSubterm(ck, key) && float64(cd.cnt) >= 0.8*float64(chosen[i].cnt) {
				chosen = append(chosen[:i], chosen[i+1:]...) // the longer one is more informative
				i--
			}
		}
		if skip {
			continue
		}
		chosen = append(chosen, cd)
		if len(chosen) == storyTopTerms {
			break
		}
	}
	out := make([]int32, len(chosen))
	for i, ch := range chosen {
		out[i] = ch.id
	}
	return out
}

// representative returns the headline most typical of the cluster (summed
// weight of its terms shared with the other members), with a mild
// preference for short titles.
func representative(c *corpus, vecs []svec, cl *cluster, iw int) string {
	bestA, bestS := int32(-1), math.Inf(-1)
	for _, mem := range cl.members {
		v := &vecs[mem]
		s := 0.0
		for k, t := range v.ids {
			s += float64(v.w[k]) * float64(cl.cnt[t]-1)
		}
		a := int32(iw) + mem
		s = s/float64(len(cl.members)) - 0.01*float64(len([]rune(c.arts[a].Title)))
		if s > bestS || (s == bestS && a < bestA) {
			bestA, bestS = a, s
		}
	}
	return c.arts[bestA].Title
}
