package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/revisions"
)

var _ domain.RevisionStore = (*SQLite)(nil)

// RecordRevision implements domain.RevisionStore. Everything happens in one
// write transaction: revision rows, the article update (the FTS triggers keep
// the index in sync) and the re-queue for embedding.
func (s *SQLite) RecordRevision(ctx context.Context, canonical, newTitle, newSummary string, now time.Time) (bool, *domain.Revision, error) {
	newTitle, newSummary = strings.TrimSpace(newTitle), strings.TrimSpace(newSummary)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var first *domain.Revision
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var id, seen int64
		var title, summary, content string
		err := tx.QueryRowContext(ctx, `SELECT id,title,summary,content,first_seen_at FROM articles WHERE canonical_url=?`, canonical).
			Scan(&id, &title, &summary, &content, &seen)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var have int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE article_id=?`, id).Scan(&have); err != nil {
			return err
		}

		// known reports whether this normalized text was already part of the
		// article's history for this kind (flapping feed: A->B->A).
		known := func(kind, h string) (bool, error) {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM article_revisions WHERE article_id=? AND kind=? AND (old_hash=? OR new_hash=?) LIMIT 1`,
				id, kind, h, h).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return err == nil, err
		}
		record := func(kind, oldT, newT string) (*domain.Revision, error) {
			res, err := tx.ExecContext(ctx, `INSERT INTO article_revisions (article_id,kind,old_text,new_text,changed_at,seen_at,old_hash,new_hash) VALUES (?,?,?,?,?,?,?,?)`,
				id, kind, oldT, newT, now.Unix(), seen, revisions.Hash(kind, oldT), revisions.Hash(kind, newT))
			if err != nil {
				return nil, err
			}
			rid, _ := res.LastInsertId()
			have++
			return &domain.Revision{ID: rid, ArticleID: id, Kind: kind, OldText: oldT, NewText: newT,
				ChangedAt: time.Unix(now.Unix(), 0).UTC(), SeenAt: time.Unix(seen, 0).UTC()}, nil
		}

		titleChanged, summaryChanged := false, false
		if newTitle != "" && newTitle != title && have < domain.MaxRevisionsPerArticle &&
			revisions.Material(revisions.KindTitle, title, newTitle) {
			if dup, err := known(domain.RevisionTitle, revisions.Hash(revisions.KindTitle, newTitle)); err != nil {
				return err
			} else if !dup {
				r, err := record(domain.RevisionTitle, title, newTitle)
				if err != nil {
					return err
				}
				first, titleChanged = r, true
			}
		}
		silentSummary := false
		if newSummary != "" && newSummary != summary {
			switch {
			case summary == "":
				silentSummary = true // the feed started providing a summary: not an edit
			case have < domain.MaxRevisionsPerArticle && revisions.Material(revisions.KindSummary, summary, newSummary):
				if dup, err := known(domain.RevisionSummary, revisions.Hash(revisions.KindSummary, newSummary)); err != nil {
					return err
				} else if !dup {
					r, err := record(domain.RevisionSummary, summary, newSummary)
					if err != nil {
						return err
					}
					if first == nil {
						first = r
					}
					summaryChanged = true
				}
			}
		}
		if titleChanged {
			// title_hash is UNIQUE per source: if another article already owns
			// the new hash, keep the old hash (dedup key) and change only the text.
			res, err := tx.ExecContext(ctx, `UPDATE OR IGNORE articles SET title=?, title_hash=? WHERE id=?`, newTitle, titleHash(newTitle), id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE articles SET title=? WHERE id=?`, newTitle, id); err != nil {
					return err
				}
			}
		}
		if summaryChanged || silentSummary {
			if _, err := tx.ExecContext(ctx, `UPDATE articles SET summary=? WHERE id=?`, newSummary, id); err != nil {
				return err
			}
		}
		// Article.Text() = title + (content, else summary): re-embed when it changed.
		if titleChanged || ((summaryChanged || silentSummary) && content == "") {
			if _, err := tx.ExecContext(ctx, `DELETE FROM article_embeds WHERE article_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, nil, err
	}
	return first != nil, first, nil
}

func (s *SQLite) ListRevisions(ctx context.Context, articleID int64) ([]domain.Revision, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,article_id,kind,old_text,new_text,changed_at,seen_at FROM article_revisions WHERE article_id=? ORDER BY changed_at, id`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRevision(r scanner, extra ...any) (domain.Revision, error) {
	var v domain.Revision
	var ch, seen int64
	dest := append([]any{&v.ID, &v.ArticleID, &v.Kind, &v.OldText, &v.NewText, &ch, &seen}, extra...)
	if err := r.Scan(dest...); err != nil {
		return v, err
	}
	v.ChangedAt, v.SeenAt = time.Unix(ch, 0).UTC(), time.Unix(seen, 0).UTC()
	return v, nil
}

// revWhere builds the clauses for aliases r (article_revisions) and a (articles).
func revWhere(f domain.RevisionFilter) (string, []any) {
	var cl []string
	var args []any
	in := func(vals []string) string {
		args = append(args, strAny(vals)...)
		return "(" + strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",") + ")"
	}
	if len(f.SourceIDs) > 0 {
		cl = append(cl, "a.source_id IN "+in(f.SourceIDs))
	}
	if len(f.Leanings) > 0 {
		cl = append(cl, "a.source_id IN (SELECT id FROM sources WHERE leaning IN "+in(f.Leanings)+")")
	}
	if f.Kind != "" {
		cl = append(cl, "r.kind = ?")
		args = append(args, f.Kind)
	}
	if !f.From.IsZero() {
		cl = append(cl, "r.changed_at >= ?")
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		cl = append(cl, "r.changed_at <= ?")
		args = append(args, f.To.Unix())
	}
	if len(cl) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(cl, " AND "), args
}

const revFrom = ` FROM article_revisions r JOIN articles a ON a.id = r.article_id`

func (s *SQLite) RecentRevisions(ctx context.Context, f domain.RevisionFilter) ([]domain.RevisionEntry, error) {
	w, args := revWhere(f)
	limit := -1
	if f.Limit > 0 {
		limit = f.Limit
	}
	args = append(args, limit, max(f.Offset, 0))
	rows, err := s.r.QueryContext(ctx, `SELECT r.id,r.article_id,r.kind,r.old_text,r.new_text,r.changed_at,r.seen_at,a.source_id,a.title,a.url`+
		revFrom+w+` ORDER BY r.changed_at DESC, r.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RevisionEntry
	for rows.Next() {
		var e domain.RevisionEntry
		e.Revision, err = scanRevision(rows, &e.SourceID, &e.ArticleTitle, &e.ArticleURL)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLite) CountRevisions(ctx context.Context, f domain.RevisionFilter) (int64, error) {
	w, args := revWhere(f)
	var n int64
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*)`+revFrom+w, args...).Scan(&n)
	return n, err
}

func (s *SQLite) RevisionsBySource(ctx context.Context, f domain.RevisionFilter) ([]domain.SourceCount, error) {
	w, args := revWhere(f)
	rows, err := s.r.QueryContext(ctx, `SELECT a.source_id, COUNT(*) c`+revFrom+w+` GROUP BY a.source_id ORDER BY c DESC, a.source_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SourceCount
	for rows.Next() {
		var c domain.SourceCount
		if err := rows.Scan(&c.SourceID, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
