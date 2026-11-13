package web

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type memDigestStore struct {
	mu sync.Mutex
	m  map[string]domain.Digest
}

func (s *memDigestStore) SaveDigest(_ context.Context, d *domain.Digest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]domain.Digest{}
	}
	s.m[d.Day] = *d
	return nil
}
func (s *memDigestStore) GetDigest(_ context.Context, day string) (*domain.Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.m[day]; ok {
		return &d, nil
	}
	return nil, domain.ErrDigestNotFound
}
func (s *memDigestStore) ListDigests(_ context.Context, n int) ([]domain.Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Digest
	for _, d := range s.m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}
func (s *memDigestStore) LatestDigest(ctx context.Context) (*domain.Digest, error) {
	l, _ := s.ListDigests(ctx, 1)
	if len(l) == 0 {
		return nil, domain.ErrDigestNotFound
	}
	return &l[0], nil
}

type fakeGen struct {
	started, release chan struct{}
	days             chan time.Time
}

func (g *fakeGen) RunOnce(context.Context) (string, error) { return "ok", nil }
func (g *fakeGen) Build(_ context.Context, day time.Time) (*domain.Digest, error) {
	g.days <- day
	g.started <- struct{}{}
	<-g.release
	return &domain.Digest{Day: day.Format("2006-01-02")}, nil
}

func sampleDigest(day string) domain.Digest {
	at := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	chg := 3
	return domain.Digest{Day: day, GeneratedAt: at, WindowFrom: at.Add(-24 * time.Hour), WindowTo: at, Model: "modelo<x>",
		Articles: 40, Outlets: 5, Changed: &chg, Lead: "Lead <b>uno</b> & más",
		Stories: []domain.DigestStory{{ID: "s", Title: "Historia <script>alert(1)</script>", Sources: 3, Articles: 5, FirstBy: "El País", FirstAt: at,
			Leanings: map[string]int{"izquierda": 2, "derecha": 1}, Missing: []string{"ABC"},
			Narrative: "Dicen cosas <img src=x onerror=alert(1)> [1].",
			Headlines: []domain.DigestHeadline{
				{N: 1, Outlet: "El País", Title: "Titular <i>1</i>", URL: "https://e.example/1", At: at},
				{N: 2, Outlet: "Mal", Title: "Titular 2", URL: "javascript:alert(1)", At: at}}}},
		Rising:      []domain.DigestTerm{{Term: "Vivienda & co", Key: "vivienda & co", Count: 4, Sources: 2, New: true}},
		Distinctive: []domain.DigestHeadline{{Outlet: "Público", Title: "Singular", URL: "https://e.example/3"}},
		Shots:       []domain.DigestShot{{Outlet: "El País", TakenAt: at, Path: "pais/a b/0900.jpg"}},
		WhatChanged: []string{"Cambio [1]."}}
}

func digestHandler(t *testing.T, ds *memDigestStore, gen domain.DigestGenerator) http.Handler {
	h, _, _, _ := newHandler(t, func(d *Deps, o *Options) { d.Digests, d.DigestGen = ds, gen })
	return h
}

