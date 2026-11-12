package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type articleJSON struct {
	ID          int64      `json:"id"`
	SourceID    string     `json:"source_id"`
	Source      string     `json:"source"`
	Leaning     string     `json:"leaning,omitempty"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary,omitempty"`
	Section     string     `json:"section,omitempty"`
	Author      string     `json:"author,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
}

func toArticleJSON(a domain.Article, sm map[string]domain.Source) articleJSON {
	so := sm[a.SourceID]
	name := so.Name
	if name == "" {
		name = a.SourceID
	}
	j := articleJSON{ID: a.ID, SourceID: a.SourceID, Source: name, Leaning: so.Leaning, URL: a.URL, Title: a.Title,
		Summary: a.Summary, Section: a.Section, Author: a.Author, FirstSeenAt: a.FirstSeenAt}
	if !a.PublishedAt.IsZero() {
		t := a.PublishedAt
		j.PublishedAt = &t
	}
	return j
}

func intParam(r *http.Request, key string, def, min, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func (s *server) apiArticles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	flt, _ := s.parseFilter(r.URL.Query())
	flt.Limit = intParam(r, "limit", 30, 1, 100)
	flt.Offset = intParam(r, "offset", 0, 0, 100000)
	arts, err := s.d.Store.ListArticles(ctx, flt)
	if err != nil {
		s.log.Error("api articles", "err", err)
		s.fail(w, r, 500, "Error al listar artículos")
		return
	}
	total, _ := s.d.Store.CountArticles(ctx, flt)
	sm := s.sourceMap(ctx)
	out := make([]articleJSON, 0, len(arts))
	for _, a := range arts {
		out = append(out, toArticleJSON(a, sm))
	}
	writeJSON(w, 200, map[string]any{"total": total, "articles": out})
}

type hitJSON struct {
	articleJSON
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

func (s *server) apiSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	flt, form := s.parseFilter(r.URL.Query())
	if form.Q == "" {
		s.fail(w, r, 400, "Falta el parámetro q")
		return
	}
	if len([]rune(r.URL.Query().Get("q"))) > maxQueryRunes {
		s.fail(w, r, 400, "Consulta demasiado larga")
		return
	}
	per := intParam(r, "limit", 10, 1, 50)
	hits, mode, more, err := s.searchPage(ctx, form.Q, flt, form.Page, per)
	if err != nil {
		s.log.Warn("api search", "err", err)
		s.fail(w, r, 503, "Búsqueda no disponible")
		return
	}
	sm := s.sourceMap(ctx)
	out := make([]hitJSON, 0, len(hits))
	for _, h := range hits {
		out = append(out, hitJSON{toArticleJSON(h.Article, sm), h.Score, plainSnippet(h.Snippet)})
	}
	writeJSON(w, 200, map[string]any{"mode": mode, "page": form.Page, "more": more, "hits": out})
}

type shotJSON struct {
	ID       int64     `json:"id"`
	SourceID string    `json:"source_id"`
	TakenAt  time.Time `json:"taken_at"`
	URL      string    `json:"url"`
	Width    int       `json:"width,omitempty"`
	Height   int       `json:"height,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
}

func (s *server) apiScreenshots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	_, from, to := s.dayRange(q.Get("date"))
	var ids []string
	if id := strings.TrimSpace(q.Get("source")); id != "" && len(id) <= 64 {
		ids = []string{id}
	} else {
		src, err := s.d.Store.ListSources(ctx)
		if err != nil {
			s.fail(w, r, 500, "Error al listar medios")
			return
		}
		for _, so := range src {
			ids = append(ids, so.ID)
		}
	}
	out := []shotJSON{}
	for _, id := range ids {
		shots, err := s.d.Store.ListScreenshots(ctx, id, from, to)
		if err != nil {
			s.log.Warn("api shots", "source", id, "err", err)
			continue
		}
		for _, sh := range shots {
			out = append(out, shotJSON{sh.ID, sh.SourceID, sh.TakenAt, shotURL(sh.Path), sh.Width, sh.Height, sh.Bytes})
		}
	}
	writeJSON(w, 200, map[string]any{"date": from.Format(dateLayout), "screenshots": out})
}

// ---- ask (SSE) ----

type askBody struct {
	Question string   `json:"question"`
	Compare  bool     `json:"compare"`
	Sources  []string `json:"sources"`
	Leanings []string `json:"leanings"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	K        int      `json:"k"`
}

type sourceJSON struct {
	N         int    `json:"n"`
	ArticleID int64  `json:"article_id"`
	Title     string `json:"title"`
	SourceID  string `json:"source_id"`
	Source    string `json:"source"`
	Leaning   string `json:"leaning,omitempty"`
	URL       string `json:"url"`
	Section   string `json:"section,omitempty"`
	Published string `json:"published,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
}

