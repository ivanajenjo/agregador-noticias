// Package shot takes full-page JPEG screenshots of news front pages with
// chromedp. The browser is started on demand for each RunOnce (or a remote CDP
// endpoint is used) and is never kept resident.
package shot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Options configures a Taker. Zero values get sensible defaults.
type Options struct {
	Sources     []domain.Source
	Dir         string        // shots root directory
	ChromePath  string        // local binary; empty => autodetect
	RemoteURL   string        // CDP endpoint (ws://host:9222 or http://host:9222); overrides local launch
	Width       int           // default 1366
	Height      int           // viewport height, default 900
	Quality     int           // JPEG quality, default 70
	PageTimeout time.Duration // per-source budget, default 60s
	SettleDelay time.Duration // wait after load, default 2s
	MaxHeight   int           // cap of the full-page height in CSS px, default 12000
	Retention   time.Duration // <=0 keeps forever
	Now         func() time.Time
	ExtraFlags  []string // extra Chrome flags, "--name" or "--name=value"
	// NoSandbox adds --no-sandbox. nil => automatic (root or inside a container).
	NoSandbox *bool
	// Prominence receives the front-page headlines detected during each
	// capture. nil => the store passed to New is used when it implements
	// domain.ProminenceStore (otherwise nothing is extracted).
	Prominence domain.ProminenceStore
	// NoFrontItems disables the headline extraction.
	NoFrontItems bool
}

// Taker implements domain.ShotTaker.
type Taker struct {
	st   domain.Store
	opts Options
	log  *slog.Logger
}

var _ domain.ShotTaker = (*Taker)(nil)

// New builds a Taker.
func New(st domain.Store, opts Options, log *slog.Logger) *Taker {
	if log == nil {
		log = slog.Default()
	}
	if opts.Width <= 0 {
		opts.Width = 1366
	}
	if opts.Height <= 0 {
		opts.Height = 900
	}
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = 70
	}
	if opts.PageTimeout <= 0 {
		opts.PageTimeout = 60 * time.Second
	}
	if opts.SettleDelay < 0 {
		opts.SettleDelay = 0
	}
	if opts.MaxHeight <= 0 {
		opts.MaxHeight = 12000
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Taker{st: st, opts: opts, log: log.With("pkg", "shot")}
}

