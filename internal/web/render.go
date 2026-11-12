package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type pageData struct {
	Title      string
	Nav        string
	Version    string
	Items      []NavItem
	MoreActive bool
	V          any
}

type errorView struct {
	Code int
	Msg  string
}

var pages = []string{"home", "search", "ask", "shots", "status", "article", "error", "offline", "trends", "trend_term"}

func (s *server) parseTemplates() map[string]*template.Template {
	funcs := template.FuncMap{
		"ago":       s.ago,
		"clock":     func(t time.Time) string { return t.In(s.loc).Format("15:04") },
		"datetime":  func(t time.Time) string { return t.In(s.loc).Format(time.RFC3339) },
		"longdate":  s.longDate,
		"fulltime":  func(t time.Time) string { return t.In(s.loc).Format("02/01/2006 15:04") },
		"leanClass": func(l string) string { return fmt.Sprintf("l%d", leaningRank(l)) },
		"bytes":     humanBytes,
		"trunc":     trunc,
		"paras":     paras,
		"isZero":    func(t time.Time) bool { return t.IsZero() },
		"score":     func(f float64) string { return fmt.Sprintf("%.3f", f) },
		"num":       humanInt,
		"add":       func(a, b int) int { return a + b },
		"leanBar":   leanBarSVG,
		"leanCap":   leanCaption,
		"spark":     sparkSVG,
	}
	out := map[string]*template.Template{}
	for _, p := range pages {
		t := template.Must(template.New(p).Funcs(funcs).ParseFS(templateFS, "templates/base.html", "templates/"+p+".html"))
		out[p] = t
	}
	return out
}

// render executes the full page layout.
func (s *server) render(w http.ResponseWriter, r *http.Request, code int, page, title, nav string, v any) {
	t := s.tpl[page]
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", s.pageData(title, nav, v)); err != nil {
		s.log.Error("template", "page", page, "err", err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

func (s *server) pageData(title, nav string, v any) pageData {
	items := s.visibleNav()
	more := nav == "more"
	for _, i := range items {
		if i.Key == nav && !i.Primary {
			more = true
		}
	}
	return pageData{Title: title, Nav: nav, Version: s.o.Version, Items: items, MoreActive: more, V: v}
}

// fragment executes a named template defined in a page file.
func (s *server) fragment(w http.ResponseWriter, page, name string, v any) {
	var buf bytes.Buffer
	if err := s.tpl[page].ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Error("template", "frag", name, "err", err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request")
	_, _ = w.Write(buf.Bytes())
}

func (s *server) ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := s.now().Sub(t)
	switch {
	case d < time.Minute:
		return "ahora mismo"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	case d < 48*time.Hour:
		return "ayer"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("hace %d días", int(d.Hours()/24))
	}
	return t.In(s.loc).Format("02/01/2006")
}

var (
	diasES  = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	mesesES = []string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
)

func (s *server) longDate(t time.Time) string {
	t = t.In(s.loc)
	return fmt.Sprintf("%s %d de %s de %d", diasES[t.Weekday()], t.Day(), mesesES[t.Month()], t.Year())
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	f, exp := float64(n), 0
	for f >= u && exp < 4 {
		f /= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", f, "KMGT"[exp-1])
}

func trunc(n int, s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	return strings.TrimSpace(string(r)) + "…"
}

func paras(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// leaningRank orders leanings from left (0) to right (4); unknown = 5.
func leaningRank(l string) int {
	l = strings.ToLower(l)
	switch {
	case strings.Contains(l, "centro") && strings.Contains(l, "izq"):
		return 1
	case strings.Contains(l, "centro") && strings.Contains(l, "der"):
		return 3
	case strings.Contains(l, "izq") || strings.Contains(l, "progres"):
		return 0
	case strings.Contains(l, "der") || strings.Contains(l, "conserv"):
		return 4
	case strings.Contains(l, "centro") || strings.Contains(l, "liberal"):
		return 2
	}
	return 5
}

func sortSources(src []domain.Source) []domain.Source {
	out := make([]domain.Source, 0, len(src))
	for _, s := range src {
		if s.IsEnabled() {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := leaningRank(out[i].Leaning), leaningRank(out[j].Leaning)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

var markRe = regexp.MustCompile(`(?i)</?mark>`)
var tagRe = regexp.MustCompile(`<[^>]*>`)

const (
	sentOpen  = "\x01"
	sentClose = "\x02"
)

// safeSnippet turns an untrusted store snippet into HTML where the only tags
// are balanced <mark> elements; everything else is escaped.
func safeSnippet(s string) template.HTML {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	open := false
	s = markRe.ReplaceAllStringFunc(s, func(m string) string {
		if m[1] == '/' {
			if open {
				open = false
				return sentClose
			}
			return ""
		}
		if open {
			return ""
		}
		open = true
		return sentOpen
	})
	if open {
		s += sentClose
	}
	s = template.HTMLEscapeString(s)
	s = strings.ReplaceAll(s, sentOpen, "<mark>")
	s = strings.ReplaceAll(s, sentClose, "</mark>")
	return template.HTML(s) // #nosec G203 -- only <mark> tags we generated
}

// plainSnippet strips any markup (used by the JSON/SSE API).
func plainSnippet(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// humanInt formats with Spanish thousands separators (12.840).
func humanInt(n int64) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
