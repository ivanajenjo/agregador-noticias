package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/framing"
)

// FramingConfig holds the thresholds of "Encuadre y puntos ciegos"; main may
// override it before calling New.
var FramingConfig = framing.DefaultConfig()

var framingSvcs sync.Map // *server -> *framing.Service

var storyIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// framingService returns the service for the requested grouping ("3" default,
// "5" = one block per leaning); nil when the trend provider cannot give story
// details.
func (s *server) framingService(bloques string) *framing.Service {
	if s.d.Trends == nil {
		return nil
	}
	v, ok := framingSvcs.Load(s)
	if !ok {
		svc := framing.New(s.d.Trends, FramingConfig)
		if svc.Detail == nil {
			return nil
		}
		v, _ = framingSvcs.LoadOrStore(s, svc)
	}
	svc := *v.(*framing.Service)
	if bloques == "5" {
		svc.Cfg.Blocks = framing.Blocks5
	}
	return &svc
}

func (s *server) framingAvailable() bool {
	if s.d.Trends == nil {
		return false
	}
	_, ok := s.d.Trends.(domain.StoryDetailProvider)
	return ok
}

func init() {
	registerPage("framing", "framing_story")
	registerNav(NavItem{Key: "framing", Label: "Encuadre", Href: "/encuadre", Order: 45,
		Desc: "Cómo cuentan una misma historia los medios y qué no aparece en algunos",
		Show: func(s *server) bool { return s.framingAvailable() },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4h5M4 4v5M20 4h-5M20 4v5M4 20h5M4 20v-5M20 20h-5M20 20v-5"/><path d="M9 12h6"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /encuadre", s.pageFraming)
		m.HandleFunc("GET /encuadre/{storyID}", s.pageFramingStory)
		m.HandleFunc("GET /api/framing/{storyID}", s.apiFraming)
		m.HandleFunc("GET /api/blindspots", s.apiBlindspots)
	})
}

// framingOpts: live (zero Now) so the engine's 5-minute cache is shared
// between the list and the detail page.
func framingOpts(q url.Values, window time.Duration) domain.TrendOptions {
	return domain.TrendOptions{Window: window, SourceIDs: cleanList(q["fuentes"])}
}

func bloquesParam(v string) string {
	if v == "5" {
		return "5"
	}
	return "3"
}

// ---- list ----

type framingStory struct {
	framing.StoryCoverage
	Bar       template.HTML
	DetailURL string
	Rows      []framingBlockRow
}

type framingBlockRow struct {
	framing.BlockCoverage
	Chart  template.HTML
	Names  string
	Absent bool
}

type framingView struct {
	Vista, Ventana, WindowLbl, Bloques string
	TabBlind, TabCompared              string
	Ventanas                           []ventOpt
	BloqueOpts                         []ventOpt
	Spots, Compared                    []framingStory
	Assessed, Total                    int
	Cfg                                framing.Config
	SkewPct                            int
	Version                            string
	Generated                          time.Time
	Err                                string
}

func framingLink(vista, ventana, bloques string) string {
	v := url.Values{}
	if vista == "comparadas" {
		v.Set("vista", vista)
	}
	if ventana != "" && ventana != defaultVentana {
		v.Set("ventana", ventana)
	}
	if bloques == "5" {
		v.Set("bloques", "5")
	}
	if len(v) == 0 {
		return "/encuadre"
	}
	return "/encuadre?" + v.Encode()
}

// rateBarSVG is a slim bar showing covered/total outlets of a block.
func rateBarSVG(covered, total int, class string) template.HTML {
	if total <= 0 {
		return ""
	}
	desc := fmt.Sprintf("%d de %d medios", covered, total)
	w := float64(covered) * 100 / float64(total)
	return template.HTML(fmt.Sprintf( // #nosec G203 -- numbers and a fixed class
		`<svg class="rbar" viewBox="0 0 100 8" preserveAspectRatio="none" role="img" aria-label="%s"><title>%s</title><rect class="bg" x="0" y="0" width="100" height="8"/><rect class="%s" x="0" y="0" width="%.1f" height="8"/></svg>`,
		desc, desc, class, w))
}

func blockClass(i, n int) string {
	if n == 3 {
		return [...]string{"b0", "b2", "b4"}[i%3]
	}
	return fmt.Sprintf("b%d", i%5)
}

