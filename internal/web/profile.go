package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/profile"
)

// "Ficha de medio": /medios, /medios/{id}, /medios/comparar and /api/outlets.
// The engine is built lazily from Deps.Store / Deps.Trends (no extra Deps).

const (
	profileTimeout = 25 * time.Second
	maxCompare     = 4
)

type profileHandler struct {
	s *server
	e *profile.Engine
}

func init() {
	registerPage("medios", "medio", "medios_cmp")
	registerNav(NavItem{Key: "outlets", Label: "Medios", Href: "/medios", Order: 55,
		Desc: "Ficha de cada medio: volumen, horarios, retardo y cobertura",
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		h := &profileHandler{s: s, e: profile.New(s.d.Store, s.d.Trends, profile.Options{Location: s.loc, Now: s.now})}
		m.HandleFunc("GET /medios", h.pageList)
		m.HandleFunc("GET /medios/comparar", h.pageCompare)
		m.HandleFunc("GET /medios/{id}", h.pageProfile)
		m.HandleFunc("GET /api/outlets", h.apiList)
		m.HandleFunc("GET /api/outlets/{id}", h.apiProfile)
	})
}

// ---- formatting helpers ----

func fnum(f float64, dec int) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', dec, 64), ".", ",", 1)
}

func lagText(min float64) string {
	if min < 1 {
		return "menos de 1 min"
	}
	return shortDur(time.Duration(min * float64(time.Minute)))
}

var weekdayShort = []string{"lun", "mar", "mié", "jue", "vie", "sáb", "dom"}

func (h *profileHandler) lagDays(r *http.Request) int {
	if r.URL.Query().Get("dias") == "7" {
		return 7
	}
	return 14
}

func (h *profileHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, profile.ErrNotFound) {
		h.s.notFound(w, r)
		return
	}
	h.s.log.Error("profile", "path", r.URL.Path, "err", err)
	h.s.fail(w, r, http.StatusInternalServerError, "No se pudo calcular la ficha en este momento.")
}

// ---- SVG ----

func barsSVG(days []profile.DayN, loc *time.Location) template.HTML {
	if len(days) == 0 {
		return ""
	}
	const W, H, left, top, bottom = 360.0, 150.0, 26.0, 14.0, 20.0
	mx := 1
	for _, d := range days {
		if d.N > mx {
			mx = d.N
		}
	}
	plotW, plotH := W-left-6, H-top-bottom
	slot := plotW / float64(len(days))
	bw := slot * 0.72
	var desc []string
	for _, d := range days {
		desc = append(desc, fmt.Sprintf("%s: %d", d.Day.In(loc).Format("2/1"), d.N))
	}
	ad := "Artículos por día, últimos " + strconv.Itoa(len(days)) + " días. " + strings.Join(desc, "; ")
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg class="dchart" viewBox="0 0 360 150" role="img" aria-label="%s"><title>%s</title>`, template.HTMLEscapeString(ad), template.HTMLEscapeString(ad))
	fmt.Fprintf(&sb, `<line class="grid" x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f"/><line class="axis" x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f"/>`, left, W-4, top, top, left, W-4, top+plotH, top+plotH)
	fmt.Fprintf(&sb, `<text class="tick" x="%.0f" y="%.1f" text-anchor="end">%d</text><text class="tick" x="%.0f" y="%.1f" text-anchor="end">0</text>`, left-4, top+3.5, mx, left-4, top+plotH+3.5)
	for i, d := range days {
		x := left + float64(i)*slot + (slot-bw)/2
		hh := float64(d.N) * plotH / float64(mx)
		fmt.Fprintf(&sb, `<rect class="pf-bar" x="%.1f" y="%.1f" width="%.1f" height="%.1f"><title>%s: %d</title></rect>`, x, top+plotH-hh, bw, hh, d.Day.In(loc).Format("2/1"), d.N)
		if i%7 == 0 || i == len(days)-1 {
			fmt.Fprintf(&sb, `<text class="tick" x="%.1f" y="%.0f" text-anchor="middle">%s</text>`, x+bw/2, H-5, d.Day.In(loc).Format("2/1"))
		}
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203 -- numbers and fixed text only
}

func peakHour(heat [7][24]int) (hour, n, total int) {
	var by [24]int
	for _, row := range heat {
		for h, c := range row {
			by[h] += c
			total += c
		}
	}
	for h, c := range by {
		if c > n {
			hour, n = h, c
		}
	}
	return
}

func heatSVG(heat [7][24]int) template.HTML {
	mx, total := 0, 0
	for _, row := range heat {
		for _, c := range row {
			total += c
			if c > mx {
				mx = c
			}
		}
	}
	if total == 0 {
		return ""
	}
	ph, pn, _ := peakHour(heat)
	const cell, left, top = 11.0, 26.0, 12.0
	ad := fmt.Sprintf("Mapa de calor de publicación por día de la semana y hora. %d artículos; hora con más publicaciones: %02d h (%d).", total, ph, pn)
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg class="pf-heat" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s"><title>%s</title>`, left+24*cell+2, top+7*cell+2, template.HTMLEscapeString(ad), template.HTMLEscapeString(ad))
	for hr := 0; hr < 24; hr += 3 {
		fmt.Fprintf(&sb, `<text class="tick" x="%.1f" y="8" text-anchor="middle">%02d</text>`, left+float64(hr)*cell+cell/2, hr)
	}
	for d := 0; d < 7; d++ {
		y := top + float64(d)*cell
		fmt.Fprintf(&sb, `<text class="tick" x="%.0f" y="%.1f" text-anchor="end">%s</text>`, left-3, y+8, weekdayShort[d])
		for hr := 0; hr < 24; hr++ {
			c := heat[d][hr]
			op := 0.06
			if c > 0 {
				op = 0.2 + 0.8*float64(c)/float64(mx)
			}
			fmt.Fprintf(&sb, `<rect class="pf-cell" x="%.1f" y="%.1f" width="%.0f" height="%.0f" rx="1.5" fill-opacity="%.2f"><title>%s %02d h: %d</title></rect>`,
				left+float64(hr)*cell, y, cell-1, cell-1, op, weekdayShort[d], hr, c)
		}
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203
}

