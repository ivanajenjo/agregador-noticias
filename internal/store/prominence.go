package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.ProminenceStore = (*SQLite)(nil)

// SaveFrontItems replaces the items of a screenshot (idempotent).
func (s *SQLite) SaveFrontItems(ctx context.Context, screenshotID int64, items []domain.FrontItem) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var src string
	var at int64
	if err := tx.QueryRowContext(ctx, `SELECT source_id, taken_at FROM screenshots WHERE id=?`, screenshotID).Scan(&src, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("store: screenshot not found")
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM frontpage_items WHERE screenshot_id=?`, screenshotID); err != nil {
		return err
	}
	st, err := tx.PrepareContext(ctx, `INSERT INTO frontpage_items
(screenshot_id,source_id,taken_at,rank,dom_order,title,url,canonical_url,x,y,w,h,font_px,prominence,above_fold)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, it := range items {
		fold := 0
		if it.AboveFold {
			fold = 1
		}
		if _, err := st.ExecContext(ctx, screenshotID, src, at, it.Rank, it.DOMOrder, it.Title, it.URL, it.CanonicalURL,
			it.X, it.Y, it.W, it.H, it.FontPx, it.Prominence, fold); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListFrontItems: items of the capture at takenAt (exact), or the latest
// capture of the source when takenAt is zero. Rank order.
func (s *SQLite) ListFrontItems(ctx context.Context, sourceID string, takenAt time.Time) ([]domain.FrontItem, error) {
	var shot int64
	var err error
	if takenAt.IsZero() {
		err = s.r.QueryRowContext(ctx, `SELECT screenshot_id FROM frontpage_items WHERE source_id=? ORDER BY taken_at DESC, screenshot_id DESC LIMIT 1`, sourceID).Scan(&shot)
	} else {
		err = s.r.QueryRowContext(ctx, `SELECT screenshot_id FROM frontpage_items WHERE source_id=? AND taken_at=? ORDER BY screenshot_id DESC LIMIT 1`, sourceID, takenAt.Unix()).Scan(&shot)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT f.id,f.screenshot_id,f.source_id,f.taken_at,f.rank,f.dom_order,f.title,f.url,f.canonical_url,
f.x,f.y,f.w,f.h,f.font_px,f.prominence,f.above_fold, COALESCE(a.id,0)
FROM frontpage_items f LEFT JOIN articles a ON f.canonical_url<>'' AND a.canonical_url=f.canonical_url
WHERE f.screenshot_id=? ORDER BY f.rank, f.id`, shot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FrontItem
	for rows.Next() {
		var it domain.FrontItem
		var at int64
		var fold int
		if err := rows.Scan(&it.ID, &it.ScreenshotID, &it.SourceID, &at, &it.Rank, &it.DOMOrder, &it.Title, &it.URL, &it.CanonicalURL,
			&it.X, &it.Y, &it.W, &it.H, &it.FontPx, &it.Prominence, &fold, &it.ArticleID); err != nil {
			return nil, err
		}
		it.TakenAt = time.Unix(at, 0)
		it.AboveFold = fold != 0
		out = append(out, it)
	}
	return out, rows.Err()
}

// TopItems aggregates per (source, canonical URL) over [from, to).
func (s *SQLite) TopItems(ctx context.Context, from, to time.Time, sourceIDs []string, limit int) ([]domain.TopItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT f.source_id, f.canonical_url, COUNT(*), MIN(f.rank), AVG(f.prominence), MAX(f.prominence), MIN(f.taken_at), MAX(f.taken_at),
 (SELECT t.title FROM frontpage_items t WHERE t.source_id=f.source_id AND t.canonical_url=f.canonical_url ORDER BY t.taken_at DESC LIMIT 1),
 COALESCE((SELECT a.id FROM articles a WHERE a.canonical_url=f.canonical_url),0)
FROM frontpage_items f WHERE f.canonical_url<>''`
	var args []any
	if !from.IsZero() {
		q += " AND f.taken_at>=?"
		args = append(args, from.Unix())
	}
	if !to.IsZero() {
		q += " AND f.taken_at<?"
		args = append(args, to.Unix())
	}
	if len(sourceIDs) > 0 {
		q += " AND f.source_id IN (?" + strings.Repeat(",?", len(sourceIDs)-1) + ")"
		for _, id := range sourceIDs {
			args = append(args, id)
		}
	}
	q += " GROUP BY f.source_id, f.canonical_url ORDER BY MIN(f.rank), AVG(f.prominence) DESC, COUNT(*) DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TopItem
	for rows.Next() {
		var t domain.TopItem
		var a, b int64
		if err := rows.Scan(&t.SourceID, &t.CanonicalURL, &t.Captures, &t.BestRank, &t.AvgProm, &t.MaxProm, &a, &b, &t.Title, &t.ArticleID); err != nil {
			return nil, err
		}
		t.FirstAt, t.LastAt = time.Unix(a, 0), time.Unix(b, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ItemHistory: every capture where the canonical URL appeared, oldest first.
func (s *SQLite) ItemHistory(ctx context.Context, canonicalURL string) ([]domain.ItemPoint, error) {
	if canonicalURL == "" {
		return nil, nil
	}
	rows, err := s.r.QueryContext(ctx, `SELECT source_id, screenshot_id, taken_at, MIN(rank), MAX(prominence), MAX(above_fold), title
FROM frontpage_items WHERE canonical_url=? GROUP BY screenshot_id ORDER BY taken_at, screenshot_id LIMIT 2000`, canonicalURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ItemPoint
	for rows.Next() {
		var p domain.ItemPoint
		var at int64
		var fold int
		if err := rows.Scan(&p.SourceID, &p.ScreenshotID, &at, &p.Rank, &p.Prominence, &fold, &p.Title); err != nil {
			return nil, err
		}
		p.TakenAt, p.AboveFold = time.Unix(at, 0), fold != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
