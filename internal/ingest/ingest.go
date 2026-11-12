// Package ingest fetches RSS/Atom feeds, normalizes items, optionally extracts
// the full article text with readability and stores new articles through
// domain.Store.
package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/mmcdole/gofeed"
	"golang.org/x/time/rate"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	shortContentChars = 600   // feed content shorter than this triggers a page fetch
	maxContentRunes   = 20000 // stored Content cap
	maxSummaryRunes   = 1000
)

// Options configures the Ingestor. Zero numeric/string fields get defaults in
// New; booleans are taken as given (use DefaultOptions for the recommended set).
type Options struct {
	Sources       []domain.Source
	UserAgent     string        // default "agregador-noticias/1.0 (+personal news aggregator)"
	Timeout       time.Duration // per HTTP request; default 20s
	Concurrency   int           // parallel feeds; default 4
	PerHostDelay  time.Duration // min delay between requests to one host; 0 = none
	MaxBodyBytes  int64         // response cap; default 5 MiB
	FetchFullText bool          // allow readability page fetches (still gated by Source.FullText)
	RespectRobots bool          // honour robots.txt for article-page fetches

	MaxRetries         int           // retries on 5xx/429/network errors; default 2 (use -1 for none)
	RetryBackoff       time.Duration // base backoff, doubles each retry; default 1s
	MaxFullTextPerFeed int           // page fetches per feed per run; default 40

	// AllowPrivate disables the anti-SSRF guard for article-page fetches
	// (tests only; never enable in production).
	AllowPrivate bool
	// HTTPClient, if set, is used for feeds AND pages (tests); the SSRF guard
	// is then the caller's responsibility.
	HTTPClient *http.Client
}

// DefaultOptions returns the recommended production options.
func DefaultOptions() Options {
	return Options{
		UserAgent: defaultUA, Timeout: 20 * time.Second, Concurrency: 4,
		PerHostDelay: 500 * time.Millisecond, MaxBodyBytes: 5 << 20,
		FetchFullText: true, RespectRobots: true,
	}
}

const defaultUA = "agregador-noticias/1.0 (+personal news aggregator)"

// urlChecker is an optional interface a Store may implement so the ingestor can
// skip fetching article pages for URLs it already has.
type urlChecker interface {
	HasURL(ctx context.Context, canonical string) (bool, error)
}

// Ingestor implements domain.Ingestor.
type Ingestor struct {
	st     domain.Store
	opts   Options
	log    *slog.Logger
	feedC  *http.Client
	pageC  *http.Client
	robots *robotsCache

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

var _ domain.Ingestor = (*Ingestor)(nil)

// New creates an Ingestor.
func New(st domain.Store, opts Options, log *slog.Logger) *Ingestor {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUA
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 5 << 20
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 2
	} else if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = time.Second
	}
	if opts.MaxFullTextPerFeed <= 0 {
		opts.MaxFullTextPerFeed = 40
	}
	in := &Ingestor{st: st, opts: opts, log: log, limiters: map[string]*rate.Limiter{}}
	if opts.HTTPClient != nil {
		in.feedC, in.pageC = opts.HTTPClient, opts.HTTPClient
	} else {
		in.feedC = newClient(opts.Timeout, true)
		in.pageC = newClient(opts.Timeout, opts.AllowPrivate)
	}
	in.robots = newRobotsCache(opts.UserAgent, func(ctx context.Context, u string) (*http.Response, error) {
		resp, _, err := in.do(ctx, in.pageC, u, nil, 0)
		return resp, err
	})
	return in
}

// RunOnce fetches every enabled source and returns the number of new articles.
// Failures of individual feeds never abort the others; they are recorded in
// FeedState. The returned error is non-nil only when ctx is cancelled.
func (in *Ingestor) RunOnce(ctx context.Context) (int, error) {
	type job struct {
		src  domain.Source
		feed domain.Feed
	}
	var jobs []job
	for _, s := range in.opts.Sources {
		if !s.IsEnabled() {
			continue
		}
		for _, f := range s.Feeds {
			jobs = append(jobs, job{s, f})
		}
	}
	var total atomic.Int64
	seen := &seenSet{m: map[string]struct{}{}}
	sem := make(chan struct{}, in.opts.Concurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					in.log.Error("ingest: panic", "source", j.src.ID, "feed", j.feed.URL, "panic", r)
				}
			}()
			n, err := in.processFeed(ctx, j.src, j.feed, seen)
			total.Add(int64(n))
			if err != nil {
				in.log.Warn("ingest: feed failed", "source", j.src.ID, "feed", j.feed.URL, "err", err)
			} else {
				in.log.Info("ingest: feed ok", "source", j.src.ID, "feed", j.feed.URL, "new", n)
			}
		}(j)
	}
	wg.Wait()
	return int(total.Load()), ctx.Err()
}

