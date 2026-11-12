package bootstrap

import (
	"ai-bud/services/api/internal/platform/config"
	"ai-bud/services/api/internal/platform/postgres"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Application struct {
	Address    string
	Handler    http.Handler
	Background func(context.Context)
}

type Factory func(config.Config, *pgxpool.Pool, *slog.Logger) (Application, error)

func Run(factory Factory) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = pool.Ping(startup); err != nil {
		return err
	}
	if err = postgres.Migrate(startup, pool); err != nil {
		return err
	}
	app, err := factory(cfg, pool, logger)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: app.Address, Handler: app.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	logger.Info("server starting", "address", app.Address, "demo_mode", true)
	return Serve(ctx, srv, app.Background)
}

func Serve(ctx context.Context, srv *http.Server, background func(context.Context)) error {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	if background != nil {
		workers.Add(1)
		go func() { defer workers.Done(); background(workerCtx) }()
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	var serveErr error
	select {
	case serveErr = <-errs:
	case <-ctx.Done():
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	shutdownErr := srv.Shutdown(shutdown)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	workers.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return shutdownErr
}
