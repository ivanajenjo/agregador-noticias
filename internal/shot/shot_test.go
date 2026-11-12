package shot

import (
	"context"
	"image"
	_ "image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeStore struct {
	domain.Store // unimplemented methods panic if used
	mu           sync.Mutex
	shots        []domain.Screenshot
}

func (f *fakeStore) SaveScreenshot(_ context.Context, s *domain.Screenshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := *s
	c.ID = int64(len(f.shots) + 1)
	f.shots = append(f.shots, c)
	return nil
}

func (f *fakeStore) DeleteScreenshotsBefore(_ context.Context, t time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var paths []string
	keep := f.shots[:0]
	for _, s := range f.shots {
		if s.TakenAt.Before(t) {
			paths = append(paths, s.Path)
		} else {
			keep = append(keep, s)
		}
	}
	f.shots = keep
	return paths, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestNewDefaults(t *testing.T) {
	tk := New(&fakeStore{}, Options{}, nil)
	o := tk.opts
	if o.Width != 1366 || o.Height != 900 || o.Quality != 70 || o.MaxHeight != 12000 || o.Now == nil {
		t.Fatalf("bad defaults: %+v", o)
	}
}

func TestRunOnceNoSources(t *testing.T) {
	off := false
	tk := New(&fakeStore{}, Options{Dir: t.TempDir(), Sources: []domain.Source{
		{ID: "a", Homepage: "http://x", Screenshot: false},
		{ID: "b", Homepage: "http://x", Screenshot: true, Enabled: &off},
	}}, quiet())
	n, err := tk.RunOnce(context.Background())
	if n != 0 || err != nil {
		t.Fatalf("got %d, %v", n, err)
	}
}

func TestRunOnceNoBrowser(t *testing.T) {
	tk := New(&fakeStore{}, Options{Dir: t.TempDir(), ChromePath: "/nonexistent/chrome",
		Sources: []domain.Source{{ID: "a", Homepage: "http://x", Screenshot: true}}}, quiet())
	n, err := tk.RunOnce(context.Background())
	if n != 0 || err == nil {
		t.Fatalf("expected error, got %d, %v", n, err)
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	st := &fakeStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.Local)
	mk := func(rel string, at time.Time) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = st.SaveScreenshot(context.Background(), &domain.Screenshot{SourceID: "a", TakenAt: at, Path: rel})
	}
	mk("a/2026/01/01/0900.jpg", now.AddDate(0, 0, -150))
	mk("a/2026/05/30/0900.jpg", now.AddDate(0, 0, -2))
	tk := New(st, Options{Dir: dir, Retention: 90 * 24 * time.Hour, Now: func() time.Time { return now }}, quiet())
	n, err := tk.Cleanup(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a/2026/01")); !os.IsNotExist(err) {
		t.Fatalf("empty dirs not pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a/2026/05/30/0900.jpg")); err != nil {
		t.Fatalf("recent file removed: %v", err)
	}
	// retention <= 0 keeps everything
	tk2 := New(st, Options{Dir: dir, Now: func() time.Time { return now }}, quiet())
	if n, err := tk2.Cleanup(context.Background()); n != 0 || err != nil {
		t.Fatalf("expected noop, got %d %v", n, err)
	}
}

func TestCleanupRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(filepath.Dir(dir), "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(victim)
	st := &fakeStore{}
	now := time.Now()
	_ = st.SaveScreenshot(context.Background(), &domain.Screenshot{TakenAt: now.Add(-time.Hour * 48), Path: "../victim.txt"})
	tk := New(st, Options{Dir: dir, Retention: time.Hour, Now: func() time.Time { return now }}, quiet())
	if _, err := tk.Cleanup(context.Background()); err == nil {
		t.Fatal("expected error for traversal path")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("file outside dir was deleted")
	}
}

const testPage = `<!doctype html><html><head><meta charset=utf-8><style>
body{margin:0;font-family:sans-serif}
.block{height:500px;background:#2a9d3a}
.block:nth-child(even){background:#e8e8e8}
#cookie-banner{position:fixed;left:0;right:0;bottom:0;height:200px;background:#ff0000;z-index:99999}
#promo{position:fixed;left:0;top:0;width:100%;height:150px;background:#0000ff;z-index:99998}
</style></head><body>
@@BANNER@@
@@PROMO@@
<div class=block></div><div class=block></div><div class=block></div><div class=block></div><div class=block></div><div class=block></div>
<script>@@JS@@</script>
</body></html>`

func srv(banner string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b, js string
		switch banner {
		case "generic": // text heuristic inside a consent-looking container
			b = `<div id="cookie-banner"><p>Usamos cookies</p><button onclick="this.parentNode.remove()">Aceptar</button></div>`
		case "custom": // only reachable through a configured selector
			b = `<div id="cookie-banner"><span class="weird" onclick="this.parentNode.remove()">go away</span></div>`
		case "stuck": // cannot be dismissed: must be hidden by CSS
			b = `<div id="cookie-banner"><p>no button</p></div>`
		}
		promo := `<div id="promo">promo</div>`
		_, _ = w.Write([]byte(strings.NewReplacer("@@BANNER@@", b, "@@PROMO@@", promo, "@@JS@@", js).Replace(testPage)))
	}))
}

