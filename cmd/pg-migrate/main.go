// Command pg-migrate applies the embedded PostgreSQL / Aurora DSQL schema idempotently.
package main

import (
	"context"
	"log"

	"micup.live/server/internal/config"
	pgstore "micup.live/server/internal/store/postgres"
)

func main() {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	ctx := context.Background()
	s, err := pgstore.New(ctx, pgstore.Config{
		DSN:          cfg.DatabaseURL,
		DSQLMode:     cfg.PostgresDSQLMode,
		DSQLEndpoint: cfg.DSQLEndpoint,
		DSQLRegion:   cfg.AWSRegion,
		DSQLUser:     cfg.DSQLUser,
	})
	if err != nil {
		log.Fatalf("connect to postgres/dsql: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.EnsureSchema(ctx); err != nil {
		log.Fatalf("apply schema: %v", err)
	}
	log.Printf("schema migration complete (dsql_mode=%v)", cfg.PostgresDSQLMode)
}
