package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ---- job rows ----

type jobStore struct {
	*fakeStore
	mu    sync.Mutex
	rows  map[int64]*domain.JobRun
	next  int64
	names []string
}

func (j *jobStore) StartJob(_ context.Context, job string) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rows == nil {
		j.rows = map[int64]*domain.JobRun{}
	}
	j.next++
	j.rows[j.next] = &domain.JobRun{ID: j.next, Job: job, StartedAt: time.Now()}
	j.names = append(j.names, job)
	return j.next, nil
}

func (j *jobStore) FinishJob(_ context.Context, id int64, ok bool, msg string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := j.rows[id]
	if r == nil {
		return errors.New("unknown")
	}
	r.FinishedAt, r.OK, r.Message = time.Now(), ok, msg
	return nil
}

func (j *jobStore) finished(job string) *domain.JobRun {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, r := range j.rows {
		if r.Job == job && !r.FinishedAt.IsZero() {
			c := *r
			return &c
		}
	}
	return nil
}

type fakeMaint struct {
	err error
	ran chan struct{}
}

func (f *fakeMaint) RunOnce(context.Context) (string, error) {
	if f.ran != nil {
		f.ran <- struct{}{}
	}
	return "4 artículos borrados", f.err
}
func (f *fakeMaint) Preview(context.Context) (domain.MaintenancePreview, error) {
	return domain.MaintenancePreview{ArticlesDays: 30, Enabled: true, WouldDelete: 12}, nil
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestUIJobsAreRecorded(t *testing.T) {
	var js *jobStore
	var ing *blockingJob
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) {
		js = &jobStore{fakeStore: d.Store.(*fakeStore)}
		d.Store = js
		ing = d.Ingestor.(*blockingJob)
		d.Maintenance = &fakeMaint{err: errors.New("boom")}
	})
	hdr := map[string]string{"X-Requested-With": "t"}
	if rec := do(h, "POST", "/api/jobs/ingest", hdr, ""); rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}
	<-ing.started
	if rec := do(h, "POST", "/api/jobs/ingest", hdr, ""); rec.Code != 409 {
		t.Fatalf("409 expected, got %d", rec.Code)
	}
	close(ing.release)
	waitFor(t, "ingest row", func() bool { return js.finished("ingest") != nil })
	if r := js.finished("ingest"); !r.OK || r.Message != "1 nuevas" {
		t.Errorf("%+v", r)
	}
	// failing maintenance is recorded as not OK
	if rec := do(h, "POST", "/api/jobs/maintenance", hdr, ""); rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}
	waitFor(t, "maintenance row", func() bool { return js.finished("maintenance") != nil })
	if r := js.finished("maintenance"); r.OK || !strings.Contains(r.Message, "boom") {
		t.Errorf("%+v", r)
	}
	// 503 when not configured
	h2, _, _, _ := newHandler(t, nil)
	if rec := do(h2, "POST", "/api/jobs/maintenance", hdr, ""); rec.Code != 503 {
		t.Errorf("%d", rec.Code)
	}
}

func TestMaintenancePreview(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Maintenance = &fakeMaint{} })
	rec := do(h, "GET", "/api/maintenance", nil, "")
	var p domain.MaintenancePreview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.WouldDelete != 12 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	h2, _, _, _ := newHandler(t, nil)
	if rec := do(h2, "GET", "/api/maintenance", nil, ""); rec.Code != 503 {
		t.Errorf("%d", rec.Code)
	}
}

// ---- readyz ----

type fakeBeat struct{ t time.Time }

func (f fakeBeat) LastBeat() time.Time { return f.t }

type failPinger struct {
	*fakeStore
	readErr, writeErr error
}

func (f failPinger) PingRead(context.Context) error  { return f.readErr }
func (f failPinger) PingWrite(context.Context) error { return f.writeErr }

