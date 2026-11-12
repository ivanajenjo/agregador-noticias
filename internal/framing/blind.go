package framing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// BlockCoverage is how many outlets of a block covered a story.
type BlockCoverage struct {
	Block   Block
	Covered int      // outlets of the block that published about it
	Total   int      // outlets of the block (covering or not)
	Rate    float64  // Covered/Total
	Names   []string // covering outlets
	Missing []string // outlets of the block not detected covering it
}

// StoryCoverage is the coverage matrix of one story by block.
type StoryCoverage struct {
	Story     domain.Story
	Blocks    []BlockCoverage
	Covered   int     // distinct outlets (all leanings)
	Skew      float64 // share of the size-normalised coverage held by the dominant block
	Dominant  int     // index of the dominant block, -1 if none
	Absent    []int   // assessed blocks with 0 covering outlets
	Assessed  bool    // enough blocks with enough outlets to judge
	Blind     bool    // matches the blind-spot rule
	Summary   string  // "Cubierta por 5 de 7 medios de Derecha y 0 de 6 de Izquierda"
	Statement string  // "No aparece en nuestras fuentes de Izquierda ni Centro."
}

// coverageOf builds the matrix and applies the blind-spot rule.
func coverageOf(cfg Config, st domain.Story) StoryCoverage {
	sc := StoryCoverage{Story: st, Dominant: -1, Blocks: make([]BlockCoverage, len(cfg.Blocks)), Covered: st.Sources}
	for i, b := range cfg.Blocks {
		sc.Blocks[i].Block = b
	}
	for _, c := range st.Coverage {
		if i := cfg.blockOf(c.Leaning); i >= 0 {
			bc := &sc.Blocks[i]
			bc.Covered++
			bc.Total++
			bc.Names = append(bc.Names, firstNonEmpty(c.Name, c.SourceID))
		}
	}
	for _, c := range st.Missing {
		if i := cfg.blockOf(c.Leaning); i >= 0 {
			bc := &sc.Blocks[i]
			bc.Total++
			bc.Missing = append(bc.Missing, firstNonEmpty(c.Name, c.SourceID))
		}
	}
	var sumRate float64
	assessed := 0
	for i := range sc.Blocks {
		bc := &sc.Blocks[i]
		sort.Strings(bc.Names)
		sort.Strings(bc.Missing)
		if bc.Total > 0 {
			bc.Rate = float64(bc.Covered) / float64(bc.Total)
		}
		if bc.Total >= cfg.MinBlockOutlets {
			assessed++
			sumRate += bc.Rate
			if bc.Covered == 0 {
				sc.Absent = append(sc.Absent, i)
			}
		}
	}
	sc.Assessed = assessed >= 2 && st.Sources >= cfg.MinCovered
	if sumRate > 0 {
		best := -1
		for i, bc := range sc.Blocks {
			if bc.Total >= cfg.MinBlockOutlets && (best < 0 || bc.Rate > sc.Blocks[best].Rate) {
				best = i
			}
		}
		sc.Dominant = best
		sc.Skew = sc.Blocks[best].Rate / sumRate
	}
	if sc.Assessed && sc.Dominant >= 0 && sc.Skew >= cfg.MinSkew && sc.Blocks[sc.Dominant].Rate >= cfg.MinDominantRate {
		sc.Blind = true
	}
	sc.Summary, sc.Statement = describe(cfg, sc)
	return sc
}

func describe(cfg Config, sc StoryCoverage) (summary, statement string) {
	var bs []BlockCoverage
	for _, bc := range sc.Blocks {
		if bc.Total >= cfg.MinBlockOutlets || bc.Covered > 0 {
			bs = append(bs, bc)
		}
	}
	if len(bs) == 0 {
		return "", ""
	}
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].Covered > bs[j].Covered })
	var parts []string
	for i, bc := range bs {
		if i == 0 {
			parts = append(parts, fmt.Sprintf("%d %s de %s", bc.Covered, plural(bc.Covered), bc.Block.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%d de %s", bc.Covered, bc.Block.Name))
		}
	}
	summary = "Cubierta por " + parts[0]
	if len(parts) > 1 {
		summary += ", " + strings.Join(parts[1:len(parts)-1], ", ")
		if len(parts) > 2 {
			summary += " y "
		} else {
			summary = strings.TrimSuffix(summary, ", ") + " y "
		}
		summary += parts[len(parts)-1]
	}
	if !sc.Assessed {
		return summary, ""
	}
	var names []string
	for _, i := range sc.Absent {
		names = append(names, sc.Blocks[i].Block.Name)
	}
	switch len(names) {
	case 0:
	case 1:
		statement = "No aparece en nuestras fuentes de " + names[0] + "."
	default:
		statement = "No aparece en nuestras fuentes de " + strings.Join(names[:len(names)-1], ", ") + " ni " + names[len(names)-1] + "."
	}
	if statement == "" && sc.Blind {
		statement = "Aparece mucho menos en el resto de bloques que en " + sc.Blocks[sc.Dominant].Block.Name + "."
	}
	return summary, statement
}

// BlindReport is the coverage matrix of the window's stories.
type BlindReport struct {
	Blocks []Block
	Spots  []StoryCoverage // blind-spot candidates, most skewed first
	All    []StoryCoverage // every story, in the provider's order
	Stats  struct{ Stories, Assessed int }
}

// BlindSpots lists the stories whose coverage is concentrated in one block.
// The claim is deliberately weak: "not detected in our sources".
func (s *Service) BlindSpots(ctx context.Context, o domain.TrendOptions) (*BlindReport, error) {
	if s.Trends == nil {
		return nil, fmt.Errorf("framing: trends not available")
	}
	cfg := s.Cfg.withDefaults()
	stories, err := s.Trends.Stories(ctx, s.Options(o))
	if err != nil {
		return nil, err
	}
	r := &BlindReport{Blocks: cfg.Blocks}
	for _, st := range stories {
		sc := coverageOf(cfg, st)
		r.All = append(r.All, sc)
		r.Stats.Stories++
		if sc.Assessed {
			r.Stats.Assessed++
		}
		if sc.Blind {
			r.Spots = append(r.Spots, sc)
		}
	}
	sort.SliceStable(r.Spots, func(i, j int) bool {
		a, b := r.Spots[i], r.Spots[j]
		if a.Covered != b.Covered {
			return a.Covered > b.Covered
		}
		return a.Skew > b.Skew
	})
	return r, nil
}

func plural(n int) string {
	if n == 1 {
		return "medio"
	}
	return "medios"
}