// ---- list ----

type outletCard struct {
	profile.Summary
	URL      string
	LeanCls  string
	Meta     string
	PerDayTx string
	HealthTx string
	Spark    template.HTML
}

type profListView struct {
	Slots     []string
	Cards     []outletCard
	Total     int
	Articles  int
	Sort      string
	Kind      string
	Region    string
	Lean      string
	Sorts     []struct{ Key, Label string }
	Kinds     []string
	Regions   []string
	Leans     []string
	Pick      []domain.Source
	Generated time.Time
	Err       string
}

var healthText = map[string]string{"ok": "Feeds al día", "warn": "Algún feed con error", "bad": "Feeds con error", "unknown": "Sin datos de feeds"}

func metaOf(s domain.Source) string {
	var p []string
	if s.Kind != "" {
		p = append(p, s.Kind)
	}
	if s.Region != "" {
		p = append(p, s.Region)
	}
	return strings.Join(p, " · ")
}

func (h *profileHandler) pageList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), profileTimeout)
	defer cancel()
	q := r.URL.Query()
	v := profListView{Slots: []string{"a", "b", "c", "d"}, Sort: q.Get("orden"), Kind: q.Get("tipo"), Region: q.Get("region"), Lean: q.Get("linea")}
	v.Sorts = []struct{ Key, Label string }{{"nombre", "Nombre"}, {"volumen", "Volumen"}, {"linea", "Línea editorial"}}
	if v.Sort != "volumen" && v.Sort != "linea" {
		v.Sort = "nombre"
	}
	for _, l := range profile.SideLabels {
		v.Leans = append(v.Leans, l)
	}
	ov, err := h.e.Overview(ctx)
	if err != nil {
		h.s.log.Error("profile: overview", "err", err)
		v.Err = "No se pudieron calcular los medios en este momento."
		h.s.render(w, r, http.StatusInternalServerError, "medios", "Medios", "outlets", v)
		return
	}
	v.Generated, v.Articles = ov.GeneratedAt, ov.Articles30
	kinds, regions := map[string]bool{}, map[string]bool{}
	for _, o := range ov.Outlets {
		v.Pick = append(v.Pick, o.Source)
		if o.Source.Kind != "" {
			kinds[o.Source.Kind] = true
		}
		if o.Source.Region != "" {
			regions[o.Source.Region] = true
		}
		if v.Kind != "" && o.Source.Kind != v.Kind || v.Region != "" && o.Source.Region != v.Region {
			continue
		}
		if v.Lean != "" && profile.SideLabels[profile.LeanBucket(o.Source.Leaning)] != v.Lean {
			continue
		}
		v.Cards = append(v.Cards, outletCard{Summary: o, URL: "/medios/" + url.PathEscape(o.Source.ID), LeanCls: leanClassOf(o.Source.Leaning),
			Meta: metaOf(o.Source), PerDayTx: fnum(o.PerDay, 1), HealthTx: healthText[o.Health], Spark: sparkSVG(o.Spark)})
	}
	for k := range kinds {
		v.Kinds = append(v.Kinds, k)
	}
	for k := range regions {
		v.Regions = append(v.Regions, k)
	}
	sort.Strings(v.Kinds)
	sort.Strings(v.Regions)
	sort.SliceStable(v.Pick, func(i, j int) bool { return strings.ToLower(v.Pick[i].Name) < strings.ToLower(v.Pick[j].Name) })
	v.Total = len(v.Cards)
	byName := func(i, j int) bool {
		return strings.ToLower(v.Cards[i].Source.Name) < strings.ToLower(v.Cards[j].Source.Name)
	}
	sort.SliceStable(v.Cards, func(i, j int) bool {
		a, b := v.Cards[i], v.Cards[j]
		switch v.Sort {
		case "volumen":
			if a.Total14 != b.Total14 {
				return a.Total14 > b.Total14
			}
		case "linea":
			ra, rb := leaningRank(a.Source.Leaning), leaningRank(b.Source.Leaning)
			if ra != rb {
				return ra < rb
			}
		}
		return byName(i, j)
	})
	h.s.render(w, r, http.StatusOK, "medios", "Medios", "outlets", v)
}

