package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func fastEmbed(url string) EmbedConfig {
	return EmbedConfig{BaseURL: url, Model: "m", Backoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Timeout: 2 * time.Second}
}

func embedHandler(calls *int32, fail func(n int32) int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(calls, 1)
		if fail != nil {
			if code := fail(n); code != 0 {
				http.Error(w, "boom", code)
				return
			}
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type d struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []d
		for i := len(req.Input) - 1; i >= 0; i-- { // reversed to test index ordering
			data = append(data, d{i, []float32{float32(len(req.Input[i])), 3, 4}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
}

func TestEmbedderBatchesOrderAndURL(t *testing.T) {
	var calls int32
	var paths []string
	var mu sync.Mutex
	h := embedHandler(&calls, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Error("missing bearer")
		}
		h(w, r)
	}))
	defer srv.Close()
	for _, base := range []string{srv.URL, srv.URL + "/v1", srv.URL + "/v1/"} {
		cfg := fastEmbed(base)
		cfg.APIKey = "k"
		cfg.BatchSize = 2
		e := NewEmbedder(cfg)
		out, err := e.Embed(context.Background(), []string{"a", "bb", "ccc", "dddd", "eeeee"})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 5 {
			t.Fatalf("len=%d", len(out))
		}
		for i, v := range out {
			if int(v[0]) != i+1 {
				t.Errorf("vec %d out of order: %v", i, v)
			}
		}
	}
	for _, p := range paths {
		if p != "/v1/embeddings" {
			t.Errorf("path %q", p)
		}
	}
	if len(paths) != 9 {
		t.Errorf("expected 9 requests, got %d", len(paths))
	}
}

func TestEmbedderRetryAndNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(embedHandler(&calls, func(n int32) int {
		if n <= 2 {
			return 503
		}
		return 0
	}))
	defer srv.Close()
	e := NewEmbedder(fastEmbed(srv.URL))
	if _, err := e.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls=%d", calls)
	}

	calls = 0
	srv2 := httptest.NewServer(embedHandler(&calls, func(int32) int { return 400 }))
	defer srv2.Close()
	_, err := NewEmbedder(fastEmbed(srv2.URL)).Embed(context.Background(), []string{"x"})
	if !IsPermanent(err) || calls != 1 {
		t.Errorf("400 must not retry: err=%v calls=%d", err, calls)
	}

	calls = 0
	srv3 := httptest.NewServer(embedHandler(&calls, func(int32) int { return 500 }))
	defer srv3.Close()
	cfg := fastEmbed(srv3.URL)
	cfg.MaxRetries = 2
	_, err = NewEmbedder(cfg).Embed(context.Background(), []string{"x"})
	if err == nil || calls != 3 {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestEmbedderConnRefusedAndCtx(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	cfg := fastEmbed(url)
	cfg.MaxRetries = 1
	if _, err := NewEmbedder(cfg).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEmbedder(cfg).Embed(ctx, []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err=%v", err)
	}
}

func TestEmbedderValidation(t *testing.T) {
	mk := func(body string) *OpenAIEmbedder {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		t.Cleanup(srv.Close)
		cfg := fastEmbed(srv.URL)
		cfg.MaxRetries = -1
		return NewEmbedder(cfg)
	}
	if _, err := mk(`{"data":[{"embedding":[1,2]}]}`).Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("count mismatch must fail")
	}
	if _, err := mk(`{"data":[{"embedding":[1,2]},{"embedding":[1]}]}`).Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("dim mismatch must fail")
	}
	if _, err := mk(`not json`).Embed(context.Background(), []string{"a"}); err == nil {
		t.Error("bad json must fail")
	}
	cfg := fastEmbed(mk(`{"data":[{"embedding":[1,2]}]}`).cfg.BaseURL)
	cfg.Dims = 3
	cfg.MaxRetries = -1
	if _, err := NewEmbedder(cfg).Embed(context.Background(), []string{"a"}); err == nil {
		t.Error("expected dims mismatch")
	}
}

