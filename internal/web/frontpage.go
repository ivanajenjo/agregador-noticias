package web

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// "Qué abre cada medio": front-page prominence (GET /portadas, /api/frontpage).
// The data is extracted by internal/shot while taking each screenshot; here we
// only read it through domain.ProminenceStore (nil => section hidden, 503).

const (
	fpTimeout     = 10 * time.Second
	fpTopN        = 3  // items per outlet in "Lo que abre hoy"
	fpTopRows     = 12 // rows of "Más tiempo en portada"
	fpTopWindow   = 24 * time.Hour
	fpMaxHistSrc  = 6
	fpMaxHistRows = 2000
	fpMaxURLRunes = 2048
)

func init() {
	registerPage("frontpage")
	registerNav(NavItem{Key: "frontpage", Label: "Portadas", Href: "/portadas", Order: 62,
		Desc: "Qué abre cada medio y cuánto tiempo se queda una noticia en portada",
		Show: func(s *server) bool { return s.d.Prominence != nil },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="3" width="16" height="18" rx="2"/><path d="M7 7h10M7 11h6M7 15h10M7 18h4"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /portadas", s.pageFrontpage)
		m.HandleFunc("GET /api/frontpage", s.apiFrontpage)
		m.HandleFunc("GET /api/frontpage/top", s.apiFrontpageTop)
	})
}

// ---- view models ----

type fpItem struct {
	Rank     int
	Title    string
	URL      string
	Pct      int // prominence 0..100
	FontPx   int
	Fold     bool
	HistURL  string
	Multi    int    // outlets (incl. this one) leading with the same story, 0/1 = none
	MultiTxt string // "También: A, B"
	ArtID    int64  // article matched by canonical URL, 0 = none
}

type fpCard struct {
	Source domain.Source
	At     time.Time
	Stale  bool
	Items  []fpItem
}

type fpTopRow struct {
	Source   string
	Title    string
	URL      string
	Captures int
	Best     int
	Pct      int
	Span     string
	HistURL  string
}

type fpMulti struct {
	Title   string
	Outlets []string
}

type fpHist struct {
	Source  string
	Title   string
	Summary string
	Chart   template.HTML
}

type frontpageView struct {
	Cards   []fpCard
	Multi   []fpMulti
	Top     []fpTopRow
	Hist    []fpHist
	HistURL string // the selected item's canonical URL
	HistErr string
	Err     string
}

func histURL(canon string) string {
	return "/portadas?item=" + url.QueryEscape(canon) + "#evolucion"
}

