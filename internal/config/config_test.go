package config

import (
	"strings"
	"testing"
)

const (
	testAuthSecret  = "test-signing-secret-with-at-least-32-bytes"
	testDatabaseURL = "postgres://micup:local@127.0.0.1:5432/micup?sslmode=disable"
)

func setTestPostgresEnv(t *testing.T) {
	t.Helper()
	t.Setenv("STORE_BACKEND", "")
	t.Setenv("DATABASE_URL", testDatabaseURL)
	t.Setenv("POSTGRES_DSQL_MODE", "")
	t.Setenv("DSQL_ENDPOINT", "")
	t.Setenv("DSQL_USER", "")
}

func TestLoadEnvironmentUsesDevelopmentDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("PORT", "")
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("SQS_ENDPOINT", "")
	t.Setenv("SES_ENDPOINT", "")
	t.Setenv("EMAIL_TRANSPORT", "")
	t.Setenv("CATALOG_ASSET_BUCKET", "")
	t.Setenv("DEVELOPER_KEY_SCOPE_MODE", "")
	setTestPostgresEnv(t)

	cfg, err := LoadEnvironment()
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if cfg.Environment != Development || cfg.Port != "8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.StoreBackend != StoreBackendPostgres || cfg.AWSRegion != "us-east-2" {
		t.Fatalf("unexpected persistence/region defaults: %+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("development origins = %v; want localhost defaults", cfg.AllowedOrigins)
	}
	if cfg.CatalogBucket != "" {
		t.Fatalf("catalog bucket = %q; want empty local default", cfg.CatalogBucket)
	}
	if cfg.EmailTransport != "log" {
		t.Fatalf("development email transport = %q; want log", cfg.EmailTransport)
	}
	if cfg.DeveloperKeyScopeMode != DeveloperKeyScopeReport {
		t.Fatalf("developer key scope mode = %q; want report", cfg.DeveloperKeyScopeMode)
	}
}

func TestLoadEnvironmentParsesDeveloperKeyScopeMode(t *testing.T) {
	t.Setenv("APP_ENV", Development)
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("DEVELOPER_KEY_SCOPE_MODE", DeveloperKeyScopeEnforce)
	setTestPostgresEnv(t)

	cfg, err := LoadEnvironment()
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if cfg.DeveloperKeyScopeMode != DeveloperKeyScopeEnforce {
		t.Fatalf("developer key scope mode = %q; want enforce", cfg.DeveloperKeyScopeMode)
	}
	t.Setenv("DEVELOPER_KEY_SCOPE_MODE", "invalid")
	if _, err := LoadEnvironment(); err == nil {
		t.Fatal("configuration accepted an invalid developer key scope mode")
	}
}

func TestLoadEnvironmentResolvesServiceEndpoints(t *testing.T) {
	t.Setenv("APP_ENV", Development)
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("AWS_ENDPOINT_URL", "http://127.0.0.1:4566/")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("SQS_ENDPOINT", "http://localhost:4566/")
	t.Setenv("SES_ENDPOINT", "")
	setTestPostgresEnv(t)

	cfg, err := LoadEnvironment()
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if cfg.S3Endpoint != "http://127.0.0.1:4566" || cfg.SESEndpoint != "http://127.0.0.1:4566" {
		t.Fatalf("shared endpoint fallback not applied: S3=%q SES=%q", cfg.S3Endpoint, cfg.SESEndpoint)
	}
	if cfg.SQSEndpoint != "http://localhost:4566" {
		t.Fatalf("explicit SQS endpoint = %q", cfg.SQSEndpoint)
	}
}

func TestLoadEnvironmentParsesUniqueOrigins(t *testing.T) {
	t.Setenv("APP_ENV", Production)
	t.Setenv("PORT", "9000")
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("CORS_ALLOW_ORIGIN", " https://micup.live,https://admin.micup.live,https://micup.live ")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("CATALOG_ASSET_BUCKET", "catalog-assets")
	t.Setenv("PUBLIC_BASE_URL", "https://micup.live")
	t.Setenv("EMAIL_TRANSPORT", "")
	setTestPostgresEnv(t)

	cfg, err := LoadEnvironment()
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://micup.live" {
		t.Fatalf("allowed origins = %v", cfg.AllowedOrigins)
	}
	if cfg.CatalogBucket != "catalog-assets" {
		t.Fatalf("catalog bucket = %q", cfg.CatalogBucket)
	}
	if cfg.EmailTransport != "ses" {
		t.Fatalf("production email transport = %q; want ses", cfg.EmailTransport)
	}
}

