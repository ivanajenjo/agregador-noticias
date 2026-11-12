package revisions

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// Kinds mirror domain.RevisionTitle / domain.RevisionSummary (this package
// has no dependencies, so the strings are repeated).
const (
	KindTitle   = "titular"
	KindSummary = "resumen"
)

var accentFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ß", "ss",
)

var titleSeps = []string{" - ", " | ", " – ", " — ", " · ", " :: "}

// stripOutletSuffix removes a trailing "- El País" style segment: the last
// segment after a separator when it is short (<= 4 words) and the head keeps
// at least 3 words.
func stripOutletSuffix(s string) string {
	for {
		cut, seplen := -1, 0
		for _, sep := range titleSeps {
			if i := strings.LastIndex(s, sep); i > cut {
				cut, seplen = i, len(sep)
			}
		}
		if cut < 0 {
			return s
		}
		head, tail := strings.TrimSpace(s[:cut]), strings.TrimSpace(s[cut+seplen:])
		if len(strings.Fields(tail)) > 4 || len([]rune(tail)) > 32 || len(strings.Fields(head)) < 3 {
			return s
		}
		s = head
	}
}

var (
	summaryTail = regexp.MustCompile(`(?i)(\s*(\[?\s*(\.{3}|…)\s*\]?|\(?leer más\)?|seguir leyendo|sigue leyendo|continúa leyendo|continua leyendo|read more)\.?\s*)+$`)
	postedBy    = regexp.MustCompile(`(?i)\s*the post .* appeared first on .*$`)
)

func baseNorm(s string) []string {
	s = accentFold.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

// Tokens normalizes text of the given kind into comparable words: lowercase,
// accent-folded, punctuation and whitespace removed; titles lose a trailing
// outlet suffix, summaries lose truncation markers ("[…]", "Leer más").
func Tokens(kind, s string) []string {
	if kind == KindTitle {
		s = stripOutletSuffix(strings.TrimSpace(s))
	} else {
		s = postedBy.ReplaceAllString(s, "")
		s = summaryTail.ReplaceAllString(strings.TrimSpace(s), "")
	}
	return baseNorm(s)
}

// Hash is a stable fingerprint of the normalized text (the hash-chain element).
func Hash(kind, s string) string {
	h := sha256.Sum256([]byte(strings.Join(Tokens(kind, s), " ")))
	return hex.EncodeToString(h[:12])
}

var stopwords = map[string]bool{
	"el": true, "la": true, "los": true, "las": true, "un": true, "una": true, "unos": true, "unas": true,
	"de": true, "del": true, "y": true, "e": true, "en": true, "a": true, "al": true, "que": true, "se": true, "lo": true,
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// MaxTypoDistance: a single-word replacement within this edit distance (and
// words of at least 4 letters, no digits) is a typo fix, not an editorial change.
const MaxTypoDistance = 2

func isTypo(a, b string) bool {
	if hasDigit(a) || hasDigit(b) || len([]rune(a)) < 4 || len([]rune(b)) < 4 {
		return false
	}
	return editDistance(a, b) <= MaxTypoDistance
}

// Material reports whether old -> new is an editorial change worth recording.
// It ignores differences in case, accents, whitespace, punctuation, trailing
// outlet suffixes (titles), truncation markers (summaries), pure word
// reordering, single-word typo fixes and added/removed articles or
// prepositions. Changes to numbers, negations or different words are material.
func Material(kind, oldText, newText string) bool {
	a, b := Tokens(kind, oldText), Tokens(kind, newText)
	if len(a) == 0 || len(b) == 0 {
		return false // nothing to compare against: feed noise, not an edit
	}
	if sameMultiset(a, b) {
		return false
	}
	if kind == KindSummary && (hasPrefix(a, b) || hasPrefix(b, a)) {
		return false // one side is the truncated version of the other
	}
	ops := diffTokens(a, b)
	var dels, inss []string
	material := false
	flush := func() {
		defer func() { dels, inss = nil, nil }()
		switch {
		case len(dels) == 0 && len(inss) == 0:
		case len(dels) == len(inss):
			for i := range dels {
				if !isTypo(dels[i], inss[i]) {
					material = true
				}
			}
		case len(dels)+len(inss) == 1:
			t := append(dels, inss...)[0]
			if !stopwords[t] {
				material = true
			}
		default:
			material = true
		}
	}
	for _, o := range ops {
		switch o.Op {
		case Equal:
			flush()
		case Delete:
			dels = append(dels, o.Tok)
		case Insert:
			inss = append(inss, o.Tok)
		}
	}
	flush()
	return material
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, t := range a {
		m[t]++
	}
	for _, t := range b {
		m[t]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func hasPrefix(long, short []string) bool {
	if len(short) > len(long) {
		return false
	}
	for i := range short {
		if long[i] != short[i] {
			return false
		}
	}
	return true
}