// DetectChrome looks for a local Chromium/Chrome/headless-shell binary.
func DetectChrome() string {
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
		"chrome", "chrome-headless-shell", "headless-shell"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	cands := []string{"/opt/pw-browsers/chromium", "/headless-shell/headless-shell"}
	for _, g := range []string{"/opt/pw-browsers/chromium-*/chrome-linux*/chrome",
		"/opt/pw-browsers/chromium_headless_shell-*/chrome-headless-shell-linux*/chrome-headless-shell"} {
		m, _ := filepath.Glob(g)
		cands = append(cands, m...)
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}

func (t *Taker) noSandbox() bool {
	if t.opts.NoSandbox != nil {
		return *t.opts.NoSandbox
	}
	if os.Geteuid() == 0 {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func (t *Taker) allocator(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if t.opts.RemoteURL != "" {
		c, cancel := remote.NewAllocator(ctx, t.opts.RemoteURL)
		return c, cancel, nil
	}
	path := t.opts.ChromePath
	if path == "" {
		path = DetectChrome()
	}
	if path == "" {
		return nil, nil, errors.New("no Chrome/Chromium binary found (set ChromePath or RemoteURL)")
	}
	flags := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(path),
		chromedp.Headless,
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("js-flags", "--max-old-space-size=256"),
		chromedp.Flag("blink-settings", "imagesEnabled=true"),
	}
	if t.noSandbox() {
		flags = append(flags, chromedp.NoSandbox)
	}
	for _, f := range t.opts.ExtraFlags {
		f = strings.TrimLeft(f, "-")
		if k, v, ok := strings.Cut(f, "="); ok {
			flags = append(flags, chromedp.Flag(k, v))
		} else if f != "" {
			flags = append(flags, chromedp.Flag(f, true))
		}
	}
	c, cancel := chromedp.NewExecAllocator(ctx, flags...)
	return c, cancel, nil
}

// RunOnce captures every enabled source with Screenshot=true, one at a time.
func (t *Taker) RunOnce(ctx context.Context) (int, error) {
	var todo []domain.Source
	for _, s := range t.opts.Sources {
		if s.IsEnabled() && s.Screenshot && s.Homepage != "" {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	allocCtx, allocCancel, err := t.allocator(ctx)
	if err != nil {
		return 0, err
	}
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	// Cancelling the contexts kills the local browser process (and, with the
	// exec allocator, waits for it); a remote browser is only disconnected.
	defer browserCancel()
	// The first Run starts the browser; it must not carry a timeout.
	stopStart := context.AfterFunc(ctx, browserCancel)
	err = chromedp.Do(browserCtx)
	stopStart()
	if err != nil {
		return 0, fmt.Errorf("start browser: %w", err)
	}

	saved := 0
	var errs []error
	for _, s := range todo {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := t.captureSource(ctx, browserCtx, s); err != nil {
			t.log.Error("screenshot failed", "source", s.ID, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", s.ID, err))
			continue
		}
		saved++
	}
	if saved == 0 && len(errs) > 0 {
		return 0, errors.Join(errs...)
	}
	return saved, nil
}

func (t *Taker) captureSource(ctx, browserCtx context.Context, s domain.Source) error {
	tabCtx, tabCancel := chromedp.NewContext(browserCtx)
	defer tabCancel() // closes the tab
	if err := chromedp.Do(tabCtx); err != nil {
		return fmt.Errorf("open tab: %w", err)
	}
	tctx, cancel := context.WithTimeout(tabCtx, t.opts.PageTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	o := t.opts
	if err := chromedp.Do(tctx,
		chromedp.EmulateViewport(int64(o.Width), int64(o.Height)),
		chromedp.Navigate(s.Homepage),
		chromedp.Sleep(o.SettleDelay),
	); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}

	t.dismissCookies(tctx, s)
	t.hideOverlays(tctx)
	t.scrollThrough(tctx)

	// Headlines are read from the same DOM state the screenshot shows
	// (overlays hidden, lazy content loaded). Failure never blocks the capture.
	var items []domain.FrontItem
	if ps := t.prominenceStore(); ps != nil {
		var xerr error
		if items, xerr = t.extractFrontItems(tctx, s.Homepage); xerr != nil {
			t.log.Warn("front-page extraction failed (screenshot kept)", "source", s.ID, "err", xerr)
			items = nil
		}
	}

	buf, w, h, err := t.capture(tctx)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	if len(buf) == 0 {
		return errors.New("empty screenshot")
	}
	id, err := t.store(ctx, s, buf, w, h)
	if err != nil {
		return err
	}
	if ps := t.prominenceStore(); ps != nil && len(items) > 0 && id > 0 {
		if err := ps.SaveFrontItems(ctx, id, items); err != nil {
			t.log.Warn("saving front-page items failed", "source", s.ID, "err", err)
		} else {
			t.log.Info("front-page items saved", "source", s.ID, "n", len(items), "top", items[0].Title)
		}
	}
	return nil
}

// prominenceStore returns where front-page items go, or nil when disabled.
func (t *Taker) prominenceStore() domain.ProminenceStore {
	if t.opts.NoFrontItems {
		return nil
	}
	if t.opts.Prominence != nil {
		return t.opts.Prominence
	}
	ps, _ := t.st.(domain.ProminenceStore)
	return ps
}

// capture takes a JPEG of the full content (capped by MaxHeight).
func (t *Taker) capture(ctx context.Context) (buf []byte, w, h int, err error) {
	o := t.opts
	m, err := chromedp.Call(ctx, page.GetLayoutMetrics, cdp.Empty{})
	if err != nil {
		return nil, 0, 0, err
	}
	w = o.Width
	h = o.Height
	if m.CSSContentSize != nil {
		h = int(math.Ceil(m.CSSContentSize.Height))
	}
	if h < o.Height {
		h = o.Height
	}
	if h > o.MaxHeight {
		h = o.MaxHeight
	}
	q := int64(o.Quality)
	yes := true
	r, err := chromedp.Call(ctx, page.CaptureScreenshot, page.CaptureScreenshotParams{
		Format:                page.CaptureScreenshotFormatJpeg,
		Quality:               &q,
		CaptureBeyondViewport: &yes,
		FromSurface:           &yes,
		Clip:                  &page.Viewport{X: 0, Y: 0, Width: float64(w), Height: float64(h), Scale: 1},
	})
	if err != nil {
		return nil, 0, 0, err
	}
	return r.Data, w, h, nil
}

func (t *Taker) store(ctx context.Context, s domain.Source, data []byte, w, h int) (int64, error) {
	now := t.opts.Now().Local()
	rel := fmt.Sprintf("%s/%04d/%02d/%02d/%02d%02d.jpg", s.ID, now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute())
	full := filepath.Join(t.opts.Dir, filepath.FromSlash(rel))
	if err := writeAtomic(full, data); err != nil {
		return 0, err
	}
	sh := &domain.Screenshot{SourceID: s.ID, TakenAt: now, Path: rel, Width: w, Height: h, Bytes: int64(len(data))}
	if err := t.st.SaveScreenshot(ctx, sh); err != nil {
		_ = os.Remove(full)
		return 0, fmt.Errorf("save record: %w", err)
	}
	t.log.Info("screenshot saved", "source", s.ID, "path", rel, "bytes", len(data), "h", h)
	return sh.ID, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.jpg")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = bytes.NewReader(data).WriteTo(tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o644)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// Cleanup removes screenshots older than Retention (DB rows and files).
func (t *Taker) Cleanup(ctx context.Context) (int, error) {
	if t.opts.Retention <= 0 {
		return 0, nil
	}
	paths, err := t.st.DeleteScreenshotsBefore(ctx, t.opts.Now().Add(-t.opts.Retention))
	if err != nil {
		return 0, err
	}
	root, err := filepath.Abs(t.opts.Dir)
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if rel, err := filepath.Rel(root, full); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			errs = append(errs, fmt.Errorf("refusing path outside shots dir: %q", p))
			continue
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		// prune empty parents up to (excluding) root
		for d := filepath.Dir(full); d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
			if os.Remove(d) != nil { // fails if not empty
				break
			}
		}
	}
	return len(paths), errors.Join(errs...)
}
