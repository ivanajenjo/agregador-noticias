package trends

// Unigram statistics used to decide whether a sentence-initial capitalised
// word is part of a proper name.
const (
	statNone uint8 = iota
	statCapMid
	statLowMid
)

// cand is a term candidate emitted for one article. Byte slices are only
// valid during the emit call.
type cand struct {
	key  []byte
	disp string // original text form
	kind uint8
	sent bool  // display comes from a sentence start (capitalisation unreliable)
	caps bool  // display comes from an all-caps/title-case text
	stat uint8 // statCapMid / statLowMid for unigrams

	// Pending entity: a capitalised run that starts at a sentence start. It
	// is resolved after the whole corpus has been seen: if the first token
	// is a known proper noun the full key is kept, otherwise restKey (the
	// run without its first token, may be empty) replaces it.
	pend     bool
	restKey  []byte
	restDisp string
	firstKey []byte
}

type extractor struct {
	z      tokenizer
	phrase []byte
	rest   []byte
}

func newExtractor(stop map[string]struct{}) *extractor {
	return &extractor{z: tokenizer{stop: stop}}
}

// extract emits the candidates of one article: unigrams (title+summary),
// bigrams/trigrams of consecutive content words (title only) and entities
// (capitalised runs; title+summary). The caller dedups per article.
func (x *extractor) extract(title, summary string, emit func(*cand)) {
	x.gen(title, true, emit)
	if summary != "" {
		x.gen(summary, false, emit)
	}
}

func (x *extractor) gen(text string, isTitle bool, emit func(*cand)) {
	z := &x.z
	allCaps := z.tokenize(text)
	toks := z.toks
	text = z.cur
	n := len(toks)
	if n == 0 {
		return
	}
	// Title-case / ALL-CAPS texts carry no capitalisation signal.
	noCaps := allCaps
	if !noCaps {
		letters, capd := 0, 0
		for _, t := range toks {
			if t.flags&fNum != 0 {
				continue
			}
			letters++
			if t.flags&fCap != 0 {
				capd++
			}
		}
		if letters >= 5 && capd*4 >= letters*3 {
			noCaps = true
		}
	}

	var c cand
	// unigrams
	for i := range toks {
		t := toks[i]
		if t.flags&fLong == 0 {
			continue
		}
		c = cand{key: z.key(t), disp: text[t.start:t.end], kind: kindWord, sent: t.flags&fSent != 0, caps: noCaps}
		if !noCaps && t.flags&fSent == 0 {
			if t.flags&fCap != 0 {
				c.stat = statCapMid
			} else {
				c.stat = statLowMid
			}
		}
		emit(&c)
	}
	// n-grams (title only)
	if isTitle {
		for i := 0; i+1 < n; i++ {
			if toks[i].flags&fLong == 0 {
				continue
			}
			for l := 2; l <= 3 && i+l <= n; l++ {
				last := toks[i+l-1]
				if last.flags&fLong == 0 || last.flags&fHard != 0 {
					break
				}
				x.phrase = x.phrase[:0]
				for k := i; k < i+l; k++ {
					if k > i {
						x.phrase = append(x.phrase, ' ')
					}
					x.phrase = append(x.phrase, z.key(toks[k])...)
				}
				c = cand{key: x.phrase, disp: text[toks[i].start:last.end], kind: kindPhrase, caps: noCaps, sent: toks[i].flags&fSent != 0}
				emit(&c)
			}
		}
	}
	if noCaps {
		return
	}
	// entities
	part := func(k int) bool {
		return k < n && toks[k].flags&fCap != 0 && toks[k].flags&fLong != 0
	}
	for i := 0; i < n; {
		if !part(i) {
			i++
			continue
		}
		first := i
		last := i
		nparts := 1
		for {
			k := last + 1
			if k >= n || toks[k].flags&fHard != 0 {
				break
			}
			if part(k) {
				last = k
				nparts++
				continue
			}
			kk := string(z.key(toks[k]))
			if (kk == "de" || kk == "del") && k+1 < n && toks[k+1].flags&fHard == 0 {
				if part(k + 1) {
					last = k + 1
					nparts++
					continue
				}
				m := string(z.key(toks[k+1]))
				if kk == "de" && (m == "la" || m == "las" || m == "los" || m == "el") && part(k+2) && toks[k+2].flags&fHard == 0 {
					last = k + 2
					nparts++
					continue
				}
			}
			break
		}
		i = last + 1
		if nparts < 2 || nparts > 5 {
			continue
		}
		x.phrase = x.phrase[:0]
		for k := first; k <= last; k++ {
			if k > first {
				x.phrase = append(x.phrase, ' ')
			}
			x.phrase = append(x.phrase, z.key(toks[k])...)
		}
		c = cand{key: x.phrase, disp: text[toks[first].start:toks[last].end], kind: kindEntity}
		if toks[first].flags&fSent != 0 {
			// find the second part to build the "rest" run
			x.rest = x.rest[:0]
			restStart := -1
			for k := first + 1; k <= last; k++ {
				if part(k) {
					restStart = k
					break
				}
			}
			c.pend = true
			c.firstKey = z.key(toks[first])
			if nparts-1 >= 2 && restStart >= 0 {
				for k := restStart; k <= last; k++ {
					if k > restStart {
						x.rest = append(x.rest, ' ')
					}
					x.rest = append(x.rest, z.key(toks[k])...)
				}
				c.restKey = x.rest
				c.restDisp = text[toks[restStart].start:toks[last].end]
			}
			c.sent = true
		}
		emit(&c)
	}
}
