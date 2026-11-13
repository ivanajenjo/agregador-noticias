package framing

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestZScoresHandComputed(t *testing.T) {
	// y1={a:3,b:1} y2={a:1,b:3} a={a:1,b:1}: n1=n2=4, a0=2
	// a: delta = ln(4/2) - ln(2/4) = 2 ln 2; var = 1/4+1/2 = .75
	z := ZScores(map[string]float64{"a": 3, "b": 1}, map[string]float64{"a": 1, "b": 3}, map[string]float64{"a": 1, "b": 1})
	want := 2 * math.Ln2 / math.Sqrt(0.75)
	if math.Abs(z["a"]-want) > 1e-9 || math.Abs(z["b"]+want) > 1e-9 {
		t.Fatalf("z=%v want ±%v", z, want)
	}
	if math.Abs(want-1.6008) > 1e-3 {
		t.Fatalf("hand value %v", want)
	}
}

func TestZScoresPriorShrinksCommonWords(t *testing.T) {
	// same counts, but a heavy prior (word common in background) lowers |z|
	y1 := map[string]float64{"x": 3, "y": 3}
	y2 := map[string]float64{"x": 0, "y": 0, "z": 6}
	low := ZScores(y1, y2, map[string]float64{"x": 0.1, "y": 0.1, "z": 0.1})
	hi := ZScores(y1, y2, map[string]float64{"x": 20, "y": 20, "z": 20})
	if !(low["x"] > hi["x"] && hi["x"] > 0) {
		t.Fatalf("low=%v hi=%v", low["x"], hi["x"])
	}
}

func TestDiff(t *testing.T) {
	ops := Diff(strings.Fields("a b c d"), strings.Fields("a c x d"))
	var s string
	for _, o := range ops {
		s += string(o.Kind)
	}
	if s != "=-=+=" {
		t.Fatalf("ops %q", s)
	}
	if len(Diff(nil, nil)) != 0 || len(Diff([]string{"a"}, nil)) != 1 {
		t.Fatal("empty cases")
	}
}

func TestCompareHeadlines(t *testing.T) {
	r := CompareHeadlines([]string{
		"El Gobierno aprueba la ley de vivienda",
		"Sánchez aprueba la ley de vivienda entre aplausos",
		"El Gobierno aprueba la ley de vivienda hoy",
		"Golpe a los propietarios: aprueba la ley de vivienda",
	})
	if r.Consensus == "" || r.ConsensusIx < 0 {
		t.Fatal("no consensus")
	}
	find := func(i int, w string) string {
		for _, x := range r.Words[i] {
			if wordKey(x.Text) == w {
				return x.Class
			}
		}
		return "?"
	}
	if c := find(0, "vivienda"); c != ClassCommon {
		t.Errorf("vivienda: %s", c)
	}
	if c := find(1, "aplausos"); c != ClassUnique {
		t.Errorf("aplausos: %s", c)
	}
	if c := find(3, "golpe"); c != ClassUnique {
		t.Errorf("golpe: %s", c)
	}
	if c := find(0, "gobierno"); c != ClassCommon { // 2 of 4 outlets
		t.Errorf("gobierno: %s", c)
	}
	if c := find(1, "sanchez"); c != ClassUnique {
		t.Errorf("sanchez (accent folded): %s", c)
	}
	if !strings.Contains(strings.Join(r.Omitted[1], " "), "Gobierno") {
		t.Errorf("omitted %v", r.Omitted[1])
	}
}

type fakeProv struct {
	stories []domain.Story
	arts    []domain.ArticleLite
	win     []domain.ArticleLite
}

func (f *fakeProv) Trending(context.Context, domain.TrendOptions) (*domain.TrendReport, error) {
	return nil, nil
}
func (f *fakeProv) Term(context.Context, string, int, domain.TrendOptions) (*domain.TermDetail, error) {
	return nil, nil
}
func (f *fakeProv) Stories(context.Context, domain.TrendOptions) ([]domain.Story, error) {
	return f.stories, nil
}
func (f *fakeProv) StoryArticles(_ context.Context, id string, _ domain.TrendOptions) ([]domain.ArticleLite, error) {
	return f.arts, nil
}
func (f *fakeProv) WindowArticles(context.Context, domain.TrendOptions) ([]domain.ArticleLite, error) {
	return f.win, nil
}

func cov(id, lean string) domain.SourceCoverage {
	return domain.SourceCoverage{SourceID: id, Name: strings.ToUpper(id), Leaning: lean, Count: 1}
}

