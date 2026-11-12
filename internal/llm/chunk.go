package llm

import (
	"strings"
	"unicode"
)

// ChunkText splits text into chunks of at most maxChars runes, preferring
// paragraph, then sentence, then word boundaries. Consecutive chunks share
// roughly `overlap` runes of trailing context (cut at a word boundary).
// Empty or blank text yields nil.
func ChunkText(text string, maxChars, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxChars <= 0 {
		return []string{text}
	}
	if overlap < 0 || overlap >= maxChars/2 {
		overlap = max(0, maxChars/5)
	}
	if len([]rune(text)) <= maxChars {
		return []string{text}
	}
	var units []string
	for _, p := range splitParagraphs(text) {
		if runeLen(p) <= maxChars {
			units = append(units, p)
			continue
		}
		for _, s := range splitSentences(p) {
			if runeLen(s) <= maxChars {
				units = append(units, s)
				continue
			}
			units = append(units, hardSplit(s, maxChars)...)
		}
	}
	var chunks []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
		}
		cur.Reset()
		curLen = 0
	}
	for _, u := range units {
		ul := runeLen(u)
		sep := 1
		if curLen == 0 {
			sep = 0
		}
		if curLen > 0 && curLen+sep+ul > maxChars {
			prev := cur.String()
			flush()
			if ov := tail(prev, overlap); ov != "" && runeLen(ov)+1+ul <= maxChars {
				cur.WriteString(ov)
				curLen = runeLen(ov)
				sep = 1
			} else {
				sep = 0
			}
		}
		if curLen > 0 {
			cur.WriteString("\n")
			curLen += sep
		}
		cur.WriteString(u)
		curLen += ul
	}
	flush()
	return chunks
}

func runeLen(s string) int { return len([]rune(s)) }

func splitParagraphs(t string) []string {
	var out []string
	for _, p := range strings.Split(t, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitSentences splits after . ! ? … followed by whitespace. Spanish
// inverted marks open a sentence and need no handling.
func splitSentences(p string) []string {
	rs := []rune(p)
	var out []string
	start := 0
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '.', '!', '?', '…':
			j := i + 1
			for j < len(rs) && (rs[j] == '"' || rs[j] == '»' || rs[j] == '”' || rs[j] == ')' || rs[j] == '.') {
				j++
			}
			if j >= len(rs) || unicode.IsSpace(rs[j]) {
				if s := strings.TrimSpace(string(rs[start:j])); s != "" {
					out = append(out, s)
				}
				start = j
				i = j - 1
			}
		}
	}
	if s := strings.TrimSpace(string(rs[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// hardSplit cuts s into pieces of at most n runes at spaces when possible.
func hardSplit(s string, n int) []string {
	rs := []rune(s)
	var out []string
	for len(rs) > n {
		cut := n
		for i := n; i > n/2; i-- {
			if unicode.IsSpace(rs[i]) {
				cut = i
				break
			}
		}
		if p := strings.TrimSpace(string(rs[:cut])); p != "" {
			out = append(out, p)
		}
		rs = []rune(strings.TrimSpace(string(rs[cut:])))
	}
	if len(rs) > 0 {
		out = append(out, string(rs))
	}
	return out
}

// tail returns about the last n runes of s starting at a word boundary.
func tail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return ""
	}
	rs = rs[len(rs)-n:]
	for i, r := range rs {
		if unicode.IsSpace(r) {
			return strings.TrimSpace(string(rs[i:]))
		}
	}
	return ""
}
