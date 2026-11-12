package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.FollowStore = (*SQLite)(nil)

func jsonList(v []string) string {
	b, _ := json.Marshal(nonNilStrings(v))
	return string(b)
}

func parseList(s string) []string {
	var v []string
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

func (s *SQLite) CreateWatch(ctx context.Context, w *domain.Watch) error {
	if strings.TrimSpace(w.Name) == "" || strings.TrimSpace(w.Query) == "" {
		return errors.New("store: watch needs name and query")
	}
	now := time.Now().UTC()
	res, err := s.w.ExecContext(ctx, `INSERT INTO watches (name,query,source_ids_json,leanings_json,notify,created_at) VALUES (?,?,?,?,?,?)`,
		w.Name, w.Query, jsonList(w.SourceIDs), jsonList(w.Leanings), b2i(w.Notify), now.Unix())
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	w.CreatedAt = time.Unix(now.Unix(), 0).UTC()
	return nil
}

const watchCols = `id,name,query,source_ids_json,leanings_json,notify,created_at,last_eval_at`

func scanWatch(r scanner) (domain.Watch, error) {
	var w domain.Watch
	var src, ln string
	var notify int
	var created int64
	var ev sql.NullInt64
	if err := r.Scan(&w.ID, &w.Name, &w.Query, &src, &ln, &notify, &created, &ev); err != nil {
		return w, err
	}
	w.SourceIDs, w.Leanings, w.Notify = parseList(src), parseList(ln), notify != 0
	w.CreatedAt = time.Unix(created, 0).UTC()
	w.LastEvalAt = fromUnix(ev)
	return w, nil
}

func (s *SQLite) GetWatch(ctx context.Context, id int64) (*domain.Watch, error) {
	w, err := scanWatch(s.r.QueryRowContext(ctx, `SELECT `+watchCols+` FROM watches WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *SQLite) ListWatches(ctx context.Context) ([]domain.Watch, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+watchCols+` FROM watches ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteWatch(ctx context.Context, id int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM watches WHERE id=?`, id)
	return err
}

func (s *SQLite) SetWatchEvaluated(ctx context.Context, id int64, t time.Time) error {
	_, err := s.w.ExecContext(ctx, `UPDATE watches SET last_eval_at=? WHERE id=?`, t.Unix(), id)
	return err
}

func (s *SQLite) NewWatchHits(ctx context.Context, watchID int64, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.r.QueryContext(ctx, `SELECT article_id FROM watch_hits WHERE watch_id=?`, watchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []int64
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *SQLite) MarkWatchHits(ctx context.Context, watchID int64, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO watch_hits (watch_id,article_id,seen_at) VALUES (?,?,?)`, watchID, id, at.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLite) PruneWatchHits(ctx context.Context, before time.Time) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM watch_hits WHERE seen_at < ?`, before.Unix())
	return err
}

func (s *SQLite) SaveAlert(ctx context.Context, a domain.AlertRecord) error {
	if a.Kind == "" || a.Key == "" || a.Channel == "" {
		return errors.New("store: alert needs kind, key and channel")
	}
	if a.SentAt.IsZero() {
		a.SentAt = time.Now()
	}
	_, err := s.w.ExecContext(ctx, `
INSERT INTO alert_log (kind,key,title,url,sent_at,channel,ok,detail) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(kind,key,channel) DO UPDATE SET ok=excluded.ok, detail=excluded.detail`,
		a.Kind, a.Key, a.Title, a.URL, a.SentAt.Unix(), a.Channel, b2i(a.OK), a.Detail)
	return err
}

func (s *SQLite) AlertHandled(ctx context.Context, kind, key string, giveUpBefore time.Time) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_log WHERE kind=? AND key=?
AND (ok=1 OR channel IN ('log','quiet') OR sent_at < ?)`, kind, key, giveUpBefore.Unix()).Scan(&n)
	return n > 0, err
}

func scanAlerts(rows *sql.Rows) ([]domain.AlertRecord, error) {
	defer rows.Close()
	var out []domain.AlertRecord
	for rows.Next() {
		var a domain.AlertRecord
		var t int64
		var ok int
		if err := rows.Scan(&a.ID, &a.Kind, &a.Key, &a.Title, &a.URL, &t, &a.Channel, &ok, &a.Detail); err != nil {
			return nil, err
		}
		a.SentAt, a.OK = time.Unix(t, 0).UTC(), ok != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

const alertCols = `id,kind,key,title,url,sent_at,channel,ok,detail`

func (s *SQLite) ListAlerts(ctx context.Context, limit int) ([]domain.AlertRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.r.QueryContext(ctx, `SELECT `+alertCols+` FROM alert_log ORDER BY sent_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanAlerts(rows)
}

func (s *SQLite) CountAlertsSince(ctx context.Context, t time.Time) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(DISTINCT kind||'|'||key) FROM alert_log
WHERE sent_at >= ? AND channel <> 'quiet' AND kind <> 'test'`, t.Unix()).Scan(&n)
	return n, err
}

func (s *SQLite) PendingQuiet(ctx context.Context) ([]domain.AlertRecord, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+alertCols+` FROM alert_log WHERE channel='quiet' AND ok=0 ORDER BY sent_at, id`)
	if err != nil {
		return nil, err
	}
	return scanAlerts(rows)
}

func (s *SQLite) ResolveQuiet(ctx context.Context, ids []int64, detail string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE alert_log SET ok=1, detail=? WHERE id=? AND channel='quiet'`, detail, id); err != nil {
				return err
			}
		}
		return nil
	})
}
