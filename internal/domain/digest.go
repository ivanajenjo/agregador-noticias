package domain

import (
	"context"
	"errors"
	"time"
)

// ErrDigestNotFound is returned by DigestStore when no digest exists for a day.
var ErrDigestNotFound = errors.New("digest: not found")

// Digest is the daily summary ("Resumen diario"). Everything except Narrative
// fields is computed deterministically from stored headlines.
type Digest struct {
	Day         string    `json:"day"` // YYYY-MM-DD, local date the digest is about
	GeneratedAt time.Time `json:"generated_at"`
	WindowFrom  time.Time `json:"window_from"`
	WindowTo    time.Time `json:"window_to"`
	// Model is the chat model that wrote the narrative parts; empty when the
	// digest is purely deterministic.
	Model string `json:"model,omitempty"`

	Articles int `json:"articles"`
	Outlets  int `json:"outlets"`
	// Changed is the number of headlines modified in the window; nil when the
	// revisions feature is not available.
	Changed *int `json:"changed,omitempty"`

	Lead        string           `json:"lead"` // one-paragraph deterministic intro
	Stories     []DigestStory    `json:"stories,omitempty"`
	Rising      []DigestTerm     `json:"rising,omitempty"`
	Distinctive []DigestHeadline `json:"distinctive,omitempty"` // one per outlet
	Shots       []DigestShot     `json:"shots,omitempty"`
	// WhatChanged holds the (LLM) "lo que cambió hoy" bullets, empty without LLM.
	WhatChanged []string `json:"what_changed,omitempty"`
}

// DigestHeadline is a numbered, citable headline.
type DigestHeadline struct {
	N        int       `json:"n,omitempty"` // citation number within the digest (1-based), 0 if not citable
	SourceID string    `json:"source_id"`
	Outlet   string    `json:"outlet"`
	Leaning  string    `json:"leaning,omitempty"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	At       time.Time `json:"at"`
}

type DigestStory struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Sources   int              `json:"sources"`
	Articles  int              `json:"articles"`
	Leanings  map[string]int   `json:"leanings,omitempty"`
	FirstBy   string           `json:"first_by,omitempty"` // outlet name
	FirstAt   time.Time        `json:"first_at"`
	Missing   []string         `json:"missing,omitempty"` // outlet names
	Terms     []string         `json:"terms,omitempty"`
	Headlines []DigestHeadline `json:"headlines,omitempty"`
	Narrative string           `json:"narrative,omitempty"` // LLM text with [n] citations; empty if none
}

type DigestTerm struct {
	Term    string `json:"term"`
	Key     string `json:"key"`
	Count   int    `json:"count"`
	Sources int    `json:"sources"`
	New     bool   `json:"new,omitempty"`
}

type DigestShot struct {
	SourceID string    `json:"source_id"`
	Outlet   string    `json:"outlet"`
	TakenAt  time.Time `json:"taken_at"`
	Path     string    `json:"path"` // relative to the shots dir
}

// DigestStore persists digests (one per day, overwritten on regeneration).
// internal/store implements it.
type DigestStore interface {
	SaveDigest(ctx context.Context, d *Digest) error
	// GetDigest returns ErrDigestNotFound when the day has none.
	GetDigest(ctx context.Context, day string) (*Digest, error)
	LatestDigest(ctx context.Context) (*Digest, error) // ErrDigestNotFound when empty
	// ListDigests returns the newest digests first.
	ListDigests(ctx context.Context, limit int) ([]Digest, error)
}

// DigestGenerator is implemented by internal/digest and used by the web layer
// for "Generar ahora".
type DigestGenerator interface {
	// RunOnce builds and stores today's digest (job entry point).
	RunOnce(ctx context.Context) (string, error)
	// Build builds and stores the digest for the given local day.
	Build(ctx context.Context, day time.Time) (*Digest, error)
}

// RevisionCounter is an optional extension of Store/RevisionStore: how many
// headlines changed in [from, to). The digest uses it via type assertion.
type RevisionCounter interface {
	CountHeadlineChanges(ctx context.Context, from, to time.Time) (int, error)
}
