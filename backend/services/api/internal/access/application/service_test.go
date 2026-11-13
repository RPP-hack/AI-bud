package application_test

import (
	"ai-bud/services/api/internal/access/adapters/memory"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

var actor = identity.Actor{ID: "alex", Team: "notifications"}
var params = domain.Parameters{Resource: "notifications", Access: "read", Reason: "first task"}

type policy struct{ deny atomic.Bool }

func (p *policy) CanRequest(identity.Actor, domain.Parameters) bool { return !p.deny.Load() }

type gateway struct {
	mu         sync.Mutex
	tickets    map[string]domain.Ticket
	calls      int
	lose, down bool
	lookupErr  error
	invalid    bool
}

func (g *gateway) Create(_ context.Context, key, owner string, p domain.Parameters) (domain.Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.down {
		return domain.Ticket{}, errors.New("down")
	}
	if g.invalid {
		return domain.Ticket{ID: "fake", Status: "invalid"}, nil
	}
	t, ok := g.tickets[key]
	if !ok {
		t = domain.Ticket{ID: "ticket-" + key, Status: domain.Pending}
		g.tickets[key] = t
	}
	if g.lose {
		return domain.Ticket{}, context.DeadlineExceeded
	}
	return t, nil
}
func (g *gateway) Lookup(_ context.Context, key string) (domain.Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lookupErr != nil {
		return domain.Ticket{}, g.lookupErr
	}
	t, ok := g.tickets[key]
	if !ok {
		return t, domain.ErrNotFound
	}
	return t, nil
}
func setup() (*application.Service, *memory.Repository, *gateway, *policy) {
	r := memory.New()
	g := &gateway{tickets: map[string]domain.Ticket{}}
	p := &policy{}
	return application.New(r, g, p), r, g, p
}
func propose(t *testing.T, s *application.Service) domain.Request {
	t.Helper()
	r, err := s.Propose(context.Background(), actor, "key", params)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestConcurrentConfirmAndProposalIdempotency(t *testing.T) {
	s, _, g, _ := setup()
	ctx := context.Background()
	r := propose(t, s)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same, err := s.Propose(ctx, actor, "key", params)
			if err != nil || same.ID != r.ID {
				t.Errorf("proposal %v %v", same, err)
			}
			if _, err = s.Confirm(ctx, actor, r.ID, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if g.calls != 1 || len(g.tickets) != 1 {
		t.Fatal("duplicate", g.calls)
	}
	got, err := s.Get(ctx, actor, r.ID)
	if err != nil || got.State != domain.Submitted || got.Version != 3 {
		t.Fatal(got, err)
	}
	changed := params
	changed.Reason = "another"
	if _, err = s.Propose(ctx, actor, "key", changed); err != domain.ErrConflict {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, actor)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
func TestLostResponseAndRestartRecovery(t *testing.T) {
	s, r, g, p := setup()
	ctx := context.Background()
	v := propose(t, s)
	g.lose = true
	v, err := s.Confirm(ctx, actor, v.ID, v.Version)
	if err != nil || v.State != domain.Unknown {
		t.Fatal(v, err)
	}
	s = application.New(r, g, p)
	v, err = s.Refresh(ctx, actor, v.ID)
	if err != nil || v.State != domain.Submitted || g.calls != 1 {
		t.Fatal(v, err)
	}
	g.tickets[v.ID] = domain.Ticket{ID: v.Ticket.ID, Status: domain.Granted}
	v, err = s.Refresh(ctx, actor, v.ID)
	if err != nil || v.Ticket.Status != domain.Granted {
		t.Fatal(v, err)
	}
}
func TestRetryMissingTicketUsesSameOperation(t *testing.T) {
	s, _, g, p := setup()
	ctx := context.Background()
	v := propose(t, s)
	g.down = true
	v, err := s.Confirm(ctx, actor, v.ID, 1)
	if err != nil || v.State != domain.Unknown {
		t.Fatal(v, err)
	}
	p.deny.Store(true)
	if _, err = s.Refresh(ctx, actor, v.ID); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	p.deny.Store(false)
	g.down = false
	v, err = s.Refresh(ctx, actor, v.ID)
	if err != nil || v.State != domain.Submitted || len(g.tickets) != 1 {
		t.Fatal(v, err)
	}
}
func TestAuthorizationAndCancellation(t *testing.T) {
	s, _, g, p := setup()
	ctx := context.Background()
	r := propose(t, s)
	other := identity.Actor{ID: "sam"}
	if _, err := s.Get(ctx, other, r.ID); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := s.Confirm(ctx, other, r.ID, 1); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, other, r.ID, 1); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := s.Confirm(ctx, actor, r.ID, 2); err != domain.ErrConflict {
		t.Fatal(err)
	}
	p.deny.Store(true)
	if _, err := s.Confirm(ctx, actor, r.ID, 1); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	if _, err := s.Propose(ctx, actor, "new", params); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Cancel(ctx, actor, r.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	p.deny.Store(false)
	if _, err := s.Confirm(ctx, actor, r.ID, 1); err != domain.ErrConflict {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, actor, r.ID); err != nil {
		t.Fatal(err)
	}
	if g.calls != 0 {
		t.Fatal(g.calls)
	}
	if _, err := s.Refresh(ctx, actor, "missing"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}
func TestInvalidAndAnonymousInputs(t *testing.T) {
	s, _, _, _ := setup()
	ctx := context.Background()
	anon := identity.Actor{}
	checks := []func() error{
		func() error { _, e := s.Propose(ctx, anon, "key", params); return e }, func() error { _, e := s.Get(ctx, anon, "id"); return e }, func() error { _, e := s.List(ctx, anon); return e }, func() error { _, e := s.Confirm(ctx, anon, "id", 1); return e }, func() error { _, e := s.Cancel(ctx, anon, "id", 1); return e },
	}
	for _, f := range checks {
		if f() != domain.ErrForbidden {
			t.Fatal("anonymous")
		}
	}
	if _, err := s.Propose(ctx, actor, "key", domain.Parameters{}); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if _, err := s.Propose(ctx, actor, "", params); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if _, err := s.Confirm(ctx, actor, "id", 0); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, actor, "id", 0); err != domain.ErrInvalid {
		t.Fatal(err)
	}
}
func TestGatewayFailures(t *testing.T) {
	s, _, g, _ := setup()
	ctx := context.Background()
	v := propose(t, s)
	g.invalid = true
	v, err := s.Confirm(ctx, actor, v.ID, 1)
	if err != nil || v.State != domain.Unknown {
		t.Fatal(v, err)
	}
	g.lookupErr = errors.New("offline")
	if _, err = s.Refresh(ctx, actor, v.ID); err != g.lookupErr {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, actor, v.ID, 1); err != nil || g.calls != 1 {
		t.Fatal("confirm must not blindly retry", err)
	}
}
