package web

import (
	"context"
	"net/http"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type termJSON struct {
	Term         string         `json:"term"`
	Key          string         `json:"key"`
	Kind         string         `json:"kind"`
	Count        int            `json:"count"`
	Sources      int            `json:"sources"`
	BaselineRate float64        `json:"baseline_rate"`
	Score        float64        `json:"score"`
	New          bool           `json:"new"`
	FirstAt      *time.Time     `json:"first_at,omitempty"`
	Leanings     map[string]int `json:"leanings"`
	Spark        []int          `json:"spark"`
}

type refJSON struct {
	ID       int64     `json:"id"`
	SourceID string    `json:"source_id"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	At       time.Time `json:"at"`
}

type coverageJSON struct {
	SourceID  string     `json:"source_id"`
	Name      string     `json:"name"`
	Leaning   string     `json:"leaning,omitempty"`
	Count     int        `json:"count"`
	FirstAt   *time.Time `json:"first_at,omitempty"`
	Headlines []refJSON  `json:"headlines"`
}

type dayJSON struct {
	Day      string         `json:"day"` // YYYY-MM-DD in the configured zone
	Total    int            `json:"total"`
	Leanings map[string]int `json:"leanings"`
}

type storyJSON struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Terms    []string       `json:"terms"`
	Sources  int            `json:"sources"`
	Articles int            `json:"articles"`
	Leanings map[string]int `json:"leanings"`
	FirstAt  *time.Time     `json:"first_at,omitempty"`
	LastAt   *time.Time     `json:"last_at,omitempty"`
	FirstBy  string         `json:"first_by"`
	Score    float64        `json:"score"`
	Coverage []coverageJSON `json:"coverage"`
	Missing  []coverageJSON `json:"missing"`
}

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func orEmptyMap(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

func orEmptyStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toCoverageJSON(in []domain.SourceCoverage) []coverageJSON {
	out := make([]coverageJSON, 0, len(in))
	for _, c := range in {
		j := coverageJSON{SourceID: c.SourceID, Name: c.Name, Leaning: c.Leaning, Count: c.Count, FirstAt: tptr(c.FirstAt), Headlines: []refJSON{}}
		if j.Name == "" {
			j.Name = c.SourceID
		}
		for _, h := range c.Headlines {
			j.Headlines = append(j.Headlines, refJSON{h.ID, h.SourceID, h.Title, h.URL, h.At})
		}
		out = append(out, j)
	}
	return out
}

func (s *server) trendsAPIPrecheck(w http.ResponseWriter, r *http.Request) bool {
	if s.d.Trends == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "Tendencias no disponibles")
		return false
	}
	return true
}

func (s *server) apiTrends(w http.ResponseWriter, r *http.Request) {
	if !s.trendsAPIPrecheck(w, r) {
		return
	}
	q := r.URL.Query()
	_, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "ventana no válida (6h, 24h, 48h, 7d)")
		return
	}
	limit := intParam(r, "limit", defaultTrendLim, 1, maxTrendLimit)
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	rep, err := s.d.Trends.Trending(ctx, s.trendOpts(q, window, limit))
	if err != nil || rep == nil {
		s.log.Error("api trends", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular tendencias")
		return
	}
	terms := make([]termJSON, 0, len(rep.Terms))
	for i, t := range rep.Terms {
		if i >= limit {
			break
		}
		terms = append(terms, termJSON{t.Term, t.Key, t.Kind, t.Count, t.Sources, t.BaselineRate, t.Score, t.New, tptr(t.FirstAt),
			orEmptyMap(t.Leanings), append([]int{}, t.Spark...)})
	}
	writeJSON(w, 200, map[string]any{
		"generated_at": rep.GeneratedAt, "window_hours": rep.Window.Hours(), "baseline_hours": rep.Baseline.Hours(),
		"articles": rep.Articles, "terms": terms,
	})
}

func (s *server) apiTrendTerm(w http.ResponseWriter, r *http.Request) {
	if !s.trendsAPIPrecheck(w, r) {
		return
	}
	q := r.URL.Query()
	key := q.Get("key")
	if !validKey(key) {
		s.fail(w, r, http.StatusBadRequest, "Falta el parámetro key (o no es válido)")
		return
	}
	days := intParam(r, "dias", defaultTermDays, minTermDays, maxTermDays)
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	d, err := s.d.Trends.Term(ctx, key, days, domain.TrendOptions{SourceIDs: cleanList(q["fuentes"])})
	if err != nil && !isNotFound(err) {
		s.log.Error("api trend term", "key", key, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular el término")
		return
	}
	if err != nil || d == nil || (d.Total == 0 && len(d.Coverage) == 0) {
		s.fail(w, r, http.StatusNotFound, "Término no encontrado")
		return
	}
	daily := make([]dayJSON, 0, len(d.Daily))
	for _, x := range d.Daily {
		daily = append(daily, dayJSON{x.Day.In(s.loc).Format(dateLayout), x.Total, orEmptyMap(x.Leanings)})
	}
	writeJSON(w, 200, map[string]any{
		"term": d.Term, "key": d.Key, "days": days, "total": d.Total,
		"coverage": toCoverageJSON(d.Coverage), "missing": toCoverageJSON(d.Missing),
		"daily": daily, "related": orEmptyStr(d.Related),
	})
}

func (s *server) apiStories(w http.ResponseWriter, r *http.Request) {
	if !s.trendsAPIPrecheck(w, r) {
		return
	}
	q := r.URL.Query()
	_, window, ok := parseVentana(q.Get("ventana"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "ventana no válida (6h, 24h, 48h, 7d)")
		return
	}
	limit := intParam(r, "limit", defaultTrendLim, 1, maxTrendLimit)
	ctx, cancel := context.WithTimeout(r.Context(), trendsTimeout)
	defer cancel()
	st, err := s.d.Trends.Stories(ctx, s.trendOpts(q, window, limit))
	if err != nil {
		s.log.Error("api stories", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al calcular historias")
		return
	}
	out := make([]storyJSON, 0, len(st))
	for i, x := range st {
		if i >= limit {
			break
		}
		out = append(out, storyJSON{x.ID, x.Title, orEmptyStr(x.Terms), x.Sources, x.Articles, orEmptyMap(x.Leanings),
			tptr(x.FirstAt), tptr(x.LastAt), x.FirstBy, x.Score, toCoverageJSON(x.Coverage), toCoverageJSON(x.Missing)})
	}
	writeJSON(w, 200, map[string]any{"generated_at": s.now(), "window_hours": window.Hours(), "stories": out})
}
