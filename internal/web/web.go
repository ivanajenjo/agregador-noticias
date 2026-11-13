// Package web implements the HTML UI (html/template + htmx) and the JSON API.
package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps are the collaborators of the web layer. Store is required; the others
// may be nil, in which case the related features answer 503 / are hidden.
type Deps struct {
	Store       domain.Store
	Retriever   domain.Retriever
	Ingestor    domain.Ingestor
	Shots       domain.ShotTaker
	LLM         domain.StatusProvider  // may be nil
	Trends      domain.TrendProvider   // may be nil: Tendencias hidden, routes answer 503
	ShotsDir    string                 // served read-only at /shots/
	Maintenance domain.Maintainer      // may be nil: POST /api/jobs/maintenance and GET /api/maintenance answer 503
	Heartbeat   domain.Heartbeat       // may be nil: /readyz skips the scheduler check
	Follow      domain.FollowStore     // may be nil: Seguimientos hidden, routes answer 503
	Alerts      domain.AlertService    // may be nil: "Probar notificación" disabled
	Digests     domain.DigestStore     // may be nil: Resumen hidden, routes answer 503
	DigestGen   domain.DigestGenerator // may be nil: "Generar ahora" answers 503
	Prominence  domain.ProminenceStore // may be nil: Portadas hidden, routes answer 503
	Log         *slog.Logger

	// Annotations: tone/intent/framing estimates (internal/annotate). nil => section hidden, routes 503.
	Annotations        domain.AnnotationReader
	AnnotationModel    string // configured model whose rows are shown by default
	AnnotationsEnabled bool   // worker enabled; false + no stored rows => hidden
}

// Options configure the handler.
type Options struct {
	BasicAuthUser string // auth is enabled when user or pass is non-empty; /healthz is exempt
	BasicAuthPass string
	Location      *time.Location // default Europe/Madrid (falls back to UTC if tzdata is missing)
	Version       string
	EmbedModel    string // passed to Store.Stats for embedding counters (optional)
	PublicURL     string // optional absolute base URL for feed links (else derived from the request)
}

type server struct {
	d      Deps
	o      Options
	log    *slog.Logger
	loc    *time.Location
	tpl    map[string]*template.Template
	static map[string]staticFile
	jobs   *jobTracker
	ops    *opsState // metrics registry + export limiter (metrics.go)
	now    func() time.Time
}

