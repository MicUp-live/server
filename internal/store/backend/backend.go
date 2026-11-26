// Package backend constructs the MicUp persistence store
// (PostgreSQL / Aurora DSQL) behind a unified interface.
package backend

import (
	"context"
	"fmt"

	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	pgstore "micup.live/server/internal/store/postgres"
)

// Store combines all persistence interfaces with lifecycle cleanup.
type Store interface {
	store.AuthStore
	store.FavoritesStore
	store.PasskeyStore
	store.DeveloperKeyStore
	store.SongbookStore
	store.CatalogStore
	store.CatalogChangeStore
	store.ImportJobStore
	store.EventStore
	store.RequestStore
	store.SongbookAccessStore
	store.RateLimitStore
	ListCatalogVersions(ctx context.Context) ([]*models.CatalogVersion, error)
	Close() error
}

// Open connects to PostgreSQL / Aurora DSQL and ensures local development
// schemas when ensureDevSchema is true.
func Open(ctx context.Context, cfg config.Config, ensureDevSchema bool) (Store, error) {
	if cfg.StoreBackend != "" && cfg.StoreBackend != config.StoreBackendPostgres {
		return nil, fmt.Errorf("unsupported STORE_BACKEND %q", cfg.StoreBackend)
	}
	s, err := pgstore.New(ctx, pgstore.Config{
		DSN:          cfg.DatabaseURL,
		DSQLMode:     cfg.PostgresDSQLMode,
		DSQLEndpoint: cfg.DSQLEndpoint,
		DSQLRegion:   cfg.AWSRegion,
		DSQLUser:     cfg.DSQLUser,
	})
	if err != nil {
		return nil, err
	}
	if ensureDevSchema {
		if err := s.EnsureSchema(ctx); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}