func leanClassOf(l string) string { return fmt.Sprintf("l%d", leaningRank(l)) }

// ---- profile page ----

type profRowView struct {
	Label, Note string
	Cells       []string
	Small       []bool // sample too small to read much into it
}

type profBar struct {
	Label  string
	N, Pct int
}

type profPageView struct {
	*profile.Profile
	LeanCls     string
	Meta        string
	Bars        template.HTML
	Heat        template.HTML
	HeatNote    string
	Spark       template.HTML
	HealthTx    string
	LagDays     int
	DayOpts     []dayOpt
	Metrics     []profRowView
	Sides       []profile.SideStat
	SectionRows []profBar
	ShotURL     string
	ShotsPage   string
	Pick        []domain.Source
	HeadlineURL func(int64) string
	Generated   time.Time
	NoTrends    bool
	ToneLabels  []profile.Count
}

func (h *profileHandler) view(ctx context.Context, p *profile.Profile, days int, pick []domain.Source) profPageView {
	v := profPageView{Profile: p, LeanCls: leanClassOf(p.Source.Leaning), Meta: metaOf(p.Source), Bars: barsSVG(p.Daily, h.s.loc), Heat: heatSVG(p.Heat),
		HealthTx: healthText[p.Health], LagDays: days, Generated: p.GeneratedAt, NoTrends: !p.Lag.Available, Pick: pick}
	var sp []int
	for _, d := range p.Daily {
		sp = append(sp, d.N)
	}
	v.Spark = sparkSVG(sp)
	if hr, n, tot := peakHour(p.Heat); tot > 0 {
		v.HeatNote = fmt.Sprintf("Hora con más publicaciones: %02d:00–%02d:59 (%d de %d artículos, %d%%). Horas en %s.", hr, hr, n, tot, n*100/tot, h.s.loc)
	}
	for _, o := range []int{7, 14} {
		v.DayOpts = append(v.DayOpts, dayOpt{Days: o, URL: "/medios/" + url.PathEscape(p.Source.ID) + "?dias=" + strconv.Itoa(o), On: o == days})
	}
	for _, sd := range p.Sections {
		pc := 0
		if p.SectionsN > 0 {
			pc = sd.N * 100 / p.SectionsN
		}
		v.SectionRows = append(v.SectionRows, profBar{Label: sd.Label, N: sd.N, Pct: pc})
	}
	for _, sd := range p.Lag.Sides {
		if sd.Covered > 0 {
			v.Sides = append(v.Sides, sd)
		}
	}
	if p.Shot != nil {
		v.ShotURL = (&url.URL{Path: "/shots/" + p.Shot.Path}).EscapedPath()
		v.ShotsPage = "/capturas/" + url.PathEscape(p.Source.ID)
	}
	if p.Tone != nil {
		v.ToneLabels = p.Tone.Labels
	}
	return v
}

