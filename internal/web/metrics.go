package web

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Prometheus text exposition (format 0.0.4) without external dependencies.
// Label cardinality is bounded: routes are mux patterns (a fixed set), status
// is the class (2xx..5xx), sources and jobs are capped.

const (
	maxRouteLabels  = 100
	maxSourceLabels = 150
	maxJobLabels    = 20
	storeCacheTTL   = 10 * time.Second
)

var httpBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

type reqKey struct{ route, class string }

type hist struct {
	counts []uint64 // per bucket, non-cumulative
	sum    float64
	n      uint64
}

type opsState struct {
	start    time.Time
	mu       sync.Mutex
	reqs     map[reqKey]uint64
	hists    map[string]*hist
	routes   map[string]bool
	inflight atomic.Int64
	export   chan struct{} // limits concurrent exports to one

	cmu     sync.Mutex
	cacheAt time.Time
	cache   []*family
}

func newOpsState() *opsState {
	return &opsState{start: time.Now(), reqs: map[reqKey]uint64{}, hists: map[string]*hist{},
		routes: map[string]bool{}, export: make(chan struct{}, 1)}
}

func (o *opsState) observe(route string, status int, d time.Duration) {
	class := strconv.Itoa(status/100) + "xx"
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.routes[route] {
		if len(o.routes) >= maxRouteLabels {
			route = "other"
		}
		o.routes[route] = true
	}
	o.reqs[reqKey{route, class}]++
	h := o.hists[route]
	if h == nil {
		h = &hist{counts: make([]uint64, len(httpBuckets))}
		o.hists[route] = h
	}
	sec := d.Seconds()
	h.sum += sec
	h.n++
	for i, b := range httpBuckets {
		if sec <= b {
			h.counts[i]++
			break
		}
	}
}

// metricsMW records request count and latency by mux route pattern.
func (s *server) metricsMW(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pat := mux.Handler(r)
		switch pat {
		case "", "/":
			pat = "unmatched"
		}
		start := time.Now()
		s.ops.inflight.Add(1)
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			s.ops.inflight.Add(-1)
			st := sw.status
			if st == 0 {
				st = 200
			}
			if v := recover(); v != nil { // recoverMW (outer) answers 500
				s.ops.observe(pat, 500, time.Since(start))
				panic(v)
			}
			s.ops.observe(pat, st, time.Since(start))
		}()
		next.ServeHTTP(sw, r)
	})
}

// ---- exposition builder ----

type family struct {
	name, help, typ string
	lines           []string
}

type expo struct {
	fams  map[string]*family
	order []string
}

func newExpo() *expo { return &expo{fams: map[string]*family{}} }

func (e *expo) fam(name, help, typ string) *family {
	if f, ok := e.fams[name]; ok {
		return f
	}
	f := &family{name: name, help: help, typ: typ}
	e.fams[name] = f
	e.order = append(e.order, name)
	return f
}

func labelStr(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i] + `="` + escLabel(kv[i+1]) + `"`)
	}
	b.WriteByte('}')
	return b.String()
}

var labelEsc = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escLabel(s string) string { return labelEsc.Replace(s) }

func fmtVal(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func (e *expo) add(name, help, typ string, v float64, kv ...string) {
	f := e.fam(name, help, typ)
	f.lines = append(f.lines, name+labelStr(kv)+" "+fmtVal(v))
}

func (e *expo) raw(name, help, typ, series string, v float64) {
	f := e.fam(name, help, typ)
	f.lines = append(f.lines, series+" "+fmtVal(v))
}

func (e *expo) render() []byte {
	names := append([]string(nil), e.order...)
	sort.Strings(names)
	var b bytes.Buffer
	for _, n := range names {
		f := e.fams[n]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.typ)
		sort.Strings(f.lines) // deterministic; histogram buckets are emitted in order below
		for _, l := range f.lines {
			b.WriteString(l + "\n")
		}
	}
	return b.Bytes()
}

// ---- collection ----

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	e := newExpo()
	s.collectStatic(e)
	s.collectHTTP(e)
	for _, f := range s.storeFamilies(r.Context()) {
		cp := *f
		e.fams[f.name] = &cp
		e.order = append(e.order, f.name)
	}
	e.add("agregador_scrape_duration_seconds", "Time spent building this exposition.", "gauge", time.Since(t0).Seconds())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(e.render())
}

