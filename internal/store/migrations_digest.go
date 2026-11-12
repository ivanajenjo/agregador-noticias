package store

func init() {
	// 5: digests (resumen diario). One row per local day; regenerate overwrites.
	migrations[4] = `
CREATE TABLE digests (
  id INTEGER PRIMARY KEY,
  day TEXT NOT NULL UNIQUE,      -- YYYY-MM-DD (local)
  generated_at INTEGER NOT NULL, -- unix seconds UTC
  model TEXT NOT NULL DEFAULT '',
  json BLOB NOT NULL
);
`
}
