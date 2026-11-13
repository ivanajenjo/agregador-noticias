package web

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	dateLayout    = "2006-01-02"
	maxQueryRunes = 300
	searchPerPage = 20
	maxSearchK    = 200
	maxPage       = 10
)

// ---- filters ----

type filterForm struct {
	Q        string
	Sources  []string
	Leanings []string
	From, To string
	Page     int
	Compare  bool
}

func cleanList(vs []string) []string {
	var out []string
	for _, v := range vs {
		v = strings.TrimSpace(v)
		if v != "" && len(v) <= 64 && len(out) < 50 {
			out = append(out, v)
		}
	}
	return out
}

func (s *server) parseFilter(v url.Values) (domain.Filter, filterForm) {
	f := filterForm{
		Q:        truncRunes(strings.TrimSpace(v.Get("q")), maxQueryRunes),
		Sources:  cleanList(v["source"]),
		Leanings: cleanList(v["leaning"]),
		Page:     1,
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n >= 1 && n <= maxPage {
		f.Page = n
	}
	flt := domain.Filter{SourceIDs: f.Sources, Leanings: f.Leanings}
	if t, err := time.ParseInLocation(dateLayout, v.Get("from"), s.loc); err == nil {
		f.From, flt.From = t.Format(dateLayout), t
	}
	if t, err := time.ParseInLocation(dateLayout, v.Get("to"), s.loc); err == nil {
		f.To = t.Format(dateLayout)
		flt.To = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, s.loc)
	}
	return flt, f
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

type srcOpt struct {
	domain.Source
	Checked bool
}
type leanOpt struct {
	Name    string
	Checked bool
}
type filterView struct {
	Sources  []srcOpt
	Leanings []leanOpt
	From, To string
	Active   int // number of active filters
}

func (s *server) filterView(ctx context.Context, f filterForm) filterView {
	fv := filterView{From: f.From, To: f.To}
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		s.log.Warn("list sources", "err", err)
	}
	in := func(list []string, x string) bool {
		for _, v := range list {
			if v == x {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	for _, so := range sortSources(src) {
		fv.Sources = append(fv.Sources, srcOpt{so, in(f.Sources, so.ID)})
		if l := so.Leaning; l != "" && !seen[l] {
			seen[l] = true
			fv.Leanings = append(fv.Leanings, leanOpt{l, in(f.Leanings, l)})
		}
	}
	fv.Active = len(f.Sources) + len(f.Leanings)
	if f.From != "" {
		fv.Active++
	}
	if f.To != "" {
		fv.Active++
	}
	return fv
}

func (s *server) sourceMap(ctx context.Context) map[string]domain.Source {
	m := map[string]domain.Source{}
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		s.log.Warn("list sources", "err", err)
	}
	for _, so := range src {
		m[so.ID] = so
	}
	return m
}

// ---- home ----

type homeCol struct {
	Source   domain.Source
	Articles []domain.Article
}
type homeView struct {
	Cols    []homeCol
	Updated time.Time
	Err     string
	// Digest teaser (empty when no digest exists).
	DigestLabel, DigestURL string
}

const homePerSource = 8

func (s *server) buildHome(ctx context.Context) homeView {
	v := homeView{Updated: s.now()}
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		s.log.Error("home: sources", "err", err)
		v.Err = "No se pudo leer la lista de medios."
		return v
	}
	for _, so := range sortSources(src) {
		arts, err := s.d.Store.ListArticles(ctx, domain.Filter{SourceIDs: []string{so.ID}, Limit: homePerSource})
		if err != nil {
			s.log.Warn("home: articles", "source", so.ID, "err", err)
			continue
		}
		v.Cols = append(v.Cols, homeCol{Source: so, Articles: arts})
	}
	return v
}

func (s *server) pageHome(w http.ResponseWriter, r *http.Request) {
	v := s.buildHome(r.Context())
	v.DigestLabel, v.DigestURL = s.digestHomeLink(r.Context())
	s.render(w, r, 200, "home", "Portada", "home", v)
}

func (s *server) fragHome(w http.ResponseWriter, r *http.Request) {
	s.fragment(w, "home", "home_cols", s.buildHome(r.Context()))
}

// ---- search ----

type hitView struct {
	Article    domain.Article
	SourceName string
	Leaning    string
	Score      float64
	Snippet    template.HTML
}

type searchView struct {
	Q        string
	Filter   filterView
	Searched bool
	Hits     []hitView
	Mode     string
	Page     int
	PrevURL  string
	NextURL  string
	Err      string
	Total    int
}

// searchPage runs Retriever.Search and returns the window for the page.
func (s *server) searchPage(ctx context.Context, q string, f domain.Filter, page, per int) (hits []domain.Hit, mode domain.SearchMode, more bool, err error) {
	if s.d.Retriever == nil {
		return nil, "", false, errors.New("retriever not configured")
	}
	k := page*per + 1
	if k > maxSearchK {
		k = maxSearchK
	}
	all, mode, err := s.d.Retriever.Search(ctx, q, f, k)
	if err != nil {
		return nil, mode, false, err
	}
	start := (page - 1) * per
	if start >= len(all) {
		return nil, mode, false, nil
	}
	end := start + per
	if end < len(all) {
		more = true
	} else {
		end = len(all)
	}
	return all[start:end], mode, more, nil
}

func (s *server) pageSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	flt, form := s.parseFilter(q)
	v := searchView{Q: form.Q, Page: form.Page, Filter: s.filterView(ctx, form)}
	if form.Q != "" {
		v.Searched = true
		hits, mode, more, err := s.searchPage(ctx, form.Q, flt, form.Page, searchPerPage)
		if err != nil {
			s.log.Warn("search", "err", err)
			v.Err = "La búsqueda no está disponible en este momento."
		} else {
			v.Mode = string(mode)
			sm := s.sourceMap(ctx)
			for _, h := range hits {
				v.Hits = append(v.Hits, s.toHitView(h, sm))
			}
			link := func(p int) string {
				c := url.Values{}
				for k, vs := range q {
					if k != "page" {
						c[k] = vs
					}
				}
				c.Set("page", strconv.Itoa(p))
				return "/buscar?" + c.Encode()
			}
			if form.Page > 1 {
				v.PrevURL = link(form.Page - 1)
			}
			if more && form.Page < maxPage {
				v.NextURL = link(form.Page + 1)
			}
		}
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-History-Restore-Request") != "true" {
		s.fragment(w, "search", "search_results", v)
		return
	}
	s.render(w, r, 200, "search", "Buscar", "search", v)
}

