package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ---- screenshots ----------------------------------------------------------

func (s *SQLite) SaveScreenshot(ctx context.Context, sh *domain.Screenshot) error {
	if sh.TakenAt.IsZero() {
		sh.TakenAt = time.Now().UTC()
	}
	res, err := s.w.ExecContext(ctx, `INSERT INTO screenshots (source_id,taken_at,path,width,height,bytes) VALUES (?,?,?,?,?,?)`,
		sh.SourceID, sh.TakenAt.Unix(), sh.Path, sh.Width, sh.Height, sh.Bytes)
	if err != nil {
		return err
	}
	sh.ID, _ = res.LastInsertId()
	return nil
}

// ListScreenshots returns screenshots oldest first. Empty sourceID = all
// sources; zero from/to = unbounded (both inclusive).
func (s *SQLite) ListScreenshots(ctx context.Context, sourceID string, from, to time.Time) ([]domain.Screenshot, error) {
	q := `SELECT id,source_id,taken_at,path,width,height,bytes FROM screenshots WHERE 1=1`
	var args []any
	if sourceID != "" {
		q += " AND source_id=?"
		args = append(args, sourceID)
	}
	if !from.IsZero() {
		q += " AND taken_at>=?"
		args = append(args, from.Unix())
	}
	if !to.IsZero() {
		q += " AND taken_at<=?"
		args = append(args, to.Unix())
	}
	q += " ORDER BY taken_at, id"
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Screenshot
	for rows.Next() {
		var x domain.Screenshot
		var t int64
		if err := rows.Scan(&x.ID, &x.SourceID, &t, &x.Path, &x.Width, &x.Height, &x.Bytes); err != nil {
			return nil, err
		}
		x.TakenAt = time.Unix(t, 0).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteScreenshotsBefore(ctx context.Context, t time.Time) ([]string, error) {
	var paths []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT path FROM screenshots WHERE taken_at<? ORDER BY taken_at`, t.Unix())
		if err != nil {
			return err
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			paths = append(paths, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		_, err = tx.ExecContext(ctx, `DELETE FROM screenshots WHERE taken_at<?`, t.Unix())
		return err
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// ---- feed state -----------------------------------------------------------

// GetFeedState returns (nil, nil) when there is no state yet.
func (s *SQLite) GetFeedState(ctx context.Context, sourceID, feedURL string) (*domain.FeedState, error) {
	row := s.r.QueryRowContext(ctx, `SELECT source_id,feed_url,etag,last_modified,last_ok,last_error,last_error_at,last_count FROM feed_state WHERE source_id=? AND feed_url=?`, sourceID, feedURL)
	fs, err := scanFeedState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fs, nil
}

func scanFeedState(r scanner) (domain.FeedState, error) {
	var fs domain.FeedState
	var ok, errAt sql.NullInt64
	err := r.Scan(&fs.SourceID, &fs.FeedURL, &fs.ETag, &fs.LastModified, &ok, &fs.LastError, &errAt, &fs.LastCount)
	fs.LastOK, fs.LastErrorAt = fromUnix(ok), fromUnix(errAt)
	return fs, err
}

func (s *SQLite) SaveFeedState(ctx context.Context, fs *domain.FeedState) error {
	_, err := s.w.ExecContext(ctx, `
INSERT INTO feed_state (source_id,feed_url,etag,last_modified,last_ok,last_error,last_error_at,last_count)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(source_id,feed_url) DO UPDATE SET etag=excluded.etag,last_modified=excluded.last_modified,
 last_ok=excluded.last_ok,last_error=excluded.last_error,last_error_at=excluded.last_error_at,last_count=excluded.last_count`,
		fs.SourceID, fs.FeedURL, fs.ETag, fs.LastModified, toUnix(fs.LastOK), fs.LastError, toUnix(fs.LastErrorAt), fs.LastCount)
	return err
}

func (s *SQLite) ListFeedStates(ctx context.Context) ([]domain.FeedState, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT source_id,feed_url,etag,last_modified,last_ok,last_error,last_error_at,last_count FROM feed_state ORDER BY source_id,feed_url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FeedState
	for rows.Next() {
		fs, err := scanFeedState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fs)
	}
	return out, rows.Err()
}

// ---- jobs -----------------------------------------------------------------

func (s *SQLite) StartJob(ctx context.Context, job string) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO job_runs (job,started_at) VALUES (?,?)`, job, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLite) FinishJob(ctx context.Context, id int64, ok bool, msg string) error {
	res, err := s.w.ExecContext(ctx, `UPDATE job_runs SET finished_at=?, ok=?, message=? WHERE id=?`, time.Now().Unix(), b2i(ok), msg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("store: unknown job run")
	}
	return nil
}

// ListJobRuns returns the most recent runs first. limit<=0 means 50.
func (s *SQLite) ListJobRuns(ctx context.Context, limit int) ([]domain.JobRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id,job,started_at,finished_at,ok,message FROM job_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.JobRun
	for rows.Next() {
		var j domain.JobRun
		var st int64
		var fin sql.NullInt64
		var ok int
		if err := rows.Scan(&j.ID, &j.Job, &st, &fin, &ok, &j.Message); err != nil {
			return nil, err
		}
		j.StartedAt, j.FinishedAt, j.OK = time.Unix(st, 0).UTC(), fromUnix(fin), ok != 0
		out = append(out, j)
	}
	return out, rows.Err()
}

// ---- stats ----------------------------------------------------------------

func (s *SQLite) Stats(ctx context.Context, model string) (*domain.Stats, error) {
	st := &domain.Stats{ArticlesBySource: map[string]int64{}}
	rows, err := s.r.QueryContext(ctx, `SELECT source_id, COUNT(*) FROM articles GROUP BY source_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.ArticlesBySource[id] = n
		st.Articles += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var oldest, newest sql.NullInt64
	if err := s.r.QueryRowContext(ctx, `SELECT MIN(`+dateExpr+`), MAX(`+dateExpr+`) FROM articles a`).Scan(&oldest, &newest); err != nil {
		return nil, err
	}
	st.OldestArticle, st.NewestArticle = fromUnix(oldest), fromUnix(newest)
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_embeds WHERE model=?`, model).Scan(&st.Embedded); err != nil {
		return nil, err
	}
	st.PendingEmbed = st.Articles - st.Embedded
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(bytes),0) FROM screenshots`).Scan(&st.Screenshots, &st.ShotsBytes); err != nil {
		return nil, err
	}
	var pc, ps int64
	if err := s.r.QueryRowContext(ctx, `SELECT (SELECT page_count FROM pragma_page_count), (SELECT page_size FROM pragma_page_size)`).Scan(&pc, &ps); err != nil {
		return nil, err
	}
	st.DBBytes = pc * ps
	return st, nil
}
