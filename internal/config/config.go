// Package config loads the YAML configuration with AGREGADOR_* env overrides.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type Config struct {
	Listen      string `yaml:"listen"`   // ":8080"
	DataDir     string `yaml:"data_dir"` // db + shots
	SourcesFile string `yaml:"sources_file"`
	Timezone    string `yaml:"timezone"`
	LogLevel    string `yaml:"log_level"` // debug|info|warn|error
	Auth        struct {
		User string `yaml:"user"`
		Pass string `yaml:"pass"`
	} `yaml:"auth"`

	Ingest struct {
		Enabled         bool          `yaml:"enabled"`
		Cron            string        `yaml:"cron"` // robfig spec, TZ applied
		Timeout         time.Duration `yaml:"timeout"`
		Concurrency     int           `yaml:"concurrency"`
		PerHostDelay    time.Duration `yaml:"per_host_delay"`
		FetchFullText   bool          `yaml:"fetch_full_text"`
		FullTextPerFeed int           `yaml:"full_text_per_feed"` // cap of page fetches per feed and run
		RespectRobots   bool          `yaml:"respect_robots"`
		UserAgent       string        `yaml:"user_agent"`
	} `yaml:"ingest"`

	Screenshots struct {
		Enabled    bool          `yaml:"enabled"`
		Cron       string        `yaml:"cron"`
		ChromePath string        `yaml:"chrome_path"`
		RemoteURL  string        `yaml:"remote_url"` // CDP endpoint of a remote Chromium
		Width      int           `yaml:"width"`
		Height     int           `yaml:"height"`
		Quality    int           `yaml:"quality"`
		MaxHeight  int           `yaml:"max_height"`
		Retention  time.Duration `yaml:"retention"`
		NoSandbox  *bool         `yaml:"no_sandbox"` // nil = automatic
	} `yaml:"screenshots"`

	Embeddings struct {
		BaseURL     string        `yaml:"base_url"` // e.g. http://macbook.local:11434/v1
		Model       string        `yaml:"model"`
		APIKey      string        `yaml:"api_key"`
		BatchSize   int           `yaml:"batch_size"`
		QueryPrefix string        `yaml:"query_prefix"`
		DocPrefix   string        `yaml:"doc_prefix"`
		Interval    time.Duration `yaml:"interval"`
		Timeout     time.Duration `yaml:"timeout"`
	} `yaml:"embeddings"`

	Retention struct {
		ArticlesDays int `yaml:"articles_days"` // 0 = keep forever
	} `yaml:"retention"`

	Alerts struct {
		Enabled      bool          `yaml:"enabled"`
		Interval     time.Duration `yaml:"interval"`    // evaluation period
		MinSources   int           `yaml:"min_sources"` // outlets covering a story to alert
		Window       time.Duration `yaml:"window"`
		NtfyURL      string        `yaml:"ntfy_url"` // e.g. https://ntfy.sh or self-hosted
		NtfyTopic    string        `yaml:"ntfy_topic"`
		TelegramBot  string        `yaml:"telegram_bot_token"`
		TelegramChat string        `yaml:"telegram_chat_id"`
		WebhookURL   string        `yaml:"webhook_url"`
		PublicURL    string        `yaml:"public_url"` // base URL used in alert links
	} `yaml:"alerts"`

	Digest struct {
		Enabled bool   `yaml:"enabled"`
		Cron    string `yaml:"cron"`    // daily generation time
		UseLLM  bool   `yaml:"use_llm"` // narrative with the chat model when reachable
	} `yaml:"digest"`

	Revisions struct {
		Enabled bool `yaml:"enabled"` // track headline/summary changes of known articles
	} `yaml:"revisions"`

	Annotations struct {
		Enabled   bool          `yaml:"enabled"`
		BaseURL   string        `yaml:"base_url"` // empty => reuse chat.base_url
		Model     string        `yaml:"model"`    // small model, empty => chat.model
		APIKey    string        `yaml:"api_key"`
		Interval  time.Duration `yaml:"interval"`
		BatchSize int           `yaml:"batch_size"`
	} `yaml:"annotations"`

	Chat struct {
		BaseURL     string        `yaml:"base_url"`
		Model       string        `yaml:"model"`
		APIKey      string        `yaml:"api_key"`
		Temperature float64       `yaml:"temperature"`
		MaxTokens   int           `yaml:"max_tokens"`
		Timeout     time.Duration `yaml:"timeout"`
	} `yaml:"chat"`
}