func TestLoadEnvironmentRejectsUnknownEmailTransport(t *testing.T) {
	t.Setenv("APP_ENV", Development)
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("EMAIL_TRANSPORT", "smtp")
	setTestPostgresEnv(t)

	err := func() error {
		_, err := LoadEnvironment()
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "EMAIL_TRANSPORT") {
		t.Fatalf("expected EMAIL_TRANSPORT validation error, got: %v", err)
	}
}

func TestProductionRequiresExplicitOrigins(t *testing.T) {
	t.Setenv("APP_ENV", Production)
	t.Setenv("PORT", "8080")
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("PUBLIC_BASE_URL", "https://micup.live")
	setTestPostgresEnv(t)

	_, err := LoadEnvironment()
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOW_ORIGIN") {
		t.Fatalf("expected CORS_ALLOW_ORIGIN validation error, got: %v", err)
	}
}

func TestProductionRejectsWildcardOrigin(t *testing.T) {
	t.Setenv("APP_ENV", Production)
	t.Setenv("PORT", "8080")
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("CORS_ALLOW_ORIGIN", "*")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("PUBLIC_BASE_URL", "https://micup.live")
	setTestPostgresEnv(t)

	_, err := LoadEnvironment()
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOW_ORIGIN") {
		t.Fatalf("expected wildcard CORS_ALLOW_ORIGIN validation error, got: %v", err)
	}
}

func TestLoadWorkerEnvironmentDoesNotRequireHTTPSettings(t *testing.T) {
	t.Setenv("APP_ENV", Production)
	t.Setenv("AUTH_SECRET", "")
	t.Setenv("CORS_ALLOW_ORIGIN", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("PORT", "invalid")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("CATALOG_ASSET_BUCKET", "micup-app-prod")
	setTestPostgresEnv(t)

	cfg, err := LoadWorkerEnvironment()
	if err != nil {
		t.Fatalf("LoadWorkerEnvironment failed: %v", err)
	}
	if cfg.Environment != Production || cfg.StoreBackend != StoreBackendPostgres || cfg.CatalogBucket != "micup-app-prod" {
		t.Fatalf("unexpected worker config: %+v", cfg)
	}

	t.Setenv("APP_ENV", "staging")
	if _, err := LoadWorkerEnvironment(); err == nil {
		t.Fatal("LoadWorkerEnvironment accepted invalid APP_ENV")
	}

	t.Setenv("APP_ENV", Production)
	t.Setenv("QUEUE_RETENTION_DAYS", "0")
	if _, err := LoadWorkerEnvironment(); err == nil {
		t.Fatal("LoadWorkerEnvironment accepted invalid QUEUE_RETENTION_DAYS")
	}
}

func TestValidateWorkerRequiresDatabaseOrDSQL(t *testing.T) {
	t.Setenv("APP_ENV", Development)
	t.Setenv("AUTH_SECRET", testAuthSecret)
	t.Setenv("STORE_BACKEND", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_DSQL_MODE", "")
	t.Setenv("DSQL_ENDPOINT", "")

	if _, err := LoadWorkerEnvironment(); err == nil {
		t.Fatal("LoadWorkerEnvironment succeeded without DATABASE_URL or DSQL config")
	}
	if _, err := LoadEnvironment(); err == nil {
		t.Fatal("LoadEnvironment succeeded without DATABASE_URL or DSQL config")
	}

	t.Setenv("POSTGRES_DSQL_MODE", "true")
	if _, err := LoadWorkerEnvironment(); err == nil {
		t.Fatal("LoadWorkerEnvironment succeeded with POSTGRES_DSQL_MODE=true but empty DSQL_ENDPOINT")
	}

	t.Setenv("DSQL_ENDPOINT", "abc.dsql.us-east-2.on.aws")
	cfg, err := LoadWorkerEnvironment()
	if err != nil {
		t.Fatalf("LoadWorkerEnvironment with DSQL config failed: %v", err)
	}
	if !cfg.PostgresDSQLMode || cfg.DSQLEndpoint != "abc.dsql.us-east-2.on.aws" {
		t.Fatalf("unexpected DSQL config: %+v", cfg)
	}

	t.Setenv("STORE_BACKEND", "dynamo")
	if _, err := LoadWorkerEnvironment(); err == nil {
		t.Fatal("LoadWorkerEnvironment accepted retired STORE_BACKEND=dynamo")
	}
}
