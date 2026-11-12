package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

// EmbedConfig configures the embeddings client.
type EmbedConfig struct {
	BaseURL string // with or without trailing /v1
	APIKey  string // optional bearer token
	Model   string

	BatchSize   int           // texts per request (default 16)
	Timeout     time.Duration // per request (default 60s)
	DialTimeout time.Duration // default 5s
	MaxRetries  int           // 0 => default 3; negative => no retries
	Backoff     time.Duration // initial retry delay (default 500ms)
	MaxBackoff  time.Duration // default 8s

	Normalize bool // L2-normalize returned vectors
	Dims      int  // expected dimensions (0 = accept any, but must be consistent)

	QueryPrefix string // e.g. "search_query: " (nomic) or "query: " (e5)
	DocPrefix   string // e.g. "search_document: " or "passage: "

	HTTPClient *http.Client // optional
}

// OpenAIEmbedder implements domain.Embedder against /v1/embeddings.
type OpenAIEmbedder struct {
	cfg    EmbedConfig
	base   string
	client *http.Client
	rc     retryCfg
}

// NewEmbedder builds an embeddings client.
func NewEmbedder(cfg EmbedConfig) *OpenAIEmbedder {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 16
	}
	cfg.Timeout = orDur(cfg.Timeout, 60*time.Second)
	cfg.DialTimeout = orDur(cfg.DialTimeout, 5*time.Second)
	retries := cfg.MaxRetries
	if retries == 0 {
		retries = 3
	} else if retries < 0 {
		retries = 0
	}
	e := &OpenAIEmbedder{
		cfg:    cfg,
		base:   normalizeBase(cfg.BaseURL),
		client: cfg.HTTPClient,
		rc:     retryCfg{max: retries, base: orDur(cfg.Backoff, 500*time.Millisecond), capD: orDur(cfg.MaxBackoff, 8*time.Second)},
	}
	if e.client == nil {
		e.client = newClient(cfg.DialTimeout, cfg.Timeout)
	}
	return e
}

// Model implements domain.Embedder.
func (e *OpenAIEmbedder) Model() string { return e.cfg.Model }

// Embed embeds texts as given (no prefix applied).
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += e.cfg.BatchSize {
		j := min(i+e.cfg.BatchSize, len(texts))
		vecs, err := e.embedBatch(ctx, texts[i:j])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	if e.cfg.Dims == 0 && len(out) > 1 {
		for _, v := range out {
			if len(v) != len(out[0]) {
				return nil, fmt.Errorf("embeddings: inconsistent dimensions across batches (%d vs %d)", len(v), len(out[0]))
			}
		}
	}
	return out, nil
}

// EmbedDocs embeds document chunks, prepending DocPrefix.
func (e *OpenAIEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if e.cfg.DocPrefix == "" {
		return e.Embed(ctx, texts)
	}
	p := make([]string, len(texts))
	for i, t := range texts {
		p[i] = e.cfg.DocPrefix + t
	}
	return e.Embed(ctx, p)
}

// EmbedQuery embeds a search query, prepending QueryPrefix.
func (e *OpenAIEmbedder) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	v, err := e.Embed(ctx, []string{e.cfg.QueryPrefix + q})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

type embedResp struct {
	Data []struct {
		Index     *int      `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	in := make([]string, len(texts))
	for i, t := range texts {
		if t == "" {
			t = " "
		}
		in[i] = t
	}
	body, _ := json.Marshal(map[string]any{"model": e.cfg.Model, "input": in})
	var vecs [][]float32
	err := doRetry(ctx, e.rc, isTransient, func() error {
		rctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, e.base+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if e.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
		if err != nil {
			return err
		}
		if resp.StatusCode/100 != 2 {
			return &HTTPError{Status: resp.StatusCode, Body: snippet(raw), RetryAfter: retryAfter(resp.Header)}
		}
		vecs, err = e.parse(raw, len(in))
		return err
	})
	return vecs, err
}

func (e *OpenAIEmbedder) parse(raw []byte, n int) ([][]float32, error) {
	var r embedResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("embeddings: invalid JSON: %w", err)
	}
	if r.Error != nil && len(r.Data) == 0 {
		return nil, fmt.Errorf("embeddings: server error: %s", r.Error.Message)
	}
	if len(r.Data) != n {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(r.Data), n)
	}
	idx := make([]int, n)
	useIdx := true
	seen := make(map[int]bool, n)
	for i, d := range r.Data {
		if d.Index == nil || *d.Index < 0 || *d.Index >= n || seen[*d.Index] {
			useIdx = false
			break
		}
		seen[*d.Index] = true
		idx[i] = *d.Index
	}
	if !useIdx {
		for i := range idx {
			idx[i] = i
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return idx[order[a]] < idx[order[b]] })
	out := make([][]float32, n)
	dims := e.cfg.Dims
	for pos, o := range order {
		v := r.Data[o].Embedding
		if len(v) == 0 {
			return nil, fmt.Errorf("embeddings: empty vector at %d", pos)
		}
		if dims == 0 {
			dims = len(v)
		}
		if len(v) != dims {
			return nil, fmt.Errorf("embeddings: vector %d has %d dims, expected %d", pos, len(v), dims)
		}
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, fmt.Errorf("embeddings: non-finite value in vector %d", pos)
			}
		}
		if e.cfg.Normalize {
			normalize(v)
		}
		out[pos] = v
	}
	return out, nil
}

func normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= inv
	}
}
