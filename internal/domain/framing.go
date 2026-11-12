package domain

import "context"

// StoryDetailProvider is an optional extension of TrendProvider (implemented
// by internal/trends.Engine) giving access to the raw article texts behind a
// story. Callers must pass the same TrendOptions (window, limit, sources,
// Now) used to list the stories, so the result comes from the same cache.
type StoryDetailProvider interface {
	// StoryArticles returns the articles of the story, oldest first. It
	// returns an error wrapping sql.ErrNoRows-like "not found" text when the
	// story is not in the window.
	StoryArticles(ctx context.Context, storyID string, o TrendOptions) ([]ArticleLite, error)
	// WindowArticles returns every analysed article of the window (the
	// background corpus), oldest first. The slice is read-only.
	WindowArticles(ctx context.Context, o TrendOptions) ([]ArticleLite, error)
}
