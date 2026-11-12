// Package domain defines the shared types and the interfaces (contracts)
// between the internal packages. Other packages depend only on this one.
package domain

import (
	"context"
	"time"
)

// Source is a news outlet defined in config/sources.yaml.
type Source struct {
	ID              string   `yaml:"id"`      // stable slug, e.g. "elpais"
	Name            string   `yaml:"name"`    // display name
	Leaning         string   `yaml:"leaning"` // user-editable label, e.g. "centro-izquierda"
	Homepage        string   `yaml:"homepage"`
	Feeds           []Feed   `yaml:"feeds"`
	FullText        bool     `yaml:"full_text"`        // fetch article page + readability when feed lacks body
	Screenshot      bool     `yaml:"screenshot"`       // take front-page screenshots
	CookieSelectors []string `yaml:"cookie_selectors"` // CSS selectors of the consent "accept" button
	Enabled         *bool    `yaml:"enabled"`          // nil => true
}

func (s Source) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

type Feed struct {
	URL     string `yaml:"url"`
	Section string `yaml:"section"`
}

// Article is a stored news item.
type Article struct {
	ID           int64
	SourceID     string
	URL          string
	CanonicalURL string // normalized URL used for dedup
	Title        string
	Summary      string
	Content      string // plain text, may be empty
	Author       string
	Section      string
	PublishedAt  time.Time // zero if unknown
	FirstSeenAt  time.Time
}

// Text returns the text that should be indexed/embedded.
func (a Article) Text() string {
	t := a.Title + "\n\n"
	if a.Content != "" {
		return t + a.Content
	}
	return t + a.Summary
}

type Screenshot struct {
	ID       int64
	SourceID string
	TakenAt  time.Time
	Path     string // relative to the shots dir, slash-separated
	Width    int
	Height   int
	Bytes    int64
}

// Filter restricts searches/listings. Zero values mean "no restriction".
type Filter struct {
	SourceIDs []string
	Leanings  []string
	From, To  time.Time // on COALESCE(published_at, first_seen_at)
	Limit     int
	Offset    int
}

type Hit struct {
	Article Article
	Score   float64 // higher is better; scale depends on the method
	Chunk   int     // chunk index for vector hits, -1 otherwise
	Snippet string  // highlighted/short text if available
}

// FeedState is the conditional-GET + health state for a feed.
type FeedState struct {
	SourceID     string
	FeedURL      string
	ETag         string
	LastModified string
	LastOK       time.Time
	LastError    string
	LastErrorAt  time.Time
	LastCount    int
}

// JobRun records an execution of a background job.
type JobRun struct {
	ID         int64
	Job        string // ingest | screenshots | embed | cleanup
	StartedAt  time.Time
	FinishedAt time.Time
	OK         bool
	Message    string
}

// Stats is a summary used by the status page.
type Stats struct {
	Articles         int64
	ArticlesBySource map[string]int64
	Embedded         int64 // articles with all chunks embedded for the current model
	PendingEmbed     int64
	Screenshots      int64
	DBBytes          int64
	ShotsBytes       int64
	OldestArticle    time.Time
	NewestArticle    time.Time
}

// Store is the persistence contract (implemented by internal/store).
type Store interface {
	UpsertSources(ctx context.Context, src []Source) error
	ListSources(ctx context.Context) ([]Source, error)

	// InsertArticle inserts if new (by CanonicalURL or by (source,title hash)).
	// It returns the stored article and whether it was newly inserted.
	InsertArticle(ctx context.Context, a *Article) (inserted bool, err error)
	GetArticle(ctx context.Context, id int64) (*Article, error)
	ListArticles(ctx context.Context, f Filter) ([]Article, error)
	CountArticles(ctx context.Context, f Filter) (int64, error)

	// Embeddings. A vector belongs to (article, chunk, model).
	ArticlesNeedingEmbedding(ctx context.Context, model string, limit int) ([]Article, error)
	SaveEmbeddings(ctx context.Context, articleID int64, model string, chunks []ChunkVec) error
	// VectorIndex search (brute force cosine); filter applies to articles.
	SearchVector(ctx context.Context, query []float32, model string, f Filter, k int) ([]Hit, error)
	// SearchText uses FTS5/BM25. Query is free user text (must be sanitized by the store).
	SearchText(ctx context.Context, query string, f Filter, k int) ([]Hit, error)

	SaveScreenshot(ctx context.Context, s *Screenshot) error
	ListScreenshots(ctx context.Context, sourceID string, from, to time.Time) ([]Screenshot, error)
	// DeleteScreenshotsBefore removes rows and returns their paths so the caller can delete files.
	DeleteScreenshotsBefore(ctx context.Context, t time.Time) ([]string, error)

	GetFeedState(ctx context.Context, sourceID, feedURL string) (*FeedState, error)
	SaveFeedState(ctx context.Context, s *FeedState) error
	ListFeedStates(ctx context.Context) ([]FeedState, error)

	StartJob(ctx context.Context, job string) (int64, error)
	FinishJob(ctx context.Context, id int64, ok bool, msg string) error
	ListJobRuns(ctx context.Context, limit int) ([]JobRun, error)

	Stats(ctx context.Context, model string) (*Stats, error)
	Close() error
}

// ChunkVec is one embedded chunk of an article.
type ChunkVec struct {
	Index int
	Text  string
	Vec   []float32
}

// Embedder turns texts into vectors (implemented by internal/llm).
type Embedder interface {
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type ChatMessage struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Chat streams a completion; onDelta is called for each text fragment.
type Chat interface {
	Model() string
	Stream(ctx context.Context, msgs []ChatMessage, onDelta func(string) error) error
}

// Health describes reachability of the remote model server.
type Health struct {
	OK        bool
	Detail    string
	CheckedAt time.Time
}

// Retriever is implemented by internal/rag and consumed by internal/web.
type Retriever interface {
	// Search runs hybrid (or text-only fallback) search.
	Search(ctx context.Context, query string, f Filter, k int) ([]Hit, SearchMode, error)
	// Ask streams an answer; sources are sent first through onSources.
	Ask(ctx context.Context, req AskRequest, onSources func([]Hit), onDelta func(string) error) error
}

type SearchMode string

const (
	ModeHybrid SearchMode = "hybrid"
	ModeText   SearchMode = "text" // embeddings unavailable
)

type AskRequest struct {
	Question string
	Filter   Filter
	Compare  bool // retrieve per leaning/source and ask for a contrast
	K        int
}

// Ingestor and ShotTaker are the job entry points used by sched/web.
type Ingestor interface {
	// RunOnce fetches all enabled sources. Returns number of new articles.
	RunOnce(ctx context.Context) (int, error)
}

type ShotTaker interface {
	// RunOnce screenshots all enabled sources with Screenshot=true. Returns number saved.
	RunOnce(ctx context.Context) (int, error)
	// Cleanup deletes screenshots older than the retention window.
	Cleanup(ctx context.Context) (int, error)
}

// StatusProvider is exposed for the status page.
type StatusProvider interface {
	LLMHealth(ctx context.Context) (chat Health, embed Health)
}
