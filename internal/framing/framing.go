// Package framing compares how outlets of different editorial leanings cover
// the same story, with no LLM: distinctive vocabulary per leaning block
// (log-odds ratio with an informative prior), word-level headline diffs and
// "blind spot" detection. Everything it shows is descriptive: it never
// attributes intent and its leaning labels are the user-editable ones from
// sources.yaml.
package framing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Block is a group of leaning ranks compared as one side (0 izquierda,
// 1 centro-izquierda, 2 centro, 3 centro-derecha, 4 derecha).
type Block struct {
	Key   string
	Name  string
	Ranks []int
}

// Blocks3 is the default grouping.
var Blocks3 = []Block{
	{"izquierda", "Izquierda", []int{0, 1}},
	{"centro", "Centro", []int{2}},
	{"derecha", "Derecha", []int{3, 4}},
}

// Blocks5 keeps every leaning apart.
var Blocks5 = []Block{
	{"izquierda", "Izquierda", []int{0}},
	{"centro-izquierda", "Centro-izquierda", []int{1}},
	{"centro", "Centro", []int{2}},
	{"centro-derecha", "Centro-derecha", []int{3}},
	{"derecha", "Derecha", []int{4}},
}

// Config holds every threshold; zero values get the defaults of
// DefaultConfig.
type Config struct {
	Blocks        []Block
	MinTermCount  int     // min articles of the block containing a term (default 2)
	TopTerms      int     // terms listed per block (default 10)
	MinZ          float64 // min z-score to list a term (default 1.0)
	PriorStrength float64 // alpha0 of the Dirichlet prior (default 20)
	MaxBackground int     // background articles used (default 6000)

	// Blind spots
	MinCovered      int     // min distinct covering outlets (default 3)
	MinBlockOutlets int     // a block needs this many outlets to be assessed (default 2)
	MinSkew         float64 // share of the (size-normalised) coverage in one block (default 0.8)
	MinDominantRate float64 // fraction of that block's outlets covering it (default 0.2)
	MaxStories      int     // stories examined (default 100)
}

// DefaultConfig returns the default thresholds.
func DefaultConfig() Config {
	return Config{Blocks: Blocks3, MinTermCount: 2, TopTerms: 10, MinZ: 1.0, PriorStrength: 20, MaxBackground: 6000,
		MinCovered: 3, MinBlockOutlets: 2, MinSkew: 0.8, MinDominantRate: 0.2, MaxStories: 100}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if len(c.Blocks) == 0 {
		c.Blocks = d.Blocks
	}
	if c.MinTermCount <= 0 {
		c.MinTermCount = d.MinTermCount
	}
	if c.TopTerms <= 0 {
		c.TopTerms = d.TopTerms
	}
	if c.MinZ <= 0 {
		c.MinZ = d.MinZ
	}
	if c.PriorStrength <= 0 {
		c.PriorStrength = d.PriorStrength
	}
	if c.MaxBackground <= 0 {
		c.MaxBackground = d.MaxBackground
	}
	if c.MinCovered <= 0 {
		c.MinCovered = d.MinCovered
	}
	if c.MinBlockOutlets <= 0 {
		c.MinBlockOutlets = d.MinBlockOutlets
	}
	if c.MinSkew <= 0 {
		c.MinSkew = d.MinSkew
	}
	if c.MinDominantRate <= 0 {
		c.MinDominantRate = d.MinDominantRate
	}
	if c.MaxStories <= 0 {
		c.MaxStories = d.MaxStories
	}
	return c
}

// ErrNotFound: the story is not in the analysed window.
var ErrNotFound = errors.New("story not found")

// Service computes framing and blind-spot reports.
type Service struct {
	Trends domain.TrendProvider
	Detail domain.StoryDetailProvider // needed by Framing only
	Cfg    Config
}

// New builds a Service. Detail is taken from trends when it implements
// domain.StoryDetailProvider.
func New(t domain.TrendProvider, cfg Config) *Service {
	s := &Service{Trends: t, Cfg: cfg.withDefaults()}
	if d, ok := t.(domain.StoryDetailProvider); ok {
		s.Detail = d
	}
	return s
}

// Options normalises the trend options so every call hits the same cache.
func (s *Service) Options(o domain.TrendOptions) domain.TrendOptions {
	o.Limit = s.Cfg.withDefaults().MaxStories
	return o
}

// Rank maps a free-form leaning label to 0..4 (izquierda..derecha), or 5.
func Rank(l string) int {
	l = strings.ToLower(l)
	switch {
	case strings.Contains(l, "centro") && strings.Contains(l, "izq"):
		return 1
	case strings.Contains(l, "centro") && strings.Contains(l, "der"):
		return 3
	case strings.Contains(l, "izq") || strings.Contains(l, "progres"):
		return 0
	case strings.Contains(l, "der") || strings.Contains(l, "conserv"):
		return 4
	case strings.Contains(l, "centro") || strings.Contains(l, "liberal"):
		return 2
	}
	return 5
}

// blockOf returns the index of the block holding the leaning, or -1.
func (c Config) blockOf(leaning string) int {
	r := Rank(leaning)
	for i, b := range c.Blocks {
		for _, x := range b.Ranks {
			if x == r {
				return i
			}
		}
	}
	return -1
}

// ---- framing --------------------------------------------------------------

// Term is a distinctive word of a block.
type Term struct {
	Text  string
	Z     float64
	Count int // articles of the block containing it
	Other int // articles of the other blocks containing it
}

// BlockFraming is the vocabulary of one block in a story.
type BlockFraming struct {
	Block    Block
	Articles int
	Outlets  int
	Terms    []Term
}

// HeadlineRow is one outlet's headline with word-level highlighting.
type HeadlineRow struct {
	SourceID  string
	Name      string
	Leaning   string
	Block     int // index into Framing.Blocks, -1 = unclassified
	BlockName string
	Title     string
	URL       string
	At        time.Time
	Words     []Word
	Omitted   []string
	More      int  // further headlines of the outlet in the story
	Consensus bool // this is the consensus headline
}

// Framing is the full comparison of a story.
type Framing struct {
	Story      domain.Story
	Blocks     []BlockFraming
	Headlines  []HeadlineRow
	Consensus  string
	Coverage   StoryCoverage
	Articles   int
	Background int
	Note       string // why part of the analysis is missing, if so
}

// Framing analyses one story. o must be the options the page uses to list
// stories (window, sources, Now).
func (s *Service) Framing(ctx context.Context, storyID string, o domain.TrendOptions) (*Framing, error) {
	if s.Trends == nil || s.Detail == nil {
		return nil, errors.New("framing: story detail provider not available")
	}
	cfg := s.Cfg.withDefaults()
	o = s.Options(o)
	stories, err := s.Trends.Stories(ctx, o)
	if err != nil {
		return nil, err
	}
	var st *domain.Story
	for i := range stories {
		if stories[i].ID == storyID {
			st = &stories[i]
			break
		}
	}
	if st == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, storyID)
	}
	arts, err := s.Detail.StoryArticles(ctx, storyID, o)
	if err != nil {
		return nil, err
	}
	win, err := s.Detail.WindowArticles(ctx, o)
	if err != nil {
		return nil, err
	}
	f := &Framing{Story: *st, Articles: len(arts), Coverage: coverageOf(cfg, *st)}

	leanOf := map[string]string{}
	nameOf := map[string]string{}
	for _, c := range st.Coverage {
		leanOf[c.SourceID], nameOf[c.SourceID] = c.Leaning, c.Name
	}
	blockOfSrc := func(id string) int {
		l, ok := leanOf[id]
		if !ok {
			return -1
		}
		return cfg.blockOf(l)
	}

	// vocabulary per article (binary doc counts) and per block
	type doc struct {
		keys  map[string]struct{}
		block int
	}
	display := map[string]map[string]int{}
	docs := make([]doc, 0, len(arts))
	inStory := map[int64]bool{}
	blockArts := make([]int, len(cfg.Blocks))
	blockSrc := make([]map[string]bool, len(cfg.Blocks))
	for i := range blockSrc {
		blockSrc[i] = map[string]bool{}
	}
	for _, a := range arts {
		inStory[a.ID] = true
		b := blockOfSrc(a.SourceID)
		d := doc{keys: map[string]struct{}{}, block: b}
		ks, ds := terms(a.Title + ". " + a.Summary)
		for i, k := range ks {
			d.keys[k] = struct{}{}
			m := display[k]
			if m == nil {
				m = map[string]int{}
				display[k] = m
			}
			m[ds[i]]++
		}
		docs = append(docs, d)
		if b >= 0 {
			blockArts[b]++
			blockSrc[b][a.SourceID] = true
		}
	}
	// background: other articles of the window
	bg := map[string]float64{}
	nbg := 0
	step := 1
	if len(win) > cfg.MaxBackground {
		step = len(win)/cfg.MaxBackground + 1
	}
	for i := 0; i < len(win); i += step {
		a := win[i]
		if inStory[a.ID] {
			continue
		}
		nbg++
		ks, _ := terms(a.Title + ". " + a.Summary)
		seen := map[string]struct{}{}
		for _, k := range ks {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				bg[k]++
			}
		}
	}
	f.Background = nbg
	vocab := map[string]struct{}{}
	for k := range display {
		vocab[k] = struct{}{}
	}
	prior := priorFrom(vocab, bg, cfg.PriorStrength)

	active := 0
	for _, n := range blockArts {
		if n > 0 {
			active++
		}
	}
	if active < 2 {
		f.Note = "Solo hay artículos de un bloque editorial en esta historia: no hay con qué contrastar el vocabulario."
	}
	for bi, b := range cfg.Blocks {
		bf := BlockFraming{Block: b, Articles: blockArts[bi], Outlets: len(blockSrc[bi])}
		if active >= 2 && blockArts[bi] > 0 {
			y1, y2 := map[string]float64{}, map[string]float64{}
			c1, c2 := map[string]int{}, map[string]int{}
			for _, d := range docs {
				if d.block < 0 {
					continue
				}
				for k := range d.keys {
					if d.block == bi {
						y1[k]++
						c1[k]++
					} else {
						y2[k]++
						c2[k]++
					}
				}
			}
			minCount := cfg.MinTermCount
			if blockArts[bi] < 3 {
				minCount = 1
			}
			z := ZScores(y1, y2, prior)
			for _, sc := range topByZ(z, func(w string) bool { return c1[w] >= minCount }, cfg.MinZ, cfg.TopTerms) {
				bf.Terms = append(bf.Terms, Term{Text: bestDisplay(display[sc.key]), Z: sc.z, Count: c1[sc.key], Other: c2[sc.key]})
			}
		}
		f.Blocks = append(f.Blocks, bf)
	}

	// headlines: first headline of each outlet (earliest article)
	type pick struct {
		a    domain.ArticleLite
		more int
	}
	picks := map[string]*pick{}
	var order []string
	for _, a := range arts {
		p := picks[a.SourceID]
		if p == nil {
			picks[a.SourceID] = &pick{a: a}
			order = append(order, a.SourceID)
			continue
		}
		p.more++
		if a.At.Before(p.a.At) {
			p.a = a
		}
	}
	titles := make([]string, len(order))
	for i, id := range order {
		titles[i] = picks[id].a.Title
	}
	hd := CompareHeadlines(titles)
	f.Consensus = hd.Consensus
	for i, id := range order {
		p := picks[id]
		bi := blockOfSrc(id)
		row := HeadlineRow{SourceID: id, Name: firstNonEmpty(nameOf[id], id), Leaning: leanOf[id], Block: bi, Title: p.a.Title, URL: p.a.URL,
			At: p.a.At, Words: hd.Words[i], Omitted: hd.Omitted[i], More: p.more, Consensus: i == hd.ConsensusIx}
		if bi >= 0 {
			row.BlockName = cfg.Blocks[bi].Name
		}
		f.Headlines = append(f.Headlines, row)
	}
	sort.SliceStable(f.Headlines, func(i, j int) bool {
		a, b := f.Headlines[i], f.Headlines[j]
		ba, bb := a.Block, b.Block
		if ba < 0 {
			ba = len(cfg.Blocks)
		}
		if bb < 0 {
			bb = len(cfg.Blocks)
		}
		if ba != bb {
			return ba < bb
		}
		if Rank(a.Leaning) != Rank(b.Leaning) {
			return Rank(a.Leaning) < Rank(b.Leaning)
		}
		return a.Name < b.Name
	})
	return f, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func bestDisplay(m map[string]int) string {
	best, bn := "", -1
	for d, n := range m {
		if n > bn || (n == bn && d < best) {
			best, bn = d, n
		}
	}
	return best
}
