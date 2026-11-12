package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestDigestStore(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.LatestDigest(ctx); !errors.Is(err, domain.ErrDigestNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := s.SaveDigest(ctx, &domain.Digest{Day: "bad"}); err == nil {
		t.Fatal("invalid day accepted")
	}
	at := time.Unix(1700000000, 0)
	d1 := &domain.Digest{Day: "2026-11-11", GeneratedAt: at, Model: "m1", Articles: 5, Lead: "uno"}
	d2 := &domain.Digest{Day: "2026-11-12", GeneratedAt: at, Articles: 7, Stories: []domain.DigestStory{{Title: "Ñ <b>"}}}
	for _, d := range []*domain.Digest{d1, d2} {
		if err := s.SaveDigest(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	d2b := &domain.Digest{Day: "2026-11-12", GeneratedAt: at.Add(time.Hour), Articles: 9}
	if err := s.SaveDigest(ctx, d2b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDigest(ctx, "2026-11-12")
	if err != nil || got.Articles != 9 || len(got.Stories) != 0 {
		t.Fatalf("overwrite failed: %+v %v", got, err)
	}
	l, _ := s.LatestDigest(ctx)
	if l.Day != "2026-11-12" {
		t.Fatalf("latest %s", l.Day)
	}
	list, err := s.ListDigests(ctx, 10)
	if err != nil || len(list) != 2 || list[0].Day != "2026-11-12" || list[1].Model != "m1" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := s.GetDigest(ctx, "2020-01-01"); !errors.Is(err, domain.ErrDigestNotFound) {
		t.Fatal(err)
	}
}
