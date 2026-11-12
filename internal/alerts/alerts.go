// Package alerts evaluates "story" and "watch" alerts periodically and delivers
// them through ntfy / Telegram / a generic webhook, with dedup, an hourly rate
// limit and quiet hours. It depends only on internal/domain.
package alerts

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Deps are the engine's collaborators. Follow is required; Trends and Store may
// be nil (that kind of alert is then skipped).
type Deps struct {
	Store  domain.Store
	Follow domain.FollowStore
	Trends domain.TrendProvider
	Log    *slog.Logger
}

// Options configure the engine. Zero values get defaults.
type Options struct {
	Window     time.Duration // story window (default 2h)
	MinSources int           // outlets covering a story (default 4)
	MaxPerHour int           // global rate limit; <=0 = unlimited
	QuietStart string        // "HH:MM" local; empty or equal to QuietEnd = never quiet
	QuietEnd   string
	Location   *time.Location
	PublicURL  string // base of the links; empty => links omitted

	NtfyURL, NtfyTopic        string
	TelegramBot, TelegramChat string
	TelegramAPI               string // override for tests; default https://api.telegram.org
	WebhookURL                string

	Notifiers  []Notifier // if non-nil, replaces the ones built from the fields above
	HTTPClient *http.Client
	Retries    int           // attempts per notifier (default 3)
	Backoff    time.Duration // first retry delay, doubled each time (default 2s)
	Now        func() time.Time

	MaxStoriesPerRun int // default 5
}

// Engine implements domain.AlertService and exposes RunOnce for the scheduler.
type Engine struct {
	d         Deps
	o         Options
	log       *slog.Logger
	notifiers []Notifier
	qs, qe    int // quiet hours as minutes since midnight; -1 = off
}

var _ domain.AlertService = (*Engine)(nil)

const (
	runTimeout       = 3 * time.Minute
	watchLookback    = 24 * time.Hour // late-ingested articles are still found
	watchSearchLimit = 50
	giveUpAfter      = 2 * time.Hour // stop retrying a failing delivery
)

