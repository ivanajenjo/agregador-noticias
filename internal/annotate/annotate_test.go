package annotate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const goodJSON = `{"tone":"crítico","intent":"opinar","frame":"conflicto","confidence":0.8,"rationale":"Juicio explícito.","is_opinion":true}`

func TestParse(t *testing.T) {
	cases := []struct {
		name, in string
		tone     string
		conf     float64
		err      bool
		problems int
	}{
		{"plain", goodJSON, "crítico", 0.8, false, 0},
		{"fenced", "Aquí tienes:\n```json\n" + goodJSON + "\n```\nEspero que ayude", "crítico", 0.8, false, 0},
		{"think", "<think>Voy a pensar {\"tone\":\"neutral\"}</think>\n" + goodJSON, "crítico", 0.8, false, 0},
		{"think closing only", "razonamiento largo</think>" + goodJSON, "crítico", 0.8, false, 0},
		{"trailing text", goodJSON + "\nNota: es una columna.", "crítico", 0.8, false, 0},
		{"accents and case", `{"tone":"Critico","intent":"OPINAR","frame":"Interes Humano","confidence":"85%","rationale":"x","is_opinion":"sí"}`, "crítico", 0.85, false, 0},
		{"percent number", `{"tone":"neutral","intent":"informar","frame":"otros","confidence":90}`, "neutral", 0.9, false, 0},
		{"invalid enum dropped", `{"tone":"furioso","intent":"informar","frame":"otros","confidence":0.7}`, "", 0.7, false, 1},
		{"unclosed think", "<think>no termina {\"tone\":\"neutral\"", "", 0, true, 0},
		{"no json", "lo siento, no puedo", "", 0, true, 0},
		{"braces in string", `{"tone":"neutral","intent":"informar","frame":"otros","confidence":0.5,"rationale":"usa } y { raros"}`, "neutral", 0.5, false, 0},
		{"out of range conf", `{"tone":"neutral","intent":"informar","frame":"otros","confidence":7000}`, "neutral", 0, false, 0},
	}
	for _, c := range cases {
		r, err := Parse(c.in)
		if (err != nil) != c.err {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if err != nil {
			continue
		}
		if r.Tone != c.tone || r.Confidence != c.conf || len(r.Problems) != c.problems {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	long := strings.Repeat("á", 300)
	r, _ := Parse(`{"tone":"neutral","rationale":"` + long + `"}`)
	if n := len([]rune(r.Rationale)); n != 120 {
		t.Errorf("rationale len %d", n)
	}
	r, _ = Parse(goodJSON)
	if r.IsOpinion == nil || !*r.IsOpinion || r.Frame != "conflicto" {
		t.Errorf("%+v", r)
	}
}

// ---- fakes ----

type memStore struct {
	mu   sync.Mutex
	arts []domain.Article
	rows map[string]domain.Annotation
}

func newMem(n int) *memStore {
	m := &memStore{rows: map[string]domain.Annotation{}}
	for i := n; i >= 1; i-- { // newest first
		m.arts = append(m.arts, domain.Article{ID: int64(i), Title: fmt.Sprintf("Titular %d", i), Summary: "resumen"})
	}
	return m
}
func key(id int64, k, m string) string { return fmt.Sprint(id, "|", k, "|", m) }
func (m *memStore) SaveAnnotation(_ context.Context, a domain.Annotation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key(a.ArticleID, a.Kind, a.Model)] = a
	return nil
}
func (m *memStore) ListAnnotations(context.Context, int64) ([]domain.Annotation, error) {
	return nil, nil
}
func (m *memStore) CountAnnotationsByLabel(context.Context, string, string, domain.Filter) (map[string]int64, error) {
	return nil, nil
}
func (m *memStore) ArticlesNeedingAnnotation(_ context.Context, kind, model string, limit int) ([]domain.Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Article
	for _, a := range m.arts {
		if _, ok := m.rows[key(a.ID, kind, model)]; !ok && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

type fakeLLM struct {
	calls atomic.Int32
	fn    func(n int, msgs []Message) (string, error)
}

func (f *fakeLLM) Complete(_ context.Context, m []Message, _ CallOptions) (string, error) {
	return f.fn(int(f.calls.Add(1)), m)
}

func TestWorkerSavesRowsPerKindAndDedups(t *testing.T) {
	st := newMem(3)
	f := &fakeLLM{fn: func(int, []Message) (string, error) { return "```json\n" + goodJSON + "\n```", nil }}
	w := NewWorker(st, f, WorkerOptions{Model: "m1", BatchSize: 10})
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if len(st.rows) != 12 { // 3 articles x tone,intent,frame,opinion
		t.Fatalf("rows %d", len(st.rows))
	}
	a := st.rows[key(1, "tone", "m1")]
	if a.Label != "crítico" || a.Score != 0.8 || a.Model != "m1" {
		t.Fatalf("%+v", a)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(a.Data), &d); err != nil || d["rationale"] != "Juicio explícito." {
		t.Fatalf("%v %v", d, err)
	}
	if st.rows[key(2, "opinion", "m1")].Label != "sí" {
		t.Fatal("opinion")
	}
	calls := f.calls.Load()
	if n, _ := w.RunOnce(context.Background()); n != 0 || f.calls.Load() != calls {
		t.Fatal("second run must not call the model again")
	}
	// another model re-annotates without touching history
	w2 := NewWorker(st, f, WorkerOptions{Model: "m2", BatchSize: 10})
	if n, _ := w2.RunOnce(context.Background()); n != 3 || len(st.rows) != 24 {
		t.Fatal(n, len(st.rows))
	}
}

func TestWorkerBatchSizeAndNewestFirst(t *testing.T) {
	st := newMem(5)
	var order []string
	f := &fakeLLM{fn: func(_ int, m []Message) (string, error) {
		order = append(order, strings.SplitN(m[1].Content, "\n", 2)[0])
		return goodJSON, nil
	}}
	w := NewWorker(st, f, WorkerOptions{Model: "m", BatchSize: 2})
	n, _ := w.RunOnce(context.Background())
	if n != 2 || len(order) != 2 || order[0] != "Titular: Titular 5" || order[1] != "Titular: Titular 4" {
		t.Fatal(n, order)
	}
}

func TestWorkerInvalidOutputRetriedThenSkipped(t *testing.T) {
	st := newMem(2)
	f := &fakeLLM{fn: func(_ int, m []Message) (string, error) {
		if strings.Contains(m[1].Content, "Titular 2") {
			return "no sé", nil
		}
		return goodJSON, nil
	}}
	w := NewWorker(st, f, WorkerOptions{Model: "m", BatchSize: 5, Retries: 1})
	for i := 0; i < 5; i++ {
		if _, err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := st.rows[key(1, "tone", "m")]; !ok {
		t.Fatal("good article not saved")
	}
	if _, ok := st.rows[key(2, "tone", "m")]; ok {
		t.Fatal("invalid article saved")
	}
	// 3 failures x 2 attempts for article 2 + 1 call for article 1 = 7, then skipped for good
	if c := f.calls.Load(); c != 7 {
		t.Fatalf("calls %d", c)
	}
}

func TestWorkerInvalidEnumDiscardedPartial(t *testing.T) {
	st := newMem(1)
	f := &fakeLLM{fn: func(int, []Message) (string, error) {
		return `{"tone":"neutral","intent":"gritar","frame":"otros","confidence":0.6}`, nil
	}}
	w := NewWorker(st, f, WorkerOptions{Model: "m"})
	if n, _ := w.RunOnce(context.Background()); n != 1 {
		t.Fatal(n)
	}
	if _, ok := st.rows[key(1, "intent", "m")]; ok {
		t.Fatal("invalid intent must be discarded")
	}
	if st.rows[key(1, "tone", "m")].Label != "neutral" || st.rows[key(1, "frame", "m")].Label != "otros" {
		t.Fatal("valid kinds must be kept")
	}
}

func TestWorkerOutageAbortsAndRecovers(t *testing.T) {
	st := newMem(3)
	down := atomic.Bool{}
	down.Store(true)
	f := &fakeLLM{fn: func(int, []Message) (string, error) {
		if down.Load() {
			return "", &HTTPError{Status: 503, Body: "loading"}
		}
		return goodJSON, nil
	}}
	w := NewWorker(st, f, WorkerOptions{Model: "m"})
	if n, err := w.RunOnce(context.Background()); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("an outage must stop the batch at the first call, got %d", f.calls.Load())
	}
	down.Store(false)
	if n, err := w.RunOnce(context.Background()); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}

func TestIsOutage(t *testing.T) {
	if !IsOutage(&HTTPError{Status: 502}) || !IsOutage(errors.New("connection refused")) {
		t.Fatal("should be outage")
	}
	if IsOutage(&HTTPError{Status: 400}) || IsOutage(ErrUnusable) || IsOutage(context.Canceled) {
		t.Fatal("should not be outage")
	}
}

func TestClientAndProbeAgainstFakeServer(t *testing.T) {
	var gotAuth string
	var sawJSONMode, rejectJSON atomic.Bool
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(404) // any HTTP answer means reachable
		case "/v1/chat/completions":
			gotAuth = r.Header.Get("Authorization")
			reqBody = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if _, ok := reqBody["response_format"]; ok {
				sawJSONMode.Store(true)
				if rejectJSON.Load() {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"unsupported response_format"}`))
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": goodJSON}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "k", "small")
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed := 7
	out, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hola"}}, CallOptions{Seed: &seed, JSON: true})
	if err != nil || out != goodJSON || gotAuth != "Bearer k" || !sawJSONMode.Load() {
		t.Fatal(out, err, gotAuth)
	}
	if reqBody["model"] != "small" || reqBody["temperature"] != float64(0) || reqBody["seed"] != float64(7) || reqBody["stream"] != false {
		t.Fatalf("%v", reqBody)
	}
	// server without json mode: fall back and remember
	rejectJSON.Store(true)
	sawJSONMode.Store(false)
	if out, err := c.Complete(context.Background(), nil, CallOptions{JSON: true}); err != nil || out != goodJSON {
		t.Fatal(out, err)
	}
	sawJSONMode.Store(false)
	_, _ = c.Complete(context.Background(), nil, CallOptions{JSON: true})
	if sawJSONMode.Load() {
		t.Fatal("must stop sending response_format after a rejection")
	}
	srv.Close()
	if err := c.Probe(context.Background()); err == nil {
		t.Fatal("probe must fail when the server is down")
	}
}

func TestWorkerSkipsBatchWhenServerUnreachableAndLogsOnce(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	st := newMem(2)
	c := NewClient(url, "", "m")
	w := NewWorker(st, c, WorkerOptions{Model: "m", Interval: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond})
	if n, err := w.RunOnce(context.Background()); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	w.Run(ctx) // must return on ctx done, nothing saved
	if len(st.rows) != 0 {
		t.Fatal("nothing may be annotated while down")
	}
}

func TestArticleTimeoutIsPerArticle(t *testing.T) {
	st := newMem(2)
	f := &fakeLLM{}
	f.fn = func(n int, m []Message) (string, error) {
		if strings.Contains(m[1].Content, "Titular 2") {
			time.Sleep(200 * time.Millisecond) // hangs
			return "", context.DeadlineExceeded
		}
		return goodJSON, nil
	}
	cf := completerFunc(func(ctx context.Context, m []Message, o CallOptions) (string, error) {
		if strings.Contains(m[1].Content, "Titular 2") {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return f.Complete(ctx, m, o)
	})
	w := NewWorker(st, cf, WorkerOptions{Model: "m", ArticleTimeout: 30 * time.Millisecond})
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

type completerFunc func(context.Context, []Message, CallOptions) (string, error)

func (f completerFunc) Complete(c context.Context, m []Message, o CallOptions) (string, error) {
	return f(c, m, o)
}

func TestMessagesContent(t *testing.T) {
	m := Messages(Input{Title: "  T\n x ", Summary: "S", Content: strings.Repeat("a", 2000)})
	u := m[1].Content
	if !strings.Contains(u, "Titular: T x") || !strings.Contains(u, "Resumen: S") {
		t.Fatal(u)
	}
	if n := strings.Count(u, "a"); n > 640 { // 600 chars of body (+ a few letters in labels)
		t.Fatalf("content not truncated: %d", n)
	}
	if !strings.Contains(m[0].Content, "interés humano") || !strings.Contains(m[0].Content, "sensacionalista") {
		t.Fatal("system prompt lacks vocab")
	}
}
