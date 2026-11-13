package shot

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const frontFixture = `<!doctype html><html lang=es><head><meta charset=utf-8><style>
body{margin:0;font-family:Georgia,serif;width:100%}
nav.main{background:#222;padding:10px}nav.main a{color:#fff;font-size:15px;margin-right:12px}
nav.main h3{display:inline;font-size:15px}
.wrap{display:grid;grid-template-columns:3fr 1fr;gap:20px;padding:20px}
.hero h1{font-size:48px;line-height:1.1;margin:0 0 10px}.hero img{display:block;width:100%;height:240px;background:#789}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-top:20px}
.grid h2{font-size:26px;margin:0}
aside h3{font-size:16px;margin:0 0 8px}aside li{list-style:none}
.ad-slot h2{font-size:40px}
#cookie-bar{position:fixed;left:0;right:0;bottom:0;background:#eee;padding:12px;z-index:9}
#cookie-bar h2{font-size:30px}
.stickyhead{position:sticky;top:0;background:#fff}.stickyhead h2{font-size:30px}
.below{margin-top:1800px;padding:20px}.below h2{font-size:28px}
footer{background:#111;padding:20px}footer h3{font-size:20px}footer a{color:#fff}
a{color:#111;text-decoration:none}
</style></head><body>
<div id="cookie-bar"><h2><a href="/privacidad/politica-de-cookies-y-datos">Utilizamos cookies propias y de terceros para mejorar</a></h2></div>
<div class="stickyhead"><h2><a href="/seccion/portada-del-dia-edicion-digital">Edición digital de la portada de hoy mismo</a></h2></div>
<nav class="main"><h3><a href="/politica/elecciones-municipales-seccion-completa">Elecciones municipales: toda la sección</a></h3> <a href="/espana">España</a></nav>
<div class="wrap"><main>
 <div class="hero"><a href="/politica/gobierno-aprueba-ley-vivienda.html?utm_source=home&amp;fbclid=zzz"><img alt=""></a>
  <h1><a href="/politica/gobierno-aprueba-ley-vivienda.html?utm_source=home">El Gobierno aprueba la ley de vivienda tras una larga negociación</a></h1></div>
 <div class="ad-slot"><h2><a href="https://ads.example.net/x">Patrocinado: compra ya tu coche nuevo con descuento</a></h2></div>
 <div class="grid">
  <article><h2><a href="/economia/inflacion-baja-dos-decimas-en-octubre.html">La inflación baja dos décimas en octubre hasta el 3,1%</a></h2></article>
  <article><h2><a href="/deportes/el-madrid-gana-el-clasico-en-el-descuento.html">El Madrid gana el clásico en el minuto 94 de partido</a></h2></article>
  <article><a href="/cultura/estreno-pelicula-premiada-en-cannes.html"><h2>Estreno de la película premiada en el festival de Cannes</h2></a></article>
  <article><h2><a href="/externo">Titular que apunta a otro sitio</a></h2><h2><a href="https://otro-medio.example.org/noticia-larga-de-otra-web">Noticia larguísima que enlaza a otro medio distinto</a></h2></article>
 </div></main>
 <aside><h3>Lo más leído</h3><ul>
  <li><h3><a href="/sociedad/lluvias-torrenciales-valencia-alerta-roja.html">Lluvias torrenciales en Valencia con alerta roja</a></h3></li>
  <li><h3><a href="/sociedad/hallan-el-cuerpo-de-un-alpinista-desaparecido.html">Hallan el cuerpo de un alpinista desaparecido</a></h3></li>
  <li><h3><a href="/corto">Corto</a></h3></li>
 </ul></aside></div>
<div class="below"><h2><a href="/opinion/la-columna-del-director-sobre-la-semana.html">La columna del director sobre la semana política</a></h2></div>
<footer><h3><a href="/aviso-legal-y-condiciones-de-uso-del-sitio">Aviso legal y condiciones de uso del sitio web</a></h3></footer>
</body></html>`

type promStore struct {
	fakeStore
	mu2   sync.Mutex
	items map[int64][]domain.FrontItem
}

func (p *promStore) SaveFrontItems(_ context.Context, id int64, it []domain.FrontItem) error {
	p.mu2.Lock()
	defer p.mu2.Unlock()
	if p.items == nil {
		p.items = map[int64][]domain.FrontItem{}
	}
	p.items[id] = it
	return nil
}
func (p *promStore) ListFrontItems(context.Context, string, time.Time) ([]domain.FrontItem, error) {
	return nil, nil
}
func (p *promStore) TopItems(context.Context, time.Time, time.Time, []string, int) ([]domain.TopItem, error) {
	return nil, nil
}
func (p *promStore) ItemHistory(context.Context, string) ([]domain.ItemPoint, error) {
	return nil, nil
}