func TestReadyz(t *testing.T) {
	type res struct {
		Status string               `json:"status"`
		Checks map[string]checkJSON `json:"checks"`
	}
	get := func(mod func(*Deps, *Options)) (int, res) {
		h, _, _, _ := newHandler(t, mod)
		rec := do(h, "GET", "/readyz", nil, "")
		var r res
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err, rec.Body)
		}
		return rec.Code, r
	}
	if code, r := get(nil); code != 200 || r.Status != "ok" || !r.Checks["scheduler"].OK {
		t.Errorf("default: %d %+v", code, r)
	}
	if code, _ := get(func(d *Deps, o *Options) { d.Heartbeat = fakeBeat{time.Now()} }); code != 200 {
		t.Errorf("fresh beat: %d", code)
	}
	if code, r := get(func(d *Deps, o *Options) { d.Heartbeat = fakeBeat{time.Now().Add(-10 * time.Minute)} }); code != 503 || r.Checks["scheduler"].OK {
		t.Errorf("stale beat: %d %+v", code, r)
	}
	if code, _ := get(func(d *Deps, o *Options) { d.Heartbeat = fakeBeat{} }); code != 503 {
		t.Errorf("not started: %d", code)
	}
	if code, r := get(func(d *Deps, o *Options) {
		d.Store = failPinger{fakeStore: d.Store.(*fakeStore), writeErr: errors.New("locked")}
	}); code != 503 || !r.Checks["db_read"].OK || r.Checks["db_write"].OK {
		t.Errorf("write fail: %d %+v", code, r)
	}
	if code, r := get(func(d *Deps, o *Options) {
		d.Store = failPinger{fakeStore: d.Store.(*fakeStore), readErr: errors.New("io")}
	}); code != 503 || r.Checks["db_read"].OK {
		t.Errorf("read fail: %d %+v", code, r)
	}
	// auth-exempt, while /metrics is not
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { o.BasicAuthUser, o.BasicAuthPass = "u", "p" })
	if rec := do(h, "GET", "/readyz", nil, ""); rec.Code != 200 {
		t.Errorf("readyz auth: %d", rec.Code)
	}
	if rec := do(h, "GET", "/metrics", nil, ""); rec.Code != 401 {
		t.Errorf("metrics must need auth: %d", rec.Code)
	}
	if rec := do(h, "GET", "/healthz", nil, ""); rec.Code != 200 {
		t.Errorf("healthz: %d", rec.Code)
	}
}

// ---- metrics ----

var sampleRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(.*)\})? (\S+)$`)
var labelRe = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)

// parseExposition validates the text format and returns series -> value.
func parseExposition(t *testing.T, body string) (map[string]string, map[string]string) {
	t.Helper()
	series := map[string]string{}
	types := map[string]string{}
	helps := map[string]bool{}
	for i, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			n := strings.Fields(line)[2]
			if helps[n] {
				t.Errorf("line %d: duplicate HELP %s", i+1, n)
			}
			helps[n] = true
		case strings.HasPrefix(line, "# TYPE "):
			f := strings.Fields(line)
			if _, dup := types[f[2]]; dup {
				t.Errorf("line %d: duplicate TYPE %s", i+1, f[2])
			}
			types[f[2]] = f[3]
		case strings.HasPrefix(line, "#"):
		default:
			m := sampleRe.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("line %d: invalid sample %q", i+1, line)
				continue
			}
			if _, ok := series[m[1]+m[2]]; ok {
				t.Errorf("duplicate series %s%s", m[1], m[2])
			}
			if m[3] != "" && labelRe.ReplaceAllString(m[3], "") != strings.Repeat(",", strings.Count(m[3], ",")) {
				t.Errorf("line %d: bad labels %q", i+1, m[3])
			}
			series[m[1]+m[2]] = m[4]
			base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(m[1], "_bucket"), "_sum"), "_count")
			if _, ok := types[m[1]]; !ok {
				if _, ok := types[base]; !ok {
					t.Errorf("line %d: sample %s without TYPE", i+1, m[1])
				}
			}
		}
	}
	return series, types
}

func TestMetricsExposition(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) {
		js := &jobStore{fakeStore: d.Store.(*fakeStore)}
		id, _ := js.StartJob(context.Background(), "ingest")
		_ = js.FinishJob(context.Background(), id, true, "ok")
		js.fakeStore.jobs = []domain.JobRun{{ID: 1, Job: "ingest", StartedAt: time.Now().Add(-time.Minute), FinishedAt: time.Now(), OK: true}}
		d.Store = js
		d.Heartbeat = fakeBeat{time.Now()}
	})
	// generate traffic: many distinct article ids must not create many series
	for i := 1; i <= 30; i++ {
		do(h, "GET", "/articulo/"+string(rune('0'+i%10)), nil, "")
	}
	do(h, "GET", "/api/articles", nil, "")
	do(h, "GET", "/no/existe/"+"abc", nil, "")
	rec := do(h, "GET", "/metrics", nil, "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	series, types := parseExposition(t, rec.Body.String())
	for _, want := range []string{"agregador_articles_total", "agregador_db_bytes", "agregador_shots_bytes", "agregador_articles_embedded",
		"agregador_articles_embed_pending", "agregador_screenshots_total", "agregador_store_up", "go_goroutines",
		"agregador_job_last_run_timestamp_seconds", "agregador_job_last_success_timestamp_seconds", "agregador_llm_up",
		"agregador_http_requests_total", "agregador_http_request_duration_seconds", "agregador_scheduler_last_beat_timestamp_seconds", "agregador_build_info"} {
		if _, ok := types[want]; !ok {
			t.Errorf("missing family %s", want)
		}
	}
	if types["agregador_http_request_duration_seconds"] != "histogram" || types["agregador_http_requests_total"] != "counter" {
		t.Errorf("types %v", types)
	}
	if series[`agregador_http_requests_total{route="GET /articulo/{id}",class="2xx"}`] == "" ||
		series[`agregador_http_request_duration_seconds_count{route="GET /articulo/{id}"}`] != "30" {
		t.Errorf("route pattern counting: %v", series)
	}
	if series[`agregador_http_requests_total{route="unmatched",class="4xx"}`] != "1" {
		t.Errorf("unmatched not bucketed")
	}
	routes := map[string]bool{}
	for s := range series {
		if m := regexp.MustCompile(`route="([^"]*)"`).FindStringSubmatch(s); m != nil {
			routes[m[1]] = true
		}
	}
	if len(routes) > 6 {
		t.Errorf("route cardinality too high: %v", routes)
	}
	// histogram: +Inf bucket equals count
	for s, v := range series {
		if strings.HasPrefix(s, `agregador_http_request_duration_seconds_bucket{route="GET /api/articles",le="+Inf"`) {
			if series[`agregador_http_request_duration_seconds_count{route="GET /api/articles"}`] != v {
				t.Errorf("+Inf bucket != count")
			}
		}
	}
}

