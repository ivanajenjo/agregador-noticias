// Package demo generates a deterministic, fully fictional data directory
// (SQLite database + synthetic front-page JPEGs) that the real application can
// serve, to produce documentation screenshots without network or LLM.
// Only the public API of internal/store is used (plus a small SQL fix-up of
// timestamps the store stamps with the wall clock, see fixTimes).
package demo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Madrid without system tzdata

	"gopkg.in/yaml.v3"

	"github.com/ivanajenjo/agregador-noticias/internal/config"
	"github.com/ivanajenjo/agregador-noticias/internal/digest"
	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
)

// AnnotationModel is the model name written on every annotation row.
const AnnotationModel = "qwen2.5:7b"

// Options configure Generate.
type Options struct {
	Out         string         // data dir (agregador.db + shots/)
	Days        int            // days of background chatter (default 14)
	Seed        int64          // default 42
	Now         time.Time      // zero => time.Now()
	SourcesFile string         // default config/sources.yaml
	Location    *time.Location // default Europe/Madrid
	Log         func(format string, args ...any)
	SkipShots   bool // do not draw the front pages (tests)
}

// Summary describes what Generate wrote.
type Summary struct {
	Now         time.Time
	Outlets     []string
	Articles    int
	StoryArts   int
	Stories     []string // recent story keys
	Revisions   int
	Annotations int
	Screenshots int
	FrontItems  int
	Watches     int
	Alerts      int
	Digests     int
	Hash        string // sha256 over every stored article and revision
}

// demoOutlets are the outlets of the demo (only these are loaded from sources.yaml, so that
// "who did not cover this" lists real participants and no empty columns appear), with their mean articles per working day.
var demoOutlets = []struct {
	ID    string
	Daily float64
	Op    float64 // share of opinion columns
}{
	{"eldiario", 6, .08}, {"infolibre", 3, .08}, {"elplural", 3, .10},
	{"elpais", 6, .07}, {"lasexta", 4, 0}, {"elperiodico", 3, .05},
	{"lavanguardia", 5, .05}, {"20minutos", 6, .02}, {"elconfidencial", 5, .06}, {"europapress", 9, 0}, {"newtral", 2, 0}, {"marca", 7, .03},
	{"elmundo", 6, .07}, {"elespanol", 5, .06}, {"theobjective", 4, .08}, {"larazon", 5, .07}, {"cope", 3, .03}, {"expansion", 5, .04},
	{"abc", 6, .08}, {"okdiario", 5, .10}, {"libertaddigital", 4, .12}, {"eldebate", 3, .10},
}

// shotOutlets are the outlets with synthetic front pages.
var shotOutlets = []string{"elpais", "eldiario", "lavanguardia", "20minutos", "elmundo", "abc", "elconfidencial", "elespanol", "okdiario", "larazon", "lasexta", "elplural"}

type pend struct {
	src, sec, bucket string
	pub, seen        time.Time
	title, sum, body string
	author           string
	st               *story
	ent              *entry
	opinion          bool
	leaning          string
}

type gen struct {
	o      Options
	ctx    context.Context
	st     *store.SQLite
	loc    *time.Location
	now    time.Time
	r      *rand.Rand
	h      hash.Hash
	log    func(string, ...any)
	srcs   map[string]domain.Source
	decks  map[string]*deck
	titles map[string]bool
	uses   map[string]int
	tick   int
	sum    Summary

	arts    []domain.Article // inserted, in ID order (final title after revisions is read back)
	pends   []*pend          // parallel to arts
	storyOf map[int64]*story // article id -> story
}

