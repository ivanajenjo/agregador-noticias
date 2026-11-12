package domain

import (
	"context"
	"time"
)

// ArticleLite is a lightweight article projection for bulk text analysis
// (no full content: keeps memory low on a Raspberry Pi).
type ArticleLite struct {
	ID       int64
	SourceID string
	Title    string
	Summary  string
	URL      string
	At       time.Time // COALESCE(published_at, first_seen_at)
}

// TrendStore is what the trends engine needs from persistence.
// internal/store implements it.
type TrendStore interface {
	ListSources(ctx context.Context) ([]Source, error)
	// ArticleTexts returns articles with At in [from, to), oldest first.
	ArticleTexts(ctx context.Context, from, to time.Time) ([]ArticleLite, error)
}

// TrendOptions controls trend/story detection. Zero values get defaults.
type TrendOptions struct {
	Window     time.Duration // recent window (default 24h)
	Baseline   time.Duration // reference period before the window (default 14d)
	MinCount   int           // min articles in window (default 3)
	MinSources int           // min distinct outlets in window (default 2)
	Limit      int           // max results (default 30)
	SourceIDs  []string      // restrict to these outlets
	Now        time.Time     // zero => time.Now()
}

type ArticleRef struct {
	ID       int64
	SourceID string
	Title    string
	URL      string
	At       time.Time
}

// Term is a keyword/phrase/entity that is trending.
type Term struct {
	Term         string  // display form, e.g. "Ley de Vivienda"
	Key          string  // normalized key used in URLs/lookups
	Kind         string  // "word" | "phrase" | "entity"
	Count        int     // articles in window
	Sources      int     // distinct outlets in window
	BaselineRate float64 // avg articles/day in the baseline
	Score        float64 // burst score, higher = more unusual
	New          bool    // (almost) absent in the baseline
	FirstAt      time.Time
	Leanings     map[string]int // articles per leaning in window
	Spark        []int          // articles per day, oldest→newest, last 14 days (incl. today)
}

type TrendReport struct {
	GeneratedAt time.Time
	Window      time.Duration
	Baseline    time.Duration
	Articles    int // articles analysed in the window
	Terms       []Term
}

// SourceCoverage is how one outlet covered a term/story.
type SourceCoverage struct {
	SourceID  string
	Name      string
	Leaning   string
	Count     int
	FirstAt   time.Time
	Headlines []ArticleRef // newest first, capped
}

type DayCount struct {
	Day      time.Time
	Total    int
	Leanings map[string]int
}

type TermDetail struct {
	Term     string
	Key      string
	Total    int
	Coverage []SourceCoverage // covering outlets, earliest first
	Missing  []SourceCoverage // enabled outlets with no coverage (Count 0)
	Daily    []DayCount       // last N days, oldest→newest
	Related  []string         // co-occurring terms
}

// Story is a cluster of articles from several outlets about the same event.
type Story struct {
	ID       string // stable hash of the cluster seed
	Title    string // representative headline
	Terms    []string
	Sources  int
	Articles int
	Leanings map[string]int
	FirstAt  time.Time
	LastAt   time.Time
	FirstBy  string // source id that published first
	Score    float64
	Coverage []SourceCoverage // earliest first
	Missing  []SourceCoverage // enabled outlets that did not cover it
}

// TrendProvider is implemented by internal/trends and consumed by internal/web.
type TrendProvider interface {
	Trending(ctx context.Context, o TrendOptions) (*TrendReport, error)
	Term(ctx context.Context, key string, days int, o TrendOptions) (*TermDetail, error)
	Stories(ctx context.Context, o TrendOptions) ([]Story, error)
}
