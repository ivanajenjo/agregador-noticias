package annotate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const goldPath = "../../docs/anotaciones-gold.example.json"

func TestGoldFileIsValid(t *testing.T) {
	s, err := LoadSamples(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) < 20 {
		t.Fatalf("only %d samples", len(s))
	}
	in := func(v string, vocab []string) bool {
		for _, x := range vocab {
			if x == v {
				return true
			}
		}
		return false
	}
	for i, x := range s {
		if x.Title == "" || !in(x.Tone, Tones) || !in(x.Intent, Intents) || !in(x.Frame, Frames) || x.IsOpinion == nil {
			t.Errorf("sample %d invalid: %+v", i, x)
		}
	}
}

// oracle answers with the gold label of the headline it is shown.
func oracle(samples []Sample, wrong func(i int) bool) Completer {
	return completerFunc(func(_ context.Context, m []Message, _ CallOptions) (string, error) {
		for i, s := range samples {
			if strings.Contains(m[1].Content, "Titular: "+s.Title+"\n") {
				tone, conf := s.Tone, 0.9
				if wrong(i) {
					tone, conf = "neutral", 0.4
					if s.Tone == "neutral" {
						tone = "alarmista"
					}
				}
				b, _ := json.Marshal(map[string]any{"tone": tone, "intent": s.Intent, "frame": s.Frame, "confidence": conf, "rationale": "x", "is_opinion": *s.IsOpinion})
				return string(b), nil
			}
		}
		return "no sé", nil
	})
}

func TestEvaluateWithFakeModel(t *testing.T) {
	samples, err := LoadSamples(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Evaluate(context.Background(), oracle(samples, func(int) bool { return false }), samples, EvalOptions{Repeats: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"tone", "intent", "frame", "opinion"} {
		if rep.Fields[f].Accuracy != 1 {
			t.Errorf("%s accuracy %v", f, rep.Fields[f].Accuracy)
		}
	}
	if rep.Consistency != 1 || rep.Unusable != 0 || rep.ConfWrong != 0 {
		t.Fatalf("%+v", rep)
	}
	// every 4th tone wrong, with lower confidence
	rep, _ = Evaluate(context.Background(), oracle(samples, func(i int) bool { return i%4 == 0 }), samples, EvalOptions{})
	want := float64(len(samples)-(len(samples)+3)/4) / float64(len(samples))
	if got := rep.Fields["tone"].Accuracy; got != want {
		t.Fatalf("tone accuracy %v want %v", got, want)
	}
	if rep.ConfRight <= rep.ConfWrong || rep.Fields["intent"].Accuracy != 1 {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(rep.String(), "tone") {
		t.Fatal(rep.String())
	}
	// unusable model
	rep, _ = Evaluate(context.Background(), completerFunc(func(context.Context, []Message, CallOptions) (string, error) { return "???", nil }), samples[:3], EvalOptions{})
	if rep.Unusable != 3 || rep.Fields["tone"].Accuracy != 0 {
		t.Fatalf("%+v", rep)
	}
}

// TestEvaluateRealModel runs the gold file against a real model. It is skipped
// unless ANNOTATE_EVAL_URL and ANNOTATE_EVAL_MODEL are set:
//
//	ANNOTATE_EVAL_URL=http://mac.local:11434 ANNOTATE_EVAL_MODEL=qwen2.5:7b \
//	  go test ./internal/annotate -run RealModel -v
//
// Use the printed accuracy and the right/wrong confidence gap to choose a model
// and the minimum confidence used by the /anotaciones page.
func TestEvaluateRealModel(t *testing.T) {
	url, model := os.Getenv("ANNOTATE_EVAL_URL"), os.Getenv("ANNOTATE_EVAL_MODEL")
	if url == "" || model == "" {
		t.Skip("define ANNOTATE_EVAL_URL y ANNOTATE_EVAL_MODEL para evaluar un modelo real")
	}
	samples, err := LoadSamples(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Evaluate(context.Background(), NewClient(url, os.Getenv("ANNOTATE_EVAL_KEY"), model), samples, EvalOptions{Repeats: 2})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Print(rep.String())
}
