package memory

import (
	"ai-bud/services/api/internal/access/domain"
	"context"
	"testing"
	"time"
)

func TestRepositoryRollbackAndIsolation(t *testing.T) {
	r := New()
	ctx := context.Background()
	v, _ := domain.New("1", "owner", "key", domain.Parameters{Resource: "repo", Access: "read", Reason: "why"}, time.Now())
	r.Insert(ctx, v)
	if _, err := r.Get(ctx, "other", "1"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := r.Update(ctx, "owner", "1", func(v *domain.Request) error { v.State = domain.Cancelled; return domain.ErrConflict }); err != domain.ErrConflict {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, "owner", "1")
	if got.State != domain.Proposed {
		t.Fatal("not rolled back")
	}
	v.IdempotencyKey = "different"
	if _, err := r.Insert(ctx, v); err != domain.ErrConflict {
		t.Fatal(err)
	}
	v.ID = "2"
	r.Insert(ctx, v)
	rows, err := r.List(ctx, "owner")
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Get(cancelled, "owner", "1"); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := r.List(cancelled, "owner"); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := r.Insert(cancelled, v); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := r.Update(cancelled, "owner", "1", func(*domain.Request) error { return nil }); err != context.Canceled {
		t.Fatal(err)
	}
}
