package framing

import (
	"math"
	"sort"
	"strings"
)

// Op is one step of a word-level diff between sequences a and b.
type Op struct {
	Kind byte // '=' in both, '-' only in a, '+' only in b
	A, B int  // indexes (-1 when absent)
}

// Diff computes a word-level diff (longest common subsequence) of a to b.
func Diff(a, b []string) []Op {
	n, m := len(a), len(b)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else if l[i+1][j] >= l[i][j+1] {
				l[i][j] = l[i+1][j]
			} else {
				l[i][j] = l[i][j+1]
			}
		}
	}
	var ops []Op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, Op{'=', i, j})
			i++
			j++
		case l[i+1][j] >= l[i][j+1]:
			ops = append(ops, Op{'-', i, -1})
			i++
		default:
			ops = append(ops, Op{'+', -1, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, Op{'-', i, -1})
	}
	for ; j < m; j++ {
		ops = append(ops, Op{'+', -1, j})
	}
	return ops
}

// Word classes of a headline token.
const (
	ClassCommon = "common" // shared by most outlets (or by the consensus headline): neutral
	ClassSome   = "some"   // shared by a minority of outlets
	ClassUnique = "uniq"   // appears in this outlet's headline only
)

// Word is a token of a headline with its highlight class.
type Word struct {
	Text  string
	Class string
}

type headline struct {
	toks []string // display tokens
	keys []string // normalised keys, parallel to toks
}

func newHeadline(title string) headline {
	var h headline
	for _, t := range strings.Fields(title) {
		k := wordKey(t)
		if k == "" { // lone punctuation: attach to the previous token
			if len(h.toks) > 0 {
				h.toks[len(h.toks)-1] += " " + t
			}
			continue
		}
		h.toks = append(h.toks, t)
		h.keys = append(h.keys, k)
	}
	return h
}

// HeadlineDiff is the comparison of several headlines against their
// consensus.
type HeadlineDiff struct {
	Consensus   string
	ConsensusIx int      // index of the headline chosen as consensus
	Words       [][]Word // per input headline
	Omitted     [][]string
}

// CompareHeadlines highlights, for every headline (one per outlet), the words
// that differ from the group: a word used by at least half of the outlets (or
// that appears in the consensus headline) is neutral; one that no other outlet
// uses is "unique"; the rest are "some". The consensus headline is the one
// sharing most words with the others. Omitted lists the consensus-wide words
// an outlet's headline lacks.
func CompareHeadlines(titles []string) HeadlineDiff {
	hs := make([]headline, len(titles))
	freq := map[string]int{}
	for i, t := range titles {
		hs[i] = newHeadline(t)
		seen := map[string]bool{}
		for _, k := range hs[i].keys {
			if !seen[k] {
				seen[k] = true
				freq[k]++
			}
		}
	}
	n := len(titles)
	d := HeadlineDiff{Words: make([][]Word, n), Omitted: make([][]string, n), ConsensusIx: -1}
	if n == 0 {
		return d
	}
	need := (n + 1) / 2 // ceil(n/2)
	if need < 2 {
		need = 2
	}
	best := math.Inf(-1)
	for i, h := range hs {
		if len(h.keys) == 0 {
			continue
		}
		s := 0.0
		for _, k := range h.keys {
			if !isStop(k) {
				s += float64(freq[k] - 1)
			}
		}
		s /= math.Sqrt(float64(len(h.keys)))
		if s > best {
			best, d.ConsensusIx = s, i
		}
	}
	if d.ConsensusIx < 0 {
		return d
	}
	cons := hs[d.ConsensusIx]
	d.Consensus = titles[d.ConsensusIx]
	for i, h := range hs {
		inCons := map[string]bool{}
		if i == d.ConsensusIx {
			for _, k := range h.keys {
				inCons[k] = true
			}
		} else {
			for _, op := range Diff(h.keys, cons.keys) {
				if op.Kind == '=' {
					inCons[h.keys[op.A]] = true
				}
			}
		}
		present := map[string]bool{}
		for j, k := range h.keys {
			present[k] = true
			c := ClassSome
			switch {
			case isStop(k) || freq[k] >= need || (inCons[k] && freq[k] > 1):
				c = ClassCommon
			case freq[k] == 1 && n > 1:
				c = ClassUnique
			}
			d.Words[i] = append(d.Words[i], Word{Text: h.toks[j], Class: c})
		}
		if n >= 3 {
			var om []string
			seen := map[string]bool{}
			for j, k := range cons.keys {
				if !present[k] && !isStop(k) && freq[k] >= need && !seen[k] {
					seen[k] = true
					om = append(om, strings.Trim(cons.toks[j], `.,;:!?"«»“”()`))
				}
			}
			sort.Strings(om)
			d.Omitted[i] = om
		}
	}
	return d
}
