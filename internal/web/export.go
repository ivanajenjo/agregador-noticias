package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"time"
)

const (
	exportBatch   = 500
	exportMaxRows = 200000
	exportMaxSumm = 4000 // runes of summary per line
)

type exportLine struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Published string `json:"published,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

// apiExportArticles streams articles as NDJSON (one JSON object per line) with
// metadata only (no content). One export at a time; capped at exportMaxRows.
func (s *server) apiExportArticles(w http.ResponseWriter, r *http.Request) {
	select {
	case s.ops.export <- struct{}{}:
		defer func() { <-s.ops.export }()
	default:
		w.Header().Set("Retry-After", "30")
		s.fail(w, r, http.StatusTooManyRequests, "Ya hay una exportación en curso")
		return
	}
	flt, _ := s.parseFilter(r.URL.Query())
	max := intParam(r, "limit", exportMaxRows, 1, exportMaxRows)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="articulos.ndjson"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")

	bw := bufio.NewWriterSize(w, 32<<10)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	sent := 0
	for sent < max {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		f := flt
		f.Limit, f.Offset = min(exportBatch, max-sent), sent
		arts, err := s.d.Store.ListArticles(r.Context(), f)
		if err != nil {
			if sent == 0 {
				s.fail(w, r, http.StatusInternalServerError, "Error al exportar")
			} else {
				s.log.Warn("export aborted", "err", err)
			}
			return
		}
		for _, a := range arts {
			l := exportLine{Title: a.Title, URL: a.URL, Source: a.SourceID, Summary: truncRunes(a.Summary, exportMaxSumm)}
			if !a.PublishedAt.IsZero() {
				l.Published = a.PublishedAt.UTC().Format(time.RFC3339)
			}
			if err := enc.Encode(l); err != nil {
				return
			}
		}
		sent += len(arts)
		if err := bw.Flush(); err != nil {
			return
		}
		_ = rc.Flush()
		if len(arts) < f.Limit {
			break
		}
	}
	_ = bw.Flush()
}

func init() {
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /api/export/articles.ndjson", s.apiExportArticles)
	})
}
