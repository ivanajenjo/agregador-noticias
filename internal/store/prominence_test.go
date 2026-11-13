package store

import (
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestFrontItems(t *testing.T) {
	s := newStore(t)
	if err := s.UpsertSources(ctx, []domain.Source{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	artID := ins(t, s, domain.Article{SourceID: "a", URL: "https://a.es/n1", CanonicalURL: "https://a.es/n1", Title: "Noticia uno", FirstSeenAt: day(1)})
	t0 := time.Unix(1_800_000_000, 0)
	shot := func(src string, at time.Time) int64 {
		sh := &domain.Screenshot{SourceID: src, TakenAt: at, Path: src + at.Format("/150405.jpg")}
		if err := s.SaveScreenshot(ctx, sh); err != nil {
			t.Fatal(err)
		}
		return sh.ID
	}
	mk := func(rank int, u string, prom float64) domain.FrontItem {
		return domain.FrontItem{Rank: rank, DOMOrder: rank, Title: "T " + u, URL: u, CanonicalURL: u, X: 1, Y: 2, W: 300, H: 40, FontPx: 32, Prominence: prom, AboveFold: rank == 1}
	}
	s1 := shot("a", t0)
	s2 := shot("a", t0.Add(time.Hour))
	s3 := shot("b", t0.Add(time.Hour))
	for id, items := range map[int64][]domain.FrontItem{
		s1: {mk(1, "https://a.es/n1", 1), mk(2, "https://a.es/n2", .5)},
		s2: {mk(1, "https://a.es/n2", 1), mk(3, "https://a.es/n1", .3)},
		s3: {mk(1, "https://b.es/x", 1)},
	} {
		if err := s.SaveFrontItems(ctx, id, items); err != nil {
			t.Fatal(err)
		}
	}
	// replace is idempotent
	if err := s.SaveFrontItems(ctx, s3, []domain.FrontItem{mk(1, "https://b.es/x", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFrontItems(ctx, 9999, nil); err == nil {
		t.Fatal("unknown screenshot accepted")
	}

	latest, err := s.ListFrontItems(ctx, "a", time.Time{})
	if err != nil || len(latest) != 2 || latest[0].CanonicalURL != "https://a.es/n2" || !latest[0].AboveFold || latest[0].SourceID != "a" {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if latest[1].ArticleID != artID {
		t.Fatalf("article link: %+v", latest[1])
	}
	exact, _ := s.ListFrontItems(ctx, "a", t0)
	if len(exact) != 2 || exact[0].CanonicalURL != "https://a.es/n1" || exact[0].W != 300 {
		t.Fatalf("exact: %+v", exact)
	}
	if none, _ := s.ListFrontItems(ctx, "a", t0.Add(5*time.Minute)); len(none) != 0 {
		t.Fatal(none)
	}

	top, err := s.TopItems(ctx, t0.Add(-time.Hour), t0.Add(2*time.Hour), nil, 10)
	if err != nil || len(top) != 3 {
		t.Fatalf("top: %+v %v", top, err)
	}
	var n1 *domain.TopItem
	for i := range top {
		if top[i].CanonicalURL == "https://a.es/n1" {
			n1 = &top[i]
		}
	}
	if n1 == nil || n1.Captures != 2 || n1.BestRank != 1 || n1.ArticleID != artID || n1.AvgProm < .64 || n1.AvgProm > .66 || !n1.LastAt.After(n1.FirstAt) {
		t.Fatalf("n1: %+v", n1)
	}
	if top1, _ := s.TopItems(ctx, time.Time{}, time.Time{}, []string{"b"}, 5); len(top1) != 1 || top1[0].SourceID != "b" {
		t.Fatalf("filter: %+v", top1)
	}
	if lim, _ := s.TopItems(ctx, time.Time{}, time.Time{}, nil, 2); len(lim) != 2 {
		t.Fatalf("limit: %d", len(lim))
	}
	if early, _ := s.TopItems(ctx, time.Time{}, t0, nil, 10); len(early) != 0 {
		t.Fatalf("to is exclusive: %+v", early)
	}

	h, err := s.ItemHistory(ctx, "https://a.es/n1")
	if err != nil || len(h) != 2 || h[0].Rank != 1 || h[1].Rank != 3 || !h[0].TakenAt.Before(h[1].TakenAt) {
		t.Fatalf("history: %+v %v", h, err)
	}
	if h, _ := s.ItemHistory(ctx, ""); h != nil {
		t.Fatal("empty canonical must be nil")
	}
}

func TestFrontItemsCascade(t *testing.T) {
	s := newStore(t)
	_ = s.UpsertSources(ctx, []domain.Source{{ID: "a", Name: "A"}})
	sh := &domain.Screenshot{SourceID: "a", TakenAt: time.Unix(1_700_000_000, 0), Path: "a/x.jpg"}
	if err := s.SaveScreenshot(ctx, sh); err != nil {
		t.Fatal(err)
	}
	_ = s.SaveFrontItems(ctx, sh.ID, []domain.FrontItem{{Rank: 1, Title: "t", URL: "https://a.es/1", CanonicalURL: "https://a.es/1"}})
	if _, err := s.DeleteScreenshotsBefore(ctx, time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListFrontItems(ctx, "a", time.Time{}); len(l) != 0 {
		t.Fatalf("items survived screenshot deletion: %+v", l)
	}
}