func (h *profileHandler) sourcesPick(ctx context.Context) []domain.Source {
	src, err := h.s.d.Store.ListSources(ctx)
	if err != nil {
		return nil
	}
	out := sortSources(src)
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (h *profileHandler) pageProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || len(id) > 64 {
		h.s.notFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), profileTimeout)
	defer cancel()
	days := h.lagDays(r)
	p, err := h.e.Profile(ctx, id, days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := h.view(ctx, p, days, h.sourcesPick(ctx))
	h.s.render(w, r, http.StatusOK, "medio", p.Source.Name+" · ficha", "outlets", v)
}

// ---- compare ----

type profCmpView struct {
	Profiles  []*profile.Profile
	Heads     []profCmpHead
	Rows      []profRowView
	Pick      []domain.Source
	Sel       []string // 4 slots
	LagDays   int
	DayOpts   []dayOpt
	Generated time.Time
	Msg       string
}

type profCmpHead struct {
	Name, URL, Lean, LeanCls string
	Spark                    template.HTML
}

func (h *profileHandler) pageCompare(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), profileTimeout)
	defer cancel()
	q := r.URL.Query()
	days := h.lagDays(r)
	v := profCmpView{LagDays: days, Pick: h.sourcesPick(ctx), Sel: make([]string, maxCompare)}
	var ids []string
	seen := map[string]bool{}
	for _, k := range []string{"a", "b", "c", "d"} {
		id := strings.TrimSpace(q.Get(k))
		if id == "" || seen[id] || len(id) > 64 {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for i, id := range ids {
		v.Sel[i] = id
	}
	for _, id := range ids {
		p, err := h.e.Profile(ctx, id, days)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Profiles = append(v.Profiles, p)
		v.Generated = p.GeneratedAt
	}
	qs := url.Values{}
	for i, id := range ids {
		qs.Set(string(rune('a'+i)), id)
	}
	for _, o := range []int{7, 14} {
		u := url.Values{}
		for k, x := range qs {
			u[k] = x
		}
		u.Set("dias", strconv.Itoa(o))
		v.DayOpts = append(v.DayOpts, dayOpt{Days: o, URL: "/medios/comparar?" + u.Encode(), On: o == days})
	}
	switch len(v.Profiles) {
	case 0:
		v.Msg = "Elige al menos dos medios para compararlos."
	case 1:
		v.Msg = "Elige otro medio para compararlo."
	}
	for _, p := range v.Profiles {
		var sp []int
		for _, d := range p.Daily {
			sp = append(sp, d.N)
		}
		v.Heads = append(v.Heads, profCmpHead{Name: p.Source.Name, URL: "/medios/" + url.PathEscape(p.Source.ID), Lean: p.Source.Leaning, LeanCls: leanClassOf(p.Source.Leaning), Spark: sparkSVG(sp)})
	}
	v.Rows = h.metricRows(v.Profiles, days)
	h.s.render(w, r, http.StatusOK, "medios_cmp", "Comparar medios", "outlets", v)
}

// metricRows builds the side-by-side table; every cell states its sample size.
func (h *profileHandler) metricRows(ps []*profile.Profile, days int) []profRowView {
	mk := func(label, note string, f func(p *profile.Profile) (string, bool)) profRowView {
		r := profRowView{Label: label, Note: note}
		for _, p := range ps {
			c, small := f(p)
			r.Cells, r.Small = append(r.Cells, c), append(r.Small, small)
		}
		return r
	}
	dash := "—"
	pd := fmt.Sprintf("%d d", days)
	rows := []profRowView{
		mk("Artículos por día", "media de los últimos 30 d", func(p *profile.Profile) (string, bool) {
			return fmt.Sprintf("%s (n=%s)", fnum(p.Avg30, 1), humanInt(int64(p.Total30))), p.Total30 < 30
		}),
		mk("Último artículo", "", func(p *profile.Profile) (string, bool) {
			if p.Last.IsZero() {
				return dash, false
			}
			return h.s.ago(p.Last), false
		}),
		mk("Hora con más publicaciones", "últimos 30 d, hora local", func(p *profile.Profile) (string, bool) {
			hr, n, tot := peakHour(p.Heat)
			if tot == 0 {
				return dash, false
			}
			return fmt.Sprintf("%02d h (%d%% de n=%d)", hr, n*100/tot, tot), tot < 50
		}),
		mk("Cobertura de historias", "historias con ≥3 medios, "+pd, func(p *profile.Profile) (string, bool) {
			if !p.Lag.Available || p.Lag.Stories == 0 {
				return dash, false
			}
			return fmt.Sprintf("%s %% (%d de %d)", fnum(p.Lag.CoveragePct(), 0), p.Lag.Covered, p.Lag.Stories), p.Lag.Stories < 10
		}),
		mk("Retardo mediano frente al primero", "minutos desde la primera publicación, "+pd, func(p *profile.Profile) (string, bool) {
			if !p.Lag.Available || p.Lag.LagSamples == 0 {
				return dash, false
			}
			return fmt.Sprintf("%s (n=%d)", lagText(p.Lag.MedianLagMin), p.Lag.LagSamples), p.Lag.LagSamples < 10
		}),
		mk("Veces que publica primero", "sobre las historias que cubre, "+pd, func(p *profile.Profile) (string, bool) {
			if !p.Lag.Available || p.Lag.Covered == 0 {
				return dash, false
			}
			return fmt.Sprintf("%s %% (%d de %d)", fnum(p.Lag.SharePct(), 0), p.Lag.FirstN, p.Lag.Covered), p.Lag.Covered < 10
		}),
		mk("Historias omitidas", "cubiertas por otros ≥3 medios, "+pd, func(p *profile.Profile) (string, bool) {
			if !p.Lag.Available || p.Lag.Stories == 0 {
				return dash, false
			}
			return fmt.Sprintf("%d de %d", p.Lag.Omitted, p.Lag.Stories), p.Lag.Stories < 10
		}),
	}
	anyRev, anyTone := false, false
	for _, p := range ps {
		anyRev = anyRev || p.Rev != nil
		anyTone = anyTone || p.Tone != nil
	}
	if anyRev {
		rows = append(rows, mk("Titulares editados", "por cada 100 artículos, 14 d", func(p *profile.Profile) (string, bool) {
			if p.Rev == nil {
				return dash, false
			}
			return fmt.Sprintf("%s (%d en n=%d)", fnum(p.Rev.Rate(), 1), p.Rev.Edits, p.Rev.Articles), p.Rev.Articles < 50
		}))
	}
	if anyTone {
		rows = append(rows, mk("Tono (estimación de un modelo)", "etiqueta más frecuente, 14 d", func(p *profile.Profile) (string, bool) {
			if p.Tone == nil || len(p.Tone.Labels) == 0 {
				return dash, false
			}
			l := p.Tone.Labels[0]
			return fmt.Sprintf("%s %d %% (n=%d, %s)", l.Label, l.N*100/p.Tone.Total, p.Tone.Total, p.Tone.Model), p.Tone.Total < 30
		}))
	}
	return rows
}

// ---- API ----

type outletSummaryJSON struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Leaning  string     `json:"leaning"`
	Kind     string     `json:"kind,omitempty"`
	Region   string     `json:"region,omitempty"`
	Homepage string     `json:"homepage,omitempty"`
	Total30  int        `json:"articles_30d"`
	Total14  int        `json:"articles_14d"`
	PerDay   float64    `json:"articles_per_day_14d"`
	LastAt   *time.Time `json:"last_article_at,omitempty"`
	Health   string     `json:"feed_health"`
	Spark    []int      `json:"spark_14d"`
}