func (s *server) collectStatic(e *expo) {
	goVer := runtime.Version()
	e.add("agregador_build_info", "Build information.", "gauge", 1, "version", s.o.Version, "goversion", goVer)
	e.add("process_start_time_seconds", "Start time of the process since the Unix epoch.", "gauge", float64(s.ops.start.Unix()))
	e.add("go_goroutines", "Number of goroutines.", "gauge", float64(runtime.NumGoroutine()))
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	e.add("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", "gauge", float64(m.HeapAlloc))
	e.add("go_memstats_heap_inuse_bytes", "Bytes in in-use heap spans.", "gauge", float64(m.HeapInuse))
	e.add("go_memstats_sys_bytes", "Bytes obtained from the OS.", "gauge", float64(m.Sys))
	e.add("go_memstats_next_gc_bytes", "Heap size target of the next GC.", "gauge", float64(m.NextGC))
	e.add("go_gc_cycles_total", "Completed GC cycles.", "counter", float64(m.NumGC))
	e.add("go_gc_pause_seconds_total", "Cumulative GC stop-the-world pause.", "counter", float64(m.PauseTotalNs)/1e9)
	if s.d.Heartbeat != nil {
		if t := s.d.Heartbeat.LastBeat(); !t.IsZero() {
			e.add("agregador_scheduler_last_beat_timestamp_seconds", "Last scheduler heartbeat.", "gauge", float64(t.Unix()))
		}
	}
	e.add("agregador_http_requests_in_flight", "Requests being served right now.", "gauge", float64(s.ops.inflight.Load()))
}

func (s *server) collectHTTP(e *expo) {
	o := s.ops
	o.mu.Lock()
	defer o.mu.Unlock()
	for k, n := range o.reqs {
		e.add("agregador_http_requests_total", "HTTP requests by route pattern and status class.", "counter", float64(n), "route", k.route, "class", k.class)
	}
	const hn = "agregador_http_request_duration_seconds"
	for route, h := range o.hists {
		var cum uint64
		for i, b := range httpBuckets {
			cum += h.counts[i]
			e.raw(hn, "HTTP request latency by route pattern.", "histogram",
				hn+"_bucket"+labelStr([]string{"route", route, "le", fmtVal(b)}), float64(cum))
		}
		e.raw(hn, "", "histogram", hn+"_bucket"+labelStr([]string{"route", route, "le", "+Inf"}), float64(h.n))
		e.raw(hn, "", "histogram", hn+"_sum"+labelStr([]string{"route", route}), h.sum)
		e.raw(hn, "", "histogram", hn+"_count"+labelStr([]string{"route", route}), float64(h.n))
	}
}

