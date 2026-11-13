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
	"github.com/ivanajenjo/agregador-noticias/internal/revisions"
)

const (
	revTimeout   = 10 * time.Second
	revPageSize  = 30
	revMaxOffset = 5000
)

var revVentanas = []struct {
	Key, Label string
	D          time.Duration
}{
	{"24h", "24 horas", 24 * time.Hour},
	{"7d", "7 días", 7 * 24 * time.Hour},
	{"30d", "30 días", 30 * 24 * time.Hour},
}

const revDefaultVentana = "7d"

func init() {
	registerPage("revisions")
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /cambios", s.pageRevisions)
		m.HandleFunc("GET /api/revisions", s.apiRevisions)
	})
	registerNav(NavItem{Key: "revisions", Label: "Cambios", Href: "/cambios", Order: 46,
		Desc: "Titulares y resúmenes que los medios modifican después de publicar",
		Show: func(s *server) bool { _, ok := s.d.Store.(domain.RevisionStore); return ok },
		Icon: ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h11M4 12h7M4 17h5"/><path d="M15 15l5-5 2 2-5 5-3 1z"/></g>`)})
}

func (s *server) revStore() (domain.RevisionStore, bool) {
	rs, ok := s.d.Store.(domain.RevisionStore)
	return rs, ok
}

// ---- pure helpers ----

// diffHTML renders the word diff of old -> new as two escaped HTML fragments.
// The "before" side marks deleted words with <del>, the "after" side marks
// inserted words with <ins>. Besides colour/strike/underline, each marked run
// carries a visually hidden text cue ("eliminado:" / "añadido:") for screen
// readers and for non-visual contexts.
func diffHTML(oldText, newText string) (before, after template.HTML) {
	var b, a strings.Builder
	for _, sg := range revisions.WordDiff(oldText, newText) {
		t := template.HTMLEscapeString(sg.Text)
		switch sg.Op {
		case revisions.Equal:
			b.WriteString(t + " ")
			a.WriteString(t + " ")
		case revisions.Delete:
			b.WriteString(`<del><span class="sr">[eliminado: </span>` + t + `<span class="sr">]</span></del> `)
		case revisions.Insert:
			a.WriteString(`<ins><span class="sr">[añadido: </span>` + t + `<span class="sr">]</span></ins> `)
		}
	}
	return template.HTML(strings.TrimSpace(b.String())), template.HTML(strings.TrimSpace(a.String())) // #nosec G203 -- all text is escaped
}

// delayText formats the time between first seen and change.
func delayText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de 1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 día"
	}
	return fmt.Sprintf("%d días", days)
}

func kindLabel(k string) string {
	if k == domain.RevisionSummary {
		return "Resumen"
	}
	return "Titular"
}

// ---- view models ----

type revCard struct {
	ID         int64
	ArticleID  int64
	Kind       string
	KindLabel  string
	Before     template.HTML
	After      template.HTML
	Source     string
	Leaning    string
	ChangedAt  time.Time
	Delay      string
	ArticleURL string // internal page
	ExtURL     string
	Title      string
}

type revView struct {
	Ventana   string
	Ventanas  []ventOpt
	Tipo      string
	Tipos     []ventOpt
	Sources   []srcOpt
	Leanings  []leanOpt
	Fuentes   int
	Summary   string
	Total     int64
	Cards     []revCard
	NextURL   string
	Unsupport bool
}

func (s *server) revCardOf(e domain.RevisionEntry, sm map[string]domain.Source) revCard {
	so := sm[e.SourceID]
	if so.Name == "" {
		so.Name = e.SourceID
	}
	b, a := diffHTML(e.OldText, e.NewText)
	return revCard{ID: e.ID, ArticleID: e.ArticleID, Kind: e.Kind, KindLabel: kindLabel(e.Kind), Before: b, After: a,
		Source: so.Name, Leaning: so.Leaning, ChangedAt: e.ChangedAt, Delay: delayText(e.ChangedAt.Sub(e.SeenAt)),
		ArticleURL: "/articulo/" + strconv.FormatInt(e.ArticleID, 10), ExtURL: e.ArticleURL, Title: e.ArticleTitle}
}

