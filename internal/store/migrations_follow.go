package store

// Slot 4: seguimientos (watches) and the alert log.
func init() {
	migrations[3] = `
CREATE TABLE watches (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  query TEXT NOT NULL,
  source_ids_json TEXT NOT NULL DEFAULT '[]',
  leanings_json TEXT NOT NULL DEFAULT '[]',
  notify INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  last_eval_at INTEGER
);

-- Articles already reported for a watch (dedup of watch alerts).
CREATE TABLE watch_hits (
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
  seen_at INTEGER NOT NULL,
  PRIMARY KEY (watch_id, article_id)
) WITHOUT ROWID;
CREATE INDEX idx_watch_hits_seen ON watch_hits (seen_at);

CREATE TABLE alert_log (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  key TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  sent_at INTEGER NOT NULL,
  channel TEXT NOT NULL,
  ok INTEGER NOT NULL DEFAULT 0,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_alert_dedup ON alert_log (kind, key, channel);
CREATE INDEX idx_alert_sent ON alert_log (sent_at DESC);
`
}
