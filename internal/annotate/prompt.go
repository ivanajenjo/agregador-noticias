package annotate

import (
	"fmt"
	"strings"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Input is what the model sees about an article.
type Input struct {
	Title, Summary, Content string
}

// InputFor builds the model input from an article: title, summary and the
// first 600 characters of the body.
func InputFor(a domain.Article) Input {
	return Input{Title: a.Title, Summary: a.Summary, Content: a.Content}
}

const systemPrompt = `Eres un analista de medios. Clasificas noticias en español a partir del titular y un extracto.
Responde SOLO con un objeto JSON, sin texto adicional ni bloques de código, con estas claves:
- "tone": uno de neutral, alarmista, laudatorio, crítico, sensacionalista, humorístico
- "intent": uno de informar, opinar, persuadir, entretener, promocionar
- "frame": encuadre dominante, uno de conflicto, interés humano, económico, moralidad, responsabilidad, otros
- "confidence": número entre 0 y 1 (tu seguridad; usa valores bajos si el texto es ambiguo)
- "rationale": justificación de como máximo 120 caracteres
- "is_opinion": true si es un artículo de opinión/editorial/columna, false si no

Criterios: "neutral" es un relato factual sin adjetivación; no inventes tono si el texto es seco.
Un titular informativo normal es neutral/informar. El encuadre describe el ángulo (Semetko y Valkenburg):
conflicto (enfrentamiento entre partes), interés humano (historia personal o emotiva), económico (costes, cifras, mercado),
moralidad (valores, ética), responsabilidad (quién tiene la culpa o debe resolverlo), otros.
Juzgas el texto, no si el hecho es cierto ni tu opinión sobre el tema.

Ejemplo 1
Titular: El Banco de España mantiene su previsión de crecimiento del PIB en el 2,4 % para este año
Resumen: El supervisor revisa levemente la inflación subyacente a la baja.
{"tone":"neutral","intent":"informar","frame":"económico","confidence":0.9,"rationale":"Dato económico expuesto sin adjetivos.","is_opinion":false}

Ejemplo 2
Titular: ¡Caos total! Miles de viajeros atrapados y nadie da la cara
Resumen: Una avería deja sin servicio la línea durante horas.
{"tone":"sensacionalista","intent":"informar","frame":"responsabilidad","confidence":0.75,"rationale":"Lenguaje exagerado y reproche a los responsables.","is_opinion":false}

Ejemplo 3
Titular: Tribuna | El Gobierno vuelve a mirar hacia otro lado
Resumen: Una columna sostiene que la inacción en vivienda tiene un coste político.
{"tone":"crítico","intent":"opinar","frame":"responsabilidad","confidence":0.85,"rationale":"Columna con juicio explícito contra el Gobierno.","is_opinion":true}`

// Messages builds the chat request for in.
func Messages(in Input) []Message {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Titular: %s\n", oneLine(in.Title))
	if s := strings.TrimSpace(in.Summary); s != "" {
		fmt.Fprintf(&sb, "Resumen: %s\n", truncRunes(oneLine(s), 700))
	}
	if c := strings.TrimSpace(in.Content); c != "" {
		fmt.Fprintf(&sb, "Inicio del texto: %s\n", truncRunes(oneLine(c), 600))
	}
	sb.WriteString("\nJSON:")
	return []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: sb.String()}}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