func (s *server) storyCov(sc framing.StoryCoverage, vent string) framingStory {
	fs := framingStory{StoryCoverage: sc, Bar: leanBarSVG(sc.Story.Leanings),
		DetailURL: "/encuadre/" + url.PathEscape(sc.Story.ID) + ventSuffix(vent)}
	for i, bc := range sc.Blocks {
		fs.Rows = append(fs.Rows, framingBlockRow{BlockCoverage: bc, Chart: rateBarSVG(bc.Covered, bc.Total, blockClass(i, len(sc.Blocks))),
			Names: strings.Join(bc.Names, ", "), Absent: bc.Covered == 0 && bc.Total >= FramingConfig.MinBlockOutlets})
	}
	return fs
}

func ventSuffix(v string) string {
	if v == "" || v == defaultVentana {
		return ""
	}
	return "?ventana=" + v
}

func (s *server) pageFraming(w http.ResponseWriter, r *http.Request) {
	bl := bloquesParam(r.URL.Query().Get("bloques"))
	svc := s.framingService(bl)
	if svc == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Encuadre no disponible: el análisis de tendencias no está activado en este servidor.")
		return
	}
	q := r.URL.Query()
	key, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Ventana no válida: usa 6h, 24h, 48h o 7d.")
		return
	}
	vista := "ciegos"
	if q.Get("vista") == "comparadas" {
		vista = "comparadas"
	}
	v := framingView{Vista: vista, Ventana: key, Bloques: bl, Cfg: svc.Cfg, Version: s.o.Version, SkewPct: int(svc.Cfg.MinSkew*100 + 0.5),
		TabBlind: framingLink("ciegos", key, bl), TabCompared: framingLink("comparadas", key, bl)}
	for _, x := range ventanas {
		v.Ventanas = append(v.Ventanas, ventOpt{Key: x.Key, Label: x.Label, Short: strings.Replace(x.Label, " horas", " h", 1), URL: framingLink(vista, x.Key, bl), On: x.Key == key})
		if x.Key == key {
			v.WindowLbl = x.Label
		}
	}
	v.BloqueOpts = []ventOpt{{Key: "3", Short: "3 bloques", Label: "Izquierda · Centro · Derecha", URL: framingLink(vista, key, "3"), On: bl == "3"},
		{Key: "5", Short: "5 líneas", Label: "Cada línea editorial por separado", URL: framingLink(vista, key, "5"), On: bl == "5"}}
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	code := http.StatusOK
	rep, err := svc.BlindSpots(ctx, framingOpts(q, window))
	if err != nil {
		s.log.Error("framing: blindspots", "err", err)
		v.Err, code = "No se pudieron calcular las historias en este momento.", http.StatusInternalServerError
	} else {
		v.Generated, v.Total, v.Assessed = s.now(), rep.Stats.Stories, rep.Stats.Assessed
		for _, sc := range rep.Spots {
			v.Spots = append(v.Spots, s.storyCov(sc, key))
		}
		for _, sc := range rep.All {
			// compared: covered by at least two blocks
			n := 0
			for _, bc := range sc.Blocks {
				if bc.Covered > 0 {
					n++
				}
			}
			if n >= 2 {
				v.Compared = append(v.Compared, s.storyCov(sc, key))
			}
		}
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-History-Restore-Request") != "true" {
		w.WriteHeader(code)
		s.fragment(w, "framing", "framing_body", v)
		return
	}
	s.render(w, r, code, "framing", "Encuadre", "framing", v)
}

// ---- detail ----

type framingDetail struct {
	*framing.Framing
	Back, CompareURL, Ventana string
	Version                   string
	Bar                       template.HTML
	Cov                       framingStory
	Terms                     []framingTermBlock
	Rows                      []framingHeadline
	Ventanas                  []ventOpt
	Generated                 time.Time
}

type framingTermBlock struct {
	framing.BlockFraming
	Class string
	Empty bool
}

type framingHeadline struct {
	framing.HeadlineRow
	OmittedTxt string
}

