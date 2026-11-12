package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func count(t *testing.T, s *SQLite, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedRetention inserts n old and m recent embedded+annotated articles.
func seedRetention(t *testing.T, s *SQLite, old, recent int) (oldIDs, newIDs []int64) {
	t.Helper()
	oldT := time.Now().Add(-100 * 24 * time.Hour)
	newT := time.Now().Add(-1 * time.Hour)
	mk := func(i int, seen time.Time, tag string) int64 {
		id := ins(t, s, domain.Article{SourceID: "a", URL: fmt.Sprintf("http://x/%s/%d", tag, i), CanonicalURL: fmt.Sprintf("http://x/%s/%d", tag, i),
			Title: fmt.Sprintf("titular %s%d unicornio", tag, i), Summary: "res", Content: "contenido", FirstSeenAt: seen})
		must(t, s.SaveEmbeddings(ctx, id, "m", []domain.ChunkVec{cv(0, "c0", vec(1, 0)), cv(1, "c1", vec(0, 1))}))
		must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: id, Kind: "tone", Model: "m", Label: "neutral"}))
		return id
	}
	for i := 0; i < old; i++ {
		oldIDs = append(oldIDs, mk(i, oldT, "old"))
	}
	for i := 0; i < recent; i++ {
		newIDs = append(newIDs, mk(i, newT, "new"))
	}
	return
}

func TestRetentionCascadeAndCache(t *testing.T) {
	s := newStore(t)
	oldIDs, newIDs := seedRetention(t, s, 5, 3)
	// warm the vector cache
	if _, err := s.SearchVector(ctx, vec(1, 0), "m", domain.Filter{}, 20); err != nil {
		t.Fatal(err)
	}
	cut := time.Now().Add(-30 * 24 * time.Hour)
	if n, err := s.CountArticlesBefore(ctx, cut); err != nil || n != 5 {
		t.Fatalf("dry run: %d %v", n, err)
	}
	n, err := s.DeleteArticlesBefore(ctx, cut, 2) // 3 batches
	if err != nil || n != 5 {
		t.Fatalf("deleted %d err %v", n, err)
	}
	for name, q := range map[string]string{
		"articles": `SELECT COUNT(*) FROM articles`,
		"chunks":   `SELECT COUNT(*) FROM article_chunks`,
		"embeds":   `SELECT COUNT(*) FROM article_embeds`,
		"annot":    `SELECT COUNT(*) FROM article_annotations`,
	} {
		want := 3
		if name == "chunks" {
			want = 6
		}
		if got := count(t, s, q); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	// FTS: old rows gone, new rows still found
	if got := count(t, s, `SELECT COUNT(*) FROM articles_fts WHERE articles_fts MATCH 'unicornio'`); got != 3 {
		t.Errorf("fts matches %d, want 3", got)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM articles_fts WHERE rowid IN (`+idList(oldIDs)+`)`); got != 0 {
		t.Errorf("fts still has %d old rows", got)
	}
	must(t, func() error {
		_, err := s.w.ExecContext(ctx, `INSERT INTO articles_fts(articles_fts) VALUES('integrity-check')`)
		return err
	}())
	// vector cache
	hits, err := s.SearchVector(ctx, vec(1, 0), "m", domain.Filter{}, 20)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits %d err %v", len(hits), err)
	}
	got := map[int64]bool{}
	for _, h := range hits {
		got[h.Article.ID] = true
	}
	for _, id := range newIDs {
		if !got[id] {
			t.Errorf("recent article %d missing from vector search", id)
		}
	}
	s.vecs.mu.RLock()
	entries := len(s.vecs.models["m"].entries)
	posn := len(s.vecs.models["m"].pos)
	s.vecs.mu.RUnlock()
	if entries != 3 || posn != 3 {
		t.Errorf("cache holds %d entries / %d pos, want 3", entries, posn)
	}
	// idempotent
	if n, err := s.DeleteArticlesBefore(ctx, cut, 2); err != nil || n != 0 {
		t.Errorf("second run: %d %v", n, err)
	}
}

func idList(ids []int64) string {
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprint(id)
	}
	return s
}

func TestMaintain(t *testing.T) {
	s := newStore(t)
	seedRetention(t, s, 4, 2)
	res, err := s.Maintain(ctx, 30*24*time.Hour)
	if err != nil || res.Deleted != 4 || !res.WALCheckpoint {
		t.Fatalf("%+v %v", res, err)
	}
	if res.AutoVacuum != 2 {
		t.Errorf("fresh DB should use incremental auto_vacuum, got %d", res.AutoVacuum)
	}
	if res.DBBytes <= 0 {
		t.Errorf("DBBytes %d", res.DBBytes)
	}
	// keep-forever mode only does upkeep
	res, err = s.Maintain(ctx, 0)
	if err != nil || res.Deleted != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM articles`); got != 2 {
		t.Errorf("articles %d", got)
	}
}

func TestMaintainReclaimsSpace(t *testing.T) {
	s := newStore(t)
	// enough bulky rows to leave > vacuumMinFreePages free pages
	big := make([]byte, 6000)
	for i := range big {
		big[i] = 'a' + byte(i%26)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < 800; i++ {
		ins(t, s, domain.Article{SourceID: "a", URL: fmt.Sprintf("http://b/%d", i), CanonicalURL: fmt.Sprintf("http://b/%d", i),
			Title: fmt.Sprintf("t%d", i), Content: string(big) + fmt.Sprint(i), FirstSeenAt: old})
	}
	res, err := s.Maintain(ctx, 30*24*time.Hour)
	if err != nil || res.Deleted != 800 {
		t.Fatalf("%+v %v", res, err)
	}
	if res.ReclaimedPages == 0 {
		t.Errorf("no pages reclaimed: %+v", res)
	}
	if res.FreelistPages > 0 && res.ReclaimedPages > incrementalVacuumPages {
		t.Errorf("vacuum not bounded: %+v", res)
	}
}

func TestExistingDBKeepsAutoVacuumMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path) // reopen: initAutoVacuum is skipped (user_version != 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var av int
	_ = s.w.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&av)
	if av != 2 {
		t.Errorf("auto_vacuum %d", av)
	}
}

func TestPing(t *testing.T) {
	s := newStore(t)
	if err := s.PingRead(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.PingWrite(ctx); err != nil {
		t.Fatal(err)
	}
}
