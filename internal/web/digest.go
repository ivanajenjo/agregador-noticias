package web

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	digestTimeout   = 10 * time.Second
	digestArchive   = 60
	digestFeedItems = 14
	digestMaxAgeDay = 400 // on-demand generation: how far back
)

func init() {
	registerPage("digest")
	registerNav(NavItem{Key: "digest", Label: "Resumen", Href: "/resumen", Order: 35,
		Desc: "Lo más importante del día: historias, términos en alza y portadas",
		Show: func(s *server) bool { return s.d.Digests != nil },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3h9l4 4v14H6z"/><path d="M14 3v5h5M9 12h7M9 15.5h7M9 19h4"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /resumen", s.pageDigest)
		m.HandleFunc("GET /resumen/{day}", s.pageDigest)
		m.HandleFunc("GET /resumen.atom", s.digestAtom)
		m.HandleFunc("GET /api/digest", s.apiDigest)
		m.HandleFunc("POST /api/jobs/digest", s.apiDigestJob)
	})
}

// ---- helpers ----

func (s *server) parseDigestDay(v string) (string, bool) {
	t, err := time.ParseInLocation(dateLayout, v, s.loc)
	if err != nil || t.Format(dateLayout) != v {
		return "", false
	}
	return v, true
}

func (s *server) today() string { return s.now().In(s.loc).Format(dateLayout) }

func (s *server) digestTitle(day string) string {
	t, err := time.ParseInLocation(dateLayout, day, s.loc)
	if err != nil {
		return "Resumen"
	}
	out := fmt.Sprintf("Resumen del %s %d de %s", diasES[t.Weekday()], t.Day(), mesesES[t.Month()])
	if t.Year() != s.now().In(s.loc).Year() {
		out += fmt.Sprintf(" de %d", t.Year())
	}
	return out
}

// digestHomeLink returns the label/URL of the home-page teaser, or "" when
// there is no digest (or the feature is off).
func (s *server) digestHomeLink(ctx context.Context) (label, href string) {
	if s.d.Digests == nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	d, err := s.d.Digests.LatestDigest(ctx)
	if err != nil || d == nil {
		return "", ""
	}
	if d.Day == s.today() {
		return "Resumen de hoy", "/resumen"
	}
	return "Último resumen (" + s.shortDay(d.Day) + ")", "/resumen/" + d.Day
}

func (s *server) shortDay(day string) string {
	t, err := time.ParseInLocation(dateLayout, day, s.loc)
	if err != nil {
		return day
	}
	return fmt.Sprintf("%d %s", t.Day(), mesesES[t.Month()][:3])
}

var citeLinkRe = regexp.MustCompile(`\[(\d{1,3})\]`)

// citeHTML escapes untrusted narrative text and turns [n] into links to the
// numbered headline list.
func citeHTML(s string) template.HTML {
	e := template.HTMLEscapeString(s)
	e = citeLinkRe.ReplaceAllString(e, `<sup class="cite"><a href="#f$1" aria-label="Fuente $1">$1</a></sup>`)
	return template.HTML(e) // #nosec G203 -- escaped text + generated anchors
}

// safeURL allows only http(s) links.
func safeURL(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return p.String()
}

// ---- page ----

type archiveItem struct {
	Day, Label, URL string
	Stories         int
	On              bool
}

type digestStoryView struct {
	domain.DigestStory
	Cite       template.HTML
	MissingTxt string
	CompareURL string
}

type digestShotView struct {
	domain.DigestShot
	URL, Time string
}

type risingView struct {
	domain.DigestTerm
	URL string
}

type digestView struct {
	Rising     []risingView
	Found      bool
	Day        string
	Title      string
	D          *domain.Digest
	Stories    []digestStoryView
	Changes    []template.HTML
	Shots      []digestShotView
	Prev, Next string
	Archive    []archiveItem
	CanGen     bool
	GenDay     string // day passed to the generate button ("" = today)
	GenLabel   string
	WindowTxt  string
	Err        string
}

