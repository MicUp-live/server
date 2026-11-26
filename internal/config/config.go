// Package config loads and validates MicUp runtime configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	// Development identifies a local development runtime.
	Development = "development"
	// Production identifies the deployed production runtime.
	Production = "production"
	// DeveloperKeyScopeReport logs scope denials while preserving access.
	DeveloperKeyScopeReport = "report"
	// DeveloperKeyScopeEnforce rejects developer-key requests missing a scope.
	DeveloperKeyScopeEnforce = "enforce"
	// StoreBackendPostgres selects the PostgreSQL / Aurora DSQL persistence backend (default).
	StoreBackendPostgres = "postgres"
)

var developmentOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

// Config contains resolved runtime configuration independent of its source.
type Config struct {
	Environment    string
	Port           string
	AuthSecret     string
	AllowedOrigins []string
	AWSRegion      string
	// AWSEndpoint is an optional endpoint applied to every AWS service unless
	// that service has an explicit endpoint configured. It is intended for
	// local emulators such as LocalStack.
	AWSEndpoint           string
	StoreBackend          string
	DatabaseURL           string
	PostgresDSQLMode      bool
	DSQLEndpoint          string
	DSQLUser              string
	S3Endpoint            string
	SQSEndpoint           string
	SESEndpoint           string
	EmailTransport        string
	CatalogBucket         string
	CatalogBuildQueueURL  string
	ImportJobQueueURL     string
	AuthRateLimit         int
	PublicRequestLimit    int
	QueueRetentionDays    int
	PublicBaseURL         string
	DeveloperKeyScopeMode string
}

