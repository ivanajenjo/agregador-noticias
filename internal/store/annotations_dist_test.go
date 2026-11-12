package store

import (
	"testing"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestAnnotationDistributionAndList(t *testing.T) {
	s := newStore(t)
	mk := func(src, u string, d int) int64 {
		a := domain.Article{SourceID: src, URL: "http://x/" + u, CanonicalURL: "http://x/" + u, Title: "T " + u, PublishedAt: day(d), FirstSeenAt: day(d)}
		ins(t, s, a)
		got, err := s.ListArticles(ctx, domain.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range got {
			if g.URL == a.URL {
				return g.ID
			}
		}
		t.Fatal("not found")
		return 0
	}
	a1, a2, a3, a4 := mk("a", "1", 1), mk("a", "2", 2), mk("b", "3", 3), mk("b", "4", 4)
	save := func(id int64, label string, score float64, model string) {
		if err := s.SaveAnnotation(ctx, domain.Annotation{ArticleID: id, Kind: "tone", Model: model, Label: label, Score: score}); err != nil {
			t.Fatal(err)
		}
	}
	save(a1, "neutral", 0.9, "m")
	save(a2, "crítico", 0.4, "m")
	save(a3, "crítico", 0.8, "m")
	save(a4, "neutral", 0.9, "other")

	q := domain.AnnotationQuery{Kind: "tone", Model: "m"}
	d, err := s.AnnotationDistribution(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, c := range d {
		got[c.SourceID+"/"+c.Label] = c.N
	}
	if len(got) != 3 || got["a/neutral"] != 1 || got["a/crítico"] != 1 || got["b/crítico"] != 1 {
		t.Fatalf("%v", got)
	}
	q.MinScore = 0.5
	d, _ = s.AnnotationDistribution(ctx, q)
	if len(d) != 2 {
		t.Fatalf("min score ignored: %v", d)
	}
	q.SourceIDs = []string{"b"}
	if d, _ = s.AnnotationDistribution(ctx, q); len(d) != 1 || d[0].SourceID != "b" {
		t.Fatalf("%v", d)
	}
	q = domain.AnnotationQuery{Kind: "tone", Model: "m", From: day(2), To: day(3)}
	if d, _ = s.AnnotationDistribution(ctx, q); len(d) != 2 {
		t.Fatalf("window: %v", d)
	}

	l, err := s.ListAnnotated(ctx, domain.AnnotationQuery{Kind: "tone", Model: "m", Label: "crítico"})
	if err != nil || len(l) != 2 || l[0].Article.ID != a3 || l[1].Article.ID != a2 || l[1].Score != 0.4 || l[0].Label != "crítico" {
		t.Fatalf("%+v %v", l, err)
	}
	if l, _ = s.ListAnnotated(ctx, domain.AnnotationQuery{Kind: "tone", Model: "m", Label: "crítico", MinScore: 0.5, Limit: 1}); len(l) != 1 || l[0].Article.ID != a3 {
		t.Fatalf("%+v", l)
	}
	ms, err := s.AnnotationModels(ctx, "tone")
	if err != nil || len(ms) != 2 || ms[0].Model != "m" || ms[0].Count != 3 {
		t.Fatalf("%+v %v", ms, err)
	}
	var _ domain.AnnotationReader = s
}
