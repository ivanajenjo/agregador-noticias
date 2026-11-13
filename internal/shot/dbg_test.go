package shot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestDbg(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(frontFixture))
	}))
	defer s.Close()
	tk := New(&fakeStore{}, Options{}, quiet())
	a, c, _ := tk.allocator(context.Background())
	defer c()
	ctx, c2 := chromedp.NewContext(a)
	defer c2()
	chromedp.Run(ctx, chromedp.Navigate(s.URL))
	js, err := chromedp.Run(ctx, chromedp.Evaluate[string](fmt.Sprintf(extractJS, 4000)))
	fmt.Println(js, err)
}
