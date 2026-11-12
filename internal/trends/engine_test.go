package trends

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ---- fake store ------------------------------------------------------------

type fakeStore struct {
	sources []domain.Source
	arts    []domain.ArticleLite
	calls   atomic.Int32
}

func (f *fakeStore) ListSources(ctx context.Context) ([]domain.Source, error) {
	return f.sources, nil
}

func (f *fakeStore) ArticleTexts(ctx context.Context, from, to time.Time) ([]domain.ArticleLite, error) {
	f.calls.Add(1)
	var out []domain.ArticleLite
	for _, a := range f.arts {
		if !a.At.Before(from) && a.At.Before(to) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (f *fakeStore) add(src, title, summary string, at time.Time) {
	id := int64(len(f.arts) + 1)
	f.arts = append(f.arts, domain.ArticleLite{ID: id, SourceID: src, Title: title, Summary: summary,
		URL: fmt.Sprintf("https://%s.example/%d", src, id), At: at})
}

var scenarioNow = time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)

var outlets = []struct{ id, name, leaning string }{
	{"eldiario", "elDiario.es", "izquierda"},
	{"publico", "Público", "izquierda"},
	{"elpais", "El País", "centro-izquierda"},
	{"elperiodico", "El Periódico", "centro-izquierda"},
	{"efe", "EFE", "centro"},
	{"rtve", "RTVE", "centro"},
	{"lavanguardia", "La Vanguardia", "centro"},
	{"20minutos", "20minutos", "centro"},
	{"elmundo", "El Mundo", "centro-derecha"},
	{"abc", "ABC", "derecha"},
	{"larazon", "La Razón", "derecha"},
	{"okdiario", "OKDiario", "derecha"},
}

func newScenarioStore() *fakeStore {
	f := &fakeStore{}
	for _, o := range outlets {
		f.sources = append(f.sources, domain.Source{ID: o.id, Name: o.name, Leaning: o.leaning})
	}
	rng := rand.New(rand.NewSource(7))
	pick := func(l []string) string { return l[rng.Intn(len(l))] }
	subjects := []string{"El Gobierno", "Pedro Sánchez", "Feijóo", "El Congreso", "El Real Madrid", "El Barcelona", "El Banco de España", "Hacienda", "La Ley de Vivienda",
		"Yolanda Díaz", "Santiago Abascal", "El Senado", "La Generalitat", "El Ayuntamiento de Madrid", "Iberdrola", "Mercadona", "La Fiscalía", "El Supremo", "Bruselas", "El Atlético",
		"El Sevilla", "Telefónica", "El Ibex", "Junts", "El PNV", "Sumar", "Vox", "Podemos", "La Comisión Europea", "Aena"}
	verbs := []string{"aprueba", "defiende", "rechaza", "estudia", "anuncia", "critica", "presenta", "negocia", "firma", "lanza", "frena", "avisa", "prepara", "exige", "pospone",
		"investiga", "propone", "cierra", "abre", "recorta"}
	extras := []string{"", "", "", " tras una reunión", " en Bruselas", " ante la oposición", " según fuentes oficiales", " el lunes", " con polémica", " por sorpresa"}
	// Zipf-distributed pseudo vocabulary: a few frequent topics, a long tail.
	syl := []string{"ba", "ce", "di", "fo", "gu", "ha", "ji", "ko", "lu", "ma", "ne", "po", "qui", "ra", "su", "to", "vu", "xe", "yo", "zi"}
	vocab := make([]string, 3000)
	for i := range vocab {
		vocab[i] = syl[rng.Intn(len(syl))] + syl[rng.Intn(len(syl))] + syl[rng.Intn(len(syl))] + []string{"n", "r", "l", "s"}[rng.Intn(4)] + fmt.Sprint(i % 7)[:0]
	}
	zipf := rand.NewZipf(rng, 1.05, 30, uint64(len(vocab)-1))
	obj := func() string { return vocab[zipf.Uint64()] + " " + vocab[zipf.Uint64()] }
	bg := func() string {
		return fmt.Sprintf("%s %s %s%s", pick(subjects), pick(verbs), obj(), pick(extras))
	}
	for d := 14; d >= 1; d-- {
		for _, o := range outlets {
			for k := 0; k < 5; k++ {
				at := scenarioNow.Add(-time.Duration(d)*24*time.Hour + time.Duration(rng.Intn(20*60))*time.Minute)
				f.add(o.id, bg(), "Resumen de la noticia sobre economía y política nacional.", at)
			}
		}
	}
	// a few Valencia (football) mentions in the baseline
	for i := 0; i < 6; i++ {
		f.add(outlets[i].id, "El Valencia ficha a un delantero para la próxima temporada", "", scenarioNow.Add(-time.Duration(3+i)*24*time.Hour))
	}
	// last 24h: regular chatter too
	for _, o := range outlets {
		for k := 0; k < 4; k++ {
			at := scenarioNow.Add(-time.Duration(rng.Intn(23*60)+30) * time.Minute)
			f.add(o.id, bg(), "Resumen de la noticia sobre economía y política nacional.", at)
		}
	}
	at := func(h float64) time.Time { return scenarioNow.Add(-time.Duration(h * float64(time.Hour))) }

	// Story A: DANA in Valencia, 6 outlets; EFE first.
	f.add("efe", "Inundaciones en Valencia por la dana: la Generalitat activa el plan de emergencia", "La Aemet mantiene la alerta roja.", at(10))
	f.add("elpais", "La DANA deja inundaciones en Valencia y obliga a desalojar a miles de vecinos", "", at(9.5))
	f.add("eldiario", "Valencia, bajo el agua: la dana provoca desalojos y cortes de carreteras", "", at(9))
	f.add("elmundo", "Alerta roja en Valencia por la DANA: suspendidas las clases", "", at(8.5))
	f.add("abc", "La DANA arrasa Valencia con lluvias torrenciales e inundaciones", "", at(8))
	f.add("rtve", "Emergencia por la dana en Valencia: cientos de desalojados", "", at(7))
	f.add("elpais", "Los vecinos de Valencia relatan la noche de la DANA", "", at(3))
	f.add("efe", "La DANA deja de nuevo lluvias intensas en Valencia", "", at(2))

	// Story B: 4 outlets, unrelated: resignation of a (fictional) federation president.
	f.add("elmundo", "Dimite Luis Mendoza, presidente de la Federación de Ajedrez, por el escándalo de las apuestas", "", at(6))
	f.add("larazon", "Escándalo en el ajedrez: Mendoza renuncia a la presidencia de la Federación", "", at(5.5))
	f.add("abc", "La Federación de Ajedrez pierde a su presidente Luis Mendoza por las apuestas", "", at(5))
	f.add("lavanguardia", "Luis Mendoza dimite de la Federación de Ajedrez tras el escándalo", "", at(4))

	// Story C: 3 outlets about the new housing law agreement (shares "Pedro Sánchez" with background).
	f.add("elpais", "Sánchez cierra con sus socios el pacto del alquiler para la Ley de Vivienda", "", at(5))
	f.add("publico", "El pacto del alquiler desbloquea la Ley de Vivienda tras meses de bloqueo", "", at(4.5))
	f.add("eldiario", "Socios y Gobierno sellan el pacto del alquiler en la Ley de Vivienda", "", at(4))

	// Burst topic with an entity: "Isabel Torres" (3 outlets)
	f.add("elpais", "Isabel Torres dimite como ministra de Industria", "", at(11))
	f.add("elmundo", "Polémica por la dimisión de Isabel Torres", "", at(10))
	f.add("rtve", "Tras la salida de Isabel Torres, el Gobierno busca relevo", "", at(9))
	f.add("abc", "El PP exige explicaciones por Isabel Torres", "", at(8))

	// Single-outlet burst: OKDiario only, many times.
	for i := 0; i < 8; i++ {
		f.add("okdiario", fmt.Sprintf("Nueva revelación sobre el caso Zafra número %d", i), "El caso Zafra sigue creciendo.", at(float64(i)*2+1))
	}
	return f
}

func newEngine(f *fakeStore) *Engine {
	return New(f, Options{Now: func() time.Time { return scenarioNow }})
}

func termByKey(ts []domain.Term, key string) *domain.Term {
	for i := range ts {
		if ts[i].Key == key {
			return &ts[i]
		}
	}
	return nil
}

func rankOfKey(ts []domain.Term, key string) int {
	for i := range ts {
		if ts[i].Key == key {
			return i
		}
	}
	return -1
}

func TestTrendingScenario(t *testing.T) {
	f := newScenarioStore()
	e := newEngine(f)
	rep, err := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow})
	if err != nil {
		t.Fatal(err)
	}
	for i, tm := range rep.Terms {
		t.Logf("%2d %-28s %-6s n=%d src=%d score=%.1f new=%v spark=%v", i, tm.Term, tm.Kind, tm.Count, tm.Sources, tm.Score, tm.New, tm.Spark)
	}
	if rep.Articles == 0 || rep.Window != 24*time.Hour || rep.Baseline != 14*24*time.Hour {
		t.Fatalf("report header %+v", rep)
	}
	dana := termByKey(rep.Terms, "dana")
	if dana == nil {
		t.Fatal("dana not trending")
	}
	if r := rankOfKey(rep.Terms, "dana"); r > 3 {
		t.Errorf("dana rank %d, want top 4", r)
	}
	if !dana.New || dana.Sources != 6 || dana.Count < 7 || dana.Term != "DANA" && dana.Term != "dana" {
		t.Errorf("dana: %+v", dana)
	}
	if dana.Spark[13] != dana.Count || dana.Spark[12] != 0 || len(dana.Spark) != 14 {
		t.Errorf("spark %v", dana.Spark)
	}
	if dana.FirstAt.IsZero() || dana.Leanings["centro"] == 0 || dana.Leanings["derecha"] == 0 {
		t.Errorf("leanings/firstAt %+v", dana)
	}
	// Valencia is also bursting but was in the baseline (football): not "new".
	if v := termByKey(rep.Terms, "valencia"); v == nil {
		t.Error("valencia should trend")
	}
	// single-outlet burst must not trend
	for _, tm := range rep.Terms {
		if strings.Contains(tm.Key, "zafra") || strings.Contains(tm.Key, "okdiario") {
			t.Errorf("single-outlet term trends: %+v", tm)
		}
	}
	// entity wins over sub-terms
	if it := termByKey(rep.Terms, "isabel torres"); it == nil || it.Kind != "entity" || it.Term != "Isabel Torres" {
		t.Errorf("isabel torres: %+v", it)
	}
	for _, k := range []string{"isabel", "torres"} {
		if termByKey(rep.Terms, k) != nil {
			t.Errorf("sub-term %q must collapse into the entity", k)
		}
	}
	// permanent background chatter must not trend
	for _, k := range []string{"gobierno", "congreso", "pedro sanchez", "pension", "inflacion"} {
		if r := rankOfKey(rep.Terms, k); r >= 0 {
			t.Errorf("background term %q trends (rank %d)", k, r)
		}
	}
	// sorted by score desc
	for i := 1; i < len(rep.Terms); i++ {
		if rep.Terms[i].Score > rep.Terms[i-1].Score {
			t.Fatalf("not sorted at %d", i)
		}
	}
	// MinSources=1 lets the one-outlet burst through (proves the filter is what hides it)
	rep1, _ := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow, MinSources: 1, Limit: 100})
	found := false
	for _, tm := range rep1.Terms {
		if strings.Contains(tm.Key, "zafra") {
			found = true
			if tm.Sources != 1 {
				t.Errorf("%+v", tm)
			}
		}
	}
	if !found {
		t.Error("MinSources=1 should expose the OKDiario burst")
	}
	// MinCount
	repC, _ := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow, MinCount: 8})
	for _, tm := range repC.Terms {
		if tm.Count < 8 {
			t.Errorf("MinCount violated: %+v", tm)
		}
	}
	// Limit
	repL, _ := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow, Limit: 3})
	if len(repL.Terms) != 3 {
		t.Errorf("limit: %d", len(repL.Terms))
	}
	// source restriction: only right-wing outlets; dana still there but with fewer sources
	repS, _ := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow, SourceIDs: []string{"abc", "larazon", "okdiario", "elmundo"}, MinCount: 2})
	if d := termByKey(repS.Terms, "dana"); d == nil || d.Sources != 2 {
		t.Errorf("restricted dana: %+v", d)
	}
}

