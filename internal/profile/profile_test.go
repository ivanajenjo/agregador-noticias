package profile

import (
	"context"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeStore struct {
	domain.Store // nil: only the methods below are used
	srcs         []domain.Source
	arts         []domain.ArticleLite
	feeds        []domain.FeedState
	calls        int
}

func (f *fakeStore) ListSources(context.Context) ([]domain.Source, error) { return f.srcs, nil }
func (f *fakeStore) ListFeedStates(context.Context) ([]domain.FeedState, error) {
	return f.feeds, nil
}
func (f *fakeStore) ArticleTexts(_ context.Context, from, to time.Time) ([]domain.ArticleLite, error) {
	f.calls++
	var out []domain.ArticleLite
	for _, a := range f.arts {
		if !a.At.Before(from) && a.At.Before(to) {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeStore) ListArticles(context.Context, domain.Filter) ([]domain.Article, error) {
	return []domain.Article{{Section: "Política"}, {Section: "Política"}, {Section: ""}}, nil
}
func (f *fakeStore) ListScreenshots(context.Context, string, time.Time, time.Time) ([]domain.Screenshot, error) {
	return nil, nil
}

type fakeTP struct{ st []domain.Story }

func (f fakeTP) Trending(context.Context, domain.TrendOptions) (*domain.TrendReport, error) {
	return nil, nil
}
func (f fakeTP) Term(context.Context, string, int, domain.TrendOptions) (*domain.TermDetail, error) {
	return nil, nil
}
func (f fakeTP) Stories(context.Context, domain.TrendOptions) ([]domain.Story, error) {
	return f.st, nil
}

func TestComputeLag(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	cov := func(id, lean string, min int) domain.SourceCoverage {
		return domain.SourceCoverage{SourceID: id, Leaning: lean, Count: 1, FirstAt: now.Add(-48 * time.Hour).Add(time.Duration(min) * time.Minute)}
	}
	mk := func(id string, cs ...domain.SourceCoverage) domain.Story {
		return domain.Story{ID: id, Title: id, FirstAt: cs[0].FirstAt, FirstBy: cs[0].SourceID, Coverage: cs}
	}
	st := []domain.Story{
		mk("s1", cov("a", "izquierda", 0), cov("x", "derecha", 10), cov("c", "centro", 20)),                            // x lag 10
		mk("s2", cov("x", "derecha", 0), cov("a", "izquierda", 30), cov("c", "centro", 40)),                            // x first
		mk("s3", cov("a", "izquierda", 0), cov("c", "centro", 5), cov("d", "derecha", 6)),                              // x omitted
		mk("s4", cov("c", "centro", 0), cov("x", "derecha", 50), cov("a", "izquierda", 60)),                            // x lag 50
		{ID: "two", FirstAt: now.Add(-time.Hour), Coverage: []domain.SourceCoverage{cov("a", "", 0), cov("c", "", 1)}}, // <3 outlets
	}
	st = append(st, domain.Story{ID: "old", FirstAt: now.Add(-10 * 24 * time.Hour), Coverage: st[0].Coverage})
	l := ComputeLag(st, "x", 7, now)
	if l.Stories != 4 || l.Covered != 3 || l.Omitted != 1 || l.FirstN != 1 {
		t.Fatalf("counts: %+v", l)
	}
	if l.MedianLagMin != 10 || l.LagSamples != 3 {
		t.Fatalf("median = %v n=%d", l.MedianLagMin, l.LagSamples)
	}
	if l.SharePct() < 33 || l.SharePct() > 34 || l.CoveragePct() != 75 {
		t.Fatalf("pcts %v %v", l.SharePct(), l.CoveragePct())
	}
	// s3 omitted: covered by izquierda, centro, derecha(d)
	if l.Sides[0].Omitted != 1 || l.Sides[4].Omitted != 1 || l.Sides[2].Omitted != 1 {
		t.Fatalf("sides %+v", l.Sides)
	}
	if l.Sides[0].Covered != 4 || len(l.OmittedTop) != 1 || l.OmittedTop[0].ID != "s3" {
		t.Fatalf("sides/omitted %+v %+v", l.Sides, l.OmittedTop)
	}
	if l14 := ComputeLag(st, "x", 14, now); l14.Stories != 5 {
		t.Fatalf("14d stories = %d", l14.Stories)
	}
}

func TestEngineBucketsAndCache(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	// 2026-11-10 is Tuesday. 23:30 UTC = 00:30 Wednesday in Madrid (CET).
	now := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, time.UTC) }
	fs := &fakeStore{
		srcs: []domain.Source{{ID: "x", Name: "Medio X", Leaning: "centro", Feeds: []domain.Feed{{URL: "http://f/1"}, {URL: "http://f/2"}}}, {ID: "y", Name: "Y"}, {ID: "off", Name: "Off", Enabled: new(bool)}},
		arts: []domain.ArticleLite{
			{ID: 1, SourceID: "x", Title: "Presupuestos aprobados hoy", At: at(8, 15)},
			{ID: 2, SourceID: "x", Title: "Presupuestos y deuda", At: at(8, 45)},
			{ID: 3, SourceID: "x", Title: "Presupuestos de la Generalitat", At: at(9, 0)},
			{ID: 4, SourceID: "y", Title: "Lluvia en Valencia", At: at(9, 0)},
			{ID: 5, SourceID: "x", Title: "Madrugada", At: at(21, 30)}, // today Madrid 22:30
		},
		feeds: []domain.FeedState{{SourceID: "x", FeedURL: "http://f/1", LastOK: now}, {SourceID: "x", FeedURL: "http://f/2", LastOK: now.Add(-time.Hour), LastError: "boom", LastErrorAt: now}},
	}
	e := New(fs, fakeTP{}, Options{Location: loc, Now: func() time.Time { return now }})
	p, err := e.Profile(context.Background(), "x", 14)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total30 != 4 || p.Daily[DailyDays-1].N != 4 {
		t.Fatalf("totals %d %d", p.Total30, p.Daily[DailyDays-1].N)
	}
	// Tuesday (index 1): 08:xx UTC = 09 Madrid (2), 09 UTC = 10 Madrid (1), 21:30 UTC = 22 Madrid (1)
	if p.Heat[1][9] != 2 || p.Heat[1][10] != 1 || p.Heat[1][22] != 1 {
		t.Fatalf("heat %v", p.Heat[1])
	}
	if p.Health != "warn" || len(p.Feeds) != 2 || p.Feeds[1].Status != "bad" {
		t.Fatalf("health %s %+v", p.Health, p.Feeds)
	}
	if len(p.Headlines) != 4 || p.Headlines[0].ID != 5 {
		t.Fatalf("headlines %+v", p.Headlines)
	}
	if len(p.Terms) == 0 || p.Terms[0].Term != "presupuestos" || p.Terms[0].Count != 3 {
		t.Fatalf("terms %+v", p.Terms)
	}
	if p.SectionsN != 3 || p.Sections[0].Label != "Política" || p.Sections[0].N != 2 {
		t.Fatalf("sections %+v", p.Sections)
	}
	calls := fs.calls
	if _, err := e.Profile(context.Background(), "x", 14); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fs.calls != calls {
		t.Fatal("cache not used")
	}
	if _, err := e.Profile(context.Background(), "off", 14); err != ErrNotFound {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := e.Profile(context.Background(), "nope", 14); err != ErrNotFound {
		t.Fatalf("unknown: %v", err)
	}
	ov, _ := e.Overview(context.Background())
	if len(ov.Outlets) != 2 || ov.Outlets[0].Total30 != 4 {
		t.Fatalf("overview %+v", ov.Outlets)
	}
}