// New builds the engine. Invalid channel settings are logged and that channel is skipped.
func New(d Deps, o Options) *Engine {
	e := &Engine{d: d, o: o, log: d.Log, qs: -1, qe: -1}
	if e.log == nil {
		e.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if e.o.Window <= 0 {
		e.o.Window = 2 * time.Hour
	}
	if e.o.MinSources <= 0 {
		e.o.MinSources = 4
	}
	if e.o.Location == nil {
		e.o.Location = time.UTC
	}
	if e.o.Now == nil {
		e.o.Now = time.Now
	}
	if e.o.Retries <= 0 {
		e.o.Retries = 3
	}
	if e.o.Backoff <= 0 {
		e.o.Backoff = 2 * time.Second
	}
	if e.o.MaxStoriesPerRun <= 0 {
		e.o.MaxStoriesPerRun = 5
	}
	e.o.PublicURL = strings.TrimRight(strings.TrimSpace(e.o.PublicURL), "/")
	client := e.o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Notifiers != nil {
		e.notifiers = o.Notifiers
	} else {
		add := func(n Notifier, err error) {
			if err != nil {
				e.log.Warn("alerts: canal ignorado", "err", err)
				return
			}
			e.notifiers = append(e.notifiers, n)
		}
		if o.NtfyURL != "" || o.NtfyTopic != "" {
			add(newNtfy(client, o.NtfyURL, o.NtfyTopic))
		}
		if o.TelegramBot != "" || o.TelegramChat != "" {
			add(newTelegram(client, o.TelegramAPI, o.TelegramBot, o.TelegramChat))
		}
		if o.WebhookURL != "" {
			add(newWebhook(client, o.WebhookURL))
		}
	}
	e.qs, e.qe = parseClock(o.QuietStart), parseClock(o.QuietEnd)
	if e.qs < 0 || e.qe < 0 || e.qs == e.qe {
		e.qs, e.qe = -1, -1
	}
	return e
}

func parseClock(s string) int {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}

// Channels lists the active notifier names.
func (e *Engine) Channels() []string {
	out := make([]string, 0, len(e.notifiers))
	for _, n := range e.notifiers {
		out = append(out, n.Name())
	}
	return out
}

func (e *Engine) quiet(now time.Time) bool {
	if e.qs < 0 {
		return false
	}
	t := now.In(e.o.Location)
	m := t.Hour()*60 + t.Minute()
	if e.qs < e.qe {
		return m >= e.qs && m < e.qe
	}
	return m >= e.qs || m < e.qe // wraps midnight
}

// link builds an absolute URL from a path, or "" without a public URL.
func (e *Engine) link(path string) string {
	if e.o.PublicURL == "" {
		return ""
	}
	return e.o.PublicURL + path
}

// deliver sends m through every channel (or only logs it when there is none),
// recording one alert_log row per channel. It reports whether anything got out
// or was logged; a total failure is recorded with ok=0 and reported as !sent.
func (e *Engine) deliver(ctx context.Context, kind, key string, m Message, now time.Time) (sent bool) {
	m.Kind, m.At = kind, now.UTC().Format(time.RFC3339)
	rec := func(ch string, err error, detail string) {
		a := domain.AlertRecord{Kind: kind, Key: key, Title: m.Title, URL: m.URL, SentAt: now, Channel: ch, OK: err == nil, Detail: detail}
		if err != nil {
			a.Detail = truncate(err.Error(), 300)
			e.log.Warn("alerts: envío fallido", "channel", ch, "kind", kind, "err", err)
		}
		if serr := e.d.Follow.SaveAlert(ctx, a); serr != nil {
			e.log.Error("alerts: guardar alert_log", "err", serr)
		}
	}
	if len(e.notifiers) == 0 {
		rec(domain.ChannelLog, nil, "sin canal configurado")
		return true
	}
	for _, n := range e.notifiers {
		err := sendWithRetry(ctx, n, m, e.o.Retries, e.o.Backoff)
		rec(n.Name(), err, "enviada")
		if err == nil {
			sent = true
		}
	}
	return sent
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// holdQuiet records an alert held back by quiet hours.
func (e *Engine) holdQuiet(ctx context.Context, kind, key string, m Message, now time.Time) {
	err := e.d.Follow.SaveAlert(ctx, domain.AlertRecord{Kind: kind, Key: key, Title: m.Title, URL: m.URL, SentAt: now,
		Channel: domain.ChannelQuiet, Detail: m.Body})
	if err != nil {
		e.log.Error("alerts: guardar alerta en espera", "err", err)
	}
}

type runStats struct{ sent, held, limited, failed int }

// RunOnce evaluates story and watch alerts. It is meant for sched.Job.
func (e *Engine) RunOnce(ctx context.Context) (string, error) {
	if e.d.Follow == nil {
		return "", fmt.Errorf("alerts: sin almacén de seguimientos")
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	now := e.o.Now()
	quiet := e.quiet(now)
	var st runStats

	used := 0
	if e.o.MaxPerHour > 0 {
		n, err := e.d.Follow.CountAlertsSince(ctx, now.Add(-time.Hour))
		if err != nil {
			return "", err
		}
		used = n
	}
	// allow reports whether one more message may go out now.
	allow := func() bool {
		if quiet {
			return true // held, not sent
		}
		if e.o.MaxPerHour > 0 && used >= e.o.MaxPerHour {
			st.limited++
			return false
		}
		return true
	}
	// dispatch sends (or holds) one alert; reports whether it is settled.
	dispatch := func(kind, key string, m Message) bool {
		if quiet {
			e.holdQuiet(ctx, kind, key, m, now)
			st.held++
			return true
		}
		used++
		if e.deliver(ctx, kind, key, m, now) {
			st.sent++
			return true
		}
		st.failed++
		return false
	}

	var firstErr error
	if !quiet {
		e.flushQuiet(ctx, now, &st)
		if e.o.MaxPerHour > 0 { // the summary counts against the limit
			if n, err := e.d.Follow.CountAlertsSince(ctx, now.Add(-time.Hour)); err == nil {
				used = n
			}
		}
	}
	if err := e.storyAlerts(ctx, now, allow, dispatch); err != nil {
		firstErr = err
	}
	if err := e.watchAlerts(ctx, now, allow, dispatch); err != nil && firstErr == nil {
		firstErr = err
	}
	_ = e.d.Follow.PruneWatchHits(ctx, now.Add(-3*watchLookback))
	msg := fmt.Sprintf("%d enviadas, %d en espera, %d limitadas, %d fallidas", st.sent, st.held, st.limited, st.failed)
	return msg, firstErr
}

// flushQuiet sends one summary of the alerts held during quiet hours.
func (e *Engine) flushQuiet(ctx context.Context, now time.Time, st *runStats) {
	pend, err := e.d.Follow.PendingQuiet(ctx)
	if err != nil || len(pend) == 0 {
		if err != nil {
			e.log.Warn("alerts: pendientes", "err", err)
		}
		return
	}
	var lines []string
	for i, p := range pend {
		if i >= 10 {
			lines = append(lines, fmt.Sprintf("… y %d más", len(pend)-10))
			break
		}
		lines = append(lines, "· "+p.Title)
	}
	m := Message{
		Title: fmt.Sprintf("Resumen nocturno: %d %s", len(pend), plural(len(pend), "aviso", "avisos")),
		Body:  strings.Join(lines, "\n"),
		URL:   e.link("/seguimientos"),
	}
	key := "quiet-" + now.In(e.o.Location).Format("20060102T1504")
	if !e.deliver(ctx, domain.AlertSummary, key, m, now) {
		st.failed++
		return // keep them pending; retried next run
	}
	st.sent++
	ids := make([]int64, len(pend))
	for i, p := range pend {
		ids[i] = p.ID
	}
	if err := e.d.Follow.ResolveQuiet(ctx, ids, "incluida en el resumen "+key); err != nil {
		e.log.Error("alerts: resolver pendientes", "err", err)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (e *Engine) sourceNames(ctx context.Context) map[string]string {
	m := map[string]string{}
	if e.d.Store == nil {
		return m
	}
	src, err := e.d.Store.ListSources(ctx)
	if err != nil {
		e.log.Warn("alerts: fuentes", "err", err)
		return m
	}
	for _, s := range src {
		m[s.ID] = s.Name
	}
	return m
}

func nameOf(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	return id
}

func (e *Engine) storyAlerts(ctx context.Context, now time.Time, allow func() bool, dispatch func(kind, key string, m Message) bool) error {
	if e.d.Trends == nil {
		return nil
	}
	stories, err := e.d.Trends.Stories(ctx, domain.TrendOptions{Window: e.o.Window, MinSources: e.o.MinSources, Now: now, Limit: 30})
	if err != nil {
		return fmt.Errorf("historias: %w", err)
	}
	day := now.In(e.o.Location).Format("2006-01-02")
	done := 0
	for _, s := range stories {
		if s.Sources < e.o.MinSources || s.ID == "" {
			continue
		}
		key := s.ID + "@" + day
		if h, err := e.d.Follow.AlertHandled(ctx, domain.AlertStory, key, now.Add(-giveUpAfter)); err != nil {
			return err
		} else if h {
			continue
		}
		if done >= e.o.MaxStoriesPerRun || !allow() {
			break
		}
		done++
		dispatch(domain.AlertStory, key, e.storyMessage(s))
	}
	return nil
}

func (e *Engine) storyMessage(s domain.Story) Message {
	var names []string
	for _, c := range s.Coverage {
		names = append(names, firstNonEmpty(c.Name, c.SourceID))
	}
	body := fmt.Sprintf("%d medios", s.Sources)
	list := names
	more := ""
	if len(list) > 4 {
		list, more = list[:4], "…"
	}
	if len(list) > 0 {
		body += ": " + strings.Join(list, ", ") + more
	}
	if s.FirstBy != "" {
		first := s.FirstBy
		for _, c := range s.Coverage {
			if c.SourceID == s.FirstBy && c.Name != "" {
				first = c.Name
			}
		}
		body += ". Primero: " + first
	}
	return Message{Title: truncate(oneLine(s.Title), 140), Body: body, URL: e.link("/tendencias"), Sources: names}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (e *Engine) watchAlerts(ctx context.Context, now time.Time, allow func() bool, dispatch func(kind, key string, m Message) bool) error {
	if e.d.Store == nil {
		return nil
	}
	watches, err := e.d.Follow.ListWatches(ctx)
	if err != nil {
		return fmt.Errorf("seguimientos: %w", err)
	}
	names := e.sourceNames(ctx)
	for _, w := range watches {
		if !w.Notify {
			continue
		}
		hits, err := e.d.Store.SearchText(ctx, w.Query, domain.Filter{SourceIDs: w.SourceIDs, Leanings: w.Leanings, From: now.Add(-watchLookback)}, watchSearchLimit)
		if err != nil {
			e.log.Warn("alerts: búsqueda del seguimiento", "watch", w.ID, "err", err)
			continue
		}
		ids := make([]int64, len(hits))
		for i, h := range hits {
			ids[i] = h.Article.ID
		}
		fresh, err := e.d.Follow.NewWatchHits(ctx, w.ID, ids)
		if err != nil {
			return err
		}
		if w.LastEvalAt.IsZero() { // first evaluation: existing matches are history, not news
			_ = e.d.Follow.MarkWatchHits(ctx, w.ID, fresh, now)
			_ = e.d.Follow.SetWatchEvaluated(ctx, w.ID, now)
			continue
		}
		if len(fresh) == 0 {
			_ = e.d.Follow.SetWatchEvaluated(ctx, w.ID, now)
			continue
		}
		set := map[int64]bool{}
		for _, id := range fresh {
			set[id] = true
		}
		var arts []domain.Article
		for _, h := range hits {
			if set[h.Article.ID] {
				arts = append(arts, h.Article)
			}
		}
		sort.SliceStable(arts, func(i, j int) bool { return arts[i].PublishedAt.After(arts[j].PublishedAt) })
		key := fmt.Sprintf("w%d:a%d:n%d", w.ID, arts[0].ID, len(arts))
		if h, err := e.d.Follow.AlertHandled(ctx, domain.AlertWatch, key, now.Add(-giveUpAfter)); err != nil {
			return err
		} else if h { // already delivered/held, or failing for too long: stop retrying
			_ = e.d.Follow.MarkWatchHits(ctx, w.ID, fresh, now)
			_ = e.d.Follow.SetWatchEvaluated(ctx, w.ID, now)
			continue
		}
		if !allow() {
			continue // hits stay unmarked: retried next run
		}
		m := e.watchMessage(w, arts, names)
		if dispatch(domain.AlertWatch, key, m) {
			_ = e.d.Follow.MarkWatchHits(ctx, w.ID, fresh, now)
			_ = e.d.Follow.SetWatchEvaluated(ctx, w.ID, now)
		}
	}
	return nil
}

func (e *Engine) watchMessage(w domain.Watch, arts []domain.Article, names map[string]string) Message {
	var srcs []string
	seen := map[string]bool{}
	var lines []string
	for i, a := range arts {
		n := nameOf(names, a.SourceID)
		if !seen[a.SourceID] {
			seen[a.SourceID] = true
			srcs = append(srcs, n)
		}
		if i < 5 {
			lines = append(lines, fmt.Sprintf("· %s: %s", n, truncate(oneLine(a.Title), 120)))
		}
	}
	if len(arts) > 5 {
		lines = append(lines, fmt.Sprintf("… y %d más", len(arts)-5))
	}
	title := fmt.Sprintf("«%s»: %d %s", truncate(oneLine(w.Name), 60), len(arts), plural(len(arts), "novedad", "novedades"))
	if len(arts) == 1 {
		title = fmt.Sprintf("«%s»: %s", truncate(oneLine(w.Name), 60), truncate(oneLine(arts[0].Title), 100))
		lines = []string{nameOf(names, arts[0].SourceID)}
	}
	body := strings.Join(lines, "\n")
	if len(srcs) > 1 {
		list := srcs
		more := ""
		if len(list) > 4 {
			list, more = list[:4], "…"
		}
		body = fmt.Sprintf("%d medios: %s%s\n%s", len(srcs), strings.Join(list, ", "), more, body)
	}
	link := e.link("/seguimientos")
	if len(arts) == 1 {
		link = e.link(fmt.Sprintf("/articulo/%d", arts[0].ID))
	}
	return Message{Title: title, Body: body, URL: link, Sources: srcs}
}

// SendTest sends a test message through every configured channel.
func (e *Engine) SendTest(ctx context.Context) ([]domain.AlertRecord, error) {
	if e.d.Follow == nil {
		return nil, fmt.Errorf("alerts: sin almacén de seguimientos")
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	now := e.o.Now()
	key := fmt.Sprintf("test-%d", now.UnixNano())
	e.deliver(ctx, domain.AlertTest, key, Message{
		Title: "Prueba de notificación",
		Body:  "Si ves este mensaje, el canal funciona.",
		URL:   e.link("/seguimientos"),
	}, now)
	all, err := e.d.Follow.ListAlerts(ctx, 20)
	if err != nil {
		return nil, err
	}
	var out []domain.AlertRecord
	for _, a := range all {
		if a.Kind == domain.AlertTest && a.Key == key {
			out = append(out, a)
		}
	}
	return out, nil
}
