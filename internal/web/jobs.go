package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type jobResult struct {
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	Count   int       `json:"count"`
	Message string    `json:"message,omitempty"`
}

type jobTracker struct {
	mu      sync.Mutex
	running map[string]time.Time
	last    map[string]jobResult
}

func newJobTracker() *jobTracker {
	return &jobTracker{running: map[string]time.Time{}, last: map[string]jobResult{}}
}

func (t *jobTracker) start(name string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.running[name]; ok {
		return false
	}
	t.running[name] = now
	return true
}

func (t *jobTracker) finish(name string, r jobResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.running, name)
	t.last[name] = r
}

type jobSnapshot struct {
	Running map[string]time.Time
	Last    map[string]jobResult
}

func (t *jobTracker) snapshot() jobSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := jobSnapshot{Running: map[string]time.Time{}, Last: map[string]jobResult{}}
	for k, v := range t.running {
		s.Running[k] = v
	}
	for k, v := range t.last {
		s.Last[k] = v
	}
	return s
}

const jobTimeout = 2 * time.Hour

// jobFunc runs a job and returns a human message plus a count.
type jobFunc func(ctx context.Context) (msg string, n int, err error)

func (s *server) jobRunner(name string) jobFunc {
	switch name {
	case "ingest":
		if s.d.Ingestor != nil {
			return func(ctx context.Context) (string, int, error) {
				n, err := s.d.Ingestor.RunOnce(ctx)
				return fmt.Sprintf("%d nuevas", n), n, err
			}
		}
	case "screenshots":
		if s.d.Shots != nil {
			return func(ctx context.Context) (string, int, error) {
				n, err := s.d.Shots.RunOnce(ctx)
				return fmt.Sprintf("%d capturas", n), n, err
			}
		}
	case "maintenance":
		if s.d.Maintenance != nil {
			return func(ctx context.Context) (string, int, error) {
				msg, err := s.d.Maintenance.RunOnce(ctx)
				return msg, 0, err
			}
		}
	}
	return nil
}

// recordJobStart writes the job_runs row like the scheduler does. A store that
// cannot record (error or panic) must never prevent the job from running.
func (s *server) recordJobStart(name string) (id int64) {
	defer func() {
		if v := recover(); v != nil {
			id = 0
		}
	}()
	id, err := s.d.Store.StartJob(context.Background(), name)
	if err != nil {
		s.log.Warn("job row start", "job", name, "err", err)
		return 0
	}
	return id
}

func (s *server) recordJobFinish(id int64, ok bool, msg string) {
	if id == 0 {
		return
	}
	defer func() { _ = recover() }()
	// fresh context so the record survives the job's own timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.d.Store.FinishJob(ctx, id, ok, msg); err != nil {
		s.log.Warn("job row finish", "id", id, "err", err)
	}
}

// apiJob starts a job asynchronously: 202 on start, 409 when already running,
// 503 when the collaborator is not configured. Runs are recorded in job_runs.
func (s *server) apiJob(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run := s.jobRunner(name)
		if run == nil {
			s.jobReply(w, r, http.StatusServiceUnavailable, "Tarea no disponible")
			return
		}
		if !s.jobs.start(name, s.now()) {
			s.jobReply(w, r, http.StatusConflict, "Ya hay una ejecución en curso")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
			defer cancel()
			res := jobResult{At: s.now()}
			rowID := s.recordJobStart(name)
			defer func() {
				if v := recover(); v != nil {
					res.OK, res.Message = false, fmt.Sprint("panic: ", v)
					s.log.Error("job panic", "job", name, "panic", v)
				}
				s.recordJobFinish(rowID, res.OK, res.Message)
				s.jobs.finish(name, res)
			}()
			msg, n, err := run(ctx)
			res.Count = n
			res.Message = msg
			if err != nil {
				res.Message = fmt.Sprintf("%s: %v", msg, err)
				s.log.Error("job failed", "job", name, "err", err)
				return
			}
			res.OK = true
			s.log.Info("job finished", "job", name, "result", msg)
		}()
		s.jobReply(w, r, http.StatusAccepted, "Tarea iniciada")
	}
}

func (s *server) jobReply(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if r.Header.Get("HX-Request") != "" {
		cls := "ok"
		if code >= 400 {
			cls = "err"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, `<span class="notice %s" role="status">%s</span>`, cls, msg)
		return
	}
	status := "started"
	if code == http.StatusConflict {
		status = "running"
	} else if code >= 400 {
		status = "error"
	}
	writeJSON(w, code, map[string]string{"status": status, "message": msg})
}

func init() {
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("POST /api/jobs/maintenance", s.apiJob("maintenance"))
	})
}
