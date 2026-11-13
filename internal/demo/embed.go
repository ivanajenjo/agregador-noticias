package demo

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// EmbedModel / EmbedDim describe the fake embeddings stored by the generator:
// a 256-dimensional hashed bag of words, enough for the vector index to have
// something to read without any model. A fake OpenAI-compatible server that
// implements the same function (see docs/DEMO.md) makes hybrid search work.
const (
	EmbedModel = "nomic-embed-text"
	EmbedDim   = 256
)

var foldAccents = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

// Embed returns the L2-normalised hashed bag-of-words vector of text.
func Embed(text string) []float32 {
	v := make([]float32, EmbedDim)
	toks := strings.FieldsFunc(foldAccents.Replace(strings.ToLower(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, t := range toks {
		if len([]rune(t)) < 3 {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(t))
		v[h.Sum32()%EmbedDim]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n == 0 {
		v[0] = 1
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}
