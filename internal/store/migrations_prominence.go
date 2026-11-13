package store

// Slot 6: front-page prominence (qué abre cada medio): headlines detected on
// each front-page capture with their position, size and prominence score.
func init() {
	migrations[5] = `
CREATE TABLE frontpage_items (
  id INTEGER PRIMARY KEY,
  screenshot_id INTEGER NOT NULL REFERENCES screenshots(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL,
  taken_at INTEGER NOT NULL,       -- unix seconds UTC (copy of the screenshot's)
  rank INTEGER NOT NULL,           -- 1 = most prominent
  dom_order INTEGER NOT NULL DEFAULT 0,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  canonical_url TEXT NOT NULL DEFAULT '',
  x REAL NOT NULL DEFAULT 0,
  y REAL NOT NULL DEFAULT 0,
  w REAL NOT NULL DEFAULT 0,
  h REAL NOT NULL DEFAULT 0,
  font_px REAL NOT NULL DEFAULT 0,
  prominence REAL NOT NULL DEFAULT 0,
  above_fold INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_frontitems_source_time ON frontpage_items (source_id, taken_at);
CREATE INDEX idx_frontitems_canonical ON frontpage_items (canonical_url);
CREATE INDEX idx_frontitems_shot ON frontpage_items (screenshot_id);
`
}
