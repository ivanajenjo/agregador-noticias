package store

import (
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestArticleTexts(t *testing.T) {
	s := newStore(t)
	long := strings.Repeat("ñ", 1000)
	ins(t, s, domain.Article{SourceID: "a", URL: "http://a/1", CanonicalURL: "http://a/1", Title: "Uno", Summary: long, PublishedAt: day(1), FirstSeenAt: day(5)})
	ins(t, s, domain.Article{SourceID: "b", URL: "http://b/2", CanonicalURL: "http://b/2", Title: "Dos", FirstSeenAt: day(2)}) // no published_at
	ins(t, s, domain.Article{SourceID: "c", URL: "http://c/3", CanonicalURL: "http://c/3", Title: "Tres", PublishedAt: day(3), FirstSeenAt: day(3)})

	got, err := s.ArticleTexts(ctx, day(1), day(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Uno" || got[1].Title != "Dos" {
		t.Fatalf("%+v", got)
	}
	if n := len([]rune(got[0].Summary)); n != 400 {
		t.Fatalf("summary len %d", n)
	}
	if !got[0].At.Equal(day(1)) || got[0].At.Location() != time.UTC || got[1].At != day(2) {
		t.Fatalf("times %v %v", got[0].At, got[1].At)
	}
	if got[0].URL != "http://a/1" || got[0].SourceID != "a" || got[0].ID == 0 {
		t.Fatalf("%+v", got[0])
	}
	all, _ := s.ArticleTexts(ctx, day(1), day(4))
	if len(all) != 3 {
		t.Fatal(len(all))
	}
	none, err := s.ArticleTexts(ctx, day(10), day(11))
	if err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
}
