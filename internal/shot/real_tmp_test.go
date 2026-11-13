package shot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestRealTmp(t *testing.T) {
	u := os.Getenv("REAL_URL")
	if u == "" {
		t.Skip()
	}
	st := &promStore{}
	tk := New(st, Options{Sources: []domain.Source{{ID: "x", Homepage: u, Screenshot: true}}, Dir: os.Getenv("REAL_DIR"), SettleDelay: 3 * time.Second, PageTimeout: 60 * time.Second}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	n, err := tk.RunOnce(context.Background())
	fmt.Println("shots", n, err)
	for _, it := range st.items[1] {
		fmt.Printf("#%d p=%.2f f=%.0f fold=%v y=%.0f x=%.0f w=%.0f h=%.0f | %s | %s\n", it.Rank, it.Prominence, it.FontPx, it.AboveFold, it.Y, it.X, it.W, it.H, it.Title, it.CanonicalURL)
	}
}
