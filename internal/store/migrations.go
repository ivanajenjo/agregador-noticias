package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; the schema version is PRAGMA user_version.
// Never edit an applied migration: append a new one.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  leaning TEXT NOT NULL DEFAULT '',
  homepage TEXT NOT NULL DEFAULT '',
  feeds_json TEXT NOT NULL DEFAULT '[]',
  full_text INTEGER NOT NULL DEFAULT 0,
  screenshot INTEGER NOT NULL DEFAULT 0,
  cookie_selectors_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE articles (
  id INTEGER PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  canonical_url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  title_hash TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  section TEXT NOT NULL DEFAULT '',
  published_at INTEGER,            -- unix seconds UTC, NULL if unknown
  first_seen_at INTEGER NOT NULL,  -- unix seconds UTC
  UNIQUE (source_id, title_hash)
);
CREATE INDEX idx_articles_date ON articles (COALESCE(published_at, first_seen_at) DESC, id DESC);
CREATE INDEX idx_articles_source_date ON articles (source_id, COALESCE(published_at, first_seen_at) DESC);

-- External-content FTS5 index over title+summary+content. unicode61 with
-- remove_diacritics 2 makes "educación" match "educacion" (and ñ/n).
-- No porter stemmer: it is English-only.
CREATE VIRTUAL TABLE articles_fts USING fts5(
  title, summary, content,
  content='articles', content_rowid='id',
  tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER articles_ai AFTER INSERT ON articles BEGIN
  INSERT INTO articles_fts(rowid, title, summary, content) VALUES (new.id, new.title, new.summary, new.content);
END;
CREATE TRIGGER articles_ad AFTER DELETE ON articles BEGIN
  INSERT INTO articles_fts(articles_fts, rowid, title, summary, content) VALUES ('delete', old.id, old.title, old.summary, old.content);
END;
CREATE TRIGGER articles_au AFTER UPDATE ON articles BEGIN
  INSERT INTO articles_fts(articles_fts, rowid, title, summary, content) VALUES ('delete', old.id, old.title, old.summary, old.content);
  INSERT INTO articles_fts(rowid, title, summary, content) VALUES (new.id, new.title, new.summary, new.content);
END;

-- One row per (article, model) once embedded (even if it produced 0 chunks),
-- so empty articles are not retried forever.
CREATE TABLE article_embeds (
  article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  chunks INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (article_id, model)
) WITHOUT ROWID;

CREATE TABLE article_chunks (
  article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  idx INTEGER NOT NULL,
  text TEXT NOT NULL,
  dim INTEGER NOT NULL,
  vec BLOB NOT NULL,               -- float32 little endian, dim*4 bytes
  PRIMARY KEY (article_id, model, idx)
) WITHOUT ROWID;
CREATE INDEX idx_chunks_model ON article_chunks (model);

CREATE TABLE screenshots (
  id INTEGER PRIMARY KEY,
  source_id TEXT NOT NULL,
  taken_at INTEGER NOT NULL,
  path TEXT NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_shots_source_time ON screenshots (source_id, taken_at);
CREATE INDEX idx_shots_time ON screenshots (taken_at);

CREATE TABLE feed_state (
  source_id TEXT NOT NULL,
  feed_url TEXT NOT NULL,
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  last_ok INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  last_error_at INTEGER,
  last_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (source_id, feed_url)
) WITHOUT ROWID;

CREATE TABLE job_runs (
  id INTEGER PRIMARY KEY,
  job TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  ok INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_jobs_started ON job_runs (started_at DESC);
`,
	// 2: article annotations (bias/intent/tone labels from small models)
	`
CREATE TABLE article_annotations (
  article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  model TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  score REAL NOT NULL DEFAULT 0,
  data TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (article_id, kind, model)
) WITHOUT ROWID;
CREATE INDEX idx_annotations_kind_label ON article_annotations (kind, model, label);
`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	var cur int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return fmt.Errorf("store: read user_version: %w", err)
	}
	if cur > len(migrations) {
		return fmt.Errorf("store: database schema v%d is newer than this binary (v%d)", cur, len(migrations))
	}
	for v := cur; v < len(migrations); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %d: set version: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
