package annotate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Sample is one hand-labelled example of the gold file.
type Sample struct {
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Tone      string `json:"tone"`
	Intent    string `json:"intent"`
	Frame     string `json:"frame"`
	IsOpinion *bool  `json:"is_opinion,omitempty"`
	Note      string `json:"note,omitempty"`
}

// LoadSamples reads a gold file: {"samples":[...]} or a bare array.
func LoadSamples(path string) ([]Sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Samples []Sample `json:"samples"`
	}
	if json.Unmarshal(b, &wrap) == nil && len(wrap.Samples) > 0 {
		return wrap.Samples, nil
	}
	var arr []Sample
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return arr, nil
}

// FieldReport is the agreement of one field with the gold labels.
type FieldReport struct {
	N         int                       `json:"n"` // samples where the model gave a valid value
	Correct   int                       `json:"correct"`
	Accuracy  float64                   `json:"accuracy"`  // Correct / labelled samples (invalid counts as wrong)
	Confusion map[string]map[string]int `json:"confusion"` // gold -> predicted -> count
}

// Report summarises an Evaluate run.
type Report struct {
	Total       int                    `json:"total"`
	Unusable    int                    `json:"unusable"` // no valid output at all
	Fields      map[string]FieldReport `json:"fields"`
	Consistency float64                `json:"consistency"` // agreement between repeated runs (1 = identical), 0 if Repeats<2
	ConfRight   float64                `json:"mean_confidence_right"`
	ConfWrong   float64                `json:"mean_confidence_wrong"`
}

// EvalOptions tune Evaluate.
type EvalOptions struct {
	Repeats int // answers per sample used for the consistency figure (default 1 = skip)
	Seed    *int
}

// String renders the report for humans.
func (r *Report) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "muestras: %d, sin salida utilizable: %d\n", r.Total, r.Unusable)
	names := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		f := r.Fields[k]
		fmt.Fprintf(&sb, "  %-8s exactitud %.0f%% (%d aciertos, %d con valor válido)\n", k, f.Accuracy*100, f.Correct, f.N)
	}
	if r.Consistency > 0 {
		fmt.Fprintf(&sb, "  consistencia entre repeticiones: %.0f%%\n", r.Consistency*100)
	}
	fmt.Fprintf(&sb, "  confianza media acierto %.2f / fallo %.2f (si no se separan, la confianza no sirve para filtrar)\n", r.ConfRight, r.ConfWrong)
	return sb.String()
}

// Evaluate runs the annotator over hand-labelled samples and reports
// accuracy per field, the confusion matrices, how well the model's confidence
// separates right from wrong answers, and (Repeats>1) consistency between
// repeated runs. It is the calibration utility for choosing a model and a
// minimum confidence; see TestEvaluateRealModel in evaluate_test.go for running it against a real model.
func Evaluate(ctx context.Context, c Completer, samples []Sample, o EvalOptions) (*Report, error) {
	an := &Annotator{C: c, Seed: o.Seed, Retries: 1}
	rep := &Report{Total: len(samples), Fields: map[string]FieldReport{}}
	for _, f := range []string{"tone", "intent", "frame", "opinion"} {
		rep.Fields[f] = FieldReport{Confusion: map[string]map[string]int{}}
	}
	var cr, cw float64
	var nr, nw, agree, pairs int
	for _, s := range samples {
		res, err := an.Annotate(ctx, Input{Title: s.Title, Summary: s.Summary})
		if err != nil {
			if IsOutage(err) {
				return rep, err
			}
			rep.Unusable++
		}
		op := ""
		if res.IsOpinion != nil {
			op = "no"
			if *res.IsOpinion {
				op = "sí"
			}
		}
		gop := ""
		if s.IsOpinion != nil {
			gop = "no"
			if *s.IsOpinion {
				gop = "sí"
			}
		}
		for _, p := range []struct{ name, gold, pred string }{
			{"tone", s.Tone, res.Tone}, {"intent", s.Intent, res.Intent}, {"frame", s.Frame, res.Frame}, {"opinion", gop, op},
		} {
			if p.gold == "" {
				continue
			}
			f := rep.Fields[p.name]
			if p.pred != "" {
				f.N++
			}
			if f.Confusion[p.gold] == nil {
				f.Confusion[p.gold] = map[string]int{}
			}
			f.Confusion[p.gold][p.pred]++
			if p.pred == p.gold {
				f.Correct++
				cr += res.Confidence
				nr++
			} else {
				cw += res.Confidence
				nw++
			}
			rep.Fields[p.name] = f
		}
		for i := 1; i < o.Repeats; i++ {
			again, err := an.Annotate(ctx, Input{Title: s.Title, Summary: s.Summary})
			if err != nil && IsOutage(err) {
				return rep, err
			}
			pairs++
			if again.Tone == res.Tone && again.Intent == res.Intent && again.Frame == res.Frame {
				agree++
			}
		}
	}
	for k, f := range rep.Fields {
		gold := 0
		for _, row := range f.Confusion {
			for _, n := range row {
				gold += n
			}
		}
		if gold > 0 {
			f.Accuracy = float64(f.Correct) / float64(gold)
		}
		rep.Fields[k] = f
	}
	if nr > 0 {
		rep.ConfRight = cr / float64(nr)
	}
	if nw > 0 {
		rep.ConfWrong = cw / float64(nw)
	}
	if pairs > 0 {
		rep.Consistency = float64(agree) / float64(pairs)
	}
	return rep, nil
}
