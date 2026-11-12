// Package store implements domain.Store on top of SQLite (modernc.org/sqlite,
// pure Go, no CGO): versioned migrations, FTS5 full-text search, and a
// brute-force cosine vector search backed by an in-memory cache.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

var _ domain.Store = (*SQLite)(nil)

// SQLite is the domain.Store implementation.
//
// Concurrency model: all writes go through a single connection (w, with
// BEGIN IMMEDIATE transactions), so writers queue inside database/sql instead
// of colliding with SQLITE_BUSY. Reads use a small separate pool (r) that WAL
// lets run concurrently with the writer. For ":memory:" databases both point
// to the same single connection (every connection would otherwise be its own
// private database).
type SQLite struct {
	w, r *sql.DB
	vecs *vectorIndex
}

const readConns = 4

// pragmas tuned for a Raspberry Pi on an SD card: WAL + synchronous=NORMAL
// (fewer fsyncs), 8 MiB page cache per connection, no mmap, bounded WAL file.
var pragmas = []string{
	"busy_timeout(10000)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"foreign_keys(1)",
	"cache_size(-8192)",
	"temp_store(MEMORY)",
	"mmap_size(0)",
	"journal_size_limit(33554432)",
}

func dsn(path string, extra ...string) string {
	var q []string
	for _, p := range pragmas {
		q = append(q, "_pragma="+p)
	}
	q = append(q, extra...)
	esc := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return "file:" + esc + "?" + strings.Join(q, "&")
}

// Open opens (creating if needed) the database at path and applies pending
// migrations. Use ":memory:" for an ephemeral database.
func Open(path string) (*SQLite, error) {
	mem := path == ":memory:" || path == ""
	if mem {
		path = ":memory:"
	}
	w, err := sql.Open("sqlite", dsn(path, "_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	s := &SQLite{w: w, r: w, vecs: newVectorIndex()}
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(context.Background(), w); err != nil {
		w.Close()
		return nil, err
	}
	if !mem {
		r, err := sql.Open("sqlite", dsn(path, "_pragma=query_only(1)"))
		if err != nil {
			w.Close()
			return nil, err
		}
		r.SetMaxOpenConns(readConns)
		r.SetMaxIdleConns(readConns)
		r.SetConnMaxLifetime(0)
		s.r = r
	}
	return s, nil
}

// Close closes the database handles.
func (s *SQLite) Close() error {
	var err error
	if s.r != s.w {
		err = s.r.Close()
	}
	if e := s.w.Close(); err == nil {
		err = e
	}
	return err
}

func (s *SQLite) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

var _ domain.AnnotationStore = (*SQLite)(nil)
