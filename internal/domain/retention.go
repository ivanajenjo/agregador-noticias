package domain

import (
	"context"
	"fmt"
	"time"
)

// MaintainResult summarises one maintenance run (retention + SQLite upkeep).
type MaintainResult struct {
	Deleted        int // articles removed by retention
	Cutoff         time.Time
	WALCheckpoint  bool  // wal_checkpoint(TRUNCATE) completed without being blocked
	FreelistPages  int64 // free pages left in the DB file after the run
	ReclaimedPages int64 // pages returned to the OS by incremental_vacuum
	AutoVacuum     int   // 0 none, 1 full, 2 incremental
	DBBytes        int64 // size of the DB file after the run
}

func (r MaintainResult) String() string {
	s := fmt.Sprintf("%d artículos borrados", r.Deleted)
	if r.ReclaimedPages > 0 {
		s += fmt.Sprintf(", %d páginas liberadas", r.ReclaimedPages)
	}
	if !r.WALCheckpoint {
		s += ", checkpoint WAL pendiente"
	}
	return s
}

// RetentionStore is implemented by internal/store: age-based deletion of
// articles (cascading to chunks, embeddings, annotations and the FTS index)
// plus the SQLite housekeeping that keeps the file small on an SD card.
type RetentionStore interface {
	// CountArticlesBefore is the dry run: how many articles first seen before t.
	CountArticlesBefore(ctx context.Context, t time.Time) (int64, error)
	// DeleteArticlesBefore deletes articles first seen before t in transactions
	// of at most batch rows (default 200) and returns how many were removed.
	DeleteArticlesBefore(ctx context.Context, t time.Time, batch int) (int, error)
	// Maintain deletes articles older than olderThan (0 = keep everything) and
	// then checkpoints the WAL, runs PRAGMA optimize and, when the database was
	// created with auto_vacuum=INCREMENTAL, returns free pages to the OS.
	Maintain(ctx context.Context, olderThan time.Duration) (MaintainResult, error)
}

// MaintenancePreview is the dry-run answer shown by the UI/API.
type MaintenancePreview struct {
	ArticlesDays int       `json:"articles_days"`
	Enabled      bool      `json:"enabled"`
	Cutoff       time.Time `json:"cutoff,omitzero"`
	WouldDelete  int64     `json:"would_delete"`
}

// Maintainer is the job entry point used by the scheduler and by the web
// (POST /api/jobs/maintenance, GET /api/maintenance).
type Maintainer interface {
	RunOnce(ctx context.Context) (string, error)
	Preview(ctx context.Context) (MaintenancePreview, error)
}

// Heartbeat reports the last time the scheduler loop proved to be alive.
type Heartbeat interface {
	LastBeat() time.Time
}

// Pinger lets /readyz probe the two SQLite pools (implemented by internal/store).
type Pinger interface {
	PingRead(ctx context.Context) error
	// PingWrite acquires and releases the write lock (BEGIN IMMEDIATE; ROLLBACK).
	PingWrite(ctx context.Context) error
}