// New builds the full HTTP handler.
func New(deps Deps, opts Options) http.Handler {
	s := &server{d: deps, o: opts, log: deps.Log, loc: opts.Location, now: time.Now, jobs: newJobTracker(), ops: newOpsState()}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	if s.loc == nil {
		l, err := time.LoadLocation("Europe/Madrid")
		if err != nil {
			l = time.UTC
		}
		s.loc = l
	}
	if s.o.Version == "" {
		s.o.Version = "dev"
	}
	s.tpl = s.parseTemplates()
	s.static = loadStatic()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.pageHome)
	mux.HandleFunc("GET /fragmentos/portada", s.fragHome)
	mux.HandleFunc("GET /buscar", s.pageSearch)
	mux.HandleFunc("GET /preguntar", s.pageAsk)
	mux.HandleFunc("GET /capturas", s.pageShots)
	mux.HandleFunc("GET /tendencias", s.pageTrends)
	mux.HandleFunc("GET /tendencias/termino/{key}", s.pageTerm)
	mux.HandleFunc("GET /estado", s.pageStatus)
	mux.HandleFunc("GET /fragmentos/estado", s.fragStatus)
	mux.HandleFunc("GET /articulo/{id}", s.pageArticle)
	mux.HandleFunc("GET /offline", s.pageOffline)

	mux.HandleFunc("GET /api/articles", s.apiArticles)
	mux.HandleFunc("GET /api/search", s.apiSearch)
	mux.HandleFunc("GET /api/screenshots", s.apiScreenshots)
	mux.HandleFunc("GET /api/trends", s.apiTrends)
	mux.HandleFunc("GET /api/trends/term", s.apiTrendTerm)
	mux.HandleFunc("GET /api/stories", s.apiStories)
	mux.HandleFunc("POST /api/ask", s.apiAsk)
	mux.HandleFunc("POST /api/jobs/ingest", s.apiJob("ingest"))
	mux.HandleFunc("POST /api/jobs/screenshots", s.apiJob("screenshots"))
	mux.HandleFunc("GET /api/status", s.apiStatus)
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /static/{file...}", s.serveStatic)
	mux.HandleFunc("GET /sw.js", s.serveRootStatic("sw.js"))
	mux.HandleFunc("GET /manifest.webmanifest", s.serveRootStatic("manifest.webmanifest"))
	mux.HandleFunc("GET /favicon.svg", s.serveRootStatic("icon.svg"))
	mux.HandleFunc("GET /shots/{path...}", s.serveShot)
	navMu.Lock()
	hooks := append([]func(*server, *http.ServeMux){}, routeHooks...)
	navMu.Unlock()
	for _, h := range hooks {
		h(s, mux)
	}
	mux.HandleFunc("/", s.notFound)

	var h http.Handler = mux
	h = s.timeoutMW(h)
	h = s.csrfMW(h)
	h = s.authMW(h)
	h = s.securityMW(h)
	h = s.logMW(h)
	h = s.metricsMW(mux, h)
	h = s.recoverMW(h)
	return h
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// ---- middleware ----

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(c int) {
	if w.status == 0 {
		w.status = c
	}
	w.ResponseWriter.WriteHeader(c)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic in handler", "path", r.URL.Path, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				s.fail(w, r, http.StatusInternalServerError, "Error interno")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes, "dur", time.Since(start).Round(time.Millisecond))
	})
}

const csp = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"connect-src 'self'; manifest-src 'self'; worker-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func (s *server) securityMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *server) authMW(next http.Handler) http.Handler {
	if s.o.BasicAuthUser == "" && s.o.BasicAuthPass == "" {
		return next
	}
	wantU := sha256.Sum256([]byte(s.o.BasicAuthUser))
	wantP := sha256.Sum256([]byte(s.o.BasicAuthPass))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		gu := sha256.Sum256([]byte(u))
		gp := sha256.Sum256([]byte(p))
		okU := subtle.ConstantTimeCompare(gu[:], wantU[:])
		okP := subtle.ConstantTimeCompare(gp[:], wantP[:])
		if !ok || okU&okP != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Agregador", charset="UTF-8"`)
			http.Error(w, "No autorizado", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfMW: unsafe methods need a custom header (cannot be set cross-site without
// CORS) or a same-origin Origin header.
func (s *server) csrfMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sameOrigin(r) && r.Header.Get("X-Requested-With") == "" && r.Header.Get("HX-Request") == "" {
				s.fail(w, r, http.StatusForbidden, "Falta cabecera X-Requested-With u Origin válido")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(r) {
				s.fail(w, r, http.StatusForbidden, "Origen no permitido")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	i := strings.Index(o, "://")
	return i > 0 && o[i+3:] == r.Host
}

// timeoutMW bounds the context of every non-streaming request.
func (s *server) timeoutMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ask" && !strings.HasPrefix(r.URL.Path, "/api/export/") {
			ctx, cancel := ctxTimeout(r, 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// fail answers an error as JSON for /api paths and as an HTML page otherwise.
func (s *server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`<p class="notice err" role="alert">` + template.HTMLEscapeString(msg) + `</p>`))
		return
	}
	s.render(w, r, code, "error", "Error", "", errorView{Code: code, Msg: msg})
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, http.StatusNotFound, "No encontrado")
}

func staticSub() fs.FS {
	sub, _ := fs.Sub(staticFS, "static")
	return sub
}
