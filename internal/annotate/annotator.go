package annotate

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Annotator turns an Input into a validated Result with a Completer.
type Annotator struct {
	C       Completer
	Seed    *int // default 42
	Retries int  // extra attempts when the output is unusable (default 1)
}

// ErrUnusable means the model answered but nothing valid could be extracted
// (a per-article problem, not an outage).
var ErrUnusable = errors.New("annotate: respuesta del modelo inutilizable")

func (a *Annotator) opts() CallOptions {
	seed := 42
	if a.Seed != nil {
		seed = *a.Seed
	}
	return CallOptions{Temperature: 0, MaxTokens: 300, Seed: &seed, JSON: true}
}

// Annotate asks the model; unusable outputs are retried, transport errors are
// returned as is so the caller can tell an outage from a bad article.
func (a *Annotator) Annotate(ctx context.Context, in Input) (Result, error) {
	retries := a.Retries
	if retries <= 0 {
		retries = 1
	}
	var last Result
	var lastErr error
	for i := 0; i <= retries; i++ {
		raw, err := a.C.Complete(ctx, Messages(in), a.opts())
		if err != nil {
			return last, err
		}
		r, perr := Parse(raw)
		last = r
		if perr == nil && r.OK() {
			return r, nil
		}
		lastErr = fmt.Errorf("%w: %v", ErrUnusable, firstErr(perr, r))
	}
	return last, lastErr
}

func firstErr(perr error, r Result) error {
	if perr != nil {
		return perr
	}
	return fmt.Errorf("sin etiquetas válidas (%v)", r.Problems)
}

// IsOutage reports whether err means the model server is unavailable (as
// opposed to a bad answer for one article).
func IsOutage(err error) bool {
	if err == nil || errors.Is(err, ErrUnusable) {
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
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
