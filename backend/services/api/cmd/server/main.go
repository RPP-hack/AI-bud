package main

import (
	"ai-bud/services/api/internal/access/adapters/demo"
	"ai-bud/services/api/internal/access/adapters/postgres"
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/application"
	identitydemo "ai-bud/services/api/internal/identity/demo"
	"ai-bud/services/api/internal/platform/bootstrap"
	"ai-bud/services/api/internal/platform/config"
	"ai-bud/services/api/internal/ragclient"
	"ai-bud/services/api/internal/transport/httpapi"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := bootstrap.Run(func(c config.Config, pool *pgxpool.Pool, logger *slog.Logger) (bootstrap.Application, error) {
		svc := application.New(postgres.New(pool), tickethttp.New(c.MockURL, c.MockToken, &http.Client{Timeout: 5 * time.Second}), demo.Policy{})
		app := bootstrap.Application{Address: c.HTTPAddr}
		if c.ReconcileEnabled {
			worker, err := application.NewReconciler(postgres.NewQueue(pool), identitydemo.Directory{}, svc, c.Reconciliation, logger)
			if err != nil {
				return bootstrap.Application{}, err
			}
			app.Background = worker.Run
		}
		var options []func(*httpapi.Handler)
		if c.RAGTarget != "" {
			client, err := ragclient.New(c.RAGTarget, c.RAGToken)
			if err != nil {
				return bootstrap.Application{}, err
			}
			app.Close = client.Close
			options = append(options, httpapi.WithRAG(client))
		}
		app.Handler = httpapi.New(svc, httpapi.DemoAuth{}, pool.Ping, logger, options...)
		return app, nil
	}); err != nil {
		log.Fatal(err)
	}
}
