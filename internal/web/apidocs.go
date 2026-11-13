package web

import (
	_ "embed"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// openapi.yaml is the single source of truth (docs/openapi.yaml is a symlink to it).
//
//go:embed openapi.yaml
var openapiYAML []byte

type specDoc struct {
	Info struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Paths      map[string]map[string]specOp `yaml:"paths"`
	Components struct {
		Parameters map[string]specParam `yaml:"parameters"`
	} `yaml:"components"`
}

type specParam struct {
	Name        string `yaml:"name"`
	In          string `yaml:"in"`
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
	Ref         string `yaml:"$ref"`
}

type specOp struct {
	Summary     string      `yaml:"summary"`
	Description string      `yaml:"description"`
	Tags        []string    `yaml:"tags"`
	Parameters  []specParam `yaml:"parameters"`
	Responses   map[string]struct {
		Description string `yaml:"description"`
		Ref         string `yaml:"$ref"`
	} `yaml:"responses"`
}

type docOp struct {
	Method, Path, Summary, Description string
	Params                             []string
	Responses                          []string
}

var (
	docOnce sync.Once
	docHTML []byte
)

var methodOrder = map[string]int{"get": 0, "post": 1, "put": 2, "patch": 3, "delete": 4}

func buildDocs() []byte {
	var d specDoc
	if err := yaml.Unmarshal(openapiYAML, &d); err != nil {
		return []byte("<!doctype html><p>Especificación no válida</p>")
	}
	var ops []docOp
	for p, ms := range d.Paths {
		for m, o := range ms {
			op := docOp{Method: strings.ToUpper(m), Path: p, Summary: o.Summary, Description: strings.TrimSpace(o.Description)}
			for _, pa := range o.Parameters {
				if pa.Ref != "" {
					pa = d.Components.Parameters[pa.Ref[strings.LastIndex(pa.Ref, "/")+1:]]
				}
				s := pa.Name + " (" + pa.In
				if pa.Required {
					s += ", obligatorio"
				}
				s += ")"
				if pa.Description != "" {
					s += ": " + pa.Description
				}
				op.Params = append(op.Params, s)
			}
			codes := make([]string, 0, len(o.Responses))
			for c := range o.Responses {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			for _, c := range codes {
				r := o.Responses[c]
				desc := r.Description
				if desc == "" && r.Ref != "" {
					desc = r.Ref[strings.LastIndex(r.Ref, "/")+1:]
				}
				op.Responses = append(op.Responses, c+" "+desc)
			}
			ops = append(ops, op)
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return methodOrder[strings.ToLower(ops[i].Method)] < methodOrder[strings.ToLower(ops[j].Method)]
	})
	var b strings.Builder
	_ = docTpl.Execute(&b, map[string]any{"Title": d.Info.Title, "Version": d.Info.Version, "Intro": strings.TrimSpace(d.Info.Description), "Ops": ops})
	return []byte(b.String())
}

var docTpl = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{--bg:#fafaf7;--surface:#fff;--text:#1d1d1b;--muted:#6b6b66;--border:#dcdcd4;--accent:#9a2a2a}
@media(prefers-color-scheme:dark){:root{--bg:#161614;--surface:#1f1f1c;--text:#ecece6;--muted:#a0a09a;--border:#37372f;--accent:#e28b8b}}
body{margin:0;background:var(--bg);color:var(--text);font:16px/1.5 system-ui,sans-serif}
main{max-width:52rem;margin:0 auto;padding:1rem}
h1{font-family:Georgia,serif}
article{background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:.75rem 1rem;margin:.75rem 0}
code{font-family:ui-monospace,monospace}
.m{display:inline-block;min-width:3.5rem;font-weight:700;color:var(--accent)}
.muted{color:var(--muted)}pre{white-space:pre-wrap}ul{margin:.25rem 0;padding-left:1.2rem}
</style></head><body><main>
<h1>{{.Title}} <small class="muted">v{{.Version}}</small></h1>
<p class="muted">Especificación completa: <a href="/api/openapi.yaml">/api/openapi.yaml</a></p>
<pre>{{.Intro}}</pre>
{{range .Ops}}<article><h2><span class="m">{{.Method}}</span> <code>{{.Path}}</code></h2>
<p>{{.Summary}}</p>{{if .Description}}<pre>{{.Description}}</pre>{{end}}
{{if .Params}}<strong>Parámetros</strong><ul>{{range .Params}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Responses}}<strong>Respuestas</strong><ul>{{range .Responses}}<li>{{.}}</li>{{end}}</ul>{{end}}
</article>{{end}}
</main></body></html>
`))

func (s *server) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openapiYAML)
}

func (s *server) apiDocs(w http.ResponseWriter, r *http.Request) {
	docOnce.Do(func() { docHTML = buildDocs() })
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(docHTML)
}

func (s *server) apiMaintenancePreview(w http.ResponseWriter, r *http.Request) {
	if s.d.Maintenance == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Mantenimiento no disponible")
		return
	}
	p, err := s.d.Maintenance.Preview(r.Context())
	if err != nil {
		s.log.Error("maintenance preview", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular la previsión")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func init() {
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /api/openapi.yaml", s.apiOpenAPI)
		m.HandleFunc("GET /api/docs", s.apiDocs)
		m.HandleFunc("GET /api/maintenance", s.apiMaintenancePreview)
	})
}