// Default returns the configuration used when no file/env is provided.
func Default() Config {
	var c Config
	c.Listen = ":8080"
	c.DataDir = "data"
	c.SourcesFile = "config/sources.yaml"
	c.Timezone = "Europe/Madrid"
	c.LogLevel = "info"
	c.Ingest.Enabled = true
	c.Ingest.Cron = "*/30 * * * *"
	c.Ingest.Timeout = 20 * time.Second
	c.Ingest.Concurrency = 6
	c.Ingest.PerHostDelay = time.Second
	c.Ingest.FullTextPerFeed = 12
	c.Ingest.FetchFullText = true
	c.Ingest.RespectRobots = true
	c.Ingest.UserAgent = "agregador-noticias/2.0 (personal news aggregator)"
	c.Screenshots.Enabled = true
	c.Screenshots.Cron = "0 9,17,22 * * *"
	c.Screenshots.Width = 1366
	c.Screenshots.Height = 900
	c.Screenshots.Quality = 70
	c.Screenshots.MaxHeight = 12000
	c.Screenshots.Retention = 90 * 24 * time.Hour
	c.Embeddings.BatchSize = 16
	c.Embeddings.Interval = time.Minute
	c.Embeddings.Timeout = 60 * time.Second
	c.Retention.ArticlesDays = 0
	c.Alerts.Interval = 5 * time.Minute
	c.Alerts.MinSources = 4
	c.Alerts.Window = 2 * time.Hour
	c.Digest.Enabled = true
	c.Digest.Cron = "0 8 * * *"
	c.Digest.UseLLM = true
	c.Revisions.Enabled = true
	c.Annotations.Interval = 2 * time.Minute
	c.Annotations.BatchSize = 8
	c.Chat.Temperature = 0.2
	c.Chat.Timeout = 5 * time.Minute
	return c
}

// Load reads path (optional: empty or missing file => defaults) and applies env overrides.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("config %s: %w", path, err)
			}
		case !os.IsNotExist(err):
			return c, err
		}
	}
	applyEnv(&c, os.Getenv)
	return c, c.Validate()
}