type seenSet struct {
	mu sync.Mutex
	m  map[string]struct{}
}

// claim returns true if key was not yet claimed in this run.
func (s *seenSet) claim(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; ok {
		return false
	}
	s.m[key] = struct{}{}
	return true
}

func (in *Ingestor) processFeed(ctx context.Context, src domain.Source, feed domain.Feed, seen *seenSet) (n int, err error) {
	state, serr := in.st.GetFeedState(ctx, src.ID, feed.URL)
	if serr != nil || state == nil {
		state = &domain.FeedState{}
	}
	st := *state
	st.SourceID, st.FeedURL = src.ID, feed.URL
	defer func() {
		now := time.Now()
		if err != nil {
			st.LastError, st.LastErrorAt = err.Error(), now
		} else {
			st.LastError, st.LastOK = "", now
		}
		if e := in.st.SaveFeedState(context.WithoutCancel(ctx), &st); e != nil {
			in.log.Warn("ingest: save feed state", "feed", feed.URL, "err", e)
		}
	}()

	hdr := http.Header{}
	hdr.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	if st.ETag != "" {
		hdr.Set("If-None-Match", st.ETag)
	}
	if st.LastModified != "" {
		hdr.Set("If-Modified-Since", st.LastModified)
	}
	resp, body, err := in.do(ctx, in.feedC, feed.URL, hdr, in.opts.MaxBodyBytes)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusNotModified {
		st.LastCount = 0
		return 0, nil
	}
	parsed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("parse feed: %w", err)
	}
	st.ETag = resp.Header.Get("ETag")
	st.LastModified = resp.Header.Get("Last-Modified")
	st.LastCount = len(parsed.Items)

	fullTextBudget := in.opts.MaxFullTextPerFeed
	var firstErr error
	for _, it := range parsed.Items {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		a, ok := in.normalizeItem(src, feed, it)
		if !ok || !seen.claim(src.ID+"|"+a.CanonicalURL) {
			continue
		}
		if in.wantFullText(src) && len([]rune(a.Content)) < shortContentChars && fullTextBudget > 0 {
			if uc, ok := in.st.(urlChecker); ok {
				if has, e := uc.HasURL(ctx, a.CanonicalURL); e == nil && has {
					continue // already stored; nothing to do
				}
			}
			fullTextBudget--
			if text, e := in.fetchArticleText(ctx, a.URL); e != nil {
				in.log.Debug("ingest: full text unavailable", "url", a.URL, "err", e)
			} else if len([]rune(text)) > len([]rune(a.Content)) {
				a.Content = truncateRunes(text, maxContentRunes)
				if a.Summary == "" {
					a.Summary = truncateRunes(text, 300)
				}
			}
		}
		inserted, e := in.st.InsertArticle(ctx, &a)
		if e != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("insert %s: %w", a.CanonicalURL, e)
			}
			continue
		}
		if inserted {
			n++
		}
	}
	return n, firstErr
}

func (in *Ingestor) wantFullText(src domain.Source) bool {
	return in.opts.FetchFullText && src.FullText
}

