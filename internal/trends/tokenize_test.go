package trends

import (
	"sort"
	"strings"
	"testing"
)

func testStop() map[string]struct{} { return buildStopSet(nil) }

// keysOf runs the extractor on one title (+summary) and returns "kind:key" strings.
func keysOf(title, summary string) []string {
	x := newExtractor(testStop())
	var out []string
	x.extract(title, summary, func(c *cand) {
		k := [...]string{"w", "p", "e"}[c.kind]
		if c.pend {
			out = append(out, "pend:"+string(c.key)+"/"+string(c.restKey))
			return
		}
		out = append(out, k+":"+string(c.key))
	})
	sort.Strings(out)
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestFoldAndStem(t *testing.T) {
	z := &tokenizer{stop: testStop()}
	cases := []struct{ in, want string }{
		{"Incendios", "incendio"},
		{"incendio", "incendio"},
		{"Gobiernos", "gobierno"},
		{"gobierno", "gobierno"},
		{"crisis", "crisis"},
		{"Tesis", "tesis"},
		{"análisis", "analisis"},
		{"lunes", "lunes"},
		{"virus", "virus"},
		{"elecciones", "eleccion"},
		{"Elecciones", "eleccion"},
		{"ciudades", "ciudad"},
		{"trabajadores", "trabajador"},
		{"millones", "millon"},
		{"IVA", "iva"},
		{"5G", "5g"},
		{"EEUU", "eeuu"},
		{"Ley de Vivienda", "ley de vivienda"},
		{"Pedro Sánchez", "pedro sanchez"},
		{"Estados Unidos", "estado unido"},
		{"Año", "año"},
		{"Canarias", "canaria"},
	}
	for _, c := range cases {
		if got := z.canonKey(c.in); got != c.want {
			t.Errorf("canonKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPluralMerge(t *testing.T) {
	a := keysOf("Un incendio forestal arrasa 300 hectáreas", "")
	b := keysOf("Los incendios forestales obligan a evacuar", "")
	if !has(a, "w:incendio") || !has(b, "w:incendio") {
		t.Fatalf("incendio/incendios must merge: %v %v", a, b)
	}
	c := keysOf("La crisis de la vivienda y la tesis de Gobierno", "")
	for _, k := range []string{"w:crisis", "w:tesis", "w:vivienda", "w:gobierno"} {
		if !has(c, k) {
			t.Errorf("missing %s in %v", k, c)
		}
	}
}

func TestStopwordsNoiseNumbers(t *testing.T) {
	got := keysOf("Última hora: Sánchez dice que será el lunes 12 de marzo de 2025 según el Gobierno", "")
	for _, bad := range []string{"w:dice", "w:sera", "w:lunes", "w:marzo", "w:2025", "w:12", "w:segun", "w:ultima", "w:hora", "w:que"} {
		if has(got, bad) {
			t.Errorf("%s should be dropped: %v", bad, got)
		}
	}
	if !has(got, "w:sanchez") || !has(got, "w:gobierno") {
		t.Errorf("content words lost: %v", got)
	}
	// outlet/section boilerplate
	got = keysOf("VÍDEO | Opinión: así el análisis en directo", "")
	for _, k := range got {
		if strings.HasPrefix(k, "w:") {
			t.Errorf("only noise expected, got %v", got)
		}
	}
}

func TestExtraStopwords(t *testing.T) {
	x := newExtractor(buildStopSet([]string{"Cadena SER", "Mercadona"}))
	var out []string
	x.extract("Mercadona sube los precios", "", func(c *cand) { out = append(out, string(c.key)) })
	for _, k := range out {
		if k == "mercadona" {
			t.Fatal("extra stopword not applied")
		}
	}
}

func TestAcronyms(t *testing.T) {
	got := keysOf("El IVA de los alimentos y el 5G llegan a la UE tras el pacto de ETA", "")
	for _, k := range []string{"w:iva", "w:5g", "w:ue", "w:eta"} {
		if !has(got, k) {
			t.Errorf("missing %s in %v", k, got)
		}
	}
	got = keysOf("PP y PSOE pactan", "")
	if !has(got, "w:pp") || !has(got, "w:psoe") {
		t.Errorf("%v", got)
	}
	// ALL CAPS headline: acronym logic must not fire for ordinary words
	got = keysOf("EL GOBIERNO APRUEBA LA LEY DE VIVIENDA", "")
	if has(got, "w:el") || has(got, "w:la") {
		t.Errorf("%v", got)
	}
	for _, k := range got {
		if strings.HasPrefix(k, "e:") {
			t.Errorf("no entities in all-caps titles: %v", got)
		}
	}
}

func TestEntities(t *testing.T) {
	cases := []struct {
		title string
		want  []string
		not   []string
	}{
		{"Feijóo critica a Pedro Sánchez por la Ley de Vivienda", []string{"e:pedro sanchez", "e:ley de vivienda", "w:feijoo"}, []string{"e:feijoo"}},
		{"El Tribunal Supremo anula la sentencia del Banco de España", []string{"e:tribunal supremo", "e:banco de españa"}, nil},
		{"Ayuso defiende a Isabel Díaz Ayuso", []string{"e:isabel diaz ayuso"}, nil},
		{"Sánchez, Feijóo y Abascal chocan en el Congreso", nil, []string{"e:sanchez feijoo", "e:sanchez feijoo abascal"}}, // commas/'y' break runs
		{"Acuerdo entre Reino Unido y Estados Unidos", []string{"e:reino unido", "e:estado unido"}, nil},
		{"Detenido el alcalde de Marbella", nil, []string{"e:detenido"}},                              // single sentence-start capital is not an entity
		{"El Gobierno Aprueba La Ley De Vivienda En El Consejo", nil, []string{"e:gobierno aprueba"}}, // Title Case: no entities
	}
	for _, c := range cases {
		got := keysOf(c.title, "")
		for _, w := range c.want {
			if !has(got, w) {
				t.Errorf("%q: missing %s in %v", c.title, w, got)
			}
		}
		for _, n := range c.not {
			if has(got, n) {
				t.Errorf("%q: unexpected %s in %v", c.title, n, got)
			}
		}
	}
}

func TestBigramsTitleOnly(t *testing.T) {
	got := keysOf("Fuerte temporal azota el litoral gallego", "Fuerte temporal en la costa norte")
	if !has(got, "p:fuerte temporal") || !has(got, "p:temporal azota") || !has(got, "p:fuerte temporal azota") {
		t.Errorf("bigram/trigram missing: %v", got)
	}
	// no n-grams from summary: "costa norte" must not appear
	if has(got, "p:costa norte") {
		t.Errorf("summary produced a bigram: %v", got)
	}
	// stopword breaks n-grams
	if has(got, "p:azota litoral") {
		t.Errorf("n-gram across a stopword: %v", got)
	}
}

func TestSentenceStartPending(t *testing.T) {
	// "Muere Gabriel García Márquez": sentence-initial verb must not be glued
	// to the name; resolved later from corpus statistics.
	got := keysOf("Muere Gabriel García Márquez a los 87 años", "")
	if !has(got, "pend:muere gabriel garcia marquez/gabriel garcia marquez") {
		t.Errorf("%v", got)
	}
}

func TestDisplayForms(t *testing.T) {
	c := &corpus{stop: testStop()}
	_ = c
	x := newExtractor(testStop())
	var disp []string
	x.extract("Pedro Sánchez visita Valencia", "El presidente Pedro Sánchez habló en la DANA", func(cd *cand) {
		if string(cd.key) == "pedro sanchez" {
			disp = append(disp, cd.disp)
		}
	})
	if len(disp) == 0 || disp[0] != "Pedro Sánchez" {
		t.Fatalf("display %v", disp)
	}
}
