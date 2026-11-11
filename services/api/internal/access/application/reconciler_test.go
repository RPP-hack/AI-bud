package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
)

type testQueue struct {
	jobs                []ReconciliationJob
	results             []ReconciliationResult
	claimErr, finishErr error
	claimHook           func()
}

func (q *testQueue) Claim(ctx context.Context, lease time.Duration) (ReconciliationJob, bool, error) {
	if q.claimHook != nil {
		q.claimHook()
	}
	if q.claimErr != nil {
		return ReconciliationJob{}, false, q.claimErr
	}
	if len(q.jobs) == 0 {
		return ReconciliationJob{}, false, nil
	}
	j := q.jobs[0]
	q.jobs = q.jobs[1:]
	return j, true, nil
}

func (q *testQueue) Finish(ctx context.Context, job ReconciliationJob, result ReconciliationResult) error {
	q.results = append(q.results, result)
	return q.finishErr
}

type resolveFunc func(context.Context, string) (identity.Actor, error)

func (f resolveFunc) Resolve(ctx context.Context, id string) (identity.Actor, error) {
	return f(ctx, id)
}

type refreshFunc func(context.Context, identity.Actor, string) (domain.Request, error)

func (f refreshFunc) Refresh(ctx context.Context, a identity.Actor, id string) (domain.Request, error) {
	return f(ctx, a, id)
}

func options() ReconciliationOptions {
	return ReconciliationOptions{Interval: time.Millisecond, PollInterval: 5 * time.Second, Timeout: time.Second, LeaseDuration: 3 * time.Second, RetryBase: 2 * time.Second, RetryMax: time.Minute, BatchSize: 4}
}

func newWorker(t *testing.T, q ReconciliationQueue, actors ActorResolver, service Refresher) *Reconciler {
	t.Helper()
	w, err := NewReconciler(q, actors, service, options(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func knownActor(ctx context.Context, id string) (identity.Actor, error) {
	return identity.Actor{ID: id, Team: "notifications"}, nil
}

func TestReconciliationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  domain.State
		status domain.TicketStatus
		err    error
		done   bool
		code   string
	}{
		{"pending", domain.Submitted, domain.Pending, nil, false, ""},
		{"granted", domain.Submitted, domain.Granted, nil, true, ""},
		{"rejected", domain.Submitted, domain.Rejected, nil, true, ""},
		{"cancelled", domain.Cancelled, "", nil, true, ""},
		{"proposed", domain.Proposed, "", nil, true, ""},
		{"unknown", domain.Unknown, "", nil, false, "result_unknown"},
		{"submitting", domain.Submitting, "", nil, false, "result_unknown"},
		{"forbidden", "", "", domain.ErrForbidden, false, "permission_denied"},
		{"unavailable", "", "", errors.New("offline"), false, "refresh_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &testQueue{jobs: []ReconciliationJob{{RequestID: "r", OwnerID: "alex", LeaseToken: "lease"}}}
			w := newWorker(t, q, resolveFunc(knownActor), refreshFunc(func(ctx context.Context, a identity.Actor, id string) (domain.Request, error) {
				if a.ID != "alex" || id != "r" {
					t.Fatal(a, id)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing attempt deadline")
				}
				return domain.Request{State: tc.state, Ticket: domain.Ticket{Status: tc.status}}, tc.err
			}))
			if err := w.ProcessBatch(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(q.results) != 1 || q.results[0].Done != tc.done || q.results[0].ErrorCode != tc.code {
				t.Fatal(q.results)
			}
			if !tc.done && tc.code == "" && q.results[0].RetryAfter != options().PollInterval {
				t.Fatal(q.results)
			}
		})
	}
}

func TestReconciliationRechecksIdentity(t *testing.T) {
	for _, tc := range []struct {
		actor identity.Actor
		err   error
	}{
		{identity.Actor{}, errors.New("deleted")}, {identity.Actor{ID: "other"}, nil}, {identity.Actor{}, nil},
	} {
		q := &testQueue{jobs: []ReconciliationJob{{RequestID: "r", OwnerID: "alex"}}}
		w := newWorker(t, q, resolveFunc(func(context.Context, string) (identity.Actor, error) { return tc.actor, tc.err }), refreshFunc(func(context.Context, identity.Actor, string) (domain.Request, error) {
			t.Fatal("must not call service")
			return domain.Request{}, nil
		}))
		if err := w.ProcessBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(q.results) != 1 || q.results[0].ErrorCode != "identity_unavailable" {
			t.Fatal(q.results)
		}
	}
}

func TestReconciliationBackoff(t *testing.T) {
	w := newWorker(t, &testQueue{}, resolveFunc(knownActor), nil)
	for _, tc := range []struct {
		attempts int
		want     time.Duration
	}{{0, 2 * time.Second}, {1, 4 * time.Second}, {4, 32 * time.Second}, {5, time.Minute}, {1000000, time.Minute}} {
		r := w.retry(ReconciliationJob{Attempts: tc.attempts}, "offline")
		if r.RetryAfter != tc.want {
			t.Fatal(tc, r)
		}
	}
}

func TestReconciliationBatchLimitAndStorageFailures(t *testing.T) {
	service := refreshFunc(func(context.Context, identity.Actor, string) (domain.Request, error) {
		return domain.Request{State: domain.Cancelled}, nil
	})
	q := &testQueue{jobs: make([]ReconciliationJob, 7)}
	for i := range q.jobs {
		q.jobs[i].OwnerID = "alex"
	}
	w := newWorker(t, q, resolveFunc(knownActor), service)
	if err := w.ProcessBatch(context.Background()); err != nil || len(q.jobs) != 3 || len(q.results) != 4 {
		t.Fatal(q, err)
	}
	q.claimErr = errors.New("database down")
	if err := w.ProcessBatch(context.Background()); err != q.claimErr {
		t.Fatal(err)
	}
	q.claimErr = nil
	q.finishErr = errors.New("commit failed")
	if err := w.ProcessBatch(context.Background()); err != q.finishErr {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.ProcessBatch(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	q.finishErr = nil
	w.service = refreshFunc(func(context.Context, identity.Actor, string) (domain.Request, error) {
		cancel()
		return domain.Request{}, context.Canceled
	})
	before := len(q.results)
	if err := w.ProcessBatch(ctx); err != context.Canceled || len(q.results) != before {
		t.Fatal(err, q.results)
	}
}

func TestReconciliationRunStopsAndRetriesQueueFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	q := &testQueue{claimErr: errors.New("database offline"), claimHook: func() {
		calls++
		if calls == 2 {
			cancel()
		}
	}}
	w := newWorker(t, q, resolveFunc(knownActor), nil)
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestInvalidReconciliationOptions(t *testing.T) {
	mutations := []func(*ReconciliationOptions){
		func(o *ReconciliationOptions) { o.Interval = 0 }, func(o *ReconciliationOptions) { o.PollInterval = 0 },
		func(o *ReconciliationOptions) { o.Timeout = 0 }, func(o *ReconciliationOptions) { o.LeaseDuration = o.Timeout },
		func(o *ReconciliationOptions) { o.RetryBase = 0 }, func(o *ReconciliationOptions) { o.RetryMax = o.RetryBase - 1 },
		func(o *ReconciliationOptions) { o.BatchSize = 0 }, func(o *ReconciliationOptions) { o.BatchSize = 101 },
	}
	for _, mutate := range mutations {
		o := options()
		mutate(&o)
		if _, err := NewReconciler(nil, nil, nil, o, nil); err == nil {
			t.Fatal(o)
		}
	}
}
