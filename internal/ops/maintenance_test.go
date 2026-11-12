package ops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

type fakeRet struct {
	age   time.Duration
	count int64
	err   error
}

func (f *fakeRet) CountArticlesBefore(context.Context, time.Time) (int64, error) { return f.count, nil }
func (f *fakeRet) DeleteArticlesBefore(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (f *fakeRet) Maintain(_ context.Context, a time.Duration) (domain.MaintainResult, error) {
	f.age = a
	return domain.MaintainResult{Deleted: 3, WALCheckpoint: true}, f.err
}

func TestMaintenance(t *testing.T) {
	f := &fakeRet{count: 7}
	m := &Maintenance{Store: f, Days: 30}
	msg, err := m.RunOnce(context.Background())
	if err != nil || f.age != 30*24*time.Hour || msg != "3 artículos borrados" {
		t.Fatalf("%q %v %v", msg, err, f.age)
	}
	p, err := m.Preview(context.Background())
	if err != nil || !p.Enabled || p.WouldDelete != 7 || p.Cutoff.IsZero() {
		t.Fatalf("%+v %v", p, err)
	}
	m.Days = 0
	if _, err := m.RunOnce(context.Background()); err != nil || f.age != 0 {
		t.Fatal("days=0 must keep articles", f.age)
	}
	if p, _ := m.Preview(context.Background()); p.Enabled || p.WouldDelete != 0 {
		t.Fatalf("%+v", p)
	}
	f.err = errors.New("boom")
	if _, err := m.RunOnce(context.Background()); err == nil {
		t.Fatal("error not propagated")
	}
}