func (s *server) toHitView(h domain.Hit, sm map[string]domain.Source) hitView {
	so := sm[h.Article.SourceID]
	name := so.Name
	if name == "" {
		name = h.Article.SourceID
	}
	sn := h.Snippet
	if strings.TrimSpace(sn) == "" {
		sn = trunc(240, firstNonEmpty(h.Article.Summary, h.Article.Content))
	}
	return hitView{Article: h.Article, SourceName: name, Leaning: so.Leaning, Score: h.Score, Snippet: safeSnippet(sn)}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---- ask ----

type askView struct {
	Filter    filterView
	Available bool
	LLMDown   bool
	Detail    string
	Q         string
	Compare   bool
}

func (s *server) pageAsk(w http.ResponseWriter, r *http.Request) {
	_, form := s.parseFilter(r.URL.Query())
	cmp := r.URL.Query().Get("comparar")
	v := askView{Filter: s.filterView(r.Context(), form), Available: s.d.Retriever != nil, Q: form.Q,
		Compare: cmp == "1" || cmp == "true" || cmp == "on"}
	if s.d.LLM != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		chat, _ := s.d.LLM.LLMHealth(ctx)
		if !chat.OK {
			v.LLMDown, v.Detail = true, chat.Detail
		}
	}
	s.render(w, r, 200, "ask", "Preguntar", "ask", v)
}

// ---- screenshots ----

