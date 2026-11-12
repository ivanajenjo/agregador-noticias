package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

var accentFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ß", "ss",
)

// foldWord lowercases and strips accents.
func fold(s string) string { return accentFold.Replace(strings.ToLower(s)) }

// normalizeTitle lowercases, strips accents and punctuation, and collapses
// whitespace; it is the basis of the (source, title) dedup key.
func normalizeTitle(s string) string {
	s = fold(s)
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func titleHash(title string) string {
	h := sha256.Sum256([]byte(normalizeTitle(title)))
	return hex.EncodeToString(h[:16])
}
