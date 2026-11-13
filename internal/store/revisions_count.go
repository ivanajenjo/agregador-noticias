package store

import (
	"context"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// CountHeadlineChanges implements domain.RevisionCounter: headline (title)
// revisions recorded in [from, to).
func (s *SQLite) CountHeadlineChanges(ctx context.Context, from, to time.Time) (int, error) {
	n, err := s.CountRevisions(ctx, domain.RevisionFilter{From: from, To: to.Add(-time.Second), Kind: domain.RevisionTitle})
	return int(n), err
}

var _ domain.RevisionCounter = (*SQLite)(nil)
