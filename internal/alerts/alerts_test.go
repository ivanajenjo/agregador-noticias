package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
)

var ctx = context.Background()

type fakeTrends struct {
	stories []domain.Story
	err     error
	last    domain.TrendOptions
}

func (f *fakeTrends) Trending(context.Context, domain.TrendOptions) (*domain.TrendReport, error) {
	return nil, nil
}
func (f *fakeTrends) Term(context.Context, string, int, domain.TrendOptions) (*domain.TermDetail, error) {
	return nil, nil
}
func (f *fakeTrends) Stories(_ context.Context, o domain.TrendOptions) ([]domain.Story, error) {
	f.last = o
	return f.stories, f.err
}

type recNotifier struct {
	mu   sync.Mutex
	name string
	msgs []Message
	fail error
}

func (r *recNotifier) Name() string { return r.name }
func (r *recNotifier) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.msgs = append(r.msgs, m)
	return nil
}
func (r *recNotifier) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.msgs) }

type env struct {
	st  *store.SQLite
	tr  *fakeTrends
	now time.Time
	rec *recNotifier
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.UpsertSources(ctx, []domain.Source{{ID: "a", Name: "Medio A", Leaning: "izquierda"}, {ID: "b", Name: "Medio B", Leaning: "derecha"}}); err != nil {
		t.Fatal(err)
	}
	return &env{st: st, tr: &fakeTrends{}, now: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), rec: &recNotifier{name: "fake"}}
}

func (e *env) engine(mod func(*Options)) *Engine {
	o := Options{MinSources: 3, Notifiers: []Notifier{e.rec}, Now: func() time.Time { return e.now }, PublicURL: "http://pi.local:8080/", Backoff: time.Millisecond}
	if mod != nil {
		mod(&o)
	}
	return New(Deps{Store: e.st, Follow: e.st, Trends: e.tr}, o)
}

func story(id, title string, n int) domain.Story {
	s := domain.Story{ID: id, Title: title, Sources: n, Articles: n, FirstBy: "pais"}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		s.Coverage = append(s.Coverage, domain.SourceCoverage{SourceID: id, Name: "Medio " + strings.ToUpper(id)})
	}
	s.Coverage[0].SourceID, s.Coverage[0].Name = "pais", "El País"
	return s
}

func (e *env) addArticle(t *testing.T, src, title string, at time.Time) int64 {
	t.Helper()
	a := domain.Article{SourceID: src, URL: "http://x/" + title, CanonicalURL: "http://x/" + title, Title: title, PublishedAt: at, FirstSeenAt: at}
	if ok, err := e.st.InsertArticle(ctx, &a); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return a.ID
}

func TestStoryAlertFiresOnceAndDedups(t *testing.T) {
	e := newEnv(t)
	e.tr.stories = []domain.Story{story("s1", "El Gobierno aprueba la ley", 4), story("s2", "Pocos medios", 2)}
	eng := e.engine(nil)
	msg, err := eng.RunOnce(ctx)
	if err != nil || e.rec.count() != 1 {
		t.Fatalf("%q %v count=%d", msg, err, e.rec.count())
	}
	m := e.rec.msgs[0]
	if m.Kind != "story" || m.Title != "El Gobierno aprueba la ley" || m.URL != "http://pi.local:8080/tendencias" ||
		!strings.Contains(m.Body, "4 medios: El País, Medio B, Medio C, Medio D") || !strings.Contains(m.Body, "Primero: El País") {
		t.Fatalf("%+v", m)
	}
	if e.tr.last.MinSources != 3 || e.tr.last.Window != 2*time.Hour {
		t.Fatalf("%+v", e.tr.last)
	}
	// same run again and a "restarted" engine: no duplicates
	_, _ = eng.RunOnce(ctx)
	_, _ = e.engine(nil).RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatalf("dup: %d", e.rec.count())
	}
	// next day the same story may alert again
	e.now = e.now.Add(24 * time.Hour)
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 2 {
		t.Fatalf("next day: %d", e.rec.count())
	}
	l, _ := e.st.ListAlerts(ctx, 10)
	if len(l) != 2 || l[0].Channel != "fake" || !l[0].OK {
		t.Fatalf("%+v", l)
	}
}

