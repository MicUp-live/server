// Command local-bootstrap applies the local PostgreSQL schema without contacting AWS.
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"micup.live/server/internal/config"
	pgstore "micup.live/server/internal/store/postgres"
)

func main() {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		log.Fatalf("load local configuration: %v", err)
	}
	if cfg.PostgresDSQLMode {
		log.Fatal("refusing to run local-bootstrap with POSTGRES_DSQL_MODE=true")
	}
	if err := requireLocalDatabaseURL(cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	store, err := pgstore.New(ctx, pgstore.Config{DSN: cfg.DatabaseURL})
	if err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.EnsureSchema(ctx); err != nil {
		log.Fatalf("apply local PostgreSQL schema: %v", err)
	}
	log.Printf("local PostgreSQL schema is ready")
}

func requireLocalDatabaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("DATABASE_URL is required; refusing to continue")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("DATABASE_URL %q must use postgres:// or postgresql:// scheme", raw)
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "postgres":
		return nil
	default:
		return fmt.Errorf("DATABASE_URL host %q is not local; refusing to continue", parsed.Hostname())
	}
}

func init() {
	// Prevent the AWS SDK from probing EC2 instance metadata if this command is
	// launched outside mise.
	_ = os.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}
