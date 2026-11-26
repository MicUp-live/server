package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBuildDSQLDSNAndIAMBeforeConnect(t *testing.T) {
	endpoint := "abc123.dsql.us-east-2.on.aws"
	dsn := buildDSQLDSN(endpoint, "")
	if !strings.Contains(dsn, "postgres://admin@"+endpoint+":5432/postgres?sslmode=require") {
		t.Fatalf("unexpected DSQL DSN: %s", dsn)
	}

	staticCreds := credentials.NewStaticCredentialsProvider("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "session-token")

	t.Run("admin role", func(t *testing.T) {
		poolCfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if err := configureDSQLPool(context.Background(), Config{
			DSQLMode:        true,
			DSQLEndpoint:    endpoint,
			DSQLRegion:      "us-east-2",
			DSQLCredentials: staticCreds,
		}, poolCfg); err != nil {
			t.Fatalf("configureDSQLPool: %v", err)
		}
		if poolCfg.MaxConnLifetime != defaultDSQLMaxLifetime || poolCfg.MaxConnLifetime > time.Hour {
			t.Fatalf("expected MaxConnLifetime=%v, got %v", defaultDSQLMaxLifetime, poolCfg.MaxConnLifetime)
		}
		if poolCfg.BeforeConnect == nil {
			t.Fatal("expected BeforeConnect hook to be configured")
		}
		connCfg := poolCfg.ConnConfig.Copy()
		if err := poolCfg.BeforeConnect(context.Background(), connCfg); err != nil {
			t.Fatalf("BeforeConnect: %v", err)
		}
		if connCfg.User != "admin" {
			t.Fatalf("expected user admin, got %q", connCfg.User)
		}
		if !strings.Contains(connCfg.Password, "Action=DbConnectAdmin") || !strings.Contains(connCfg.Password, "X-Amz-Signature=") {
			t.Fatalf("expected signed DbConnectAdmin token, got %q", connCfg.Password)
		}
	})

	t.Run("custom db role", func(t *testing.T) {
		customDSN := buildDSQLDSN(endpoint, "micup_app")
		poolCfg, err := pgxpool.ParseConfig(customDSN)
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if err := configureDSQLPool(context.Background(), Config{
			DSQLMode:        true,
			DSQLEndpoint:    endpoint,
			DSQLRegion:      "us-east-2",
			DSQLUser:        "micup_app",
			DSQLCredentials: staticCreds,
		}, poolCfg); err != nil {
			t.Fatalf("configureDSQLPool: %v", err)
		}
		connCfg := poolCfg.ConnConfig.Copy()
		if err := poolCfg.BeforeConnect(context.Background(), connCfg); err != nil {
			t.Fatalf("BeforeConnect: %v", err)
		}
		if connCfg.User != "micup_app" {
			t.Fatalf("expected user micup_app, got %q", connCfg.User)
		}
		if !strings.Contains(connCfg.Password, "Action=DbConnect&") || !strings.Contains(connCfg.Password, "X-Amz-Signature=") {
			t.Fatalf("expected signed DbConnect token, got %q", connCfg.Password)
		}
	})
}
