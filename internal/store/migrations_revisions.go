package store

// Migration slot 3: headline/summary revisions (cambios de titulares).
func init() {
	migrations[2] = `
CREATE TABLE article_revisions (
  id INTEGER PRIMARY KEY,
  article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,              -- 'titular' | 'resumen'
  old_text TEXT NOT NULL,
  new_text TEXT NOT NULL,
  changed_at INTEGER NOT NULL,     -- unix seconds UTC: when the change was detected
  seen_at INTEGER NOT NULL,        -- unix seconds UTC: when the article was first seen
  old_hash TEXT NOT NULL DEFAULT '', -- hash chain over normalized text (anti A->B->A flapping)
  new_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_revisions_article ON article_revisions (article_id);
CREATE INDEX idx_revisions_changed ON article_revisions (changed_at DESC);
`
}
