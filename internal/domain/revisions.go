package domain

import (
	"context"
	"time"
)

// Revision kinds.
const (
	RevisionTitle   = "titular"
	RevisionSummary = "resumen"
)

// MaxRevisionsPerArticle caps the history kept per article (anti-churn).
const MaxRevisionsPerArticle = 20

// Revision is one detected edit of an already stored article ("stealth edit").
type Revision struct {
	ID        int64
	ArticleID int64
	Kind      string // RevisionTitle | RevisionSummary
	OldText   string
	NewText   string
	ChangedAt time.Time // when we noticed the change
	SeenAt    time.Time // when we first saw the article (delay = ChangedAt-SeenAt)
}

// RevisionFilter restricts revision listings. Zero values mean "no restriction".
// From/To apply to Revision.ChangedAt (inclusive).
type RevisionFilter struct {
	From, To  time.Time
	SourceIDs []string
	Leanings  []string
	Kind      string // "" = all kinds
	Limit     int
	Offset    int
}

// RevisionEntry is a revision plus the minimal article context to display it.
type RevisionEntry struct {
	Revision
	SourceID     string
	ArticleTitle string // current stored title
	ArticleURL   string
}

// SourceCount is a per-outlet counter.
type SourceCount struct {
	SourceID string
	Count    int64
}

// RevisionStore is an optional extension of Store (implemented by
// internal/store). Consumers use it through a type assertion so that stores
// (or fakes) without it keep working.
type RevisionStore interface {
	// RecordRevision compares the feed's current title/summary with the stored
	// article identified by its canonical URL. If they differ materially it
	// records one revision per changed kind and updates the stored article to
	// the latest text, all in one transaction. changed is false (and rev nil)
	// when the article is unknown, nothing changed materially, the change is
	// feed jitter (A->B->A) or the article reached MaxRevisionsPerArticle.
	// rev is the title revision when both kinds changed.
	RecordRevision(ctx context.Context, canonical, newTitle, newSummary string, now time.Time) (changed bool, rev *Revision, err error)
	// ListRevisions returns an article's revisions, oldest first.
	ListRevisions(ctx context.Context, articleID int64) ([]Revision, error)
	// RecentRevisions returns revisions matching f, newest first.
	RecentRevisions(ctx context.Context, f RevisionFilter) ([]RevisionEntry, error)
	CountRevisions(ctx context.Context, f RevisionFilter) (int64, error)
	// RevisionsBySource counts revisions per outlet, most edited first.
	RevisionsBySource(ctx context.Context, f RevisionFilter) ([]SourceCount, error)
}