// Generate writes the demo data dir.
func Generate(ctx context.Context, o Options) (*Summary, error) {
	if o.Out == "" {
		return nil, errors.New("demo: Out is required")
	}
	if o.Days <= 0 {
		o.Days = 14
	}
	if o.Seed == 0 {
		o.Seed = 42
	}
	if o.SourcesFile == "" {
		o.SourcesFile = "config/sources.yaml"
	}
	if o.Location == nil {
		l, err := time.LoadLocation("Europe/Madrid")
		if err != nil {
			return nil, err
		}
		o.Location = l
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	o.Now = o.Now.UTC().Truncate(time.Minute)
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	for _, p := range []string{"agregador.db", "agregador.db-wal", "agregador.db-shm", "sources.yaml"} {
		_ = os.Remove(filepath.Join(o.Out, p))
	}
	if err := os.RemoveAll(filepath.Join(o.Out, "shots")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(o.Out, "shots"), 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(o.Out, "agregador.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	g := &gen{o: o, ctx: ctx, st: st, loc: o.Location, now: o.Now, log: o.Log,
		r: rand.New(rand.NewPCG(uint64(o.Seed), 0x9e3779b97f4a7c15)), h: sha256.New(),
		srcs: map[string]domain.Source{}, decks: map[string]*deck{}, titles: map[string]bool{}, uses: map[string]int{}, storyOf: map[int64]*story{}}
	g.sum.Now = o.Now
	var fix fixups
	if err := g.run(&fix); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	if err := fix.apply(dbPath); err != nil {
		return nil, err
	}
	g.sum.Hash = hex.EncodeToString(g.h.Sum(nil))
	return &g.sum, nil
}

func (g *gen) run(fix *fixups) error {
	if err := g.loadSources(); err != nil {
		return err
	}
	if err := g.insertArticles(); err != nil {
		return err
	}
	if err := g.embed(); err != nil {
		return err
	}
	if err := g.annotate(); err != nil {
		return err
	}
	if !g.o.SkipShots {
		if err := g.screenshots(); err != nil {
			return err
		}
	}
	if err := g.feedsAndJobs(fix); err != nil {
		return err
	}
	if err := g.follow(fix); err != nil {
		return err
	}
	return g.digests()
}

// ---- sources --------------------------------------------------------------

func (g *gen) loadSources() error {
	all, err := config.LoadSources(g.o.SourcesFile)
	if err != nil {
		return err
	}
	in := map[string]bool{}
	for _, d := range demoOutlets {
		in[d.ID] = true
	}
	var out []domain.Source
	for _, s := range all {
		s := s
		if !in[s.ID] {
			continue // the web app lists every stored outlet: only the demo ones are loaded
		}
		on := true
		s.Enabled = &on
		g.srcs[s.ID] = s
		out = append(out, s)
	}
	for _, d := range demoOutlets {
		if _, ok := g.srcs[d.ID]; !ok {
			return fmt.Errorf("demo: outlet %q not in %s", d.ID, g.o.SourcesFile)
		}
		g.sum.Outlets = append(g.sum.Outlets, d.ID)
	}
	// The app upserts its sources file at start-up, so write the demo subset
	// next to the database (AGREGADOR_SOURCES_FILE=<out>/sources.yaml).
	y, err := yaml.Marshal(map[string]any{"sources": out})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(g.o.Out, "sources.yaml"), append([]byte("# generated by demo-seed: the demo outlets of config/sources.yaml\n"), y...), 0o644); err != nil {
		return err
	}
	return g.st.UpsertSources(g.ctx, out)
}

// ---- decks ----------------------------------------------------------------

type deck struct {
	items []string
	pos   int
	last  string
}

func (g *gen) deckFor(key string, items []string) *deck {
	d, ok := g.decks[key]
	if !ok {
		d = &deck{items: append([]string(nil), items...), pos: len(items)}
		g.decks[key] = d
	}
	return d
}

func (g *gen) next(key string, items []string) string {
	d := g.deckFor(key, items)
	if d.pos >= len(d.items) {
		g.r.Shuffle(len(d.items), func(i, j int) { d.items[i], d.items[j] = d.items[j], d.items[i] })
		if len(d.items) > 1 && d.items[0] == d.last {
			d.items[0], d.items[1] = d.items[1], d.items[0]
		}
		d.pos = 0
	}
	v := d.items[d.pos]
	d.pos++
	d.last = v
	return v
}

// lru returns the least recently used pattern of the bucket (round robin from
// a shuffled start), so a pattern is not reused until all the others were.
func (g *gen) lru(bucket string, pats []string) string {
	d := g.deckFor(bucket+"/lru", pats)
	if d.pos == len(d.items) && d.last == "" {
		g.r.Shuffle(len(d.items), func(i, j int) { d.items[i], d.items[j] = d.items[j], d.items[i] })
		d.pos = 0
		d.last = "init"
	}
	v := d.items[d.pos%len(d.items)]
	d.pos++
	return v
}

var slotRe = regexp.MustCompile(`\{(\w+)\}`)

func (g *gen) expand(bk string, b *bucket, pattern string) string {
	s := pattern
	for i := 0; i < 4 && strings.Contains(s, "{"); i++ {
		s = slotRe.ReplaceAllStringFunc(s, func(m string) string {
			name := m[1 : len(m)-1]
			switch name {
			case "pct":
				return fmt.Sprintf("%d,%d", g.r.IntN(4), 1+g.r.IntN(9))
			case "n":
				return strconv.Itoa(3 + g.r.IntN(93))
			case "big":
				return fmt.Sprintf("%d.%03d", 1+g.r.IntN(98), g.r.IntN(1000))
			}
			if vals, ok := b.fills[name]; ok {
				return g.next(bk+"/"+name, vals)
			}
			return m
		})
	}
	return s
}

var opinionTitles = []string{
	"La paciencia de los que esperan", "Elogio de la lentitud", "Lo que cuesta un buen servicio público", "Cartas desde la sala de espera", "El silencio de los despachos",
	"Una conversación pendiente", "Los números y las personas", "Contra el ruido de los lunes", "El país de los plazos", "Lo importante y lo urgente",
	"La memoria corta de los mercados", "Otoño en la ciudad", "Defensa de la letra pequeña", "El Estado y la ventanilla", "Escuchar al que no grita",
	"Pensar el invierno", "La hora de los acuerdos", "De la queja a la propuesta", "Lo público y lo común", "La fatiga del debate",
	"El mapa y el territorio", "Cuentas claras", "Lo que se dice y lo que se hace", "Pequeña teoría del atasco",
}

// ---- articles ---------------------------------------------------------------

var hourWeights = [24]float64{.05, .03, .02, .02, .03, .08, .3, .8, 1.5, 2, 2.2, 2.2, 2, 1.8, 1.4, 1, 1.2, 1.6, 1.8, 1.8, 1.7, 1.2, .7, .3}

func (g *gen) pickHour() int {
	var tot float64
	for _, w := range hourWeights {
		tot += w
	}
	x := g.r.Float64() * tot
	for h, w := range hourWeights {
		if x < w {
			return h
		}
		x -= w
	}
	return 12
}

func slug(s string, max int) string {
	s = strings.ToLower(foldAccents.Replace(strings.ToLower(s)))
	var b strings.Builder
	dash := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > max {
		out = out[:max]
		if i := strings.LastIndex(out, "-"); i > 20 {
			out = out[:i]
		}
	}
	return out
}

func (g *gen) portadaBucket() string {
	x := g.r.Float64()
	switch {
	case x < .28:
		return "politica"
	case x < .48:
		return "economia"
	case x < .72:
		return "sociedad"
	case x < .88:
		return "internacional"
	case x < .98:
		return "cultura"
	}
	return "deportes"
}

func (g *gen) day0(dayOff int) time.Time {
	n := g.now.In(g.loc)
	return time.Date(n.Year(), n.Month(), n.Day()-dayOff, 0, 0, 0, 0, g.loc)
}

func (g *gen) background() []*pend {
	var out []*pend
	for d := g.o.Days - 1; d >= 0; d-- {
		day := g.day0(d)
		wk := 1.0
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			wk = .72
		}
		for _, od := range demoOutlets {
			src := g.srcs[od.ID]
			var secs []string
			for _, f := range src.Feeds {
				secs = append(secs, f.Section)
			}
			n := int(math.Round(od.Daily * .88 * wk * (.8 + .4*g.r.Float64())))
			for i := 0; i < n; i++ {
				t := day.Add(time.Duration(g.pickHour())*time.Hour + time.Duration(g.r.IntN(60))*time.Minute + time.Duration(g.r.IntN(60))*time.Second)
				if t.After(g.now.Add(-25 * time.Minute)) {
					continue
				}
				sec := secs[g.r.IntN(len(secs))]
				p := &pend{src: od.ID, sec: sec, pub: t.UTC(), leaning: src.Leaning}
				switch {
				case src.Kind == "verificacion":
					p.bucket = "bulos"
				case od.Op > 0 && g.r.Float64() < od.Op:
					p.bucket, p.sec, p.opinion = "opinion", "opinión", true
				default:
					p.bucket = sectionBucket(sec)
					if p.bucket == "" {
						p.bucket = g.portadaBucket()
					}
				}
				p.seen = p.pub.Add(time.Duration(1+g.r.IntN(14)) * time.Minute)
				out = append(out, p)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pub.Before(out[j].pub) })
	for _, p := range out {
		if p.bucket == "opinion" {
			p.title = g.next("opinion", opinionTitles)
			p.sum = "Una columna sobre la actualidad y sus consecuencias cotidianas."
		} else {
			b := buckets[p.bucket]
			pat := g.lru(p.bucket, b.patterns)
			for try := 0; try < 12; try++ {
				p.title = g.expand(p.bucket, b, pat)
				if !g.titles[p.title] {
					break
				}
			}
			g.titles[p.title] = true
			p.sum = g.next(p.bucket+"/sum", b.sums)
		}
		p.author = authors[g.r.IntN(len(authors))]
	}
	return out
}

func (g *gen) storyArticles() []*pend {
	var out []*pend
	for i := range stories {
		s := &stories[i]
		var t0 time.Time
		if s.Ago > 0 {
			t0 = g.now.Add(-s.Ago)
		} else {
			t0 = g.day0(s.DayOff).Add(time.Duration(s.Hour)*time.Hour + time.Duration(s.Min)*time.Minute).UTC()
		}
		seenIdx := map[string]int{}
		for j := range s.Entries {
			e := &s.Entries[j]
			src := g.srcs[e.O]
			title, sum := e.T, e.S
			if title == "" {
				k := seenIdx[src.Leaning]
				seenIdx[src.Leaning]++
				title = s.Frames[src.Leaning] + altTails[k%len(altTails)]
			}
			if sum == "" { // vary the default summary a little between outlets
				switch j % 3 {
				case 0:
					sum = s.Sum
				case 1:
					sum = s.Body
				default:
					sum = s.Sum + " " + s.Body
				}
			}
			pub := t0.Add(time.Duration(e.D) * time.Minute)
			if pub.After(g.now.Add(-20 * time.Minute)) {
				pub = g.now.Add(-20 * time.Minute)
			}
			sec := e.Sec
			if sec == "" {
				sec = "portada"
			}
			p := &pend{src: e.O, sec: sec, bucket: "story", pub: pub, seen: pub.Add(time.Duration(2+g.r.IntN(7)) * time.Minute),
				title: title, sum: sum, body: sum + "\n\n" + s.Body, st: s, ent: e, leaning: src.Leaning,
				author: authors[g.r.IntN(len(authors))]}
			out = append(out, p)
		}
	}
	return out
}

func (g *gen) insertArticles() error {
	all := append(g.background(), g.storyArticles()...)
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].seen.Equal(all[j].seen) {
			return all[i].seen.Before(all[j].seen)
		}
		return all[i].src < all[j].src
	})
	for _, p := range all {
		src := g.srcs[p.src]
		host := strings.TrimPrefix(strings.TrimPrefix(src.Homepage, "https://"), "http://")
		pl := p.pub.In(g.loc)
		hsum := sha256.Sum256([]byte(p.src + "|" + p.title))
		u := fmt.Sprintf("https://%s/%s/%04d-%02d-%02d/%s-%s.html", host, slug(p.sec, 20), pl.Year(), int(pl.Month()), pl.Day(), slug(p.title, 70), hex.EncodeToString(hsum[:3]))
		a := &domain.Article{SourceID: p.src, URL: u, CanonicalURL: u, Title: p.title, Summary: p.sum, Content: p.body,
			Author: p.author, Section: p.sec, PublishedAt: p.pub, FirstSeenAt: p.seen}
		ins, err := g.st.InsertArticle(g.ctx, a)
		if err != nil {
			return err
		}
		if !ins {
			continue
		}
		fmt.Fprintf(g.h, "A|%s|%s|%d\n", a.SourceID, a.Title, a.FirstSeenAt.Unix())
		g.arts = append(g.arts, *a)
		g.pends = append(g.pends, p)
		if p.st != nil {
			g.storyOf[a.ID] = p.st
			g.sum.StoryArts++
		}
	}
	g.sum.Articles = len(g.arts)
	g.log("articles: %d (%d in stories)", g.sum.Articles, g.sum.StoryArts)
	for _, s := range stories {
		if s.Ago > 0 {
			g.sum.Stories = append(g.sum.Stories, s.Key)
		}
	}
	// stealth edits
	for i, p := range g.pends {
		if p.ent == nil || p.ent.Rev == "" {
			continue
		}
		a := g.arts[i]
		at := p.seen.Add(time.Duration(p.ent.RA) * time.Minute)
		if at.After(g.now) {
			at = g.now
		}
		ok, _, err := g.st.RecordRevision(g.ctx, a.CanonicalURL, p.ent.Rev, a.Summary, at)
		if err != nil {
			return err
		}
		if ok {
			g.sum.Revisions++
			fmt.Fprintf(g.h, "R|%s|%s|%d\n", a.CanonicalURL, p.ent.Rev, at.Unix())
			g.arts[i].Title = p.ent.Rev
		}
	}
	g.log("revisions: %d", g.sum.Revisions)
	return nil
}