func TestIntegrationFrontItems(t *testing.T) {
	chrome := DetectChrome()
	if chrome == "" {
		t.Skip("no Chrome/Chromium available")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(frontFixture))
	}))
	defer s.Close()
	st := &promStore{}
	tk := New(st, Options{
		Sources: []domain.Source{{ID: "x", Homepage: s.URL + "/", Screenshot: true}}, Dir: t.TempDir(), ChromePath: chrome,
		Width: 1100, Height: 700, MaxHeight: 4000, SettleDelay: 200 * time.Millisecond, PageTimeout: 40 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if n, err := tk.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("RunOnce %d %v", n, err)
	}
	items := st.items[1]
	if len(items) == 0 {
		t.Fatal("no items saved")
	}
	for _, it := range items {
		t.Logf("#%d p=%.3f font=%.0f y=%.0f fold=%v %s", it.Rank, it.Prominence, it.FontPx, it.Y, it.AboveFold, it.Title)
	}
	byPath := map[string]domain.FrontItem{}
	for _, it := range items {
		byPath[it.CanonicalURL[len("https://"):]] = it
	}
	host := s.URL[len("http://"):]
	get := func(p string) (domain.FrontItem, bool) { it, ok := byPath[host+p]; return it, ok }

	hero, ok := get("/politica/gobierno-aprueba-ley-vivienda.html")
	if !ok || hero.Rank != 1 || hero.Prominence != 1 || !hero.AboveFold || hero.FontPx != 48 {
		t.Fatalf("hero should be #1: %+v", hero)
	}
	if hero.CanonicalURL != "https://"+host+"/politica/gobierno-aprueba-ley-vivienda.html" {
		t.Errorf("tracking not stripped: %s", hero.CanonicalURL)
	}
	infl, ok1 := get("/economia/inflacion-baja-dos-decimas-en-octubre.html")
	mad, ok2 := get("/deportes/el-madrid-gana-el-clasico-en-el-descuento.html")
	cult, ok3 := get("/cultura/estreno-pelicula-premiada-en-cannes.html")
	side, ok4 := get("/sociedad/lluvias-torrenciales-valencia-alerta-roja.html")
	below, ok5 := get("/opinion/la-columna-del-director-sobre-la-semana.html")
	if !(ok1 && ok2 && ok3 && ok4 && ok5) {
		t.Fatalf("missing items: %v %v %v %v %v", ok1, ok2, ok3, ok4, ok5)
	}
	if !(hero.Rank < infl.Rank && infl.Rank < side.Rank && side.Rank < below.Rank) {
		t.Errorf("rank order: hero %d infl %d side %d below %d", hero.Rank, infl.Rank, side.Rank, below.Rank)
	}
	if infl.Prominence <= side.Prominence || side.Prominence <= below.Prominence || below.AboveFold || !infl.AboveFold {
		t.Errorf("prominence/fold: %+v %+v %+v", infl, side, below)
	}
	if mad.DOMOrder < infl.DOMOrder || cult.W <= 0 || cult.H <= 0 {
		t.Errorf("dom order/box: %+v %+v", mad, cult)
	}
	for _, bad := range []string{"/privacidad/", "/seccion/portada", "/politica/elecciones", "/aviso-legal", "/corto", "/externo"} {
		for p := range byPath {
			if len(p) >= len(host+bad) && p[:len(host+bad)] == host+bad {
				t.Errorf("noise item kept: %s", p)
			}
		}
	}
	if len(items) != 7 {
		t.Errorf("want 7 items (hero, inflación, madrid, cultura, 2 aside, columna), got %d", len(items))
	}
	for i, it := range items {
		if it.Rank != i+1 {
			t.Errorf("ranks not consecutive: %+v", it)
		}
	}
}

