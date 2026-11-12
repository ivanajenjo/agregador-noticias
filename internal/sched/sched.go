// Package sched runs the periodic jobs with a cron scheduler in the configured timezone.
package sched

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Job is a named unit of work. Overlapping runs of the same job are skipped.
type Job struct {
	Name string
	Spec string // cron spec; empty => not scheduled
	Run  func(ctx context.Context) (string, error)
}

type Scheduler struct {
	c    *cron.Cron
	st   domain.Store
	log  *slog.Logger
	ctx  context.Context
	mu   sync.Mutex
	busy map[string]bool
	wg   sync.WaitGroup
}

func New(ctx context.Context, st domain.Store, loc *time.Location, log *slog.Logger) *Scheduler {
	return &Scheduler{
		c:    cron.New(cron.WithLocation(loc)),
		st:   st,
		log:  log,
		ctx:  ctx,
		busy: map[string]bool{},
	}
}

// Add registers a job on its cron spec.
func (s *Scheduler) Add(j Job) error {
	if j.Spec == "" {
		return nil
	}
	_, err := s.c.AddFunc(j.Spec, func() { s.Trigger(j) })
	if err != nil {
		return fmt.Errorf("job %s: %w", j.Name, err)
	}
	return nil
}

// Trigger runs the job now in the background; returns false if it is already running.
func (s *Scheduler) Trigger(j Job) bool {
	s.mu.Lock()
	if s.busy[j.Name] {
		s.mu.Unlock()
		s.log.Warn("job already running, skipped", "job", j.Name)
		return false
	}
	s.busy[j.Name] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.busy, j.Name)
			s.mu.Unlock()
		}()
		s.run(j)
	}()
	return true
}

func (s *Scheduler) run(j Job) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("job panic", "job", j.Name, "panic", r)
		}
	}()
	id, _ := s.st.StartJob(s.ctx, j.Name)
	msg, err := j.Run(s.ctx)
	ok := err == nil
	if err != nil {
		msg = fmt.Sprintf("%s: %v", msg, err)
		s.log.Error("job failed", "job", j.Name, "err", err)
	} else {
		s.log.Info("job done", "job", j.Name, "result", msg)
	}
	// use a fresh context so the record survives shutdown
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.st.FinishJob(fctx, id, ok, msg)
}

func (s *Scheduler) Start() { s.c.Start() }

// Stop stops scheduling and waits for running jobs (ctx cancellation is the caller's job).
func (s *Scheduler) Stop() {
	<-s.c.Stop().Done()
	s.wg.Wait()
}
