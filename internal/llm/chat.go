package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// ChatConfig configures the chat client.
type ChatConfig struct {
	BaseURL string
	APIKey  string
	Model   string

	Temperature *float64 // nil => server default
	MaxTokens   int      // 0 => server default

	HeaderTimeout time.Duration // wait for response headers / first byte (default 120s)
	DialTimeout   time.Duration // default 5s
	MaxRetries    int           // retries before the first delta; 0 => 1; negative => none
	Backoff       time.Duration // default 500ms
	MaxBackoff    time.Duration // default 4s

	HTTPClient *http.Client
}

// OpenAIChat implements domain.Chat against /v1/chat/completions (SSE).
type OpenAIChat struct {
	cfg    ChatConfig
	base   string
	client *http.Client
	rc     retryCfg
}

var _ domain.Chat = (*OpenAIChat)(nil)
var _ domain.Embedder = (*OpenAIEmbedder)(nil)

// NewChat builds a streaming chat client.
func NewChat(cfg ChatConfig) *OpenAIChat {
	retries := cfg.MaxRetries
	if retries == 0 {
		retries = 1
	} else if retries < 0 {
		retries = 0
	}
	c := &OpenAIChat{
		cfg:    cfg,
		base:   normalizeBase(cfg.BaseURL),
		client: cfg.HTTPClient,
		rc:     retryCfg{max: retries, base: orDur(cfg.Backoff, 500*time.Millisecond), capD: orDur(cfg.MaxBackoff, 4*time.Second)},
	}
	if c.client == nil {
		c.client = newClient(orDur(cfg.DialTimeout, 5*time.Second), orDur(cfg.HeaderTimeout, 120*time.Second))
	}
	return c
}

// Model implements domain.Chat.
func (c *OpenAIChat) Model() string { return c.cfg.Model }

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

// Stream implements domain.Chat. Cancelling ctx closes the connection.
func (c *OpenAIChat) Stream(ctx context.Context, msgs []domain.ChatMessage, onDelta func(string) error) error {
	reqBody := map[string]any{"model": c.cfg.Model, "messages": msgs, "stream": true}
	if c.cfg.Temperature != nil {
		reqBody["temperature"] = *c.cfg.Temperature
	}
	if c.cfg.MaxTokens > 0 {
		reqBody["max_tokens"] = c.cfg.MaxTokens
	}
	body, _ := json.Marshal(reqBody)

	started := false
	var cbErr error
	deliver := func(s string) error {
		if s == "" {
			return nil
		}
		started = true
		if err := onDelta(s); err != nil {
			cbErr = err
			return err
		}
		return nil
	}
	retry := func(err error) bool { return !started && cbErr == nil && isTransient(err) }

	err := doRetry(ctx, c.rc, retry, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			return &HTTPError{Status: resp.StatusCode, Body: snippet(raw), RetryAfter: retryAfter(resp.Header)}
		}
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			// Server ignored stream:true; deliver the whole answer at once.
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			if err != nil {
				return err
			}
			var ch chatChunk
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("chat: invalid JSON: %w", err)
			}
			if len(ch.Choices) > 0 {
				return deliver(ch.Choices[0].Message.Content)
			}
			return nil
		}
		return parseSSE(resp.Body, func(data string) (bool, error) {
			if data == "[DONE]" {
				return true, nil
			}
			var ch chatChunk
			if err := json.Unmarshal([]byte(data), &ch); err != nil {
				return false, nil // tolerate junk frames
			}
			if len(ch.Error) > 0 && string(ch.Error) != "null" {
				return false, fmt.Errorf("chat: server error: %s", snippet(ch.Error))
			}
			if len(ch.Choices) == 0 {
				return false, nil
			}
			return false, deliver(ch.Choices[0].Delta.Content)
		})
	})
	if cbErr != nil {
		return cbErr
	}
	return err
}

// parseSSE reads Server-Sent Events from r and calls onData with the data
// payload of each event. onData returns done=true to stop. Comment lines
// (": keep-alive") and non-data fields are ignored; frames may be split at
// arbitrary byte boundaries.
func parseSSE(r io.Reader, onData func(data string) (done bool, err error)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var data []string
	flush := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		s := strings.Join(data, "\n")
		data = data[:0]
		return onData(s)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if done, e := flush(); e != nil || done {
					return e
				}
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_, e := flush()
				return e
			}
			return err
		}
	}
}
