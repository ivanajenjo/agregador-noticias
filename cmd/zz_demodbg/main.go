package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/trends"
)

func main() {
	st, _ := store.Open(os.Args[1] + "/agregador.db")
	e := trends.New(st, trends.Options{})
	ctx := context.Background()
	ss, err := e.Stories(ctx, domain.TrendOptions{})
	fmt.Println("stories", len(ss), err)
	for _, s := range ss {
		if len(os.Args) > 2 {
			as, _ := e.StoryArticles(ctx, s.ID, domain.TrendOptions{})
			for _, a := range as {
				fmt.Printf("      - %s | %s\n", a.SourceID, a.Title)
			}
		}
		fmt.Printf("%d src %d arts %v | %s | by %s missing %d\n", int(s.Score), s.Sources, s.Articles, s.Title, s.FirstBy, len(s.Missing))
	}
	r, _ := e.Trending(ctx, domain.TrendOptions{})
	for i, t := range r.Terms {
		if i > 24 {
			break
		}
		fmt.Printf("term %-28s cnt %d src %d new %v score %.1f base %.2f\n", t.Term, t.Count, t.Sources, t.New, t.Score, t.BaselineRate)
	}
}
