package main

import (
	"ai-bud/services/api/internal/mocktickets"
	"ai-bud/services/api/internal/platform/bootstrap"
	"ai-bud/services/api/internal/platform/config"
	"log"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := bootstrap.Run(func(c config.Config, pool *pgxpool.Pool, _ *slog.Logger) (bootstrap.Application, error) {
		return bootstrap.Application{Address: c.MockAddr, Handler: mocktickets.New(pool, c.MockToken, c.GrantAfter)}, nil
	}); err != nil {
		log.Fatal(err)
	}
}
