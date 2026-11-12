package annotate

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Result is a validated model answer. A field that was missing or outside its
// vocabulary is left empty (and listed in Problems) rather than guessed.
type Result struct {
	Tone       string
	Intent     string
	Frame      string
	Confidence float64 // 0..1; 0 when absent or unparsable
	Rationale  string  // <= 120 runes
	IsOpinion  *bool   // nil when absent
	Problems   []string
	Raw        string // model output as received
}

// OK reports whether at least one label survived validation.
func (r Result) OK() bool { return r.Tone != "" || r.Intent != "" || r.Frame != "" }

var (
	thinkRe  = regexp.MustCompile(`(?is)<think(?:ing)?>.*?</think(?:ing)?>`)
	fenceRe  = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*(.*?)```")
	errNoObj = errors.New("sin objeto JSON")
)

// Parse extracts and validates the JSON object from raw model output. It
// tolerates <think> blocks (also unclosed or with only the closing tag), code
// fences and text before/after the object.
func Parse(raw string) (Result, error) {
	res := Result{Raw: raw}
	s := raw
	s = thinkRe.ReplaceAllString(s, "")
	if i := strings.LastIndex(strings.ToLower(s), "</think>"); i >= 0 { // closing tag only
		s = s[i+len("</think>"):]
	}
	if i := strings.Index(strings.ToLower(s), "<think>"); i >= 0 { // never closed
		s = s[:i]
	}
	if m := fenceRe.FindStringSubmatch(s); m != nil && strings.Contains(m[1], "{") {
		s = m[1]
	}
	obj, ok := firstObject(s)
	if !ok {
		return res, errNoObj
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(obj), &m); err != nil {
		return res, err
	}
	get := func(keys ...string) (json.RawMessage, bool) {
		for k, v := range m {
			for _, want := range keys {
				if strings.EqualFold(k, want) {
					return v, true
				}
			}
		}
		return nil, false
	}
	str := func(key string, vocab []string) string {
		v, ok := get(key)
		if !ok {
			res.Problems = append(res.Problems, key+": ausente")
			return ""
		}
		var x string
		if json.Unmarshal(v, &x) != nil {
			res.Problems = append(res.Problems, key+": no es texto")
			return ""
		}
		if l, ok := normalizeLabel(x, vocab); ok {
			return l
		}
		res.Problems = append(res.Problems, key+": valor fuera de vocabulario")
		return ""
	}
	res.Tone = str("tone", Tones)
	res.Intent = str("intent", Intents)
	res.Frame = str("frame", Frames)
	if v, ok := get("confidence"); ok {
		res.Confidence = parseConfidence(v)
	}
	if v, ok := get("rationale"); ok {
		var x string
		if json.Unmarshal(v, &x) == nil {
			res.Rationale = truncRunes(strings.Join(strings.Fields(x), " "), 120)
		}
	}
	if v, ok := get("is_opinion"); ok {
		res.IsOpinion = parseBool(v)
	}
	return res, nil
}

// firstObject returns the first balanced {...} (string-aware).
func firstObject(s string) (string, bool) {
	for start := strings.IndexByte(s, '{'); start >= 0; {
		depth, inStr, esc := 0, false, false
		for i := start; i < len(s); i++ {
			c := s[i]
			switch {
			case inStr:
				if esc {
					esc = false
				} else if c == '\\' {
					esc = true
				} else if c == '"' {
					inStr = false
				}
			case c == '"':
				inStr = true
			case c == '{':
				depth++
			case c == '}':
				depth--
				if depth == 0 {
					cand := s[start : i+1]
					if json.Valid([]byte(cand)) {
						return cand, true
					}
					goto next
				}
			}
		}
		return "", false
	next:
		n := strings.IndexByte(s[start+1:], '{')
		if n < 0 {
			return "", false
		}
		start += 1 + n
	}
	return "", false
}

func fold(s string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u")
	return r.Replace(strings.Join(strings.Fields(strings.ToLower(s)), " "))
}

// normalizeLabel maps x onto the vocabulary ignoring case and accents.
func normalizeLabel(x string, vocab []string) (string, bool) {
	f := fold(strings.Trim(x, " .\"'"))
	for _, v := range vocab {
		if fold(v) == f {
			return v, true
		}
	}
	return "", false
}

func parseConfidence(v json.RawMessage) float64 {
	var f float64
	if json.Unmarshal(v, &f) != nil {
		var s string
		if json.Unmarshal(v, &s) != nil {
			return 0
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
		x, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		if err != nil {
			return 0
		}
		f = x
	}
	if f > 1 && f <= 100 { // given as a percentage
		f /= 100
	}
	if f < 0 || f > 1 || f != f {
		return 0
	}
	return f
}

func parseBool(v json.RawMessage) *bool {
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return &b
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		switch fold(s) {
		case "true", "si", "yes":
			t := true
			return &t
		case "false", "no":
			f := false
			return &f
		}
	}
	return nil
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
