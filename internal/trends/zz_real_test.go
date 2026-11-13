package trends

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
)

func TestRealData(t *testing.T) {
	p := os.Getenv("REAL_DB")
	if p == "" {
		t.Skip()
	}
	st, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	e := New(st, Options{})
	for _, w := range []time.Duration{24 * time.Hour, 48 * time.Hour} {
		s, err := e.Stories(context.Background(), domain.TrendOptions{Window: w, Limit: 100000})
		if err != nil {
			t.Fatal(err)
		}
		var sz []int
		dom, tot := 0, 0
		for _, x := range s {
			sz = append(sz, x.Articles)
			tot += x.Articles
			m := 0
			for _, c := range x.Coverage {
				if c.Count > m {
					m = c.Count
				}
			}
			if float64(m) > 0.5*float64(x.Articles) {
				dom++
			}
		}
		sort.Ints(sz)
		n := len(sz)
		b := map[string]int{}
		for _, x := range sz {
			k := ">40"
			switch {
			case x <= 5:
				k = "1-5"
			case x <= 10:
				k = "6-10"
			case x <= 20:
				k = "11-20"
			case x <= 40:
				k = "21-40"
			}
			b[k]++
		}
		fmt.Printf("window %v: stories=%d articles=%d med=%d p90=%d max=%d dominated=%d buckets=%v\n", w, n, tot, sz[n/2], sz[n*9/10], sz[n-1], dom, b)
		if w == 24*time.Hour {
			for i, x := range s {
				if i < 100 && (x.Articles >= 25 || i < 12) {
					fmt.Printf("  %3d art %2d src  %s | %v\n", x.Articles, x.Sources, trunc(x.Title, 80), x.Terms)
				}
			}
		}
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
