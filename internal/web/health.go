package web

import (
	"context"
	"net/http"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// schedulerStale is how old the scheduler heartbeat may be (it ticks every 30 s).
const schedulerStale = 2 * time.Minute

type checkJSON struct {
	OK     bool    `json:"ok"`
	Detail string  `json:"detail,omitempty"`
	Ms     float64 `json:"ms"`
}

// readyz answers 200 when the DB read and write pools and the scheduler work,
// 503 otherwise. /healthz stays the trivial liveness probe.
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	checks := map[string]checkJSON{}
	timed := func(name string, timeout time.Duration, f func(ctx context.Context) (string, error)) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		t0 := time.Now()
		detail, err := f(ctx)
		c := checkJSON{OK: err == nil, Detail: detail, Ms: float64(time.Since(t0).Microseconds()) / 1000}
		if err != nil {
			c.Detail = err.Error()
		}
		checks[name] = c
	}
	if p, ok := s.d.Store.(domain.Pinger); ok {
		timed("db_read", 3*time.Second, func(ctx context.Context) (string, error) { return "", p.PingRead(ctx) })
		timed("db_write", 3*time.Second, func(ctx context.Context) (string, error) { return "", p.PingWrite(ctx) })
	} else {
		timed("db_read", 3*time.Second, func(ctx context.Context) (string, error) {
			_, err := s.d.Store.ListJobRuns(ctx, 1)
			return "", err
		})
		checks["db_write"] = checkJSON{OK: true, Detail: "no comprobado"}
	}
	if s.d.Heartbeat == nil {
		checks["scheduler"] = checkJSON{OK: true, Detail: "no configurado"}
	} else {
		last := s.d.Heartbeat.LastBeat()
		age := s.now().Sub(last)
		c := checkJSON{OK: !last.IsZero() && age <= schedulerStale}
		if last.IsZero() {
			c.Detail = "el planificador no ha arrancado"
		} else if !c.OK {
			c.Detail = "último latido hace " + age.Round(time.Second).String()
		}
		checks["scheduler"] = c
	}
	code, status := http.StatusOK, "ok"
	for _, c := range checks {
		if !c.OK {
			code, status = http.StatusServiceUnavailable, "fail"
		}
	}
	writeJSON(w, code, map[string]any{"status": status, "version": s.o.Version, "time": s.now(), "checks": checks})
}

func init() {
	registerRoutes(func(s *server, m *http.ServeMux) { m.HandleFunc("GET /readyz", s.readyz) })
}
