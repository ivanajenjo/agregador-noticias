package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
)

// fakeStore implements only what the ingestor uses; the embedded nil interface
// makes any other call panic loudly.
type fakeStore struct {
	domain.Store
	mu       sync.Mutex
	arts     map[string]*domain.Article
	states   map[string]domain.FeedState
	hasURLOn bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{arts: map[string]*domain.Article{}, states: map[string]domain.FeedState{}}
}

func (f *fakeStore) InsertArticle(_ context.Context, a *domain.Article) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.arts[a.CanonicalURL]; ok {
		return false, nil
	}
	c := *a
	c.ID = int64(len(f.arts) + 1)
	f.arts[a.CanonicalURL] = &c
	return true, nil
}

func (f *fakeStore) GetFeedState(_ context.Context, s, u string) (*domain.FeedState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st, ok := f.states[s+"|"+u]; ok {
		return &st, nil
	}
	return nil, nil
}

func (f *fakeStore) SaveFeedState(_ context.Context, s *domain.FeedState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[s.SourceID+"|"+s.FeedURL] = *s
	return nil
}

func (f *fakeStore) state(s, u string) domain.FeedState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[s+"|"+u]
}

func (f *fakeStore) article(canon string) *domain.Article {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arts[canon]
}

// storeWithHas adds the optional urlChecker.
type storeWithHas struct{ *fakeStore }

func (s storeWithHas) HasURL(_ context.Context, c string) (bool, error) {
	return s.fakeStore.article(c) != nil, nil
}

func testOpts(src ...domain.Source) Options {
	o := DefaultOptions()
	o.Sources = src
	o.AllowPrivate = true
	o.PerHostDelay = 0
	o.RespectRobots = false
	o.RetryBackoff = time.Millisecond
	o.Timeout = 5 * time.Second
	return o
}

const rssFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>T</title>
<item><title>Primera &amp; noticia</title><link>http://Example.com/a/?utm_source=x&amp;id=1#frag</link>
<description>&lt;p&gt;Resumen &lt;b&gt;con&lt;/b&gt; html&lt;/p&gt;&lt;script&gt;alert(1)&lt;/script&gt;</description>
<dc:creator>Ana Pérez</dc:creator><category>Política</category><pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate></item>
<item><title>Segunda</title><link>https://example.com/b/</link><description>texto b</description></item>
</channel></rss>`

const atomFixture = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title><id>x</id><updated>2024-05-01T10:00:00Z</updated>
<entry><title>Entrada atom</title><link href="https://example.org/atom/1?fbclid=zzz"/><id>1</id>
<updated>2024-05-01T10:00:00Z</updated><author><name>Luis</name></author><summary>Resumen atom</summary></entry></feed>`

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestRSSAndAtom(t *testing.T) {
	rss := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, rssFixture) })
	atom := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, atomFixture) })
	st := newFakeStore()
	src := domain.Source{ID: "s", Name: "S", Feeds: []domain.Feed{{URL: rss.URL, Section: ""}, {URL: atom.URL, Section: "Mundo"}}}
	in := New(st, testOpts(src), nil)
	n, err := in.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	a := st.article("https://example.com/a?id=1")
	if a == nil {
		t.Fatalf("article not found: %v", st.arts)
	}
	if a.Title != "Primera & noticia" || a.Summary != "Resumen con html" || a.Author != "Ana Pérez" ||
		a.Section != "Política" || a.PublishedAt.Year() != 2006 || a.SourceID != "s" {
		t.Errorf("bad article: %+v", a)
	}
	b := st.article("https://example.org/atom/1")
	if b == nil || b.Author != "Luis" || b.Section != "Mundo" || b.Summary != "Resumen atom" || b.PublishedAt.IsZero() {
		t.Errorf("bad atom article: %+v", b)
	}
	// Second run: nothing new.
	if n, _ := in.RunOnce(context.Background()); n != 0 {
		t.Errorf("second run inserted %d", n)
	}
}

