package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
)

type followTestStore struct {
	domain.Store
	real *store.SQLite
}

func (f followTestStore) ListSources(ctx context.Context) ([]domain.Source, error) {
	return f.real.ListSources(ctx)
}
func (f followTestStore) SearchText(ctx context.Context, q string, fl domain.Filter, k int) ([]domain.Hit, error) {
	return f.real.SearchText(ctx, q, fl, k)
}

type fakeAlerts struct {
	recs []domain.AlertRecord
	n    int
}

func (f *fakeAlerts) Channels() []string { return []string{"ntfy"} }
func (f *fakeAlerts) SendTest(context.Context) ([]domain.AlertRecord, error) {
	f.n++
	return f.recs, nil
}

var sameOriginHdr = map[string]string{"Origin": "http://example.com", "Content-Type": "application/x-www-form-urlencoded"}

func followHandler(t *testing.T) (http.Handler, *store.SQLite, *fakeAlerts) {
	t.Helper()
	real, err := store.Open(filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { real.Close() })
	ctx := context.Background()
	_ = real.UpsertSources(ctx, []domain.Source{{ID: "pais", Name: "El País <b>", Leaning: "izquierda"}, {ID: "abc", Name: "ABC", Leaning: "derecha"}})
	now := time.Now()
	for i, a := range []domain.Article{
		{SourceID: "pais", Title: "Ley de vivienda <script>alert(1)</script>", URL: "http://e/1", CanonicalURL: "http://e/1", PublishedAt: now.Add(-time.Hour), FirstSeenAt: now},
		{SourceID: "abc", Title: "Vivienda: nuevo plan", URL: "http://e/2", CanonicalURL: "http://e/2", PublishedAt: now.AddDate(0, 0, -2), FirstSeenAt: now},
		{SourceID: "abc", Title: "Otra cosa", URL: "http://e/3", CanonicalURL: "http://e/3", PublishedAt: now, FirstSeenAt: now},
	} {
		a := a
		if ok, err := real.InsertArticle(ctx, &a); err != nil || !ok {
			t.Fatal(i, ok, err)
		}
	}
	fa := &fakeAlerts{}
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) {
		d.Store = followTestStore{real: real}
		d.Follow = real
		d.Alerts = fa
	})
	return h, real, fa
}

