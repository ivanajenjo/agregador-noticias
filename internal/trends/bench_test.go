package trends

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// bigStore builds n synthetic Spanish-like articles over 15 days and 25 outlets.
func bigStore(n int) *fakeStore {
	f := &fakeStore{}
	for i := 0; i < 25; i++ {
		f.sources = append(f.sources, domain.Source{ID: fmt.Sprintf("s%02d", i), Name: fmt.Sprintf("Medio %d", i), Leaning: []string{"izquierda", "centro", "derecha"}[i%3]})
	}
	rng := rand.New(rand.NewSource(3))
	syl := []string{"ba", "ce", "di", "fo", "gu", "ha", "ji", "ko", "lu", "ma", "ne", "po", "qui", "ra", "su", "to", "vu", "xe", "yo", "zi", "cion", "ment"}
	vocab := make([]string, 12000)
	for i := range vocab {
		vocab[i] = syl[rng.Intn(len(syl))] + syl[rng.Intn(len(syl))] + syl[rng.Intn(len(syl))]
	}
	zipf := rand.NewZipf(rng, 1.07, 20, uint64(len(vocab)-1))
	glue := []string{"de", "la", "el", "en", "los", "por", "con", "para", "tras", "ante", "que", "una"}
	capw := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	f.arts = make([]domain.ArticleLite, 0, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		words := 7 + rng.Intn(8)
		for w := 0; w < words; w++ {
			word := vocab[zipf.Uint64()]
			switch {
			case w == 0 || rng.Intn(6) == 0:
				word = capw(word)
			}
			if w > 0 {
				b.WriteByte(' ')
				if rng.Intn(3) == 0 {
					b.WriteString(glue[rng.Intn(len(glue))])
					b.WriteByte(' ')
				}
			}
			b.WriteString(word)
		}
		var sum strings.Builder
		for w := 0; w < 28; w++ {
			if w > 0 {
				sum.WriteByte(' ')
			}
			sum.WriteString(vocab[zipf.Uint64()])
			if w%9 == 8 {
				sum.WriteByte('.')
			}
		}
		at := now.Add(-time.Duration(rng.Int63n(int64(15 * 24 * time.Hour))))
		f.add(f.sources[rng.Intn(25)].ID, b.String(), sum.String(), at)
	}
	return f
}

func BenchmarkCold30k(b *testing.B) {
	f := bigStore(30000)
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := New(f, Options{})
		if _, err := e.Trending(ctx, domain.TrendOptions{Now: now}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkColdAll30k(b *testing.B) {
	f := bigStore(30000)
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := New(f, Options{})
		o := domain.TrendOptions{Now: now}
		if _, err := e.Trending(ctx, o); err != nil {
			b.Fatal(err)
		}
		if _, err := e.Stories(ctx, o); err != nil {
			b.Fatal(err)
		}
		if _, err := e.Term(ctx, "kolukon", 7, o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWarmTrending30k(b *testing.B) {
	f := bigStore(30000)
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	e := New(f, Options{})
	e.Trending(ctx, domain.TrendOptions{Now: now}) // builds the corpus
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// different Limit => different result key, same corpus
		if _, err := e.Trending(ctx, domain.TrendOptions{Now: now, Limit: 10 + i%1000}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoriesWarm30k(b *testing.B) {
	f := bigStore(30000)
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	e := New(f, Options{})
	e.Trending(ctx, domain.TrendOptions{Now: now})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Stories(ctx, domain.TrendOptions{Now: now, Limit: 10 + i%1000}); err != nil {
			b.Fatal(err)
		}
	}
}
