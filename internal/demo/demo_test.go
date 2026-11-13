package demo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
)

var testNow = time.Date(2026, 11, 13, 14, 30, 0, 0, time.UTC)

func genT(t *testing.T, dir string, seed int64) *Summary {
	t.Helper()
	s, err := Generate(context.Background(), Options{Out: dir, Days: 14, Seed: seed, Now: testNow,
		SourcesFile: filepath.Join("..", "..", "config", "sources.yaml"), SkipShots: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeterministic(t *testing.T) {
	a := genT(t, t.TempDir(), 42)
	b := genT(t, t.TempDir(), 42)
	if a.Hash != b.Hash || a.Articles != b.Articles || a.Annotations != b.Annotations || a.Revisions != b.Revisions {
		t.Fatalf("same seed gave different data: %+v vs %+v", a, b)
	}
	c := genT(t, t.TempDir(), 7)
	if c.Hash == a.Hash {
		t.Fatal("different seed gave identical data")
	}
}

func TestInvariants(t *testing.T) {
	dir := t.TempDir()
	s := genT(t, dir, 42)
	if s.Revisions != 12 {
		t.Errorf("revisions = %d, want 12", s.Revisions)
	}
	if s.Articles < 14*60 || s.Digests != 4 || s.Watches != 3 || s.Alerts < 6 {
		t.Errorf("unexpected summary %+v", s)
	}
	st, err := store.Open(filepath.Join(dir, "agregador.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	srcs, _ := st.ListSources(ctx)
	lean := map[string]string{}
	for _, x := range srcs {
		lean[x.ID] = x.Leaning
	}
	if lean["abc"] != "derecha" || lean["eldiario"] != "izquierda" || lean["elpais"] != "centro-izquierda" {
		t.Errorf("leanings differ from sources.yaml: %v", lean)
	}
	rev, ok := interface{}(st).(domain.RevisionStore)
	if !ok {
		t.Fatal("no RevisionStore")
	}
	if n, _ := rev.CountRevisions(ctx, domain.RevisionFilter{}); n != 12 {
		t.Errorf("stored revisions = %d", n)
	}

	eng := trends.New(st, trends.Options{Now: func() time.Time { return testNow }})
	o := domain.TrendOptions{Now: testNow}
	sts, err := eng.Stories(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(sts) < 4 {
		t.Fatalf("stories = %d, want >= 4", len(sts))
	}
	left, right := map[string]bool{"izquierda": true, "centro-izquierda": true}, map[string]bool{"derecha": true, "centro-derecha": true}
	var onlyLeft, onlyRight, big int
	for _, st := range sts {
		if st.Sources >= 5 {
			big++
		}
		if st.Sources < 5 {
			continue
		}
		l, r, c := 0, 0, 0
		for _, cv := range st.Coverage {
			switch {
			case left[cv.Leaning]:
				l++
			case right[cv.Leaning]:
				r++
			default:
				c++
			}
		}
		if r == 0 && c == 0 {
			onlyLeft++
		}
		if l == 0 && c == 0 {
			onlyRight++
		}
	}
	if big < 4 {
		t.Errorf("stories with >= 5 outlets = %d", big)
	}
	if onlyLeft < 1 || onlyRight < 1 {
		t.Errorf("blind-spot stories: left-only %d, right-only %d", onlyLeft, onlyRight)
	}
	rep, err := eng.Trending(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	newTerms := map[string]bool{}
	for _, tm := range rep.Terms {
		if tm.New {
			newTerms[strings.ToLower(tm.Term)] = true
		}
	}
	if !newTerms["ciro"] || !newTerms["tractorada"] {
		t.Errorf("new terms missing ciro/tractorada: %v", newTerms)
	}
}

func TestEmbedDeterministic(t *testing.T) {
	a, b := Embed("La borrasca Ciro"), Embed("la borrasca CIRO")
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("embedding not case-insensitive")
		}
	}
}

func TestWrapText(t *testing.T) {
	l := WrapText("uno dos tres cuatro cinco seis siete ocho nueve diez", 2, 12*10, 2)
	if len(l) != 2 || !strings.HasSuffix(l[1], "...") {
		t.Errorf("wrap: %q", l)
	}
}
