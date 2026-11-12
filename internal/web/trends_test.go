package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeTrends struct {
	report  *domain.TrendReport
	detail  map[string]*domain.TermDetail
	stories []domain.Story
	err     error
	last    domain.TrendOptions
	lastDay int
}

func (f *fakeTrends) Trending(_ context.Context, o domain.TrendOptions) (*domain.TrendReport, error) {
	f.last = o
	if f.err != nil {
		return nil, f.err
	}
	if f.report == nil {
		return &domain.TrendReport{GeneratedAt: time.Now(), Window: o.Window}, nil
	}
	return f.report, nil
}
func (f *fakeTrends) Term(_ context.Context, key string, days int, o domain.TrendOptions) (*domain.TermDetail, error) {
	f.last, f.lastDay = o, days
	if f.err != nil {
		return nil, f.err
	}
	return f.detail[key], nil
}
func (f *fakeTrends) Stories(_ context.Context, o domain.TrendOptions) ([]domain.Story, error) {
	f.last = o
	return f.stories, f.err
}

func newFakeTrends() *fakeTrends {
	now := time.Now()
	ref := func(id int64, src, title string, ago time.Duration) domain.ArticleRef {
		return domain.ArticleRef{ID: id, SourceID: src, Title: title, URL: "https://e.example/" + src, At: now.Add(-ago)}
	}
	cov := []domain.SourceCoverage{
		{SourceID: "pais", Name: "El País", Leaning: "centro-izquierda", Count: 2, FirstAt: now.Add(-5 * time.Hour), Headlines: []domain.ArticleRef{ref(1, "pais", "Vivienda <script>alert(1)</script>", 5*time.Hour)}},
		{SourceID: "abc", Name: "Medio <Derecha>", Leaning: "derecha", Count: 1, FirstAt: now.Add(-3 * time.Hour), Headlines: []domain.ArticleRef{ref(2, "abc", "Ley de Vivienda", 3*time.Hour)}},
		{SourceID: "pub", Name: "Público", Leaning: "izquierda", Count: 1, FirstAt: now.Add(-4 * time.Hour), Headlines: []domain.ArticleRef{ref(3, "pub", "Otro", 4*time.Hour)}},
	}
	miss := []domain.SourceCoverage{{SourceID: "mundo", Name: "ElMundo"}}
	return &fakeTrends{
		report: &domain.TrendReport{GeneratedAt: now, Window: 24 * time.Hour, Baseline: 14 * 24 * time.Hour, Articles: 321, Terms: []domain.Term{
			{Term: "Ley de Vivienda", Key: "ley de vivienda", Kind: "phrase", Count: 12, Sources: 4, Score: 9.1, FirstAt: now.Add(-6 * time.Hour),
				Leanings: map[string]int{"izquierda": 3, "derecha": 2}, Spark: []int{0, 0, 1, 0, 2, 1, 3, 2, 4, 3, 5, 6, 8, 12}},
			{Term: "<img src=x onerror=alert(1)>", Key: "xss", Kind: "word", Count: 5, Sources: 2, New: true, Leanings: map[string]int{"centro": 5}, Spark: []int{0, 0, 5}},
		}},
		detail: map[string]*domain.TermDetail{
			"vivienda": {Term: "Vivienda <b>", Key: "vivienda", Total: 4, Coverage: cov, Missing: miss,
				Daily:   []domain.DayCount{{Day: now.AddDate(0, 0, -1), Total: 1, Leanings: map[string]int{"izquierda": 1}}, {Day: now, Total: 3, Leanings: map[string]int{"derecha": 1, "centro": 1}}},
				Related: []string{"alquiler", "<i>x</i>"}},
			"ley de vivienda": {Term: "Ley de Vivienda", Key: "ley de vivienda", Total: 2, Coverage: cov[:1]},
		},
		stories: []domain.Story{{ID: "s1", Title: "El Gobierno aprueba <b>la ley</b>", Terms: []string{"Ley de Vivienda"}, Sources: 3, Articles: 4,
			Leanings: map[string]int{"izquierda": 1, "centro-izquierda": 2, "derecha": 1}, FirstAt: now.Add(-5 * time.Hour), LastAt: now, FirstBy: "pais",
			Coverage: cov, Missing: miss}},
	}
}

