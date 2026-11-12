package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ---- fakes ----

type fakeStore struct {
	domain.Store // nil: unimplemented methods panic (caught by tests if used)
	sources      []domain.Source
	arts         []domain.Article
	shots        []domain.Screenshot
	feeds        []domain.FeedState
	jobs         []domain.JobRun
}

func (f *fakeStore) ListSources(context.Context) ([]domain.Source, error) { return f.sources, nil }
func (f *fakeStore) ListArticles(_ context.Context, fl domain.Filter) ([]domain.Article, error) {
	var out []domain.Article
	for _, a := range f.arts {
		if len(fl.SourceIDs) > 0 && !contains(fl.SourceIDs, a.SourceID) {
			continue
		}
		out = append(out, a)
	}
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}
func (f *fakeStore) CountArticles(context.Context, domain.Filter) (int64, error) {
	return int64(len(f.arts)), nil
}
func (f *fakeStore) GetArticle(_ context.Context, id int64) (*domain.Article, error) {
	for i := range f.arts {
		if f.arts[i].ID == id {
			return &f.arts[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeStore) ListScreenshots(_ context.Context, src string, from, to time.Time) ([]domain.Screenshot, error) {
	var out []domain.Screenshot
	for _, s := range f.shots {
		if s.SourceID == src && !s.TakenAt.Before(from) && s.TakenAt.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeStore) ListFeedStates(context.Context) ([]domain.FeedState, error) { return f.feeds, nil }
func (f *fakeStore) ListJobRuns(context.Context, int) ([]domain.JobRun, error)  { return f.jobs, nil }
func (f *fakeStore) Stats(context.Context, string) (*domain.Stats, error) {
	return &domain.Stats{Articles: 3, Embedded: 2, PendingEmbed: 1, Screenshots: 2, DBBytes: 2048, ShotsBytes: 4 << 20}, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type fakeRetriever struct {
	hits    []domain.Hit
	mode    domain.SearchMode
	askErr  error
	deltas  []string
	lastReq domain.AskRequest
}

func (r *fakeRetriever) Search(_ context.Context, q string, _ domain.Filter, k int) ([]domain.Hit, domain.SearchMode, error) {
	h := r.hits
	if len(h) > k {
		h = h[:k]
	}
	return h, r.mode, nil
}
func (r *fakeRetriever) Ask(_ context.Context, req domain.AskRequest, onSources func([]domain.Hit), onDelta func(string) error) error {
	r.lastReq = req
	onSources(r.hits)
	for _, d := range r.deltas {
		if err := onDelta(d); err != nil {
			return err
		}
	}
	return r.askErr
}

type blockingJob struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingJob) RunOnce(ctx context.Context) (int, error) {
	b.started <- struct{}{}
	<-b.release
	return 1, nil
}

type fakeShots struct{ blockingJob }

func (f *fakeShots) Cleanup(context.Context) (int, error) { return 0, nil }

type fakeLLM struct{ ok bool }

func (f fakeLLM) LLMHealth(context.Context) (domain.Health, domain.Health) {
	h := domain.Health{OK: f.ok, Detail: "http://mac:11434", CheckedAt: time.Now()}
	return h, h
}

var madrid, _ = time.LoadLocation("Europe/Madrid")

func fixture(t *testing.T) (*fakeStore, *fakeRetriever, string) {
	t.Helper()
	now := time.Now()
	st := &fakeStore{
		sources: []domain.Source{
			{ID: "abc", Name: "Medio <Derecha>", Leaning: "derecha", Screenshot: true},
			{ID: "pais", Name: "El País", Leaning: "centro-izquierda", Screenshot: true},
			{ID: "pub", Name: "Público", Leaning: "izquierda"},
		},
		arts: []domain.Article{
			{ID: 1, SourceID: "pais", URL: "https://e.example/1", Title: "Titular <script>alert(1)</script>", Summary: "Resumen", Content: "Línea uno\n\nLínea dos", Section: "España", PublishedAt: now.Add(-10 * time.Minute), FirstSeenAt: now},
			{ID: 2, SourceID: "abc", URL: "https://e.example/2", Title: "Otro titular", Summary: "Solo resumen", FirstSeenAt: now.Add(-3 * time.Hour)},
		},
		feeds: []domain.FeedState{{SourceID: "pais", FeedURL: "https://e.example/rss", LastOK: now, LastError: "boom <b>", LastErrorAt: now.Add(-time.Hour)}},
		jobs:  []domain.JobRun{{ID: 1, Job: "ingest", StartedAt: now, FinishedAt: now, OK: true, Message: "3 nuevos"}},
	}
	day := time.Now().In(madrid)
	d := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, madrid)
	st.shots = []domain.Screenshot{
		{ID: 1, SourceID: "pais", TakenAt: d, Path: "pais/a b/0900.jpg"},
		{ID: 2, SourceID: "abc", TakenAt: d.Add(8 * time.Hour), Path: "abc/1700.jpg"},
	}
	ret := &fakeRetriever{mode: domain.ModeHybrid, deltas: []string{"Hola ", "mundo [1]\nlinea2"}, hits: []domain.Hit{
		{Article: st.arts[0], Score: 0.9, Snippet: `un <mark>titular</mark> <img src=x onerror=alert(1)> y </mark><mark>`},
		{Article: st.arts[1], Score: 0.5, Chunk: -1},
	}}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pais"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pais", "x.jpg"), []byte("JPEGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(dir), "secret.jpg")
	_ = os.WriteFile(secret, []byte("SECRET"), 0o644)
	return st, ret, dir
}

func newHandler(t *testing.T, mod func(*Deps, *Options)) (http.Handler, *fakeStore, *fakeRetriever, string) {
	st, ret, dir := fixture(t)
	d := Deps{Store: st, Retriever: ret, Ingestor: &blockingJob{make(chan struct{}, 1), make(chan struct{})},
		Shots: &fakeShots{blockingJob{make(chan struct{}, 1), make(chan struct{})}}, LLM: fakeLLM{ok: true}, Trends: newFakeTrends(), ShotsDir: dir,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	o := Options{Location: madrid, Version: "test", EmbedModel: "m"}
	if mod != nil {
		mod(&d, &o)
	}
	return New(d, o), st, ret, dir
}

func do(h http.Handler, method, target string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---- tests ----

var allPages = []string{"/", "/fragmentos/portada", "/buscar", "/buscar?q=algo", "/preguntar", "/capturas", "/capturas?mode=compare",
	"/estado", "/fragmentos/estado", "/articulo/1", "/offline",
	"/tendencias", "/tendencias?vista=terminos&ventana=7d", "/tendencias/termino/vivienda"}

func TestPagesRenderPopulated(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, p := range allPages {
		rec := do(h, "GET", p, nil, "")
		if rec.Code != 200 {
			t.Errorf("%s: status %d: %s", p, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s: missing CSP", p)
		}
	}
}

type emptyStore struct{ domain.Store }

func (emptyStore) ListSources(context.Context) ([]domain.Source, error) { return nil, nil }
func (emptyStore) ListArticles(context.Context, domain.Filter) ([]domain.Article, error) {
	return nil, nil
}
func (emptyStore) ListScreenshots(context.Context, string, time.Time, time.Time) ([]domain.Screenshot, error) {
	return nil, nil
}
func (emptyStore) CountArticles(context.Context, domain.Filter) (int64, error) { return 0, nil }
func (emptyStore) ListFeedStates(context.Context) ([]domain.FeedState, error)  { return nil, nil }
func (emptyStore) ListJobRuns(context.Context, int) ([]domain.JobRun, error)   { return nil, nil }
func (emptyStore) Stats(context.Context, string) (*domain.Stats, error)        { return &domain.Stats{}, nil }
func (emptyStore) GetArticle(context.Context, int64) (*domain.Article, error)  { return nil, nil }

func TestPagesRenderEmpty(t *testing.T) {
	h := New(Deps{Store: emptyStore{}, Trends: &fakeTrends{}, ShotsDir: t.TempDir()}, Options{})
	for _, p := range allPages {
		want := 200
		if strings.HasPrefix(p, "/articulo") || strings.HasPrefix(p, "/tendencias/termino") {
			want = 404
		}
		if rec := do(h, "GET", p, nil, ""); rec.Code != want {
			t.Errorf("%s: status %d (want %d): %s", p, rec.Code, want, rec.Body.String())
		}
	}
	// api with empty data
	for _, p := range []string{"/api/articles", "/api/screenshots", "/api/status"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/search?q=x", nil, ""); rec.Code != 503 {
		t.Errorf("search without retriever: %d", rec.Code)
	}
}

func TestHomeContent(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	body := do(h, "GET", "/", nil, "").Body.String()
	// ordered left to right: Público (izq) < El País (c-izq) < Medio (der)
	i, j, k := strings.Index(body, `id="src-pub"`), strings.Index(body, `id="src-pais"`), strings.Index(body, `id="src-abc"`)
	if !(i > 0 && i < j && j < k) {
		t.Errorf("columns not ordered by leaning: %d %d %d", i, j, k)
	}
	for _, s := range []string{`rel="noopener noreferrer"`, `target="_blank"`, "hx-trigger=\"every 300s\"", "hace 10 min", `rel="manifest"`, `viewport-fit=cover`, `apple-touch-icon`} {
		if !strings.Contains(body, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestXSSEscaping(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for _, p := range []string{"/", "/buscar?q=x", "/articulo/1", "/estado", "/capturas"} {
		body := do(h, "GET", p, nil, "").Body.String()
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") || strings.Contains(body, "<b>") && p == "/estado" {
			t.Errorf("%s: unescaped content", p)
		}
	}
	body := do(h, "GET", "/buscar?q=x", nil, "").Body.String()
	if !strings.Contains(body, "<mark>titular</mark>") {
		t.Errorf("mark lost: %s", body)
	}
	if strings.Contains(body, "onerror=alert(1)>") && !strings.Contains(body, "&lt;img") {
		t.Errorf("img not escaped")
	}
	if strings.Count(body, "<mark>") != strings.Count(body, "</mark>") {
		t.Errorf("unbalanced marks")
	}
	if !strings.Contains(body, "Medio &lt;Derecha&gt;") {
		t.Errorf("source name not escaped")
	}
}

func TestSafeSnippet(t *testing.T) {
	cases := map[string]string{
		`a <MARK>b</MARK> c`:          `a <mark>b</mark> c`,
		`</mark>x<mark>y`:             `x<mark>y</mark>`,
		`<script>x</script>`:          `&lt;script&gt;x&lt;/script&gt;`,
		"\x01evil\x02":                `evil`,
		`<mark><mark>a</mark></mark>`: `<mark>a</mark>`,
	}
	for in, want := range cases {
		if got := string(safeSnippet(in)); got != want {
			t.Errorf("%q => %q, want %q", in, got, want)
		}
	}
}

func TestSearchFragment(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	rec := do(h, "GET", "/buscar?q=algo", map[string]string{"HX-Request": "true"}, "")
	b := rec.Body.String()
	if strings.Contains(b, "<html") || !strings.Contains(b, "Búsqueda híbrida") {
		t.Errorf("fragment wrong: %s", b)
	}
	// history restore returns full page
	rec = do(h, "GET", "/buscar?q=algo", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"}, "")
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Error("history restore should be full page")
	}
	// text mode badge
	h2, _, ret, _ := newHandler(t, nil)
	ret.mode = domain.ModeText
	if b := do(h2, "GET", "/buscar?q=algo", nil, "").Body.String(); !strings.Contains(b, "Solo texto") {
		t.Error("text badge missing")
	}
	// overly long query is truncated, not an error
	if rec := do(h, "GET", "/buscar?q="+strings.Repeat("a", 5000), nil, ""); rec.Code != 200 {
		t.Errorf("long q: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/search?q="+strings.Repeat("a", 5000), nil, ""); rec.Code != 400 {
		t.Errorf("api long q: %d", rec.Code)
	}
}

func TestSSEAskOrder(t *testing.T) {
	h, _, ret, _ := newHandler(t, nil)
	rec := do(h, "POST", "/api/ask", map[string]string{"X-Requested-With": "t", "Content-Type": "application/json"},
		`{"question":"¿qué pasa?","compare":true,"k":99,"sources":["pais"],"from":"2026-01-02"}`)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	b := rec.Body.String()
	order := []string{"event: sources", "event: delta\ndata: Hola ", "event: delta\ndata: mundo [1]\ndata: linea2", "event: done"}
	pos := -1
	for _, o := range order {
		i := strings.Index(b, o)
		if i <= pos {
			t.Fatalf("event %q out of order/missing in:\n%s", o, b)
		}
		pos = i
	}
	if !ret.lastReq.Compare || ret.lastReq.K != 20 || ret.lastReq.Filter.SourceIDs[0] != "pais" || ret.lastReq.Filter.From.IsZero() {
		t.Errorf("request not mapped: %+v", ret.lastReq)
	}
	if !strings.Contains(b, `"n":1`) || strings.Contains(b, "<mark>") {
		t.Errorf("sources payload: %s", b)
	}
	// error from LLM: sources still sent, error event, done
	ret.askErr = errors.New("llm down")
	b = do(h, "POST", "/api/ask", map[string]string{"X-Requested-With": "t"}, `{"question":"x"}`).Body.String()
	if !(strings.Index(b, "event: sources") < strings.Index(b, "event: error") && strings.Index(b, "event: error") < strings.Index(b, "event: done")) {
		t.Errorf("error order: %s", b)
	}
	if strings.Contains(b, "llm down") {
		t.Error("internal error leaked")
	}
	// validation
	for _, body := range []string{`{}`, `nope`, `{"question":"` + strings.Repeat("a", 3000) + `"}`} {
		if rec := do(h, "POST", "/api/ask", map[string]string{"X-Requested-With": "t"}, body); rec.Code != 400 {
			t.Errorf("body %.20q: %d", body, rec.Code)
		}
	}
}

func TestCSRF(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	if rec := do(h, "POST", "/api/ask", nil, `{"question":"x"}`); rec.Code != 403 {
		t.Errorf("no header: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/ask", map[string]string{"Origin": "http://evil.example", "X-Requested-With": "x"}, `{"question":"x"}`); rec.Code != 403 {
		t.Errorf("cross origin: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/ask", map[string]string{"Origin": "http://example.com"}, `{"question":"x"}`); rec.Code != 200 {
		t.Errorf("same origin: %d", rec.Code)
	}
}

func TestJobsConcurrent409(t *testing.T) {
	var ing *blockingJob
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { ing = d.Ingestor.(*blockingJob) })
	hdr := map[string]string{"X-Requested-With": "t"}
	if rec := do(h, "POST", "/api/jobs/ingest", hdr, ""); rec.Code != 202 {
		t.Fatalf("first: %d", rec.Code)
	}
	<-ing.started
	if rec := do(h, "POST", "/api/jobs/ingest", hdr, ""); rec.Code != 409 {
		t.Fatalf("second: %d", rec.Code)
	}
	// the other job is independent
	if rec := do(h, "POST", "/api/jobs/screenshots", hdr, ""); rec.Code != 202 {
		t.Fatalf("shots: %d", rec.Code)
	}
	close(ing.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec := do(h, "POST", "/api/jobs/ingest", hdr, "")
		if rec.Code == 202 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestJobsUnavailable(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Ingestor = nil })
	if rec := do(h, "POST", "/api/jobs/ingest", map[string]string{"X-Requested-With": "t"}, ""); rec.Code != 503 {
		t.Errorf("%d", rec.Code)
	}
}

func TestAuth(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { o.BasicAuthUser, o.BasicAuthPass = "u", "p" })
	if rec := do(h, "GET", "/", nil, ""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no creds: %d", rec.Code)
	}
	if rec := do(h, "GET", "/healthz", nil, ""); rec.Code != 200 {
		t.Errorf("healthz: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("u", "bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("bad creds: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("u", "p")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("good creds: %d", rec.Code)
	}
	// off
	h2, _, _, _ := newHandler(t, nil)
	if rec := do(h2, "GET", "/", nil, ""); rec.Code != 200 {
		t.Errorf("auth off: %d", rec.Code)
	}
}

func TestShotsServing(t *testing.T) {
	h, _, _, dir := newHandler(t, nil)
	rec := do(h, "GET", "/shots/pais/x.jpg", nil, "")
	if rec.Code != 200 || rec.Body.String() != "JPEGDATA" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("good: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	bad := []string{"/shots/../secret.jpg", "/shots/pais/../../secret.jpg", "/shots/%2e%2e/secret.jpg", "/shots/pais/%2e%2e/%2e%2e/secret.jpg",
		"/shots/..%2fsecret.jpg", "/shots/pais", "/shots/", "/shots/pais/", "/shots/pais/x.txt", "/shots/%5c..%5csecret.jpg", "/shots/pais/nope.jpg"}
	for _, p := range bad {
		rec := do(h, "GET", p, nil, "")
		if rec.Code == 200 || strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("%s: served (%d)", p, rec.Code)
		}
	}
	// symlink escaping the dir
	if err := os.Symlink(filepath.Join(filepath.Dir(dir), "secret.jpg"), filepath.Join(dir, "link.jpg")); err == nil {
		if rec := do(h, "GET", "/shots/link.jpg", nil, ""); rec.Code == 200 {
			t.Error("symlink escape served")
		}
	}
}

func TestStaticAndPWA(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for p, ct := range map[string]string{"/static/htmx.min.js": "javascript", "/static/app.css": "css", "/static/app.js": "javascript",
		"/manifest.webmanifest": "manifest+json", "/sw.js": "javascript", "/static/icon-192.png": "image/png", "/static/icon-512.png": "image/png", "/favicon.svg": "svg"} {
		rec := do(h, "GET", p, nil, "")
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), ct) || rec.Header().Get("ETag") == "" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if rec := do(h, "GET", "/sw.js", nil, ""); rec.Header().Get("Service-Worker-Allowed") != "/" {
		t.Error("sw scope header")
	}
	rec := do(h, "GET", "/static/app.css", nil, "")
	if rec2 := do(h, "GET", "/static/app.css", map[string]string{"If-None-Match": rec.Header().Get("ETag")}, ""); rec2.Code != 304 {
		t.Errorf("etag: %d", rec2.Code)
	}
	if rec := do(h, "GET", "/static/nope.js", nil, ""); rec.Code != 404 {
		t.Errorf("404: %d", rec.Code)
	}
	if rec := do(h, "GET", "/static/../web.go", nil, ""); rec.Code == 200 {
		t.Error("static traversal")
	}
}

func TestAPIJSON(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	for p, want := range map[string]string{
		"/api/articles?limit=1":     `"articles"`,
		"/api/search?q=hola":        `"mode":"hybrid"`,
		"/api/screenshots":          `/shots/pais/a%20b/0900.jpg`,
		"/api/status":               `"llm"`,
		"/api/articles?source=pais": `"source":"El País"`,
	} {
		rec := do(h, "GET", p, nil, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	if rec := do(h, "GET", "/api/search", nil, ""); rec.Code != 400 {
		t.Errorf("missing q: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/nope", nil, ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("api 404: %d %s", rec.Code, rec.Body.String())
	}
}

func TestShotsPageCompare(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	b := do(h, "GET", "/capturas?mode=compare&hour=09", nil, "").Body.String()
	for _, s := range []string{`data-viewer="cmp"`, "Sin captura a esta hora", `/shots/pais/a%20b/0900.jpg`, `loading="lazy"`} {
		if !strings.Contains(b, s) {
			t.Errorf("missing %q", s)
		}
	}
	b = do(h, "GET", "/capturas", nil, "").Body.String()
	if !strings.Contains(b, `data-viewer="grid"`) || !strings.Contains(b, `data-viewer="tl-pais"`) {
		t.Errorf("grid markup missing")
	}
}

func TestRecoverMiddleware(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Store = &fakeStore{Store: nil, sources: nil} })
	// GetArticle is overridden but Stats etc fine; force a panic through unimplemented method:
	rec := do(h, "GET", "/api/articles", nil, "") // CountArticles ok; ListArticles ok -> 200
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	type panicStore struct{ domain.Store }
	h2 := New(Deps{Store: panicStore{}}, Options{})
	if rec := do(h2, "GET", "/api/articles", nil, ""); rec.Code != 500 {
		t.Errorf("panic not recovered: %d", rec.Code)
	}
	if rec := do(h2, "GET", "/", nil, ""); rec.Code != 500 {
		t.Errorf("panic not recovered page: %d", rec.Code)
	}
}
