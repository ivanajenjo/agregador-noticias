package store

import (
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func revArticle(t *testing.T, s *SQLite, src, url, title, sum string) int64 {
	t.Helper()
	a := &domain.Article{SourceID: src, URL: url, CanonicalURL: url, Title: title, Summary: sum, FirstSeenAt: time.Unix(1_700_000_000, 0)}
	if ok, err := s.InsertArticle(ctx, a); err != nil || !ok {
		t.Fatalf("insert: %v %v", ok, err)
	}
	return a.ID
}

func TestRecordRevisionBasics(t *testing.T) {
	s := newStore(t)
	id := revArticle(t, s, "a", "http://a/1", "Detenido el alcalde de Marbella por corrupción", "Resumen inicial de la noticia sobre el alcalde")
	now := time.Unix(1_700_000_000+42*60, 0)

	// unknown article
	if ch, r, err := s.RecordRevision(ctx, "http://a/zzz", "x y z w", "", now); err != nil || ch || r != nil {
		t.Fatalf("unknown: %v %v %v", ch, r, err)
	}
	// identical / trivial (accent, suffix, typo)
	for _, ti := range []string{"Detenido el alcalde de Marbella por corrupción", "detenido el alcalde de marbella por corrupcion - El País", "Detenido el alcalde de Marbella por corrupcón"} {
		if ch, _, err := s.RecordRevision(ctx, "http://a/1", ti, "", now); err != nil || ch {
			t.Fatalf("trivial %q: changed=%v err=%v", ti, ch, err)
		}
	}
	// material
	ch, rev, err := s.RecordRevision(ctx, "http://a/1", "Liberado el alcalde de Marbella tras declarar", "", now)
	if err != nil || !ch || rev == nil {
		t.Fatalf("material: %v %v %v", ch, rev, err)
	}
	if rev.Kind != domain.RevisionTitle || rev.OldText != "Detenido el alcalde de Marbella por corrupción" || rev.ArticleID != id {
		t.Fatalf("rev = %+v", rev)
	}
	if d := rev.ChangedAt.Sub(rev.SeenAt); d != 42*time.Minute {
		t.Fatalf("delay = %v", d)
	}
	a, _ := s.GetArticle(ctx, id)
	if a.Title != "Liberado el alcalde de Marbella tras declarar" {
		t.Fatalf("title not updated: %q", a.Title)
	}
	// FTS follows the update
	if hits, err := s.SearchText(ctx, "liberado", domain.Filter{}, 5); err != nil || len(hits) != 1 {
		t.Fatalf("fts new: %v %v", hits, err)
	}
	if hits, _ := s.SearchText(ctx, "detenido", domain.Filter{}, 5); len(hits) != 0 {
		t.Fatalf("fts old still matches: %v", hits)
	}
	// same text again: no new revision
	if ch, _, _ := s.RecordRevision(ctx, "http://a/1", "Liberado el alcalde de Marbella tras declarar", "", now); ch {
		t.Fatal("repeat recorded")
	}
	revs, err := s.ListRevisions(ctx, id)
	if err != nil || len(revs) != 1 {
		t.Fatalf("list: %v %v", revs, err)
	}
}

func TestRecordRevisionFlapping(t *testing.T) {
	s := newStore(t)
	id := revArticle(t, s, "a", "http://a/f", "El Gobierno sube el salario mínimo", "")
	now := time.Now()
	if ch, _, _ := s.RecordRevision(ctx, "http://a/f", "El Gobierno congela el salario mínimo", "", now); !ch {
		t.Fatal("A->B should record")
	}
	// jitter back to A and again to B: nothing more
	for _, ti := range []string{"El Gobierno sube el salario mínimo", "El Gobierno congela el salario mínimo", "El Gobierno sube el salario mínimo"} {
		if ch, _, _ := s.RecordRevision(ctx, "http://a/f", ti, "", now); ch {
			t.Fatalf("flap %q recorded", ti)
		}
	}
	if revs, _ := s.ListRevisions(ctx, id); len(revs) != 1 {
		t.Fatalf("revisions = %d", len(revs))
	}
}

func TestRecordRevisionMaxAndSummary(t *testing.T) {
	s := newStore(t)
	id := revArticle(t, s, "a", "http://a/m", "Titular inicial sobre economía española", "Resumen original del artículo sobre economía")
	now := time.Now()
	words := []string{"alfa", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa", "lambda", "omicron", "sigma", "tau", "upsilon", "omega", "bravo", "charlie", "foxtrot", "golf", "hotel", "india", "juliet"}
	n := 0
	for _, w := range words {
		ch, _, err := s.RecordRevision(ctx, "http://a/m", "Titular sobre "+w+" cambia completamente hoy", "", now)
		if err != nil {
			t.Fatal(err)
		}
		if ch {
			n++
		}
	}
	if n != domain.MaxRevisionsPerArticle {
		t.Fatalf("recorded %d, want cap %d", n, domain.MaxRevisionsPerArticle)
	}
	_ = id

	// summary revision on another article + both kinds at once
	id2 := revArticle(t, s, "b", "http://a/s", "Otro titular de política nacional", "La policía ha detenido al alcalde por un presunto delito de cohecho")
	ch, rev, err := s.RecordRevision(ctx, "http://a/s", "Otro titular de política nacional", "El juez deja en libertad al alcalde sin cargos tras declarar", now)
	if err != nil || !ch || rev.Kind != domain.RevisionSummary {
		t.Fatalf("summary: %v %+v %v", ch, rev, err)
	}
	// summary empty -> text: silent update, no revision
	id3 := revArticle(t, s, "b", "http://a/s3", "Titular sin resumen alguno todavía", "")
	if ch, _, _ := s.RecordRevision(ctx, "http://a/s3", "Titular sin resumen alguno todavía", "Ahora sí hay resumen", now); ch {
		t.Fatal("empty->text must be silent")
	}
	if a, _ := s.GetArticle(ctx, id3); a.Summary != "Ahora sí hay resumen" {
		t.Fatalf("silent summary = %q", a.Summary)
	}
	// both kinds
	ch, rev, _ = s.RecordRevision(ctx, "http://a/s", "Liberado el alcalde tras declarar ante el juez", "Nuevo resumen totalmente distinto del anterior texto", now)
	if !ch || rev.Kind != domain.RevisionTitle {
		t.Fatalf("both: %v %+v", ch, rev)
	}
	if revs, _ := s.ListRevisions(ctx, id2); len(revs) != 3 {
		t.Fatalf("revs = %d", len(revs))
	}
}

func TestRecordRevisionRequeuesEmbedding(t *testing.T) {
	s := newStore(t)
	id := revArticle(t, s, "a", "http://a/e", "Titular que cambiará pronto en portada", "Resumen")
	if err := s.SaveEmbeddings(ctx, id, "m1", []domain.ChunkVec{{Index: 0, Text: "x", Vec: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ArticlesNeedingEmbedding(ctx, "m1", 10); len(l) != 0 {
		t.Fatalf("precondition: %v", l)
	}
	if ch, _, _ := s.RecordRevision(ctx, "http://a/e", "Titular distinto ahora en portada completamente", "", time.Now()); !ch {
		t.Fatal("not changed")
	}
	if l, _ := s.ArticlesNeedingEmbedding(ctx, "m1", 10); len(l) != 1 {
		t.Fatalf("should be requeued: %v", l)
	}
}

func TestRecordRevisionTitleHashCollision(t *testing.T) {
	s := newStore(t)
	revArticle(t, s, "a", "http://a/c1", "Titular uno completamente distinto aquí", "")
	revArticle(t, s, "a", "http://a/c2", "Segundo titular sobre otra noticia diferente", "")
	// c2 becomes c1's title (same source): must not fail on UNIQUE(source,title_hash)
	ch, _, err := s.RecordRevision(ctx, "http://a/c2", "Titular uno completamente distinto aquí", "", time.Now())
	if err != nil || !ch {
		t.Fatalf("collision: %v %v", ch, err)
	}
}

func TestRevisionQueriesAndCascade(t *testing.T) {
	s := newStore(t)
	base := time.Now().Add(-2 * time.Hour)
	i1 := revArticle(t, s, "a", "http://a/q1", "Primer titular sobre el tema uno", "")
	revArticle(t, s, "b", "http://a/q2", "Segundo titular sobre el tema dos", "")
	s.RecordRevision(ctx, "http://a/q1", "Primer titular cambiado por completo ahora", "", base)
	s.RecordRevision(ctx, "http://a/q2", "Segundo titular cambiado por completo ahora", "", base.Add(time.Hour))
	s.RecordRevision(ctx, "http://a/q1", "Tercera versión del primer titular distinta", "", base.Add(90*time.Minute))

	all, err := s.RecentRevisions(ctx, domain.RevisionFilter{})
	if err != nil || len(all) != 3 || all[0].NewText != "Tercera versión del primer titular distinta" || all[0].ArticleURL != "http://a/q1" {
		t.Fatalf("recent: %+v %v", all, err)
	}
	if n, _ := s.CountRevisions(ctx, domain.RevisionFilter{SourceIDs: []string{"b"}}); n != 1 {
		t.Fatalf("count b = %d", n)
	}
	if n, _ := s.CountRevisions(ctx, domain.RevisionFilter{Leanings: []string{"izquierda"}}); n != 2 {
		t.Fatalf("count leaning = %d", n)
	}
	if n, _ := s.CountRevisions(ctx, domain.RevisionFilter{From: base.Add(30 * time.Minute)}); n != 2 {
		t.Fatalf("count from = %d", n)
	}
	if l, _ := s.RecentRevisions(ctx, domain.RevisionFilter{Limit: 1, Offset: 1}); len(l) != 1 || l[0].SourceID != "b" {
		t.Fatalf("paging: %+v", l)
	}
	by, _ := s.RevisionsBySource(ctx, domain.RevisionFilter{})
	if len(by) != 2 || by[0].SourceID != "a" || by[0].Count != 2 {
		t.Fatalf("by source: %+v", by)
	}
	// cascade with article deletion
	if _, err := s.w.ExecContext(ctx, `DELETE FROM articles WHERE id=?`, i1); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountRevisions(ctx, domain.RevisionFilter{}); n != 1 {
		t.Fatalf("after cascade = %d", n)
	}
}