func TestETagConditionalGet(t *testing.T) {
	var hits, conds atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			conds.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		fmt.Fprint(w, rssFixture)
	})
	st := newFakeStore()
	src := domain.Source{ID: "s", Feeds: []domain.Feed{{URL: srv.URL}}}
	in := New(st, testOpts(src), nil)
	if n, _ := in.RunOnce(context.Background()); n != 2 {
		t.Fatalf("first run n=%d", n)
	}
	fs := st.state("s", srv.URL)
	if fs.ETag != `"v1"` || fs.LastModified == "" || fs.LastOK.IsZero() || fs.LastError != "" {
		t.Fatalf("state: %+v", fs)
	}
	if n, err := in.RunOnce(context.Background()); n != 0 || err != nil {
		t.Fatalf("second run n=%d err=%v", n, err)
	}
	if conds.Load() != 1 || hits.Load() != 2 {
		t.Errorf("hits=%d conds=%d", hits.Load(), conds.Load())
	}
	if fs := st.state("s", srv.URL); fs.ETag != `"v1"` || fs.LastError != "" {
		t.Errorf("state after 304: %+v", fs)
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := []struct{ in, base, want string }{
		{"http://Example.COM/a/b/", "", "https://example.com/a/b"},
		{"https://example.com/a?utm_source=x&utm_medium=y&id=3", "", "https://example.com/a?id=3"},
		{"https://example.com/a?b=2&a=1&fbclid=abc&gclid=1", "", "https://example.com/a?a=1&b=2"},
		{"https://example.com/a#section", "", "https://example.com/a"},
		{"https://example.com/", "", "https://example.com"},
		{"https://example.com:443/x", "", "https://example.com/x"},
		{"http://example.com:8080/x/", "", "https://example.com:8080/x"},
		{"/rel/path/?utm_campaign=z", "https://example.com/feed.xml", "https://example.com/rel/path"},
		{"  https://example.com/ñ/x  ", "", "https://example.com/%C3%B1/x"},
		{"https://example.com/a?UTM_Source=x", "", "https://example.com/a"},
	}
	for _, c := range cases {
		got, err := CanonicalURL(c.in, c.base)
		if err != nil || got != c.want {
			t.Errorf("CanonicalURL(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "ftp://x.com/a", "javascript:alert(1)", "/nohost"} {
		if got, err := CanonicalURL(bad, ""); err == nil {
			t.Errorf("CanonicalURL(%q) = %q, want error", bad, got)
		}
	}
	// Idempotent.
	once, _ := CanonicalURL("http://Example.com/a/?utm_x=1&z=1", "")
	twice, _ := CanonicalURL(once, "")
	if once != twice {
		t.Errorf("not idempotent: %q vs %q", once, twice)
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<p>Hola <b>mundo</b></p><p>dos</p>":        "Hola mundo dos",
		"a &amp; b&nbsp;c":                          "a & b c",
		"<style>x{}</style>texto<script>1</script>": "texto",
		"  plain   text ":                           "plain text",
		"línea<br/>otra":                            "línea otra",
	}
	for in, want := range cases {
		if got := StripHTML(in); got != want {
			t.Errorf("StripHTML(%q) = %q want %q", in, got, want)
		}
	}
}

func TestFailureIsolationAndRetry(t *testing.T) {
	var flakyHits atomic.Int32
	good := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, rssFixture) })
	bad500 := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) })
	garbage := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>not a feed</html>") })
	flaky := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if flakyHits.Add(1) == 1 {
			http.Error(w, "try later", 503)
			return
		}
		fmt.Fprint(w, atomFixture)
	})
	notFound := serve(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	st := newFakeStore()
	srcs := []domain.Source{
		{ID: "bad", Feeds: []domain.Feed{{URL: bad500.URL}, {URL: garbage.URL}, {URL: notFound.URL}, {URL: "http://127.0.0.1:1/none"}}},
		{ID: "good", Feeds: []domain.Feed{{URL: good.URL}}},
		{ID: "flaky", Feeds: []domain.Feed{{URL: flaky.URL}}},
	}
	in := New(st, testOpts(srcs...), nil)
	n, err := in.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, u := range []string{bad500.URL, garbage.URL, notFound.URL, "http://127.0.0.1:1/none"} {
		if fs := st.state("bad", u); fs.LastError == "" || fs.LastErrorAt.IsZero() {
			t.Errorf("no error recorded for %s: %+v", u, fs)
		}
	}
	if fs := st.state("good", good.URL); fs.LastError != "" || fs.LastCount != 2 {
		t.Errorf("good state: %+v", fs)
	}
	if flakyHits.Load() != 2 || st.state("flaky", flaky.URL).LastError != "" {
		t.Errorf("retry failed: hits=%d", flakyHits.Load())
	}
}