type shotView struct {
	Shot domain.Screenshot
	URL  string
	Time string
}
type shotRow struct {
	Source domain.Source
	Shots  []shotView
	Latest *shotView
}
type cmpCell struct {
	Source domain.Source
	Shot   *shotView
}
type shotsView struct {
	Date, Prev, Next, Today string
	DayLabel                string
	IsToday                 bool
	Mode                    string
	Hours                   []string
	Hour                    string
	Rows                    []shotRow
	Cells                   []cmpCell
	Total                   int
	Err                     string
	CanCapture              bool
}

func shotURL(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "/shots/" + strings.Join(parts, "/")
}

func (s *server) dayRange(v string) (day, from, to time.Time) {
	day, err := time.ParseInLocation(dateLayout, v, s.loc)
	if err != nil {
		n := s.now().In(s.loc)
		day = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
	}
	return day, day, day.AddDate(0, 0, 1)
}

func (s *server) pageShots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	day, from, to := s.dayRange(q.Get("date"))
	if q.Get("date") == "" {
		// Nothing captured yet today (e.g. before the first scheduled run):
		// show the most recent day that has captures instead of an empty page.
		if all, err := s.d.Store.ListScreenshots(ctx, "", from.AddDate(0, 0, -14), to); err == nil && len(all) > 0 {
			var latest time.Time
			for _, sh := range all {
				if sh.TakenAt.After(latest) {
					latest = sh.TakenAt
				}
			}
			if latest.Before(from) {
				l := latest.In(s.loc)
				day = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, s.loc)
				from, to = day, day.AddDate(0, 0, 1)
			}
		}
	}
	n := s.now().In(s.loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
	v := shotsView{
		Date: day.Format(dateLayout), Prev: day.AddDate(0, 0, -1).Format(dateLayout),
		Today: today.Format(dateLayout), DayLabel: s.longDate(day), IsToday: day.Equal(today),
		Mode: "grid", CanCapture: s.d.Shots != nil,
	}
	if !day.Before(today) {
		v.Next = ""
	} else {
		v.Next = day.AddDate(0, 0, 1).Format(dateLayout)
	}
	if q.Get("mode") == "compare" {
		v.Mode = "compare"
	}
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		s.log.Error("shots: sources", "err", err)
		v.Err = "No se pudo leer la lista de medios."
	}
	hourSet := map[string]bool{}
	for _, so := range sortSources(src) {
		shots, err := s.d.Store.ListScreenshots(ctx, so.ID, from, to)
		if err != nil {
			s.log.Warn("shots: list", "source", so.ID, "err", err)
			continue
		}
		sort.Slice(shots, func(i, j int) bool { return shots[i].TakenAt.Before(shots[j].TakenAt) })
		row := shotRow{Source: so}
		for _, sh := range shots {
			sv := shotView{Shot: sh, URL: shotURL(sh.Path), Time: sh.TakenAt.In(s.loc).Format("15:04")}
			row.Shots = append(row.Shots, sv)
			hourSet[sh.TakenAt.In(s.loc).Format("15")] = true
		}
		if len(row.Shots) > 0 {
			l := row.Shots[len(row.Shots)-1]
			row.Latest = &l
			v.Total += len(row.Shots)
		} else if !so.Screenshot {
			continue
		}
		v.Rows = append(v.Rows, row)
	}
	for h := range hourSet {
		v.Hours = append(v.Hours, h)
	}
	sort.Strings(v.Hours)
	if v.Mode == "compare" && len(v.Hours) > 0 {
		v.Hour = q.Get("hour")
		if !hourSet[v.Hour] {
			v.Hour = v.Hours[0]
		}
		for _, row := range v.Rows {
			c := cmpCell{Source: row.Source}
			for i := range row.Shots {
				if row.Shots[i].Shot.TakenAt.In(s.loc).Format("15") == v.Hour {
					c.Shot = &row.Shots[i]
				}
			}
			v.Cells = append(v.Cells, c)
		}
	}
	s.render(w, r, 200, "shots", "Capturas", "shots", v)
}

// ---- status ----

