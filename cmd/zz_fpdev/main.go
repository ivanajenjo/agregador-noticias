package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/shot"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
	"github.com/ivanajenjo/agregador-noticias/internal/web"
)

const page = `<!doctype html><html><head><meta charset=utf-8><style>
body{margin:0;font-family:Georgia,serif}.hero h1{font-size:46px;margin:20px;line-height:1.1}.hero img{display:block;margin:0 20px;height:200px;background:#789}
.grid{display:grid;grid-template-columns:1fr 1fr 1fr;gap:16px;padding:20px}.grid h2{font-size:24px;margin:0}
aside h3{font-size:16px;margin:8px 20px}a{color:#111;text-decoration:none}nav{background:#222;padding:10px}nav a{color:#fff}
</style></head><body><nav><a href="/">Inicio</a></nav>
<div class=hero><h1><a href="/n/@H1@">@T1@</a></h1><img alt=""></div>
<div class=grid>
<h2><a href="/n/@H2@">@T2@</a></h2><h2><a href="/n/@H3@">@T3@</a></h2><h2><a href="/n/@H4@">@T4@</a></h2></div>
<aside><h3><a href="/n/@H5@">@T5@</a></h3><h3><a href="/n/@H6@">@T6@</a></h3></aside><div style="height:1500px"></div>
</body></html>`

var titles = map[string]string{
	"vivienda":  "El Gobierno aprueba la ley de vivienda tras una larga negociación",
	"inflacion": "La inflación baja dos décimas en octubre hasta el 3,1% anual",
	"clasico":   "El Madrid gana el clásico en el minuto 94 de partido",
	"lluvia":    "Lluvias torrenciales en Valencia con alerta roja en la costa",
	"alpinista": "Hallan el cuerpo de un alpinista desaparecido hace una semana",
	"cultura":   "Estreno de la película premiada en el festival de Cannes",
	"xss":       "Titular con <img src=x onerror=alert(1)> y comillas \" peligrosas",
}

func main() {
	dir := os.Args[1]
	st, err := store.Open(dir + "/dev.db")
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	lay := func(order ...string) string {
		s := page
		for i, k := range order {
			s = strings.ReplaceAll(s, fmt.Sprintf("@H%d@", i+1), k)
			s = strings.ReplaceAll(s, fmt.Sprintf("@T%d@", i+1), titles[k])
		}
		return s
	}
	var cur = map[string]string{}
	mk := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(cur[id]))
		}))
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	srcs := []domain.Source{
		{ID: "a", Name: "Diario Uno", Leaning: "izquierda", Homepage: a.URL, Screenshot: true},
		{ID: "b", Name: "El Centro", Leaning: "centro", Homepage: b.URL, Screenshot: true},
		{ID: "c", Name: "La Derecha <b>", Leaning: "derecha", Homepage: c.URL, Screenshot: true},
	}
	_ = st.UpsertSources(ctx, srcs)
	// articles so canonical URLs link to stories
	now := time.Now()
	for _, s := range srcs {
		u := strings.Replace(s.Homepage, "http://", "https://", 1) + "/n/vivienda"
		_, _ = st.InsertArticle(ctx, &domain.Article{SourceID: s.ID, URL: u, CanonicalURL: u, Title: titles["vivienda"], FirstSeenAt: now.Add(-3 * time.Hour)})
	}
	var tk *shot.Taker
	cfgNow := time.Now()
	for i := 0; i < 5; i++ {
		at := cfgNow.Add(-time.Duration(4-i) * 40 * time.Minute)
		lead := "vivienda"
		if i == 3 {
			lead = "inflacion"
		}
		cur["a"] = lay(lead, "inflacion", "clasico", "lluvia", "alpinista", "cultura")
		cur["b"] = lay("vivienda", "clasico", "inflacion", "cultura", "xss", "lluvia")
		if i >= 2 {
			cur["c"] = lay("clasico", "vivienda", "xss", "inflacion", "lluvia", "alpinista")
		} else {
			cur["c"] = lay("xss", "clasico", "vivienda", "inflacion", "lluvia", "alpinista")
		}
		tk = shot.New(st, shot.Options{Sources: srcs, Dir: dir + "/shots", Width: 1100, Height: 700, MaxHeight: 3000, SettleDelay: 100 * time.Millisecond,
			Now: func() time.Time { return at }}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
		if n, err := tk.RunOnce(ctx); err != nil || n != 3 {
			fmt.Println("shots", n, err)
		}
	}
	eng := trends.New(st, trends.Options{})
	h := web.New(web.Deps{Store: st, Prominence: st, Trends: eng, Shots: tk, ShotsDir: dir + "/shots"}, web.Options{Version: "dev"})
	fmt.Println("listening :8791")
	panic(http.ListenAndServe("127.0.0.1:8791", h))
}