func TestCoverageBlindSpot(t *testing.T) {
	cfg := DefaultConfig().withDefaults()
	st := domain.Story{ID: "s", Sources: 5,
		Coverage: []domain.SourceCoverage{cov("d1", "derecha"), cov("d2", "derecha"), cov("d3", "centro-derecha"), cov("d4", "derecha"), cov("d5", "centro-derecha")},
		Missing:  []domain.SourceCoverage{cov("d6", "derecha"), cov("i1", "izquierda"), cov("i2", "centro-izquierda"), cov("c1", "centro"), cov("c2", "centro"), cov("u", "")}}
	sc := coverageOf(cfg, st)
	if !sc.Assessed || !sc.Blind || sc.Dominant != 2 {
		t.Fatalf("%+v", sc)
	}
	if sc.Blocks[2].Covered != 5 || sc.Blocks[2].Total != 6 || sc.Blocks[0].Covered != 0 || sc.Blocks[0].Total != 2 {
		t.Fatalf("matrix %+v", sc.Blocks)
	}
	if len(sc.Absent) != 2 {
		t.Fatalf("absent %v", sc.Absent)
	}
	if !strings.HasPrefix(sc.Summary, "Cubierta por 5 medios de Derecha") || !strings.Contains(sc.Summary, "0 de Izquierda") {
		t.Fatalf("summary %q", sc.Summary)
	}
	if !strings.Contains(sc.Statement, "No aparece en nuestras fuentes de Izquierda ni Centro") {
		t.Fatalf("statement %q", sc.Statement)
	}

	// balanced coverage: not a blind spot
	st2 := domain.Story{ID: "b", Sources: 4,
		Coverage: []domain.SourceCoverage{cov("d1", "derecha"), cov("d2", "derecha"), cov("i1", "izquierda"), cov("i2", "centro-izquierda")},
		Missing:  []domain.SourceCoverage{cov("d3", "derecha"), cov("c1", "centro"), cov("c2", "centro")}}
	if sc2 := coverageOf(cfg, st2); sc2.Blind {
		t.Fatalf("balanced flagged: %+v", sc2)
	}
	// block sizes normalise: 2 of 2 left outlets vs 2 of 8 right ones is NOT right-skewed
	st3 := domain.Story{ID: "n", Sources: 4,
		Coverage: []domain.SourceCoverage{cov("i1", "izquierda"), cov("i2", "izquierda"), cov("d1", "derecha"), cov("d2", "derecha")}}
	for i := 3; i < 9; i++ {
		st3.Missing = append(st3.Missing, cov("d"+string(rune('0'+i)), "derecha"))
	}
	st3.Missing = append(st3.Missing, cov("c1", "centro"), cov("c2", "centro"))
	if sc3 := coverageOf(cfg, st3); sc3.Blind && sc3.Dominant == 2 {
		t.Fatalf("unnormalised: %+v", sc3)
	}
	// too few outlets in a block: cannot assess
	st4 := domain.Story{ID: "f", Sources: 3,
		Coverage: []domain.SourceCoverage{cov("d1", "derecha"), cov("d2", "derecha"), cov("d3", "derecha")},
		Missing:  []domain.SourceCoverage{cov("i1", "izquierda")}}
	if sc4 := coverageOf(cfg, st4); sc4.Assessed || sc4.Blind {
		t.Fatalf("%+v", sc4)
	}
}

func TestBlindSpotsConfigurable(t *testing.T) {
	st := domain.Story{ID: "s", Sources: 4,
		Coverage: []domain.SourceCoverage{cov("d1", "derecha"), cov("d2", "derecha"), cov("d3", "derecha"), cov("i1", "izquierda")},
		Missing:  []domain.SourceCoverage{cov("i2", "izquierda"), cov("i3", "izquierda"), cov("i4", "izquierda"), cov("c1", "centro"), cov("c2", "centro")}}
	// right rate 1.0, left .25, center 0 => skew .8: below 0.9, above .75
	svc := New(&fakeProv{stories: []domain.Story{st}}, Config{MinSkew: 0.9})
	r, err := svc.BlindSpots(context.Background(), domain.TrendOptions{})
	if err != nil || len(r.Spots) != 0 || len(r.All) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	svc = New(&fakeProv{stories: []domain.Story{st}}, Config{MinSkew: 0.75})
	r, _ = svc.BlindSpots(context.Background(), domain.TrendOptions{})
	if len(r.Spots) != 1 {
		t.Fatalf("expected spot: %+v", r.All[0])
	}
}

func art(id int64, src, title, sum string) domain.ArticleLite {
	return domain.ArticleLite{ID: id, SourceID: src, Title: title, Summary: sum, URL: "https://x/" + src, At: time.Unix(1700000000+id, 0)}
}

func TestFramingDistinctiveTerms(t *testing.T) {
	st := domain.Story{ID: "s", Title: "t", Sources: 4, Articles: 8,
		Coverage: []domain.SourceCoverage{cov("i1", "izquierda"), cov("i2", "centro-izquierda"), cov("d1", "derecha"), cov("d2", "derecha")}}
	arts := []domain.ArticleLite{
		art(1, "i1", "El Gobierno protege a los inquilinos con la ley de vivienda", "Una ley histórica para los inquilinos"),
		art(2, "i2", "Ley de vivienda: avance para los inquilinos", "Los inquilinos ganan derechos"),
		art(3, "d1", "La ley de vivienda ataca a los propietarios", "Los propietarios denuncian okupación"),
		art(4, "d2", "Okupación: la ley de vivienda desprotege a propietarios", "Propietarios en alerta"),
	}
	var win []domain.ArticleLite
	for i := 0; i < 50; i++ {
		win = append(win, art(int64(100+i), "x", "Partido de liga de la jornada", "El gobierno de la liga"))
	}
	svc := New(&fakeProv{stories: []domain.Story{st}, arts: arts, win: win}, Config{})
	f, err := svc.Framing(context.Background(), "s", domain.TrendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	has := func(b int, w string) bool {
		for _, x := range f.Blocks[b].Terms {
			if x.Text == w {
				return true
			}
		}
		return false
	}
	if !has(0, "inquilinos") || !has(2, "propietarios") {
		t.Fatalf("blocks: %+v", f.Blocks)
	}
	if has(0, "vivienda") || has(2, "vivienda") {
		t.Fatalf("shared word listed as distinctive: %+v", f.Blocks)
	}
	if len(f.Headlines) != 4 || f.Consensus == "" || f.Background != 50 {
		t.Fatalf("%d %q %d", len(f.Headlines), f.Consensus, f.Background)
	}
	if f.Headlines[0].Block != 0 || f.Headlines[3].Block != 2 {
		t.Fatalf("order %+v", f.Headlines)
	}
	if _, err := svc.Framing(context.Background(), "nope", domain.TrendOptions{}); err == nil {
		t.Fatal("expected not found")
	}
}