type feedRow struct {
	domain.FeedState
	Section string
}
type srcHealth struct {
	Source domain.Source
	Feeds  []feedRow
	Errors int
}
type statusView struct {
	Sources                       []srcHealth
	Jobs                          []domain.JobRun
	Stats                         *domain.Stats
	HaveLLM                       bool
	Chat                          domain.Health
	Embed                         domain.Health
	Running                       map[string]time.Time
	Last                          map[string]jobResult
	Version                       string
	CanIngest                     bool
	IngestRunning, CaptureRunning bool
	CanCapture                    bool
	Err                           string
}

func (s *server) buildStatus(ctx context.Context) statusView {
	v := statusView{Version: s.o.Version, CanIngest: s.d.Ingestor != nil, CanCapture: s.d.Shots != nil}
	snap := s.jobs.snapshot()
	v.Running, v.Last = snap.Running, snap.Last
	_, v.IngestRunning = snap.Running["ingest"]
	_, v.CaptureRunning = snap.Running["screenshots"]
	src, err := s.d.Store.ListSources(ctx)
	if err != nil {
		v.Err = "No se pudo leer el estado de las fuentes."
		s.log.Error("status: sources", "err", err)
	}
	states, err := s.d.Store.ListFeedStates(ctx)
	if err != nil {
		s.log.Warn("status: feed states", "err", err)
	}
	for _, so := range sortSources(src) {
		h := srcHealth{Source: so}
		secs := map[string]string{}
		for _, f := range so.Feeds {
			secs[f.URL] = f.Section
		}
		for _, st := range states {
			if st.SourceID == so.ID {
				h.Feeds = append(h.Feeds, feedRow{st, secs[st.FeedURL]})
				if st.LastError != "" && st.LastErrorAt.After(st.LastOK) {
					h.Errors++
				}
			}
		}
		v.Sources = append(v.Sources, h)
	}
	if jobs, err := s.d.Store.ListJobRuns(ctx, 20); err == nil {
		v.Jobs = jobs
	}
	if st, err := s.d.Store.Stats(ctx, s.o.EmbedModel); err == nil {
		v.Stats = st
	} else {
		s.log.Warn("status: stats", "err", err)
	}
	if s.d.LLM != nil {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		v.Chat, v.Embed = s.d.LLM.LLMHealth(c)
		v.HaveLLM = true
	}
	return v
}

func (s *server) pageStatus(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, 200, "status", "Estado", "status", s.buildStatus(r.Context()))
}
func (s *server) fragStatus(w http.ResponseWriter, r *http.Request) {
	s.fragment(w, "status", "status_body", s.buildStatus(r.Context()))
}

// ---- article ----

type articleView struct {
	A       domain.Article
	Source  domain.Source
	Paras   []string
	Related []hitView
	Revs    []revCard // historial de cambios (vacío si no hay)
}

func isNotFound(err error) bool {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "not found") || strings.Contains(m, "no rows") || strings.Contains(m, "no encontrad")
}

func (s *server) pageArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	a, err := s.d.Store.GetArticle(ctx, id)
	if err != nil && !isNotFound(err) {
		s.log.Error("article", "id", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer el artículo")
		return
	}
	if err != nil || a == nil {
		s.notFound(w, r)
		return
	}
	sm := s.sourceMap(ctx)
	v := articleView{A: *a, Source: sm[a.SourceID], Paras: paras(firstNonEmpty(a.Content, a.Summary))}
	if v.Source.Name == "" {
		v.Source.Name = a.SourceID
	}
	v.Revs = s.articleRevisions(ctx, a.ID, v.Source.Name)
	if s.d.Retriever != nil && strings.TrimSpace(a.Title) != "" {
		if hits, _, err := s.d.Retriever.Search(ctx, truncRunes(a.Title, maxQueryRunes), domain.Filter{}, 9); err == nil {
			for _, h := range hits {
				if h.Article.ID != a.ID && len(v.Related) < 6 {
					v.Related = append(v.Related, s.toHitView(h, sm))
				}
			}
		}
	}
	s.render(w, r, 200, "article", a.Title, "", v)
}

func (s *server) pageOffline(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, 200, "offline", "Sin conexión", "", nil)
}