func pct(p float64) int {
	v := int(math.Round(p * 100))
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// ---- page ----

func (s *server) pageFrontpage(w http.ResponseWriter, r *http.Request) {
	if s.d.Prominence == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Las portadas no están disponibles")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fpTimeout)
	defer cancel()
	v := frontpageView{}
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		s.log.Error("frontpage: sources", "err", err)
		v.Err = "No se pudo leer la lista de medios."
	}
	names := map[string]string{}
	for _, so := range src {
		names[so.ID] = so.Name
	}
	now := s.now()

	// 1. Lo que abre hoy.
	for _, so := range sortSources(src) {
		items, err := s.d.Prominence.ListFrontItems(ctx, so.ID, time.Time{})
		if err != nil {
			s.log.Warn("frontpage: items", "source", so.ID, "err", err)
			continue
		}
		if len(items) == 0 {
			continue
		}
		c := fpCard{Source: so, At: items[0].TakenAt, Stale: now.Sub(items[0].TakenAt) > 48*time.Hour}
		for i, it := range items {
			if i >= fpTopN {
				break
			}
			c.Items = append(c.Items, fpItem{Rank: it.Rank, Title: it.Title, URL: it.URL, Pct: pct(it.Prominence),
				FontPx: int(math.Round(it.FontPx)), Fold: it.AboveFold, HistURL: histURL(it.CanonicalURL), ArtID: it.ArticleID})
		}
		v.Cards = append(v.Cards, c)
	}
	v.Multi = s.markShared(ctx, v.Cards)

	// 2. Más tiempo en portada (últimas 24 h).
	top, err := s.d.Prominence.TopItems(ctx, now.Add(-fpTopWindow), now.Add(time.Minute), nil, 200)
	if err != nil {
		s.log.Warn("frontpage: top", "err", err)
	}
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].Captures != top[j].Captures {
			return top[i].Captures > top[j].Captures
		}
		return top[i].BestRank < top[j].BestRank
	})
	for i, t := range top {
		if i >= fpTopRows {
			break
		}
		name := names[t.SourceID]
		if name == "" {
			name = t.SourceID
		}
		span := t.FirstAt.In(s.loc).Format("15:04")
		if t.LastAt.After(t.FirstAt) {
			span += "–" + t.LastAt.In(s.loc).Format("15:04")
		}
		v.Top = append(v.Top, fpTopRow{Source: name, Title: t.Title, URL: t.CanonicalURL, Captures: t.Captures,
			Best: t.BestRank, Pct: pct(t.MaxProm), Span: span, HistURL: histURL(t.CanonicalURL)})
	}

	// 3. Evolución de un titular concreto.
	if item := r.URL.Query().Get("item"); item != "" {
		v.HistURL = item
		if utf8.RuneCountInString(item) > fpMaxURLRunes {
			v.HistErr = "Dirección demasiado larga."
		} else {
			pts, err := s.d.Prominence.ItemHistory(ctx, item)
			if err != nil {
				s.log.Warn("frontpage: history", "err", err)
				v.HistErr = "No se pudo leer el historial."
			}
			v.Hist = s.buildHist(pts, names)
			if len(v.Hist) == 0 && v.HistErr == "" {
				v.HistErr = "Esta noticia no aparece en ninguna portada capturada."
			}
		}
	}
	s.render(w, r, http.StatusOK, "frontpage", "Portadas", "frontpage", v)
}

// markShared finds stories that several outlets lead with. It matches the
// top items to the stories of domain.TrendProvider through the article id the
// store resolved from the canonical URL (cheap: no text similarity here).
// Without a TrendProvider or without matches nothing is marked.
func (s *server) markShared(ctx context.Context, cards []fpCard) []fpMulti {
	if s.d.Trends == nil || len(cards) < 2 {
		return nil
	}
	stories, err := s.d.Trends.Stories(ctx, domain.TrendOptions{Window: 24 * time.Hour, Limit: 40, Now: s.now()})
	if err != nil || len(stories) == 0 {
		return nil
	}
	byArt := map[int64]int{}
	for si, st := range stories {
		for _, cov := range st.Coverage {
			for _, h := range cov.Headlines {
				byArt[h.ID] = si
			}
		}
	}
	type ref struct{ card, item int }
	groups := map[int][]ref{}
	for ci := range cards {
		seen := map[int]bool{}
		for ii := range cards[ci].Items {
			if cards[ci].Items[ii].ArtID == 0 {
				continue
			}
			if si, ok := byArt[cards[ci].Items[ii].ArtID]; ok && !seen[si] {
				seen[si] = true
				groups[si] = append(groups[si], ref{ci, ii})
			}
		}
	}
	var keys []int
	for si, g := range groups {
		if len(g) >= 2 {
			keys = append(keys, si)
		}
	}
	sort.Ints(keys)
	var out []fpMulti
	for _, si := range keys {
		g := groups[si]
		var outlets []string
		for _, rf := range g {
			outlets = append(outlets, cards[rf.card].Source.Name+" (#"+strconv.Itoa(cards[rf.card].Items[rf.item].Rank)+")")
		}
		for _, rf := range g {
			it := &cards[rf.card].Items[rf.item]
			it.Multi = len(g)
			var others []string
			for _, o := range g {
				if o != rf {
					others = append(others, cards[o.card].Source.Name)
				}
			}
			it.MultiTxt = "También abren con esto: " + strings.Join(others, ", ")
		}
		out = append(out, fpMulti{Title: stories[si].Title, Outlets: outlets})
	}
	return out
}

