package store

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var ctx = context.Background()

func newStore(t testing.TB) *SQLite {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	seed(t, s)
	return s
}

func seed(t testing.TB, s *SQLite) {
	t.Helper()
	if err := s.UpsertSources(ctx, []domain.Source{
		{ID: "a", Name: "A", Leaning: "izquierda", Feeds: []domain.Feed{{URL: "http://a/rss", Section: "x"}}},
		{ID: "b", Name: "B", Leaning: "derecha", CookieSelectors: []string{"#ok"}},
		{ID: "c", Name: "C", Leaning: "centro"},
	}); err != nil {
		t.Fatal(err)
	}
}

func day(n int) time.Time { return time.Date(2025, 1, n, 12, 0, 0, 0, time.UTC) }

func ins(t testing.TB, s *SQLite, a domain.Article) int64 {
	t.Helper()
	ok, err := s.InsertArticle(ctx, &a)
	if err != nil || !ok {
		t.Fatalf("insert %q: ok=%v err=%v", a.Title, ok, err)
	}
	return a.ID
}

func TestSources(t *testing.T) {
	s := newStore(t)
	got, err := s.ListSources(ctx)
	if err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	if got[0].Feeds[0].URL != "http://a/rss" || got[1].CookieSelectors[0] != "#ok" || !got[0].IsEnabled() {
		t.Fatalf("%+v", got)
	}
	f := false
	_ = s.UpsertSources(ctx, []domain.Source{{ID: "a", Name: "A2", Enabled: &f}})
	got, _ = s.ListSources(ctx)
	if got[0].Name != "A2" || got[0].IsEnabled() {
		t.Fatalf("%+v", got[0])
	}
}

func TestDedup(t *testing.T) {
	s := newStore(t)
	id := ins(t, s, domain.Article{SourceID: "a", URL: "http://a/1", CanonicalURL: "http://a/1", Title: "¡Gran Noticia: Educación!"})
	tests := []struct {
		name string
		a    domain.Article
		want bool
	}{
		{"same canonical", domain.Article{SourceID: "a", URL: "http://a/1?x", CanonicalURL: "http://a/1", Title: "otra"}, false},
		{"same canonical other source", domain.Article{SourceID: "b", URL: "http://a/1", CanonicalURL: "http://a/1", Title: "x"}, false},
		{"normalized title same source", domain.Article{SourceID: "a", CanonicalURL: "http://a/2", Title: "gran noticia educacion"}, false},
		{"same title other source", domain.Article{SourceID: "b", CanonicalURL: "http://b/2", Title: "Gran noticia: educación"}, true},
		{"new", domain.Article{SourceID: "a", CanonicalURL: "http://a/3", Title: "distinta"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.a
			got, err := s.InsertArticle(ctx, &a)
			if err != nil || got != tc.want {
				t.Fatalf("inserted=%v err=%v", got, err)
			}
			if !got && a.ID != id {
				t.Fatalf("duplicate should report existing id %d, got %d", id, a.ID)
			}
		})
	}
	if n, _ := s.CountArticles(ctx, domain.Filter{}); n != 3 {
		t.Fatalf("count %d", n)
	}
	if got, _ := s.GetArticle(ctx, id); got.Title != "¡Gran Noticia: Educación!" || got.FirstSeenAt.IsZero() || !got.PublishedAt.IsZero() {
		t.Fatalf("%+v", got)
	}
}

