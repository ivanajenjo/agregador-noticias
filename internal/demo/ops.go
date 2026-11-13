package demo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
)

// brokenFeeds: outlet|section -> (error, hours since the last good fetch).
var brokenFeeds = map[string]struct {
	Err   string
	Hours float64
}{
	"elplural|portada":           {"HTTP 503 Service Unavailable", 6.5},
	"okdiario|españa":            {"Get \"https://okdiario.com/feed/espana\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)", 2.4},
	"libertaddigital|portada":    {"HTTP 403 Forbidden (el servidor rechaza al agente de usuario)", 26},
	"theobjective|internacional": {"feed vacío: 0 elementos", 3.1},
}

func (g *gen) feedsAndJobs(fix *fixups) error {
	ids := append([]string(nil), g.sum.Outlets...)
	sort.Strings(ids)
	n := 0
	for _, id := range ids {
		src := g.srcs[id]
		for _, f := range src.Feeds {
			sum := sha256.Sum256([]byte(f.URL))
			fs := &domain.FeedState{SourceID: id, FeedURL: f.URL, ETag: `W/"` + hex.EncodeToString(sum[:5]) + `"`,
				LastCount: 12 + g.r.IntN(40)}
			okAt := g.now.Add(-time.Duration(2+g.r.IntN(24)) * time.Minute)
			fs.LastModified = okAt.Add(-time.Duration(g.r.IntN(30)) * time.Minute).Format(time.RFC1123)
			fs.LastOK = okAt
			if b, bad := brokenFeeds[id+"|"+f.Section]; bad {
				fs.LastOK = g.now.Add(-time.Duration(b.Hours * float64(time.Hour)))
				fs.LastError = b.Err
				fs.LastErrorAt = g.now.Add(-time.Duration(1+g.r.IntN(20)) * time.Minute)
				if strings.HasPrefix(b.Err, "feed vacío") {
					fs.LastCount = 0
				}
			}
			if err := g.st.SaveFeedState(g.ctx, fs); err != nil {
				return err
			}
			n++
		}
	}
	g.log("feed states: %d", n)

	type run struct {
		job string
		at  time.Time
		dur time.Duration
		ok  bool
		msg string
	}
	var runs []run
	// ingest every 30 minutes during the last 18 hours
	for i := 0; i < 36; i++ {
		at := g.now.Add(-time.Duration(i)*30*time.Minute - time.Duration(g.r.IntN(40))*time.Second)
		r := run{job: "ingest", at: at, dur: time.Duration(18+g.r.IntN(30)) * time.Second, ok: true,
			msg: fmt.Sprintf("%d nuevas", g.r.IntN(14))}
		switch i {
		case 3:
			r.ok, r.msg = false, "ingest: 1 feed con error (okdiario: context deadline exceeded)"
		case 11:
			r.ok, r.msg = false, "ingest: 2 feeds con error (elplural: HTTP 503, okdiario: timeout)"
		case 29:
			r.ok, r.msg = false, "ingest: 1 feed con error (elplural: HTTP 503)"
		}
		runs = append(runs, r)
	}
	// screenshots three times a day, digest at 08:00, cleanup and maintenance at night
	for d := 0; d < 7; d++ {
		for _, hm := range [][2]int{{9, 0}, {17, 0}, {22, 0}} {
			at := g.day0(d).Add(time.Duration(hm[0])*time.Hour + time.Duration(g.r.IntN(5))*time.Minute).UTC()
			if at.After(g.now) {
				continue
			}
			r := run{job: "screenshots", at: at, dur: time.Duration(95+g.r.IntN(80)) * time.Second, ok: true, msg: fmt.Sprintf("%d capturas", len(shotOutlets))}
			if d == 2 && hm[0] == 17 {
				r.ok, r.msg = false, "11 capturas, 1 fallo (elplural: timeout esperando la carga de la página)"
			}
			runs = append(runs, r)
		}
		for _, j := range []struct {
			name string
			h, m int
			msg  string
		}{{"digest", 8, 0, "resumen: sin narrativa"}, {"cleanup", 4, 30, "0 capturas borradas"}, {"maintenance", 4, 45, "0 artículos borrados"}} {
			at := g.day0(d).Add(time.Duration(j.h)*time.Hour + time.Duration(j.m)*time.Minute + time.Duration(g.r.IntN(50))*time.Second).UTC()
			if at.After(g.now) {
				continue
			}
			runs = append(runs, run{job: j.name, at: at, dur: time.Duration(1+g.r.IntN(6)) * time.Second, ok: true, msg: j.msg})
		}
	}
	for i := 0; i < 12; i++ {
		at := g.now.Add(-time.Duration(i)*5*time.Minute - time.Duration(g.r.IntN(20))*time.Second)
		runs = append(runs, run{job: "alerts", at: at, dur: time.Duration(200+g.r.IntN(600)) * time.Millisecond, ok: true, msg: "0 alertas nuevas"})
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].at.Before(runs[j].at) })
	for _, r := range runs {
		id, err := g.st.StartJob(g.ctx, r.job)
		if err != nil {
			return err
		}
		if err := g.st.FinishJob(g.ctx, id, r.ok, r.msg); err != nil {
			return err
		}
		fix.jobs = append(fix.jobs, jobFix{id: id, start: r.at, end: r.at.Add(r.dur)})
	}
	g.log("job runs: %d", len(runs))
	return nil
}

