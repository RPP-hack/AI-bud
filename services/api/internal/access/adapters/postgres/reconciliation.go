package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func schedule(ctx context.Context, tx pgx.Tx, r domain.Request) error {
	if !r.NeedsReconciliation() {
		_, err := tx.Exec(ctx, "DELETE FROM access_reconciliation_jobs WHERE request_id=$1", r.ID)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO access_reconciliation_jobs(request_id, due_at)
		VALUES($1, now() + interval '5 seconds') ON CONFLICT(request_id) DO NOTHING`, r.ID)
	return err
}

type Queue struct{ pool *pgxpool.Pool }

func NewQueue(pool *pgxpool.Pool) *Queue { return &Queue{pool: pool} }

func (q *Queue) Claim(ctx context.Context, lease time.Duration) (application.ReconciliationJob, bool, error) {
	if lease < time.Microsecond {
		return application.ReconciliationJob{}, false, domain.ErrInvalid
	}
	var job application.ReconciliationJob
	err := q.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT request_id FROM access_reconciliation_jobs
		WHERE due_at <= now() AND (leased_until IS NULL OR leased_until <= now())
		ORDER BY due_at, request_id FOR UPDATE SKIP LOCKED LIMIT 1
	)
	UPDATE access_reconciliation_jobs AS job
	SET lease_token=$1, leased_until=clock_timestamp() + $2 * interval '1 microsecond'
	FROM candidate, access_requests AS request
	WHERE job.request_id=candidate.request_id AND request.id=job.request_id
	RETURNING job.request_id, request.owner_id, job.lease_token, job.attempts`, rand.Text(), lease.Microseconds()).Scan(&job.RequestID, &job.OwnerID, &job.LeaseToken, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, false, nil
	}
	return job, err == nil, err
}

func (q *Queue) Finish(ctx context.Context, job application.ReconciliationJob, result application.ReconciliationResult) error {
	if job.RequestID == "" || job.LeaseToken == "" {
		return domain.ErrInvalid
	}
	if result.Done {
		_, err := q.pool.Exec(ctx, "DELETE FROM access_reconciliation_jobs WHERE request_id=$1 AND lease_token=$2", job.RequestID, job.LeaseToken)
		return err
	}
	if result.RetryAfter < time.Microsecond {
		return domain.ErrInvalid
	}
	_, err := q.pool.Exec(ctx, `UPDATE access_reconciliation_jobs
		SET due_at=clock_timestamp() + $3 * interval '1 microsecond',
		lease_token=NULL, leased_until=NULL, last_error_code=$4,
		attempts=CASE WHEN $4='' THEN 0 ELSE LEAST(attempts, 999999) + 1 END
		WHERE request_id=$1 AND lease_token=$2`, job.RequestID, job.LeaseToken, result.RetryAfter.Microseconds(), result.ErrorCode)
	return err
}