func TestMetricsLabelEscaping(t *testing.T) {
	if got := labelStr([]string{"a", "x\"y\\z\nw"}); got != `{a="x\"y\\z\nw"}` {
		t.Errorf("%s", got)
	}
}

// ---- OpenAPI ----

var routeRe = regexp.MustCompile(`Handle(?:Func)?\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]*)"`)

func TestOpenAPIMatchesRoutes(t *testing.T) {
	var spec struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openapiYAML, &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") || len(spec.Paths) == 0 {
		t.Fatalf("bad spec header %q", spec.OpenAPI)
	}
	inSpec := map[string]bool{}
	for p, ms := range spec.Paths {
		for m := range ms {
			inSpec[strings.ToUpper(m)+" "+p] = true
		}
	}
	files, _ := filepath.Glob("*.go")
	inCode := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range routeRe.FindAllStringSubmatch(string(b), -1) {
			inCode[m[1]+" "+m[2]] = true
		}
	}
	for r := range inCode {
		if !inSpec[r] {
			t.Errorf("route %s is not documented in openapi.yaml", r)
		}
	}
	for r := range inSpec {
		if !inCode[r] {
			t.Errorf("openapi.yaml documents %s but no such route is registered", r)
		}
	}
	// docs/openapi.yaml must be the same file
	if b, err := os.ReadFile("../../docs/openapi.yaml"); err != nil || string(b) != string(openapiYAML) {
		t.Errorf("docs/openapi.yaml differs from the embedded spec: %v", err)
	}
}

func TestOpenAPIServed(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	rec := do(h, "GET", "/api/openapi.yaml", nil, "")
	if rec.Code != 200 || rec.Body.String() != string(openapiYAML) {
		t.Fatalf("%d", rec.Code)
	}
	rec = do(h, "GET", "/api/docs", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/api/articles") || strings.Contains(rec.Body.String(), "http://") && strings.Contains(rec.Body.String(), "<script") {
		t.Fatalf("%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script") {
		t.Error("docs page must not need scripts")
	}
}

// ---- export ----

func TestExportNDJSON(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	rec := do(h, "GET", "/api/export/articles.ndjson", nil, "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %d: %s", len(lines), rec.Body)
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err, l)
		}
		for k := range m {
			if k != "title" && k != "url" && k != "source" && k != "published" && k != "summary" {
				t.Errorf("unexpected field %s", k)
			}
		}
		if m["title"] == "" || m["url"] == "" || m["source"] == "" {
			t.Errorf("incomplete %v", m)
		}
	}
	if strings.Contains(rec.Body.String(), "Línea uno") {
		t.Error("content must not be exported")
	}
	if strings.Contains(rec.Body.String(), "\\u003c") {
		t.Error("HTML escaping should be off")
	}
	rec = do(h, "GET", "/api/export/articles.ndjson?limit=1", nil, "")
	if n := len(strings.Split(strings.TrimSpace(rec.Body.String()), "\n")); n != 1 {
		t.Errorf("limit: %d lines", n)
	}
}

type pagedStore struct {
	*fakeStore
	total int
}

func (p pagedStore) ListArticles(_ context.Context, f domain.Filter) ([]domain.Article, error) {
	var out []domain.Article
	for i := f.Offset; i < p.total && len(out) < f.Limit; i++ {
		out = append(out, domain.Article{ID: int64(i + 1), SourceID: "abc", URL: "http://x/", Title: "t"})
	}
	return out, nil
}

func TestExportPagesAndLimiter(t *testing.T) {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Store = pagedStore{d.Store.(*fakeStore), 1234} })
	rec := do(h, "GET", "/api/export/articles.ndjson", nil, "")
	if n := len(strings.Split(strings.TrimSpace(rec.Body.String()), "\n")); n != 1234 {
		t.Errorf("paged export: %d lines", n)
	}
	// limiter: occupy the slot
	var srv *server
	h2, _, _, _ := newHandler(t, nil)
	_ = h2
	srv = &server{ops: newOpsState()}
	srv.ops.export <- struct{}{}
	if len(srv.ops.export) != 1 {
		t.Fatal("limiter")
	}
}
