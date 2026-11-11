package postgres

import (
	"ai-bud/services/api/internal/access/domain"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool} }
func decode(row pgx.Row) (domain.Request, error) {
	var data []byte
	var v domain.Request
	if err := row.Scan(&data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, domain.ErrNotFound
		}
		return v, err
	}
	err := json.Unmarshal(data, &v)
	return v, err
}
func audit(ctx context.Context, tx pgx.Tx, v domain.Request) error {
	_, err := tx.Exec(ctx, "INSERT INTO access_events(request_id,state,ticket_status,version) VALUES($1,$2,$3,$4)", v.ID, v.State, v.Ticket.Status, v.Version)
	return err
}
func (r *Repository) Insert(ctx context.Context, v domain.Request) (domain.Request, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Request{}, err
	}
	defer tx.Rollback(ctx)
	data, err := json.Marshal(v)
	if err != nil {
		return domain.Request{}, err
	}
	tag, err := tx.Exec(ctx, "INSERT INTO access_requests(id,owner_id,idempotency_key,payload) VALUES($1,$2,$3,$4) ON CONFLICT(owner_id,idempotency_key) DO NOTHING", v.ID, v.OwnerID, v.IdempotencyKey, data)
	if err != nil {
		return domain.Request{}, err
	}
	if tag.RowsAffected() == 0 {
		old, err := decode(tx.QueryRow(ctx, "SELECT payload FROM access_requests WHERE owner_id=$1 AND idempotency_key=$2", v.OwnerID, v.IdempotencyKey))
		if err != nil {
			return old, err
		}
		if old.Parameters != v.Parameters {
			return domain.Request{}, domain.ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if err = audit(ctx, tx, v); err != nil {
		return domain.Request{}, err
	}
	if err = schedule(ctx, tx, v); err != nil {
		return domain.Request{}, err
	}
	return v, tx.Commit(ctx)
}
func (r *Repository) Get(ctx context.Context, owner, id string) (domain.Request, error) {
	return decode(r.pool.QueryRow(ctx, "SELECT payload FROM access_requests WHERE owner_id=$1 AND id=$2", owner, id))
}
func (r *Repository) List(ctx context.Context, owner string) ([]domain.Request, error) {
	rows, err := r.pool.Query(ctx, "SELECT payload FROM access_requests WHERE owner_id=$1 ORDER BY id", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Request{}
	for rows.Next() {
		v, err := decode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (r *Repository) Update(ctx context.Context, owner, id string, fn func(*domain.Request) error) (domain.Request, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Request{}, err
	}
	defer tx.Rollback(ctx)
	old, err := decode(tx.QueryRow(ctx, "SELECT payload FROM access_requests WHERE owner_id=$1 AND id=$2 FOR UPDATE", owner, id))
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
		data, err := json.Marshal(v)
		if err != nil {
			return old, err
		}
		if _, err = tx.Exec(ctx, "UPDATE access_requests SET payload=$1 WHERE id=$2", data, id); err != nil {
			return old, err
		}
		if err = audit(ctx, tx, v); err != nil {
			return old, err
		}
		if err = schedule(ctx, tx, v); err != nil {
			return old, err
		}
	}
	return v, tx.Commit(ctx)
}