func (s *server) pageFramingStory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bl := bloquesParam(q.Get("bloques"))
	svc := s.framingService(bl)
	if svc == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Encuadre no disponible: el análisis de tendencias no está activado en este servidor.")
		return
	}
	id := r.PathValue("storyID")
	if !storyIDRe.MatchString(id) {
		s.notFound(w, r)
		return
	}
	key, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Ventana no válida: usa 6h, 24h, 48h o 7d.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	f, err := svc.Framing(ctx, id, framingOpts(q, window))
	if err != nil {
		if errors.Is(err, framing.ErrNotFound) || isNotFound(err) {
			s.fail(w, r, http.StatusNotFound, "No encuentro esa historia en esta ventana de tiempo. Puede haber caducado: prueba con otra ventana desde la lista.")
			return
		}
		s.log.Error("framing: story", "id", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo calcular el encuadre en este momento.")
		return
	}
	v := framingDetail{Framing: f, Back: framingLink("ciegos", key, bl), Ventana: key, Version: s.o.Version, Bar: leanBarSVG(f.Story.Leanings),
		Cov: s.storyCov(f.Coverage, key), Generated: s.now(),
		CompareURL: askURL("Contrasta el enfoque que dan los medios de izquierda, centro y derecha a esta historia: «" + f.Story.Title + "»")}
	for i, b := range f.Blocks {
		v.Terms = append(v.Terms, framingTermBlock{BlockFraming: b, Class: blockClass(i, len(f.Blocks)), Empty: len(b.Terms) == 0})
	}
	for _, h := range f.Headlines {
		v.Rows = append(v.Rows, framingHeadline{HeadlineRow: h, OmittedTxt: strings.Join(h.Omitted, ", ")})
	}
	s.render(w, r, http.StatusOK, "framing_story", f.Story.Title, "framing", v)
}

// ---- API ----

type blockCovJSON struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Covered int      `json:"covered"`
	Total   int      `json:"total"`
	Rate    float64  `json:"rate"`
	Outlets []string `json:"outlets"`
	Missing []string `json:"missing"`
}

type storyCovJSON struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Sources   int            `json:"sources"`
	Articles  int            `json:"articles"`
	Leanings  map[string]int `json:"leanings"`
	FirstAt   *time.Time     `json:"first_at,omitempty"`
	Blocks    []blockCovJSON `json:"blocks"`
	Skew      float64        `json:"skew"`
	Assessed  bool           `json:"assessed"`
	Blind     bool           `json:"blind_spot"`
	Dominant  string         `json:"dominant,omitempty"`
	Absent    []string       `json:"absent"`
	Summary   string         `json:"summary"`
	Statement string         `json:"statement"`
}

func toStoryCovJSON(sc framing.StoryCoverage) storyCovJSON {
	j := storyCovJSON{ID: sc.Story.ID, Title: sc.Story.Title, Sources: sc.Story.Sources, Articles: sc.Story.Articles,
		Leanings: orEmptyMap(sc.Story.Leanings), FirstAt: tptr(sc.Story.FirstAt), Skew: sc.Skew, Assessed: sc.Assessed, Blind: sc.Blind,
		Absent: []string{}, Summary: sc.Summary, Statement: sc.Statement, Blocks: []blockCovJSON{}}
	for _, b := range sc.Blocks {
		j.Blocks = append(j.Blocks, blockCovJSON{b.Block.Key, b.Block.Name, b.Covered, b.Total, b.Rate, orEmptyStr(b.Names), orEmptyStr(b.Missing)})
	}
	if sc.Dominant >= 0 {
		j.Dominant = sc.Blocks[sc.Dominant].Block.Key
	}
	for _, i := range sc.Absent {
		j.Absent = append(j.Absent, sc.Blocks[i].Block.Key)
	}
	return j
}

