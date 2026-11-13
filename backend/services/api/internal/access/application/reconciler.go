package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
)

type ReconciliationJob struct {
	RequestID  string
	OwnerID    string
	LeaseToken string
	Attempts   int
}

type ReconciliationResult struct {
	Done       bool
	RetryAfter time.Duration
	ErrorCode  string
}

type ReconciliationQueue interface {
	Claim(context.Context, time.Duration) (ReconciliationJob, bool, error)
	Finish(context.Context, ReconciliationJob, ReconciliationResult) error
}

type ActorResolver interface {
	Resolve(context.Context, string) (identity.Actor, error)
}

type Refresher interface {
	Refresh(context.Context, identity.Actor, string) (domain.Request, error)
}

type ReconciliationOptions struct {
	Interval      time.Duration
	PollInterval  time.Duration
	Timeout       time.Duration
	LeaseDuration time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
	BatchSize     int
}

func (o ReconciliationOptions) Validate() error {
	if o.Interval <= 0 || o.PollInterval <= 0 || o.Timeout <= 0 || o.LeaseDuration <= o.Timeout || o.RetryBase <= 0 || o.RetryMax < o.RetryBase || o.BatchSize < 1 || o.BatchSize > 100 {
		return fmt.Errorf("invalid reconciliation options")
	}
	return nil
}

type Reconciler struct {
	queue   ReconciliationQueue
	actors  ActorResolver
	service Refresher
	options ReconciliationOptions
	logger  *slog.Logger
}

func NewReconciler(queue ReconciliationQueue, actors ActorResolver, service Refresher, options ReconciliationOptions, logger *slog.Logger) (*Reconciler, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Reconciler{queue: queue, actors: actors, service: service, options: options, logger: logger}, nil
}

func (w *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(w.options.Interval)
	defer ticker.Stop()
	for {
		if err := w.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
			w.logger.Error("reconciliation batch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Reconciler) ProcessBatch(ctx context.Context) error {
	for i := 0; i < w.options.BatchSize; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		claimCtx, cancel := context.WithTimeout(ctx, w.options.Timeout)
		job, found, err := w.queue.Claim(claimCtx, w.options.LeaseDuration)
		cancel()
		if err != nil || !found {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, w.options.Timeout)
		result := w.process(attemptCtx, job)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		finishCtx, cancel := context.WithTimeout(ctx, w.options.Timeout)
		err = w.queue.Finish(finishCtx, job, result)
		cancel()
		if err != nil {
			return err
		}
		if result.ErrorCode != "" {
			w.logger.Warn("reconciliation deferred", "request_id", job.RequestID, "code", result.ErrorCode, "retry_after", result.RetryAfter)
		}
	}
	return nil
}

func (w *Reconciler) process(ctx context.Context, job ReconciliationJob) ReconciliationResult {
	a, err := w.actors.Resolve(ctx, job.OwnerID)
	if err != nil || a.ID != job.OwnerID || a.ID == "" {
		return w.retry(job, "identity_unavailable")
	}
	r, err := w.service.Refresh(ctx, a, job.RequestID)
	if errors.Is(err, domain.ErrForbidden) {
		return w.retry(job, "permission_denied")
	}
	if err != nil {
		return w.retry(job, "refresh_failed")
	}
	if r.State == domain.Unknown || r.State == domain.Submitting {
		return w.retry(job, "result_unknown")
	}
	if !r.NeedsReconciliation() {
		return ReconciliationResult{Done: true}
	}
	return ReconciliationResult{RetryAfter: w.options.PollInterval}
}

func (w *Reconciler) retry(job ReconciliationJob, code string) ReconciliationResult {
	delay := w.options.RetryBase
	for i := 0; i < job.Attempts && delay < w.options.RetryMax; i++ {
		if delay > w.options.RetryMax/2 {
			delay = w.options.RetryMax
			break
		}
		delay *= 2
	}
	return ReconciliationResult{RetryAfter: delay, ErrorCode: code}
}
