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

// ---- time helpers: unix seconds UTC; zero time <-> NULL -------------------

func toUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func fromUnix(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- sources --------------------------------------------------------------

func (s *SQLite) UpsertSources(ctx context.Context, src []domain.Source) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, x := range src {
			if x.ID == "" {
				return errors.New("store: source with empty id")
			}
			feeds, _ := json.Marshal(nonNilFeeds(x.Feeds))
			cs, _ := json.Marshal(nonNilStrings(x.CookieSelectors))
			_, err := tx.ExecContext(ctx, `
INSERT INTO sources (id,name,leaning,homepage,feeds_json,full_text,screenshot,cookie_selectors_json,enabled)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, leaning=excluded.leaning, homepage=excluded.homepage,
  feeds_json=excluded.feeds_json, full_text=excluded.full_text, screenshot=excluded.screenshot,
  cookie_selectors_json=excluded.cookie_selectors_json, enabled=excluded.enabled`,
				x.ID, x.Name, x.Leaning, x.Homepage, string(feeds), b2i(x.FullText), b2i(x.Screenshot), string(cs), b2i(x.IsEnabled()))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNilFeeds(f []domain.Feed) []domain.Feed {
	if f == nil {
		return []domain.Feed{}
	}
	return f
}
func nonNilStrings(f []string) []string {
	if f == nil {
		return []string{}
	}
	return f
}

// feedJSON mirrors domain.Feed with explicit JSON keys (domain only has yaml tags).
func (s *SQLite) ListSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,name,leaning,homepage,feeds_json,full_text,screenshot,cookie_selectors_json,enabled FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Source
	for rows.Next() {
		var x domain.Source
		var feeds, cs string
		var ft, sh, en int
		if err := rows.Scan(&x.ID, &x.Name, &x.Leaning, &x.Homepage, &feeds, &ft, &sh, &cs, &en); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(feeds), &x.Feeds)
		_ = json.Unmarshal([]byte(cs), &x.CookieSelectors)
		x.FullText, x.Screenshot = ft != 0, sh != 0
		e := en != 0
		x.Enabled = &e
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- articles -------------------------------------------------------------

const articleCols = `a.id,a.source_id,a.url,a.canonical_url,a.title,a.summary,a.content,a.author,a.section,a.published_at,a.first_seen_at`

type scanner interface{ Scan(dest ...any) error }

func scanArticle(r scanner, extra ...any) (domain.Article, error) {
	var a domain.Article
	var pub sql.NullInt64
	var seen int64
	dest := append([]any{&a.ID, &a.SourceID, &a.URL, &a.CanonicalURL, &a.Title, &a.Summary, &a.Content, &a.Author, &a.Section, &pub, &seen}, extra...)
	if err := r.Scan(dest...); err != nil {
		return a, err
	}
	a.PublishedAt = fromUnix(pub)
	a.FirstSeenAt = time.Unix(seen, 0).UTC()
	return a, nil
}

// InsertArticle inserts a unless an article with the same canonical URL or the
// same (source, normalized title) already exists. On a duplicate, a.ID is set to
// the existing article's ID and inserted is false.
func (s *SQLite) InsertArticle(ctx context.Context, a *domain.Article) (bool, error) {
	if a.CanonicalURL == "" {
		a.CanonicalURL = a.URL
	}
	if a.URL == "" {
		a.URL = a.CanonicalURL
	}
	if a.CanonicalURL == "" || a.SourceID == "" || strings.TrimSpace(a.Title) == "" {
		return false, errors.New("store: article needs source, url and title")
	}
	if a.FirstSeenAt.IsZero() {
		a.FirstSeenAt = time.Now().UTC()
	}
	hash := titleHash(a.Title)
	var inserted bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
INSERT INTO articles (source_id,url,canonical_url,title,title_hash,summary,content,author,section,published_at,first_seen_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
			a.SourceID, a.URL, a.CanonicalURL, a.Title, hash, a.Summary, a.Content, a.Author, a.Section,
			toUnix(a.PublishedAt), a.FirstSeenAt.Unix())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			a.ID, _ = res.LastInsertId()
			inserted = true
			return nil
		}
		return tx.QueryRowContext(ctx,
			`SELECT id FROM articles WHERE canonical_url=? OR (source_id=? AND title_hash=?) ORDER BY canonical_url=? DESC LIMIT 1`,
			a.CanonicalURL, a.SourceID, hash, a.CanonicalURL).Scan(&a.ID)
	})
	return inserted, err
}

// GetArticle returns the article or sql.ErrNoRows-wrapping error if missing.
func (s *SQLite) GetArticle(ctx context.Context, id int64) (*domain.Article, error) {
	a, err := scanArticle(s.r.QueryRowContext(ctx, `SELECT `+articleCols+` FROM articles a WHERE a.id=?`, id))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

const dateExpr = `COALESCE(a.published_at, a.first_seen_at)`

// where builds the filter clauses on alias "a". Limit/Offset are not handled.
func where(f domain.Filter) (string, []any) {
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
	if !f.From.IsZero() {
		cl = append(cl, dateExpr+" >= ?")
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		cl = append(cl, dateExpr+" <= ?")
		args = append(args, f.To.Unix())
	}
	if len(cl) == 0 {
		return "", nil
	}
	return strings.Join(cl, " AND "), args
}

func strAny(v []string) []any {
	o := make([]any, len(v))
	for i := range v {
		o[i] = v[i]
	}
	return o
}

func isTrivial(f domain.Filter) bool {
	return len(f.SourceIDs) == 0 && len(f.Leanings) == 0 && f.From.IsZero() && f.To.IsZero()
}

// ListArticles lists newest first (by COALESCE(published_at, first_seen_at)).
// Filter.Limit == 0 means no limit. From/To are inclusive.
func (s *SQLite) ListArticles(ctx context.Context, f domain.Filter) ([]domain.Article, error) {
	w, args := where(f)
	q := `SELECT ` + articleCols + ` FROM articles a`
	if w != "" {
		q += " WHERE " + w
	}
	q += ` ORDER BY ` + dateExpr + ` DESC, a.id DESC`
	limit := -1
	if f.Limit > 0 {
		limit = f.Limit
	}
	q += " LIMIT ? OFFSET ?"
	args = append(args, limit, max(f.Offset, 0))
	rows, err := s.r.QueryContext(ctx, q, args...)
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

func (s *SQLite) CountArticles(ctx context.Context, f domain.Filter) (int64, error) {
	w, args := where(f)
	q := `SELECT COUNT(*) FROM articles a`
	if w != "" {
		q += " WHERE " + w
	}
	var n int64
	err := s.r.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}
