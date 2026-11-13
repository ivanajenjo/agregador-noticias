package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// profStore adds the cheap projection used by the profile engine.
type profStore struct {
	*fakeStore
	lite []domain.ArticleLite
}

func (p profStore) ArticleTexts(_ context.Context, from, to time.Time) ([]domain.ArticleLite, error) {
	var out []domain.ArticleLite
	for _, a := range p.lite {
		if !a.At.Before(from) && a.At.Before(to) {
			out = append(out, a)
		}
	}
	return out, nil
}

type tlog struct{ t *testing.T }

func (l tlog) Write(b []byte) (int, error) { l.t.Log(strings.TrimSpace(string(b))); return len(b), nil }

func rebuildWith(t *testing.T, now time.Time) http.Handler {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) {
		d.Log = slog.New(slog.NewTextHandler(tlog{t}, nil))
		fs := d.Store.(*fakeStore)
		fs.sources = []domain.Source{
			{ID: "pais", Name: "El País", Leaning: "centro-izquierda", Kind: "prensa", Region: "nacional", Homepage: "https://e.example/", Feeds: []domain.Feed{{URL: "https://e.example/rss"}}},
			{ID: "xss", Name: "<script>alert(1)</script>", Leaning: "derecha", Kind: "digital", Region: "cataluña"},
			{ID: "pub", Name: "Público", Leaning: "izquierda"},
		}
		var lite []domain.ArticleLite
		for i := 0; i < 20; i++ {
			lite = append(lite, domain.ArticleLite{ID: int64(i + 1), SourceID: "pais", Title: "Vivienda alquiler <b>x</b> " + string(rune('a'+i)), URL: "https://e.example/a", At: now.Add(-time.Duration(i) * 3 * time.Hour)})
		}
		lite = append(lite, domain.ArticleLite{ID: 100, SourceID: "xss", Title: "<img src=x onerror=alert(1)>", At: now.Add(-time.Hour)})
		d.Store = profStore{fs, lite}
		ref := func(id, name, lean string, ago time.Duration) domain.SourceCoverage {
			return domain.SourceCoverage{SourceID: id, Name: name, Leaning: lean, Count: 1, FirstAt: now.Add(-ago)}
		}
		d.Trends = &fakeTrends{stories: []domain.Story{{ID: "s1", Title: "Historia 1", FirstAt: now.Add(-5 * time.Hour), FirstBy: "pais", Sources: 3,
			Coverage: []domain.SourceCoverage{ref("pais", "El País", "centro-izquierda", 5*time.Hour), ref("pub", "Público", "izquierda", 4*time.Hour), ref("xss", "x", "derecha", 3*time.Hour)}}}}
	})
	return h
}

func TestProfilePages(t *testing.T) {
	h := rebuildWith(t, time.Now())
	r := do(h, "GET", "/medios", nil, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "El País") || !strings.Contains(r.Body.String(), "subjetiva") {
		t.Fatalf("list %d", r.Code)
	}
	if strings.Contains(r.Body.String(), "<script>alert(1)") {
		t.Fatal("unescaped outlet name")
	}
	if b := do(h, "GET", "/medios?tipo=digital&orden=volumen", nil, "").Body.String(); strings.Contains(b, `href="/medios/pais"`) || !strings.Contains(b, "&lt;script&gt;") {
		t.Fatal("kind filter")
	}
	r = do(h, "GET", "/medios/pais", nil, "")
	b := r.Body.String()
	if r.Code != 200 || !strings.Contains(b, "Frente al resto") || !strings.Contains(b, "etiqueta subjetiva") || !strings.Contains(b, "n=20") {
		t.Fatalf("profile %d", r.Code)
	}
	if strings.Contains(b, "<b>x</b>") {
		t.Fatal("headline not escaped")
	}
	b = do(h, "GET", "/medios/xss", nil, "").Body.String()
	if strings.Contains(b, "<script>alert(1)") || strings.Contains(b, "<img src=x") {
		t.Fatal("xss in profile")
	}
	if do(h, "GET", "/medios/nope", nil, "").Code != 404 {
		t.Fatal("404 expected")
	}
	r = do(h, "GET", "/medios/comparar?a=pais&b=pub&c=xss", nil, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Cobertura de historias") || !strings.Contains(r.Body.String(), "Público") {
		t.Fatalf("compare %d", r.Code)
	}
	if strings.Contains(r.Body.String(), "<script>alert(1)") {
		t.Fatal("xss in compare")
	}
	if do(h, "GET", "/medios/comparar", nil, "").Code != 200 || do(h, "GET", "/medios/comparar?a=nope", nil, "").Code != 404 {
		t.Fatal("compare edge cases")
	}
	if !strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/medios") {
		t.Fatal("nav item missing")
	}
}

func TestProfileAPI(t *testing.T) {
	h := rebuildWith(t, time.Now())
	var list struct {
		Outlets []map[string]any `json:"outlets"`
	}
	r := do(h, "GET", "/api/outlets", nil, "")
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &list) != nil || len(list.Outlets) != 3 {
		t.Fatalf("api list %d %s", r.Code, r.Body.String())
	}
	var one map[string]any
	r = do(h, "GET", "/api/outlets/pais", nil, "")
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &one) != nil || one["id"] != "pais" || one["lag"] == nil {
		t.Fatalf("api one %d", r.Code)
	}
	if r = do(h, "GET", "/api/outlets/nope", nil, ""); r.Code != 404 || !strings.Contains(r.Body.String(), "error") {
		t.Fatalf("api 404 %d", r.Code)
	}
}

func TestProfileEmptyAndNoTrends(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Store = emptyStore{}; d.Trends = nil })
	if r := do(h, "GET", "/medios", nil, ""); r.Code != 200 {
		t.Fatalf("empty list %d", r.Code)
	}
	if r := do(h, "GET", "/medios/x", nil, ""); r.Code != 404 {
		t.Fatalf("empty 404 %d", r.Code)
	}
}