// follow creates the watches ("seguimientos") and the alert log.
func (g *gen) follow(fix *fixups) error {
	ws := []domain.Watch{
		{Name: "Borrasca", Query: "borrasca", Notify: true},
		{Name: "Ley de Vivienda", Query: `"ley de vivienda"`, Notify: true},
		{Name: "Bolsa", Query: "bolsa", Notify: false},
	}
	for i := range ws {
		w := &ws[i]
		if err := g.st.CreateWatch(g.ctx, w); err != nil {
			return err
		}
		fix.watches = append(fix.watches, watchFix{id: w.ID, created: g.now.Add(-time.Duration(12-3*i) * 24 * time.Hour)})
		if err := g.st.SetWatchEvaluated(g.ctx, w.ID, g.now.Add(-time.Duration(2+i)*time.Minute)); err != nil {
			return err
		}
		hits, err := g.st.SearchText(g.ctx, w.Query, domain.Filter{}, 200)
		if err != nil {
			return err
		}
		var ids []int64
		for _, h := range hits {
			ids = append(ids, h.Article.ID)
		}
		if err := g.st.MarkWatchHits(g.ctx, w.ID, ids, g.now.Add(-time.Hour)); err != nil {
			return err
		}
		g.sum.Watches++
	}

	// Real story ids from the engine so the alert links resolve.
	eng := trends.New(g.st, trends.Options{})
	sts, err := eng.Stories(g.ctx, domain.TrendOptions{Now: g.now})
	if err != nil {
		return err
	}
	storyID := func(sub string) (string, string) {
		for _, s := range sts {
			if strings.Contains(strings.ToLower(s.Title+" "+strings.Join(s.Terms, " ")), sub) {
				return s.ID, s.Title
			}
		}
		return "", ""
	}
	type al struct {
		kind, key, title, url, ch, detail string
		ok                                bool
		ago                               time.Duration
	}
	var rows []al
	add := func(sub, ch, detail string, ok bool, ago time.Duration) {
		id, title := storyID(sub)
		if id == "" {
			return
		}
		rows = append(rows, al{domain.AlertStory, id, title, "/encuadre/" + id, ch, detail, ok, ago})
	}
	add("borrasca", "ntfy", "HTTP 200", true, 13*time.Hour+10*time.Minute)
	add("borrasca", "telegram", "telegram: HTTP 502 Bad Gateway", false, 13*time.Hour+10*time.Minute)
	add("huelga", "ntfy", "HTTP 200", true, 9*time.Hour+30*time.Minute)
	add("tractorada", "ntfy", "HTTP 200", true, 5*time.Hour+20*time.Minute)
	rows = append(rows,
		al{domain.AlertWatch, "borrasca:ciro", "Seguimiento «Borrasca»: 4 artículos nuevos", "/buscar?q=borrasca", "ntfy", "HTTP 200", true, 13 * time.Hour},
		al{domain.AlertWatch, "ley-de-vivienda", "Seguimiento «Ley de Vivienda»: 3 artículos nuevos", `/buscar?q="ley de vivienda"`, "ntfy", "HTTP 200", true, 50 * time.Hour},
		al{domain.AlertWatch, "bolsa", "Seguimiento «Bolsa»: 2 artículos nuevos", "/buscar?q=bolsa", domain.ChannelLog, "sin canal configurado: solo registro", true, 31 * time.Hour},
		al{domain.AlertTest, "test-1", "Mensaje de prueba del agregador", "", "ntfy", "HTTP 200", true, 6 * 24 * time.Hour},
	)
	for _, r := range rows {
		if err := g.st.SaveAlert(g.ctx, domain.AlertRecord{Kind: r.kind, Key: r.key, Title: r.title, URL: r.url,
			SentAt: g.now.Add(-r.ago), Channel: r.ch, OK: r.ok, Detail: r.detail}); err != nil {
			return err
		}
		g.sum.Alerts++
	}
	g.log("watches %d, alerts %d", g.sum.Watches, g.sum.Alerts)
	return nil
}
