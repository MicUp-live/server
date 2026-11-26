// Command dynamo-to-pg performs a one-time, idempotent, foreign-key-ordered
// migration of operational state from DynamoDB to PostgreSQL / Aurora DSQL.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"micup.live/server/internal/config"
	pgstore "micup.live/server/internal/store/postgres"
)

// Options controls DynamoDB -> PostgreSQL migration behavior.
type Options struct {
	DryRun        bool
	SkipEphemeral bool
	EnsureSchema  bool
	Now           time.Time
	Logger        *log.Logger
}

// TableStats reports per-table counts from a migration run.
type TableStats struct {
	Table         string
	DynamoTable   string
	Scanned       int
	Migrated      int
	Skipped       int
	VerifiedCount int64
}

// Report summarizes all tables processed by Migrate.
type Report struct {
	DryRun bool
	Tables []TableStats
}

func main() {
	dryRun := flag.Bool("dry-run", true, "Scan DynamoDB and validate rows without writing to PostgreSQL")
	skipEphemeral := flag.Bool("skip-ephemeral", true, "Skip rate_limits, webauthn_sessions, and expired TTL rows")
	ensureSchema := flag.Bool("ensure-schema", true, "Apply embedded PostgreSQL/DSQL schema before migrating (ignored in dry-run)")
	flag.Parse()

	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	ctx := context.Background()
	ddb, err := newDynamoClient(ctx, cfg.AWSRegion, cfg.DynamoEndpoint)
	if err != nil {
		log.Fatalf("connect to DynamoDB: %v", err)
	}

	var pg *pgstore.Store
	if !*dryRun || cfg.DatabaseURL != "" || (cfg.PostgresDSQLMode && cfg.DSQLEndpoint != "") {
		pg, err = pgstore.New(ctx, pgstore.Config{
			DSN:          cfg.DatabaseURL,
			DSQLMode:     cfg.PostgresDSQLMode,
			DSQLEndpoint: cfg.DSQLEndpoint,
			DSQLRegion:   cfg.AWSRegion,
			DSQLUser:     cfg.DSQLUser,
		})
		if err != nil {
			log.Fatalf("connect to PostgreSQL/DSQL: %v", err)
		}
		defer func() { _ = pg.Close() }()
	}

	report, err := Migrate(ctx, ddb, cfg.DynamoPrefix, pg, Options{
		DryRun:        *dryRun,
		SkipEphemeral: *skipEphemeral,
		EnsureSchema:  *ensureSchema,
		Logger:        log.Default(),
	})
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	printReport(report)
}

func newDynamoClient(ctx context.Context, region, endpoint string) (*dynamodb.Client, error) {
	r := strings.TrimSpace(region)
	if r == "" {
		r = "us-east-2"
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(r)}
	if strings.TrimSpace(endpoint) != "" {
		ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
		opts = append(opts,
			awsconfig.WithBaseEndpoint(ep),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
		)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return dynamodb.NewFromConfig(awsCfg), nil
}

func printReport(r *Report) {
	if r == nil {
		return
	}
	mode := "APPLY"
	if r.DryRun {
		mode = "DRY-RUN"
	}
	fmt.Fprintf(os.Stdout, "DynamoDB -> PostgreSQL migration (%s)\n", mode)
	fmt.Fprintf(os.Stdout, "%-22s %-32s %8s %8s %8s %10s\n", "PG_TABLE", "DYNAMO_TABLE", "SCANNED", "MIGRATED", "SKIPPED", "VERIFIED")
	var totalScanned, totalMigrated, totalSkipped int
	for _, t := range r.Tables {
		totalScanned += t.Scanned
		totalMigrated += t.Migrated
		totalSkipped += t.Skipped
		fmt.Fprintf(os.Stdout, "%-22s %-32s %8d %8d %8d %10d\n", t.Table, t.DynamoTable, t.Scanned, t.Migrated, t.Skipped, t.VerifiedCount)
	}
	fmt.Fprintf(os.Stdout, "%-22s %-32s %8d %8d %8d\n", "TOTAL", "", totalScanned, totalMigrated, totalSkipped)
}