// LoadEnvironment resolves and validates full HTTP server configuration from environment variables.
func LoadEnvironment() (Config, error) {
	cfg := loadBaseEnvironment()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadWorkerEnvironment resolves configuration for background workers and CLIs
// that do not serve HTTP or sign authentication tokens (no AUTH_SECRET, PORT,
// CORS_ALLOW_ORIGIN, or PUBLIC_BASE_URL required).
func LoadWorkerEnvironment() (Config, error) {
	cfg := loadBaseEnvironment()
	if err := cfg.ValidateWorker(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadBaseEnvironment() Config {
	cfg := Config{
		Environment:           strings.TrimSpace(os.Getenv("APP_ENV")),
		Port:                  strings.TrimSpace(os.Getenv("PORT")),
		AuthSecret:            os.Getenv("AUTH_SECRET"),
		AWSRegion:             strings.TrimSpace(os.Getenv("AWS_REGION")),
		AWSEndpoint:           strings.TrimRight(strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL")), "/"),
		StoreBackend:          strings.ToLower(strings.TrimSpace(os.Getenv("STORE_BACKEND"))),
		DatabaseURL:           strings.TrimSpace(os.Getenv("DATABASE_URL")),
		PostgresDSQLMode:      strings.EqualFold(strings.TrimSpace(os.Getenv("POSTGRES_DSQL_MODE")), "true"),
		DSQLEndpoint:          strings.TrimSpace(os.Getenv("DSQL_ENDPOINT")),
		DSQLUser:              strings.TrimSpace(os.Getenv("DSQL_USER")),
		S3Endpoint:            endpointEnv("S3_ENDPOINT"),
		SQSEndpoint:           endpointEnv("SQS_ENDPOINT"),
		SESEndpoint:           endpointEnv("SES_ENDPOINT"),
		EmailTransport:        strings.ToLower(strings.TrimSpace(os.Getenv("EMAIL_TRANSPORT"))),
		CatalogBucket:         strings.TrimSpace(os.Getenv("CATALOG_ASSET_BUCKET")),
		CatalogBuildQueueURL:  strings.TrimSpace(os.Getenv("CATALOG_BUILD_QUEUE_URL")),
		ImportJobQueueURL:     strings.TrimSpace(os.Getenv("IMPORT_JOB_QUEUE_URL")),
		AuthRateLimit:         intEnv("AUTH_RATE_LIMIT_PER_MINUTE", 10),
		PublicRequestLimit:    intEnv("PUBLIC_REQUEST_LIMIT_PER_MINUTE", 12),
		QueueRetentionDays:    intEnv("QUEUE_RETENTION_DAYS", 14),
		PublicBaseURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
		DeveloperKeyScopeMode: strings.ToLower(strings.TrimSpace(os.Getenv("DEVELOPER_KEY_SCOPE_MODE"))),
	}
	if cfg.Environment == "" {
		cfg.Environment = Development
	}
	if cfg.EmailTransport == "" {
		if cfg.Environment == Production {
			cfg.EmailTransport = "ses"
		} else {
			cfg.EmailTransport = "log"
		}
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DeveloperKeyScopeMode == "" {
		cfg.DeveloperKeyScopeMode = DeveloperKeyScopeReport
	}
	if cfg.StoreBackend == "" {
		cfg.StoreBackend = StoreBackendPostgres
	}
	if cfg.AWSRegion == "" {
		cfg.AWSRegion = "us-east-2"
	}
	// A service-specific endpoint takes precedence over the shared endpoint.
	// Keep the resolved values explicit so callers do not each implement their
	// own environment-variable precedence rules.
	if cfg.S3Endpoint == "" {
		cfg.S3Endpoint = cfg.AWSEndpoint
	}
	if cfg.SQSEndpoint == "" {
		cfg.SQSEndpoint = cfg.AWSEndpoint
	}
	if cfg.SESEndpoint == "" {
		cfg.SESEndpoint = cfg.AWSEndpoint
	}
	if origins := os.Getenv("CORS_ALLOW_ORIGIN"); origins != "" {
		cfg.AllowedOrigins = splitOrigins(origins)
	} else if cfg.Environment == Development {
		cfg.AllowedOrigins = append([]string(nil), developmentOrigins...)
	}
	return cfg
}

// ValidateWorker rejects unsafe or incomplete configuration for background workers.
func (c Config) ValidateWorker() error {
	if c.Environment != Development && c.Environment != Production {
		return fmt.Errorf("APP_ENV must be %q or %q", Development, Production)
	}
	if c.StoreBackend != StoreBackendPostgres {
		return fmt.Errorf("STORE_BACKEND must be %q", StoreBackendPostgres)
	}
	if c.DatabaseURL == "" && !(c.PostgresDSQLMode && c.DSQLEndpoint != "") {
		return errors.New("DATABASE_URL or DSQL_ENDPOINT (with POSTGRES_DSQL_MODE=true) is required")
	}
	if c.AWSRegion == "" {
		return errors.New("AWS_REGION is required")
	}
	if c.QueueRetentionDays < 1 || c.QueueRetentionDays > 365 {
		return errors.New("QUEUE_RETENTION_DAYS must be between 1 and 365")
	}
	return nil
}

// Validate rejects unsafe or incomplete HTTP server runtime configuration.
func (c Config) Validate() error {
	if err := c.ValidateWorker(); err != nil {
		return err
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be a number between 1 and 65535")
	}
	if len(c.AuthSecret) < 32 {
		return errors.New("AUTH_SECRET must be at least 32 bytes")
	}
	if c.Environment == Production && len(c.AllowedOrigins) == 0 {
		return errors.New("CORS_ALLOW_ORIGIN is required in production")
	}
	if c.Environment == Production {
		for _, origin := range c.AllowedOrigins {
			if origin == "*" {
				return errors.New("CORS_ALLOW_ORIGIN cannot contain * in production")
			}
		}
		baseURL, err := url.Parse(c.PublicBaseURL)
		if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
			return errors.New("PUBLIC_BASE_URL must be an absolute HTTPS URL in production")
		}
	}
	if c.AuthRateLimit < 1 || c.PublicRequestLimit < 1 {
		return errors.New("rate limits must be at least 1 request per minute")
	}
	if c.EmailTransport != "log" && c.EmailTransport != "ses" {
		return errors.New("EMAIL_TRANSPORT must be log or ses")
	}
	if c.DeveloperKeyScopeMode != DeveloperKeyScopeReport && c.DeveloperKeyScopeMode != DeveloperKeyScopeEnforce {
		return fmt.Errorf("DEVELOPER_KEY_SCOPE_MODE must be %q or %q", DeveloperKeyScopeReport, DeveloperKeyScopeEnforce)
	}
	return nil
}

func intEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func endpointEnv(name string) string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv(name)), "/")
}

func splitOrigins(origins string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, origin := range strings.Split(origins, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		result = append(result, origin)
	}
	return result
}
