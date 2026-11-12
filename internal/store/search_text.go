package store

import (
	"context"
	"html"
	"strings"
	"unicode"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`el la los las un una unos unas de del al y o u e a en con por para que se su sus lo es son fue ha han como mas pero sin sobre entre este esta estos estas ese esa`) {
		stopwords[w] = true
	}
}

// ftsQuery turns arbitrary user text into a safe FTS5 query: every token is a
// double-quoted string (so FTS operators, colons, quotes etc. are inert),
// tokens are OR-ed (BM25 ranks documents matching more/rarer terms first), the
// last token is a prefix match (search-as-you-type), and common Spanish
// stopwords are dropped unless nothing else is left. Returns "" if no tokens.
func ftsQuery(in string) string {
	toks := strings.FieldsFunc(in, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var all, keep []string
	seen := map[string]bool{}
	for _, t := range toks {
		t = strings.ToLower(t)
		if seen[t] {
			continue
		}
		seen[t] = true
		all = append(all, t)
		if !stopwords[fold(t)] {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		keep = all
	}
	if len(keep) > 32 {
		keep = keep[:32]
	}
	if len(keep) == 0 {
		return ""
	}
	parts := make([]string, len(keep))
	for i, t := range keep {
		parts[i] = `"` + t + `"`
	}
	parts[len(parts)-1] += "*"
	return strings.Join(parts, " OR ")
}

// SearchText runs a BM25-ranked FTS5 search (title weighted 5x, summary 2x).
// Hit.Score is -bm25 (higher is better). Hit.Snippet is SAFE HTML: the text is
// escaped and matches are wrapped in <mark>...</mark>. f.Offset applies; k is
// the page size (falls back to f.Limit, then 20).
func (s *SQLite) SearchText(ctx context.Context, query string, f domain.Filter, k int) ([]domain.Hit, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	if k <= 0 {
		k = f.Limit
	}
	if k <= 0 {
		k = 20
	}
	sqlq := `SELECT ` + articleCols + `, -bm25(articles_fts,5.0,2.0,1.0), snippet(articles_fts,-1,char(1),char(2),'…',24)
FROM articles_fts JOIN articles a ON a.id=articles_fts.rowid WHERE articles_fts MATCH ?`
	args := []any{q}
	if w, wa := where(f); w != "" {
		sqlq += " AND " + w
		args = append(args, wa...)
	}
	sqlq += ` ORDER BY bm25(articles_fts,5.0,2.0,1.0), a.id DESC LIMIT ? OFFSET ?`
	args = append(args, k, max(f.Offset, 0))
	rows, err := s.r.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Hit
	for rows.Next() {
		var h domain.Hit
		var snip string
		a, err := scanArticle(rows, &h.Score, &snip)
		if err != nil {
			return nil, err
		}
		h.Article, h.Chunk = a, -1
		snip = html.EscapeString(snip)
		snip = strings.ReplaceAll(strings.ReplaceAll(snip, "\x01", "<mark>"), "\x02", "</mark>")
		h.Snippet = snip
		out = append(out, h)
	}
	return out, rows.Err()
}
