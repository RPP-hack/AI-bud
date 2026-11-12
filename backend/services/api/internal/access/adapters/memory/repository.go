package memory

import (
	"ai-bud/services/api/internal/access/domain"
	"context"
	"sort"
	"sync"
	"time"
)

type Repository struct {
	mu   sync.Mutex
	rows map[string]domain.Request
}

func New() *Repository { return &Repository{rows: map[string]domain.Request{}} }
func (r *Repository) Insert(ctx context.Context, v domain.Request) (domain.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Request{}, err
	}
	for _, old := range r.rows {
		if old.OwnerID == v.OwnerID && old.IdempotencyKey == v.IdempotencyKey {
			if old.Parameters != v.Parameters {
				return domain.Request{}, domain.ErrConflict
			}
			return old, nil
		}
	}
	if _, ok := r.rows[v.ID]; ok {
		return domain.Request{}, domain.ErrConflict
	}
	r.rows[v.ID] = v
	return v, nil
}
func (r *Repository) Get(ctx context.Context, owner, id string) (domain.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Request{}, err
	}
	return r.get(owner, id)
}
func (r *Repository) get(owner, id string) (domain.Request, error) {
	v, ok := r.rows[id]
	if !ok || v.OwnerID != owner {
		return domain.Request{}, domain.ErrNotFound
	}
	return v, nil
}
func (r *Repository) List(ctx context.Context, owner string) ([]domain.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []domain.Request{}
	for _, v := range r.rows {
		if v.OwnerID == owner {
			result = append(result, v)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func (r *Repository) Update(ctx context.Context, owner, id string, fn func(*domain.Request) error) (domain.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Request{}, err
	}
	old, err := r.get(owner, id)
	if err != nil {
		return old, err
	}
	v := old
	if err = fn(&v); err != nil {
		return old, err
	}
	if v != old {
		v.Version++
		v.UpdatedAt = time.Now().UTC()
		r.rows[id] = v
	}
	return v, nil
}
