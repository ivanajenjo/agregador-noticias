package annotate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CallOptions tune one completion.
type CallOptions struct {
	Temperature float64
	MaxTokens   int
	Seed        *int // deterministic sampling when the server supports it
	JSON        bool // ask for response_format json_object
}

// Completer is a non-streaming chat completion. *Client implements it; tests
// and Evaluate use fakes.
type Completer interface {
	Complete(ctx context.Context, msgs []Message, o CallOptions) (string, error)
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("http %d: %s", e.Status, e.Body) }

// Client talks to an OpenAI-compatible /v1/chat/completions endpoint.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client

	noJSONMode atomic.Bool // server rejected response_format: stop sending it
}

// NewClient builds a client. baseURL may be "http://host:11434" or
// "http://host:1234/v1".
func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{BaseURL: normalizeBase(baseURL), APIKey: apiKey, Model: model,
		HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func normalizeBase(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return ""
	}
	last := u[strings.LastIndex(u, "/")+1:]
	if len(last) >= 2 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		return u
	}
	return u + "/v1"
}

type chatReq struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Seed           *int              `json:"seed,omitempty"`
	Stream         bool              `json:"stream"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

// Complete performs one request and returns the assistant text.
func (c *Client) Complete(ctx context.Context, msgs []Message, o CallOptions) (string, error) {
	useJSON := o.JSON && !c.noJSONMode.Load()
	out, err := c.do(ctx, msgs, o, useJSON)
	var he *HTTPError
	if err != nil && useJSON && asHTTP(err, &he) && (he.Status == 400 || he.Status == 422) &&
		strings.Contains(strings.ToLower(he.Body), "response_format") {
		c.noJSONMode.Store(true)
		return c.do(ctx, msgs, o, false)
	}
	return out, err
}

func asHTTP(err error, target **HTTPError) bool {
	he, ok := err.(*HTTPError)
	if ok {
		*target = he
	}
	return ok
}

func (c *Client) do(ctx context.Context, msgs []Message, o CallOptions, jsonMode bool) (string, error) {
	rq := chatReq{Model: c.Model, Messages: msgs, Temperature: o.Temperature, MaxTokens: o.MaxTokens, Seed: o.Seed}
	if jsonMode {
		rq.ResponseFormat = map[string]string{"type": "json_object"}
	}
	body, _ := json.Marshal(rq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", &HTTPError{Status: resp.StatusCode, Body: truncate(string(b), 300)}
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("annotate: respuesta no válida: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("annotate: respuesta sin choices")
	}
	return r.Choices[0].Message.Content, nil
}

// Probe reports whether the server answers at all (any HTTP status counts as
// reachable; only network errors mean "down"). It is cheap and is used before
// each batch so nothing is attempted while the model host is off.
func (c *Client) Probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.Body.Close()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
