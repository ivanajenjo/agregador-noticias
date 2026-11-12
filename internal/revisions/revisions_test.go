package revisions

import (
	"strings"
	"testing"
)

func TestMaterialTitles(t *testing.T) {
	cases := []struct {
		name, old, new string
		want           bool
	}{
		{"identical", "El Gobierno aprueba la ley", "El Gobierno aprueba la ley", false},
		{"case and accents", "El Gobierno aprueba la reforma fiscal", "el gobierno aprueba la reforma fiscal", false},
		{"accent only", "Sanchez anuncia medidas contra la inflacion", "Sánchez anuncia medidas contra la inflación", false},
		{"whitespace and punctuation", "Detenido el alcalde de Marbella:  la Fiscalía actúa", "Detenido el alcalde de Marbella: la Fiscalía actúa.", false},
		{"typo fix", "El Gobiero aprueba la reforma fiscal", "El Gobierno aprueba la reforma fiscal", false},
		{"outlet suffix added", "Detenido el alcalde de Marbella por corrupción", "Detenido el alcalde de Marbella por corrupción - El País", false},
		{"outlet suffix changed", "Sube el paro en octubre - RTVE.es", "Sube el paro en octubre | RTVE", false},
		{"suffix pipe", "Sube el paro en octubre | Economía", "Sube el paro en octubre", false},
		{"rewrite", "Detenido el alcalde de Marbella por corrupción", "Liberado el alcalde de Marbella tras declarar", true},
		{"number change", "Tres muertos en un accidente en la A-4", "Cinco muertos en un accidente en la A-4", true},
		{"digit typo is material", "El PIB crece un 3,1% en el tercer trimestre", "El PIB crece un 3,7% en el tercer trimestre", true},
		{"negation", "El PP apoya la ley de vivienda del Gobierno", "El PP no apoya la ley de vivienda del Gobierno", true},
		{"stopword only", "Detenido el alcalde de Marbella por corrupción", "Detenido alcalde de Marbella por corrupción", false},
		{"reordering", "Alcalde detenido en Marbella", "Marbella en detenido alcalde", false},
		{"word swap", "El Gobierno sube el salario mínimo", "El Gobierno congela el salario mínimo", true},
		{"empty new", "Algo pasó en el Congreso hoy", "", false},
		{"long suffix is content", "Rescatan a un montañero - tras pasar dos noches a la intemperie en Gredos", "Rescatan a un montañero", true},
	}
	for _, c := range cases {
		if got := Material(KindTitle, c.old, c.new); got != c.want {
			t.Errorf("%s: Material(%q, %q) = %v, want %v", c.name, c.old, c.new, got, c.want)
		}
	}
}

func TestMaterialSummaries(t *testing.T) {
	cases := []struct {
		name, old, new string
		want           bool
	}{
		{"ellipsis", "La Fiscalía investiga los contratos de emergencia firmados durante la pandemia […]", "La Fiscalía investiga los contratos de emergencia firmados durante la pandemia", false},
		{"leer mas", "El Congreso debate hoy la reforma de la ley electoral. Leer más", "El Congreso debate hoy la reforma de la ley electoral.", false},
		{"truncation", "El Congreso debate hoy la reforma de la ley electoral y los partidos", "El Congreso debate hoy la reforma de la ley electoral", false},
		{"post appeared", "Texto del resumen sobre vivienda. The post Vivienda appeared first on Medio.", "Texto del resumen sobre vivienda.", false},
		{"rewrite", "La policía ha detenido al alcalde por un presunto delito de cohecho en la contratación", "El juez deja en libertad al alcalde sin cargos tras declarar ante la fiscalía", true},
		{"one word swap", "La policía ha detenido esta mañana al alcalde de la localidad", "La policía ha liberado esta mañana al alcalde de la localidad", true},
	}
	for _, c := range cases {
		if got := Material(KindSummary, c.old, c.new); got != c.want {
			t.Errorf("%s: Material = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHashStable(t *testing.T) {
	a := Hash(KindTitle, "Detenido el alcalde - El País")
	b := Hash(KindTitle, "detenido el ALCALDE.")
	if a != b {
		t.Fatalf("hash should ignore suffix/case/punct: %s %s", a, b)
	}
	if a == Hash(KindTitle, "Liberado el alcalde") {
		t.Fatal("different text, same hash")
	}
}

func render(segs []Seg) string {
	var p []string
	for _, s := range segs {
		switch s.Op {
		case Delete:
			p = append(p, "[-"+s.Text+"-]")
		case Insert:
			p = append(p, "{+"+s.Text+"+}")
		default:
			p = append(p, s.Text)
		}
	}
	return strings.Join(p, " ")
}

func TestWordDiff(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"a b c", "a b c", "a b c"},
		{"Detenido el alcalde de Marbella", "Liberado el alcalde de Marbella", "[-Detenido-] {+Liberado+} el alcalde de Marbella"},
		{"uno dos tres", "uno tres", "uno [-dos-] tres"},
		{"uno tres", "uno dos tres", "uno {+dos+} tres"},
		{"", "nuevo texto", "{+nuevo texto+}"},
		{"viejo texto", "", "[-viejo texto-]"},
		{"el PP apoya la ley", "el PP no apoya la ley", "el PP {+no+} apoya la ley"},
		{"muere un hombre en Madrid", "fallecen dos hombres en Madrid", "[-muere un hombre-] {+fallecen dos hombres+} en Madrid"},
	}
	for _, c := range cases {
		if got := render(WordDiff(c.a, c.b)); got != c.want {
			t.Errorf("WordDiff(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestWordDiffHuge(t *testing.T) {
	a := strings.Repeat("a b c d ", 300)
	b := strings.Repeat("e f g h ", 300)
	if segs := WordDiff(a, b); len(segs) != 2 {
		t.Fatalf("fallback segs = %d", len(segs))
	}
}
