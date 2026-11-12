package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.DigestStore = (*SQLite)(nil)

var digestDayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// SaveDigest inserts or overwrites the digest of d.Day.
func (s *SQLite) SaveDigest(ctx context.Context, d *domain.Digest) error {
	if d == nil || !digestDayRe.MatchString(d.Day) {
		return errors.New("store: digest needs a day YYYY-MM-DD")
	}
	if d.GeneratedAt.IsZero() {
		d.GeneratedAt = time.Now().UTC()
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `
INSERT INTO digests (day,generated_at,model,json) VALUES (?,?,?,?)
ON CONFLICT(day) DO UPDATE SET generated_at=excluded.generated_at,model=excluded.model,json=excluded.json`,
		d.Day, d.GeneratedAt.Unix(), d.Model, b)
	return err
}

func scanDigest(row interface{ Scan(...any) error }) (*domain.Digest, error) {
	var b []byte
	var day string
	var at int64
	if err := row.Scan(&day, &at, &b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrDigestNotFound
		}
		return nil, err
	}
	var d domain.Digest
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("store: digest %s: %w", day, err)
	}
	d.Day = day
	d.GeneratedAt = time.Unix(at, 0).UTC()
	return &d, nil
}

func (s *SQLite) GetDigest(ctx context.Context, day string) (*domain.Digest, error) {
	return scanDigest(s.r.QueryRowContext(ctx, `SELECT day,generated_at,json FROM digests WHERE day=?`, day))
}

func (s *SQLite) LatestDigest(ctx context.Context) (*domain.Digest, error) {
	return scanDigest(s.r.QueryRowContext(ctx, `SELECT day,generated_at,json FROM digests ORDER BY day DESC LIMIT 1`))
}

func (s *SQLite) ListDigests(ctx context.Context, limit int) ([]domain.Digest, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.r.QueryContext(ctx, `SELECT day,generated_at,json FROM digests ORDER BY day DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Digest
	for rows.Next() {
		d, err := scanDigest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
