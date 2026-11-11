//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-bud/services/api/internal/access/adapters/demo"
	storage "ai-bud/services/api/internal/access/adapters/postgres"
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
	identitydemo "ai-bud/services/api/internal/identity/demo"
	"ai-bud/services/api/internal/mocktickets"
	"ai-bud/services/api/internal/onboarding"
	database "ai-bud/services/api/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func makeDue(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "UPDATE access_reconciliation_jobs SET due_at=now()-interval '1 second' WHERE request_id=$1", id); err != nil {
		t.Fatal(err)
	}
}

func insertSubmitting(t *testing.T, pool *pgxpool.Pool, id string) domain.Request {
	t.Helper()
	r, err := domain.New(id, "alex", id, domain.Parameters{Resource: "notifications", Access: "read", Reason: "task"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	repo := storage.New(pool)
	r, err = repo.Insert(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r, err = repo.Update(context.Background(), r.OwnerID, r.ID, func(v *domain.Request) error { return v.Confirm(1) })
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, id)
	return r
}

func TestReconciliationQueueLeasesAndFencing(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := insertSubmitting(t, pool, "lease-request")
	q := storage.NewQueue(pool)
	jobs := make(chan application.ReconciliationJob, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, ok, err := q.Claim(ctx, time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				jobs <- job
			}
		}()
	}
	wg.Wait()
	close(jobs)
	var old application.ReconciliationJob
	count := 0
	for job := range jobs {
		old = job
		count++
	}
	if count != 1 || old.OwnerID != "alex" || old.RequestID != r.ID {
		t.Fatal(count, old)
	}
	if _, err := pool.Exec(ctx, "UPDATE access_reconciliation_jobs SET leased_until=now()-interval '1 second' WHERE request_id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	current, ok, err := storage.NewQueue(pool).Claim(ctx, time.Minute)
	if err != nil || !ok || current.LeaseToken == old.LeaseToken {
		t.Fatal(current, ok, err)
	}
	for _, result := range []application.ReconciliationResult{{Done: true}, {RetryAfter: time.Hour, ErrorCode: "stale"}} {
		if err := q.Finish(ctx, old, result); err != nil {
			t.Fatal(err)
		}
	}
	var token, code string
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT lease_token, attempts, last_error_code FROM access_reconciliation_jobs WHERE request_id=$1", r.ID).Scan(&token, &attempts, &code); err != nil {
		t.Fatal(err)
	}
	if token != current.LeaseToken || attempts != 0 || code != "" {
		t.Fatal(token, attempts, code)
	}
	if err := q.Finish(ctx, current, application.ReconciliationResult{RetryAfter: time.Minute, ErrorCode: "offline"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatal(ok, err)
	}
	makeDue(t, pool, r.ID)
	current, ok, err = q.Claim(ctx, time.Minute)
	if err != nil || !ok || current.Attempts != 1 {
		t.Fatal(current, ok, err)
	}
	if err := q.Finish(ctx, current, application.ReconciliationResult{RetryAfter: time.Minute}); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, r.ID)
	current, ok, err = q.Claim(ctx, time.Minute)
	if err != nil || !ok || current.Attempts != 0 {
		t.Fatal(current, ok, err)
	}
	if err := q.Finish(ctx, current, application.ReconciliationResult{Done: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, _, err := q.Claim(ctx, 0); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, application.ReconciliationJob{}, application.ReconciliationResult{Done: true}); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, current, application.ReconciliationResult{}); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := q.Claim(cancelled, time.Minute); err == nil {
		t.Fatal("cancelled claim succeeded")
	}
	if err := q.Finish(cancelled, current, application.ReconciliationResult{Done: true}); err == nil {
		t.Fatal("cancelled finish succeeded")
	}
}

func integrationWorker(t *testing.T, pool *pgxpool.Pool, svc *application.Service, actors application.ActorResolver) *application.Reconciler {
	t.Helper()
	options := application.ReconciliationOptions{Interval: time.Second, PollInterval: time.Second, Timeout: time.Second, LeaseDuration: 5 * time.Second, RetryBase: time.Second, RetryMax: time.Minute, BatchSize: 10}
	w, err := application.NewReconciler(storage.NewQueue(pool), actors, svc, options, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

type lostResponseTransport struct {
	base http.RoundTripper
	lost atomic.Bool
}

func (t *lostResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, err := t.base.RoundTrip(req)
	if err == nil && req.Method == http.MethodPost && !t.lost.Swap(true) {
		r.Body.Close()
		return nil, errors.New("response lost after commit")
	}
	return r, err
}

func TestWorkerRecoversLostResponseAndUpdatesProgress(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	repo := storage.New(pool)
	mock := httptest.NewServer(mocktickets.New(pool, "token", 0))
	defer mock.Close()
	badHTTP := &http.Client{Transport: &lostResponseTransport{base: mock.Client().Transport}}
	svc := application.New(repo, tickethttp.New(mock.URL, "token", badHTTP), demo.Policy{})
	a := identity.Actor{ID: "alex", Team: "notifications"}
	r, err := svc.Propose(ctx, a, "lost", domain.Parameters{Resource: "notifications", Access: "read", Reason: "task"})
	if err != nil {
		t.Fatal(err)
	}
	r, err = svc.Confirm(ctx, a, r.ID, 1)
	if err != nil || r.State != domain.Unknown {
		t.Fatal(r, err)
	}
	makeDue(t, pool, r.ID)
	svc = application.New(storage.New(pool), tickethttp.New(mock.URL, "token", mock.Client()), demo.Policy{})
	w := integrationWorker(t, pool, svc, identitydemo.Directory{})
	if err = w.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = repo.Get(ctx, a.ID, r.ID)
	if err != nil || r.State != domain.Submitted || r.Ticket.Status != domain.Pending {
		t.Fatal(r, err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM mock_access.tickets").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE mock_access.tickets SET status='granted' WHERE operation_id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, r.ID)
	if err = w.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = repo.Get(ctx, a.ID, r.ID)
	if err != nil || onboarding.ForAccess(r).State != "done" {
		t.Fatal(r, err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM access_reconciliation_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

type actorResolverFunc func(context.Context, string) (identity.Actor, error)

func (f actorResolverFunc) Resolve(ctx context.Context, id string) (identity.Actor, error) {
	return f(ctx, id)
}

func TestWorkerRechecksRightsAndResumesAbandonedSubmission(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := insertSubmitting(t, pool, "abandoned")
	mock := httptest.NewServer(mocktickets.New(pool, "token", 0))
	defer mock.Close()
	svc := application.New(storage.New(pool), tickethttp.New(mock.URL, "token", mock.Client()), demo.Policy{})
	revoked := actorResolverFunc(func(context.Context, string) (identity.Actor, error) {
		return identity.Actor{ID: "alex", Team: "payments"}, nil
	})
	w := integrationWorker(t, pool, svc, revoked)
	if err := w.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	var code string
	var count, attempts int
	if err := pool.QueryRow(ctx, "SELECT attempts,last_error_code FROM access_reconciliation_jobs WHERE request_id=$1", r.ID).Scan(&attempts, &code); err != nil || attempts != 1 || code != "permission_denied" {
		t.Fatal(attempts, code, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM mock_access.tickets").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	makeDue(t, pool, r.ID)
	w = integrationWorker(t, pool, svc, identitydemo.Directory{})
	if err := w.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := storage.New(pool).Get(ctx, "alex", r.ID)
	if err != nil || got.State != domain.Submitted {
		t.Fatal(got, err)
	}
}

func TestQueueIsAtomicWithStateAndTerminalTransitions(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	repo := storage.New(pool)
	r, err := domain.New("atomic", "alex", "atomic", domain.Parameters{Resource: "notifications", Access: "read", Reason: "task"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Insert(ctx, r); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM access_reconciliation_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	_, err = repo.Update(ctx, "alex", r.ID, func(r *domain.Request) error { r.Confirm(1); return domain.ErrConflict })
	if err != domain.ErrConflict {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM access_reconciliation_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = repo.Update(ctx, "alex", r.ID, func(r *domain.Request) error { return r.Confirm(1) }); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, r.ID)
	q := storage.NewQueue(pool)
	job, ok, err := q.Claim(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err = repo.Update(ctx, "alex", r.ID, func(r *domain.Request) error { return r.Apply(domain.Ticket{ID: "ticket", Status: domain.Rejected}) }); err != nil {
		t.Fatal(err)
	}
	if err = q.Finish(ctx, job, application.ReconciliationResult{RetryAfter: time.Minute, ErrorCode: "late"}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM access_reconciliation_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestMigrationBackfillsPendingOperations(t *testing.T) {
	pool := emptyTestDB(t)
	ctx := context.Background()
	schema, err := os.ReadFile("../../internal/platform/postgres/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "CREATE TABLE schema_migrations(version integer PRIMARY KEY); INSERT INTO schema_migrations VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		state  domain.State
		status domain.TicketStatus
	}{
		{"proposed", domain.Proposed, ""}, {"cancelled", domain.Cancelled, ""}, {"unknown", domain.Unknown, ""},
		{"submitting", domain.Submitting, ""}, {"pending", domain.Submitted, domain.Pending}, {"granted", domain.Submitted, domain.Granted},
	} {
		r := domain.Request{ID: tc.id, OwnerID: "alex", IdempotencyKey: tc.id, State: tc.state, Ticket: domain.Ticket{Status: tc.status}, Version: 1}
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "INSERT INTO access_requests(id,owner_id,idempotency_key,payload) VALUES($1,$2,$3,$4)", r.ID, r.OwnerID, r.IdempotencyKey, data); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err = database.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM access_reconciliation_jobs").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err = pool.QueryRow(ctx, "SELECT max(version) FROM schema_migrations").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
