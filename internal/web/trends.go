package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	trendsTimeout    = 10 * time.Second
	maxTrendLimit    = 100
	defaultTrendLim  = 30
	maxTermKeyRunes  = 120
	defaultTermDays  = 7
	minTermDays      = 1
	maxTermDays      = 30
	maxHeadlinesView = 3
)

// ventanas are the accepted analysis windows (query value -> duration).
var ventanas = []struct {
	Key   string
	Label string
	D     time.Duration
}{
	{"6h", "6 horas", 6 * time.Hour},
	{"24h", "24 horas", 24 * time.Hour},
	{"48h", "48 horas", 48 * time.Hour},
	{"7d", "7 días", 7 * 24 * time.Hour},
}

const defaultVentana = "24h"

func parseVentana(v string) (key string, d time.Duration, ok bool) {
	if v == "" {
		v = defaultVentana
	}
	for _, x := range ventanas {
		if x.Key == v {
			return x.Key, x.D, true
		}
	}
	return "", 0, false
}

// leanNames are the canonical labels of the leaning ranks (see leaningRank).
var leanNames = []string{"Izquierda", "Centro-izquierda", "Centro", "Centro-derecha", "Derecha", "Sin clasificar"}

// leanBuckets sums a leaning->count map into the 6 canonical ranks.
func leanBuckets(m map[string]int) (b [6]int, total int) {
	for k, n := range m {
		if n <= 0 {
			continue
		}
		b[leaningRank(k)] += n
		total += n
	}
	return b, total
}

func leanSummary(b [6]int) string {
	var parts []string
	for i, n := range b {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", leanNames[i], n))
		}
	}
	return strings.Join(parts, " · ")
}

// leanBarSVG renders a slim stacked bar (left to right) with a text alternative.
func leanBarSVG(m map[string]int) template.HTML {
	b, total := leanBuckets(m)
	if total == 0 {
		return ""
	}
	sum := leanSummary(b)
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg class="lbar" viewBox="0 0 100 6" preserveAspectRatio="none" role="img" aria-label="Reparto por línea editorial: %s"><title>%s</title>`,
		template.HTMLEscapeString(sum), template.HTMLEscapeString(sum))
	x := 0.0
	for i, n := range b {
		if n == 0 {
			continue
		}
		w := float64(n) * 100 / float64(total)
		fmt.Fprintf(&sb, `<rect class="b%d" x="%.2f" y="0" width="%.2f" height="6"/>`, i, x, w)
		x += w
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203 -- built from numbers and escaped labels
}

func leanCaption(m map[string]int) string {
	b, _ := leanBuckets(m)
	return leanSummary(b)
}

// sparkSVG renders a tiny line chart of daily counts.
func sparkSVG(v []int) template.HTML {
	if len(v) == 0 {
		return ""
	}
	const w, h, pad = 84.0, 22.0, 2.5
	mx := 1
	strs := make([]string, len(v))
	for i, n := range v {
		if n > mx {
			mx = n
		}
		strs[i] = strconv.Itoa(n)
	}
	desc := "Artículos por día, últimos " + strconv.Itoa(len(v)) + " días (de más antiguo a hoy): " + strings.Join(strs, ", ")
	var pts []string
	var lx, ly float64
	for i, n := range v {
		x := pad
		if len(v) > 1 {
			x = pad + float64(i)*(w-2*pad)/float64(len(v)-1)
		}
		y := h - pad - float64(n)*(h-2*pad)/float64(mx)
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
		lx, ly = x, y
	}
	return template.HTML(fmt.Sprintf( // #nosec G203
		`<svg class="spark" viewBox="0 0 84 22" role="img" aria-label="%s"><title>%s</title><polyline points="%s" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round"/><circle cx="%.1f" cy="%.1f" r="2.2" fill="currentColor"/></svg>`,
		template.HTMLEscapeString(desc), template.HTMLEscapeString(desc), strings.Join(pts, " "), lx, ly))
}

