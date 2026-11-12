package store

import (
	"context"
	"strconv"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.TrendStore = (*SQLite)(nil)

// trendSummaryMax is the number of characters of the summary kept for trend
// analysis (truncated in SQL so big summaries never cross the driver).
const trendSummaryMax = 400

// ArticleTexts returns lightweight article projections with
// COALESCE(published_at, first_seen_at) in [from, to), oldest first. It uses
// the idx_articles_date expression index (scanned in reverse).
func (s *SQLite) ArticleTexts(ctx context.Context, from, to time.Time) ([]domain.ArticleLite, error) {
	rows, err := s.r.QueryContext(ctx, `
SELECT a.id, a.source_id, a.title, substr(a.summary, 1, `+strconv.Itoa(trendSummaryMax)+`), a.url, `+dateExpr+` AS at
FROM articles a
WHERE `+dateExpr+` >= ? AND `+dateExpr+` < ?
ORDER BY `+dateExpr+` ASC, a.id ASC`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ArticleLite
	for rows.Next() {
		var a domain.ArticleLite
		var at int64
		if err := rows.Scan(&a.ID, &a.SourceID, &a.Title, &a.Summary, &a.URL, &at); err != nil {
			return nil, err
		}
		a.At = time.Unix(at, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}