func TestStoriesScenario(t *testing.T) {
	f := newScenarioStore()
	e := newEngine(f)
	st, err := e.Stories(context.Background(), domain.TrendOptions{Now: scenarioNow})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		t.Logf("%s %q terms=%v src=%d art=%d first=%s score=%.2f", s.ID, s.Title, s.Terms, s.Sources, s.Articles, s.FirstBy, s.Score)
	}
	var a, b, c *domain.Story
	for i := range st {
		s := &st[i]
		switch {
		case strings.Contains(strings.ToLower(s.Title), "dana") || strings.Contains(s.Title, "Valencia"):
			a = s
		case strings.Contains(s.Title, "Mendoza"):
			b = s
		case strings.Contains(s.Title, "alquiler"):
			c = s
		}
	}
	if a == nil || b == nil || c == nil {
		t.Fatalf("missing stories a=%v b=%v c=%v", a != nil, b != nil, c != nil)
	}
	if a == b || a == c || b == c {
		t.Fatal("stories merged")
	}
	if a.Sources != 6 || a.Articles != 8 {
		t.Errorf("DANA story: sources=%d articles=%d", a.Sources, a.Articles)
	}
	if a.FirstBy != "efe" || !a.FirstAt.Equal(scenarioNow.Add(-10*time.Hour)) {
		t.Errorf("FirstBy %s at %v", a.FirstBy, a.FirstAt)
	}
	if len(a.Coverage) != 6 || a.Coverage[0].SourceID != "efe" || a.Coverage[1].SourceID != "elpais" {
		t.Errorf("coverage order: %+v", a.Coverage)
	}
	for i := 1; i < len(a.Coverage); i++ {
		if a.Coverage[i].FirstAt.Before(a.Coverage[i-1].FirstAt) {
			t.Error("coverage not earliest-first")
		}
	}
	for _, sc := range a.Coverage {
		if len(sc.Headlines) == 0 || len(sc.Headlines) > 3 || sc.Name == "" {
			t.Errorf("coverage %+v", sc)
		}
	}
	// efe has 2 headlines, newest first
	if h := a.Coverage[0].Headlines; len(h) != 2 || !h[0].At.After(h[1].At) {
		t.Errorf("efe headlines %+v", h)
	}
	var missing []string
	for _, m := range a.Missing {
		missing = append(missing, m.SourceID)
		if m.Count != 0 {
			t.Errorf("missing with count: %+v", m)
		}
	}
	wantMissing := []string{"publico", "elperiodico", "20minutos", "lavanguardia", "larazon", "okdiario"}
	// sorted by leaning: izquierda, centro-izquierda, centro, centro-derecha(none missing), derecha
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing %v want %v", missing, wantMissing)
	}
	if b.Sources != 4 || b.Articles != 4 || b.FirstBy != "elmundo" {
		t.Errorf("story B %+v", b)
	}
	if c.Sources != 3 || c.FirstBy != "elpais" && c.FirstBy != "eldiario" {
		t.Errorf("story C %+v", c)
	}
	if len(a.Terms) == 0 || len(a.Terms) > 4 {
		t.Errorf("terms %v", a.Terms)
	}
	joined := strings.ToLower(strings.Join(a.Terms, " "))
	if !strings.Contains(joined, "dana") || !strings.Contains(joined, "valencia") {
		t.Errorf("DANA terms %v", a.Terms)
	}
	if a.ID == "" || a.ID == b.ID {
		t.Error("ids")
	}
	// the Isabel Torres coverage (4 outlets) is a story too
	foundIT := false
	for _, s := range st {
		if strings.Contains(strings.Join(s.Terms, " "), "Isabel Torres") {
			foundIT = true
			if s.Sources != 4 {
				t.Errorf("isabel torres story %+v", s)
			}
		}
	}
	if !foundIT {
		t.Error("Isabel Torres story missing")
	}
	// single-outlet noise is never a story
	for _, s := range st {
		if s.Sources < 3 {
			t.Errorf("story below MinSources: %+v", s)
		}
	}
	// ordering by score, and the biggest story (DANA) first
	if st[0].ID != a.ID {
		t.Errorf("first story %q, want DANA", st[0].Title)
	}
	for i := 1; i < len(st); i++ {
		if st[i].Score > st[i-1].Score {
			t.Fatal("not sorted")
		}
	}
	// stable ID across engines
	st2, _ := newEngine(newScenarioStore()).Stories(context.Background(), domain.TrendOptions{Now: scenarioNow})
	if len(st2) != len(st) || st2[0].ID != st[0].ID {
		t.Error("story IDs not stable")
	}
	// MinSources honoured
	st7, _ := e.Stories(context.Background(), domain.TrendOptions{Now: scenarioNow, MinSources: 5})
	foundA := false
	for _, s := range st7 {
		if s.Sources < 5 {
			t.Errorf("MinSources=5 violated: %+v", s)
		}
		foundA = foundA || s.ID == a.ID
	}
	if !foundA {
		t.Error("DANA missing with MinSources=5")
	}
}