func TestEmbedderNormalizeAndPrefixes(t *testing.T) {
	var got []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		got = append(got, req.Input...)
		mu.Unlock()
		io.WriteString(w, `{"data":[{"index":0,"embedding":[3,4]}]}`)
	}))
	defer srv.Close()
	cfg := fastEmbed(srv.URL)
	cfg.Normalize = true
	cfg.QueryPrefix = "query: "
	cfg.DocPrefix = "passage: "
	e := NewEmbedder(cfg)
	v, err := e.EmbedQuery(context.Background(), "hola")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 {
		t.Errorf("not normalized: %v", v)
	}
	if _, err := e.EmbedDocs(context.Background(), []string{"texto"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "query: hola" || got[1] != "passage: texto" {
		t.Errorf("inputs: %v", got)
	}
}

// ---- chat / SSE

func sseServer(t *testing.T, frames []string, hook func(w http.ResponseWriter)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true || req["temperature"] != 0.2 || req["max_tokens"] != float64(50) {
			t.Errorf("bad request %v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, f := range frames {
			io.WriteString(w, f)
			fl.Flush()
		}
		if hook != nil {
			hook(w)
		}
	}))
}

func delta(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", b)
}

func TestChatStream(t *testing.T) {
	srv := sseServer(t, []string{
		": keep-alive\n\n",
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"pensando\"}}]}\n\n",
		delta("Hola"), "data: {\"choices\":[]}\n\n", delta(" mundo ñ"),
		"data: [DONE]\n\n", delta("IGNORED"),
	}, nil)
	defer srv.Close()
	temp := 0.2
	c := NewChat(ChatConfig{BaseURL: srv.URL, Model: "m", Temperature: &temp, MaxTokens: 50})
	var sb strings.Builder
	err := c.Stream(context.Background(), []domain.ChatMessage{{Role: "user", Content: "x"}}, func(s string) error { sb.WriteString(s); return nil })
	if err != nil || sb.String() != "Hola mundo ñ" {
		t.Fatalf("got %q err=%v", sb.String(), err)
	}
}

func TestChatCallbackErrorStops(t *testing.T) {
	srv := sseServer(t, []string{delta("a"), delta("b"), delta("c")}, nil)
	defer srv.Close()
	temp := 0.2
	c := NewChat(ChatConfig{BaseURL: srv.URL, Model: "m", Temperature: &temp, MaxTokens: 50})
	stop := errors.New("stop")
	n := 0
	err := c.Stream(context.Background(), nil, func(string) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Errorf("err=%v n=%d", err, n)
	}
}

func TestChatCancelClosesStream(t *testing.T) {
	released := make(chan struct{})
	srv := sseServer(t, []string{delta("a")}, func(w http.ResponseWriter) {
		<-released
	})
	defer srv.Close()
	defer close(released)
	temp := 0.2
	c := NewChat(ChatConfig{BaseURL: srv.URL, Model: "m", Temperature: &temp, MaxTokens: 50})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Stream(ctx, nil, func(string) error { cancel(); return nil })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed on cancel")
	}
}

func TestChatRetryBeforeStartOnly(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "busy", 503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, delta("ok")+"data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewChat(ChatConfig{BaseURL: srv.URL, Model: "m", Backoff: time.Millisecond})
	var sb strings.Builder
	if err := c.Stream(context.Background(), nil, func(s string) error { sb.WriteString(s); return nil }); err != nil || sb.String() != "ok" {
		t.Fatalf("%q %v", sb.String(), err)
	}
	if calls != 2 {
		t.Errorf("calls=%d", calls)
	}
}

func TestChatNonStreamingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"entero"}}]}`)
	}))
	defer srv.Close()
	var sb strings.Builder
	if err := NewChat(ChatConfig{BaseURL: srv.URL}).Stream(context.Background(), nil, func(s string) error { sb.WriteString(s); return nil }); err != nil || sb.String() != "entero" {
		t.Fatalf("%q %v", sb.String(), err)
	}
}

func TestParseSSESplitFrames(t *testing.T) {
	input := ": ping\r\n\r\ndata: {\"a\":1}\r\n\r\nevent: x\ndata: l1\ndata: l2\n\ndata:nospace\n\ndata: [DONE]\n\n"
	for name, r := range map[string]io.Reader{
		"onebyte": iotest.OneByteReader(strings.NewReader(input)),
		"half":    iotest.HalfReader(strings.NewReader(input)),
		"whole":   strings.NewReader(input),
	} {
		var got []string
		err := parseSSE(r, func(d string) (bool, error) { got = append(got, d); return d == "[DONE]", nil })
		want := []string{`{"a":1}`, "l1\nl2", "nospace", "[DONE]"}
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %q err=%v", name, got, err)
		}
	}
	// no trailing blank line at EOF
	var got []string
	parseSSE(strings.NewReader("data: last"), func(d string) (bool, error) { got = append(got, d); return false, nil })
	if len(got) != 1 || got[0] != "last" {
		t.Errorf("eof flush: %q", got)
	}
}

// ---- health

func TestProberCache(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.WriteString(w, `{"data":[{"id":"m"}]}`)
	}))
	defer srv.Close()
	p := NewProber(srv.URL, "", "m", time.Hour, time.Second)
	now := time.Now()
	p.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if h := p.Check(context.Background()); !h.OK {
			t.Fatal(h)
		}
	}
	if calls != 1 {
		t.Errorf("calls=%d", calls)
	}
	now = now.Add(2 * time.Hour)
	p.Check(context.Background())
	if calls != 2 {
		t.Errorf("calls=%d", calls)
	}
	p2 := NewProber(srv.URL, "", "otro", time.Hour, time.Second)
	if h := p2.Check(context.Background()); !h.OK || !strings.Contains(h.Detail, "otro") {
		t.Errorf("%+v", h)
	}
}

func TestStatusDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	s := NewStatus(NewEmbedder(EmbedConfig{BaseURL: url, Model: "e"}), NewChat(ChatConfig{BaseURL: url, Model: "c"}), time.Minute, time.Second)
	c, e := s.LLMHealth(context.Background())
	if c.OK || e.OK || c.Detail == "" {
		t.Errorf("%+v %+v", c, e)
	}
	c, e = NewStatus(nil, nil, 0, 0).LLMHealth(context.Background())
	if c.OK || e.OK {
		t.Error("nil clients must be not ok")
	}
}

// ---- chunker

func TestChunkText(t *testing.T) {
	if ChunkText("", 100, 10) != nil || ChunkText("  \n ", 100, 10) != nil {
		t.Error("empty must be nil")
	}
	if c := ChunkText("Hola mundo.", 100, 10); len(c) != 1 || c[0] != "Hola mundo." {
		t.Errorf("%q", c)
	}
	es := "El Gobierno ha aprobado hoy el decreto. La oposición, sin embargo, lo considera «insuficiente». ¿Qué pasará ahora? Nadie lo sabe con certeza; los analistas esperan más ruido que nueces.\n\nSegundo párrafo con acentos: canción, niño, pingüino. " + strings.Repeat("Otra frase más larga de relleno para forzar el troceado. ", 20)
	chunks := ChunkText(es, 200, 40)
	if len(chunks) < 3 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if runeLen(c) > 200 || strings.TrimSpace(c) == "" {
			t.Errorf("chunk %d bad (%d runes)", i, runeLen(c))
		}
	}
	if !strings.HasPrefix(chunks[0], "El Gobierno ha aprobado hoy el decreto.") {
		t.Errorf("first chunk: %q", chunks[0])
	}
	// overlap: second chunk starts with words from the end of first
	if len(chunks) > 1 {
		w := strings.Fields(chunks[0])
		last := w[len(w)-1]
		if !strings.Contains(chunks[1][:min(len(chunks[1]), 60)], last) {
			t.Errorf("no overlap: %q -> %q", chunks[0][len(chunks[0])-30:], chunks[1][:40])
		}
	}
	// no unbroken text / no space at all
	long := strings.Repeat("ñ", 1000)
	for _, c := range ChunkText(long, 300, 30) {
		if runeLen(c) > 300 {
			t.Errorf("long unit chunk %d", runeLen(c))
		}
	}
	// very long
	big := strings.Repeat("Una frase corta. ", 5000)
	cs := ChunkText(big, 500, 50)
	if len(cs) < 100 {
		t.Errorf("chunks=%d", len(cs))
	}
	for _, c := range cs {
		if runeLen(c) > 500 {
			t.Fatal("too long")
		}
	}
	// maxChars <= 0 => single chunk
	if len(ChunkText(big, 0, 0)) != 1 {
		t.Error("maxChars 0")
	}
}

// ---- worker

type fakeStore struct {
	domain.Store
	mu    sync.Mutex
	arts  []domain.Article
	saved map[int64][]domain.ChunkVec
}

func (f *fakeStore) ArticlesNeedingEmbedding(_ context.Context, _ string, limit int) ([]domain.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Article
	for _, a := range f.arts {
		if _, ok := f.saved[a.ID]; !ok && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) SaveEmbeddings(_ context.Context, id int64, _ string, ch []domain.ChunkVec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[id] = ch
	return nil
}

func (f *fakeStore) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.saved) }

type flakyEmb struct {
	down  atomic.Bool
	calls atomic.Int32
	texts []string
	mu    sync.Mutex
}

func (e *flakyEmb) Model() string { return "m" }
func (e *flakyEmb) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	if e.down.Load() {
		return nil, errors.New("connection refused")
	}
	e.mu.Lock()
	e.texts = append(e.texts, texts...)
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func newFS(n int) *fakeStore {
	fs := &fakeStore{saved: map[int64][]domain.ChunkVec{}}
	for i := 1; i <= n; i++ {
		fs.arts = append(fs.arts, domain.Article{ID: int64(i), Title: fmt.Sprintf("Título %d", i), Content: "Contenido del artículo. Con dos frases."})
	}
	return fs
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWorkerRunOnceChunksAndEmpty(t *testing.T) {
	fs := &fakeStore{saved: map[int64][]domain.ChunkVec{}}
	fs.arts = []domain.Article{
		{ID: 1, Title: "Solo título"},
		{ID: 2, Title: "T2", Summary: "Resumen"},
		{ID: 3, URL: "http://x/3"},
		{ID: 4, Title: "Largo", Content: strings.Repeat("Frase de prueba número uno. ", 100)},
	}
	emb := &flakyEmb{}
	w := NewEmbedWorker(fs, emb, WorkerOptions{BatchSize: 10, MaxChunkChars: 300, Overlap: 30, Log: quiet()})
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := fs.saved[1][0].Text; got != "Solo título" {
		t.Errorf("%q", got)
	}
	if got := fs.saved[3][0].Text; got != "http://x/3" {
		t.Errorf("%q", got)
	}
	if !strings.HasPrefix(fs.saved[2][0].Text, "T2") {
		t.Errorf("%q", fs.saved[2][0].Text)
	}
	if len(fs.saved[4]) < 5 || !strings.HasPrefix(fs.saved[4][0].Text, "Largo") {
		t.Errorf("chunks=%d", len(fs.saved[4]))
	}
	for i, c := range fs.saved[4] {
		if c.Index != i || len(c.Vec) != 2 {
			t.Errorf("chunk %d: %+v", i, c)
		}
	}
	if n, _ := w.RunOnce(context.Background()); n != 0 {
		t.Errorf("second run n=%d", n)
	}
}

func TestWorkerOutageRecovery(t *testing.T) {
	fs := newFS(5)
	emb := &flakyEmb{}
	emb.down.Store(true)
	var logBuf strings.Builder
	var lmu sync.Mutex
	lg := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) { lmu.Lock(); logBuf.Write(p); lmu.Unlock() }), nil))
	w := NewEmbedWorker(fs, emb, WorkerOptions{BatchSize: 2, Interval: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, Log: lg})
	if n, err := w.RunOnce(context.Background()); err == nil || n != 0 {
		t.Fatalf("expected error while down, n=%d err=%v", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	time.Sleep(150 * time.Millisecond)
	if emb.calls.Load() < 3 {
		t.Errorf("worker should keep retrying, calls=%d", emb.calls.Load())
	}
	emb.down.Store(false)
	deadline := time.Now().Add(3 * time.Second)
	for fs.count() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fs.count() != 5 {
		t.Fatalf("saved=%d", fs.count())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	lmu.Lock()
	defer lmu.Unlock()
	if c := strings.Count(logBuf.String(), "no disponibles"); c != 1 {
		t.Errorf("outage logged %d times:\n%s", c, logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "operativos de nuevo") {
		t.Error("recovery not logged")
	}
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

type rejectEmb struct{ flakyEmb }

func (e *rejectEmb) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	for _, t := range texts {
		if strings.Contains(t, "VENENO") {
			return nil, &HTTPError{Status: 400, Body: "too long"}
		}
	}
	return e.flakyEmb.Embed(ctx, texts)
}

func TestWorkerSkipsPoisonArticle(t *testing.T) {
	fs := newFS(3)
	fs.arts[0].Content = "VENENO"
	w := NewEmbedWorker(fs, &rejectEmb{}, WorkerOptions{BatchSize: 2, Log: quiet()})
	for i := 0; i < 5; i++ {
		if _, err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fs.count() != 2 {
		t.Errorf("saved=%d", fs.count())
	}
	if _, ok := fs.saved[1]; ok {
		t.Error("poison saved")
	}
}
