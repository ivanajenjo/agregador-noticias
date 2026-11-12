// Package ops holds the operational glue: the nightly maintenance job
// (article retention + SQLite upkeep).
package ops

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

var _ domain.Maintainer = (*Maintenance)(nil)

// Maintenance implements domain.Maintainer on top of a RetentionStore.
type Maintenance struct {
	Store domain.RetentionStore
	Days  int // retention.articles_days; <=0 keeps every article
	Log   *slog.Logger
	Now   func() time.Time // default time.Now
}

func (m *Maintenance) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Maintenance) age() time.Duration {
	if m.Days <= 0 {
		return 0
	}
	return time.Duration(m.Days) * 24 * time.Hour
}

// RunOnce deletes expired articles and runs the SQLite housekeeping. It is the
// function registered as the "maintenance" sched.Job.
func (m *Maintenance) RunOnce(ctx context.Context) (string, error) {
	res, err := m.Store.Maintain(ctx, m.age())
	if m.Log != nil && err == nil {
		m.Log.Info("maintenance", "deleted", res.Deleted, "reclaimed_pages", res.ReclaimedPages,
			"freelist_pages", res.FreelistPages, "db_bytes", res.DBBytes, "wal_checkpoint", res.WALCheckpoint)
	}
	if err != nil {
		return fmt.Sprintf("%d artículos borrados", res.Deleted), err
	}
	return res.String(), nil
}

// Preview is the dry run: how many articles the next run would delete.
func (m *Maintenance) Preview(ctx context.Context) (domain.MaintenancePreview, error) {
	p := domain.MaintenancePreview{ArticlesDays: m.Days, Enabled: m.Days > 0}
	if !p.Enabled {
		return p, nil
	}
	p.Cutoff = m.now().Add(-m.age())
	n, err := m.Store.CountArticlesBefore(ctx, p.Cutoff)
	p.WouldDelete = n
	return p, err
}
