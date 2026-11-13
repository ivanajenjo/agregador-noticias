// Command agregador runs the news aggregator: scheduler, ingestion, screenshots,
// embedding worker, RAG and the web UI in a single process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/alerts"
	"github.com/ivanajenjo/agregador-noticias/internal/annotate"
	"github.com/ivanajenjo/agregador-noticias/internal/config"
	"github.com/ivanajenjo/agregador-noticias/internal/digest"
	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/ingest"
	"github.com/ivanajenjo/agregador-noticias/internal/llm"
	"github.com/ivanajenjo/agregador-noticias/internal/ops"
	"github.com/ivanajenjo/agregador-noticias/internal/rag"
	"github.com/ivanajenjo/agregador-noticias/internal/sched"
	"github.com/ivanajenjo/agregador-noticias/internal/shot"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
	"github.com/ivanajenjo/agregador-noticias/internal/web"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", envOr("AGREGADOR_CONFIG", "config/config.yaml"), "path to config.yaml (optional)")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	loc := cfg.Location()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shotsDir := filepath.Join(cfg.DataDir, "shots")
	if err := os.MkdirAll(shotsDir, 0o755); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "agregador.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	sources, err := config.LoadSources(cfg.SourcesFile)
	if err != nil {
		return err
	}
	if err := st.UpsertSources(ctx, sources); err != nil {
		return err
	}
	log.Info("sources loaded", "count", len(sources))

	// --- LLM side (optional: empty base_url disables it) ---
	var (
		emb   *llm.OpenAIEmbedder
		chat  *llm.OpenAIChat
		embed domain.Embedder
		chatI domain.Chat
	)
	if cfg.Embeddings.BaseURL != "" {
		emb = llm.NewEmbedder(llm.EmbedConfig{
			BaseURL: cfg.Embeddings.BaseURL, Model: cfg.Embeddings.Model, APIKey: cfg.Embeddings.APIKey,
			BatchSize: cfg.Embeddings.BatchSize, Timeout: cfg.Embeddings.Timeout, Normalize: true,
			QueryPrefix: cfg.Embeddings.QueryPrefix, DocPrefix: cfg.Embeddings.DocPrefix,
		})
		embed = emb
	}
	if cfg.Chat.BaseURL != "" {
		t := cfg.Chat.Temperature
		chat = llm.NewChat(llm.ChatConfig{
			BaseURL: cfg.Chat.BaseURL, Model: cfg.Chat.Model, APIKey: cfg.Chat.APIKey,
			Temperature: &t, MaxTokens: cfg.Chat.MaxTokens,
		})
		chatI = chat
	}
	status := llm.NewStatus(emb, chat, 15*time.Second, 3*time.Second)
	retriever := rag.New(st, embed, chatI, rag.Options{}, log)

	var worker *llm.EmbedWorker
	if emb != nil {
		worker = llm.NewEmbedWorker(st, emb, llm.WorkerOptions{Interval: cfg.Embeddings.Interval, Log: log})
		go worker.Run(ctx)
	} else {
		log.Warn("embeddings disabled (embeddings.base_url empty): search is text-only")
	}

	// --- jobs ---
	iopts := ingest.DefaultOptions()
	iopts.Sources = sources
	iopts.UserAgent = cfg.Ingest.UserAgent
	iopts.Timeout = cfg.Ingest.Timeout
	iopts.Concurrency = cfg.Ingest.Concurrency
	iopts.PerHostDelay = cfg.Ingest.PerHostDelay
	iopts.FetchFullText = cfg.Ingest.FetchFullText
	iopts.MaxFullTextPerFeed = cfg.Ingest.FullTextPerFeed
	iopts.TrackRevisions = cfg.Revisions.Enabled
	iopts.RespectRobots = cfg.Ingest.RespectRobots
	ing := ingest.New(st, iopts, log)

	sopts := shot.Options{
		Sources: sources, Dir: shotsDir, ChromePath: cfg.Screenshots.ChromePath, RemoteURL: cfg.Screenshots.RemoteURL,
		Width: cfg.Screenshots.Width, Height: cfg.Screenshots.Height, Quality: cfg.Screenshots.Quality,
		MaxHeight: cfg.Screenshots.MaxHeight, Retention: cfg.Screenshots.Retention, SettleDelay: 2 * time.Second,
	}
	sopts.NoSandbox = cfg.Screenshots.NoSandbox
	shots := shot.New(st, sopts, log)

	sc := sched.New(ctx, st, loc, log)
	ingestJob := sched.Job{Name: "ingest", Run: func(ctx context.Context) (string, error) {
		n, err := ing.RunOnce(ctx)
		if worker != nil {
			worker.Kick()
		}
		return fmt.Sprintf("%d nuevas", n), err
	}}
	shotJob := sched.Job{Name: "screenshots", Run: func(ctx context.Context) (string, error) {
		n, err := shots.RunOnce(ctx)
		return fmt.Sprintf("%d capturas", n), err
	}}
	cleanupJob := sched.Job{Name: "cleanup", Spec: "30 4 * * *", Run: func(ctx context.Context) (string, error) {
		n, err := shots.Cleanup(ctx)
		return fmt.Sprintf("%d capturas borradas", n), err
	}}
	if cfg.Ingest.Enabled {
		ingestJob.Spec = cfg.Ingest.Cron
	}
	if cfg.Screenshots.Enabled {
		if cfg.Screenshots.RemoteURL == "" && shot.DetectChrome() == "" && cfg.Screenshots.ChromePath == "" {
			log.Warn("screenshots disabled: no Chrome/Chromium found and screenshots.remote_url is empty")
		} else {
			shotJob.Spec = cfg.Screenshots.Cron
		}
	}
	for _, j := range []sched.Job{ingestJob, shotJob, cleanupJob} {
		if err := sc.Add(j); err != nil {
			return err
		}
	}
	sc.Start()
	if cfg.Ingest.Enabled {
		sc.Trigger(ingestJob) // first fetch right away
	}

	trendEngine := trends.New(st, trends.Options{})

	// --- annotations (tone/intent with a small model) ---
	annoModel := firstNonEmpty(cfg.Annotations.Model, cfg.Chat.Model)
	annoBase := firstNonEmpty(cfg.Annotations.BaseURL, cfg.Chat.BaseURL)
	annoKey := firstNonEmpty(cfg.Annotations.APIKey, cfg.Chat.APIKey)
	if cfg.Annotations.Enabled {
		if annoBase == "" || annoModel == "" {
			log.Warn("annotations enabled but no base_url/model (annotations.* or chat.*): disabled")
		} else {
			aw := annotate.NewWorker(st, annotate.NewClient(annoBase, annoKey, annoModel), annotate.WorkerOptions{
				Model: annoModel, BatchSize: cfg.Annotations.BatchSize, Interval: cfg.Annotations.Interval, Log: log,
			})
			go aw.Run(ctx)
		}
	}

	// --- alerts, digest, maintenance ---
	alertEng := alerts.New(alerts.Deps{Store: st, Follow: st, Trends: trendEngine, Log: log}, alerts.Options{
		Window: cfg.Alerts.Window, MinSources: cfg.Alerts.MinSources, MaxPerHour: cfg.Alerts.MaxPerHour,
		QuietStart: cfg.Alerts.QuietStart, QuietEnd: cfg.Alerts.QuietEnd, Location: loc, PublicURL: cfg.Alerts.PublicURL,
		NtfyURL: cfg.Alerts.NtfyURL, NtfyTopic: cfg.Alerts.NtfyTopic,
		TelegramBot: cfg.Alerts.TelegramBot, TelegramChat: cfg.Alerts.TelegramChat, WebhookURL: cfg.Alerts.WebhookURL,
	})
	alertsJob := sched.Job{Name: "alerts", Run: alertEng.RunOnce}
	if cfg.Alerts.Enabled && cfg.Alerts.Interval > 0 {
		alertsJob.Spec = "@every " + cfg.Alerts.Interval.String()
	}
	digestBuilder := digest.New(digest.Deps{Store: st, Trends: trendEngine, Digests: st, Chat: chatI, Revisions: st, Log: log},
		digest.Options{UseLLM: cfg.Digest.UseLLM, Location: loc})
	digestJob := sched.Job{Name: "digest", Run: digestBuilder.RunOnce}
	if cfg.Digest.Enabled {
		digestJob.Spec = cfg.Digest.Cron
	}
	maint := &ops.Maintenance{Store: st, Days: cfg.Retention.ArticlesDays, Log: log}
	maintJob := sched.Job{Name: "maintenance", Spec: "45 4 * * *", Run: maint.RunOnce}
	for _, j := range []sched.Job{alertsJob, digestJob, maintJob} {
		if err := sc.Add(j); err != nil {
			return err
		}
	}

	// --- web ---
	handler := web.New(web.Deps{
		Store: st, Retriever: retriever, Ingestor: ing, Shots: shots, LLM: status,
		Trends: trendEngine, ShotsDir: shotsDir, Log: log,
		Follow: st, Alerts: alertEng, Digests: st, DigestGen: digestBuilder, Prominence: st,
		Maintenance: maint, Heartbeat: sc,
		Annotations: st, AnnotationModel: annoModel, AnnotationsEnabled: cfg.Annotations.Enabled,
	}, web.Options{BasicAuthUser: cfg.Auth.User, BasicAuthPass: cfg.Auth.Pass, Location: loc, Version: version, EmbedModel: cfg.Embeddings.Model, PublicURL: cfg.Alerts.PublicURL})
	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "version", version)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	sc.Stop()
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}