func TestFilters(t *testing.T) {
	s := newStore(t)
	ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "uno", PublishedAt: day(1)})
	ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u2", Title: "dos", PublishedAt: day(5)})
	ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u3", Title: "tres", PublishedAt: day(10)})
	ins(t, s, domain.Article{SourceID: "c", CanonicalURL: "u4", Title: "cuatro", FirstSeenAt: day(15)}) // no published_at
	tests := []struct {
		name string
		f    domain.Filter
		want []string
	}{
		{"all newest first", domain.Filter{}, []string{"cuatro", "tres", "dos", "uno"}},
		{"source", domain.Filter{SourceIDs: []string{"a"}}, []string{"dos", "uno"}},
		{"sources", domain.Filter{SourceIDs: []string{"a", "c"}}, []string{"cuatro", "dos", "uno"}},
		{"leaning", domain.Filter{Leanings: []string{"derecha"}}, []string{"tres"}},
		{"from", domain.Filter{From: day(5)}, []string{"cuatro", "tres", "dos"}},
		{"to", domain.Filter{To: day(5)}, []string{"dos", "uno"}},
		{"range uses first_seen fallback", domain.Filter{From: day(12), To: day(20)}, []string{"cuatro"}},
		{"limit offset", domain.Filter{Limit: 2, Offset: 1}, []string{"tres", "dos"}},
		{"combined", domain.Filter{Leanings: []string{"izquierda"}, From: day(2)}, []string{"dos"}},
		{"none", domain.Filter{SourceIDs: []string{"zzz"}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListArticles(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, a := range got {
				titles = append(titles, a.Title)
			}
			if strings.Join(titles, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v want %v", titles, tc.want)
			}
			cf := tc.f
			cf.Limit, cf.Offset = 0, 0
			n, _ := s.CountArticles(ctx, cf)
			if tc.f.Limit == 0 && int(n) != len(tc.want) {
				t.Fatalf("count %d", n)
			}
		})
	}
}

func TestSearchText(t *testing.T) {
	s := newStore(t)
	ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "Reforma de la educación pública", Summary: "El gobierno anuncia cambios", PublishedAt: day(1)})
	ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u2", Title: "Economía", Content: "La educacion y la sanidad centran el debate <script>", PublishedAt: day(2)})
	ins(t, s, domain.Article{SourceID: "c", CanonicalURL: "u3", Title: "Fútbol: el año del campeón", Summary: "España", PublishedAt: day(3)})
	tests := []struct {
		q    string
		f    domain.Filter
		want int
		top  string
	}{
		{"educacion", domain.Filter{}, 2, "Reforma de la educación pública"}, // title weighs more
		{"EDUCACIÓN", domain.Filter{}, 2, ""},
		{"campeon", domain.Filter{}, 1, "Fútbol: el año del campeón"},
		{"ano", domain.Filter{}, 1, ""}, // ñ folds to n? "año" -> "ano" with diacritics 2
		{"educacion", domain.Filter{SourceIDs: []string{"b"}}, 1, "Economía"},
		{"educacion", domain.Filter{Leanings: []string{"izquierda"}}, 1, ""},
		{"educacion", domain.Filter{From: day(2)}, 1, "Economía"},
		{"educ", domain.Filter{}, 2, ""}, // prefix on last token
		{`" OR ) ( * NEAR AND :`, domain.Filter{}, 0, ""},
		{`title:educacion "`, domain.Filter{}, 2, ""},
		{"", domain.Filter{}, 0, ""},
		{"   !!! ", domain.Filter{}, 0, ""},
		{"el que de la", domain.Filter{}, 3, ""}, // only stopwords -> falls back to them, still safe
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			hits, err := s.SearchText(ctx, tc.q, tc.f, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != tc.want {
				t.Fatalf("got %d hits want %d", len(hits), tc.want)
			}
			if tc.top != "" && hits[0].Article.Title != tc.top {
				t.Fatalf("top %q", hits[0].Article.Title)
			}
			for _, h := range hits {
				if h.Chunk != -1 || h.Score == 0 && false {
					t.Fatal("bad hit")
				}
			}
		})
	}
	hits, _ := s.SearchText(ctx, "sanidad", domain.Filter{}, 5)
	if len(hits) != 1 || !strings.Contains(hits[0].Snippet, "<mark>sanidad</mark>") || strings.Contains(hits[0].Snippet, "<script>") {
		t.Fatalf("snippet %q", hits[0].Snippet)
	}
	if h, _ := s.SearchText(ctx, "educacion", domain.Filter{Offset: 1}, 5); len(h) != 1 {
		t.Fatalf("offset: %d", len(h))
	}
}