func TestFrontExtractionFailureKeepsScreenshot(t *testing.T) {
	chrome := DetectChrome()
	if chrome == "" {
		t.Skip("no Chrome/Chromium available")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// breaks querySelectorAll/getComputedStyle used by the extractor
		_, _ = w.Write([]byte(`<html><body><h1><a href="/a/b">Un titular suficientemente largo para pasar</a></h1><script>Document.prototype.querySelectorAll=function(){throw new Error("boom")};Element.prototype.closest=function(){throw new Error("boom")}</script></body></html>`))
	}))
	defer s.Close()
	st := &promStore{}
	tk := New(st, Options{Sources: []domain.Source{{ID: "x", Homepage: s.URL, Screenshot: true}}, Dir: t.TempDir(), ChromePath: chrome,
		SettleDelay: 100 * time.Millisecond, PageTimeout: 30 * time.Second}, quiet())
	if n, err := tk.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce %d %v", n, err)
	}
	if len(st.shots) != 1 || len(st.items) != 0 {
		t.Fatalf("shots %d items %d", len(st.shots), len(st.items))
	}
}

func TestNoFrontItemsWithPlainStore(t *testing.T) {
	tk := New(&fakeStore{}, Options{}, nil)
	if tk.prominenceStore() != nil {
		t.Fatal("plain store must not enable extraction")
	}
	tk = New(&promStore{}, Options{NoFrontItems: true}, nil)
	if tk.prominenceStore() != nil {
		t.Fatal("NoFrontItems ignored")
	}
	if New(&promStore{}, Options{}, nil).prominenceStore() == nil {
		t.Fatal("ProminenceStore not detected")
	}
}

func TestBuildFrontItems(t *testing.T) {
	rp := rawPage{VH: 800, Items: []rawItem{
		{T: "Titular pequeño que aparece abajo del todo en la página", U: "/a/2", X: 10, Y: 3000, W: 300, H: 20, F: 14, O: 1},
		{T: "Titular grande arriba de la portada principal del día", U: "/a/1?utm_source=x#frag", X: 10, Y: 100, W: 800, H: 120, F: 44, O: 2},
		{T: "Titular grande arriba de la portada principal del día", U: "https://www.m.es/a/1", X: 10, Y: 900, W: 300, H: 40, F: 18, O: 3},
		{T: "Enlace a otro sitio web que no es del medio actual", U: "https://other.org/x", X: 0, Y: 10, W: 500, H: 50, F: 30, O: 4},
		{T: "Enlace a la propia portada del medio de comunicación", U: "/", X: 0, Y: 10, W: 500, H: 50, F: 30, O: 5},
		{T: "corto", U: "/a/9", X: 0, Y: 10, W: 500, H: 50, F: 30, O: 6},
		{T: "Un correo que no es una noticia de la portada nunca", U: "mailto:a@b.c", X: 0, Y: 10, W: 500, H: 50, F: 30, O: 7},
	}}
	got := buildFrontItems(rp, "https://www.m.es/")
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].CanonicalURL != "https://www.m.es/a/1" || got[0].Rank != 1 || got[0].Prominence != 1 || got[0].Y != 100 || !got[0].AboveFold {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Rank != 2 || got[1].AboveFold || got[1].Prominence <= 0 || got[1].Prominence >= 0.3 {
		t.Errorf("second: %+v", got[1])
	}
	// cap
	var many rawPage
	many.VH = 800
	for i := 0; i < 100; i++ {
		many.Items = append(many.Items, rawItem{T: "Titular número suficientemente largo " + string(rune('A'+i%26)) + string(rune('a'+i/26)), U: "/n/" + string(rune('a'+i%26)) + string(rune('a'+i/26)), W: 300, H: 30, F: 20, Y: float64(i * 40), O: i + 1})
	}
	if g := buildFrontItems(many, "https://www.m.es/"); len(g) != maxFrontItems {
		t.Errorf("cap: %d", len(g))
	}
	if _, err := parseFrontItems(`{"error":"boom"}`, "https://m.es"); err == nil {
		t.Error("error payload accepted")
	}
}

func TestCanonicalMatchesIngest(t *testing.T) {
	cases := map[string]string{
		"/p/x/?utm_source=a&b=2&a=1#z": "https://www.m.es/p/x?a=1&b=2",
		"HTTP://WWW.M.ES:80/Y":         "https://www.m.es/Y",
		"https://www.m.es/":            "https://www.m.es",
	}
	for in, want := range cases {
		if got, err := canonicalURL(in, "https://www.m.es/home"); err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, err, want)
		}
	}
	if !sameSite("https://cincodias.elpais.com/x", "https://elpais.com/") || sameSite("https://elpais.com/x", "https://elmundo.es/") {
		t.Error("sameSite")
	}
	if siteKey("https://www.bbc.co.uk/a") != "bbc.co.uk" {
		t.Error(siteKey("https://www.bbc.co.uk/a"))
	}
	_ = os.Stdout
}