func TestDisabledSourceAndBodyLimit(t *testing.T) {
	var hits atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, rssFixture) })
	off := false
	st := newFakeStore()
	in := New(st, testOpts(domain.Source{ID: "off", Enabled: &off, Feeds: []domain.Feed{{URL: srv.URL}}}), nil)
	if n, _ := in.RunOnce(context.Background()); n != 0 || hits.Load() != 0 {
		t.Errorf("disabled source fetched: n=%d hits=%d", n, hits.Load())
	}
	o := testOpts(domain.Source{ID: "big", Feeds: []domain.Feed{{URL: srv.URL}}})
	o.MaxBodyBytes = 100
	in = New(st, o, nil)
	if n, _ := in.RunOnce(context.Background()); n != 0 {
		t.Errorf("oversized feed accepted")
	}
	if e := st.state("big", srv.URL).LastError; !strings.Contains(e, "exceeds") {
		t.Errorf("error = %q", e)
	}
}

func TestContextCancel(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	in := New(newFakeStore(), testOpts(domain.Source{ID: "s", Feeds: []domain.Feed{{URL: srv.URL}}}), nil)
	start := time.Now()
	_, err := in.RunOnce(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("err=%v elapsed=%v", err, time.Since(start))
	}
}

func TestIsBlockedIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "192.168.0.5": true, "172.16.0.1": true,
		"169.254.169.254": true, "::1": true, "fe80::1": true, "fd00::1": true, "0.0.0.0": true,
		"100.64.0.1": true, "::ffff:127.0.0.1": true, "8.8.8.8": false, "93.184.216.34": false,
		"2606:4700:4700::1111": false,
	} {
		if got := isBlockedIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("isBlockedIP(%s) = %v want %v", ip, got, want)
		}
	}
}

func TestSSRFGuard(t *testing.T) {
	var hits atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, "<html>secret</html>") })
	o := testOpts()
	o.AllowPrivate = false
	in := New(newFakeStore(), o, nil)
	_, err := in.fetchArticleText(context.Background(), srv.URL+"/x")
	if !errors.Is(err, ErrBlockedAddress) || hits.Load() != 0 {
		t.Fatalf("loopback not refused: err=%v hits=%d", err, hits.Load())
	}
	// Redirect from a would-be-allowed host to loopback is also stopped by the dial guard;
	// unsupported schemes are refused up front.
	if _, err := in.fetchArticleText(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("file scheme accepted")
	}
	// With the test escape hatch it works.
	o.AllowPrivate = true
	in = New(newFakeStore(), o, nil)
	if _, err := in.fetchArticleText(context.Background(), srv.URL+"/x"); errors.Is(err, ErrBlockedAddress) {
		t.Errorf("AllowPrivate still blocked: %v", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	var srv *httptest.Server
	srv = serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, srv.URL+r.URL.Path+"x", 302) })
	in := New(newFakeStore(), testOpts(), nil)
	if _, err := in.fetchArticleText(context.Background(), srv.URL+"/"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("err=%v", err)
	}
}