func (s *server) digestView(ctx context.Context, day string, d *domain.Digest) digestView {
	v := digestView{Day: day, Title: s.digestTitle(day), CanGen: s.d.DigestGen != nil}
	if day != s.today() {
		v.GenDay = day
		v.GenLabel = "Generar este día"
	} else {
		v.GenLabel = "Generar ahora"
	}
	if d == nil {
		return v
	}
	v.Found, v.D = true, d
	for _, st := range d.Stories {
		sv := digestStoryView{DigestStory: st, MissingTxt: strings.Join(st.Missing, ", "), CompareURL: askURL(st.Title)}
		if st.Narrative != "" {
			sv.Cite = citeHTML(st.Narrative)
		}
		v.Stories = append(v.Stories, sv)
	}
	for _, t := range d.Rising {
		v.Rising = append(v.Rising, risingView{DigestTerm: t, URL: termURL(t.Key)})
	}
	for _, c := range d.WhatChanged {
		v.Changes = append(v.Changes, citeHTML(c))
	}
	for _, sh := range d.Shots {
		v.Shots = append(v.Shots, digestShotView{DigestShot: sh, URL: shotURL(sh.Path), Time: sh.TakenAt.In(s.loc).Format("15:04")})
	}
	if !d.WindowFrom.IsZero() {
		v.WindowTxt = fmt.Sprintf("%s → %s", d.WindowFrom.In(s.loc).Format("02/01 15:04"), d.WindowTo.In(s.loc).Format("02/01 15:04"))
	}
	return v
}

func (s *server) loadArchive(ctx context.Context, v *digestView) {
	list, err := s.d.Digests.ListDigests(ctx, digestArchive)
	if err != nil {
		s.log.Warn("digest: archive", "err", err)
		return
	}
	for i, x := range list {
		v.Archive = append(v.Archive, archiveItem{Day: x.Day, Label: s.digestTitle(x.Day), URL: "/resumen/" + x.Day, Stories: len(x.Stories), On: x.Day == v.Day})
		if x.Day == v.Day {
			if i+1 < len(list) {
				v.Prev = "/resumen/" + list[i+1].Day
			}
			if i > 0 {
				v.Next = "/resumen/" + list[i-1].Day
			}
		}
	}
}