func TestTermDetail(t *testing.T) {
	f := newScenarioStore()
	e := newEngine(f)
	d, err := e.Term(context.Background(), "DANA", 7, domain.TrendOptions{Now: scenarioNow})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v related=%v", d.Total, d.Related)
	if d.Key != "dana" || d.Term != "DANA" && d.Term != "dana" || d.Total != 8 {
		t.Errorf("%+v", d)
	}
	if len(d.Coverage) != 6 || d.Coverage[0].SourceID != "efe" {
		t.Errorf("coverage %+v", d.Coverage)
	}
	if len(d.Missing) != 6 {
		t.Errorf("missing %d", len(d.Missing))
	}
	if len(d.Daily) != 7 {
		t.Fatalf("daily %d", len(d.Daily))
	}
	sum := 0
	for _, dc := range d.Daily {
		sum += dc.Total
		l := 0
		for _, n := range dc.Leanings {
			l += n
		}
		if l != dc.Total {
			t.Errorf("leaning breakdown %v != %d", dc.Leanings, dc.Total)
		}
	}
	if sum != 8 || d.Daily[6].Total != 8 || !d.Daily[6].Day.Equal(time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("daily %+v", d.Daily)
	}
	if !contains(d.Related, "Valencia") {
		t.Errorf("related %v", d.Related)
	}
	if len(d.Related) > 8 {
		t.Errorf("related %v", d.Related)
	}
	// multiword key given in display form and as key
	d1, _ := e.Term(context.Background(), "Isabel Torres", 7, domain.TrendOptions{Now: scenarioNow})
	d2, _ := e.Term(context.Background(), "isabel torres", 0, domain.TrendOptions{Now: scenarioNow})
	if d1.Total != 4 || d2.Total != 4 || d1.Key != "isabel torres" || d1.Term != "Isabel Torres" {
		t.Errorf("%+v %+v", d1, d2)
	}
	// plural/accents: "Dimisión"
	d3, _ := e.Term(context.Background(), "dimisión", 7, domain.TrendOptions{Now: scenarioNow})
	if d3.Total != 1 {
		t.Errorf("dimision %+v", d3)
	}
	// unknown term: empty but well-formed
	d4, err := e.Term(context.Background(), "inexistente", 7, domain.TrendOptions{Now: scenarioNow})
	if err != nil || d4.Total != 0 || len(d4.Missing) != 12 || len(d4.Daily) != 7 {
		t.Errorf("unknown %+v %v", d4, err)
	}
	// days window restricts
	d5, _ := e.Term(context.Background(), "valencia", 1, domain.TrendOptions{Now: scenarioNow})
	if d5.Total < 8 || len(d5.Daily) != 1 {
		t.Errorf("1 day: %+v", d5)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestCacheTTL(t *testing.T) {
	f := newScenarioStore()
	clock := scenarioNow
	var mu sync.Mutex
	e := New(f, Options{CacheTTL: 5 * time.Minute, Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }})
	ctx := context.Background()
	if _, err := e.Trending(ctx, domain.TrendOptions{}); err != nil {
		t.Fatal(err)
	}
	n := f.calls.Load()
	if n == 0 {
		t.Fatal("no store call")
	}
	// same options within TTL: nothing recomputed; stories reuse the corpus
	e.Trending(ctx, domain.TrendOptions{})
	e.Stories(ctx, domain.TrendOptions{})
	e.Trending(ctx, domain.TrendOptions{Window: 6 * time.Hour})
	if f.calls.Load() != n {
		t.Errorf("store hit again within TTL: %d -> %d", n, f.calls.Load())
	}
	mu.Lock()
	clock = clock.Add(6 * time.Minute)
	mu.Unlock()
	e.Trending(ctx, domain.TrendOptions{})
	if f.calls.Load() == n {
		t.Error("expired cache not refreshed")
	}
}

func TestSingleflight(t *testing.T) {
	f := newScenarioStore()
	e := newEngine(f)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			switch i % 3 {
			case 0:
				e.Trending(ctx, domain.TrendOptions{Now: scenarioNow})
			case 1:
				e.Stories(ctx, domain.TrendOptions{Now: scenarioNow})
			default:
				e.Term(ctx, "dana", 7, domain.TrendOptions{Now: scenarioNow})
			}
		}(i)
	}
	wg.Wait()
	if n := f.calls.Load(); n != 1 {
		t.Errorf("store calls = %d, want 1 (shared corpus)", n)
	}
}