// storeFamilies returns the DB-derived families, cached for storeCacheTTL.
func (s *server) storeFamilies(ctx context.Context) []*family {
	o := s.ops
	o.cmu.Lock()
	defer o.cmu.Unlock()
	if o.cache != nil && time.Since(o.cacheAt) < storeCacheTTL {
		return o.cache
	}
	e := newExpo()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	up := true

	if st, err := s.d.Store.Stats(cctx, s.o.EmbedModel); err != nil {
		up = false
	} else {
		e.add("agregador_articles_total", "Stored articles.", "gauge", float64(st.Articles))
		srcs := make([]string, 0, len(st.ArticlesBySource))
		for id := range st.ArticlesBySource {
			srcs = append(srcs, id)
		}
		sort.Slice(srcs, func(i, j int) bool {
			a, b := st.ArticlesBySource[srcs[i]], st.ArticlesBySource[srcs[j]]
			if a != b {
				return a > b
			}
			return srcs[i] < srcs[j]
		})
		var other int64
		for i, id := range srcs {
			if i >= maxSourceLabels {
				other += st.ArticlesBySource[id]
				continue
			}
			e.add("agregador_articles_by_source", "Stored articles per source.", "gauge", float64(st.ArticlesBySource[id]), "source", id)
		}
		if other > 0 {
			e.add("agregador_articles_by_source", "Stored articles per source.", "gauge", float64(other), "source", "_other")
		}
		e.add("agregador_articles_embedded", "Articles embedded for the current model.", "gauge", float64(st.Embedded))
		e.add("agregador_articles_embed_pending", "Articles waiting for embeddings.", "gauge", float64(st.PendingEmbed))
		e.add("agregador_screenshots_total", "Stored screenshots.", "gauge", float64(st.Screenshots))
		e.add("agregador_db_bytes", "SQLite database size in bytes.", "gauge", float64(st.DBBytes))
		e.add("agregador_shots_bytes", "Bytes used by screenshots.", "gauge", float64(st.ShotsBytes))
		if !st.OldestArticle.IsZero() {
			e.add("agregador_oldest_article_timestamp_seconds", "Date of the oldest article.", "gauge", float64(st.OldestArticle.Unix()))
		}
		if !st.NewestArticle.IsZero() {
			e.add("agregador_newest_article_timestamp_seconds", "Date of the newest article.", "gauge", float64(st.NewestArticle.Unix()))
		}
	}

	if runs, err := s.d.Store.ListJobRuns(cctx, 300); err != nil {
		up = false
	} else {
		seenRun, seenOK := map[string]bool{}, map[string]bool{}
		for _, j := range runs { // newest first
			if !seenRun[j.Job] && len(seenRun) >= maxJobLabels {
				continue
			}
			if !seenRun[j.Job] {
				seenRun[j.Job] = true
				l := []string{"job", j.Job}
				e.add("agregador_job_last_run_timestamp_seconds", "Start of the last run of the job.", "gauge", float64(j.StartedAt.Unix()), l...)
				running := j.FinishedAt.IsZero() && time.Since(j.StartedAt) < jobTimeout
				e.add("agregador_job_running", "1 while the job is running.", "gauge", b2f(running), l...)
				if !j.FinishedAt.IsZero() {
					e.add("agregador_job_last_duration_seconds", "Duration of the last finished run.", "gauge", j.FinishedAt.Sub(j.StartedAt).Seconds(), l...)
					e.add("agregador_job_last_ok", "1 if the last finished run succeeded.", "gauge", b2f(j.OK), l...)
				}
			}
			if j.OK && !j.FinishedAt.IsZero() && !seenOK[j.Job] {
				seenOK[j.Job] = true
				e.add("agregador_job_last_success_timestamp_seconds", "Start of the last successful run.", "gauge", float64(j.StartedAt.Unix()), "job", j.Job)
			}
		}
	}

	if feeds, err := s.d.Store.ListFeedStates(cctx); err != nil {
		up = false
	} else {
		failing := 0
		bySrc := map[string]int{}
		for _, f := range feeds {
			if f.LastError != "" && !f.LastErrorAt.Before(f.LastOK) {
				failing++
				bySrc[f.SourceID]++
			}
		}
		e.add("agregador_feeds_total", "Feeds with recorded state.", "gauge", float64(len(feeds)))
		e.add("agregador_feeds_failing", "Feeds whose last fetch failed.", "gauge", float64(failing))
		ids := make([]string, 0, len(bySrc))
		for id := range bySrc {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for i, id := range ids {
			if i >= maxSourceLabels {
				break
			}
			e.add("agregador_feed_errors_by_source", "Failing feeds per source.", "gauge", float64(bySrc[id]), "source", id)
		}
	}

	if s.d.LLM != nil {
		lctx, lc := context.WithTimeout(cctx, 4*time.Second)
		chat, emb := s.d.LLM.LLMHealth(lctx)
		lc()
		e.add("agregador_llm_up", "1 if the model server answers.", "gauge", b2f(chat.OK), "role", "chat")
		e.add("agregador_llm_up", "1 if the model server answers.", "gauge", b2f(emb.OK), "role", "embed")
	}
	e.add("agregador_store_up", "1 if the store answered the scrape queries.", "gauge", b2f(up))

	fams := make([]*family, 0, len(e.fams))
	for _, f := range e.fams {
		fams = append(fams, f)
	}
	o.cache, o.cacheAt = fams, time.Now()
	return fams
}

func init() {
	registerRoutes(func(s *server, m *http.ServeMux) { m.HandleFunc("GET /metrics", s.handleMetrics) })
}
