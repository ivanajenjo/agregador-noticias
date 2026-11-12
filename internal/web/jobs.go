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

// apiJob starts a job asynchronously: 202 on start, 409 when already running,
// 503 when the collaborator is not configured.
func (s *server) apiJob(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var run func(context.Context) (int, error)
		switch name {
		case "ingest":
			if s.d.Ingestor != nil {
				run = s.d.Ingestor.RunOnce
			}
		case "screenshots":
			if s.d.Shots != nil {
				run = s.d.Shots.RunOnce
			}
		}
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
			defer func() {
				if v := recover(); v != nil {
					res.OK, res.Message = false, fmt.Sprint("panic: ", v)
					s.log.Error("job panic", "job", name, "panic", v)
				}
				s.jobs.finish(name, res)
			}()
			n, err := run(ctx)
			res.Count = n
			if err != nil {
				res.Message = err.Error()
				s.log.Error("job failed", "job", name, "err", err)
				return
			}
			res.OK = true
			s.log.Info("job finished", "job", name, "count", n)
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
