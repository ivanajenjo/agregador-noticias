package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Prober checks reachability of an OpenAI-compatible server (GET /models)
// and caches the result for a TTL so status pages do not hammer it.
type Prober struct {
	base, key, model string
	ttl, timeout     time.Duration
	client           *http.Client
	now              func() time.Time

	mu   sync.Mutex
	last domain.Health
}

// NewProber creates a prober. ttl defaults to 15s, timeout to 3s.
func NewProber(baseURL, apiKey, model string, ttl, timeout time.Duration) *Prober {
	return &Prober{
		base: normalizeBase(baseURL), key: apiKey, model: model,
		ttl: orDur(ttl, 15*time.Second), timeout: orDur(timeout, 3*time.Second),
		client: newClient(2*time.Second, 3*time.Second), now: time.Now,
	}
}

// Check returns the (possibly cached) health of the server.
func (p *Prober) Check(ctx context.Context) domain.Health {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.CheckedAt.IsZero() && p.now().Sub(p.last.CheckedAt) < p.ttl {
		return p.last
	}
	h := p.probe(ctx)
	if ctx.Err() == nil { // do not cache results of a cancelled request
		p.last = h
	}
	return h
}

func (p *Prober) probe(ctx context.Context) domain.Health {
	h := domain.Health{CheckedAt: p.now()}
	if p.base == "" {
		h.Detail = "no configurado"
		return h
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/models", nil)
	if err != nil {
		h.Detail = err.Error()
		return h
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		h.Detail = "sin conexión: " + err.Error()
		return h
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode/100 == 2:
		h.OK = true
		h.Detail = "ok"
		var ml struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &ml) == nil && len(ml.Data) > 0 && p.model != "" {
			found := false
			for _, m := range ml.Data {
				if m.ID == p.model || strings.TrimSuffix(m.ID, ":latest") == strings.TrimSuffix(p.model, ":latest") {
					found = true
					break
				}
			}
			if !found {
				h.Detail = fmt.Sprintf("servidor accesible, pero el modelo %q no figura en /models", p.model)
			}
		}
	case resp.StatusCode == 404 || resp.StatusCode == 405:
		h.OK = true
		h.Detail = "servidor accesible (sin /models)"
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		h.Detail = fmt.Sprintf("acceso denegado (HTTP %d): revisa la API key", resp.StatusCode)
	default:
		h.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return h
}

// Status combines the embed and chat probers; implements domain.StatusProvider.
type Status struct {
	embed, chat *Prober
}

var _ domain.StatusProvider = (*Status)(nil)

// NewStatus builds a Status from the two clients (either may be nil).
func NewStatus(emb *OpenAIEmbedder, chat *OpenAIChat, ttl, timeout time.Duration) *Status {
	s := &Status{}
	if emb != nil {
		s.embed = NewProber(emb.cfg.BaseURL, emb.cfg.APIKey, emb.cfg.Model, ttl, timeout)
	}
	if chat != nil {
		s.chat = NewProber(chat.cfg.BaseURL, chat.cfg.APIKey, chat.cfg.Model, ttl, timeout)
	}
	return s
}

// LLMHealth implements domain.StatusProvider. Both probes run in parallel.
func (s *Status) LLMHealth(ctx context.Context) (chat domain.Health, embed domain.Health) {
	var wg sync.WaitGroup
	run := func(p *Prober, out *domain.Health) {
		defer wg.Done()
		if p == nil {
			*out = domain.Health{Detail: "no configurado", CheckedAt: time.Now()}
			return
		}
		*out = p.Check(ctx)
	}
	wg.Add(2)
	go run(s.chat, &chat)
	go run(s.embed, &embed)
	wg.Wait()
	return
}
