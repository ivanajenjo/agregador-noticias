// Package annotate labels every article with tone, intent and framing using a
// SMALL chat model reached through an OpenAI-compatible endpoint (typically
// Ollama / LM Studio on a Mac or PC; the Raspberry Pi only sends text).
//
// The labels are probabilistic estimates of the model, never facts: they are
// stored with the model name and the model's own confidence, and the UI shows
// them as "estimación automática".
//
// Throughput: a 3-8B model answers in ~2-4 s per article on a laptop, so
// roughly 1000-1500 articles/hour if the worker ran flat out. The worker is
// deliberately throttled (BatchSize articles per Interval, strictly
// sequential requests) so a CPU-bound machine is never saturated.
//
// Calibration: see Evaluate and docs/anotaciones-gold.example.json.
package annotate

import "github.com/ivanajenjo/agregador-noticias/internal/domain"

// PromptVersion is stored in the annotation data so rows made with a
// different prompt can be told apart.
const PromptVersion = 1

// Closed vocabularies. The model must answer with one of these (accents and
// case are tolerated when parsing, anything else is discarded).
var (
	Tones   = domain.AnnoTones
	Intents = domain.AnnoIntents
	Frames  = domain.AnnoFrames
)
