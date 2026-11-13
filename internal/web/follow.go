package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	followWindowDays = 7
	followHitLimit   = 100
	followLatest     = 5
	maxWatches       = 50
	maxWatchName     = 80
	maxWatchQuery    = 200
	followTimeout    = 10 * time.Second
)

func init() {
	registerPage("follow")
	registerNav(NavItem{Key: "follow", Label: "Seguimientos", Href: "/seguimientos", Order: 70,
		Desc: "Temas que sigues y alertas de las historias más cubiertas",
		Show: func(s *server) bool { return s.d.Follow != nil },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 17h12l-1.5-2V10a4.5 4.5 0 0 0-9 0v5zM10 20h4"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /seguimientos", s.pageFollow)
		m.HandleFunc("POST /seguimientos", s.followCreate)
		m.HandleFunc("POST /seguimientos/probar", s.followTest)
		m.HandleFunc("POST /seguimientos/{id}/borrar", s.followDelete)
	})
}

type outletCount struct {
	Name  string
	Count int
}
type dayBar struct {
	Label string
	N     int
}
type watchHit struct {
	ID     int64
	Title  string
	Source string
	When   time.Time
}
type watchView struct {
	domain.Watch
	Total      int
	Capped     bool
	Outlets    []outletCount
	Latest     []watchHit
	Chart      template.HTML
	Filters    string
	CompareURL string
	SearchURL  string
	Err        string
}
type alertView struct {
	domain.AlertRecord
	Status    string
	Class     string
	KindLabel string
}
type followForm struct {
	Name, Query string
	Sources     []string
	Leanings    []string
	Notify      bool
}
type followView struct {
	Watches  []watchView
	Alerts   []alertView
	Sources  []srcOpt
	Leanings []leanOpt
	Channels []string
	CanTest  bool
	Notice   string
	Err      string
	Form     followForm
}

func (s *server) followUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.Follow == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Seguimientos no disponible en este servidor.")
		return true
	}
	return false
}

func (s *server) buildFollow(ctx context.Context, f followForm, notice, errMsg string) followView {
	ctx, cancel := context.WithTimeout(ctx, followTimeout)
	defer cancel()
	v := followView{Notice: notice, Err: errMsg, Form: f, CanTest: s.d.Alerts != nil}
	if s.d.Alerts != nil {
		v.Channels = s.d.Alerts.Channels()
	}
	fv := s.filterView(ctx, filterForm{Sources: f.Sources, Leanings: f.Leanings})
	v.Sources, v.Leanings = fv.Sources, fv.Leanings
	sm := s.sourceMap(ctx)
	watches, err := s.d.Follow.ListWatches(ctx)
	if err != nil {
		s.log.Error("follow: list", "err", err)
		v.Err = firstNonEmpty(v.Err, "No se pudieron leer los seguimientos.")
	}
	for _, w := range watches {
		v.Watches = append(v.Watches, s.watchView(ctx, w, sm))
	}
	al, err := s.d.Follow.ListAlerts(ctx, 50)
	if err != nil {
		s.log.Error("follow: alerts", "err", err)
	}
	for _, a := range al {
		av := alertView{AlertRecord: a, KindLabel: alertKindLabel(a.Kind)}
		switch {
		case a.Channel == domain.ChannelQuiet && !a.OK:
			av.Status, av.Class = "en espera (horas de silencio)", "warn"
		case a.Channel == domain.ChannelQuiet:
			av.Status, av.Class = "enviada en resumen", "ok"
		case a.Channel == domain.ChannelLog:
			av.Status, av.Class = "sin canal configurado", "muted"
		case a.OK:
			av.Status, av.Class = "enviada", "ok"
		default:
			av.Status, av.Class = "fallida", "err"
		}
		v.Alerts = append(v.Alerts, av)
	}
	return v
}

func alertKindLabel(k string) string {
	switch k {
	case domain.AlertStory:
		return "Historia"
	case domain.AlertWatch:
		return "Seguimiento"
	case domain.AlertSummary:
		return "Resumen"
	case domain.AlertTest:
		return "Prueba"
	}
	return k
}

