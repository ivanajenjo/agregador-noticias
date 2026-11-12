package store

import (
	"context"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func (s *SQLite) AnnotationModels(ctx context.Context, kind string) ([]domain.AnnotationModelInfo, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT model, COUNT(*), MAX(created_at) FROM article_annotations WHERE kind=? GROUP BY model ORDER BY COUNT(*) DESC, model`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AnnotationModelInfo
	for rows.Next() {
		var m domain.AnnotationModelInfo
		var t int64
		if err := rows.Scan(&m.Model, &m.Count, &t); err != nil {
			return nil, err
		}
		m.Newest = time.Unix(t, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// annoWhere builds the shared clauses (alias n = annotations, a = articles).
func annoWhere(q domain.AnnotationQuery) (string, []any) {
	cl := []string{"n.kind=?", "n.model=?", "n.score>=?"}
	args := []any{q.Kind, q.Model, q.MinScore}
	if q.Label != "" {
		cl = append(cl, "n.label=?")
		args = append(args, q.Label)
	}
	if w, wa := where(domain.Filter{SourceIDs: q.SourceIDs, From: q.From, To: q.To}); w != "" {
		cl = append(cl, w)
		args = append(args, wa...)
	}
	return strings.Join(cl, " AND "), args
}

func (s *SQLite) AnnotationDistribution(ctx context.Context, q domain.AnnotationQuery) ([]domain.AnnotationCount, error) {
	w, args := annoWhere(q)
	rows, err := s.r.QueryContext(ctx, `SELECT a.source_id, n.label, COUNT(*) FROM article_annotations n JOIN articles a ON a.id=n.article_id WHERE `+w+` GROUP BY a.source_id, n.label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AnnotationCount
	for rows.Next() {
		var c domain.AnnotationCount
		if err := rows.Scan(&c.SourceID, &c.Label, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLite) ListAnnotated(ctx context.Context, q domain.AnnotationQuery) ([]domain.AnnotatedArticle, error) {
	w, args := annoWhere(q)
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, `SELECT `+articleCols+`, n.label, n.score FROM article_annotations n JOIN articles a ON a.id=n.article_id WHERE `+w+` ORDER BY `+dateExpr+` DESC, a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AnnotatedArticle
	for rows.Next() {
		var x domain.AnnotatedArticle
		a, err := scanArticle(rows, &x.Label, &x.Score)
		if err != nil {
			return nil, err
		}
		x.Article = a
		out = append(out, x)
	}
	return out, rows.Err()
}