func TestNoChannelIsLogged(t *testing.T) {
	e := newEnv(t)
	e.tr.stories = []domain.Story{story("s1", "Algo", 3)}
	eng := e.engine(func(o *Options) { o.Notifiers = []Notifier{} })
	if len(eng.Channels()) != 0 {
		t.Fatal(eng.Channels())
	}
	_, _ = eng.RunOnce(ctx)
	_, _ = eng.RunOnce(ctx)
	l, _ := e.st.ListAlerts(ctx, 10)
	if len(l) != 1 || l[0].Channel != domain.ChannelLog || !l[0].OK || !strings.Contains(l[0].Detail, "sin canal") {
		t.Fatalf("%+v", l)
	}
}

func TestWatchAlertBatching(t *testing.T) {
	e := newEnv(t)
	w := &domain.Watch{Name: "Vivienda", Query: "vivienda", Notify: true}
	if err := e.st.CreateWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	other := &domain.Watch{Name: "Muda", Query: "vivienda", Notify: false}
	_ = e.st.CreateWatch(ctx, other)
	e.addArticle(t, "a", "Vieja noticia de vivienda", e.now.Add(-3*time.Hour))
	eng := e.engine(nil)
	// first evaluation only seeds history
	if _, err := eng.RunOnce(ctx); err != nil || e.rec.count() != 0 {
		t.Fatal(err, e.rec.count())
	}
	e.now = e.now.Add(5 * time.Minute)
	e.addArticle(t, "a", "Sube la vivienda un 5%", e.now.Add(-2*time.Minute))
	e.addArticle(t, "b", "Plan de vivienda del Gobierno", e.now.Add(-time.Minute))
	e.addArticle(t, "b", "Nada relacionado con el tema", e.now)
	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.rec.count() != 1 {
		t.Fatalf("expected 1 batched message, got %d", e.rec.count())
	}
	m := e.rec.msgs[0]
	if m.Kind != "watch" || !strings.Contains(m.Title, "«Vivienda»: 2 novedades") || !strings.Contains(m.Body, "2 medios: Medio B, Medio A") ||
		m.URL != "http://pi.local:8080/seguimientos" {
		t.Fatalf("%+v", m)
	}
	// nothing new => nothing sent; a single new one links to the article
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatal("dup", e.rec.count())
	}
	e.now = e.now.Add(5 * time.Minute)
	id := e.addArticle(t, "a", "Otra de vivienda", e.now.Add(-time.Minute))
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 2 || !strings.HasSuffix(e.rec.msgs[1].URL, "/articulo/"+itoa(id)) {
		t.Fatalf("%d %+v", e.rec.count(), e.rec.msgs)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestQuietHoursAndSummary(t *testing.T) {
	e := newEnv(t)
	loc := time.UTC
	e.now = time.Date(2026, 3, 10, 2, 0, 0, 0, loc) // 02:00, inside 00:00-07:00
	e.tr.stories = []domain.Story{story("s1", "Historia uno", 4), story("s2", "Historia dos", 5)}
	eng := e.engine(func(o *Options) { o.QuietStart, o.QuietEnd, o.Location = "00:00", "07:00", loc })
	msg, _ := eng.RunOnce(ctx)
	if e.rec.count() != 0 || !strings.Contains(msg, "2 en espera") {
		t.Fatalf("%q %d", msg, e.rec.count())
	}
	pend, _ := e.st.PendingQuiet(ctx)
	if len(pend) != 2 {
		t.Fatal(pend)
	}
	_, _ = eng.RunOnce(ctx) // still quiet, no duplicates
	if p, _ := e.st.PendingQuiet(ctx); len(p) != 2 {
		t.Fatal(p)
	}
	e.now = time.Date(2026, 3, 10, 7, 5, 0, 0, loc)
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatalf("expected a single summary, got %d: %+v", e.rec.count(), e.rec.msgs)
	}
	m := e.rec.msgs[0]
	if m.Kind != "summary" || !strings.Contains(m.Title, "2 avisos") || !strings.Contains(m.Body, "Historia uno") || !strings.Contains(m.Body, "Historia dos") {
		t.Fatalf("%+v", m)
	}
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatal("summary or stories repeated", e.rec.count())
	}
	if p, _ := e.st.PendingQuiet(ctx); len(p) != 0 {
		t.Fatal(p)
	}
}

