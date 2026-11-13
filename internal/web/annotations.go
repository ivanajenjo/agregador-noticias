package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	annoTimeout    = 10 * time.Second
	annoDefaultDay = 7
	annoDefaultMin = 0.6
	annoMaxMin     = 0.95
	annoSmallN     = 10 // fewer annotated articles than this => flagged as small sample
	annoListLimit  = 60
)

var annoDims = []struct {
	Kind, Title, Short string
	Vocab              []string
}{
	{domain.AnnoTone, "Tono", "tono", domain.AnnoTones},
	{domain.AnnoIntent, "Intención", "intención", domain.AnnoIntents},
	{domain.AnnoFrame, "Encuadre", "encuadre", domain.AnnoFrames},
}

func annoDim(kind string) (title string, vocab []string, ok bool) {
	for _, d := range annoDims {
		if d.Kind == kind {
			return d.Title, d.Vocab, true
		}
	}
	return "", nil, false
}

func init() {
	registerPage("annotations", "annotations_list")
	registerNav(NavItem{Key: "annotations", Label: "Tono", Href: "/anotaciones", Order: 50,
		Desc: "Tono, intención y encuadre por medio y línea editorial (estimaciones de un modelo)",
		Show: func(s *server) bool { return s.annoVisible() },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/></g>`)})
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /anotaciones", s.pageAnnotations)
		m.HandleFunc("GET /anotaciones/lista", s.pageAnnotationList)
		m.HandleFunc("GET /fragmentos/anotaciones/{id}", s.fragAnnotationChips)
		m.HandleFunc("GET /api/annotations/summary", s.apiAnnotationsSummary)
	})
}

// ---- visibility ----

type annoCacheEntry struct {
	at  time.Time
	has bool
}

var (
	annoCacheMu sync.Mutex
	annoCache   = map[*server]annoCacheEntry{}
)

// annoVisible: the section exists when a store is wired and either the
// feature is enabled or annotations are already stored (history stays readable).
func (s *server) annoVisible() bool {
	if s.d.Annotations == nil {
		return false
	}
	if s.d.AnnotationsEnabled {
		return true
	}
	annoCacheMu.Lock()
	e, ok := annoCache[s]
	annoCacheMu.Unlock()
	if ok && time.Since(e.at) < time.Minute {
		return e.has
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ms, err := s.d.Annotations.AnnotationModels(ctx, domain.AnnoTone)
	has := err == nil && len(ms) > 0
	annoCacheMu.Lock()
	annoCache[s] = annoCacheEntry{at: time.Now(), has: has}
	annoCacheMu.Unlock()
	return has
}

// annoModel resolves the model to show: ?modelo= if known, else the configured
// one, else the model with most rows.
func (s *server) annoModel(ctx context.Context, want string) (model string, all []domain.AnnotationModelInfo) {
	all, _ = s.d.Annotations.AnnotationModels(ctx, domain.AnnoTone)
	if want != "" {
		for _, m := range all {
			if m.Model == want {
				return want, all
			}
		}
	}
	if s.d.AnnotationModel != "" {
		return s.d.AnnotationModel, all
	}
	if len(all) > 0 {
		return all[0].Model, all
	}
	return "", all
}

// ---- parameters ----

type annoParams struct {
	Days  int
	Conf  float64
	Model string
}

func parseAnnoParams(r *http.Request) annoParams {
	p := annoParams{Days: annoDefaultDay, Conf: annoDefaultMin, Model: r.URL.Query().Get("modelo")}
	if r.URL.Query().Get("dias") == "30" {
		p.Days = 30
	}
	if v, err := strconv.ParseFloat(r.URL.Query().Get("conf"), 64); err == nil && v >= 0 && !math.IsNaN(v) {
		p.Conf = math.Min(math.Round(v*100)/100, annoMaxMin)
	}
	return p
}

func (p annoParams) query(model string) url.Values {
	q := url.Values{}
	q.Set("dias", strconv.Itoa(p.Days))
	q.Set("conf", strconv.FormatFloat(p.Conf, 'f', -1, 64))
	if model != "" {
		q.Set("modelo", model)
	}
	return q
}

// ---- aggregation ----

// annoRow is one bar: a label->count distribution for an outlet or leaning.
type annoRow struct {
	Key     string // source id or leaning name
	Name    string
	Leaning string
	N       int64
	Counts  map[string]int64
}

type annoDimData struct {
	Kind, Title string
	Vocab       []string
	Leanings    []annoRow
	Outlets     []annoRow
	Total       int64
}

type annoSummary struct {
	Model      string
	Days       int
	MinConf    float64
	Articles   int64 // articles in the window (annotated or not)
	Dims       []annoDimData
	Annotated  int64 // distinct-ish: rows of tone kind above min confidence
	GeneratedA time.Time
}

func (s *server) buildAnnoSummary(ctx context.Context, model string, p annoParams) (*annoSummary, error) {
	now := s.now()
	from := now.AddDate(0, 0, -p.Days)
	sm := s.sourceMap(ctx)
	out := &annoSummary{Model: model, Days: p.Days, MinConf: p.Conf, GeneratedA: now}
	if n, err := s.d.Store.CountArticles(ctx, domain.Filter{From: from}); err == nil {
		out.Articles = n
	}
	for _, d := range annoDims {
		cs, err := s.d.Annotations.AnnotationDistribution(ctx, domain.AnnotationQuery{Kind: d.Kind, Model: model, From: from, MinScore: p.Conf})
		if err != nil {
			return nil, err
		}
		valid := map[string]bool{}
		for _, v := range d.Vocab {
			valid[v] = true
		}
		data := annoDimData{Kind: d.Kind, Title: d.Title, Vocab: d.Vocab}
		bySrc := map[string]*annoRow{}
		byLean := map[int]*annoRow{}
		for _, c := range cs {
			if !valid[c.Label] || c.N <= 0 {
				continue // labels outside the vocabulary never reach the charts
			}
			so, known := sm[c.SourceID]
			name := so.Name
			if !known || name == "" {
				name = c.SourceID
			}
			r := bySrc[c.SourceID]
			if r == nil {
				r = &annoRow{Key: c.SourceID, Name: name, Leaning: so.Leaning, Counts: map[string]int64{}}
				bySrc[c.SourceID] = r
			}
			r.Counts[c.Label] += c.N
			r.N += c.N
			rk := leaningRank(so.Leaning)
			l := byLean[rk]
			if l == nil {
				l = &annoRow{Key: leanNames[rk], Name: leanNames[rk], Counts: map[string]int64{}}
				byLean[rk] = l
			}
			l.Counts[c.Label] += c.N
			l.N += c.N
			data.Total += c.N
		}
		for _, r := range bySrc {
			data.Outlets = append(data.Outlets, *r)
		}
		sort.Slice(data.Outlets, func(i, j int) bool {
			ri, rj := leaningRank(data.Outlets[i].Leaning), leaningRank(data.Outlets[j].Leaning)
			if ri != rj {
				return ri < rj
			}
			return data.Outlets[i].Name < data.Outlets[j].Name
		})
		for i := range leanNames {
			if l := byLean[i]; l != nil {
				data.Leanings = append(data.Leanings, *l)
			}
		}
		if d.Kind == domain.AnnoTone {
			out.Annotated = data.Total
		}
		out.Dims = append(out.Dims, data)
	}
	return out, nil
}

// ---- view ----

type annoSeg struct {
	Label string
	Class string
	N     int64
	Pct   float64
	Href  string
}

type annoBar struct {
	Name, Leaning string
	N             int64
	Small         bool
	Segs          []annoSeg
	Alt           string
}

type annoDimView struct {
	Kind, Title string
	Legend      []annoLegend
	Leanings    []annoBar
	Outlets     []annoBar
	Total       int64
}

type annoLegend struct {
	Label, Class string
	N            int64
}

type annoModelOpt struct {
	Name  string
	Count int64
	On    bool
}

type annoView struct {
	Model      string
	Models     []annoModelOpt
	Days       int
	Conf       int // percent, for the slider
	ConfVal    string
	Dims       []annoDimView
	Annotated  int64
	Articles   int64
	Coverage   int // percent
	Windows    []annoWindow
	Empty      bool
	Enabled    bool
	Err        string
	ConfDesc   string
	HiddenArgs url.Values
}

type annoWindow struct {
	Label string
	URL   string
	On    bool
}

func (s *server) annoBars(kind string, vocab []string, rows []annoRow, p annoParams, model string, leaning bool) []annoBar {
	out := make([]annoBar, 0, len(rows))
	for _, r := range rows {
		b := annoBar{Name: r.Name, Leaning: r.Leaning, N: r.N, Small: r.N < annoSmallN}
		var alt []string
		for i, l := range vocab {
			n := r.Counts[l]
			if n == 0 {
				continue
			}
			q := p.query(model)
			q.Set("kind", kind)
			q.Set("label", l)
			if leaning {
				q.Set("leaning", r.Key)
			} else {
				q.Set("source", r.Key)
			}
			pct := float64(n) * 100 / float64(r.N)
			b.Segs = append(b.Segs, annoSeg{Label: l, Class: "a" + strconv.Itoa(i), N: n, Pct: pct, Href: "/anotaciones/lista?" + q.Encode()})
			alt = append(alt, fmt.Sprintf("%s %d (%.0f %%)", l, n, pct))
		}
		b.Alt = strings.Join(alt, ", ")
		out = append(out, b)
	}
	return out
}

func (s *server) pageAnnotations(w http.ResponseWriter, r *http.Request) {
	if !s.annoVisible() {
		s.fail(w, r, http.StatusServiceUnavailable, "Las anotaciones no están activadas")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), annoTimeout)
	defer cancel()
	p := parseAnnoParams(r)
	model, models := s.annoModel(ctx, p.Model)
	v := annoView{Model: model, Days: p.Days, Conf: int(math.Round(p.Conf * 100)), ConfVal: strconv.FormatFloat(p.Conf, 'f', -1, 64), Enabled: s.d.AnnotationsEnabled}
	for _, m := range models {
		v.Models = append(v.Models, annoModelOpt{Name: m.Model, Count: m.Count, On: m.Model == model})
	}
	for _, d := range []int{7, 30} {
		q := p.query(model)
		q.Set("dias", strconv.Itoa(d))
		v.Windows = append(v.Windows, annoWindow{Label: fmt.Sprintf("%d días", d), URL: "/anotaciones?" + q.Encode(), On: d == p.Days})
	}
	if model == "" || len(models) == 0 {
		v.Empty = true
		s.render(w, r, http.StatusOK, "annotations", "Tono e intención", "annotations", v)
		return
	}
	sum, err := s.buildAnnoSummary(ctx, model, p)
	if err != nil {
		s.log.Error("annotations summary", "err", err)
		v.Err = "No se pudieron calcular las distribuciones."
		s.render(w, r, http.StatusOK, "annotations", "Tono e intención", "annotations", v)
		return
	}
	v.Annotated, v.Articles = sum.Annotated, sum.Articles
	if sum.Articles > 0 {
		v.Coverage = int(math.Min(100, float64(sum.Annotated)*100/float64(sum.Articles)))
	}
	for _, d := range sum.Dims {
		dv := annoDimView{Kind: d.Kind, Title: d.Title, Total: d.Total}
		tot := map[string]int64{}
		for _, o := range d.Outlets {
			for l, n := range o.Counts {
				tot[l] += n
			}
		}
		for i, l := range d.Vocab {
			dv.Legend = append(dv.Legend, annoLegend{Label: l, Class: "a" + strconv.Itoa(i), N: tot[l]})
		}
		dv.Leanings = s.annoBars(d.Kind, d.Vocab, d.Leanings, p, model, true)
		dv.Outlets = s.annoBars(d.Kind, d.Vocab, d.Outlets, p, model, false)
		v.Dims = append(v.Dims, dv)
	}
	s.render(w, r, http.StatusOK, "annotations", "Tono e intención", "annotations", v)
}

// ---- list of articles behind a segment ----

type annoListItem struct {
	A      domain.Article
	Source string
	Lean   string
	Score  int
}

type annoListView struct {
	Title, Back string
	Items       []annoListItem
	Model       string
	Conf        int
}

func (s *server) pageAnnotationList(w http.ResponseWriter, r *http.Request) {
	if !s.annoVisible() {
		s.fail(w, r, http.StatusServiceUnavailable, "Las anotaciones no están activadas")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), annoTimeout)
	defer cancel()
	q := r.URL.Query()
	kind, label := q.Get("kind"), q.Get("label")
	dimTitle, vocab, ok := annoDim(kind)
	valid := false
	for _, v := range vocab {
		valid = valid || v == label
	}
	if !ok || !valid {
		s.fail(w, r, http.StatusBadRequest, "Etiqueta no válida")
		return
	}
	p := parseAnnoParams(r)
	model, _ := s.annoModel(ctx, p.Model)
	sm := s.sourceMap(ctx)
	aq := domain.AnnotationQuery{Kind: kind, Model: model, Label: label, From: s.now().AddDate(0, 0, -p.Days), MinScore: p.Conf, Limit: annoListLimit}
	if src := q.Get("source"); src != "" {
		aq.SourceIDs = []string{src}
	} else if ln := q.Get("leaning"); ln != "" {
		for id, so := range sm {
			if leanNames[leaningRank(so.Leaning)] == ln {
				aq.SourceIDs = append(aq.SourceIDs, id)
			}
		}
		sort.Strings(aq.SourceIDs)
		if len(aq.SourceIDs) == 0 {
			aq.SourceIDs = []string{"\x00none"}
		}
	}
	rows, err := s.d.Annotations.ListAnnotated(ctx, aq)
	if err != nil {
		s.log.Error("annotation list", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer la lista")
		return
	}
	title := fmt.Sprintf("%s: %s", dimTitle, label)
	if src := q.Get("source"); src != "" {
		n := sm[src].Name
		if n == "" {
			n = src
		}
		title += " · " + n
	} else if ln := q.Get("leaning"); ln != "" {
		title += " · " + ln
	}
	v := annoListView{Title: title, Model: model, Conf: int(math.Round(p.Conf * 100)), Back: "/anotaciones?" + p.query(model).Encode()}
	for _, x := range rows {
		so := sm[x.Article.SourceID]
		name := so.Name
		if name == "" {
			name = x.Article.SourceID
		}
		v.Items = append(v.Items, annoListItem{A: x.Article, Source: name, Lean: so.Leaning, Score: int(math.Round(x.Score * 100))})
	}
	s.render(w, r, http.StatusOK, "annotations_list", title, "annotations", v)
}

// ---- chips on the article page ----

type annoChip struct {
	Name, Label, Tip string
	Low              bool
}

type annoChipsView struct {
	Chips []annoChip
	Model string
}

func (s *server) fragAnnotationChips(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || s.d.Annotations == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), annoTimeout)
	defer cancel()
	model, _ := s.annoModel(ctx, "")
	rows, err := s.d.Annotations.ListAnnotations(ctx, id)
	if err != nil || model == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	by := map[string]domain.Annotation{}
	for _, a := range rows {
		if a.Model == model {
			by[a.Kind] = a
		}
	}
	v := annoChipsView{Model: model}
	add := func(kind, name string) {
		a, ok := by[kind]
		if !ok {
			return
		}
		var d struct {
			Rationale string `json:"rationale"`
		}
		_ = json.Unmarshal([]byte(a.Data), &d)
		tip := fmt.Sprintf("Confianza %d %%", int(math.Round(a.Score*100)))
		if d.Rationale != "" {
			tip += " · «" + d.Rationale + "»"
		}
		tip += " · modelo " + a.Model + " · estimación automática, puede equivocarse"
		v.Chips = append(v.Chips, annoChip{Name: name, Label: a.Label, Tip: tip, Low: a.Score < annoDefaultMin})
	}
	add(domain.AnnoTone, "Tono")
	add(domain.AnnoIntent, "Intención")
	add(domain.AnnoFrame, "Encuadre")
	if o, ok := by[domain.AnnoOpinion]; ok && o.Label == "sí" {
		add(domain.AnnoOpinion, "Opinión")
	}
	if len(v.Chips) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.fragment(w, "annotations", "anno_chips", v)
}

// ---- API ----

type annoRowJSON struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Leaning string           `json:"leaning,omitempty"`
	N       int64            `json:"n"`
	Counts  map[string]int64 `json:"counts"`
}

type annoDimJSON struct {
	Kind     string        `json:"kind"`
	Total    int64         `json:"total"`
	Leanings []annoRowJSON `json:"by_leaning"`
	Outlets  []annoRowJSON `json:"by_outlet"`
}

func rowsJSON(rs []annoRow) []annoRowJSON {
	out := make([]annoRowJSON, 0, len(rs))
	for _, r := range rs {
		out = append(out, annoRowJSON{ID: r.Key, Name: r.Name, Leaning: r.Leaning, N: r.N, Counts: r.Counts})
	}
	return out
}

func (s *server) apiAnnotationsSummary(w http.ResponseWriter, r *http.Request) {
	if !s.annoVisible() {
		s.fail(w, r, http.StatusServiceUnavailable, "Las anotaciones no están activadas")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), annoTimeout)
	defer cancel()
	p := parseAnnoParams(r)
	model, _ := s.annoModel(ctx, p.Model)
	if model == "" {
		writeJSON(w, http.StatusOK, map[string]any{"model": "", "days": p.Days, "min_confidence": p.Conf, "dimensions": []any{}})
		return
	}
	sum, err := s.buildAnnoSummary(ctx, model, p)
	if err != nil {
		s.log.Error("annotations api", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo calcular el resumen")
		return
	}
	dims := make([]annoDimJSON, 0, len(sum.Dims))
	for _, d := range sum.Dims {
		dims = append(dims, annoDimJSON{Kind: d.Kind, Total: d.Total, Leanings: rowsJSON(d.Leanings), Outlets: rowsJSON(d.Outlets)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model": model, "days": p.Days, "min_confidence": p.Conf,
		"articles_in_window": sum.Articles, "annotated": sum.Annotated,
		"disclaimer": "Estimaciones automáticas de un modelo pequeño; no son hechos.",
		"dimensions": dims,
	})
}