func (s *server) watchView(ctx context.Context, w domain.Watch, sm map[string]domain.Source) watchView {
	wv := watchView{Watch: w, CompareURL: askURL(w.Query)}
	var parts []string
	for _, id := range w.SourceIDs {
		parts = append(parts, firstNonEmpty(sm[id].Name, id))
	}
	parts = append(parts, w.Leanings...)
	wv.Filters = strings.Join(parts, ", ")
	q := url.Values{"q": {w.Query}, "source": w.SourceIDs, "leaning": w.Leanings}
	wv.SearchURL = "/buscar?" + q.Encode()
	now := s.now()
	from := now.AddDate(0, 0, -followWindowDays)
	hits, err := s.d.Store.SearchText(ctx, w.Query, domain.Filter{SourceIDs: w.SourceIDs, Leanings: w.Leanings, From: from}, followHitLimit)
	if err != nil {
		s.log.Warn("follow: search", "watch", w.ID, "err", err)
		wv.Err = "No se pudo buscar ahora."
		return wv
	}
	wv.Total, wv.Capped = len(hits), len(hits) >= followHitLimit
	byOutlet := map[string]int{}
	var order []string
	days := make([]dayBar, followWindowDays)
	today := now.In(s.loc)
	for i := range days {
		d := today.AddDate(0, 0, i-followWindowDays+1)
		days[i].Label = d.Format("02/01")
	}
	for i, h := range hits {
		name := firstNonEmpty(sm[h.Article.SourceID].Name, h.Article.SourceID)
		if byOutlet[name] == 0 {
			order = append(order, name)
		}
		byOutlet[name]++
		at := h.Article.PublishedAt
		if at.IsZero() {
			at = h.Article.FirstSeenAt
		}
		if idx := followWindowDays - 1 - daysBetween(at.In(s.loc), today); idx >= 0 && idx < followWindowDays {
			days[idx].N++
		}
		if i < followLatest {
			wv.Latest = append(wv.Latest, watchHit{ID: h.Article.ID, Title: h.Article.Title, Source: name, When: at})
		}
	}
	for _, n := range order {
		wv.Outlets = append(wv.Outlets, outletCount{n, byOutlet[n]})
	}
	sortOutlets(wv.Outlets)
	wv.Chart = dayBarsSVG(days)
	return wv
}

// daysBetween counts calendar days from a to b (b later => positive).
func daysBetween(a, b time.Time) int {
	ad := time.Date(a.Year(), a.Month(), a.Day(), 12, 0, 0, 0, time.UTC)
	bd := time.Date(b.Year(), b.Month(), b.Day(), 12, 0, 0, 0, time.UTC)
	return int(bd.Sub(ad).Hours() / 24)
}

func sortOutlets(o []outletCount) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && o[j].Count > o[j-1].Count; j-- {
			o[j], o[j-1] = o[j-1], o[j]
		}
	}
}