func revLink(ventana, tipo string, fuentes, lineas []string, offset int) string {
	v := url.Values{}
	if ventana != "" && ventana != revDefaultVentana {
		v.Set("ventana", ventana)
	}
	if tipo != "" {
		v.Set("tipo", tipo)
	}
	for _, f := range fuentes {
		v.Add("fuentes", f)
	}
	for _, l := range lineas {
		v.Add("lineas", l)
	}
	if offset > 0 {
		v.Set("desde", strconv.Itoa(offset))
	}
	if len(v) == 0 {
		return "/cambios"
	}
	return "/cambios?" + v.Encode()
}

func (s *server) revFilter(q url.Values) (domain.RevisionFilter, string, string, []string, []string, bool) {
	key := q.Get("ventana")
	if key == "" {
		key = revDefaultVentana
	}
	var d time.Duration
	for _, x := range revVentanas {
		if x.Key == key {
			d = x.D
		}
	}
	if d == 0 {
		return domain.RevisionFilter{}, "", "", nil, nil, false
	}
	tipo := q.Get("tipo")
	if tipo != domain.RevisionTitle && tipo != domain.RevisionSummary {
		tipo = ""
	}
	fu, li := cleanList(q["fuentes"]), cleanList(q["lineas"])
	now := s.now()
	return domain.RevisionFilter{From: now.Add(-d), To: now, SourceIDs: fu, Leanings: li, Kind: tipo}, key, tipo, fu, li, true
}