type sseWriter struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

var sseNL = strings.NewReplacer("\r\n", "\n", "\r", "\n")

func (e *sseWriter) event(name, data string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	b.WriteString("event: " + name + "\n")
	for _, l := range strings.Split(sseNL.Replace(data), "\n") {
		b.WriteString("data: " + l + "\n")
	}
	b.WriteString("\n")
	if _, err := e.w.Write([]byte(b.String())); err != nil {
		return err
	}
	return e.rc.Flush()
}

func (e *sseWriter) comment() {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write([]byte(": ping\n\n"))
	_ = e.rc.Flush()
}

const maxQuestionRunes = 2000

func (s *server) apiAsk(w http.ResponseWriter, r *http.Request) {
	if s.d.Retriever == nil {
		s.fail(w, r, 503, "Búsqueda y preguntas no disponibles")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var b askBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.fail(w, r, 400, "JSON inválido")
		return
	}
	b.Question = strings.TrimSpace(b.Question)
	if b.Question == "" {
		s.fail(w, r, 400, "Falta la pregunta")
		return
	}
	if len([]rune(b.Question)) > maxQuestionRunes {
		s.fail(w, r, 400, "Pregunta demasiado larga")
		return
	}
	k := b.K
	if k <= 0 {
		k = 8
	}
	if k > 20 {
		k = 20
	}
	req := domain.AskRequest{Question: b.Question, Compare: b.Compare, K: k,
		Filter: domain.Filter{SourceIDs: cleanList(b.Sources), Leanings: cleanList(b.Leanings)}}
	if t, err := time.ParseInLocation(dateLayout, b.From, s.loc); err == nil {
		req.Filter.From = t
	}
	if t, err := time.ParseInLocation(dateLayout, b.To, s.loc); err == nil {
		req.Filter.To = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, s.loc)
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // streaming: no server write timeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	sse := &sseWriter{w: w, rc: rc}
	_ = rc.Flush()

	ctx := r.Context()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sse.comment()
			}
		}
	}()

	sm := s.sourceMap(ctx)
	err := s.d.Retriever.Ask(ctx, req, func(hits []domain.Hit) {
		out := make([]sourceJSON, 0, len(hits))
		for i, h := range hits {
			so := sm[h.Article.SourceID]
			name := so.Name
			if name == "" {
				name = h.Article.SourceID
			}
			pub := ""
			if t := h.Article.PublishedAt; !t.IsZero() {
				pub = t.In(s.loc).Format(time.RFC3339)
			}
			out = append(out, sourceJSON{N: i + 1, ArticleID: h.Article.ID, Title: h.Article.Title, SourceID: h.Article.SourceID,
				Source: name, Leaning: so.Leaning, URL: h.Article.URL, Section: h.Article.Section, Published: pub,
				Snippet: plainSnippet(h.Snippet)})
		}
		js, _ := json.Marshal(out)
		_ = sse.event("sources", string(js))
	}, func(d string) error {
		if d == "" {
			return nil
		}
		return sse.event("delta", d)
	})
	if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		s.log.Warn("ask failed", "err", err)
		_ = sse.event("error", "No se pudo generar la respuesta: el modelo de lenguaje no está disponible o falló. Las fuentes recuperadas (si las hay) se muestran igualmente.")
	}
	_ = sse.event("done", "ok")
}

// ---- status ----

type llmJSON struct {
	OK        bool      `json:"ok"`
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

func (s *server) apiStatus(w http.ResponseWriter, r *http.Request) {
	v := s.buildStatus(r.Context())
	feeds := []domain.FeedState{}
	for _, sh := range v.Sources {
		for _, f := range sh.Feeds {
			feeds = append(feeds, f.FeedState)
		}
	}
	out := map[string]any{
		"version": v.Version,
		"time":    s.now(),
		"feeds":   feeds,
		"jobs":    v.Jobs,
		"running": v.Running,
		"last":    v.Last,
		"stats":   v.Stats,
	}
	if v.HaveLLM {
		out["llm"] = map[string]llmJSON{
			"chat":  {v.Chat.OK, v.Chat.Detail, v.Chat.CheckedAt},
			"embed": {v.Embed.OK, v.Embed.Detail, v.Embed.CheckedAt},
		}
	}
	writeJSON(w, 200, out)
}