// dayBarsSVG draws a tiny bar chart (one bar per day) with a text alternative.
func dayBarsSVG(days []dayBar) template.HTML {
	mx := 1
	var alt []string
	for _, d := range days {
		if d.N > mx {
			mx = d.N
		}
		alt = append(alt, fmt.Sprintf("%s: %d", d.Label, d.N))
	}
	const bw, gap, h = 12.0, 4.0, 28.0
	w := float64(len(days))*(bw+gap) - gap
	var sb strings.Builder
	desc := template.HTMLEscapeString(strings.Join(alt, ", "))
	fmt.Fprintf(&sb, `<svg class="daybars" viewBox="0 0 %.0f %.0f" role="img" aria-label="Menciones por día: %s"><title>%s</title>`, w, h, desc, desc)
	for i, d := range days {
		bh := 1.5
		if d.N > 0 {
			bh = 3 + float64(d.N)/float64(mx)*(h-3)
		}
		cls := "bar"
		if d.N == 0 {
			cls = "bar zero"
		}
		fmt.Fprintf(&sb, `<rect class="%s" x="%.1f" y="%.1f" width="%.0f" height="%.1f" rx="2"/>`, cls, float64(i)*(bw+gap), h-bh, bw, bh)
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203 -- numbers and escaped labels only
}

func (s *server) pageFollow(w http.ResponseWriter, r *http.Request) {
	if s.followUnavailable(w, r) {
		return
	}
	notice := ""
	switch r.URL.Query().Get("prueba") {
	case "ok":
		notice = "Prueba enviada. Mira el resultado en «Alertas enviadas»."
	case "mal":
		notice = "La prueba falló en algún canal. Revisa «Alertas enviadas»."
	case "log":
		notice = "No hay ningún canal configurado: la prueba solo se ha registrado."
	}
	s.render(w, r, http.StatusOK, "follow", "Seguimientos", "follow", s.buildFollow(r.Context(), followForm{Notify: true}, notice, ""))
}

// respond answers a POST: htmx gets the refreshed section, plain forms a redirect.
func (s *server) followRespond(w http.ResponseWriter, r *http.Request, code int, v followView, redirect string) {
	if r.Header.Get("HX-Request") != "" {
		s.fragment(w, "follow", "seg", v) // status 200: the message is part of the fragment
		return
	}
	if code == http.StatusSeeOther {
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	s.render(w, r, code, "follow", "Seguimientos", "follow", v)
}

func (s *server) followCreate(w http.ResponseWriter, r *http.Request) {
	if s.followUnavailable(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Formulario no válido")
		return
	}
	f := followForm{
		Name:     truncRunes(cleanText(r.PostForm.Get("name")), maxWatchName),
		Query:    truncRunes(cleanText(r.PostForm.Get("q")), maxWatchQuery),
		Sources:  cleanList(r.PostForm["source"]),
		Leanings: cleanList(r.PostForm["leaning"]),
		Notify:   r.PostForm.Get("notify") != "",
	}
	if f.Name == "" {
		f.Name = f.Query
	}
	errMsg := ""
	switch {
	case f.Query == "" || !hasSearchable(f.Query):
		errMsg = "Escribe qué quieres seguir (palabras o frases)."
	default:
		if list, err := s.d.Follow.ListWatches(r.Context()); err == nil && len(list) >= maxWatches {
			errMsg = fmt.Sprintf("Máximo %d seguimientos: borra alguno antes.", maxWatches)
		}
	}
	if errMsg != "" {
		s.followRespond(w, r, http.StatusBadRequest, s.buildFollow(r.Context(), f, "", errMsg), "")
		return
	}
	wt := &domain.Watch{Name: f.Name, Query: f.Query, SourceIDs: f.Sources, Leanings: f.Leanings, Notify: f.Notify}
	if err := s.d.Follow.CreateWatch(r.Context(), wt); err != nil {
		s.log.Error("follow: create", "err", err)
		s.followRespond(w, r, http.StatusInternalServerError, s.buildFollow(r.Context(), f, "", "No se pudo guardar el seguimiento."), "")
		return
	}
	s.followRespond(w, r, http.StatusSeeOther, s.buildFollow(r.Context(), followForm{Notify: true}, "Seguimiento creado.", ""), "/seguimientos")
}

func (s *server) followDelete(w http.ResponseWriter, r *http.Request) {
	if s.followUnavailable(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return
	}
	if err := s.d.Follow.DeleteWatch(r.Context(), id); err != nil {
		s.log.Error("follow: delete", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo borrar.")
		return
	}
	s.followRespond(w, r, http.StatusSeeOther, s.buildFollow(r.Context(), followForm{Notify: true}, "Seguimiento borrado.", ""), "/seguimientos")
}

func (s *server) followTest(w http.ResponseWriter, r *http.Request) {
	if s.followUnavailable(w, r) {
		return
	}
	if s.d.Alerts == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Las alertas no están activadas en este servidor.")
		return
	}
	recs, err := s.d.Alerts.SendTest(r.Context())
	res := "ok"
	switch {
	case err != nil:
		res = "mal"
	case len(recs) > 0 && recs[0].Channel == domain.ChannelLog:
		res = "log"
	default:
		for _, a := range recs {
			if !a.OK {
				res = "mal"
			}
		}
	}
	if r.Header.Get("HX-Request") != "" {
		notice := map[string]string{"ok": "Prueba enviada.", "mal": "La prueba falló en algún canal.", "log": "No hay ningún canal configurado: la prueba solo se ha registrado."}[res]
		s.followRespond(w, r, http.StatusOK, s.buildFollow(r.Context(), followForm{Notify: true}, notice, ""), "")
		return
	}
	http.Redirect(w, r, "/seguimientos?prueba="+res, http.StatusSeeOther)
}

// cleanText collapses whitespace and drops control characters.
func cleanText(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// hasSearchable reports whether the query contains at least one letter or digit
// (the search sanitizer drops everything else, so the watch would never match).
func hasSearchable(q string) bool {
	for _, r := range q {
		if (r >= '0' && r <= '9') || strings.ToLower(string(r)) != strings.ToUpper(string(r)) {
			return true
		}
	}
	return false
}
