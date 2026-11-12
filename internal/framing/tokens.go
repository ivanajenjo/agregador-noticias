package framing

import (
	"strings"
	"unicode"
)

// stopwords: Spanish function words and newsroom boilerplate that carry no
// framing signal. Kept small on purpose; the log-odds prior already shrinks
// words that are common in the background corpus.
var stopwords = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, w := range strings.Fields(`a al algo algunas algunos ante antes aqui asi aun aunque bajo cada casi como con contra cual cuando de del desde donde dos e el ella ellas ello ellos en entre era eran es esa esas ese eso esos esta estaba estan estar este esto estos fue fueron ha habia han hasta hay la las le les lo los mas me mi mis mucho muy ni no nos nuestro o otra otras otro otros para pero poco por porque que quien se sea segun ser si sido sin sobre solo son su sus tambien tanto te tiene tienen todo todos tras tu tus un una unas uno unos y ya yo
	tras ante hace hacen hoy ayer segun asegura dice dijo tras sera seran podria podra puede pueden debe deben
	tiene dice video fotos directo ultima hora`) {
		m[w] = struct{}{}
	}
	return m
}()

// fold lowercases and strips Spanish diacritics (keeps ñ).
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isStop(key string) bool { _, ok := stopwords[key]; return ok }

// wordKey normalises one display token (surrounding punctuation is dropped).
func wordKey(tok string) string {
	return fold(strings.TrimFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
}

// terms splits text into (key, display) word pairs for vocabulary analysis:
// letters only, >= 3 runes, not a stopword. Display is the lowercase form.
func terms(text string) (keys, disp []string) {
	f := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range f {
		low := strings.ToLower(w)
		k := fold(low)
		if len([]rune(k)) < 3 || isStop(k) || strings.IndexFunc(k, unicode.IsLetter) < 0 {
			continue
		}
		keys = append(keys, k)
		disp = append(disp, low)
	}
	return
}
