package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeDetail struct {
	*fakeTrends
	arts []domain.ArticleLite
}

func (f fakeDetail) StoryArticles(_ context.Context, id string, _ domain.TrendOptions) ([]domain.ArticleLite, error) {
	return f.arts, nil
}
func (f fakeDetail) WindowArticles(context.Context, domain.TrendOptions) ([]domain.ArticleLite, error) {
	return nil, nil
}

func framingFake() fakeDetail {
	ft := newFakeTrends()
	now := time.Now()
	l := func(id int64, src, title, sum string) domain.ArticleLite {
		return domain.ArticleLite{ID: id, SourceID: src, Title: title, Summary: sum, URL: "https://e.example/" + src, At: now.Add(-time.Duration(id) * time.Minute)}
	}
	// right-only story with 3 right outlets covering and left/centre absent
	ft.stories = []domain.Story{
		{ID: "s1", Title: "Titular <script>alert(1)</script> de la ley", Sources: 3, Articles: 3, Leanings: map[string]int{"derecha": 2, "centro-derecha": 1}, FirstAt: now,
			Coverage: []domain.SourceCoverage{{SourceID: "d1", Name: "Medio <b>D1</b>", Leaning: "derecha", Count: 1}, {SourceID: "d2", Name: "D2", Leaning: "derecha", Count: 1}, {SourceID: "d3", Name: "D3", Leaning: "centro-derecha", Count: 1}},
			Missing:  []domain.SourceCoverage{{SourceID: "i1", Name: "I1", Leaning: "izquierda"}, {SourceID: "i2", Name: "I2", Leaning: "centro-izquierda"}, {SourceID: "c1", Name: "C1", Leaning: "centro"}, {SourceID: "c2", Name: "C2", Leaning: "centro"}}},
		{ID: "s2", Title: "Equilibrada", Sources: 4, Articles: 4, Leanings: map[string]int{"derecha": 2, "izquierda": 2}, FirstAt: now,
			Coverage: []domain.SourceCoverage{{SourceID: "d1", Name: "D1", Leaning: "derecha"}, {SourceID: "d2", Name: "D2", Leaning: "derecha"}, {SourceID: "i1", Name: "I1", Leaning: "izquierda"}, {SourceID: "i2", Name: "I2", Leaning: "centro-izquierda"}},
			Missing:  []domain.SourceCoverage{{SourceID: "c1", Name: "C1", Leaning: "centro"}, {SourceID: "c2", Name: "C2", Leaning: "centro"}}},
	}
	return fakeDetail{ft, []domain.ArticleLite{
		l(1, "d1", "Golpe <img src=x onerror=alert(1)> a la ley de vivienda", "Los propietarios <b>protestan</b>"),
		l(2, "d2", "La ley de vivienda ataca a los propietarios", "propietarios"),
		l(3, "d3", "Aprobada la ley de vivienda", "texto"),
	}}
}

func TestFramingPages(t *testing.T) {
	fd := framingFake()
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Trends = fd })
	rec := do(h, "GET", "/encuadre", nil, "")
	b := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, b)
	}
	for _, s := range []string{"Puntos ciegos", "Historias comparadas", "Cubierta por 3 medios de Derecha", "No aparece en nuestras fuentes de Izquierda ni Centro", `href="/encuadre/s1"`, `class="rbar"`} {
		if !strings.Contains(b, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(b, "<script>alert(1)") || strings.Contains(b, "Medio <b>") || strings.Contains(b, "Equilibrada") {
		t.Errorf("xss or wrong story in blind list:\n%s", b)
	}
	if !strings.Contains(do(h, "GET", "/encuadre?vista=comparadas", nil, "").Body.String(), "Equilibrada") {
		t.Error("compared view lacks balanced story")
	}
	if do(h, "GET", "/encuadre?ventana=3h", nil, "").Code != 400 {
		t.Error("bad window")
	}
	if !strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/encuadre") {
		t.Error("nav item missing")
	}
	// htmx fragment
	fr := do(h, "GET", "/encuadre?bloques=5", map[string]string{"HX-Request": "true"}, "").Body.String()
	if strings.Contains(fr, "<html") || !strings.Contains(fr, "Puntos ciegos") {
		t.Error("fragment")
	}

	rec = do(h, "GET", "/encuadre/s1", nil, "")
	b = rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, b)
	}
	for _, s := range []string{`class="lbar"`, "Titulares comparados", "Vocabulario distintivo", "Pedir al modelo que contraste el enfoque", "/preguntar?comparar=1", `class="w uniq"`, "no detectada en"} {
		if !strings.Contains(b, s) {
			t.Errorf("detail missing %q", s)
		}
	}
	if strings.Contains(b, "<img src=x") || strings.Contains(b, "<script>alert") || strings.Contains(b, "<b>protestan") {
		t.Errorf("xss in detail")
	}
	if do(h, "GET", "/encuadre/nope", nil, "").Code != 404 || do(h, "GET", "/encuadre/a%3Cb", nil, "").Code != 404 {
		t.Error("404 expected")
	}
	if !strings.Contains(do(h, "GET", "/tendencias", nil, "").Body.String(), "Ver encuadre") {
		t.Error("trends link")
	}
}

func TestFramingUnavailable(t *testing.T) {
	h, _, _, _ := newHandler(t, nil) // fake without StoryDetailProvider
	for _, p := range []string{"/encuadre", "/encuadre/s1", "/api/blindspots", "/api/framing/s1"} {
		if c := do(h, "GET", p, nil, "").Code; c != 503 {
			t.Errorf("%s: %d", p, c)
		}
	}
	if strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/encuadre") {
		t.Error("nav should be hidden")
	}
}

func TestFramingAPI(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Trends = framingFake() })
	rec := do(h, "GET", "/api/blindspots?todas=1", nil, "")
	var bs struct {
		Spots   []storyCovJSON `json:"blind_spots"`
		Stories []storyCovJSON `json:"stories"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &bs) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(bs.Spots) != 1 || bs.Spots[0].ID != "s1" || !bs.Spots[0].Blind || len(bs.Stories) != 2 || len(bs.Spots[0].Absent) != 2 {
		t.Fatalf("%+v", bs)
	}
	rec = do(h, "GET", "/api/framing/s1", nil, "")
	var f map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &f) != nil || f["consensus"] == "" || len(f["headlines"].([]any)) != 3 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if do(h, "GET", "/api/framing/zzz", nil, "").Code != 404 || do(h, "GET", "/api/blindspots?ventana=x", nil, "").Code != 400 {
		t.Error("errors")
	}
}
