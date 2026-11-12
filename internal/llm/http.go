// Package llm implements an OpenAI-compatible HTTP client (embeddings and
// streaming chat), a health prober and the background embedding worker.
package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// normalizeBase returns the API root ending in /v1 (appended if the given
// URL has no version segment), so both "http://h:11434" and
// "http://h:1234/v1/" work.
func normalizeBase(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return ""
	}
	last := u[strings.LastIndex(u, "/")+1:]
	if len(last) >= 2 && last[0] == 'v' {
		digits := true
		for _, r := range last[1:] {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return u
		}
	}
	return u + "/v1"
}

// HTTPError is returned for non-2xx responses.
type HTTPError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Body)
}

// IsPermanent reports whether err means "this specific input is rejected by
// the server" (400/413/422) so retrying the same input is pointless.
func IsPermanent(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 400 || he.Status == 413 || he.Status == 422
	}
	return false
}

// isTransient reports whether a request may be retried.
func isTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 429 || he.Status == 408 || he.Status >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	// connection reset, EOF mid-response, refused...
	return true
}

type retryCfg struct {
	max        int // retries after the first attempt
	base, capD time.Duration
}

func (c retryCfg) delay(attempt int, err error) time.Duration {
	d := c.base << uint(attempt-1)
	if d <= 0 || d > c.capD {
		d = c.capD
	}
	var he *HTTPError
	if errors.As(err, &he) && he.RetryAfter > 0 {
		d = he.RetryAfter
		if d > c.capD {
			d = c.capD
		}
	}
	return d
}

// doRetry runs fn, retrying while shouldRetry(err) holds, with exponential
// backoff, respecting ctx.
func doRetry(ctx context.Context, c retryCfg, shouldRetry func(error) bool, fn func() error) error {
	var err error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(c.delay(attempt, err))
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		err = fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= c.max || !shouldRetry(err) {
			return err
		}
	}
}

func retryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

func newClient(dial time.Duration, headerTimeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: dial, KeepAlive: 30 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = headerTimeout
	tr.MaxIdleConnsPerHost = 2
	return &http.Client{Transport: tr}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func orDur(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
