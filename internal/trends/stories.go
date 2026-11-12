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
// Each article of the window gets a sparse vector over its terms (entities,
// title bigrams/trigrams and unigrams) weighted by IDF computed over
// window+baseline (idf = ln((N+1)/(df+1)), x1.3 for entities, x1.15 for
// phrases) times a burst factor min(1, max(0.15, lift/3)), where lift is the
// smoothed window/baseline rate ratio (so background chatter such as
// "Gobierno" weighs little and event terms such as "dana" full), and
// L2-normalised. Terms present in <2 window articles (they can't
// link anything) or in more than max(40, 12% of the window) articles
// (background chatter) are ignored.
//
// Articles are processed in time order (single pass, "leader" clustering):
// each one joins the best existing cluster if the cosine between its vector
// and the cluster centroid (sum of unit vectors) is >= storySim, the cluster
// was updated in the last 36 h, spans < 72 h and has < storyMaxSize articles;
// otherwise it starts a new cluster. A merge sweep then joins small
// fragments whose centroids are >= storyMerge similar.
//
// Clusters with >= MinSources distinct outlets are stories; their score is
//
//	Score = outlets * (1 + 0.25*(distinct leanings - 1)) * max(0.05, 0.5^(age/12h))
//
// where age is measured from the latest article of the cluster.
const (
	storySim        = 0.27
	storyMerge      = 0.40
	storyMaxSize    = 120
	storyGap        = 36 * time.Hour
	storySpan       = 72 * time.Hour
	storyMinIDF     = 1.2
	storyHalfLife   = 12 * time.Hour
	storyTopTerms   = 4
	storyMaxDFFloor = 40
	storyMaxDFFrac  = 0.12
	storyFullLift   = 3.0  // window/baseline rate ratio at which a term gets full weight
	storyMinBurst   = 0.15 // weight floor for background terms
)

type svec struct {
	ids  []int32
	w    []float32 // unit-normalised weights
	norm float32   // raw L2 norm (raw weight = w*norm)
}

type cluster struct {
	members     []int32 // window article indexes
	cent        map[int32]float32
	sumsq       float64
	first, last int64
	dead        bool
}

func (cl *cluster) addVec(v *svec, tc map[int32][]int32, id int32) {
	for k, t := range v.ids {
		old, ok := cl.cent[t]
		if !ok {
			tc[t] = append(tc[t], id)
		}
		nv := old + v.w[k]
		cl.sumsq += float64(nv)*float64(nv) - float64(old)*float64(old)
		cl.cent[t] = nv
	}
}

// Stories implements domain.TrendProvider.
func (e *Engine) Stories(ctx context.Context, o domain.TrendOptions) ([]domain.Story, error) {
	o = normalize(o, true)
	v, err := e.flightDo(ctx, optKey("S", o, ""), true, func() (any, error) { return e.computeStories(ctx, o) })
	if err != nil {
		return nil, err
	}
	return append([]domain.Story(nil), v.([]domain.Story)...), nil
}