const articleHTML = `<!doctype html><html lang="es"><head><title>Titular - Diario</title></head><body>
<nav><ul><li>Inicio</li><li>Política</li><li>Economía</li></ul></nav>
<article><h1>Titular del artículo</h1>
%s
</article><footer>Aviso legal y cookies. Suscríbete.</footer></body></html>`

func longBody() string {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "<p>Este es el párrafo número %d del artículo principal, con suficiente texto para que el algoritmo de legibilidad lo considere contenido real, incluyendo comas, datos y varias frases largas que describen la noticia con detalle.</p>\n", i)
	}
	return b.String()
}

func TestFullTextExtraction(t *testing.T) {
	var pageHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<item><title>Corta</title><link>http://%s/art/1</link><description>breve</description></item>
<item><title>Larga</title><link>http://%s/art/2</link><content:encoded xmlns:content="http://purl.org/rss/1.0/modules/content/"><![CDATA[%s]]></content:encoded></item>
</channel></rss>`, r.Host, r.Host, strings.Repeat("<p>texto largo del feed. </p>", 60))
	})
	mux.HandleFunc("/art/", func(w http.ResponseWriter, r *http.Request) {
		pageHits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, articleHTML, longBody())
	})
	srv := serve(t, mux.ServeHTTP)

	st := newFakeStore()
	src := domain.Source{ID: "s", FullText: true, Feeds: []domain.Feed{{URL: srv.URL + "/feed"}}}
	in := New(storeWithHas{st}, testOpts(src), nil)
	n, err := in.RunOnce(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// canonical drops http->https and host:port is kept.
	short := st.article("https://" + strings.TrimPrefix(srv.URL, "http://") + "/art/1")
	if short == nil || !strings.Contains(short.Content, "párrafo número 3") || strings.Contains(short.Content, "Aviso legal") {
		t.Fatalf("short article content: %+v", short)
	}
	if short.Summary != "breve" {
		t.Errorf("summary = %q", short.Summary)
	}
	if pageHits.Load() != 1 {
		t.Errorf("page hits = %d, want 1 (long item must not be fetched)", pageHits.Load())
	}
	// Second run: HasURL prevents refetching existing articles.
	if n, _ := in.RunOnce(context.Background()); n != 0 || pageHits.Load() != 1 {
		t.Errorf("second run n=%d pageHits=%d", n, pageHits.Load())
	}
	// FullText=false never fetches pages.
	pageHits.Store(0)
	src.FullText = false
	in = New(newFakeStore(), testOpts(src), nil)
	in.RunOnce(context.Background())
	if pageHits.Load() != 0 {
		t.Errorf("fetched pages with FullText=false")
	}
}

func TestRobots(t *testing.T) {
	var pageHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /private/\nAllow: /private/ok\n\nUser-agent: otherbot\nDisallow: /\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pageHits.Add(1)
		fmt.Fprintf(w, articleHTML, longBody())
	})
	srv := serve(t, mux.ServeHTTP)
	o := testOpts()
	o.RespectRobots = true
	in := New(newFakeStore(), o, nil)
	ctx := context.Background()
	if _, err := in.fetchArticleText(ctx, srv.URL+"/private/x"); err == nil || !strings.Contains(err.Error(), "robots") {
		t.Errorf("private path fetched: %v", err)
	}
	if _, err := in.fetchArticleText(ctx, srv.URL+"/private/ok"); err != nil {
		t.Errorf("allowed path refused: %v", err)
	}
	if _, err := in.fetchArticleText(ctx, srv.URL+"/public"); err != nil {
		t.Errorf("public path refused: %v", err)
	}
	if pageHits.Load() != 2 {
		t.Errorf("page hits = %d", pageHits.Load())
	}
}

func TestPerHostDelay(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, rssFixture) })
	o := testOpts(domain.Source{ID: "s", Feeds: []domain.Feed{{URL: srv.URL + "/1"}, {URL: srv.URL + "/2"}, {URL: srv.URL + "/3"}}})
	o.PerHostDelay = 150 * time.Millisecond
	start := time.Now()
	New(newFakeStore(), o, nil).RunOnce(context.Background())
	if d := time.Since(start); d < 280*time.Millisecond {
		t.Errorf("per-host limiting not applied: %v", d)
	}
}

// ---- revisions (stealth edits) ----

func feedWith(items ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>`)
	for _, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><description>%s</description></item>`, it[0], it[1], it[2])
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func TestTrackRevisionsWithRealStore(t *testing.T) {
	var cur atomic.Value
	cur.Store(feedWith([3]string{"Detenido el alcalde de Marbella por corrupción", "https://e.example/1", "Resumen largo de la noticia sobre el alcalde"}))
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, cur.Load().(string)) })

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := domain.Source{ID: "s", Name: "S", Feeds: []domain.Feed{{URL: srv.URL}}}
	ctx := context.Background()
	if err := st.UpsertSources(ctx, []domain.Source{src}); err != nil {
		t.Fatal(err)
	}
	o := testOpts(src)
	o.TrackRevisions = true
	in := New(st, o, nil)
	run := func() int {
		n, err := in.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if run() != 1 {
		t.Fatal("first run should insert")
	}
	run() // unchanged
	if in.RevisionsRecorded() != 0 {
		t.Fatal("unchanged feed recorded a revision")
	}
	// trivial edit (suffix + case)
	cur.Store(feedWith([3]string{"DETENIDO el alcalde de Marbella por corrupción - El País", "https://e.example/1", "Resumen largo de la noticia sobre el alcalde"}))
	run()
	if in.RevisionsRecorded() != 0 {
		t.Fatal("trivial edit recorded")
	}
	// real edit
	cur.Store(feedWith([3]string{"Liberado el alcalde de Marbella tras declarar", "https://e.example/1", "Resumen largo de la noticia sobre el alcalde"}))
	if n := run(); n != 0 {
		t.Fatalf("edit inserted a new article: %d", n)
	}
	if in.RevisionsRecorded() != 1 {
		t.Fatalf("revisions = %d", in.RevisionsRecorded())
	}
	arts, _ := st.ListArticles(ctx, domain.Filter{})
	if len(arts) != 1 || arts[0].Title != "Liberado el alcalde de Marbella tras declarar" {
		t.Fatalf("stored = %+v", arts)
	}
	revs, _ := st.ListRevisions(ctx, arts[0].ID)
	if len(revs) != 1 || revs[0].OldText != "Detenido el alcalde de Marbella por corrupción" {
		t.Fatalf("revs = %+v", revs)
	}
	// flapping back to the original: ignored
	cur.Store(feedWith([3]string{"Detenido el alcalde de Marbella por corrupción", "https://e.example/1", "Resumen largo de la noticia sobre el alcalde"}))
	run()
	if in.RevisionsRecorded() != 1 {
		t.Fatalf("flapping recorded: %d", in.RevisionsRecorded())
	}

	// disabled: edits are not tracked
	cur.Store(feedWith([3]string{"Otro titular completamente nuevo y diferente", "https://e.example/1", "Resumen largo de la noticia sobre el alcalde"}))
	o2 := o
	o2.TrackRevisions = false
	New(st, o2, nil).RunOnce(ctx)
	if n, _ := st.CountRevisions(ctx, domain.RevisionFilter{}); n != 1 {
		t.Fatalf("disabled tracking recorded: %d", n)
	}
}

func TestTrackRevisionsIgnoredWithoutRevisionStore(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, feedWith([3]string{"Titular", "https://e.example/1", "x"}))
	})
	o := testOpts(domain.Source{ID: "s", Feeds: []domain.Feed{{URL: srv.URL}}})
	o.TrackRevisions = true
	in := New(newFakeStore(), o, nil) // fakeStore has no RecordRevision
	for i := 0; i < 2; i++ {
		if _, err := in.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if in.RevisionsRecorded() != 0 {
		t.Fatal("recorded without store support")
	}
}
