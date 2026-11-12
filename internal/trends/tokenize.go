package trends

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token flags.
const (
	fCap  uint16 = 1 << iota // starts with an uppercase letter (or acronym)
	fAcr                     // acronym: >=2 runes, letters/digits, no lowercase, at least one letter
	fHard                    // hard punctuation precedes the token (breaks phrases)
	fSent                    // token starts a sentence/clause
	fStop                    // stopword, noise, number, month, weekday: never a term
	fConn                    // entity connector (de, del, la, las, los, el)
	fNum                     // purely digits
	fLong                    // eligible as a unigram term
)

// Candidate kinds, ordered by priority (an entity wins over a phrase).
const (
	kindWord uint8 = iota
	kindPhrase
	kindEntity
)

type token struct {
	start, end int32 // byte range in the source text
	kOff, kLen int32 // range of the (stemmed) key in tokenizer.keyBuf
	flags      uint16
}

// fold lowercases r and strips Spanish diacritics (ñ is kept: "año" != "ano").
func fold(r rune) rune {
	if r < utf8.RuneSelf {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}
	r = unicode.ToLower(r)
	switch r {
	case 'á', 'à', 'â', 'ä', 'ã':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'ö', 'õ':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	}
	return r
}

// foldString folds a whole string (no stemming).
func foldString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(fold(r))
	}
	return b.String()
}

// stem applies the deliberately conservative plural normalisation used for
// keys. It works on a folded word of n runes and returns the new byte length.
//
//	...iones -> ...ion   (elecciones -> eleccion, decisiones -> decision)
//	...ores  -> ...or    (trabajadores -> trabajador)
//	...ades  -> ...ad    (ciudades -> ciudad)
//	...ones  -> ...on    (millones -> millon)
//	...os/as -> ...o/a   (incendios -> incendio, casas -> casa)
//
// Only words longer than 5 runes are touched, so "crisis", "tesis", "lunes",
// "virus", "paris", "gas" and friends are never mangled; acronyms and tokens
// with digits are skipped by the caller.
func stem(w []byte, nRunes int) int {
	n := len(w)
	if nRunes <= 5 {
		return n
	}
	has := func(suf string) bool { return n >= len(suf) && string(w[n-len(suf):]) == suf }
	switch {
	case has("iones"), has("ores"), has("ades"), has("ones"):
		return n - 2
	case has("os"), has("as"):
		return n - 1
	}
	return n
}

// tokenizer is a reusable scratch area; not safe for concurrent use.
type tokenizer struct {
	stop   map[string]struct{}
	keyBuf []byte
	toks   []token
	cur    string // text of the last tokenize call (after rewriting)
}

func (z *tokenizer) isStop(b []byte) bool {
	_, ok := z.stop[string(b)]
	return ok
}

// sentenceEnd are separators after which a capital letter is not
// informative about proper nouns.
func sentenceEnd(r rune) bool {
	switch r {
	case '.', '?', '!', ':', '…', '"', '“', '”', '«', '»', '(', '—', '–', '¿', '¡', '|', '\n':
		return true
	}
	return false
}

func softSep(r rune) bool {
	switch r {
	case ' ', '\t', ' ', '-', '\'', '’', '\r':
		return true
	}
	return false
}

// tokenize splits text into tokens (appending nothing: z.toks is reset).
// allCaps reports whether the text has no lowercase letters.
func (z *tokenizer) tokenize(text string) (allCaps bool) {
	z.toks = z.toks[:0]
	z.keyBuf = z.keyBuf[:0]
	if strings.Contains(text, "EE.UU") {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "EE.UU.", "EEUU"), "EE.UU", "EEUU")
	}
	// NOTE: offsets refer to the (possibly rewritten) text; callers use
	// z.text() for substrings.
	z.cur = text
	var (
		inTok                bool
		start                int
		kOff                 int
		nRunes, nUp, nLo, nD int
		first                rune
		hard, sent           = false, true
		anyLower             bool
	)
	flush := func(end int) {
		kLen := len(z.keyBuf) - kOff
		key := z.keyBuf[kOff:]
		var fl uint16
		if hard {
			fl |= fHard
		}
		if sent {
			fl |= fSent
		}
		isAcr := nLo == 0 && nUp >= 1 && nRunes >= 2
		if nD == nRunes {
			fl |= fNum | fStop
		}
		if unicode.IsUpper(first) || isAcr {
			fl |= fCap
		}
		if isAcr {
			fl |= fAcr
		}
		stopped := z.isStop(key)
		if connectors[string(key)] {
			fl |= fConn
		}
		if stopped {
			fl |= fStop
		} else if !isAcr && nD == 0 {
			nk := stem(key, nRunes)
			z.keyBuf = z.keyBuf[:kOff+nk]
			kLen = nk
			if z.isStop(z.keyBuf[kOff:]) {
				fl |= fStop
			}
		}
		if fl&fStop == 0 && (nRunes >= 3 || (isAcr && nRunes >= 2)) {
			fl |= fLong
		}
		z.toks = append(z.toks, token{start: int32(start), end: int32(end), kOff: int32(kOff), kLen: int32(kLen), flags: fl})
		hard, sent = false, false
		inTok = false
	}
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inTok {
				inTok = true
				start = i
				kOff = len(z.keyBuf)
				nRunes, nUp, nLo, nD = 0, 0, 0, 0
				first = r
			}
			nRunes++
			switch {
			case unicode.IsDigit(r):
				nD++
			case unicode.IsUpper(r):
				nUp++
			default:
				nLo++
				anyLower = true
			}
			var tmp [4]byte
			n := utf8.EncodeRune(tmp[:], fold(r))
			z.keyBuf = append(z.keyBuf, tmp[:n]...)
			continue
		}
		if inTok {
			flush(i)
		}
		if !softSep(r) {
			hard = true
			if sentenceEnd(r) {
				sent = true
			}
		}
	}
	if inTok {
		flush(len(text))
	}
	return !anyLower
}

func (z *tokenizer) key(t token) []byte { return z.keyBuf[t.kOff : t.kOff+t.kLen] }

// canonKey converts a user-supplied term (display form or key) to the key
// format: folded, stemmed per token, joined by single spaces. Stopword
// connectors are kept.
func (z *tokenizer) canonKey(s string) string {
	var out []byte
	for _, f := range splitWords(s) {
		w := []byte(foldString(f))
		nr := utf8.RuneCount(w)
		isAcr := true
		for _, r := range f {
			if unicode.IsLower(r) || unicode.IsDigit(r) {
				isAcr = false
				break
			}
		}
		if !isAcr || nr < 2 {
			// keys given back to us are already lowercase: stem unless digits
			if !strings.ContainsAny(f, "0123456789") && !(nr >= 2 && isAllUpperKey(f)) {
				w = w[:stem(w, nr)]
			}
		}
		if len(out) > 0 {
			out = append(out, ' ')
		}
		out = append(out, w...)
	}
	return string(out)
}

func isAllUpperKey(f string) bool {
	hasLetter := false
	for _, r := range f {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	return hasLetter
}

func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
