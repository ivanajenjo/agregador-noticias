package framing

import (
	"math"
	"sort"
)

// ZScores implements the log-odds ratio with an informative Dirichlet prior
// of Monroe, Colaresi & Quinn, "Fightin' Words" (2008), eq. 16 and 22:
//
//	delta_w = ln((y1_w+a_w)/(n1+a0-y1_w-a_w)) - ln((y2_w+a_w)/(n2+a0-y2_w-a_w))
//	var_w   = 1/(y1_w+a_w) + 1/(y2_w+a_w)
//	z_w     = delta_w / sqrt(var_w)
//
// y1, y2 are the word counts of the two corpora (n1, n2 their totals) and a
// the prior pseudo-counts (a0 their sum), typically proportional to the
// frequencies in a background corpus. Positive z means "more typical of 1".
// The vocabulary is the union of y1 and y2; a word missing from a gets a
// pseudo-count of 0.01 so the score stays finite.
func ZScores(y1, y2, a map[string]float64) map[string]float64 {
	var n1, n2, a0 float64
	for _, v := range y1 {
		n1 += v
	}
	for _, v := range y2 {
		n2 += v
	}
	vocab := map[string]struct{}{}
	for w := range y1 {
		vocab[w] = struct{}{}
	}
	for w := range y2 {
		vocab[w] = struct{}{}
	}
	prior := func(w string) float64 {
		if v, ok := a[w]; ok && v > 0 {
			return v
		}
		return 0.01
	}
	for w := range vocab {
		a0 += prior(w)
	}
	out := make(map[string]float64, len(vocab))
	for w := range vocab {
		aw := prior(w)
		p1, p2 := y1[w]+aw, y2[w]+aw
		d1, d2 := n1+a0-p1, n2+a0-p2
		if d1 <= 0 || d2 <= 0 {
			continue
		}
		delta := math.Log(p1/d1) - math.Log(p2/d2)
		out[w] = delta / math.Sqrt(1/p1+1/p2)
	}
	return out
}

// priorFrom builds the informative prior for vocab from background counts:
// a_w = alpha0 * (bg_w + 0.5) / (sum(bg over vocab) + 0.5*|vocab|), so the
// pseudo-counts sum to alpha0 over the vocabulary.
func priorFrom(vocab map[string]struct{}, bg map[string]float64, alpha0 float64) map[string]float64 {
	var tot float64
	for w := range vocab {
		tot += bg[w] + 0.5
	}
	a := make(map[string]float64, len(vocab))
	for w := range vocab {
		a[w] = alpha0 * (bg[w] + 0.5) / tot
	}
	return a
}

type scored struct {
	key string
	z   float64
}

func topByZ(z map[string]float64, ok func(string) bool, min float64, n int) []scored {
	var s []scored
	for w, v := range z {
		if v >= min && ok(w) {
			s = append(s, scored{w, v})
		}
	}
	sort.Slice(s, func(i, j int) bool {
		if s[i].z != s[j].z {
			return s[i].z > s[j].z
		}
		return s[i].key < s[j].key
	})
	if len(s) > n {
		s = s[:n]
	}
	return s
}
