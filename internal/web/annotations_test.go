package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeAnno struct {
	rows       []domain.Annotation
	arts       map[int64]domain.Article
	lastQ      domain.AnnotationQuery
	lastListQ  domain.AnnotationQuery
	distCalled int
}

func (f *fakeAnno) SaveAnnotation(context.Context, domain.Annotation) error { return nil }
func (f *fakeAnno) ListAnnotations(_ context.Context, id int64) ([]domain.Annotation, error) {
	var out []domain.Annotation
	for _, r := range f.rows {
		if r.ArticleID == id {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeAnno) ArticlesNeedingAnnotation(context.Context, string, string, int) ([]domain.Article, error) {
	return nil, nil
}
func (f *fakeAnno) CountAnnotationsByLabel(context.Context, string, string, domain.Filter) (map[string]int64, error) {
	return nil, nil
}
func (f *fakeAnno) AnnotationModels(_ context.Context, kind string) ([]domain.AnnotationModelInfo, error) {
	n := map[string]int64{}
	for _, r := range f.rows {
		if r.Kind == kind {
			n[r.Model]++
		}
	}
	var out []domain.AnnotationModelInfo
	for m, c := range n {
		out = append(out, domain.AnnotationModelInfo{Model: m, Count: c})
	}
	return out, nil
}
func (f *fakeAnno) AnnotationDistribution(_ context.Context, q domain.AnnotationQuery) ([]domain.AnnotationCount, error) {
	f.lastQ = q
	f.distCalled++
	m := map[[2]string]int64{}
	for _, r := range f.rows {
		if r.Kind != q.Kind || r.Model != q.Model || r.Score < q.MinScore {
			continue
		}
		m[[2]string{f.arts[r.ArticleID].SourceID, r.Label}]++
	}
	var out []domain.AnnotationCount
	for k, n := range m {
		out = append(out, domain.AnnotationCount{SourceID: k[0], Label: k[1], N: n})
	}
	return out, nil
}
func (f *fakeAnno) ListAnnotated(_ context.Context, q domain.AnnotationQuery) ([]domain.AnnotatedArticle, error) {
	f.lastListQ = q
	var out []domain.AnnotatedArticle
	for _, r := range f.rows {
		if r.Kind == q.Kind && r.Model == q.Model && r.Label == q.Label && r.Score >= q.MinScore {
			a := f.arts[r.ArticleID]
			ok := len(q.SourceIDs) == 0
			for _, s := range q.SourceIDs {
				ok = ok || s == a.SourceID
			}
			if ok {
				out = append(out, domain.AnnotatedArticle{Article: a, Label: r.Label, Score: r.Score})
			}
		}
	}
	return out, nil
}

func newFakeAnno() *fakeAnno {
	f := &fakeAnno{arts: map[int64]domain.Article{
		1: {ID: 1, SourceID: "pais", Title: "Titular <script>alert(1)</script>"},
		2: {ID: 2, SourceID: "abc", Title: "Otro titular"},
		3: {ID: 3, SourceID: "abc", Title: "Tercero"},
	}}
	add := func(id int64, kind, label string, score float64, data string) {
		f.rows = append(f.rows, domain.Annotation{ArticleID: id, Kind: kind, Model: "qwen<b>", Label: label, Score: score, Data: data})
	}
	add(1, "tone", "crítico", 0.9, `{"rationale":"<img src=x onerror=alert(1)> juicio"}`)
	add(1, "intent", "opinar", 0.9, `{}`)
	add(1, "frame", "conflicto", 0.9, `{}`)
	add(1, "opinion", "sí", 0.9, `{}`)
	add(2, "tone", "neutral", 0.8, `{}`)
	add(3, "tone", "alarmista", 0.3, `{}`) // below the default min confidence
	return f
}

func withAnno(f *fakeAnno, enabled bool) func(*Deps, *Options) {
	return func(d *Deps, _ *Options) {
		d.Annotations = f
		d.AnnotationsEnabled = enabled
		d.AnnotationModel = "qwen<b>"
	}
}

func TestAnnotationsPage(t *testing.T) {
	f := newFakeAnno()
	h, _, _, _ := newHandler(t, withAnno(f, true))
	rec := do(h, "GET", "/anotaciones", nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	for _, s := range []string{"estimaciones automáticas", "Por línea editorial", "Por medio", "n = 1", "muestra pequeña", "qwen&lt;b&gt;",
		"/anotaciones/lista?", "label=cr%C3%ADtico", "Confianza mínima", `href="/anotaciones"`} {
		if !strings.Contains(b, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(b, "qwen<b>") || strings.Contains(b, "<script>alert") {
		t.Error("unescaped data")
	}
	if f.lastQ.MinScore != 0.6 {
		t.Errorf("default min confidence %v", f.lastQ.MinScore)
	}
	if strings.Contains(b, "alarmista</a>") || strings.Contains(b, "label=alarmista") {
		t.Error("low-confidence annotation leaked into the chart")
	}
	do(h, "GET", "/anotaciones?dias=30&conf=0.2", nil, "")
	if f.lastQ.MinScore != 0.2 || time.Since(f.lastQ.From) < 29*24*time.Hour {
		t.Errorf("params not applied: %+v", f.lastQ)
	}
	do(h, "GET", "/anotaciones?conf=zzz&dias=999", nil, "")
	if f.lastQ.MinScore != 0.6 {
		t.Errorf("bad conf must fall back: %v", f.lastQ.MinScore)
	}
	do(h, "GET", "/anotaciones?conf=5", nil, "")
	if f.lastQ.MinScore != annoMaxMin {
		t.Errorf("conf must be clamped: %v", f.lastQ.MinScore)
	}
}

func TestAnnotationsEmptyAndHidden(t *testing.T) {
	// enabled, nothing yet: friendly empty state, nav visible
	h, _, _, _ := newHandler(t, withAnno(&fakeAnno{}, true))
	rec := do(h, "GET", "/anotaciones", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Todavía no hay noticias anotadas") || !strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/anotaciones") {
		t.Fatal(rec.Code)
	}
	// disabled and empty: hidden
	h, _, _, _ = newHandler(t, withAnno(&fakeAnno{}, false))
	if c := do(h, "GET", "/anotaciones", nil, "").Code; c != 503 {
		t.Fatal(c)
	}
	if c := do(h, "GET", "/api/annotations/summary", nil, "").Code; c != 503 {
		t.Fatal(c)
	}
	if strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/anotaciones") {
		t.Fatal("nav must be hidden")
	}
	// disabled but history exists: still readable
	h, _, _, _ = newHandler(t, withAnno(newFakeAnno(), false))
	if c := do(h, "GET", "/anotaciones", nil, "").Code; c != 200 {
		t.Fatal(c)
	}
	// no store at all
	h, _, _, _ = newHandler(t, nil)
	if c := do(h, "GET", "/anotaciones", nil, "").Code; c != 503 {
		t.Fatal(c)
	}
	if c := do(h, "GET", "/fragmentos/anotaciones/1", nil, "").Code; c != 204 {
		t.Fatal(c)
	}
}

func TestAnnotationList(t *testing.T) {
	f := newFakeAnno()
	h, _, _, _ := newHandler(t, withAnno(f, true))
	rec := do(h, "GET", "/anotaciones/lista?kind=tone&label=cr%C3%ADtico&source=pais&conf=0.6", nil, "")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "/articulo/1") || strings.Contains(b, "<script>alert") || !strings.Contains(b, "confianza 90 %") {
		t.Fatal(rec.Code, b)
	}
	if len(f.lastListQ.SourceIDs) != 1 || f.lastListQ.SourceIDs[0] != "pais" {
		t.Fatalf("%+v", f.lastListQ)
	}
	do(h, "GET", "/anotaciones/lista?kind=tone&label=neutral&leaning=Derecha", nil, "")
	if len(f.lastListQ.SourceIDs) != 1 || f.lastListQ.SourceIDs[0] != "abc" {
		t.Fatalf("leaning not resolved: %+v", f.lastListQ)
	}
	for _, u := range []string{"/anotaciones/lista", "/anotaciones/lista?kind=tone&label=inventado", "/anotaciones/lista?kind=x&label=neutral", "/anotaciones/lista?kind=tone&label=%3Cscript%3E"} {
		if c := do(h, "GET", u, nil, "").Code; c != 400 {
			t.Errorf("%s: %d", u, c)
		}
	}
}

func TestAnnotationChipsFragment(t *testing.T) {
	h, _, _, _ := newHandler(t, withAnno(newFakeAnno(), true))
	rec := do(h, "GET", "/fragmentos/anotaciones/1", nil, "")
	b := rec.Body.String()
	for _, s := range []string{"Tono:", "crítico", "Intención:", "opinar", "Encuadre:", "conflicto", "Opinión:", "estimación automática", "Confianza 90 %", "modelo qwen&lt;b&gt;"} {
		if !strings.Contains(b, s) {
			t.Errorf("missing %q in %s", s, b)
		}
	}
	if strings.Contains(b, "<img src=x") || strings.Contains(b, "qwen<b>") {
		t.Error("unescaped rationale/model")
	}
	if c := do(h, "GET", "/fragmentos/anotaciones/2", nil, "").Code; c != 200 {
		t.Fatal(c)
	}
	if c := do(h, "GET", "/fragmentos/anotaciones/99", nil, "").Code; c != 204 {
		t.Fatal(c)
	}
	if c := do(h, "GET", "/fragmentos/anotaciones/abc", nil, "").Code; c != 204 {
		t.Fatal(c)
	}
	if !strings.Contains(do(h, "GET", "/articulo/1", nil, "").Body.String(), "/fragmentos/anotaciones/1") {
		t.Fatal("article page lacks the chips hook")
	}
}

func TestAnnotationsAPI(t *testing.T) {
	h, _, _, _ := newHandler(t, withAnno(newFakeAnno(), true))
	rec := do(h, "GET", "/api/annotations/summary?conf=0.5", nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var out struct {
		Model     string  `json:"model"`
		MinConf   float64 `json:"min_confidence"`
		Annotated int64   `json:"annotated"`
		Dims      []struct {
			Kind    string `json:"kind"`
			Total   int64  `json:"total"`
			Outlets []struct {
				ID     string           `json:"id"`
				N      int64            `json:"n"`
				Counts map[string]int64 `json:"counts"`
			} `json:"by_outlet"`
			Leanings []struct {
				Name string `json:"name"`
				N    int64  `json:"n"`
			} `json:"by_leaning"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "qwen<b>" || out.MinConf != 0.5 || len(out.Dims) != 3 || out.Dims[0].Kind != "tone" || out.Dims[0].Total != 2 || out.Annotated != 2 {
		t.Fatalf("%+v", out)
	}
	if len(out.Dims[0].Outlets) != 2 || len(out.Dims[0].Leanings) != 2 {
		t.Fatalf("%+v", out.Dims[0])
	}
}

func TestAnnoAggregationMath(t *testing.T) {
	f := newFakeAnno()
	h, _, _, _ := newHandler(t, withAnno(f, true))
	_ = h
	// two outlets share a leaning bucket: counts must be summed, percentages derived from n
	f.arts[4] = domain.Article{ID: 4, SourceID: "pub", Title: "x"}
	f.arts[5] = domain.Article{ID: 5, SourceID: "pais", Title: "y"}
	f.rows = append(f.rows,
		domain.Annotation{ArticleID: 4, Kind: "tone", Model: "qwen<b>", Label: "neutral", Score: 0.9},
		domain.Annotation{ArticleID: 5, Kind: "tone", Model: "qwen<b>", Label: "fuera-de-vocabulario", Score: 0.9})
	h2, _, _, _ := newHandler(t, withAnno(f, true))
	b := do(h2, "GET", "/anotaciones", nil, "").Body.String()
	if strings.Contains(b, "fuera-de-vocabulario") {
		t.Error("out-of-vocabulary label shown")
	}
	// 'abc' has 1 neutral at >=0.6 (the 0.3 alarmista is dropped): 100 %
	if !strings.Contains(b, "neutral: 1 (100 %)") {
		t.Error("percent math")
	}
}