func TestFTSTracksUpdatesAndDeletes(t *testing.T) {
	s := newStore(t)
	id := ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "hola mundo"})
	if _, err := s.w.ExecContext(ctx, `UPDATE articles SET title='adios planeta' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.SearchText(ctx, "hola", domain.Filter{}, 5); len(h) != 0 {
		t.Fatal("stale fts")
	}
	if h, _ := s.SearchText(ctx, "planeta", domain.Filter{}, 5); len(h) != 1 {
		t.Fatal("missing fts")
	}
	s.w.ExecContext(ctx, `DELETE FROM articles WHERE id=?`, id)
	if h, _ := s.SearchText(ctx, "planeta", domain.Filter{}, 5); len(h) != 0 {
		t.Fatal("deleted still indexed")
	}
}

func vec(x ...float32) []float32 { return x }

func cv(i int, text string, v []float32) domain.ChunkVec {
	return domain.ChunkVec{Index: i, Text: text, Vec: v}
}

func TestVectorSearchAndCache(t *testing.T) {
	s := newStore(t)
	a1 := ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "t1", PublishedAt: day(1)})
	a2 := ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u2", Title: "t2", PublishedAt: day(2)})
	a3 := ins(t, s, domain.Article{SourceID: "c", CanonicalURL: "u3", Title: "t3", PublishedAt: day(3)})

	if n, _ := s.ArticlesNeedingEmbedding(ctx, "m", 0); len(n) != 3 || n[0].ID != a3 {
		t.Fatalf("pending %v", n)
	}
	// a1: two chunks, second is the best match for query (1,0)
	must(t, s.SaveEmbeddings(ctx, a1, "m", []domain.ChunkVec{cv(0, "c0", vec(0, 1)), cv(1, "c1", vec(10, 1))}))
	must(t, s.SaveEmbeddings(ctx, a2, "m", []domain.ChunkVec{cv(0, "b0", vec(1, 1))}))
	must(t, s.SaveEmbeddings(ctx, a3, "other", []domain.ChunkVec{cv(0, "o", vec(1, 0))})) // other model
	if n, _ := s.ArticlesNeedingEmbedding(ctx, "m", 0); len(n) != 1 || n[0].ID != a3 {
		t.Fatalf("pending after %v", n)
	}

	hits, err := s.SearchVector(ctx, vec(2, 0), "m", domain.Filter{}, 10)
	must(t, err)
	if len(hits) != 2 || hits[0].Article.ID != a1 || hits[0].Chunk != 1 || hits[0].Snippet != "c1" || hits[1].Article.ID != a2 {
		t.Fatalf("%+v", hits)
	}
	if math.Abs(hits[0].Score-10/math.Sqrt(101)) > 1e-5 || math.Abs(hits[1].Score-math.Sqrt(0.5)) > 1e-5 {
		t.Fatalf("scores %v %v", hits[0].Score, hits[1].Score)
	}
	if h, _ := s.SearchVector(ctx, vec(2, 0), "m", domain.Filter{}, 1); len(h) != 1 || h[0].Article.ID != a1 {
		t.Fatal("k")
	}
	if h, _ := s.SearchVector(ctx, vec(2, 0), "m", domain.Filter{SourceIDs: []string{"b"}}, 5); len(h) != 1 || h[0].Article.ID != a2 {
		t.Fatal("filter source")
	}
	if h, _ := s.SearchVector(ctx, vec(2, 0), "m", domain.Filter{Leanings: []string{"izquierda"}, From: day(2)}, 5); len(h) != 0 {
		t.Fatal("filter combined")
	}
	if h, _ := s.SearchVector(ctx, vec(1, 0, 0), "m", domain.Filter{}, 5); len(h) != 0 {
		t.Fatal("dim mismatch should match nothing")
	}
	if h, _ := s.SearchVector(ctx, vec(1, 0), "nomodel", domain.Filter{}, 5); len(h) != 0 {
		t.Fatal("unknown model")
	}

	// cache is warm: a DB-level change is not seen (proves no re-read) ...
	s.w.ExecContext(ctx, `DELETE FROM article_chunks WHERE article_id=?`, a2)
	if h, _ := s.SearchVector(ctx, vec(2, 0), "m", domain.Filter{SourceIDs: []string{"b"}}, 5); len(h) != 1 {
		t.Fatal("expected cached result")
	}
	// ... but SaveEmbeddings updates it in place.
	must(t, s.SaveEmbeddings(ctx, a2, "m", []domain.ChunkVec{cv(0, "new", vec(5, 0))}))
	must(t, s.SaveEmbeddings(ctx, a1, "m", []domain.ChunkVec{cv(0, "x", vec(0, 1))}))
	hits, _ = s.SearchVector(ctx, vec(1, 0), "m", domain.Filter{}, 5)
	if len(hits) != 2 || hits[0].Article.ID != a2 || hits[0].Snippet != "new" || hits[1].Chunk != 0 || hits[1].Score > 1e-6 {
		t.Fatalf("after replace %+v", hits)
	}
	// Reopen: cache is rebuilt from disk with identical results.
	path := filepath.Join(t.TempDir(), "re.db")
	s2, _ := Open(path)
	seed(t, s2)
	b := ins(t, s2, domain.Article{SourceID: "a", CanonicalURL: "z", Title: "z"})
	must(t, s2.SaveEmbeddings(ctx, b, "m", []domain.ChunkVec{cv(0, "z", vec(1, 2))}))
	s2.Close()
	s2, _ = Open(path)
	defer s2.Close()
	if h, _ := s2.SearchVector(ctx, vec(1, 2), "m", domain.Filter{}, 3); len(h) != 1 || h[0].Score < 0.999 {
		t.Fatalf("reload %+v", h)
	}
	// empty chunk list marks embedded and removes vectors
	must(t, s2.SaveEmbeddings(ctx, b, "m", nil))
	if h, _ := s2.SearchVector(ctx, vec(1, 2), "m", domain.Filter{}, 3); len(h) != 0 {
		t.Fatal("expected empty")
	}
	if n, _ := s2.ArticlesNeedingEmbedding(ctx, "m", 0); len(n) != 0 {
		t.Fatal("empty-embedded article should not be pending")
	}
	if err := s.SaveEmbeddings(ctx, a1, "m", []domain.ChunkVec{cv(0, "x", vec(1)), cv(1, "y", vec(1, 2))}); err == nil {
		t.Fatal("expected dim error")
	}
	if err := s.SaveEmbeddings(ctx, 9999, "m", []domain.ChunkVec{cv(0, "x", vec(1))}); err == nil {
		t.Fatal("expected FK error")
	}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatsScreenshotsFeedsJobs(t *testing.T) {
	s := newStore(t)
	a := ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "x", PublishedAt: day(1)})
	ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u2", Title: "y", PublishedAt: day(3)})
	must(t, s.SaveEmbeddings(ctx, a, "m", []domain.ChunkVec{cv(0, "x", vec(1))}))
	for i, d := range []int{1, 2, 3} {
		sh := domain.Screenshot{SourceID: "a", TakenAt: day(d), Path: fmt.Sprintf("a/%d.jpg", i), Bytes: 100}
		must(t, s.SaveScreenshot(ctx, &sh))
		if sh.ID == 0 {
			t.Fatal("id")
		}
	}
	st, err := s.Stats(ctx, "m")
	must(t, err)
	if st.Articles != 2 || st.ArticlesBySource["a"] != 1 || st.Embedded != 1 || st.PendingEmbed != 1 ||
		st.Screenshots != 3 || st.ShotsBytes != 300 || st.DBBytes <= 0 || !st.OldestArticle.Equal(day(1)) || !st.NewestArticle.Equal(day(3)) {
		t.Fatalf("%+v", st)
	}
	if l, _ := s.ListScreenshots(ctx, "a", day(2), time.Time{}); len(l) != 2 || l[0].Path != "a/1.jpg" {
		t.Fatalf("%+v", l)
	}
	paths, err := s.DeleteScreenshotsBefore(ctx, day(3))
	if err != nil || len(paths) != 2 {
		t.Fatal(paths, err)
	}
	if l, _ := s.ListScreenshots(ctx, "", time.Time{}, time.Time{}); len(l) != 1 {
		t.Fatal("left")
	}

	if fs, err := s.GetFeedState(ctx, "a", "f"); fs != nil || err != nil {
		t.Fatal(fs, err)
	}
	fs := &domain.FeedState{SourceID: "a", FeedURL: "f", ETag: "e", LastOK: day(1), LastCount: 4}
	must(t, s.SaveFeedState(ctx, fs))
	fs.LastError, fs.LastErrorAt = "boom", day(2)
	must(t, s.SaveFeedState(ctx, fs))
	got, _ := s.GetFeedState(ctx, "a", "f")
	if got.ETag != "e" || got.LastError != "boom" || !got.LastErrorAt.Equal(day(2)) || !got.LastOK.Equal(day(1)) {
		t.Fatalf("%+v", got)
	}
	if l, _ := s.ListFeedStates(ctx); len(l) != 1 {
		t.Fatal("list")
	}

	j1, _ := s.StartJob(ctx, "ingest")
	j2, _ := s.StartJob(ctx, "embed")
	must(t, s.FinishJob(ctx, j1, true, "ok"))
	jobs, _ := s.ListJobRuns(ctx, 10)
	if len(jobs) != 2 || jobs[0].ID != j2 || !jobs[0].FinishedAt.IsZero() || !jobs[1].OK || jobs[1].Message != "ok" {
		t.Fatalf("%+v", jobs)
	}
	if s.FinishJob(ctx, 999, true, "") == nil {
		t.Fatal("unknown job")
	}
}

func TestAnnotations(t *testing.T) {
	s := newStore(t)
	a := ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u1", Title: "x", PublishedAt: day(1)})
	b := ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u2", Title: "y", PublishedAt: day(2)})
	c := ins(t, s, domain.Article{SourceID: "b", CanonicalURL: "u3", Title: "z", PublishedAt: day(3)})
	if n, _ := s.ArticlesNeedingAnnotation(ctx, "bias", "m", 0); len(n) != 3 || n[0].ID != c {
		t.Fatal("pending")
	}
	must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: a, Kind: "bias", Model: "m", Label: "izq", Score: .8, Data: `{"k":1}`}))
	must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: b, Kind: "bias", Model: "m", Label: "der"}))
	must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: c, Kind: "bias", Model: "m", Label: "der"}))
	must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: a, Kind: "bias", Model: "m", Label: "centro", Score: .5})) // replace
	must(t, s.SaveAnnotation(ctx, domain.Annotation{ArticleID: a, Kind: "tone", Model: "m", Label: "neutral"}))
	l, _ := s.ListAnnotations(ctx, a)
	if len(l) != 2 || l[0].Kind != "bias" || l[0].Label != "centro" || l[0].CreatedAt.IsZero() || l[1].Kind != "tone" {
		t.Fatalf("%+v", l)
	}
	if n, _ := s.ArticlesNeedingAnnotation(ctx, "bias", "m", 0); len(n) != 0 {
		t.Fatal("none pending")
	}
	if n, _ := s.ArticlesNeedingAnnotation(ctx, "tone", "m", 2); len(n) != 2 {
		t.Fatal("limit")
	}
	cnt, err := s.CountAnnotationsByLabel(ctx, "bias", "m", domain.Filter{})
	if err != nil || cnt["der"] != 2 || cnt["centro"] != 1 || len(cnt) != 2 {
		t.Fatal(cnt, err)
	}
	cnt, _ = s.CountAnnotationsByLabel(ctx, "bias", "m", domain.Filter{SourceIDs: []string{"b"}, From: day(3)})
	if len(cnt) != 1 || cnt["der"] != 1 {
		t.Fatal(cnt)
	}
	if s.SaveAnnotation(ctx, domain.Annotation{ArticleID: a}) == nil {
		t.Fatal("need kind")
	}
	s.w.ExecContext(ctx, `DELETE FROM articles WHERE id=?`, a)
	if l, _ := s.ListAnnotations(ctx, a); len(l) != 0 {
		t.Fatal("cascade")
	}
}

func TestMigrationsIdempotentOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	must(t, err)
	seed(t, s)
	id := ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u", Title: "persist"})
	var v int
	s.r.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("version %d", v)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = Open(path)
		must(t, err)
		if a, err := s.GetArticle(ctx, id); err != nil || a.Title != "persist" {
			t.Fatal(a, err)
		}
		if h, _ := s.SearchText(ctx, "persist", domain.Filter{}, 3); len(h) != 1 {
			t.Fatal("fts after reopen")
		}
		s.Close()
	}
	// newer schema is refused
	s, _ = Open(path)
	s.w.Exec(fmt.Sprintf("PRAGMA user_version=%d", len(migrations)+1))
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for newer schema")
	}
}

func TestMemoryDB(t *testing.T) {
	s, err := Open(":memory:")
	must(t, err)
	defer s.Close()
	seed(t, s)
	ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "u", Title: "hola"})
	if h, _ := s.SearchText(ctx, "hola", domain.Filter{}, 3); len(h) != 1 {
		t.Fatal("memory fts")
	}
}

func TestConcurrency(t *testing.T) {
	s := newStore(t)
	var ids []int64
	for i := 0; i < 20; i++ {
		ids = append(ids, ins(t, s, domain.Article{SourceID: "a", CanonicalURL: fmt.Sprint("u", i), Title: fmt.Sprint("titulo ", i, " educación")}))
	}
	var wg sync.WaitGroup
	errc := make(chan error, 100)
	run := func(f func(i int) error, n int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				if err := f(i); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	for w := 0; w < 3; w++ {
		w := w
		run(func(i int) error {
			a := domain.Article{SourceID: "b", CanonicalURL: fmt.Sprint("w", w, "-", i), Title: fmt.Sprint("w", w, " n", i)}
			_, err := s.InsertArticle(ctx, &a)
			return err
		}, 30)
		run(func(i int) error {
			return s.SaveEmbeddings(ctx, ids[(i+w)%len(ids)], "m", []domain.ChunkVec{cv(0, "t", vec(float32(i), float32(w), 1))})
		}, 30)
	}
	for r := 0; r < 4; r++ {
		run(func(i int) error {
			if _, err := s.SearchVector(ctx, vec(1, 1, 1), "m", domain.Filter{SourceIDs: []string{"a"}}, 5); err != nil {
				return err
			}
			if _, err := s.SearchText(ctx, "educacion", domain.Filter{}, 5); err != nil {
				return err
			}
			_, err := s.Stats(ctx, "m")
			return err
		}, 30)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if n, _ := s.CountArticles(ctx, domain.Filter{}); n != 20+90 {
		t.Fatalf("count %d", n)
	}
}

func TestFTSQuery(t *testing.T) {
	for in, want := range map[string]string{
		"educación pública": `"educación" OR "pública"*`,
		`a "b" -c`:          `"b" OR "c"*`,
		"el de la":          `"el" OR "de" OR "la"*`,
		"el gato":           `"gato"*`,
		"":                  "",
		`"""`:               "",
	} {
		if got := ftsQuery(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := normalizeTitle("  ¡Ñandú, el AÑO   pasado: 2025! "); got != "nandu el ano pasado 2025" {
		t.Fatal(got)
	}
}

// BenchmarkSearchVector50k: 50k articles x 768 dims, one chunk each.
func BenchmarkSearchVector50k(b *testing.B) {
	idx := newVectorIndex()
	const n, dim = 50000, 768
	rng := rand.New(rand.NewSource(1))
	mi := &modelIndex{pos: map[int64]int{}}
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32() - 0.5
		}
		normalize(v)
		mi.pos[int64(i)] = i
		mi.entries = append(mi.entries, &vecEntry{article: int64(i), dim: dim, idxs: []int32{0}, vecs: v})
	}
	idx.models["m"] = mi
	q := make([]float32, dim)
	for j := range q {
		q[j] = rng.Float32() - 0.5
	}
	normalize(q)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := idx.search("m", q, nil, 10); len(r) != 10 {
			b.Fatal(len(r))
		}
	}
}

func TestHasURL(t *testing.T) {
	s := newStore(t)
	ins(t, s, domain.Article{SourceID: "a", CanonicalURL: "http://a/1", Title: "x"})
	if ok, err := s.HasURL(ctx, "http://a/1"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.HasURL(ctx, "http://a/2"); err != nil || ok {
		t.Fatal(ok, err)
	}
}