func (s *server) pageDigest(w http.ResponseWriter, r *http.Request) {
	if s.d.Digests == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "El resumen diario no está activado en este servidor.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), digestTimeout)
	defer cancel()
	var d *domain.Digest
	var err error
	day := r.PathValue("day")
	if day == "" {
		d, err = s.d.Digests.LatestDigest(ctx)
		if errors.Is(err, domain.ErrDigestNotFound) {
			v := s.digestView(ctx, s.today(), nil)
			v.Title = "Resumen diario"
			s.loadArchive(ctx, &v)
			s.render(w, r, http.StatusOK, "digest", "Resumen", "digest", v)
			return
		}
		if d != nil {
			day = d.Day
		}
	} else {
		var ok bool
		if day, ok = s.parseDigestDay(day); !ok {
			s.notFound(w, r)
			return
		}
		d, err = s.d.Digests.GetDigest(ctx, day)
		if errors.Is(err, domain.ErrDigestNotFound) {
			v := s.digestView(ctx, day, nil)
			s.loadArchive(ctx, &v)
			s.render(w, r, http.StatusNotFound, "digest", v.Title, "digest", v)
			return
		}
	}
	if err != nil {
		s.log.Error("digest: load", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer el resumen.")
		return
	}
	v := s.digestView(ctx, day, d)
	s.loadArchive(ctx, &v)
	s.render(w, r, http.StatusOK, "digest", v.Title, "digest", v)
}

// ---- API ----

func (s *server) apiDigest(w http.ResponseWriter, r *http.Request) {
	if s.d.Digests == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Resumen no disponible")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), digestTimeout)
	defer cancel()
	var d *domain.Digest
	var err error
	if day := r.URL.Query().Get("day"); day != "" {
		day, ok := s.parseDigestDay(day)
		if !ok {
			s.fail(w, r, http.StatusBadRequest, "day debe tener el formato AAAA-MM-DD")
			return
		}
		d, err = s.d.Digests.GetDigest(ctx, day)
	} else {
		d, err = s.d.Digests.LatestDigest(ctx)
	}
	if errors.Is(err, domain.ErrDigestNotFound) {
		s.fail(w, r, http.StatusNotFound, "No hay resumen para ese día")
		return
	}
	if err != nil {
		s.log.Error("digest: api", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer el resumen")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// apiDigestJob regenerates a digest asynchronously: 202 started, 409 running,
// 503 when the generator is not configured, 400 for a bad day. Optional
// ?day=YYYY-MM-DD (default today).
func (s *server) apiDigestJob(w http.ResponseWriter, r *http.Request) {
	if s.d.DigestGen == nil {
		s.jobReply(w, r, http.StatusServiceUnavailable, "Tarea no disponible")
		return
	}
	day := s.now().In(s.loc)
	if v := r.URL.Query().Get("day"); v != "" {
		t, err := time.ParseInLocation(dateLayout, v, s.loc)
		if err != nil || t.Format(dateLayout) != v || t.After(day) || day.Sub(t) > digestMaxAgeDay*24*time.Hour {
			s.jobReply(w, r, http.StatusBadRequest, "Día no válido")
			return
		}
		day = t
	}
	if !s.jobs.start("digest", s.now()) {
		s.jobReply(w, r, http.StatusConflict, "Ya hay una ejecución en curso")
		return
	}
	gen := s.d.DigestGen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		res := jobResult{At: s.now()}
		defer func() {
			if v := recover(); v != nil {
				res.OK, res.Message = false, fmt.Sprint("panic: ", v)
				s.log.Error("job panic", "job", "digest", "panic", v)
			}
			s.jobs.finish("digest", res)
		}()
		d, err := gen.Build(ctx, day)
		if err != nil {
			res.Message = err.Error()
			s.log.Error("job failed", "job", "digest", "err", err)
			return
		}
		res.OK, res.Count = true, len(d.Stories)
		s.log.Info("job finished", "job", "digest", "day", d.Day)
	}()
	s.jobReply(w, r, http.StatusAccepted, "Generando el resumen… recarga la página en un momento")
}

// ---- Atom ----

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}
type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}
type atomEntry struct {
	Title     string   `xml:"title"`
	ID        string   `xml:"id"`
	Updated   string   `xml:"updated"`
	Published string   `xml:"published"`
	Link      atomLink `xml:"link"`
	Summary   atomText `xml:"summary"`
	Content   atomText `xml:"content"`
}
type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Lang     string      `xml:"xml:lang,attr"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Author   atomAuthor  `xml:"author"`
	Entries  []atomEntry `xml:"entry"`
}
type atomAuthor struct {
	Name string `xml:"name"`
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](:\d{1,5})?$`)