func TestFollowPageCreateDelete(t *testing.T) {
	h, real, _ := followHandler(t)
	rec := do(h, "GET", "/seguimientos", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Aún no sigues ningún tema") || !strings.Contains(rec.Body.String(), "Sin canal") && !strings.Contains(rec.Body.String(), "Canales activos: ntfy") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// CSRF: no Origin / header => forbidden
	if r := do(h, "POST", "/seguimientos", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, "q=vivienda"); r.Code != 403 {
		t.Fatal(r.Code)
	}
	// XSS in name and query is escaped
	body := "q=" + "vivienda+%3Cimg+src%3Dx+onerror%3Dalert(1)%3E" + "&name=%3Cscript%3Ealert(2)%3C%2Fscript%3E&source=pais&notify=1"
	r := do(h, "POST", "/seguimientos", sameOriginHdr, body)
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/seguimientos" {
		t.Fatal(r.Code, r.Body.String())
	}
	list, _ := real.ListWatches(context.Background())
	if len(list) != 1 || !list[0].Notify || list[0].SourceIDs[0] != "pais" {
		t.Fatalf("%+v", list)
	}
	b := do(h, "GET", "/seguimientos", nil, "").Body.String()
	for _, bad := range []string{"<script>alert(2)", "<img src=x", "<script>alert(1)</script>"} {
		if strings.Contains(b, bad) {
			t.Fatalf("unescaped %q", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(2)", "daybars", "El País &lt;b&gt;", "/articulo/1", "/preguntar?comparar=1", "Probar notificación"} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
	// query with only symbols is rejected (400) and the form keeps the input
	r = do(h, "POST", "/seguimientos", sameOriginHdr, "q=%22%22+*+%3A%3A&name=x")
	if r.Code != 400 || !strings.Contains(r.Body.String(), "Escribe qué quieres seguir") {
		t.Fatal(r.Code)
	}
	// syntax characters are harmless (sanitized by the store)
	if r = do(h, "POST", "/seguimientos", sameOriginHdr, "q=NEAR(a+b)+OR+%22x"); r.Code != 303 {
		t.Fatal(r.Code)
	}
	if r = do(h, "GET", "/seguimientos", nil, ""); r.Code != 200 || strings.Contains(r.Body.String(), "No se pudo buscar") {
		t.Fatal(r.Code)
	}
	// htmx create returns the fragment
	r = do(h, "POST", "/seguimientos", map[string]string{"HX-Request": "true", "Content-Type": "application/x-www-form-urlencoded"}, "q=abc")
	if r.Code != 200 || strings.Contains(r.Body.String(), "<html") || !strings.Contains(r.Body.String(), "Seguimiento creado") {
		t.Fatal(r.Code, r.Body.String())
	}
	// delete
	id := list[0].ID
	if r = do(h, "POST", "/seguimientos/"+followItoa(id)+"/borrar", sameOriginHdr, ""); r.Code != 303 {
		t.Fatal(r.Code)
	}
	if r = do(h, "POST", "/seguimientos/zz/borrar", sameOriginHdr, ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
	left, _ := real.ListWatches(context.Background())
	for _, w := range left {
		if w.ID == id {
			t.Fatal("not deleted")
		}
	}
}

func followItoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestFollowAlertsAndTest(t *testing.T) {
	h, real, fa := followHandler(t)
	ctx := context.Background()
	_ = real.SaveAlert(ctx, domain.AlertRecord{Kind: "story", Key: "k", Title: "Alerta <i>x</i>", Channel: "ntfy", OK: false, Detail: "HTTP 500 <b>"})
	_ = real.SaveAlert(ctx, domain.AlertRecord{Kind: "watch", Key: "k2", Title: "Otra", Channel: "log", OK: true})
	b := do(h, "GET", "/seguimientos", nil, "").Body.String()
	for _, want := range []string{"Alerta &lt;i&gt;x&lt;/i&gt;", "fallida", "HTTP 500 &lt;b&gt;", "sin canal configurado"} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q", want)
		}
	}
	fa.recs = []domain.AlertRecord{{Channel: "ntfy", OK: true}}
	r := do(h, "POST", "/seguimientos/probar", sameOriginHdr, "")
	if r.Code != 303 || r.Header().Get("Location") != "/seguimientos?prueba=ok" || fa.n != 1 {
		t.Fatal(r.Code, r.Header())
	}
	fa.recs = []domain.AlertRecord{{Channel: "ntfy", OK: false}}
	if r = do(h, "POST", "/seguimientos/probar", sameOriginHdr, ""); r.Header().Get("Location") != "/seguimientos?prueba=mal" {
		t.Fatal(r.Header())
	}
	if b = do(h, "GET", "/seguimientos?prueba=mal", nil, "").Body.String(); !strings.Contains(b, "falló") {
		t.Fatal("no notice")
	}
}

func TestFollowUnavailableWithoutStore(t *testing.T) {
	h, _, _, _ := newHandler(t, nil)
	if r := do(h, "GET", "/seguimientos", nil, ""); r.Code != 503 {
		t.Fatal(r.Code)
	}
	if b := do(h, "GET", "/mas", nil, "").Body.String(); strings.Contains(b, "/seguimientos") {
		t.Fatal("nav should be hidden")
	}
}

func TestFollowNavVisible(t *testing.T) {
	h, _, _ := followHandler(t)
	if b := do(h, "GET", "/mas", nil, "").Body.String(); !strings.Contains(b, "/seguimientos") {
		t.Fatal("nav missing")
	}
}