// dailyChartSVG renders stacked daily bars (by leaning rank) for a term.
func dailyChartSVG(days []domain.DayCount, loc *time.Location) template.HTML {
	if len(days) == 0 {
		return ""
	}
	const W, H, left, top, bottom = 360.0, 160.0, 26.0, 16.0, 22.0
	mx := 1
	for _, d := range days {
		if d.Total > mx {
			mx = d.Total
		}
	}
	plotW, plotH := W-left-6, H-top-bottom
	slot := plotW / float64(len(days))
	bw := slot * 0.68
	step := (len(days) + 6) / 7
	var sb strings.Builder
	var desc []string
	for _, d := range days {
		desc = append(desc, fmt.Sprintf("%s: %d", d.Day.In(loc).Format("2/1"), d.Total))
	}
	ad := "Artículos por día. " + strings.Join(desc, "; ")
	fmt.Fprintf(&sb, `<svg class="dchart" viewBox="0 0 360 160" role="img" aria-label="%s"><title>%s</title>`, template.HTMLEscapeString(ad), template.HTMLEscapeString(ad))
	fmt.Fprintf(&sb, `<line class="grid" x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f"/><line class="axis" x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f"/>`,
		left, W-4, top, top, left, W-4, top+plotH, top+plotH)
	fmt.Fprintf(&sb, `<text class="tick" x="%.0f" y="%.1f" text-anchor="end">%d</text><text class="tick" x="%.0f" y="%.1f" text-anchor="end">0</text>`, left-4, top+3.5, mx, left-4, top+plotH+3.5)
	for i, d := range days {
		x := left + float64(i)*slot + (slot-bw)/2
		b, tot := leanBuckets(d.Leanings)
		if d.Total > tot {
			b[5] += d.Total - tot
		}
		label := d.Day.In(loc).Format("2/1")
		fmt.Fprintf(&sb, `<g><title>%s: %d</title>`, label, d.Total)
		y := top + plotH
		for r, n := range b {
			if n == 0 {
				continue
			}
			hh := float64(n) * plotH / float64(mx)
			y -= hh
			fmt.Fprintf(&sb, `<rect class="b%d" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, r, x, y, bw, hh)
		}
		sb.WriteString(`</g>`)
		if len(days) <= 14 && d.Total > 0 {
			fmt.Fprintf(&sb, `<text class="val" x="%.1f" y="%.1f" text-anchor="middle">%d</text>`, x+bw/2, y-3, d.Total)
		}
		if i%step == 0 || i == len(days)-1 && (len(days)-1)%step >= step/2+1 {
			fmt.Fprintf(&sb, `<text class="tick" x="%.1f" y="%.0f" text-anchor="middle">%s</text>`, x+bw/2, H-6, label)
		}
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203
}

// ---- view models ----

type ventOpt struct {
	Key, Label, Short, URL string
	On                     bool
}

type outletView struct {
	Name      string
	Leaning   string
	Count     int
	FirstAt   time.Time
	Lag       string
	First     bool
	Headlines []domain.ArticleRef
	More      int
}

type linkView struct{ Text, URL string }

type storyView struct {
	domain.Story
	FirstName  string
	Outlets    []outletView
	MissingTxt string
	CompareURL string
	TermLinks  []linkView
}

type termRow struct {
	domain.Term
	URL string
}

type trendsView struct {
	Vista     string
	Ventana   string
	WindowLbl string
	Ventanas  []ventOpt
	TabStory  string
	TabTerms  string
	Sources   []srcOpt
	Fuentes   int
	Generated time.Time
	Articles  int64
	BaseDays  int
	Stories   []storyView
	Terms     []termRow
	Err       string
}

func (s *server) trendOpts(q url.Values, window time.Duration, limit int) domain.TrendOptions {
	return domain.TrendOptions{Window: window, Limit: limit, SourceIDs: cleanList(q["fuentes"]), Now: s.now()}
}

func (s *server) coverageName(c domain.SourceCoverage) string {
	if c.Name != "" {
		return c.Name
	}
	return c.SourceID
}

func (s *server) outlets(cov []domain.SourceCoverage, first string, sortByLean bool) []outletView {
	out := make([]outletView, 0, len(cov))
	var t0 time.Time
	for _, c := range cov {
		if !c.FirstAt.IsZero() && (t0.IsZero() || c.FirstAt.Before(t0)) {
			t0 = c.FirstAt
		}
	}
	for _, c := range cov {
		o := outletView{Name: s.coverageName(c), Leaning: c.Leaning, Count: c.Count, FirstAt: c.FirstAt,
			First: first != "" && c.SourceID == first}
		if !t0.IsZero() && !c.FirstAt.IsZero() && c.FirstAt.Sub(t0) >= time.Minute {
			o.Lag = "+" + shortDur(c.FirstAt.Sub(t0))
		}
		hs := c.Headlines
		if len(hs) > maxHeadlinesView {
			o.More = len(hs) - maxHeadlinesView
			hs = hs[:maxHeadlinesView]
		}
		o.Headlines = hs
		out = append(out, o)
	}
	if sortByLean {
		sort.SliceStable(out, func(i, j int) bool { return leaningRank(out[i].Leaning) < leaningRank(out[j].Leaning) })
	}
	return out
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Round(time.Hour).Hours()))
	}
	return fmt.Sprintf("%d d", int(d.Hours()/24))
}

func missingNames(m []domain.SourceCoverage) string {
	var n []string
	for _, c := range m {
		if c.Name != "" {
			n = append(n, c.Name)
		} else if c.SourceID != "" {
			n = append(n, c.SourceID)
		}
	}
	return strings.Join(n, ", ")
}

func termURL(key string) string { return "/tendencias/termino/" + url.PathEscape(key) }

func askURL(q string) string {
	v := url.Values{"q": {truncRunes(q, maxQueryRunes)}, "comparar": {"1"}}
	return "/preguntar?" + v.Encode()
}

func (s *server) storyViews(st []domain.Story) []storyView {
	out := make([]storyView, 0, len(st))
	for _, x := range st {
		v := storyView{Story: x, FirstName: x.FirstBy, CompareURL: askURL(x.Title)}
		for _, c := range x.Coverage {
			if c.SourceID == x.FirstBy {
				v.FirstName = s.coverageName(c)
			}
		}
		v.Outlets = s.outlets(x.Coverage, x.FirstBy, true)
		v.MissingTxt = missingNames(x.Missing)
		for i, t := range x.Terms {
			if i >= 4 || strings.TrimSpace(t) == "" {
				break
			}
			v.TermLinks = append(v.TermLinks, linkView{t, termURL(t)})
		}
		out = append(out, v)
	}
	return out
}

func (s *server) trendsLink(q url.Values, vista, ventana string) string {
	v := url.Values{}
	if vista != "" && vista != "historias" {
		v.Set("vista", vista)
	}
	if ventana != "" && ventana != defaultVentana {
		v.Set("ventana", ventana)
	}
	for _, f := range cleanList(q["fuentes"]) {
		v.Add("fuentes", f)
	}
	if len(v) == 0 {
		return "/tendencias"
	}
	return "/tendencias?" + v.Encode()
}

func (s *server) pageTrends(w http.ResponseWriter, r *http.Request) {
	if s.d.Trends == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Tendencias no disponible: el análisis no está activado en este servidor.")
		return
	}
	q := r.URL.Query()
	key, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Ventana no válida: usa 6h, 24h, 48h o 7d.")
		return
	}
	vista := "historias"
	if q.Get("vista") == "terminos" {
		vista = "terminos"
	}
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()

	v := trendsView{Vista: vista, Ventana: key, TabStory: s.trendsLink(q, "historias", key), TabTerms: s.trendsLink(q, "terminos", key)}
	for _, x := range ventanas {
		v.Ventanas = append(v.Ventanas, ventOpt{Key: x.Key, Label: x.Label, Short: strings.Replace(x.Label, " horas", " h", 1), URL: s.trendsLink(q, vista, x.Key), On: x.Key == key})
		if x.Key == key {
			v.WindowLbl = x.Label
		}
	}
	// source filter
	sel := map[string]bool{}
	for _, f := range cleanList(q["fuentes"]) {
		sel[f] = true
	}
	if src, err := s.d.Store.ListSources(ctx); err == nil {
		for _, so := range sortSources(src) {
			v.Sources = append(v.Sources, srcOpt{so, sel[so.ID]})
			if sel[so.ID] {
				v.Fuentes++
			}
		}
	}
	code := http.StatusOK
	opts := s.trendOpts(q, window, defaultTrendLim)
	if vista == "historias" {
		st, err := s.d.Trends.Stories(ctx, opts)
		if err != nil {
			s.log.Error("trends: stories", "err", err)
			v.Err, code = "No se pudieron calcular las historias en este momento.", http.StatusInternalServerError
		} else {
			v.Stories = s.storyViews(st)
			v.Generated = s.now()
		}
	} else {
		rep, err := s.d.Trends.Trending(ctx, opts)
		if err != nil || rep == nil {
			s.log.Error("trends: trending", "err", err)
			v.Err, code = "No se pudieron calcular las tendencias en este momento.", http.StatusInternalServerError
		} else {
			v.Generated, v.Articles = rep.GeneratedAt, int64(rep.Articles)
			if v.Generated.IsZero() {
				v.Generated = s.now()
			}
			v.BaseDays = int(rep.Baseline.Hours() / 24)
			for _, t := range rep.Terms {
				v.Terms = append(v.Terms, termRow{t, termURL(t.Key)})
			}
		}
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-History-Restore-Request") != "true" {
		w.WriteHeader(code)
		s.fragment(w, "trends", "trends_body", v)
		return
	}
	s.render(w, r, code, "trends", "Tendencias", "trends", v)
}

// ---- term detail ----

type dayOpt struct {
	Days int
	URL  string
	On   bool
}

type termView struct {
	domain.TermDetail
	Days       int
	DayOpts    []dayOpt
	Chart      template.HTML
	Rows       []outletView
	MissingTxt string
	Related    []linkView
	CompareURL string
	Generated  time.Time
	BackURL    string
}

func validKey(k string) bool {
	if strings.TrimSpace(k) == "" || len([]rune(k)) > maxTermKeyRunes {
		return false
	}
	for _, r := range k {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parseDays(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < minTermDays || n > maxTermDays {
		return defaultTermDays
	}
	return n
}

func (s *server) pageTerm(w http.ResponseWriter, r *http.Request) {
	if s.d.Trends == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Tendencias no disponible: el análisis no está activado en este servidor.")
		return
	}
	key := r.PathValue("key")
	if !validKey(key) {
		s.notFound(w, r)
		return
	}
	days := parseDays(r.URL.Query().Get("dias"))
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	d, err := s.d.Trends.Term(ctx, key, days, domain.TrendOptions{Now: s.now()})
	if err != nil && !isNotFound(err) {
		s.log.Error("trends: term", "key", key, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo calcular este término en este momento.")
		return
	}
	if err != nil || d == nil || (d.Total == 0 && len(d.Coverage) == 0) {
		s.notFound(w, r)
		return
	}
	name := firstNonEmpty(d.Term, key)
	v := termView{TermDetail: *d, Days: days, Generated: s.now(), BackURL: "/tendencias?vista=terminos",
		Chart: dailyChartSVG(d.Daily, s.loc), Rows: s.outlets(d.Coverage, "", false), MissingTxt: missingNames(d.Missing),
		CompareURL: askURL("¿Cómo están cubriendo los medios «" + name + "»?")}
	v.Term = name
	for _, n := range []int{3, 7, 14, 30} {
		v.DayOpts = append(v.DayOpts, dayOpt{Days: n, URL: termURL(key) + "?dias=" + strconv.Itoa(n), On: n == days})
	}
	for i, t := range d.Related {
		if i >= 12 {
			break
		}
		if strings.TrimSpace(t) != "" {
			v.Related = append(v.Related, linkView{t, termURL(t)})
		}
	}
	if len(v.Rows) > 0 {
		v.Rows[0].First = true
	}
	s.render(w, r, 200, "trend_term", name, "trends", v)
}