func countColor(img image.Image, pred func(r, g, b uint32) bool) int {
	n := 0
	bd := img.Bounds()
	for y := bd.Min.Y; y < bd.Max.Y; y += 2 {
		for x := bd.Min.X; x < bd.Max.X; x += 2 {
			r, g, b, _ := img.At(x, y).RGBA()
			if pred(r>>8, g>>8, b>>8) {
				n++
			}
		}
	}
	return n
}

func TestIntegrationCapture(t *testing.T) {
	chrome := DetectChrome()
	if chrome == "" {
		t.Skip("no Chrome/Chromium available")
	}
	cases := []struct {
		name, banner string
		sels         []string
	}{
		{"generic", "generic", nil},
		{"custom", "custom", []string{"span.weird"}},
		{"stuck", "stuck", nil},
	}
	var sources []domain.Source
	for _, c := range cases {
		s := srv(c.banner)
		defer s.Close()
		sources = append(sources, domain.Source{ID: c.name, Homepage: s.URL, Screenshot: true, CookieSelectors: c.sels})
	}
	sources = append(sources, domain.Source{ID: "broken", Homepage: "http://127.0.0.1:1/", Screenshot: true})
	dir := t.TempDir()
	st := &fakeStore{}
	now := time.Date(2026, 3, 4, 17, 5, 0, 0, time.Local)
	tk := New(st, Options{
		Sources: sources, Dir: dir, ChromePath: chrome, Width: 800, Height: 600, MaxHeight: 2500,
		SettleDelay: 200 * time.Millisecond, PageTimeout: 40 * time.Second, Retention: 24 * time.Hour,
		Now: func() time.Time { return now },
	}, quiet())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	n, err := tk.RunOnce(ctx)
	if err != nil || n != 3 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}
	if len(st.shots) != 3 {
		t.Fatalf("records: %d", len(st.shots))
	}
	for _, s := range st.shots {
		if s.Path != s.SourceID+"/2026/03/04/1705.jpg" {
			t.Errorf("path %q", s.Path)
		}
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(s.Path)))
		if err != nil {
			t.Fatal(err)
		}
		img, format, err := image.Decode(f)
		f.Close()
		if err != nil || format != "jpeg" {
			t.Fatalf("%s: decode %v %s", s.SourceID, err, format)
		}
		b := img.Bounds()
		if b.Dx() != 800 || b.Dy() != 2500 || s.Width != 800 || s.Height != 2500 || s.Bytes <= 0 {
			t.Errorf("%s: size %v record %+v", s.SourceID, b, s)
		}
		red := countColor(img, func(r, g, b uint32) bool { return r > 200 && g < 60 && b < 60 })
		blue := countColor(img, func(r, g, b uint32) bool { return b > 200 && r < 60 && g < 60 })
		green := countColor(img, func(r, g, b uint32) bool { return g > 130 && r < 80 && b < 100 })
		if red != 0 {
			t.Errorf("%s: cookie banner still visible (%d red px)", s.SourceID, red)
		}
		if blue != 0 {
			t.Errorf("%s: fixed overlay still visible (%d blue px)", s.SourceID, blue)
		}
		if green == 0 {
			t.Errorf("%s: page content missing", s.SourceID)
		}
	}

	// no leftover tmp files
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error {
		if strings.Contains(d.Name(), ".tmp-") {
			t.Errorf("leftover tmp file %s", p)
		}
		return nil
	})

	// retention
	later := now.Add(48 * time.Hour)
	tk.opts.Now = func() time.Time { return later }
	c, err := tk.Cleanup(ctx)
	if err != nil || c != 3 {
		t.Fatalf("cleanup: %d %v", c, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Errorf("dir not empty after cleanup: %v", ents)
	}
}

func TestAllFailReturnsError(t *testing.T) {
	chrome := DetectChrome()
	if chrome == "" {
		t.Skip("no Chrome/Chromium available")
	}
	tk := New(&fakeStore{}, Options{Dir: t.TempDir(), ChromePath: chrome, PageTimeout: 15 * time.Second,
		SettleDelay: 0, Sources: []domain.Source{{ID: "x", Homepage: "http://127.0.0.1:1/", Screenshot: true}}}, quiet())
	n, err := tk.RunOnce(context.Background())
	if n != 0 || err == nil {
		t.Fatalf("expected total failure, got %d %v", n, err)
	}
}

func TestContextCancelKillsBrowser(t *testing.T) {
	chrome := DetectChrome()
	if chrome == "" {
		t.Skip("no Chrome/Chromium available")
	}
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer hang.Close()
	tk := New(&fakeStore{}, Options{Dir: t.TempDir(), ChromePath: chrome, PageTimeout: time.Minute,
		Sources: []domain.Source{{ID: "h", Homepage: hang.URL, Screenshot: true}}}, quiet())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	n, err := tk.RunOnce(ctx)
	if n != 0 || err == nil {
		t.Fatalf("expected error, got %d %v", n, err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("cancel took too long: %v", d)
	}
}