func TestDigestPages(t *testing.T) {
	ds := &memDigestStore{}
	for _, day := range []string{"2026-11-10", "2026-11-12"} {
		d := sampleDigest(day)
		_ = ds.SaveDigest(context.Background(), &d)
	}
	h := digestHandler(t, ds, nil)
	rec := do(h, "GET", "/resumen", nil, "")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "Resumen del jueves 12 de noviembre") {
		t.Fatalf("%d %s", rec.Code, b)
	}
	for _, bad := range []string{"<script>alert", "<img src=x", "javascript:alert", "<b>uno"} {
		if strings.Contains(b, bad) {
			t.Errorf("unescaped %q", bad)
		}
	}
	for _, want := range []string{`href="#f1"`, "Redactado por", "modelo&lt;x&gt;", "3 titulares modificados", `href="/resumen/2026-11-10" rel="prev"`, "Archivo", "/shots/pais/a%20b/0900.jpg", "No lo cubren: ABC", "/resumen.atom"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(b, "Generar ahora") {
		t.Error("generate button without generator")
	}
	if rec := do(h, "GET", "/resumen/2026-11-10", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `rel="next"`) {
		t.Fatalf("prev day %d", rec.Code)
	}
	// Not generated yet -> 404 page with empty state; bad dates -> 404.
	if rec := do(h, "GET", "/resumen/2026-11-11", nil, ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Todavía no hay resumen") {
		t.Fatalf("missing day %d", rec.Code)
	}
	for _, p := range []string{"/resumen/hola", "/resumen/2026-13-40", "/resumen/2026-2-3", "/resumen/%3Cscript%3E"} {
		if rec := do(h, "GET", p, nil, ""); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	// Nav + home teaser.
	if home := do(h, "GET", "/", nil, "").Body.String(); !strings.Contains(home, "Último resumen") || !strings.Contains(home, `href="/resumen/2026-11-12"`) {
		t.Errorf("home teaser missing: %v", strings.Contains(home, "resumen"))
	}
}

func TestDigestEmptyAndDisabled(t *testing.T) {
	h := digestHandler(t, &memDigestStore{}, &fakeGen{})
	rec := do(h, "GET", "/resumen", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Todavía no hay resumen") || !strings.Contains(rec.Body.String(), "Generar ahora") {
		t.Fatalf("%d", rec.Code)
	}
	if home := do(h, "GET", "/", nil, "").Body.String(); strings.Contains(home, "home-digest") && strings.Contains(home, "Resumen de hoy") {
		t.Error("teaser without digest")
	}
	off, _, _, _ := newHandler(t, nil)
	for _, p := range []string{"/resumen", "/resumen.atom", "/api/digest"} {
		if rec := do(off, "GET", p, nil, ""); rec.Code != 503 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if nav := do(off, "GET", "/mas", nil, "").Body.String(); strings.Contains(nav, "/resumen") {
		t.Error("nav shown when disabled")
	}
}

func TestDigestAPI(t *testing.T) {
	ds := &memDigestStore{}
	d := sampleDigest("2026-11-12")
	_ = ds.SaveDigest(context.Background(), &d)
	h := digestHandler(t, ds, nil)
	var got domain.Digest
	rec := do(h, "GET", "/api/digest?day=2026-11-12", nil, "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Articles != 40 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, "GET", "/api/digest", nil, ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := do(h, "GET", "/api/digest?day=2020-01-01", nil, ""); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec := do(h, "GET", "/api/digest?day=x", nil, ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestDigestAtom(t *testing.T) {
	ds := &memDigestStore{}
	for i := 0; i < 20; i++ {
		d := sampleDigest(time.Date(2026, 10, 1+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
		_ = ds.SaveDigest(context.Background(), &d)
	}
	h := digestHandler(t, ds, nil)
	rec := do(h, "GET", "/resumen.atom", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "news.lan:8443"}, "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/atom+xml") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var f struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
		Title   string   `xml:"title"`
		ID      string   `xml:"id"`
		Updated string   `xml:"updated"`
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Author struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Entries []struct {
			Title   string `xml:"title"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
			Link    struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
			Content struct {
				Type string `xml:"type,attr"`
				Body string `xml:",chardata"`
			} `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	if f.Title == "" || f.ID == "" || f.Author.Name == "" || len(f.Entries) != 14 || f.Links[0].Href != "https://news.lan:8443/resumen.atom" {
		t.Fatalf("%+v", f)
	}
	if _, err := time.Parse(time.RFC3339, f.Updated); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range f.Entries {
		if e.ID == "" || seen[e.ID] || e.Title == "" || e.Updated == "" || !strings.HasPrefix(e.Link.Href, "https://news.lan:8443/resumen/") || e.Content.Type != "html" {
			t.Fatalf("entry %+v", e)
		}
		seen[e.ID] = true
	}
	body := f.Entries[0].Content.Body
	for _, bad := range []string{"<script>", "javascript:", "<img src=x"} {
		if strings.Contains(body, bad) {
			t.Errorf("unescaped %q in feed html", bad)
		}
	}
	if !strings.Contains(body, `<a href="https://e.example/1">`) || !strings.Contains(body, "Redactado por modelo&lt;x&gt;") {
		t.Errorf("content: %s", body)
	}
	// Stable ids across requests; hostile Host header cannot inject.
	rec2 := do(h, "GET", "/resumen.atom", map[string]string{"X-Forwarded-Host": `evil"><x`}, "")
	if strings.Contains(rec2.Body.String(), "evil") {
		t.Error("host injection")
	}
}

func TestDigestJob(t *testing.T) {
	gen := &fakeGen{started: make(chan struct{}, 2), release: make(chan struct{}), days: make(chan time.Time, 2)}
	h := digestHandler(t, &memDigestStore{}, gen)
	hx := map[string]string{"X-Requested-With": "t"}
	if rec := do(h, "POST", "/api/jobs/digest?day=2999-01-01", hx, ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := do(h, "POST", "/api/jobs/digest?day=nope", hx, ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := do(h, "POST", "/api/jobs/digest", nil, ""); rec.Code != 403 {
		t.Fatalf("csrf %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/jobs/digest?day=2026-11-10", hx, ""); rec.Code != 202 {
		t.Fatal(rec.Code)
	}
	<-gen.started
	if d := <-gen.days; d.Format("2006-01-02") != "2026-11-10" {
		t.Fatal(d)
	}
	if rec := do(h, "POST", "/api/jobs/digest", hx, ""); rec.Code != 409 {
		t.Fatalf("want 409 got %d", rec.Code)
	}
	close(gen.release)
	off, _, _, _ := newHandler(t, nil)
	if rec := do(off, "POST", "/api/jobs/digest", hx, ""); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}