func TestTrendsPages(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	b := do(h, "GET", "/tendencias", nil, "").Body.String()
	for _, s := range []string{"Historias destacadas", "3 medios", "primera: <strong>El País</strong>", "No lo cubren: ElMundo", "Comparar enfoques",
		"/preguntar?comparar=1&amp;q=", `class="lbar"`, "Reparto por línea editorial", `rel="noopener noreferrer"`, `href="/tendencias"`} {
		if !strings.Contains(b, s) {
			t.Errorf("stories: missing %q", s)
		}
	}
	// outlets ordered by leaning: Público (izq) < El País < Medio (der)
	b0 := b[strings.Index(b, `class="outlets"`):]
	i, j, k := strings.Index(b0, "Público"), strings.Index(b0, "El País"), strings.Index(b0, "Medio &lt;Derecha&gt;")
	if !(i > 0 && i < j && j < k) {
		t.Errorf("outlets not ordered by leaning %d %d %d", i, j, k)
	}
	b = do(h, "GET", "/tendencias?vista=terminos&ventana=7d", nil, "").Body.String()
	for _, s := range []string{"Ley de Vivienda", `badge new`, `class="spark"`, "/tendencias/termino/ley%20de%20vivienda", "321 artículos"} {
		if !strings.Contains(b, s) {
			t.Errorf("terms: missing %q", s)
		}
	}
	b = do(h, "GET", "/tendencias/termino/vivienda?dias=14", nil, "").Body.String()
	for _, s := range []string{`class="dchart"`, "No lo han cubierto", "ElMundo", "alquiler", "Comparar enfoques", "Ver datos en tabla", "(&#43;1 h)"} {
		if !strings.Contains(b, s) {
			t.Errorf("term: missing %q", s)
		}
	}
	// the nav item is shown
	if !strings.Contains(b, `<span>Tendencias</span>`) {
		t.Error("nav item missing")
	}
}

func TestTrendsSourcesAndWindowPassed(t *testing.T) {
	ft := newFakeTrends()
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = ft })
	do(h2, "GET", "/tendencias?vista=terminos&ventana=6h&fuentes=pais&fuentes=abc", nil, "")
	if ft.last.Window != 6*time.Hour || len(ft.last.SourceIDs) != 2 {
		t.Errorf("opts: %+v", ft.last)
	}
	do(h2, "GET", "/tendencias/termino/vivienda?dias=500", nil, "")
	if ft.lastDay != defaultTermDays {
		t.Errorf("days %d", ft.lastDay)
	}
	do(h2, "GET", "/tendencias/termino/vivienda?dias=14", nil, "")
	if ft.lastDay != 14 {
		t.Errorf("days %d", ft.lastDay)
	}
}

func TestTrendsVentanaValidation(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, v := range []string{"6h", "24h", "48h", "7d"} {
		if rec := do(h, "GET", "/tendencias?ventana="+v, nil, ""); rec.Code != 200 {
			t.Errorf("%s: %d", v, rec.Code)
		}
		if rec := do(h, "GET", "/api/trends?ventana="+v, nil, ""); rec.Code != 200 {
			t.Errorf("api %s: %d", v, rec.Code)
		}
	}
	for _, p := range []string{"/tendencias?ventana=1y", "/api/trends?ventana=5m", "/api/stories?ventana=x", "/tendencias?ventana=24H"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 400 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestTrendsTerm404(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, p := range []string{"/tendencias/termino/nada", "/tendencias/termino/%20", "/tendencias/termino/" + strings.Repeat("a", 300), "/tendencias/termino/"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/trends/term?key=nada", nil, ""); rec.Code != 404 {
		t.Errorf("api unknown: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/trends/term", nil, ""); rec.Code != 400 {
		t.Errorf("api missing key: %d", rec.Code)
	}
}

func TestTrendsAPIShape(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	var rep map[string]any
	rec := do(h, "GET", "/api/trends?ventana=24h&limit=1", nil, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"generated_at", "window_hours", "baseline_hours", "articles", "terms"} {
		if _, ok := rep[k]; !ok {
			t.Errorf("missing %s: %v", k, rep)
		}
	}
	terms := rep["terms"].([]any)
	if len(terms) != 1 || rep["window_hours"].(float64) != 24 || rep["baseline_hours"].(float64) != 336 {
		t.Errorf("terms/limit/hours: %v", rep)
	}
	t0 := terms[0].(map[string]any)
	for _, k := range []string{"term", "key", "kind", "count", "sources", "baseline_rate", "score", "new", "first_at", "leanings", "spark"} {
		if _, ok := t0[k]; !ok {
			t.Errorf("term missing %s", k)
		}
	}
	if _, err := time.Parse(time.RFC3339, t0["first_at"].(string)); err != nil {
		t.Errorf("first_at: %v", err)
	}

	var td map[string]any
	rec = do(h, "GET", "/api/trends/term?key=vivienda&dias=7", nil, "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &td) != nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, k := range []string{"term", "key", "days", "total", "coverage", "missing", "daily", "related"} {
		if _, ok := td[k]; !ok {
			t.Errorf("detail missing %s", k)
		}
	}
	cov := td["coverage"].([]any)[0].(map[string]any)
	for _, k := range []string{"source_id", "name", "count", "first_at", "headlines"} {
		if _, ok := cov[k]; !ok {
			t.Errorf("coverage missing %s", k)
		}
	}
	var st map[string]any
	rec = do(h, "GET", "/api/stories?ventana=48h", nil, "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
		t.Fatal(rec.Code)
	}
	s0 := st["stories"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "title", "terms", "sources", "articles", "leanings", "first_at", "last_at", "first_by", "score", "coverage", "missing"} {
		if _, ok := s0[k]; !ok {
			t.Errorf("story missing %s", k)
		}
	}
	// empty lists encode as [] not null
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = &fakeTrends{} })
	if b := do(h2, "GET", "/api/stories", nil, "").Body.String(); !strings.Contains(b, `"stories":[]`) {
		t.Errorf("stories null: %s", b)
	}
	if b := do(h2, "GET", "/api/trends", nil, "").Body.String(); !strings.Contains(b, `"terms":[]`) {
		t.Errorf("terms null: %s", b)
	}
	// limit capped
	ft := newFakeTrends()
	h3, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = ft })
	do(h3, "GET", "/api/trends?limit=99999", nil, "")
	if ft.last.Limit != maxTrendLimit {
		t.Errorf("limit %d", ft.last.Limit)
	}
}