func (s *server) buildHist(pts []domain.ItemPoint, names map[string]string) []fpHist {
	by := map[string][]domain.ItemPoint{}
	var order []string
	for _, p := range pts {
		if _, ok := by[p.SourceID]; !ok {
			order = append(order, p.SourceID)
		}
		by[p.SourceID] = append(by[p.SourceID], p)
	}
	var out []fpHist
	for _, id := range order {
		if len(out) >= fpMaxHistSrc {
			break
		}
		ps := by[id]
		name := names[id]
		if name == "" {
			name = id
		}
		out = append(out, fpHist{Source: name, Title: ps[len(ps)-1].Title, Summary: s.histSummary(ps), Chart: s.stepChart(ps)})
	}
	return out
}

// medianGap is the median spacing between consecutive captures.
func medianGap(ps []domain.ItemPoint) time.Duration {
	if len(ps) < 2 {
		return 0
	}
	g := make([]time.Duration, 0, len(ps)-1)
	for i := 1; i < len(ps); i++ {
		g = append(g, ps[i].TakenAt.Sub(ps[i-1].TakenAt))
	}
	sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
	return g[len(g)/2]
}

func humanDur(d time.Duration) string {
	if d < time.Minute {
		return "menos de 1 min"
	}
	if d < 90*time.Minute {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return strings.Replace(fmt.Sprintf("%.1f h", d.Hours()), ".", ",", 1)
}

func (s *server) histSummary(ps []domain.ItemPoint) string {
	first, last := ps[0].TakenAt.In(s.loc), ps[len(ps)-1].TakenAt.In(s.loc)
	best, opened := ps[0].Rank, 0
	for _, p := range ps {
		if p.Rank < best {
			best = p.Rank
		}
		if p.Rank == 1 {
			opened++
		}
	}
	f := "15:04"
	if first.YearDay() != last.YearDay() || first.Year() != last.Year() {
		f = "02/01 15:04"
	}
	txt := fmt.Sprintf("Visto en %d captura%s, de %s a %s (≈ %s entre la primera y la última); mejor posición #%d",
		len(ps), map[bool]string{true: "", false: "s"}[len(ps) == 1], first.Format(f), last.Format(f), humanDur(last.Sub(first)), best)
	if opened > 0 {
		txt += fmt.Sprintf("; abrió portada en %d captura%s", opened, map[bool]string{true: "", false: "s"}[opened == 1])
	}
	return txt + ". Dato observado en las capturas: entre dos capturas no sabemos qué pasó."
}

// stepChart draws rank (1 at the top) over time as a step line. Gaps longer
// than 2.5x the usual capture spacing are treated as "not on the front page".
func (s *server) stepChart(ps []domain.ItemPoint) template.HTML {
	const W, H, L, R, T, B = 640.0, 220.0, 34.0, 12.0, 14.0, 30.0
	if len(ps) == 0 {
		return ""
	}
	t0, t1 := ps[0].TakenAt, ps[len(ps)-1].TakenAt
	mg := medianGap(ps)
	if mg <= 0 {
		mg = 30 * time.Minute
	}
	thr := time.Duration(float64(mg) * 2.5)
	if thr < 90*time.Minute {
		thr = 90 * time.Minute
	}
	t1 = t1.Add(mg) // the last capture still "lasts" one interval
	span := t1.Sub(t0).Seconds()
	if span <= 0 {
		span = 1
	}
	maxRank := 5
	for _, p := range ps {
		if p.Rank > maxRank {
			maxRank = p.Rank
		}
	}
	x := func(t time.Time) float64 { return L + (W-L-R)*t.Sub(t0).Seconds()/span }
	y := func(rank int) float64 { return T + (H-T-B)*float64(rank-1)/float64(maxRank-1) }

	var sb strings.Builder
	desc := "Posición en portada a lo largo del tiempo: " + s.histSummary(ps)
	fmt.Fprintf(&sb, `<svg class="fp-chart" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s"><title>%s</title>`, W, H,
		template.HTMLEscapeString(desc), template.HTMLEscapeString(desc))
	// horizontal grid + y labels
	ticks := []int{1}
	step := 1
	switch {
	case maxRank > 20:
		step = 10
	case maxRank > 10:
		step = 5
	case maxRank > 5:
		step = 2
	}
	for r := step; r <= maxRank; r += step {
		if r != 1 {
			ticks = append(ticks, r)
		}
	}
	for _, r := range ticks {
		fmt.Fprintf(&sb, `<line class="g" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text class="t" x="%.1f" y="%.1f" text-anchor="end">#%d</text>`,
			L, W-R, y(r), y(r), L-6, y(r)+4, r)
	}
	// time labels
	fm := "15:04"
	if t1.In(s.loc).Sub(t0.In(s.loc)) > 20*time.Hour {
		fm = "02/01 15:04"
	}
	for i := 0; i < 3; i++ {
		tt := t0.Add(time.Duration(float64(i) / 2 * span * float64(time.Second)))
		anchor := []string{"start", "middle", "end"}[i]
		fmt.Fprintf(&sb, `<text class="t" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, x(tt), H-8, anchor, tt.In(s.loc).Format(fm))
	}
	// step segments
	var d strings.Builder
	segStart := true
	for i, p := range ps {
		xi, yi := x(p.TakenAt), y(p.Rank)
		end := p.TakenAt.Add(mg)
		if i+1 < len(ps) && ps[i+1].TakenAt.Sub(p.TakenAt) <= thr {
			end = ps[i+1].TakenAt
		}
		if segStart {
			fmt.Fprintf(&d, "M%.1f %.1f", xi, yi)
		} else {
			fmt.Fprintf(&d, "L%.1f %.1f", xi, yi)
		}
		fmt.Fprintf(&d, "H%.1f", x(end))
		segStart = !(i+1 < len(ps) && ps[i+1].TakenAt.Sub(p.TakenAt) <= thr)
	}
	fmt.Fprintf(&sb, `<path class="line" d="%s"/>`, d.String())
	for _, p := range ps {
		cls := "dot"
		if p.Rank == 1 {
			cls = "dot top"
		}
		fmt.Fprintf(&sb, `<circle class="%s" cx="%.1f" cy="%.1f" r="3.5"><title>%s · #%d · %d%%</title></circle>`, cls, x(p.TakenAt), y(p.Rank),
			p.TakenAt.In(s.loc).Format("02/01 15:04"), p.Rank, pct(p.Prominence))
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String()) // #nosec G203 -- numbers and escaped text only
}

// ---- API ----

type fpJSONItem struct {
	Rank         int     `json:"rank"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	CanonicalURL string  `json:"canonical_url"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	W            float64 `json:"w"`
	H            float64 `json:"h"`
	FontPx       float64 `json:"font_px"`
	Prominence   float64 `json:"prominence"`
	AboveFold    bool    `json:"above_fold"`
	ArticleID    int64   `json:"article_id,omitempty"`
}

type fpJSON struct {
	Source  string       `json:"source"`
	TakenAt time.Time    `json:"taken_at"`
	Width   int          `json:"width,omitempty"`
	Height  int          `json:"height,omitempty"`
	Items   []fpJSONItem `json:"items"`
}

// apiFrontpage: ?source=ID (required) and one of &at=<unix|RFC3339>,
// &path=<screenshot path as served under /shots/>; neither => latest capture.
func (s *server) apiFrontpage(w http.ResponseWriter, r *http.Request) {
	if s.d.Prominence == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Las portadas no están disponibles")
		return
	}
	q := r.URL.Query()
	src := q.Get("source")
	if src == "" || len(src) > 100 {
		s.fail(w, r, http.StatusBadRequest, "Falta el parámetro source")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fpTimeout)
	defer cancel()
	var at time.Time
	var shot *domain.Screenshot
	switch {
	case q.Get("path") != "":
		sh, err := s.findShotByPath(ctx, src, q.Get("path"))
		if err != nil || sh == nil {
			s.fail(w, r, http.StatusNotFound, "Captura no encontrada")
			return
		}
		shot, at = sh, sh.TakenAt
	case q.Get("at") != "":
		t, ok := parseAt(q.Get("at"))
		if !ok {
			s.fail(w, r, http.StatusBadRequest, "Parámetro at no válido (segundos Unix o RFC 3339)")
			return
		}
		at = t
	}
	items, err := s.d.Prominence.ListFrontItems(ctx, src, at)
	if err != nil {
		s.log.Error("api frontpage", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al leer las portadas")
		return
	}
	out := fpJSON{Source: src, Items: []fpJSONItem{}}
	if len(items) > 0 {
		out.TakenAt = items[0].TakenAt
		if shot == nil {
			shot = s.shotAt(ctx, src, items[0].TakenAt)
		}
	}
	if shot != nil {
		out.Width, out.Height = shot.Width, shot.Height
	}
	for _, it := range items {
		out.Items = append(out.Items, fpJSONItem{Rank: it.Rank, Title: it.Title, URL: it.URL, CanonicalURL: it.CanonicalURL,
			X: it.X, Y: it.Y, W: it.W, H: it.H, FontPx: it.FontPx, Prominence: it.Prominence, AboveFold: it.AboveFold, ArticleID: it.ArticleID})
	}
	writeJSON(w, http.StatusOK, out)
}

func parseAt(v string) (time.Time, bool) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0), true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func (s *server) shotAt(ctx context.Context, src string, at time.Time) *domain.Screenshot {
	l, err := s.d.Store.ListScreenshots(ctx, src, at, at.Add(time.Second))
	if err != nil || len(l) == 0 {
		return nil
	}
	return &l[0]
}