func TestQuietWindow(t *testing.T) {
	cases := []struct {
		s, e, at string
		want     bool
	}{
		{"00:00", "07:00", "03:00", true}, {"00:00", "07:00", "07:00", false}, {"23:00", "06:00", "23:30", true},
		{"23:00", "06:00", "05:59", true}, {"23:00", "06:00", "12:00", false}, {"", "", "03:00", false}, {"08:00", "08:00", "08:00", false}, {"xx", "07:00", "03:00", false},
	}
	for _, c := range cases {
		at, _ := time.Parse("15:04", c.at)
		eng := New(Deps{}, Options{QuietStart: c.s, QuietEnd: c.e})
		if got := eng.quiet(at); got != c.want {
			t.Errorf("%v: got %v", c, got)
		}
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	for i, id := range []string{"s1", "s2", "s3", "s4"} {
		e.tr.stories = append(e.tr.stories, story(id, "Historia "+id, 3+i))
	}
	eng := e.engine(func(o *Options) { o.MaxPerHour = 2 })
	msg, _ := eng.RunOnce(ctx)
	if e.rec.count() != 2 || !strings.Contains(msg, "2 enviadas") || !strings.Contains(msg, "limitadas") {
		t.Fatalf("%q %d", msg, e.rec.count())
	}
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 2 {
		t.Fatal("limit exceeded", e.rec.count())
	}
	e.now = e.now.Add(61 * time.Minute)
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 4 {
		t.Fatalf("pending ones should go out later: %d", e.rec.count())
	}
}

func TestNotifierFailureIsRecordedAndRetried(t *testing.T) {
	e := newEnv(t)
	e.tr.stories = []domain.Story{story("s1", "Historia", 4)}
	e.rec.fail = io.ErrUnexpectedEOF
	eng := e.engine(nil)
	msg, err := eng.RunOnce(ctx)
	if err != nil || !strings.Contains(msg, "1 fallidas") {
		t.Fatalf("%q %v", msg, err)
	}
	l, _ := e.st.ListAlerts(ctx, 10)
	if len(l) != 1 || l[0].OK || !strings.Contains(l[0].Detail, "EOF") {
		t.Fatalf("%+v", l)
	}
	// channel recovers: retried on the next run and the row becomes ok
	e.rec.fail = nil
	e.now = e.now.Add(5 * time.Minute)
	_, _ = eng.RunOnce(ctx)
	l, _ = e.st.ListAlerts(ctx, 10)
	if e.rec.count() != 1 || len(l) != 1 || !l[0].OK {
		t.Fatalf("%d %+v", e.rec.count(), l)
	}
	// a channel failing for longer than the give-up window is dropped
	e2 := newEnv(t)
	e2.tr.stories = []domain.Story{story("s1", "Historia", 4)}
	e2.rec.fail = io.ErrUnexpectedEOF
	eng2 := e2.engine(nil)
	_, _ = eng2.RunOnce(ctx)
	e2.now = e2.now.Add(3 * time.Hour)
	e2.rec.fail = nil
	_, _ = eng2.RunOnce(ctx)
	if e2.rec.count() != 0 {
		t.Fatal("should have given up")
	}
}

func TestWatchFailureRetriesWithoutLosingHits(t *testing.T) {
	e := newEnv(t)
	_ = e.st.CreateWatch(ctx, &domain.Watch{Name: "X", Query: "inflación", Notify: true})
	eng := e.engine(nil)
	_, _ = eng.RunOnce(ctx) // seed
	e.now = e.now.Add(time.Minute)
	e.addArticle(t, "a", "La inflación baja", e.now)
	e.rec.fail = io.ErrUnexpectedEOF
	_, _ = eng.RunOnce(ctx)
	e.rec.fail = nil
	e.now = e.now.Add(time.Minute)
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatalf("expected retry delivery: %d", e.rec.count())
	}
	_, _ = eng.RunOnce(ctx)
	if e.rec.count() != 1 {
		t.Fatal("dup")
	}
}

// ---- real notifiers against httptest servers ----

func TestNtfyNotifier(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	n, err := newNtfy(srv.Client(), srv.URL+"/", "mi topic")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(ctx, Message{Title: "Título con ñ\nsalto", Body: "cuerpo", URL: "http://x/y"}); err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL.EscapedPath() != "/mi%20topic" || body != "cuerpo" || got.Header.Get("Click") != "http://x/y" ||
		got.Header.Get("Tags") != "newspaper" || got.Header.Get("Priority") == "" {
		t.Fatalf("%v %q", got.URL, body)
	}
	if h := got.Header.Get("Title"); !strings.HasPrefix(h, "=?utf-8?q?") || strings.ContainsAny(h, "\n\r") {
		t.Fatal(h)
	}
	if _, err := newNtfy(srv.Client(), "ftp://x", "t"); err == nil {
		t.Fatal("bad url accepted")
	}
	if _, err := newNtfy(srv.Client(), srv.URL, " "); err == nil {
		t.Fatal("empty topic accepted")
	}
}