func applyEnv(c *Config, get func(string) string) {
	str := func(key string, dst *string) {
		if v := get("AGREGADOR_" + key); v != "" {
			*dst = v
		}
	}
	num := func(key string, dst *int) {
		if v, err := strconv.Atoi(get("AGREGADOR_" + key)); err == nil {
			*dst = v
		}
	}
	dur := func(key string, dst *time.Duration) {
		if v, err := time.ParseDuration(get("AGREGADOR_" + key)); err == nil {
			*dst = v
		}
	}
	boolean := func(key string, dst *bool) {
		if v, err := strconv.ParseBool(get("AGREGADOR_" + key)); err == nil {
			*dst = v
		}
	}
	flt := func(key string, dst *float64) {
		if v, err := strconv.ParseFloat(get("AGREGADOR_"+key), 64); err == nil {
			*dst = v
		}
	}
	str("LISTEN", &c.Listen)
	str("DATA_DIR", &c.DataDir)
	str("SOURCES_FILE", &c.SourcesFile)
	str("TIMEZONE", &c.Timezone)
	str("LOG_LEVEL", &c.LogLevel)
	str("AUTH_USER", &c.Auth.User)
	str("AUTH_PASS", &c.Auth.Pass)
	boolean("INGEST_ENABLED", &c.Ingest.Enabled)
	str("INGEST_CRON", &c.Ingest.Cron)
	boolean("SCREENSHOTS_ENABLED", &c.Screenshots.Enabled)
	str("SCREENSHOTS_CRON", &c.Screenshots.Cron)
	str("SCREENSHOTS_CHROME_PATH", &c.Screenshots.ChromePath)
	str("SCREENSHOTS_REMOTE_URL", &c.Screenshots.RemoteURL)
	if v, err := strconv.ParseBool(get("AGREGADOR_SCREENSHOTS_NO_SANDBOX")); err == nil {
		c.Screenshots.NoSandbox = &v
	}
	num("SCREENSHOTS_WIDTH", &c.Screenshots.Width)
	num("SCREENSHOTS_HEIGHT", &c.Screenshots.Height)
	num("SCREENSHOTS_MAX_HEIGHT", &c.Screenshots.MaxHeight)
	num("INGEST_CONCURRENCY", &c.Ingest.Concurrency)
	num("INGEST_FULL_TEXT_PER_FEED", &c.Ingest.FullTextPerFeed)
	dur("INGEST_TIMEOUT", &c.Ingest.Timeout)
	dur("INGEST_PER_HOST_DELAY", &c.Ingest.PerHostDelay)
	boolean("INGEST_FETCH_FULL_TEXT", &c.Ingest.FetchFullText)
	boolean("INGEST_RESPECT_ROBOTS", &c.Ingest.RespectRobots)
	str("INGEST_USER_AGENT", &c.Ingest.UserAgent)
	num("EMBEDDINGS_BATCH_SIZE", &c.Embeddings.BatchSize)
	dur("EMBEDDINGS_INTERVAL", &c.Embeddings.Interval)
	flt("CHAT_TEMPERATURE", &c.Chat.Temperature)
	num("CHAT_MAX_TOKENS", &c.Chat.MaxTokens)
	dur("SCREENSHOTS_RETENTION", &c.Screenshots.Retention)
	num("SCREENSHOTS_QUALITY", &c.Screenshots.Quality)
	str("EMBEDDINGS_BASE_URL", &c.Embeddings.BaseURL)
	str("EMBEDDINGS_MODEL", &c.Embeddings.Model)
	str("EMBEDDINGS_API_KEY", &c.Embeddings.APIKey)
	str("EMBEDDINGS_QUERY_PREFIX", &c.Embeddings.QueryPrefix)
	str("EMBEDDINGS_DOC_PREFIX", &c.Embeddings.DocPrefix)
	num("RETENTION_ARTICLES_DAYS", &c.Retention.ArticlesDays)
	boolean("ALERTS_ENABLED", &c.Alerts.Enabled)
	num("ALERTS_MIN_SOURCES", &c.Alerts.MinSources)
	str("ALERTS_NTFY_URL", &c.Alerts.NtfyURL)
	str("ALERTS_NTFY_TOPIC", &c.Alerts.NtfyTopic)
	str("ALERTS_TELEGRAM_BOT_TOKEN", &c.Alerts.TelegramBot)
	str("ALERTS_TELEGRAM_CHAT_ID", &c.Alerts.TelegramChat)
	str("ALERTS_WEBHOOK_URL", &c.Alerts.WebhookURL)
	str("ALERTS_PUBLIC_URL", &c.Alerts.PublicURL)
	boolean("DIGEST_ENABLED", &c.Digest.Enabled)
	str("DIGEST_CRON", &c.Digest.Cron)
	boolean("DIGEST_USE_LLM", &c.Digest.UseLLM)
	boolean("REVISIONS_ENABLED", &c.Revisions.Enabled)
	boolean("ANNOTATIONS_ENABLED", &c.Annotations.Enabled)
	str("ANNOTATIONS_BASE_URL", &c.Annotations.BaseURL)
	str("ANNOTATIONS_MODEL", &c.Annotations.Model)
	str("ANNOTATIONS_API_KEY", &c.Annotations.APIKey)
	str("CHAT_BASE_URL", &c.Chat.BaseURL)
	str("CHAT_MODEL", &c.Chat.Model)
	str("CHAT_API_KEY", &c.Chat.APIKey)
}

func (c Config) Validate() error {
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone %q: %w", c.Timezone, err)
	}
	if c.Embeddings.BaseURL != "" && c.Embeddings.Model == "" {
		return fmt.Errorf("embeddings.model is required when embeddings.base_url is set")
	}
	if c.Chat.BaseURL != "" && c.Chat.Model == "" {
		return fmt.Errorf("chat.model is required when chat.base_url is set")
	}
	if c.Screenshots.Quality < 1 || c.Screenshots.Quality > 100 {
		return fmt.Errorf("screenshots.quality must be 1..100")
	}
	return nil
}

func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

type sourcesFile struct {
	Sources []domain.Source `yaml:"sources"`
}

// LoadSources reads the sources YAML file.
func LoadSources(path string) ([]domain.Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f sourcesFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("sources %s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, s := range f.Sources {
		if s.ID == "" || strings.ContainsAny(s.ID, " /\\") {
			return nil, fmt.Errorf("sources: invalid id %q", s.ID)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("sources: duplicate id %q", s.ID)
		}
		seen[s.ID] = true
	}
	return f.Sources, nil
}