func (h *profileHandler) apiList(w http.ResponseWriter, r *http.Request) {
	ov, err := h.e.Overview(r.Context())
	if err != nil {
		h.s.log.Error("api outlets", "err", err)
		h.s.fail(w, r, http.StatusInternalServerError, "No se pudieron calcular los medios.")
		return
	}
	out := make([]outletSummaryJSON, 0, len(ov.Outlets))
	for _, o := range ov.Outlets {
		out = append(out, outletSummaryJSON{ID: o.Source.ID, Name: o.Source.Name, Leaning: o.Source.Leaning, Kind: o.Source.Kind, Region: o.Source.Region,
			Homepage: o.Source.Homepage, Total30: o.Total30, Total14: o.Total14, PerDay: o.PerDay, LastAt: tptr(o.Last), Health: o.Health, Spark: o.Spark})
	}
	writeJSON(w, http.StatusOK, map[string]any{"generated_at": ov.GeneratedAt, "note": "La línea editorial es una etiqueta subjetiva editable en sources.yaml.", "outlets": out})
}

type feedJSON struct {
	URL       string     `json:"url"`
	Section   string     `json:"section,omitempty"`
	Status    string     `json:"status"`
	LastOK    *time.Time `json:"last_ok,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

type sideJSON struct {
	Leaning string `json:"leaning"`
	Covered int    `json:"stories_covered_by_side"`
	Omitted int    `json:"omitted_by_outlet"`
}

type lagJSON struct {
	Days         int        `json:"days"`
	Stories      int        `json:"stories"`
	Covered      int        `json:"covered"`
	Omitted      int        `json:"omitted"`
	FirstN       int        `json:"first"`
	MedianLagMin float64    `json:"median_lag_minutes"`
	P75LagMin    float64    `json:"p75_lag_minutes"`
	LagSamples   int        `json:"lag_samples"`
	Sides        []sideJSON `json:"sides"`
}

func (h *profileHandler) apiProfile(w http.ResponseWriter, r *http.Request) {
	days := h.lagDays(r)
	p, err := h.e.Profile(r.Context(), r.PathValue("id"), days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	feeds := make([]feedJSON, 0, len(p.Feeds))
	for _, f := range p.Feeds {
		feeds = append(feeds, feedJSON{URL: f.URL, Section: f.Section, Status: f.Status, LastOK: tptr(f.LastOK), LastError: f.LastError})
	}
	daily := make([]map[string]any, 0, len(p.Daily))
	for _, d := range p.Daily {
		daily = append(daily, map[string]any{"day": d.Day.Format("2006-01-02"), "articles": d.N})
	}
	secs := make([]map[string]any, 0, len(p.Sections))
	for _, s := range p.Sections {
		secs = append(secs, map[string]any{"section": s.Label, "articles": s.N})
	}
	terms := make([]map[string]any, 0, len(p.Terms))
	for _, t := range p.Terms {
		terms = append(terms, map[string]any{"term": t.Term, "articles": t.Count, "score": t.Score})
	}
	heads := make([]refJSON, 0, len(p.Headlines))
	for _, a := range p.Headlines {
		heads = append(heads, refJSON{ID: a.ID, SourceID: a.SourceID, Title: a.Title, URL: a.URL, At: a.At})
	}
	res := map[string]any{
		"generated_at": p.GeneratedAt,
		"note":         "La línea editorial es una etiqueta subjetiva editable en sources.yaml. Las métricas son descriptivas y no clasifican la calidad.",
		"id":           p.Source.ID, "name": p.Source.Name, "leaning": p.Source.Leaning, "kind": p.Source.Kind, "region": p.Source.Region,
		"homepage": p.Source.Homepage, "feed_health": p.Health, "feeds": feeds,
		"volume":         map[string]any{"period_days": profile.DailyDays, "articles": p.Total30, "per_day": p.Avg30, "daily": daily, "last_article_at": tptr(p.Last)},
		"hours":          map[string]any{"period_days": profile.DailyDays, "timezone": h.s.loc.String(), "weekday_hour": p.Heat},
		"sections":       map[string]any{"period_days": p.SectionDays, "sample": p.SectionsN, "items": secs},
		"terms":          map[string]any{"period_days": profile.TermDays, "sample": p.TermsN, "items": terms},
		"latest":         heads,
		"estimates_note": "Tono: estimación de un modelo, no un hecho.",
	}
	if p.Lag.Available {
		sides := []sideJSON{}
		for _, s := range p.Lag.Sides {
			sides = append(sides, sideJSON{s.Label, s.Covered, s.Omitted})
		}
		res["lag"] = lagJSON{p.Lag.Days, p.Lag.Stories, p.Lag.Covered, p.Lag.Omitted, p.Lag.FirstN, p.Lag.MedianLagMin, p.Lag.P75LagMin, p.Lag.LagSamples, sides}
	}
	if p.Rev != nil {
		res["revisions"] = map[string]any{"period_days": p.Rev.Days, "title_edits": p.Rev.Edits, "articles": p.Rev.Articles, "per_100_articles": p.Rev.Rate()}
	}
	if p.Tone != nil {
		res["tone"] = map[string]any{"model": p.Tone.Model, "sample": p.Tone.Total, "labels": p.Tone.Labels, "estimate": true}
	}
	writeJSON(w, http.StatusOK, res)
}