// findShotByPath resolves a screenshot from its served path
// "<source>/YYYY/MM/DD/HHMM.jpg" by listing the days around the encoded date.
func (s *server) findShotByPath(ctx context.Context, src, p string) (*domain.Screenshot, error) {
	p = strings.TrimPrefix(p, "/shots/")
	parts := strings.Split(p, "/")
	if len(parts) < 5 || parts[0] != src {
		return nil, nil
	}
	y, e1 := strconv.Atoi(parts[1])
	m, e2 := strconv.Atoi(parts[2])
	d, e3 := strconv.Atoi(parts[3])
	if e1 != nil || e2 != nil || e3 != nil {
		return nil, nil
	}
	day := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	l, err := s.d.Store.ListScreenshots(ctx, src, day.Add(-24*time.Hour), day.Add(48*time.Hour))
	if err != nil {
		return nil, err
	}
	for i := range l {
		if l[i].Path == p {
			return &l[i], nil
		}
	}
	return nil, nil
}

// apiFrontpageTop: ?hours=24 (1..720) &source=a&source=b &limit=30 (1..100).
func (s *server) apiFrontpageTop(w http.ResponseWriter, r *http.Request) {
	if s.d.Prominence == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Las portadas no están disponibles")
		return
	}
	q := r.URL.Query()
	hours, limit := 24, 30
	if v := q.Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 720 {
			s.fail(w, r, http.StatusBadRequest, "hours debe estar entre 1 y 720")
			return
		}
		hours = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			s.fail(w, r, http.StatusBadRequest, "limit debe estar entre 1 y 100")
			return
		}
		limit = n
	}
	ids := q["source"]
	if len(ids) > 50 {
		ids = ids[:50]
	}
	ctx, cancel := context.WithTimeout(r.Context(), fpTimeout)
	defer cancel()
	now := s.now()
	top, err := s.d.Prominence.TopItems(ctx, now.Add(-time.Duration(hours)*time.Hour), now.Add(time.Minute), ids, limit)
	if err != nil {
		s.log.Error("api frontpage top", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al leer las portadas")
		return
	}
	type row struct {
		Source       string    `json:"source"`
		CanonicalURL string    `json:"canonical_url"`
		Title        string    `json:"title"`
		Captures     int       `json:"captures"`
		BestRank     int       `json:"best_rank"`
		AvgProm      float64   `json:"avg_prominence"`
		MaxProm      float64   `json:"max_prominence"`
		FirstAt      time.Time `json:"first_at"`
		LastAt       time.Time `json:"last_at"`
		ArticleID    int64     `json:"article_id,omitempty"`
	}
	out := make([]row, 0, len(top))
	for _, t := range top {
		out = append(out, row{t.SourceID, t.CanonicalURL, t.Title, t.Captures, t.BestRank, t.AvgProm, t.MaxProm, t.FirstAt, t.LastAt, t.ArticleID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"hours": hours, "items": out})
}
