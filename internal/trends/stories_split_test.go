package trends

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// A broad topic (an election campaign) in which every headline mentions the
// same broad entities must split into the events inside it, and no cluster may
// exceed the event size cap.
func TestBroadTopicSplitsIntoEvents(t *testing.T) {
	f := newScenarioStore()
	at := func(h float64) time.Time { return scenarioNow.Add(-time.Duration(h * float64(time.Hour))) }
	broad := []string{"Pedro Sánchez", "Feijóo", "el PSOE", "el PP", "Vox", "la campaña"}
	events := []struct{ marker, tpl string }{
		{"Zorrilla", "%s y %s: el candidato Zorrilla firma un pacto con %s en la campaña"},
		{"Quintanar", "%s, %s y la polémica de Quintanar con %s en plena campaña"},
		{"Bermejales", "Encuesta de Bermejales: %s, %s y %s ante la campaña"},
		{"Cantabrana", "El mitin de Cantabrana con %s, %s y %s"},
	}
	// 4 events × 12 articles from the 12 outlets, 2 hours apart between events
	for ei, ev := range events {
		for i, o := range outlets {
			title := fmt.Sprintf(ev.tpl, broad[(i)%len(broad)], broad[(i+ei+1)%len(broad)], broad[(i+2*ei+2)%len(broad)])
			f.add(o.id, title, "", at(20-float64(ei)*4-float64(i)*0.15))
		}
	}
	e := New(f, Options{Now: func() time.Time { return scenarioNow }})
	o := domain.TrendOptions{Now: scenarioNow}
	st, err := e.Stories(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range st {
		if s.Articles > storyMaxSize {
			t.Errorf("story over the cap: %d articles", s.Articles)
		}
		arts, err := e.StoryArticles(context.Background(), s.ID, o)
		if err != nil || len(arts) != s.Articles {
			t.Fatalf("StoryArticles: %v %d/%d", err, len(arts), s.Articles)
		}
		var marks []string
		for _, ev := range events {
			n := 0
			for _, a := range arts {
				if strings.Contains(a.Title, ev.marker) {
					n++
				}
			}
			if n > 0 {
				marks = append(marks, ev.marker)
				if n >= 5 {
					seen[ev.marker] = true
				}
			}
		}
		if len(marks) > 1 {
			t.Errorf("story %q mixes events %v", s.Title, marks)
		}
	}
	for _, ev := range events {
		if !seen[ev.marker] {
			t.Errorf("event %s not recovered as its own story (stories: %d)", ev.marker, len(st))
		}
	}
	if _, err := e.StoryArticles(context.Background(), "nope", o); err == nil {
		t.Error("expected not found")
	}
	if w, err := e.WindowArticles(context.Background(), o); err != nil || len(w) == 0 {
		t.Errorf("window articles: %v %d", err, len(w))
	}
}
