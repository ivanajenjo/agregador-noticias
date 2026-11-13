package demo

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// tone weights per outlet in the order of domain.AnnoTones:
// neutral, alarmista, laudatorio, crítico, sensacionalista, humorístico.
var toneProfile = map[string][]float64{
	"europapress": {.86, .03, .02, .07, .01, .01}, "20minutos": {.62, .10, .04, .08, .12, .04}, "elpais": {.60, .06, .06, .24, .02, .02},
	"eldiario": {.40, .08, .07, .40, .03, .02}, "infolibre": {.38, .08, .06, .42, .04, .02}, "elplural": {.30, .10, .14, .34, .08, .04},
	"lasexta": {.50, .10, .06, .16, .14, .04}, "elperiodico": {.60, .07, .05, .20, .06, .02}, "lavanguardia": {.72, .05, .06, .12, .03, .02},
	"elconfidencial": {.62, .06, .04, .24, .03, .01}, "newtral": {.80, .02, .0, .16, .0, .02}, "marca": {.55, .10, .10, .05, .14, .06},
	"elmundo": {.50, .12, .04, .28, .04, .02}, "elespanol": {.48, .14, .04, .26, .06, .02}, "theobjective": {.42, .12, .05, .34, .05, .02},
	"larazon": {.52, .12, .06, .24, .04, .02}, "cope": {.56, .10, .08, .20, .04, .02}, "expansion": {.80, .06, .04, .08, .01, .01},
	"abc": {.40, .18, .04, .32, .04, .02}, "okdiario": {.22, .26, .02, .28, .20, .02}, "libertaddigital": {.28, .14, .04, .40, .10, .04},
	"eldebate": {.30, .20, .08, .32, .08, .02},
}

// frame weights per bucket in the order of domain.AnnoFrames:
// conflicto, interés humano, económico, moralidad, responsabilidad, otros.
var frameProfile = map[string][]float64{
	"politica": {.45, .05, .10, .05, .25, .10}, "economia": {.10, .02, .70, .0, .08, .10}, "sociedad": {.05, .50, .10, .05, .20, .10},
	"internacional": {.40, .10, .20, .02, .08, .20}, "deportes": {.30, .20, .02, .0, .03, .45}, "bulos": {.05, .05, .0, .20, .20, .50},
	"cultura": {.02, .40, .08, .0, .0, .50}, "opinion": {.30, .10, .15, .15, .25, .05}, "story": {.30, .20, .15, .05, .20, .10},
}

var rationales = map[string][]string{
	"neutral":         {"Redacción descriptiva, sin adjetivos valorativos.", "Presenta los hechos y las fuentes sin carga emocional.", "Titular informativo con datos verificables."},
	"alarmista":       {"Enfatiza el riesgo y el caos con lenguaje de urgencia.", "Usa términos de alarma («caos», «desplome») sin matices.", "Destaca el peor escenario posible."},
	"laudatorio":      {"Presenta la medida con un enfoque claramente favorable.", "Resalta beneficios y omite objeciones.", "Tono de aprobación explícita."},
	"crítico":         {"Cuestiona la actuación de las instituciones citadas.", "Atribuye responsabilidades y enfatiza fallos.", "Encuadre de reproche hacia los responsables."},
	"sensacionalista": {"Recurre a gancho emocional y exageración en el titular.", "Prioriza el efecto llamativo sobre la información.", "Lenguaje hiperbólico para captar atención."},
	"humorístico":     {"Tono irónico o jocoso en el titular.", "Juego de palabras que resta seriedad al asunto.", "Enfoque ligero y desenfadado."},
}

func (g *gen) annotate() error {
	for i, a := range g.arts {
		p := g.pends[i]
		age := g.now.Sub(a.FirstSeenAt)
		prob := .25
		if age <= 72*time.Hour {
			prob = .70
		}
		if g.r.Float64() >= prob {
			continue
		}
		tw, ok := toneProfile[a.SourceID]
		if !ok {
			tw = toneProfile["europapress"]
		}
		tone := domain.AnnoTones[g.pickWeighted(tw)]
		frame := domain.AnnoFrames[g.pickWeighted(frameProfile[p.bucket])]
		if p.st != nil {
			if f, ok := p.st.Frame[p.leaning]; ok && g.r.Float64() < .75 {
				frame = f
			}
			if t, ok := p.st.Tone[p.leaning]; ok && g.r.Float64() < .6 {
				tone = t
			}
		}
		intent := "informar"
		x := g.r.Float64()
		switch {
		case p.opinion:
			intent = "opinar"
			if g.r.Float64() < .25 {
				intent = "persuadir"
			}
		case tone == "humorístico" || (a.SourceID == "marca" && x < .12):
			intent = "entretener"
		case tone == "sensacionalista" && x < .25:
			intent = "persuadir"
		case a.SourceID == "20minutos" && x < .05, a.SourceID == "marca" && x < .04:
			intent = "promocionar"
		case (tone == "crítico" || tone == "laudatorio") && x < .12:
			intent = "opinar"
		}
		op := "no"
		if intent == "opinar" || intent == "persuadir" {
			op = "sí"
		}
		conf := .55 + .42*math.Pow(g.r.Float64(), .7)
		conf = math.Round(conf*100) / 100
		rs := rationales[tone]
		isOp := op == "sí"
		data, _ := json.Marshal(map[string]any{"rationale": rs[g.r.IntN(len(rs))], "is_opinion": isOp, "prompt": 1})
		at := a.FirstSeenAt.Add(time.Duration(3+g.r.IntN(50)) * time.Minute)
		if at.After(g.now) {
			at = g.now
		}
		for _, kv := range [][2]string{{domain.AnnoTone, tone}, {domain.AnnoIntent, intent}, {domain.AnnoFrame, frame}, {domain.AnnoOpinion, op}} {
			if err := g.st.SaveAnnotation(g.ctx, domain.Annotation{ArticleID: a.ID, Kind: kv[0], Model: AnnotationModel, Label: kv[1], Score: conf, Data: string(data), CreatedAt: at}); err != nil {
				return err
			}
			g.sum.Annotations++
		}
		fmt.Fprintf(g.h, "N|%d|%s|%s|%s\n", a.ID, tone, intent, frame)
	}
	g.log("annotations: %d rows", g.sum.Annotations)
	return nil
}
