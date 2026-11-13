package web

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeProm struct {
	items  map[string][]domain.FrontItem // by source (latest capture)
	top    []domain.TopItem
	hist   []domain.ItemPoint
	err    error
	lastAt time.Time
}

func (f *fakeProm) SaveFrontItems(context.Context, int64, []domain.FrontItem) error { return nil }
func (f *fakeProm) ListFrontItems(_ context.Context, src string, at time.Time) ([]domain.FrontItem, error) {
	f.lastAt = at
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.FrontItem
	for _, it := range f.items[src] {
		if at.IsZero() || it.TakenAt.Equal(at) {
			out = append(out, it)
		}
	}
	return out, nil
}
func (f *fakeProm) TopItems(_ context.Context, _, _ time.Time, ids []string, limit int) ([]domain.TopItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.TopItem
	for _, t := range f.top {
		if len(ids) == 0 || contains(ids, t.SourceID) {
			out = append(out, t)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeProm) ItemHistory(context.Context, string) ([]domain.ItemPoint, error) {
	return f.hist, f.err
}

func newFakeProm() *fakeProm {
	at := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	mk := func(src string, rank int, title, u string, p float64, art int64) domain.FrontItem {
		return domain.FrontItem{SourceID: src, TakenAt: at, Rank: rank, Title: title, URL: u, CanonicalURL: u, X: 10, Y: float64(rank * 100), W: 300, H: 60, FontPx: 30, Prominence: p, AboveFold: rank == 1, ArticleID: art}
	}
	base := time.Now().Add(-6 * time.Hour).Truncate(time.Minute)
	pt := func(i, rank int) domain.ItemPoint {
		return domain.ItemPoint{SourceID: "pais", TakenAt: base.Add(time.Duration(i) * 30 * time.Minute), Rank: rank, Prominence: 1 / float64(rank), Title: "Vivienda <b>x</b>"}
	}
	return &fakeProm{
		items: map[string][]domain.FrontItem{
			"pais": {mk("pais", 1, "Vivienda <script>alert(1)</script>", "https://e.example/1", 1, 1), mk("pais", 2, "Segundo titular", "javascript:alert(2)", .6, 0), mk("pais", 3, "Tercero", "https://e.example/3", .4, 0), mk("pais", 4, "Cuarto no se muestra", "https://e.example/4", .2, 0)},
			"abc":  {mk("abc", 1, "Otro titular de portada", "https://e.example/2", 1, 2)},
		},
		top:  []domain.TopItem{{SourceID: "pais", CanonicalURL: "https://e.example/1", Title: "Vivienda <i>x</i>", Captures: 9, BestRank: 1, MaxProm: 1, FirstAt: at.Add(-4 * time.Hour), LastAt: at}},
		hist: []domain.ItemPoint{pt(0, 3), pt(1, 1), pt(2, 1), pt(3, 2), pt(8, 5)}, // gap after #3
	}
}

func TestFrontpagePage(t *testing.T) {
	fp := newFakeProm()
	ft := newFakeTrends()
	// story where both outlets lead: articles 1 (pais) and 2 (abc)
	ft.stories = []domain.Story{{ID: "s", Title: "Historia <compartida>", Coverage: []domain.SourceCoverage{
		{SourceID: "pais", Headlines: []domain.ArticleRef{{ID: 1}}}, {SourceID: "abc", Headlines: []domain.ArticleRef{{ID: 2}}}}}}
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = fp; d.Trends = ft })
	rec := do(h, "GET", "/portadas", nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	for _, want := range []string{"Lo que abre hoy", "Segundo titular", "Tercero", `<span>Portadas</span>`, "Varios medios abren con la misma historia",
		"Historia &lt;compartida&gt;", "También abren con esto", "frontpage.css", "Más tiempo en portada"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(b, "<script>alert(1)") || strings.Contains(b, "Cuarto no se muestra") {
		t.Error("unescaped title or more than top 3")
	}
	if strings.Contains(b, `href="javascript:`) {
		t.Error("javascript: href rendered")
	}
	// order by leaning: "centro-izquierda" (pais) before "derecha" (abc)
	if strings.Index(b, "El País") > strings.Index(b, "Medio &lt;Derecha&gt;") {
		t.Error("cards not ordered by leaning")
	}
	if !strings.Contains(b, "Vivienda &lt;i&gt;x&lt;/i&gt;") && !strings.Contains(b, "https://e.example/1") {
		t.Error("top row missing")
	}
	// the shared highlight needs trends: without them it disappears
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = fp; d.Trends = nil })
	if b2 := do(h2, "GET", "/portadas", nil, "").Body.String(); strings.Contains(b2, "Varios medios abren") {
		t.Error("multi shown without trends")
	}
}

