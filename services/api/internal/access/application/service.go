package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
)

type Repository interface {
	Insert(context.Context, domain.Request) (domain.Request, error)
	Get(context.Context, string, string) (domain.Request, error)
	List(context.Context, string) ([]domain.Request, error)
	Update(context.Context, string, string, func(*domain.Request) error) (domain.Request, error)
}

type Gateway interface {
	Create(context.Context, string, string, domain.Parameters) (domain.Ticket, error)
	Lookup(context.Context, string) (domain.Ticket, error)
}
type Policy interface {
	CanRequest(identity.Actor, domain.Parameters) bool
}

type Service struct {
	repo    Repository
	gateway Gateway
	policy  Policy
}

func New(repo Repository, gateway Gateway, policy Policy) *Service {
	return &Service{repo, gateway, policy}
}

func (s *Service) Propose(ctx context.Context, a identity.Actor, key string, p domain.Parameters) (domain.Request, error) {
	if a.ID == "" {
		return domain.Request{}, domain.ErrForbidden
	}
	if err := p.Validate(); err != nil {
		return domain.Request{}, err
	}
	if !s.policy.CanRequest(a, p) {
		return domain.Request{}, domain.ErrForbidden
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return domain.Request{}, err
	}
	r, err := domain.New(hex.EncodeToString(b[:]), a.ID, key, p, time.Now().UTC())
	if err != nil {
		return domain.Request{}, err
	}
	return s.repo.Insert(ctx, r)
}
func (s *Service) Get(ctx context.Context, a identity.Actor, id string) (domain.Request, error) {
	if a.ID == "" {
		return domain.Request{}, domain.ErrForbidden
	}
	return s.repo.Get(ctx, a.ID, id)
}
func (s *Service) List(ctx context.Context, a identity.Actor) ([]domain.Request, error) {
	if a.ID == "" {
		return nil, domain.ErrForbidden
	}
	return s.repo.List(ctx, a.ID)
}

func (s *Service) Confirm(ctx context.Context, a identity.Actor, id string, version int64) (domain.Request, error) {
	if a.ID == "" {
		return domain.Request{}, domain.ErrForbidden
	}
	if version < 1 {
		return domain.Request{}, domain.ErrInvalid
	}
	execute := false
	r, err := s.repo.Update(ctx, a.ID, id, func(r *domain.Request) error {
		if !s.policy.CanRequest(a, r.Parameters) {
			return domain.ErrForbidden
		}
		if r.State == domain.Submitting || r.State == domain.Submitted || r.State == domain.Unknown {
			return nil
		}
		if err := r.Confirm(version); err != nil {
			return err
		}
		execute = true
		return nil
	})
	if err != nil || !execute {
		return r, err
	}
	return s.execute(ctx, r)
}

func (s *Service) Cancel(ctx context.Context, a identity.Actor, id string, version int64) (domain.Request, error) {
	if a.ID == "" {
		return domain.Request{}, domain.ErrForbidden
	}
	if version < 1 {
		return domain.Request{}, domain.ErrInvalid
	}
	return s.repo.Update(ctx, a.ID, id, func(r *domain.Request) error {
		if r.State == domain.Cancelled {
			return nil
		}
		return r.Cancel(version)
	})
}

func (s *Service) Refresh(ctx context.Context, a identity.Actor, id string) (domain.Request, error) {
	r, err := s.Get(ctx, a, id)
	if err != nil {
		return r, err
	}
	if r.State == domain.Proposed || r.State == domain.Cancelled {
		return r, nil
	}
	t, err := s.gateway.Lookup(ctx, r.ID)
	if errors.Is(err, domain.ErrNotFound) && (r.State == domain.Submitting || r.State == domain.Unknown) {
		if !s.policy.CanRequest(a, r.Parameters) {
			return r, domain.ErrForbidden
		}
		return s.execute(ctx, r)
	}
	if err != nil {
		return r, err
	}
	return s.persist(ctx, r, t)
}

func (s *Service) execute(ctx context.Context, r domain.Request) (domain.Request, error) {
	t, err := s.gateway.Create(ctx, r.ID, r.OwnerID, r.Parameters)
	if err == nil {
		err = t.Validate()
	}
	if err != nil {
		return s.repo.Update(ctx, r.OwnerID, r.ID, func(current *domain.Request) error { current.MarkUnknown(); return nil })
	}
	return s.persist(ctx, r, t)
}
func (s *Service) persist(ctx context.Context, r domain.Request, t domain.Ticket) (domain.Request, error) {
	return s.repo.Update(ctx, r.OwnerID, r.ID, func(current *domain.Request) error { return current.Apply(t) })
}