func (s *server) apiBlindspots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc := s.framingService(bloquesParam(q.Get("bloques")))
	if svc == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Encuadre no disponible")
		return
	}
	_, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "ventana no válida (6h, 24h, 48h, 7d)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	rep, err := svc.BlindSpots(ctx, framingOpts(q, window))
	if err != nil {
		s.log.Error("api blindspots", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular los puntos ciegos")
		return
	}
	spots := make([]storyCovJSON, 0, len(rep.Spots))
	for _, sc := range rep.Spots {
		spots = append(spots, toStoryCovJSON(sc))
	}
	out := map[string]any{"generated_at": s.now(), "window_hours": window.Hours(), "stories_analysed": rep.Stats.Stories,
		"stories_assessed": rep.Stats.Assessed, "blind_spots": spots,
		"thresholds": map[string]any{"min_covered": svc.Cfg.MinCovered, "min_block_outlets": svc.Cfg.MinBlockOutlets,
			"min_skew": svc.Cfg.MinSkew, "min_dominant_rate": svc.Cfg.MinDominantRate}}
	if q.Get("todas") == "1" {
		all := make([]storyCovJSON, 0, len(rep.All))
		for _, sc := range rep.All {
			all = append(all, toStoryCovJSON(sc))
		}
		out["stories"] = all
	}
	writeJSON(w, 200, out)
}

func (s *server) apiFraming(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc := s.framingService(bloquesParam(q.Get("bloques")))
	if svc == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Encuadre no disponible")
		return
	}
	id := r.PathValue("storyID")
	if !storyIDRe.MatchString(id) {
		s.fail(w, r, http.StatusNotFound, "Historia no encontrada")
		return
	}
	_, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "ventana no válida (6h, 24h, 48h, 7d)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	f, err := svc.Framing(ctx, id, framingOpts(q, window))
	if err != nil {
		if errors.Is(err, framing.ErrNotFound) || isNotFound(err) {
			s.fail(w, r, http.StatusNotFound, "Historia no encontrada en esta ventana")
			return
		}
		s.log.Error("api framing", "id", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular el encuadre")
		return
	}
	type termJ struct {
		Term  string  `json:"term"`
		Z     float64 `json:"z"`
		Count int     `json:"count"`
		Other int     `json:"other_blocks_count"`
	}
	type blockJ struct {
		Key      string  `json:"key"`
		Name     string  `json:"name"`
		Articles int     `json:"articles"`
		Outlets  int     `json:"outlets"`
		Terms    []termJ `json:"terms"`
	}
	type wordJ struct {
		Text  string `json:"text"`
		Class string `json:"class"`
	}
	type headJ struct {
		SourceID  string     `json:"source_id"`
		Name      string     `json:"name"`
		Leaning   string     `json:"leaning,omitempty"`
		Block     string     `json:"block,omitempty"`
		Title     string     `json:"title"`
		URL       string     `json:"url"`
		At        *time.Time `json:"at,omitempty"`
		Words     []wordJ    `json:"words"`
		Omitted   []string   `json:"omitted"`
		More      int        `json:"more_headlines"`
		Consensus bool       `json:"consensus"`
	}
	blocks := []blockJ{}
	for _, b := range f.Blocks {
		bj := blockJ{Key: b.Block.Key, Name: b.Block.Name, Articles: b.Articles, Outlets: b.Outlets, Terms: []termJ{}}
		for _, t := range b.Terms {
			bj.Terms = append(bj.Terms, termJ{t.Text, t.Z, t.Count, t.Other})
		}
		blocks = append(blocks, bj)
	}
	heads := []headJ{}
	for _, h := range f.Headlines {
		hj := headJ{SourceID: h.SourceID, Name: h.Name, Leaning: h.Leaning, Title: h.Title, URL: h.URL, At: tptr(h.At),
			Words: []wordJ{}, Omitted: orEmptyStr(h.Omitted), More: h.More, Consensus: h.Consensus}
		if h.Block >= 0 {
			hj.Block = f.Blocks[h.Block].Block.Key
		}
		for _, wd := range h.Words {
			hj.Words = append(hj.Words, wordJ{wd.Text, wd.Class})
		}
		heads = append(heads, hj)
	}
	writeJSON(w, 200, map[string]any{
		"story":     map[string]any{"id": f.Story.ID, "title": f.Story.Title, "sources": f.Story.Sources, "articles": f.Story.Articles, "leanings": orEmptyMap(f.Story.Leanings), "first_at": tptr(f.Story.FirstAt)},
		"blocks":    blocks,
		"consensus": f.Consensus, "headlines": heads, "coverage": toStoryCovJSON(f.Coverage),
		"background_articles": f.Background, "note": f.Note,
		"method": "log-odds con prior informativo (Monroe, Colaresi y Quinn 2008) sobre titulares y resúmenes; descriptivo, sin IA",
	})
}
