package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var (
	_ domain.RetentionStore = (*SQLite)(nil)
	_ domain.Pinger         = (*SQLite)(nil)
)

const (
	defaultRetentionBatch = 200
	maxRetentionBatch     = 900 // stays far below SQLite's bound-variable limit
	// incrementalVacuumPages caps the pages released per Maintain run (each
	// page is 4 KiB by default): (32 MiB) small enough not to hammer an SD card.
	incrementalVacuumPages = 8192
	// vacuumMinFreePages: below this many free pages (~4 MiB) it is not worth
	// touching the file.
	vacuumMinFreePages = 1000
)

// freshFile reports whether path is a database that does not exist yet (or is
// empty). Fresh files are created with auto_vacuum=INCREMENTAL: the mode can
// only be chosen before the first table exists; changing it on an existing
// database needs a full VACUUM (rewrites the whole file, needs free space equal
// to the DB and many SD-card writes), so existing databases are left untouched.
// See docs/OPERACION.md.
func freshFile(path string) bool {
	fi, err := os.Stat(path)
	return err != nil && errors.Is(err, os.ErrNotExist) || err == nil && fi.Size() == 0
}

// CountArticlesBefore is the dry run of DeleteArticlesBefore.
func (s *SQLite) CountArticlesBefore(ctx context.Context, t time.Time) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM articles WHERE first_seen_at<?`, t.Unix()).Scan(&n)
	return n, err
}

// DeleteArticlesBefore deletes articles first seen before t, oldest ids first,
// in short transactions of at most batch rows so readers and the other
// writers (ingest, embed worker) are never blocked for long. Foreign keys
// cascade to article_chunks, article_embeds and article_annotations, the
// articles_ad trigger removes the FTS rows, and the in-memory vector cache is
// purged. It is idempotent: a second call finds nothing to delete.
//
// Retention uses first_seen_at (not the published date) so that an old article
// that a feed still lists is not deleted and re-ingested over and over.
func (s *SQLite) DeleteArticlesBefore(ctx context.Context, t time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = defaultRetentionBatch
	}
	if batch > maxRetentionBatch {
		batch = maxRetentionBatch
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var ids []int64
		err := s.tx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM articles WHERE first_seen_at<? ORDER BY id LIMIT ?`, t.Unix(), batch)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if len(ids) == 0 {
				return nil
			}
			ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			args := make([]any, len(ids))
			for i, id := range ids {
				args[i] = id
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM articles WHERE id IN (`+ph+`)`, args...)
			return err
		})
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		s.vecs.remove(ids)
		total += len(ids)
		if len(ids) < batch {
			return total, nil
		}
	}
}

// remove drops the given articles from every cached model.
func (v *vectorIndex) remove(ids []int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, mi := range v.models {
		for _, id := range ids {
			p, ok := mi.pos[id]
			if !ok {
				continue
			}
			last := len(mi.entries) - 1
			mi.entries[p] = mi.entries[last]
			mi.pos[mi.entries[p].article] = p
			mi.entries[last] = nil
			mi.entries = mi.entries[:last]
			delete(mi.pos, id)
		}
	}
}

// Maintain applies retention (olderThan<=0 keeps every article) and then the
// housekeeping suited to a small SD-card database: merge FTS segments after
// deletions, PRAGMA optimize, a TRUNCATE checkpoint so the -wal file does not
// linger, and a bounded incremental_vacuum when the DB supports it.
// It never runs a full VACUUM.
func (s *SQLite) Maintain(ctx context.Context, olderThan time.Duration) (domain.MaintainResult, error) {
	var res domain.MaintainResult
	if olderThan > 0 {
		res.Cutoff = time.Now().Add(-olderThan)
		n, err := s.DeleteArticlesBefore(ctx, res.Cutoff, defaultRetentionBatch)
		res.Deleted = n
		if err != nil {
			return res, err
		}
	}
	if res.Deleted > 0 {
		// 'optimize' merges all FTS5 b-trees into one; only worth it after deletions.
		if _, err := s.w.ExecContext(ctx, `INSERT INTO articles_fts(articles_fts) VALUES('optimize')`); err != nil {
			return res, err
		}
	}
	if _, err := s.w.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return res, err
	}
	_ = s.w.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&res.AutoVacuum)
	if res.AutoVacuum == 2 {
		var free int64
		if err := s.w.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err == nil && free >= vacuumMinFreePages {
			// the statement only takes effect when stepped to completion
			if rows, err := s.w.QueryContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, incrementalVacuumPages)); err == nil {
				for rows.Next() {
				}
				rows.Close()
				var after int64
				if err := s.w.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&after); err == nil && after < free {
					res.ReclaimedPages = free - after
				}
			}
		}
	}
	var busy, logFrames, ckpt int
	if err := s.w.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &ckpt); err == nil {
		res.WALCheckpoint = busy == 0
	}
	_ = s.w.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&res.FreelistPages)
	var pc, ps int64
	if err := s.w.QueryRowContext(ctx, `SELECT (SELECT page_count FROM pragma_page_count), (SELECT page_size FROM pragma_page_size)`).Scan(&pc, &ps); err == nil {
		res.DBBytes = pc * ps
	}
	return res, nil
}

// PingRead checks that the read pool can serve a query.
func (s *SQLite) PingRead(ctx context.Context) error {
	var one int
	return s.r.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// PingWrite checks that the single writer connection can be acquired and that
// the write lock can be taken (BEGIN IMMEDIATE, then roll back).
func (s *SQLite) PingWrite(ctx context.Context) error {
	return s.tx(ctx, func(*sql.Tx) error { return nil })
}
