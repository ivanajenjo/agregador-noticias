package domain

import (
	"context"
	"time"
)

// Annotation is a derived label attached to an article by a (small) model,
// e.g. kind "bias" / "intent" / "tone". Unique per (ArticleID, Kind, Model).
type Annotation struct {
	ArticleID int64
	Kind      string
	Model     string
	Label     string
	Score     float64
	Data      string // free-form JSON text
	CreatedAt time.Time
}

// AnnotationStore is an optional extension of Store (implemented by
// internal/store). It is a separate interface so Store stays unchanged.
type AnnotationStore interface {
	// SaveAnnotation inserts or replaces the annotation for (article, kind, model).
	SaveAnnotation(ctx context.Context, a Annotation) error
	ListAnnotations(ctx context.Context, articleID int64) ([]Annotation, error)
	// ArticlesNeedingAnnotation returns articles (newest first) with no
	// annotation of this kind by this model.
	ArticlesNeedingAnnotation(ctx context.Context, kind, model string, limit int) ([]Article, error)
	// CountAnnotationsByLabel counts annotations per label for articles matching f.
	CountAnnotationsByLabel(ctx context.Context, kind, model string, f Filter) (map[string]int64, error)
}
