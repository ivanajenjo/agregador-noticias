package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message is what a Notifier delivers.
type Message struct {
	Kind    string   `json:"kind"` // story | watch | summary | test
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	URL     string   `json:"url,omitempty"`
	Sources []string `json:"sources,omitempty"`
	At      string   `json:"sent_at"`
}

// Notifier delivers a message to one channel. Send must honour ctx.
type Notifier interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// permanentError marks failures that retrying cannot fix (4xx other than 429).
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// poster is the shared HTTP plumbing of the notifiers.
type poster struct {
	client *http.Client
	secret string // redacted from error text
}

func (p poster) redact(err error) error {
	if err == nil || p.secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), p.secret, "***"))
}

func (p poster) do(req *http.Request) error {
	resp, err := p.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // drop the URL: it may embed a token
			err = ue.Err
		}
		return p.redact(fmt.Errorf("petición fallida: %w", err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	e := p.redact(fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return permanentError{e}
	}
	return e
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func validBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("URL no válida: %q", raw)
	}
	return u, nil
}

// ---- ntfy ----

type ntfyNotifier struct {
	poster
	endpoint string
}

func newNtfy(client *http.Client, base, topic string) (Notifier, error) {
	u, err := validBase(base)
	if err != nil {
		return nil, err
	}
	topic = strings.Trim(strings.TrimSpace(topic), "/")
	if topic == "" {
		return nil, errors.New("ntfy: falta el topic")
	}
	return &ntfyNotifier{poster{client: client}, strings.TrimRight(u.String(), "/") + "/" + url.PathEscape(topic)}, nil
}

func (n *ntfyNotifier) Name() string { return "ntfy" }

func (n *ntfyNotifier) Send(ctx context.Context, m Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, strings.NewReader(m.Body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", mime.QEncoding.Encode("utf-8", oneLine(m.Title))) // RFC 2047: ntfy decodes it
	req.Header.Set("Priority", "default")
	req.Header.Set("Tags", "newspaper")
	if m.URL != "" {
		req.Header.Set("Click", m.URL)
	}
	return n.do(req)
}

// ---- Telegram ----

type telegramNotifier struct {
	poster
	api, chat string
}

func newTelegram(client *http.Client, apiBase, token, chat string) (Notifier, error) {
	token, chat = strings.TrimSpace(token), strings.TrimSpace(chat)
	if token == "" || chat == "" {
		return nil, errors.New("telegram: faltan token o chat_id")
	}
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	u, err := validBase(apiBase)
	if err != nil {
		return nil, err
	}
	return &telegramNotifier{poster{client, token}, strings.TrimRight(u.String(), "/") + "/bot" + token + "/sendMessage", chat}, nil
}

func (t *telegramNotifier) Name() string { return "telegram" }

func (t *telegramNotifier) Send(ctx context.Context, m Message) error {
	text := m.Title
	if m.Body != "" {
		text += "\n" + m.Body
	}
	if m.URL != "" {
		text += "\n" + m.URL
	}
	if r := []rune(text); len(r) > 4000 { // Telegram limit is 4096
		text = string(r[:3999]) + "…"
	}
	// No parse_mode: the text is sent verbatim, nothing to escape.
	b, _ := json.Marshal(map[string]any{"chat_id": t.chat, "text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.api, bytes.NewReader(b))
	if err != nil {
		return permanentError{t.redact(err)}
	}
	req.Header.Set("Content-Type", "application/json")
	return t.do(req)
}

// ---- generic webhook ----

type webhookNotifier struct {
	poster
	endpoint string
}

func newWebhook(client *http.Client, raw string) (Notifier, error) {
	u, err := validBase(raw)
	if err != nil {
		return nil, err
	}
	return &webhookNotifier{poster{client: client}, u.String()}, nil
}

func (w *webhookNotifier) Name() string { return "webhook" }

func (w *webhookNotifier) Send(ctx context.Context, m Message) error {
	b, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(b))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	return w.do(req)
}

// sendWithRetry tries up to attempts times with exponential backoff.
func sendWithRetry(ctx context.Context, n Notifier, m Message, attempts int, backoff time.Duration) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = n.Send(ctx, m); err == nil {
			return nil
		}
		var pe permanentError
		if errors.As(err, &pe) || ctx.Err() != nil || i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff << i):
		}
	}
	return err
}