func TestTrendsNilProvider(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = nil })
	for _, p := range []string{"/tendencias", "/tendencias/termino/vivienda", "/api/trends", "/api/trends/term?key=a", "/api/stories"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 503 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if b := do(h, "GET", "/", nil, "").Body.String(); strings.Contains(b, "/tendencias") {
		t.Error("nav item should be hidden")
	}
	if b := do(h, "GET", "/tendencias", nil, "").Body.String(); !strings.Contains(b, "no disponible") {
		t.Error("expected 'no disponible' message")
	}
}

func TestTrendsProviderError(t *testing.T) {
	ft := &fakeTrends{err: errors.New("secret db path /var/x")}
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = ft })
	for _, p := range []string{"/tendencias", "/tendencias?vista=terminos", "/tendencias/termino/a", "/api/trends", "/api/trends/term?key=a", "/api/stories"} {
		rec := do(h, "GET", p, nil, "")
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestTrendsXSS(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, p := range []string{"/tendencias", "/tendencias?vista=terminos", "/tendencias/termino/vivienda", "/tendencias?vista=terminos&fuentes=%3Cscript%3E"} {
		b := do(h, "GET", p, nil, "").Body.String()
		for _, bad := range []string{"<script>alert", "<img src=x", "<b>la ley</b>", "<i>x</i>", "Vivienda <b>"} {
			if strings.Contains(b, bad) {
				t.Errorf("%s: unescaped %q", p, bad)
			}
		}
	}
	// SVG attribute context stays escaped (term in spark aria-label comes from numbers only)
	b := do(h, "GET", "/tendencias/termino/vivienda", nil, "").Body.String()
	if !strings.Contains(b, "Vivienda &lt;b&gt;") {
		t.Error("term name not escaped")
	}
	// href with javascript: scheme is neutralised
	ft := newFakeTrends()
	ft.detail["vivienda"].Coverage[0].Headlines[0].URL = "javascript:alert(1)"
	h2, _, _, _ := newHandler(t, func(d *Deps, _ *Options) { d.Trends = ft })
	if b := do(h2, "GET", "/tendencias/termino/vivienda", nil, "").Body.String(); strings.Contains(b, `href="javascript:`) {
		t.Error("javascript: URL not neutralised")
	}
}

func TestAskPrefill(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	q := url.Values{"q": {`Ley <b>de</b> "Vivienda"`}, "comparar": {"1"}}
	b := do(h, "GET", "/preguntar?"+q.Encode(), nil, "").Body.String()
	if !strings.Contains(b, `Ley &lt;b&gt;de&lt;/b&gt; &#34;Vivienda&#34;</textarea>`) {
		t.Errorf("textarea not prefilled/escaped: %s", b)
	}
	if !strings.Contains(b, `id="compare" checked`) {
		t.Error("compare not ticked")
	}
	b = do(h, "GET", "/preguntar", nil, "").Body.String()
	if strings.Contains(b, `id="compare" checked`) || !strings.Contains(b, `></textarea>`) {
		t.Error("default should be empty/unticked")
	}
}

func TestSVGHelpers(t *testing.T) {
	if leanBarSVG(nil) != "" || sparkSVG(nil) != "" || dailyChartSVG(nil, madrid) != "" {
		t.Error("empty inputs should render nothing")
	}
	if s := string(leanBarSVG(map[string]int{"x": 2, "<": 0})); !strings.Contains(s, "Sin clasificar 2") {
		t.Error(s)
	}
	if s := string(sparkSVG([]int{0, 0, 0})); !strings.Contains(s, "<polyline") || strings.Contains(s, "NaN") {
		t.Error(s)
	}
	if s := string(sparkSVG([]int{4})); strings.Contains(s, "NaN") {
		t.Error(s)
	}
}
