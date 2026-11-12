package store

import (
	"context"
	"errors"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func (s *SQLite) SaveAnnotation(ctx context.Context, a domain.Annotation) error {
	if a.Kind == "" || a.Model == "" {
		return errors.New("store: annotation needs kind and model")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.w.ExecContext(ctx, `
INSERT INTO article_annotations (article_id,kind,model,label,score,data,created_at) VALUES (?,?,?,?,?,?,?)
ON CONFLICT(article_id,kind,model) DO UPDATE SET label=excluded.label,score=excluded.score,data=excluded.data,created_at=excluded.created_at`,
		a.ArticleID, a.Kind, a.Model, a.Label, a.Score, a.Data, a.CreatedAt.Unix())
	return err
}

func (s *SQLite) ListAnnotations(ctx context.Context, articleID int64) ([]domain.Annotation, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT article_id,kind,model,label,score,data,created_at FROM article_annotations WHERE article_id=? ORDER BY kind,model`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Annotation
	for rows.Next() {
		var a domain.Annotation
		var t int64
		if err := rows.Scan(&a.ArticleID, &a.Kind, &a.Model, &a.Label, &a.Score, &a.Data, &t); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(t, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLite) ArticlesNeedingAnnotation(ctx context.Context, kind, model string, limit int) ([]domain.Article, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.r.QueryContext(ctx, `SELECT `+articleCols+` FROM articles a
WHERE NOT EXISTS (SELECT 1 FROM article_annotations n WHERE n.article_id=a.id AND n.kind=? AND n.model=?)
ORDER BY `+dateExpr+` DESC, a.id DESC LIMIT ?`, kind, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLite) CountAnnotationsByLabel(ctx context.Context, kind, model string, f domain.Filter) (map[string]int64, error) {
	q := `SELECT n.label, COUNT(*) FROM article_annotations n JOIN articles a ON a.id=n.article_id WHERE n.kind=? AND n.model=?`
	args := []any{kind, model}
	if w, wa := where(f); w != "" {
		q += " AND " + w
		args = append(args, wa...)
	}
	q += " GROUP BY n.label"
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var l string
		var n int64
		if err := rows.Scan(&l, &n); err != nil {
			return nil, err
		}
		out[l] = n
	}
	return out, rows.Err()
}
