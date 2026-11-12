// Package revisions holds the pure logic of the headline-edit tracker:
// materiality of a text change and word-level diffs. It has no dependencies.
package revisions

import "strings"

// Op is the kind of a diff segment.
type Op uint8

const (
	Equal Op = iota
	Delete
	Insert
)

// Seg is a run of consecutive words with the same Op (joined by single spaces).
type Seg struct {
	Op   Op
	Text string
}

type tokOp struct {
	Op  Op
	Tok string
}

// maxDiffCells bounds the LCS table (words(a)*words(b)) to keep memory low.
const maxDiffCells = 250_000

// diffTokens computes a minimal edit script between two token lists (LCS).
// Within a hunk, deletions come before insertions.
func diffTokens(a, b []string) []tokOp {
	// trim common prefix/suffix
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var out []tokOp
	for _, t := range a[:p] {
		out = append(out, tokOp{Equal, t})
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	out = append(out, lcsDiff(ma, mb)...)
	for _, t := range a[len(a)-s:] {
		out = append(out, tokOp{Equal, t})
	}
	return out
}

func lcsDiff(a, b []string) []tokOp {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	if len(a)*len(b) > maxDiffCells {
		var out []tokOp
		for _, t := range a {
			out = append(out, tokOp{Delete, t})
		}
		for _, t := range b {
			out = append(out, tokOp{Insert, t})
		}
		return out
	}
	n, m := len(a), len(b)
	l := make([][]int32, n+1)
	for i := range l {
		l[i] = make([]int32, m+1)
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
	var out []tokOp
	i, j := 0, 0
	var dels, inss []tokOp
	flush := func() {
		out = append(out, dels...)
		out = append(out, inss...)
		dels, inss = dels[:0], inss[:0]
	}
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			flush()
			out = append(out, tokOp{Equal, a[i]})
			i++
			j++
		case i < n && (j == m || l[i+1][j] >= l[i][j+1]):
			dels = append(dels, tokOp{Delete, a[i]})
			i++
		default:
			inss = append(inss, tokOp{Insert, b[j]})
			j++
		}
	}
	flush()
	return out
}

// WordDiff returns a word-level diff of a -> b (words are whitespace separated;
// comparison is exact, so case and accents matter). Adjacent words with the
// same Op are merged into one Seg.
func WordDiff(a, b string) []Seg {
	ops := diffTokens(strings.Fields(a), strings.Fields(b))
	var segs []Seg
	for _, o := range ops {
		if n := len(segs); n > 0 && segs[n-1].Op == o.Op {
			segs[n-1].Text += " " + o.Tok
			continue
		}
		segs = append(segs, Seg{o.Op, o.Tok})
	}
	return segs
}