// normalizeItem converts a feed item to an Article (without ID/FirstSeenAt).
func (in *Ingestor) normalizeItem(src domain.Source, feed domain.Feed, it *gofeed.Item) (domain.Article, bool) {
	link := strings.TrimSpace(it.Link)
	if link == "" && it.GUID != "" && strings.HasPrefix(it.GUID, "http") {
		link = it.GUID
	}
	canon, err := CanonicalURL(link, feed.URL)
	title := collapseSpace(StripHTML(it.Title))
	if err != nil || title == "" {
		return domain.Article{}, false
	}
	a := domain.Article{
		SourceID: src.ID, URL: link, CanonicalURL: canon, Title: title,
		Summary: truncateRunes(StripHTML(it.Description), maxSummaryRunes),
		Content: truncateRunes(StripHTML(it.Content), maxContentRunes),
		Section: feed.Section,
	}
	if a.Content == a.Summary {
		a.Content = ""
	}
	if a.Summary == "" && a.Content != "" {
		a.Summary = truncateRunes(a.Content, 300)
	}
	if a.Section == "" && len(it.Categories) > 0 {
		a.Section = collapseSpace(it.Categories[0])
	}
	switch {
	case it.Author != nil && it.Author.Name != "":
		a.Author = collapseSpace(it.Author.Name)
	case len(it.Authors) > 0 && it.Authors[0] != nil && it.Authors[0].Name != "":
		a.Author = collapseSpace(it.Authors[0].Name)
	case it.DublinCoreExt != nil && len(it.DublinCoreExt.Creator) > 0:
		a.Author = collapseSpace(it.DublinCoreExt.Creator[0])
	}
	if it.PublishedParsed != nil {
		a.PublishedAt = it.PublishedParsed.UTC()
	} else if it.UpdatedParsed != nil {
		a.PublishedAt = it.UpdatedParsed.UTC()
	}
	return a, true
}

// fetchArticleText downloads the article page (SSRF-guarded, robots-aware) and
// extracts its main text with readability.
func (in *Ingestor) fetchArticleText(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("unsupported url")
	}
	if in.opts.RespectRobots && !in.robots.Allowed(ctx, u) {
		return "", errors.New("disallowed by robots.txt")
	}
	hdr := http.Header{}
	hdr.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	resp, body, err := in.do(ctx, in.pageC, rawURL, hdr, in.opts.MaxBodyBytes)
	if err != nil {
		return "", err
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
		return "", fmt.Errorf("unexpected content type %q", ct)
	}
	final := resp.Request.URL
	art, err := readability.FromReader(bytes.NewReader(body), final)
	if err != nil {
		return "", err
	}
	text := collapseSpaceKeepParagraphs(art.TextContent)
	if text == "" {
		return "", errors.New("readability: empty text")
	}
	return text, nil
}

// collapseSpaceKeepParagraphs trims lines and collapses runs of blank lines.
func collapseSpaceKeepParagraphs(s string) string {
	var out []string
	blank := false
	for _, l := range strings.Split(s, "\n") {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			blank = len(out) > 0
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("http status %d", e.code) }

// do performs a GET with per-host rate limiting, retry/backoff and a body
// size limit. 304 returns (resp, nil, nil). Non-2xx statuses are errors.
// With maxBody == 0 the body is not read and the caller must close resp.Body.
func (in *Ingestor) do(ctx context.Context, c *http.Client, rawURL string, hdr http.Header, maxBody int64) (*http.Response, []byte, error) {
	var lastErr error
	for attempt := 0; attempt <= in.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			d := in.opts.RetryBackoff << (attempt - 1)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		if err := in.waitHost(ctx, rawURL); err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", in.opts.UserAgent)
		for k, v := range hdr {
			req.Header[k] = v
		}
		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil || errors.Is(err, ErrBlockedAddress) {
				return nil, nil, err
			}
			continue // network error: retry
		}
		if maxBody == 0 {
			if resp.StatusCode == 200 {
				return resp, nil, nil
			}
			resp.Body.Close()
			return nil, nil, statusError{resp.StatusCode}
		}
		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			return resp, nil, nil
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = statusError{resp.StatusCode}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			resp.Body.Close()
			return nil, nil, statusError{resp.StatusCode}
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if int64(len(body)) > maxBody {
			return nil, nil, fmt.Errorf("response exceeds %d bytes", maxBody)
		}
		return resp, body, nil
	}
	return nil, nil, lastErr
}

func (in *Ingestor) waitHost(ctx context.Context, rawURL string) error {
	if in.opts.PerHostDelay <= 0 {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	in.mu.Lock()
	l := in.limiters[u.Host]
	if l == nil {
		l = rate.NewLimiter(rate.Every(in.opts.PerHostDelay), 1)
		in.limiters[u.Host] = l
	}
	in.mu.Unlock()
	return l.Wait(ctx)
}