func TestTelegramNotifierAndTokenRedaction(t *testing.T) {
	var path string
	var payload map[string]any
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":false,"description":"bad <b>"}`))
	}))
	defer srv.Close()
	n, err := newTelegram(srv.Client(), srv.URL, "123:SECRET", "42")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(ctx, Message{Title: "T <b>x</b>", Body: "B", URL: "http://u"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:SECRET/sendMessage" || payload["chat_id"] != "42" || payload["text"] != "T <b>x</b>\nB\nhttp://u" || payload["parse_mode"] != nil {
		t.Fatalf("%s %v", path, payload)
	}
	status = 401
	err = n.Send(ctx, Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
	var pe permanentError
	if !asPermanent(err, &pe) {
		t.Fatal("401 must be permanent")
	}
	// connection error must not leak the token either
	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL
	dead.Close()
	n2, _ := newTelegram(&http.Client{Timeout: time.Second}, u, "123:SECRET", "42")
	if err := n2.Send(ctx, Message{Title: "x"}); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
	if _, err := newTelegram(nil, "", "", "1"); err == nil {
		t.Fatal("missing token accepted")
	}
}

func asPermanent(err error, target *permanentError) bool {
	pe, ok := err.(permanentError)
	if ok {
		*target = pe
	}
	return ok
}

func TestWebhookNotifierAndRetries(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var got Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls < 3 {
			http.Error(w, "boom", 503)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("content type")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	n, _ := newWebhook(srv.Client(), srv.URL)
	m := Message{Kind: "story", Title: "T", Body: "B", URL: "http://u", Sources: []string{"A", "B"}}
	if err := sendWithRetry(ctx, n, m, 3, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || got.Kind != "story" || got.Title != "T" || len(got.Sources) != 2 {
		t.Fatalf("%d %+v", calls, got)
	}
	// 4xx is not retried
	calls = 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "no", 400) }))
	defer bad.Close()
	n2, _ := newWebhook(bad.Client(), bad.URL)
	if err := sendWithRetry(ctx, n2, m, 3, time.Millisecond); err == nil || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestEngineBuildsChannelsFromConfigAndSendTest(t *testing.T) {
	e := newEnv(t)
	hits := map[string]int{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[strings.SplitN(r.URL.Path, "/", 3)[1]]++
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/hook") {
			http.Error(w, "down", 500)
		}
	}))
	defer srv.Close()
	eng := New(Deps{Store: e.st, Follow: e.st, Trends: e.tr}, Options{
		NtfyURL: srv.URL, NtfyTopic: "t", TelegramBot: "1:A", TelegramChat: "2", TelegramAPI: srv.URL,
		WebhookURL: srv.URL + "/hook", Retries: 2, Backoff: time.Millisecond, Now: func() time.Time { return e.now },
	})
	if got := strings.Join(eng.Channels(), ","); got != "ntfy,telegram,webhook" {
		t.Fatal(got)
	}
	recs, err := eng.SendTest(ctx)
	if err != nil || len(recs) != 3 {
		t.Fatal(recs, err)
	}
	ok := 0
	for _, r := range recs {
		if r.OK {
			ok++
		} else if r.Channel != "webhook" || !strings.Contains(r.Detail, "500") {
			t.Fatalf("%+v", r)
		}
	}
	if ok != 2 || hits["hook"] != 2 || hits["t"] != 1 || hits["bot1:A"] != 1 {
		t.Fatalf("ok=%d hits=%v", ok, hits)
	}
	// test alerts never count against the rate limit
	if n, _ := e.st.CountAlertsSince(ctx, e.now.Add(-time.Hour)); n != 0 {
		t.Fatal(n)
	}
	// invalid channel config is skipped, not fatal
	bad := New(Deps{Follow: e.st}, Options{NtfyURL: "::", NtfyTopic: "t", WebhookURL: "javascript:x"})
	if len(bad.Channels()) != 0 {
		t.Fatal(bad.Channels())
	}
}

func TestRunOnceTrendsError(t *testing.T) {
	e := newEnv(t)
	e.tr.err = io.ErrClosedPipe
	if _, err := e.engine(nil).RunOnce(ctx); err == nil {
		t.Fatal("expected error")
	}
}
