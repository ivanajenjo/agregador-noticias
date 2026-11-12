package trends

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.StoryDetailProvider = (*Engine)(nil)

// ErrStoryNotFound is returned by StoryArticles for an unknown story id.
var ErrStoryNotFound = errors.New("story not found")

// StoryArticles implements domain.StoryDetailProvider. Pass the same options
// used for Stories so the (cached) clustering is reused.
func (e *Engine) StoryArticles(ctx context.Context, storyID string, o domain.TrendOptions) ([]domain.ArticleLite, error) {
	r, err := e.storyResult(ctx, normalize(o, true))
	if err != nil {
		return nil, err
	}
	a, ok := r.arts[storyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrStoryNotFound, storyID)
	}
	return append([]domain.ArticleLite(nil), a...), nil
}

// WindowArticles implements domain.StoryDetailProvider.
func (e *Engine) WindowArticles(ctx context.Context, o domain.TrendOptions) ([]domain.ArticleLite, error) {
	r, err := e.storyResult(ctx, normalize(o, true))
	if err != nil {
		return nil, err
	}
	return r.window, nil
}
