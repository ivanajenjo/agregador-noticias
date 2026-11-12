package store

import (
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestWatchesCRUD(t *testing.T) {
	s := newStore(t)
	w := &domain.Watch{Name: "Vivienda", Query: "ley vivienda", SourceIDs: []string{"a"}, Leanings: []string{"derecha"}, Notify: true}
	if err := s.CreateWatch(ctx, w); err != nil || w.ID == 0 {
		t.Fatal(w, err)
	}
	if err := s.CreateWatch(ctx, &domain.Watch{Name: " ", Query: "x"}); err == nil {
		t.Fatal("empty name accepted")
	}
	got, err := s.GetWatch(ctx, w.ID)
	if err != nil || got.Name != "Vivienda" || got.SourceIDs[0] != "a" || got.Leanings[0] != "derecha" || !got.Notify || !got.LastEvalAt.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
	now := time.Now()
	if err := s.SetWatchEvaluated(ctx, w.ID, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetWatch(ctx, w.ID)
	if got.LastEvalAt.Unix() != now.Unix() {
		t.Fatal(got.LastEvalAt)
	}
	id1 := ins(t, s, domain.Article{SourceID: "a", URL: "http://a/1", CanonicalURL: "http://a/1", Title: "uno", FirstSeenAt: day(1)})
	id2 := ins(t, s, domain.Article{SourceID: "a", URL: "http://a/2", CanonicalURL: "http://a/2", Title: "dos", FirstSeenAt: day(1)})
	if err := s.MarkWatchHits(ctx, w.ID, []int64{id1}, now); err != nil {
		t.Fatal(err)
	}
	nw, _ := s.NewWatchHits(ctx, w.ID, []int64{id1, id2})
	if len(nw) != 1 || nw[0] != id2 {
		t.Fatal(nw)
	}
	if err := s.PruneWatchHits(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if nw, _ = s.NewWatchHits(ctx, w.ID, []int64{id1}); len(nw) != 1 {
		t.Fatal("prune did not remove", nw)
	}
	_ = s.MarkWatchHits(ctx, w.ID, []int64{id1}, now)
	if err := s.DeleteWatch(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListWatches(ctx); len(l) != 0 {
		t.Fatal(l)
	}
	if nw, _ = s.NewWatchHits(ctx, w.ID, []int64{id1}); len(nw) != 1 {
		t.Fatal("hits not cascaded")
	}
}

func TestAlertLog(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	rec := func(kind, key, ch string, ok bool, at time.Time) domain.AlertRecord {
		return domain.AlertRecord{Kind: kind, Key: key, Title: "T", URL: "u", SentAt: at, Channel: ch, OK: ok, Detail: "d"}
	}
	if err := s.SaveAlert(ctx, domain.AlertRecord{}); err == nil {
		t.Fatal("invalid accepted")
	}
	// failed recently: not handled; failed long ago: given up; ok: handled
	_ = s.SaveAlert(ctx, rec("story", "k1", "ntfy", false, now))
	if h, _ := s.AlertHandled(ctx, "story", "k1", now.Add(-time.Hour)); h {
		t.Fatal("recent failure must be retried")
	}
	if h, _ := s.AlertHandled(ctx, "story", "k1", now.Add(time.Hour)); !h {
		t.Fatal("old failure must be given up")
	}
	// retry succeeds: same unique row updated
	_ = s.SaveAlert(ctx, rec("story", "k1", "ntfy", true, now.Add(time.Minute)))
	if h, _ := s.AlertHandled(ctx, "story", "k1", now.Add(-time.Hour)); !h {
		t.Fatal("ok must be handled")
	}
	l, _ := s.ListAlerts(ctx, 10)
	if len(l) != 1 || !l[0].OK {
		t.Fatal(l)
	}
	_ = s.SaveAlert(ctx, rec("story", "k2", "quiet", false, now))
	_ = s.SaveAlert(ctx, rec("watch", "w1", "log", true, now))
	_ = s.SaveAlert(ctx, rec("test", "t", "log", true, now))
	if h, _ := s.AlertHandled(ctx, "story", "k2", now.Add(-time.Hour)); !h {
		t.Fatal("quiet is handled")
	}
	if n, _ := s.CountAlertsSince(ctx, now.Add(-time.Hour)); n != 2 {
		t.Fatalf("count %d", n)
	}
	p, _ := s.PendingQuiet(ctx)
	if len(p) != 1 || p[0].Key != "k2" {
		t.Fatal(p)
	}
	if err := s.ResolveQuiet(ctx, []int64{p[0].ID}, "resumen"); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.PendingQuiet(ctx); len(p) != 0 {
		t.Fatal(p)
	}
}
