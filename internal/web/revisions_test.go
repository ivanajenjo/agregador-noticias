package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeRevStore struct {
	*fakeStore
	ents []domain.RevisionEntry
	last domain.RevisionFilter
}

func (f *fakeRevStore) RecordRevision(context.Context, string, string, string, time.Time) (bool, *domain.Revision, error) {
	return false, nil, nil
}
func (f *fakeRevStore) ListRevisions(_ context.Context, id int64) ([]domain.Revision, error) {
	var out []domain.Revision
	for _, e := range f.ents {
		if e.ArticleID == id {
			out = append(out, e.Revision)
		}
	}
	return out, nil
}
func (f *fakeRevStore) RecentRevisions(_ context.Context, fl domain.RevisionFilter) ([]domain.RevisionEntry, error) {
	f.last = fl
	return f.ents, nil
}
func (f *fakeRevStore) CountRevisions(context.Context, domain.RevisionFilter) (int64, error) {
	return int64(len(f.ents)), nil
}
func (f *fakeRevStore) RevisionsBySource(context.Context, domain.RevisionFilter) ([]domain.SourceCount, error) {
	if len(f.ents) == 0 {
		return nil, nil
	}
	return []domain.SourceCount{{SourceID: "pais", Count: int64(len(f.ents))}}, nil
}

func revHandler(t *testing.T, ents []domain.RevisionEntry) (http.Handler, *fakeRevStore) {
	var rs *fakeRevStore
	h, _, _, _ := newHandler(t, func(d *Deps, _ *Options) {
		rs = &fakeRevStore{fakeStore: d.Store.(*fakeStore), ents: ents}
		d.Store = rs
	})
	return h, rs
}

func TestDiffHTML(t *testing.T) {
	b, a := diffHTML("Detenido el alcalde <b>", "Liberado el alcalde <b>")
	if !strings.Contains(string(b), "<del>") || !strings.Contains(string(b), "eliminado") || !strings.Contains(string(b), "Detenido") {
		t.Errorf("before = %s", b)
	}
	if !strings.Contains(string(a), "<ins>") || !strings.Contains(string(a), "añadido") || strings.Contains(string(a), "Detenido") {
		t.Errorf("after = %s", a)
	}
	if strings.Contains(string(b), "<b>") || !strings.Contains(string(b), "&lt;b&gt;") {
		t.Errorf("not escaped: %s", b)
	}
	b, a = diffHTML("igual", "igual")
	if string(b) != "igual" || string(a) != "igual" {
		t.Errorf("equal: %q %q", b, a)
	}
	_, a = diffHTML("", "<script>alert(1)</script>")
	if strings.Contains(string(a), "<script>") {
		t.Errorf("xss: %s", a)
	}
}

func TestDelayText(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second: "menos de 1 min", 42 * time.Minute: "42 min", 2 * time.Hour: "2 h",
		3*time.Hour + 5*time.Minute: "3 h 5 min", 24 * time.Hour: "1 día", 72 * time.Hour: "3 días",
	}
	for d, want := range cases {
		if got := delayText(d); got != want {
			t.Errorf("delayText(%v) = %q want %q", d, got, want)
		}
	}
}

func sampleEnts() []domain.RevisionEntry {
	now := time.Now()
	return []domain.RevisionEntry{{
		Revision: domain.Revision{ID: 1, ArticleID: 1, Kind: domain.RevisionTitle, OldText: "Detenido el alcalde de Marbella <img src=x onerror=alert(1)>",
			NewText: "Liberado el alcalde de Marbella", ChangedAt: now.Add(-time.Hour), SeenAt: now.Add(-time.Hour - 42*time.Minute)},
		SourceID: "pais", ArticleTitle: "Titular <script>alert(1)</script>", ArticleURL: "https://e.example/1",
	}}
}

func TestRevisionsPage(t *testing.T) {
	h, rs := revHandler(t, sampleEnts())
	rec := do(h, "GET", "/cambios", nil, "")
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Titulares modificados", "Antes", "Después", "modificado 42 min después", "1 cambio de titular en 7 días", "El País", "/articulo/1", "<del>", "<ins>"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<img src=x") || strings.Contains(body, "<script>alert") {
		t.Error("XSS: unescaped content")
	}
	if d := rs.last.To.Sub(rs.last.From); d != 7*24*time.Hour {
		t.Errorf("default window = %v", d)
	}
	do(h, "GET", "/cambios?ventana=24h&tipo=resumen&fuentes=pais&lineas=izquierda", nil, "")
	if rs.last.Kind != domain.RevisionSummary || len(rs.last.SourceIDs) != 1 || len(rs.last.Leanings) != 1 || rs.last.To.Sub(rs.last.From) != 24*time.Hour {
		t.Errorf("filter = %+v", rs.last)
	}
	if rec := do(h, "GET", "/cambios?ventana=zzz", nil, ""); rec.Code != 400 {
		t.Errorf("bad ventana code %d", rec.Code)
	}
	// nav item present
	if !strings.Contains(do(h, "GET", "/mas", nil, "").Body.String(), "/cambios") {
		t.Error("nav missing")
	}
}

func TestRevisionsEmptyAndUnsupported(t *testing.T) {
	h, _ := revHandler(t, nil)
	rec := do(h, "GET", "/cambios", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "No se han detectado cambios") {
		t.Fatalf("empty: %d", rec.Code)
	}
	h2, _, _, _ := newHandler(t, nil) // plain fakeStore: no RevisionStore
	if rec := do(h2, "GET", "/cambios", nil, ""); rec.Code != 503 {
		t.Errorf("unsupported code %d", rec.Code)
	}
	if rec := do(h2, "GET", "/api/revisions", nil, ""); rec.Code != 503 {
		t.Errorf("unsupported api code %d", rec.Code)
	}
	if strings.Contains(do(h2, "GET", "/mas", nil, "").Body.String(), "/cambios") {
		t.Error("nav should be hidden")
	}
}

func TestRevisionsAPIAndArticleHistory(t *testing.T) {
	h, _ := revHandler(t, sampleEnts())
	rec := do(h, "GET", "/api/revisions?limit=5", nil, "")
	var out struct {
		Total     int
		Revisions []revJSON
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Total != 1 || len(out.Revisions) != 1 || out.Revisions[0].DelaySeconds != 42*60 {
		t.Fatalf("api: %v %s", err, rec.Body.String())
	}
	art := do(h, "GET", "/articulo/1", nil, "").Body.String()
	if !strings.Contains(art, "Historial de cambios") || !strings.Contains(art, "<ins>") || strings.Contains(art, "<img src=x") {
		t.Errorf("article history missing/unsafe")
	}
	if strings.Contains(do(h, "GET", "/articulo/2", nil, "").Body.String(), "Historial de cambios") {
		t.Error("article without revisions shows disclosure")
	}
}