// ---- embeddings -------------------------------------------------------------

func (g *gen) embed() error {
	for _, a := range g.arts {
		txt := a.Text()
		if err := g.st.SaveEmbeddings(g.ctx, a.ID, EmbedModel, []domain.ChunkVec{{Index: 0, Text: txt, Vec: Embed(txt)}}); err != nil {
			return err
		}
	}
	return nil
}

// ---- helpers shared with other files -----------------------------------------

func (g *gen) pickWeighted(w []float64) int {
	var tot float64
	for _, x := range w {
		tot += x
	}
	v := g.r.Float64() * tot
	for i, x := range w {
		if v < x {
			return i
		}
		v -= x
	}
	return len(w) - 1
}

// ---- timestamp fix-ups --------------------------------------------------------

type fixups struct {
	jobs    []jobFix
	watches []watchFix
}
type jobFix struct {
	id         int64
	start, end time.Time
}
type watchFix struct {
	id      int64
	created time.Time
}

// apply patches the columns the store stamps with the wall clock (job_runs
// started/finished, watches.created_at) so the history looks lived-in. It
// runs after the store is closed, on its own connection.
func (f *fixups) apply(dbPath string) error {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer db.Close()
	for _, j := range f.jobs {
		if _, err := db.Exec(`UPDATE job_runs SET started_at=?, finished_at=? WHERE id=?`, j.start.Unix(), j.end.Unix(), j.id); err != nil {
			return err
		}
	}
	for _, w := range f.watches {
		if _, err := db.Exec(`UPDATE watches SET created_at=? WHERE id=?`, w.created.Unix(), w.id); err != nil {
			return err
		}
	}
	return nil
}

// ---- digests -------------------------------------------------------------------

func (g *gen) digests() error {
	for back := 3; back >= 0; back-- {
		day := g.day0(back)
		now := g.now
		if back > 0 {
			if end := g.day0(back - 1).Add(8 * time.Hour); end.Before(now) {
				now = end
			}
		}
		eng := trends.New(g.st, trends.Options{})
		b := digest.New(digest.Deps{Store: g.st, Trends: eng, Digests: g.st, Revisions: g.st},
			digest.Options{Location: g.loc, Now: func() time.Time { return now }})
		ref := day.Add(12 * time.Hour)
		if back == 0 {
			ref = g.now
		}
		d, err := b.Build(g.ctx, ref)
		if err != nil {
			return err
		}
		g.log("digest %s: %d articles, %d stories", d.Day, d.Articles, len(d.Stories))
		g.sum.Digests++
	}
	return nil
}