func TestDeterminism(t *testing.T) {
	var prev *domain.TrendReport
	var prevS []domain.Story
	for i := 0; i < 4; i++ {
		e := newEngine(newScenarioStore())
		r, _ := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow, Limit: 60})
		s, _ := e.Stories(context.Background(), domain.TrendOptions{Now: scenarioNow})
		if prev != nil && (!reflect.DeepEqual(prev, r) || !reflect.DeepEqual(prevS, s)) {
			t.Fatalf("run %d differs", i)
		}
		prev, prevS = r, s
	}
}

func TestEmptyStore(t *testing.T) {
	e := New(&fakeStore{}, Options{})
	r, err := e.Trending(context.Background(), domain.TrendOptions{})
	if err != nil || len(r.Terms) != 0 {
		t.Fatal(r, err)
	}
	s, err := e.Stories(context.Background(), domain.TrendOptions{})
	if err != nil || len(s) != 0 {
		t.Fatal(s, err)
	}
	d, err := e.Term(context.Background(), "x", 3, domain.TrendOptions{})
	if err != nil || d.Total != 0 || len(d.Daily) != 3 {
		t.Fatal(d, err)
	}
}

func TestRace(t *testing.T) {
	f := newScenarioStore()
	e := New(f, Options{CacheTTL: time.Nanosecond, Now: time.Now}) // constant recomputation
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			o := domain.TrendOptions{Now: scenarioNow, Window: time.Duration(6+6*(i%3)) * time.Hour}
			for k := 0; k < 3; k++ {
				if _, err := e.Trending(ctx, o); err != nil {
					t.Error(err)
				}
				if _, err := e.Stories(ctx, o); err != nil {
					t.Error(err)
				}
				if _, err := e.Term(ctx, "dana", 7, o); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestContextCancel(t *testing.T) {
	e := newEngine(newScenarioStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Trending(ctx, domain.TrendOptions{Now: scenarioNow}); err == nil {
		t.Error("expected error on cancelled context")
	}
	// and the failure is not cached
	if _, err := e.Trending(context.Background(), domain.TrendOptions{Now: scenarioNow}); err != nil {
		t.Error(err)
	}
}