// baseURL is the absolute origin used in feed links: Options.PublicURL when
// set, else derived from X-Forwarded-* / Host of the request.
func (s *server) baseURL(r *http.Request) string {
	if p := strings.TrimRight(strings.TrimSpace(s.o.PublicURL), "/"); p != "" {
		if u, err := url.Parse(p); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return p
		}
	}
	first := func(v string) string { return strings.TrimSpace(strings.Split(v, ",")[0]) }
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.ToLower(first(r.Header.Get("X-Forwarded-Proto"))); p == "http" || p == "https" {
		scheme = p
	}
	host := r.Host
	if h := first(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	if !hostRe.MatchString(host) {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (s *server) digestAtom(w http.ResponseWriter, r *http.Request) {
	if s.d.Digests == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "El resumen diario no está activado en este servidor.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), digestTimeout)
	defer cancel()
	list, err := s.d.Digests.ListDigests(ctx, digestFeedItems)
	if err != nil {
		s.log.Error("digest: atom", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo generar el feed.")
		return
	}
	base := s.baseURL(r)
	updated := time.Unix(0, 0).UTC()
	f := atomFeed{Lang: "es", Title: "Agregador · Resumen diario", Subtitle: "Lo más importante del día en los medios españoles",
		ID: "tag:agregador,2026:resumen", Author: atomAuthor{Name: "Agregador de noticias"},
		Links: []atomLink{{Rel: "self", Type: "application/atom+xml", Href: base + "/resumen.atom"}, {Rel: "alternate", Type: "text/html", Href: base + "/resumen"}}}
	for _, d := range list {
		at := d.GeneratedAt.UTC()
		if at.After(updated) {
			updated = at
		}
		f.Entries = append(f.Entries, atomEntry{
			Title: s.digestTitle(d.Day), ID: "tag:agregador,2026:resumen/" + d.Day, Updated: at.Format(time.RFC3339),
			Published: at.Format(time.RFC3339), Link: atomLink{Rel: "alternate", Type: "text/html", Href: base + "/resumen/" + d.Day},
			Summary: atomText{Type: "text", Body: d.Lead}, Content: atomText{Type: "html", Body: s.digestHTML(d, base)},
		})
	}
	if len(list) == 0 {
		f.Entries = nil
	}
	f.Updated = updated.Format(time.RFC3339)
	var sb strings.Builder
	sb.WriteString(xml.Header)
	enc := xml.NewEncoder(&sb)
	enc.Indent("", " ")
	if err := enc.Encode(f); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo generar el feed.")
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(sb.String()))
}

// digestHTML renders the digest as a self-contained HTML fragment for feed
// readers. Every dynamic piece is escaped; links are http(s) only.
func (s *server) digestHTML(d domain.Digest, base string) string {
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "<p>%s</p>", e(d.Lead))
	cite := func(t string) string { return citeLinkRe.ReplaceAllString(e(t), `<sup>$1</sup>`) }
	for _, st := range d.Stories {
		fmt.Fprintf(&b, "<h3>%s</h3><p><small>%d medios · %d artículos", e(st.Title), st.Sources, st.Articles)
		if st.FirstBy != "" {
			fmt.Fprintf(&b, " · primero: %s", e(st.FirstBy))
		}
		b.WriteString("</small></p>")
		if st.Narrative != "" {
			fmt.Fprintf(&b, "<p>%s</p>", cite(st.Narrative))
		}
		b.WriteString("<ul>")
		for _, h := range st.Headlines {
			t := e(h.Title)
			if u := safeURL(h.URL); u != "" {
				t = `<a href="` + e(u) + `">` + t + `</a>`
			}
			fmt.Fprintf(&b, "<li>[%d] %s — %s</li>", h.N, t, e(h.Outlet))
		}
		b.WriteString("</ul>")
		if len(st.Missing) > 0 {
			fmt.Fprintf(&b, "<p><small>No lo cubren: %s</small></p>", e(strings.Join(st.Missing, ", ")))
		}
	}
	if len(d.WhatChanged) > 0 {
		b.WriteString("<h3>Lo que cambió hoy</h3><ul>")
		for _, c := range d.WhatChanged {
			fmt.Fprintf(&b, "<li>%s</li>", cite(c))
		}
		b.WriteString("</ul>")
	}
	if len(d.Rising) > 0 {
		var ts []string
		for _, t := range d.Rising {
			x := e(t.Term)
			if t.New {
				x += " (nuevo)"
			}
			ts = append(ts, x)
		}
		fmt.Fprintf(&b, "<h3>Términos en alza</h3><p>%s</p>", strings.Join(ts, " · "))
	}
	if d.Model != "" {
		fmt.Fprintf(&b, "<p><small>Redactado por %s a partir de los titulares; puede contener errores.</small></p>", e(d.Model))
	}
	fmt.Fprintf(&b, `<p><a href="%s">Abrir el resumen completo</a></p>`, e(base+"/resumen/"+d.Day))
	return b.String()
}
