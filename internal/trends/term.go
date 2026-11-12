package trends

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Term implements domain.TrendProvider: coverage of one term by outlet over
// the last `days` calendar days (default 7, max 120). key may be a Term.Key or
// any display form ("Pedro Sánchez"); it is canonicalised (folded, plural
// normalised, multi-word supported).
//
// Related terms are ranked by smoothed PMI weighted by support:
//
//	score = ln( co * N / (nA * df) ) * ln(1 + co)
//
// with co = articles containing both terms, nA = articles containing the term,
// df = articles containing the candidate and N = articles in the period;
// candidates need co >= 2 and sub/super-terms of the key are skipped.
func (e *Engine) Term(ctx context.Context, key string, days int, o domain.TrendOptions) (*domain.TermDetail, error) {
	o = normalize(o, false)
	if days <= 0 {
		days = DefaultTermDays
	}
	if days > maxTermDays {
		days = maxTermDays
	}
	ck := (&tokenizer{}).canonKey(key)
	v, err := e.flightDo(ctx, optKey("D", o, ck+"|"+itoa(days)), true, func() (any, error) { return e.computeTerm(ctx, ck, key, days, o) })
	if err != nil {
		return nil, err
	}
	d := *v.(*domain.TermDetail)
	return &d, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (e *Engine) computeTerm(ctx context.Context, ck, raw string, days int, o domain.TrendOptions) (*domain.TermDetail, error) {
	c, now, err := e.getCorpus(ctx, o, func(now time.Time) time.Time {
		return startOfDay(now).AddDate(0, 0, -(days - 1))
	})
	if err != nil {
		return nil, err
	}
	today := startOfDay(now)
	start := today.AddDate(0, 0, -(days - 1))
	i0 := int32(c.firstAtOrAfter(start.Unix()))
	end := int32(len(c.arts))

	det := &domain.TermDetail{Term: raw, Key: ck}
	det.Daily = make([]domain.DayCount, days)
	dayStart := make([]int64, days)
	for k := 0; k < days; k++ {
		d := today.AddDate(0, 0, k-(days-1))
		dayStart[k] = d.Unix()
		det.Daily[k] = domain.DayCount{Day: d, Leanings: map[string]int{}}
	}
	id, ok := c.dict.ids[ck]
	var arts []int32
	if ok {
		p := c.post(id)
		arts = p[lowerBound(p, i0):]
	}
	covered := map[int16]bool{}
	for _, a := range arts {
		covered[c.src[a]] = true
		at := c.at[a]
		if k := sort.Search(days, func(i int) bool { return dayStart[i] > at }) - 1; k >= 0 {
			det.Daily[k].Total++
			det.Daily[k].Leanings[leaningOf(c.srcs[c.src[a]])]++
		}
	}
	det.Total = len(arts)
	det.Coverage = c.coverage(arts)
	det.Missing = c.missingFor(covered)
	if !ok || len(arts) == 0 {
		return det, nil
	}
	if d := c.display([]int32{id}); d[id] != "" {
		det.Term = d[id]
	}

	// related terms
	co := make(map[int32]int32)
	for _, a := range arts {
		for _, t := range c.terms(int(a)) {
			if t != id {
				co[t]++
			}
		}
	}
	nA := float64(len(arts))
	N := float64(end - i0)
	type rc struct {
		id    int32
		score float64
	}
	var rcs []rc
	for t, n := range co {
		if n < 2 {
			continue
		}
		k := c.dict.terms[t].key
		if tokenSubterm(k, ck) || tokenSubterm(ck, k) {
			continue
		}
		p := c.post(t)
		df := float64(len(p) - lowerBound(p, i0))
		lift := float64(n) * N / (nA * df)
		if lift <= 1 {
			continue
		}
		rcs = append(rcs, rc{t, math.Log(lift) * math.Log(1+float64(n))})
	}
	sort.Slice(rcs, func(i, j int) bool {
		if rcs[i].score != rcs[j].score {
			return rcs[i].score > rcs[j].score
		}
		return c.dict.terms[rcs[i].id].key < c.dict.terms[rcs[j].id].key
	})
	var chosen []int32
	for _, r := range rcs {
		k := c.dict.terms[r.id].key
		skip := false
		for _, ch := range chosen {
			ckk := c.dict.terms[ch].key
			if tokenSubterm(k, ckk) || tokenSubterm(ckk, k) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		chosen = append(chosen, r.id)
		if len(chosen) == 8 {
			break
		}
	}
	disp := c.display(chosen)
	for _, t := range chosen {
		det.Related = append(det.Related, disp[t])
	}
	return det, nil
}
