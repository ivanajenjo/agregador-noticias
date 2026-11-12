package domain

import (
	"context"
	"time"
)

// Watch is a saved topic ("seguimiento"): a free-text query (same syntax as the
// search box) optionally restricted to outlets / leanings.
type Watch struct {
	ID         int64
	Name       string
	Query      string
	SourceIDs  []string
	Leanings   []string
	Notify     bool // send alerts when new matching articles appear
	CreatedAt  time.Time
	LastEvalAt time.Time // zero until the alert engine evaluated it once
}

// Alert kinds and channels stored in AlertRecord.
const (
	AlertStory   = "story"
	AlertWatch   = "watch"
	AlertSummary = "summary" // digest of alerts held back during quiet hours
	AlertTest    = "test"

	ChannelLog   = "log"   // no channel configured: logged only
	ChannelQuiet = "quiet" // held back (quiet hours), pending the summary
)

// AlertRecord is one row of alert_log: one delivery attempt to one channel.
// (Kind, Key, Channel) is unique, which gives dedup across runs and restarts.
type AlertRecord struct {
	ID      int64
	Kind    string
	Key     string
	Title   string
	URL     string
	SentAt  time.Time
	Channel string
	OK      bool
	Detail  string
}

// FollowStore is an optional extension of Store (implemented by internal/store).
type FollowStore interface {
	CreateWatch(ctx context.Context, w *Watch) error // sets ID and CreatedAt
	GetWatch(ctx context.Context, id int64) (*Watch, error)
	ListWatches(ctx context.Context) ([]Watch, error) // oldest first
	DeleteWatch(ctx context.Context, id int64) error
	SetWatchEvaluated(ctx context.Context, id int64, t time.Time) error
	// NewWatchHits returns the subset of articleIDs not yet marked for the watch.
	NewWatchHits(ctx context.Context, watchID int64, articleIDs []int64) ([]int64, error)
	MarkWatchHits(ctx context.Context, watchID int64, articleIDs []int64, at time.Time) error
	PruneWatchHits(ctx context.Context, before time.Time) error

	// SaveAlert inserts the record, or, if (kind,key,channel) exists, updates ok/detail only.
	SaveAlert(ctx context.Context, a AlertRecord) error
	// AlertHandled reports whether (kind,key) needs no further delivery: delivered
	// OK, held for quiet hours, logged, or failed with a first attempt before giveUpBefore.
	AlertHandled(ctx context.Context, kind, key string, giveUpBefore time.Time) (bool, error)
	ListAlerts(ctx context.Context, limit int) ([]AlertRecord, error) // newest first
	// CountAlertsSince counts distinct (kind,key) alerts actually dispatched since t
	// (quiet-held and test alerts excluded). Used for the hourly rate limit.
	CountAlertsSince(ctx context.Context, t time.Time) (int, error)
	PendingQuiet(ctx context.Context) ([]AlertRecord, error)
	ResolveQuiet(ctx context.Context, ids []int64, detail string) error
}

// AlertService is implemented by internal/alerts and consumed by internal/web.
type AlertService interface {
	// Channels lists the active notifier names (empty: alerts are only logged).
	Channels() []string
	// SendTest sends a test message through every channel and returns the records.
	SendTest(ctx context.Context) ([]AlertRecord, error)
}
