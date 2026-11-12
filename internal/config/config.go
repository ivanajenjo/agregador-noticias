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
		Enabled       bool          `yaml:"enabled"`
		Cron          string        `yaml:"cron"` // robfig spec, TZ applied
		Timeout       time.Duration `yaml:"timeout"`
		Concurrency   int           `yaml:"concurrency"`
		PerHostDelay  time.Duration `yaml:"per_host_delay"`
		FetchFullText bool          `yaml:"fetch_full_text"`
		RespectRobots bool          `yaml:"respect_robots"`
		UserAgent     string        `yaml:"user_agent"`
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
		NoSandbox  bool          `yaml:"no_sandbox"`
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
	c.Ingest.Concurrency = 3
	c.Ingest.PerHostDelay = 2 * time.Second
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
	boolean("SCREENSHOTS_NO_SANDBOX", &c.Screenshots.NoSandbox)
	dur("SCREENSHOTS_RETENTION", &c.Screenshots.Retention)
	num("SCREENSHOTS_QUALITY", &c.Screenshots.Quality)
	str("EMBEDDINGS_BASE_URL", &c.Embeddings.BaseURL)
	str("EMBEDDINGS_MODEL", &c.Embeddings.Model)
	str("EMBEDDINGS_API_KEY", &c.Embeddings.APIKey)
	str("EMBEDDINGS_QUERY_PREFIX", &c.Embeddings.QueryPrefix)
	str("EMBEDDINGS_DOC_PREFIX", &c.Embeddings.DocPrefix)
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
