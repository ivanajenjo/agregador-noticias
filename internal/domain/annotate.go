package domain

import (
	"context"
	"time"
)

// Annotation kinds written by internal/annotate (one row per kind, model).
const (
	AnnoTone    = "tone"
	AnnoIntent  = "intent"
	AnnoFrame   = "frame"
	AnnoOpinion = "opinion" // label "sí" / "no"
)

// Closed vocabularies shared by the annotator and the UI.
var (
	AnnoTones   = []string{"neutral", "alarmista", "laudatorio", "crítico", "sensacionalista", "humorístico"}
	AnnoIntents = []string{"informar", "opinar", "persuadir", "entretener", "promocionar"}
	// AnnoFrames: generic news frames of Semetko & Valkenburg (2000).
	AnnoFrames = []string{"conflicto", "interés humano", "económico", "moralidad", "responsabilidad", "otros"}
)

// AnnotationQuery restricts the aggregate/list queries of AnnotationReader.
// Annotations with Score < MinScore are ignored (the model's own confidence).
type AnnotationQuery struct {
	Kind      string
	Model     string
	Label     string   // only for ListAnnotated; empty = any
	SourceIDs []string // empty = all
	From, To  time.Time
	MinScore  float64
	Limit     int
}

// AnnotationCount is one cell of a per-outlet distribution.
type AnnotationCount struct {
	SourceID string
	Label    string
	N        int64
}

// AnnotatedArticle is an article plus the confidence of the matching label.
type AnnotatedArticle struct {
	Article Article
	Label   string
	Score   float64
}

// AnnotationModelInfo summarises the rows one model has written.
type AnnotationModelInfo struct {
	Model  string
	Count  int64
	Newest time.Time
}

// AnnotationReader is what the web layer needs: the base store plus
// aggregates (implemented by internal/store).
type AnnotationReader interface {
	AnnotationStore
	// AnnotationModels lists the models with at least one row of kind, most rows first.
	AnnotationModels(ctx context.Context, kind string) ([]AnnotationModelInfo, error)
	// AnnotationDistribution counts annotations per (source, label).
	AnnotationDistribution(ctx context.Context, q AnnotationQuery) ([]AnnotationCount, error)
	// ListAnnotated lists the newest articles carrying q.Label (q.Limit, default 50).
	ListAnnotated(ctx context.Context, q AnnotationQuery) ([]AnnotatedArticle, error)
}