// revSummary builds the header sentence.
func (s *server) revSummary(ctx context.Context, rs domain.RevisionStore, f domain.RevisionFilter, ventLabel string, sm map[string]domain.Source) (string, int64) {
	tf := f
	tf.Kind = domain.RevisionTitle
	n, _ := rs.CountRevisions(ctx, tf)
	if n == 0 {
		return "", 0
	}
	word := "cambios de titular"
	if n == 1 {
		word = "cambio de titular"
	}
	out := fmt.Sprintf("%d %s en %s", n, word, ventLabel)
	by, _ := rs.RevisionsBySource(ctx, tf)
	var parts []string
	for i, c := range by {
		if i >= 3 {
			break
		}
		name := sm[c.SourceID].Name
		if name == "" {
			name = c.SourceID
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", name, c.Count))
	}
	if len(parts) > 0 {
		out += "; medios que más editan: " + strings.Join(parts, ", ")
	}
	return out, n
}

func (s *server) pageRevisions(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.revStore()
	if !ok {
		s.fail(w, r, http.StatusServiceUnavailable, "Cambios de titulares no disponible en este servidor.")
		return
	}
	q := r.URL.Query()
	f, key, tipo, fu, li, ok := s.revFilter(q)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Ventana no válida: usa 24h, 7d o 30d.")
		return
	}
	off := intParam(r, "desde", 0, 0, revMaxOffset)
	ctx, cancel := context.WithTimeout(r.Context(), revTimeout)
	defer cancel()

	v := revView{Ventana: key, Tipo: tipo}
	label := ""
	for _, x := range revVentanas {
		v.Ventanas = append(v.Ventanas, ventOpt{Key: x.Key, Label: x.Label, Short: x.Label, URL: revLink(x.Key, tipo, fu, li, 0), On: x.Key == key})
		if x.Key == key {
			label = x.Label
		}
	}
	for _, t := range []struct{ K, L string }{{"", "Todo"}, {domain.RevisionTitle, "Titulares"}, {domain.RevisionSummary, "Resúmenes"}} {
		v.Tipos = append(v.Tipos, ventOpt{Key: t.K, Label: t.L, Short: t.L, URL: revLink(key, t.K, fu, li, 0), On: t.K == tipo})
	}
	sm := s.sourceMap(ctx)
	selS, selL := map[string]bool{}, map[string]bool{}
	for _, x := range fu {
		selS[x] = true
	}
	for _, x := range li {
		selL[x] = true
	}
	seenL := map[string]bool{}
	if src, err := s.d.Store.ListSources(ctx); err == nil {
		for _, so := range sortSources(src) {
			v.Sources = append(v.Sources, srcOpt{so, selS[so.ID]})
			if selS[so.ID] {
				v.Fuentes++
			}
			if l := strings.TrimSpace(so.Leaning); l != "" && !seenL[l] {
				seenL[l] = true
				v.Leanings = append(v.Leanings, leanOpt{l, selL[l]})
				if selL[l] {
					v.Fuentes++
				}
			}
		}
	}
	v.Summary, v.Total = s.revSummary(ctx, rs, f, label, sm)

	lf := f
	lf.Limit, lf.Offset = revPageSize+1, off
	ents, err := rs.RecentRevisions(ctx, lf)
	if err != nil {
		s.log.Error("revisions", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudieron leer los cambios en este momento.")
		return
	}
	if len(ents) > revPageSize {
		ents = ents[:revPageSize]
		if off+revPageSize <= revMaxOffset {
			v.NextURL = revLink(key, tipo, fu, li, off+revPageSize)
		}
	}
	for _, e := range ents {
		v.Cards = append(v.Cards, s.revCardOf(e, sm))
	}
	s.render(w, r, http.StatusOK, "revisions", "Titulares modificados", "revisions", v)
}

// articleRevisions returns the history of one article for its page (nil if
// unsupported, empty or on error).
func (s *server) articleRevisions(ctx context.Context, articleID int64, sourceName string) []revCard {
	rs, ok := s.revStore()
	if !ok {
		return nil
	}
	revs, err := rs.ListRevisions(ctx, articleID)
	if err != nil {
		s.log.Warn("article revisions", "id", articleID, "err", err)
		return nil
	}
	var out []revCard
	for _, rv := range revs {
		b, a := diffHTML(rv.OldText, rv.NewText)
		out = append(out, revCard{ID: rv.ID, ArticleID: articleID, Kind: rv.Kind, KindLabel: kindLabel(rv.Kind), Before: b, After: a,
			Source: sourceName, ChangedAt: rv.ChangedAt, Delay: delayText(rv.ChangedAt.Sub(rv.SeenAt))})
	}
	return out
}

// ---- API ----

type revJSON struct {
	ID           int64     `json:"id"`
	ArticleID    int64     `json:"article_id"`
	SourceID     string    `json:"source_id"`
	Source       string    `json:"source"`
	Kind         string    `json:"kind"`
	OldText      string    `json:"old_text"`
	NewText      string    `json:"new_text"`
	ChangedAt    time.Time `json:"changed_at"`
	SeenAt       time.Time `json:"seen_at"`
	DelaySeconds int64     `json:"delay_seconds"`
	ArticleTitle string    `json:"article_title"`
	ArticleURL   string    `json:"article_url"`
}

func (s *server) apiRevisions(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.revStore()
	if !ok {
		s.fail(w, r, http.StatusServiceUnavailable, "Cambios de titulares no disponible en este servidor.")
		return
	}
	f, _, _, _, _, ok := s.revFilter(r.URL.Query())
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Ventana no válida: usa 24h, 7d o 30d.")
		return
	}
	f.Limit = intParam(r, "limit", 30, 1, 100)
	f.Offset = intParam(r, "offset", 0, 0, 100000)
	ents, err := rs.RecentRevisions(r.Context(), f)
	if err != nil {
		s.log.Error("api revisions", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al listar cambios")
		return
	}
	total, _ := rs.CountRevisions(r.Context(), f)
	sm := s.sourceMap(r.Context())
	out := make([]revJSON, 0, len(ents))
	for _, e := range ents {
		out = append(out, revJSON{ID: e.ID, ArticleID: e.ArticleID, SourceID: e.SourceID, Source: sm[e.SourceID].Name, Kind: e.Kind,
			OldText: e.OldText, NewText: e.NewText, ChangedAt: e.ChangedAt, SeenAt: e.SeenAt,
			DelaySeconds: int64(e.ChangedAt.Sub(e.SeenAt).Seconds()), ArticleTitle: e.ArticleTitle, ArticleURL: e.ArticleURL})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "revisions": out})
}
