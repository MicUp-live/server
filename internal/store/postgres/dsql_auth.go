package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	dsqlauth "github.com/aws/aws-sdk-go-v2/feature/dsql/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultDSQLUser        = "admin"
	defaultDSQLMaxLifetime = 45 * time.Minute
	defaultDSQLMaxIdle     = 10 * time.Minute
)

func configureDSQLPool(ctx context.Context, cfg Config, poolCfg *pgxpool.Config) error {
	endpoint := strings.TrimSpace(cfg.DSQLEndpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(poolCfg.ConnConfig.Host)
	}
	if endpoint == "" {
		return errors.New("DSQL endpoint is required when DSQLMode is enabled")
	}
	region := strings.TrimSpace(cfg.DSQLRegion)
	if region == "" {
		return errors.New("DSQL region is required when DSQLMode is enabled")
	}
	user := strings.TrimSpace(cfg.DSQLUser)
	if user == "" {
		user = strings.TrimSpace(poolCfg.ConnConfig.User)
	}
	if user == "" {
		user = defaultDSQLUser
	}

	creds := cfg.DSQLCredentials
	if creds == nil {
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return fmt.Errorf("load AWS config for DSQL IAM auth: %w", err)
		}
		creds = awsCfg.Credentials
	}

	if poolCfg.MaxConnLifetime == 0 || poolCfg.MaxConnLifetime > defaultDSQLMaxLifetime {
		poolCfg.MaxConnLifetime = defaultDSQLMaxLifetime
	}
	if poolCfg.MaxConnIdleTime == 0 || poolCfg.MaxConnIdleTime > defaultDSQLMaxIdle {
		poolCfg.MaxConnIdleTime = defaultDSQLMaxIdle
	}

	poolCfg.BeforeConnect = func(connectCtx context.Context, connCfg *pgx.ConnConfig) error {
		var (
			token string
			err   error
		)
		if user == defaultDSQLUser {
			token, err = dsqlauth.GenerateDBConnectAdminAuthToken(connectCtx, endpoint, region, creds)
		} else {
			token, err = dsqlauth.GenerateDbConnectAuthToken(connectCtx, endpoint, region, creds)
		}
		if err != nil {
			return fmt.Errorf("generate Aurora DSQL IAM auth token: %w", err)
		}
		connCfg.User = user
		connCfg.Password = token
		return nil
	}
	return nil
}

func buildDSQLDSN(endpoint, user string) string {
	u := strings.TrimSpace(user)
	if u == "" {
		u = defaultDSQLUser
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.User(u),
		Host:     strings.TrimSpace(endpoint) + ":5432",
		Path:     "/postgres",
		RawQuery: "sslmode=require",
	}).String()
}