func (e *Engine) computeStories(ctx context.Context, o domain.TrendOptions) ([]domain.Story, error) {
	c, now, err := e.getCorpus(ctx, o, func(now time.Time) time.Time { return now.Add(-o.Window - o.Baseline) })
	if err != nil {
		return nil, err
	}
	iw := c.firstAtOrAfter(now.Add(-o.Window).Unix())
	ib := c.firstAtOrAfter(now.Add(-o.Window - o.Baseline).Unix())
	end := len(c.arts)
	nWin := end - iw
	if nWin == 0 {
		return nil, nil
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
	maxDF := int32(math.Max(storyMaxDFFloor, storyMaxDFFrac*float64(nWin)))
	N := float64(end - ib)

	n1, n2 := float64(nWin), float64(iw-ib)
	vecs := make([]svec, nWin)
	for i := 0; i < nWin; i++ {
		a := iw + i
		v := &vecs[i]
		var sq float64
		for _, t := range c.terms(a) {
			if winDF[t] < 2 || winDF[t] > maxDF {
				continue
			}
			idf := math.Log((N + 1) / (float64(allDF[t]) + 1))
			if idf < storyMinIDF {
				continue
			}
			if n2 > 0 {
				// burstiness: background terms (rate in the window ~ rate in the
				// baseline) are down-weighted, event terms keep full weight.
				lift := (float64(winDF[t]) + .5) / n1 / ((float64(allDF[t]-winDF[t]) + .5) / n2)
				idf *= math.Min(1, math.Max(storyMinBurst, lift/storyFullLift))
			}
			switch c.dict.terms[t].kind {
			case kindEntity:
				idf *= 1.3
			case kindPhrase:
				idf *= 1.15
			}
			v.ids = append(v.ids, t)
			v.w = append(v.w, float32(idf))
			sq += idf * idf
		}
		if sq > 0 {
			v.norm = float32(math.Sqrt(sq))
			for k := range v.w {
				v.w[k] /= v.norm
			}
		}
	}

	// ---- single pass clustering
	var clusters []*cluster
	tc := map[int32][]int32{} // term -> clusters containing it
	acc := map[int32]float64{}
	for i := 0; i < nWin; i++ {
		v := &vecs[i]
		if len(v.ids) == 0 {
			continue
		}
		at := c.at[iw+i]
		for k := range acc {
			delete(acc, k)
		}
		for k, t := range v.ids {
			for _, cid := range tc[t] {
				cl := clusters[cid]
				if cl.dead || len(cl.members) >= storyMaxSize ||
					time.Duration(at-cl.last)*time.Second > storyGap || time.Duration(at-cl.first)*time.Second > storySpan {
					continue
				}
				acc[cid] += float64(v.w[k]) * float64(cl.cent[t])
			}
		}
		best, bestCos := int32(-1), 0.0
		for cid, dot := range acc {
			cs := dot / math.Sqrt(clusters[cid].sumsq)
			if cs > bestCos || (cs == bestCos && cid < best) {
				best, bestCos = cid, cs
			}
		}
		if best >= 0 && bestCos >= storySim {
			cl := clusters[best]
			cl.members = append(cl.members, int32(i))
			cl.addVec(v, tc, best)
			cl.last = at
			continue
		}
		cl := &cluster{cent: map[int32]float32{}, first: at, last: at, members: []int32{int32(i)}}
		id := int32(len(clusters))
		clusters = append(clusters, cl)
		cl.addVec(v, tc, id)
	}

	// ---- merge sweep: small fragments into similar clusters
	order := make([]int32, 0, len(clusters))
	for i, cl := range clusters {
		if len(cl.members) > 0 && !cl.dead {
			order = append(order, int32(i))
		}
	}
	sort.Slice(order, func(a, b int) bool {
		la, lb := len(clusters[order[a]].members), len(clusters[order[b]].members)
		if la != lb {
			return la < lb
		}
		return order[a] < order[b]
	})
	for _, sid := range order {
		s := clusters[sid]
		if s.dead {
			continue
		}
		for k := range acc {
			delete(acc, k)
		}
		for t, wt := range s.cent {
			for _, cid := range tc[t] {
				if cid == sid || clusters[cid].dead {
					continue
				}
				acc[cid] += float64(wt) * float64(clusters[cid].cent[t])
			}
		}
		best, bestCos := int32(-1), 0.0
		for cid, dot := range acc {
			o := clusters[cid]
			if len(o.members)+len(s.members) > storyMaxSize {
				continue
			}
			if gapSecs(s, o) > int64(storyGap/time.Second) || maxI(s.last, o.last)-minI(s.first, o.first) > int64(storySpan/time.Second) {
				continue
			}
			cs := dot / (math.Sqrt(s.sumsq) * math.Sqrt(o.sumsq))
			if cs > bestCos || (cs == bestCos && cid < best) {
				best, bestCos = cid, cs
			}
		}
		if best >= 0 && bestCos >= storyMerge {
			o := clusters[best]
			o.members = append(o.members, s.members...)
			for t, wt := range s.cent {
				if _, ok := o.cent[t]; !ok {
					tc[t] = append(tc[t], best)
				}
				o.cent[t] += wt
			}
			o.sumsq = 0
			for _, x := range o.cent {
				o.sumsq += float64(x) * float64(x)
			}
			o.first, o.last = minI(o.first, s.first), maxI(o.last, s.last)
			s.dead = true
		}
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
		p.terms = topClusterTerms(c, vecs, cl)
		allIDs = append(allIDs, p.terms...)
		pres = append(pres, p)
	}
	disp := c.display(allIDs)
	loc := now.Location()
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
	return stories, nil
}

func gapSecs(a, b *cluster) int64 {
	if a.last < b.first {
		return b.first - a.last
	}
	if b.last < a.first {
		return a.first - b.last
	}
	return 0
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
// preferring specific (high raw weight), widely shared and multi-word terms,
// without listing a term next to one that contains it.
func topClusterTerms(c *corpus, vecs []svec, cl *cluster) []int32 {
	type tw struct {
		cnt int
		raw float64
	}
	m := map[int32]*tw{}
	for _, mem := range cl.members {
		v := &vecs[mem]
		for k, t := range v.ids {
			x := m[t]
			if x == nil {
				x = &tw{raw: float64(v.w[k]) * float64(v.norm)}
				m[t] = x
			}
			x.cnt++
		}
	}
	type sc struct {
		id    int32
		score float64
		cnt   int
	}
	var all []sc
	for t, x := range m {
		if x.cnt < 2 {
			continue
		}
		s := float64(x.cnt) * x.raw
		all = append(all, sc{t, s, x.cnt})
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

// representative returns the headline most similar to the centroid, with a
// mild preference for short titles.
func representative(c *corpus, vecs []svec, cl *cluster, iw int) string {
	bestA, bestS := int32(-1), math.Inf(-1)
	cn := math.Sqrt(cl.sumsq)
	for _, mem := range cl.members {
		v := &vecs[mem]
		dot := 0.0
		for k, t := range v.ids {
			dot += float64(v.w[k]) * float64(cl.cent[t])
		}
		cs := 0.0
		if cn > 0 {
			cs = dot / cn
		}
		a := int32(iw) + mem
		s := cs - 0.001*float64(len([]rune(c.arts[a].Title)))
		if s > bestS || (s == bestS && a < bestA) {
			bestA, bestS = a, s
		}
	}
	return c.arts[bestA].Title
}
