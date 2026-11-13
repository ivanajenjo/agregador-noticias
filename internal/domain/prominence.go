package domain

import (
	"context"
	"time"
)

// FrontItem is one headline detected on a front-page capture (live DOM,
// extracted in the same browser session as the screenshot).
//
// Prominence is a score in (0,1] normalised per capture (the top item is 1):
//
//	raw = 0.40*font + 0.30*area + 0.20*pos + 0.10*fold
//	font = (fontPx/maxFontPx)   area = sqrt(w*h / maxArea)
//	pos  = 1 - min(y, 4*viewportH)/(4*viewportH)   fold = 1 if above the first fold
//	prominence = raw / max(raw)
//
// Rank is 1..N by descending prominence (N capped at 40).
type FrontItem struct {
	ID           int64
	ScreenshotID int64
	SourceID     string
	TakenAt      time.Time
	Rank         int
	DOMOrder     int // order of appearance in the DOM, 1-based
	Title        string
	URL          string  // absolute href as found
	CanonicalURL string  // tracking-stripped canonical form (same as ingest)
	X, Y, W, H   float64 // page coordinates in CSS px
	FontPx       float64
	Prominence   float64
	AboveFold    bool

	// Filled by queries when the item's canonical URL matches an article.
	ArticleID int64 // 0 when unknown
}

// TopItem is an aggregate row of ProminenceStore.TopItems: how a canonical URL
// performed on the front pages of one outlet in a period.
type TopItem struct {
	SourceID     string
	CanonicalURL string
	Title        string // latest title seen
	Captures     int    // captures in which it appeared
	BestRank     int
	AvgProm      float64
	MaxProm      float64
	FirstAt      time.Time
	LastAt       time.Time
	ArticleID    int64
}

// ItemPoint is one capture of an item's history (ProminenceStore.ItemHistory).
type ItemPoint struct {
	SourceID     string
	ScreenshotID int64
	TakenAt      time.Time
	Rank         int
	Prominence   float64
	AboveFold    bool
	Title        string
}

// ProminenceStore persists front-page items. It is an optional extension of
// Store (type-assert it): fakes without it keep working.
type ProminenceStore interface {
	// SaveFrontItems replaces the items of a screenshot.
	SaveFrontItems(ctx context.Context, screenshotID int64, items []FrontItem) error
	// ListFrontItems returns the items (rank order) of the capture of sourceID
	// taken exactly at takenAt, or of its latest capture when takenAt is zero.
	ListFrontItems(ctx context.Context, sourceID string, takenAt time.Time) ([]FrontItem, error)
	// TopItems aggregates items per (source, canonical URL) with taken_at in
	// [from, to), best first (lowest best rank, then highest avg prominence).
	TopItems(ctx context.Context, from, to time.Time, sourceIDs []string, limit int) ([]TopItem, error)
	// ItemHistory returns every capture where the canonical URL appeared,
	// oldest first.
	ItemHistory(ctx context.Context, canonicalURL string) ([]ItemPoint, error)
}