func TestFrontpageHistory(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = newFakeProm() })
	rec := do(h, "GET", "/portadas?item=https%3A%2F%2Fe.example%2F1", nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	b := rec.Body.String()
	for _, want := range []string{`class="fp-chart"`, "Evolución en portada", "Visto en 5 capturas", "mejor posición #1", "abrió portada en 2 capturas", `id="evolucion"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(b, "<b>x</b>") {
		t.Error("unescaped history title")
	}
	// the gap (4 capture intervals) breaks the step line into two segments
	if n := strings.Count(b, "M"); n < 2 {
		t.Error("expected a broken line")
	}
	// unknown item
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = &fakeProm{} })
	if b := do(h2, "GET", "/portadas?item=https%3A%2F%2Fnada", nil, "").Body.String(); !strings.Contains(b, "no aparece en ninguna portada") {
		t.Error("missing empty message")
	}
	if rec := do(h2, "GET", "/portadas?item="+strings.Repeat("a", 3000), nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "demasiado larga") {
		t.Error("long item not rejected gracefully")
	}
}

func TestFrontpageEmptyAndErrors(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = &fakeProm{} })
	rec := do(h, "GET", "/portadas", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Aún no hay titulares detectados") {
		t.Fatal(rec.Code)
	}
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = &fakeProm{err: errors.New("boom")} })
	if rec := do(h2, "GET", "/portadas", nil, ""); rec.Code != 200 {
		t.Error(rec.Code)
	}
	if rec := do(h2, "GET", "/api/frontpage?source=pais", nil, ""); rec.Code != 500 || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("api error leaks: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFrontpageHiddenWithoutStore(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, p := range []string{"/portadas", "/api/frontpage?source=pais", "/api/frontpage/top"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 503 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if b := do(h, "GET", "/", nil, "").Body.String(); strings.Contains(b, `<span>Portadas</span>`) {
		t.Error("nav item visible without store")
	}
	if b := do(h, "GET", "/mas", nil, "").Body.String(); strings.Contains(b, "/portadas") {
		t.Error("/mas lists Portadas without store")
	}
}

func TestFrontpageAPI(t *testing.T) {
	fp := newFakeProm()
	h, st, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = fp })
	var got fpJSON
	rec := do(h, "GET", "/api/frontpage?source=pais", nil, "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Items) != 4 || got.Items[0].Rank != 1 || got.Items[0].W != 300 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !fp.lastAt.IsZero() {
		t.Error("latest capture must pass zero time")
	}
	// at = unix seconds of the capture
	at := fp.items["pais"][0].TakenAt
	rec = do(h, "GET", "/api/frontpage?source=pais&at="+fpUnix(at.Unix()), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"rank":1`) {
		t.Fatal(rec.Code)
	}
	rec = do(h, "GET", "/api/frontpage?source=pais&at="+at.UTC().Format(time.RFC3339), nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	// unknown capture => empty list, not null
	rec = do(h, "GET", "/api/frontpage?source=pais&at=12345", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatal(rec.Body.String())
	}
	for _, p := range []string{"/api/frontpage", "/api/frontpage?source=pais&at=ayer"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 400 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	// by screenshot path: carries the true screenshot width
	d := time.Now().In(madrid)
	taken := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, madrid)
	st.shots = append(st.shots, domain.Screenshot{ID: 9, SourceID: "pais", TakenAt: taken, Path: "pais/" + taken.UTC().Format("2006/01/02") + "/0900.jpg", Width: 1366, Height: 5000})
	fp.items["pais"][0].TakenAt = taken
	for i := range fp.items["pais"] {
		fp.items["pais"][i].TakenAt = taken
	}
	rec = do(h, "GET", "/api/frontpage?source=pais&path="+"/shots/"+st.shots[len(st.shots)-1].Path, nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"width":1366`) {
		t.Fatalf("path lookup: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, "GET", "/api/frontpage?source=pais&path=pais/2020/01/01/0000.jpg", nil, ""); rec.Code != 404 {
		t.Errorf("unknown path: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/frontpage?source=abc&path=pais/2020/01/01/0000.jpg", nil, ""); rec.Code != 404 {
		t.Errorf("path of another source: %d", rec.Code)
	}
}

func fpUnix(n int64) string { return strconv.FormatInt(n, 10) }

func TestFrontpageTopAPI(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Prominence = newFakeProm() })
	rec := do(h, "GET", "/api/frontpage/top?hours=48&limit=5&source=pais", nil, "")
	var out struct {
		Hours int `json:"hours"`
		Items []struct {
			Source   string `json:"source"`
			Captures int    `json:"captures"`
		} `json:"items"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Hours != 48 || len(out.Items) != 1 || out.Items[0].Captures != 9 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, "GET", "/api/frontpage/top?source=nope", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Error(rec.Body.String())
	}
	for _, p := range []string{"hours=0", "hours=9999", "hours=x", "limit=0", "limit=101"} {
		if rec := do(h, "GET", "/api/frontpage/top?"+p, nil, ""); rec.Code != 400 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestStepChartHelpers(t *testing.T) {
	s := &server{loc: madrid}
	if s.stepChart(nil) != "" {
		t.Error("empty chart")
	}
	one := []domain.ItemPoint{{TakenAt: time.Now(), Rank: 1, Prominence: 1, Title: "x"}}
	if c := string(s.stepChart(one)); !strings.Contains(c, "<svg") || strings.Contains(c, "NaN") || strings.Contains(c, "Inf") {
		t.Errorf("single point chart: %s", c)
	}
	if humanDur(30*time.Second) == "" || humanDur(3*time.Hour) != "3,0 h" || humanDur(20*time.Minute) != "20 min" {
		t.Error("humanDur")
	}
}
