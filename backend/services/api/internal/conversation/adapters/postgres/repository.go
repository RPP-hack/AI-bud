package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"

	"ai-bud/services/api/internal/conversation/domain"
	"ai-bud/services/api/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func lock(ctx context.Context, tx pgx.Tx, actor identity.Actor) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 48329))", actor.ID)
	return err
}

func current(ctx context.Context, tx pgx.Tx, actor identity.Actor) (string, error) {
	_, err := tx.Exec(ctx, "UPDATE conversations SET closed_at=now() WHERE owner_id=$1 AND closed_at IS NULL AND (team<>$2 OR role<>$3)", actor.ID, actor.Team, actor.Role)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, "SELECT id FROM conversations WHERE owner_id=$1 AND closed_at IS NULL", actor.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id = rand.Text()
		_, err = tx.Exec(ctx, "INSERT INTO conversations(id,owner_id,team,role) VALUES($1,$2,$3,$4)", id, actor.ID, actor.Team, actor.Role)
	}
	return id, err
}

func expire(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, "UPDATE conversation_turns SET state='failed', error_code='interrupted' WHERE conversation_id=$1 AND state='pending' AND expires_at <= now()", id)
	return err
}

func turns(ctx context.Context, tx pgx.Tx, id string) ([]domain.Turn, error) {
	rows, err := tx.Query(ctx, "SELECT id,idempotency_key,input,state,answer,error_code,created_at FROM conversation_turns WHERE conversation_id=$1 ORDER BY sequence", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Turn{}
	for rows.Next() {
		var t domain.Turn
		var input, answer []byte
		if err = rows.Scan(&t.ID, &t.ClientID, &input, &t.State, &answer, &t.ErrorCode, &t.CreatedAt); err != nil {
			return nil, err
		}
		var in domain.Input
		if err = json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		t.Question, t.Mode, t.TopK = in.Question, in.Mode, *in.TopK
		if answer != nil {
			if err = json.Unmarshal(answer, &t.Answer); err != nil {
				return nil, err
			}
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (r *Repository) Current(ctx context.Context, actor identity.Actor) (domain.Conversation, error) {
	return r.load(ctx, actor, "")
}
func (r *Repository) Reset(ctx context.Context, actor identity.Actor, expected string) (domain.Conversation, error) {
	return r.load(ctx, actor, expected)
}
func (r *Repository) load(ctx context.Context, actor identity.Actor, expected string) (domain.Conversation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Conversation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx, actor); err != nil {
		return domain.Conversation{}, err
	}
	id, err := current(ctx, tx, actor)
	if err != nil {
		return domain.Conversation{}, err
	}
	if err = expire(ctx, tx, id); err != nil {
		return domain.Conversation{}, err
	}
	if expected != "" {
		var owned bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1 AND owner_id=$2 AND team=$3 AND role=$4)", expected, actor.ID, actor.Team, actor.Role).Scan(&owned); err != nil {
			return domain.Conversation{}, err
		}
		if !owned {
			return domain.Conversation{}, domain.ErrNotFound
		}
		if expected == id {
			var pending bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversation_turns WHERE conversation_id=$1 AND state='pending')", id).Scan(&pending); err != nil {
				return domain.Conversation{}, err
			}
			if pending {
				return domain.Conversation{}, domain.ErrConflict
			}
			if _, err = tx.Exec(ctx, "UPDATE conversations SET closed_at=now() WHERE id=$1", id); err != nil {
				return domain.Conversation{}, err
			}
			id, err = current(ctx, tx, actor)
			if err != nil {
				return domain.Conversation{}, err
			}
		}
	}
	items, err := turns(ctx, tx, id)
	if err != nil {
		return domain.Conversation{}, err
	}
	return domain.Conversation{ID: id, Turns: items}, tx.Commit(ctx)
}

func (r *Repository) Begin(ctx context.Context, actor identity.Actor, id, key string, input domain.Input) (domain.Turn, []domain.Turn, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Turn{}, nil, false, err
	}
	defer tx.Rollback(ctx)
	if err = lock(ctx, tx, actor); err != nil {
		return domain.Turn{}, nil, false, err
	}
	var active bool
	err = tx.QueryRow(ctx, "SELECT closed_at IS NULL FROM conversations WHERE id=$1 AND owner_id=$2 AND team=$3 AND role=$4", id, actor.ID, actor.Team, actor.Role).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Turn{}, nil, false, domain.ErrNotFound
	}
	if err != nil {
		return domain.Turn{}, nil, false, err
	}
	if !active {
		return domain.Turn{}, nil, false, domain.ErrConflict
	}
	if err = expire(ctx, tx, id); err != nil {
		return domain.Turn{}, nil, false, err
	}
	previous, err := turns(ctx, tx, id)
	if err != nil {
		return domain.Turn{}, nil, false, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return domain.Turn{}, nil, false, err
	}
	var existingID string
	var same bool
	err = tx.QueryRow(ctx, "SELECT id,input=$3::jsonb FROM conversation_turns WHERE conversation_id=$1 AND idempotency_key=$2", id, key, data).Scan(&existingID, &same)
	if err == nil {
		if !same {
			return domain.Turn{}, nil, false, domain.ErrConflict
		}
		for _, item := range previous {
			if item.ID == existingID {
				return item, nil, false, tx.Commit(ctx)
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Turn{}, nil, false, err
	}
	for _, item := range previous {
		if item.State == "pending" {
			return domain.Turn{}, nil, false, domain.ErrConflict
		}
	}
	if len(previous) >= 100 {
		return domain.Turn{}, nil, false, domain.ErrFull
	}
	t := domain.Turn{ID: rand.Text(), ClientID: key, Question: input.Question, Mode: input.Mode, TopK: *input.TopK, State: "pending"}
	err = tx.QueryRow(ctx, "INSERT INTO conversation_turns(id,conversation_id,idempotency_key,input,state,expires_at) VALUES($1,$2,$3,$4,'pending',now()+interval '120 seconds') RETURNING created_at", t.ID, id, key, data).Scan(&t.CreatedAt)
	if err != nil {
		return domain.Turn{}, nil, false, err
	}
	return t, previous, true, tx.Commit(ctx)
}

func (r *Repository) Finish(ctx context.Context, id string, answer *domain.Answer, code string) (domain.Turn, error) {
	var data []byte
	var err error
	state := "failed"
	if answer != nil {
		data, err = json.Marshal(answer)
		state = "completed"
	}
	if err != nil {
		return domain.Turn{}, err
	}
	var t domain.Turn
	var input []byte
	err = r.pool.QueryRow(ctx, "UPDATE conversation_turns SET state=$2,answer=$3,error_code=$4 WHERE id=$1 AND state='pending' RETURNING input,created_at,idempotency_key", id, state, data, code).Scan(&input, &t.CreatedAt, &t.ClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrConflict
	}
	if err != nil {
		return t, err
	}
	var in domain.Input
	if err = json.Unmarshal(input, &in); err != nil {
		return t, err
	}
	t.ID, t.Question, t.Mode, t.TopK, t.State, t.Answer, t.ErrorCode = id, in.Question, in.Mode, *in.TopK, state, answer, code
	return t, nil
}
