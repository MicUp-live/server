package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// ReserveCatalogVersionForDeletion atomically marks a ready, failed, or expired deleting/building
// catalog version as deleting, allocating an exclusive time-bounded lease and unique token.
func (s *Store) ReserveCatalogVersionForDeletion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("reserve catalog version for deletion: ID is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = DefaultBuildLeaseDuration
	}
	now := s.currentTime()
	leaseExpires := now.Add(leaseDuration)
	cutoff := now.Add(-leaseDuration)
	token := uuid.NewString()

	var v *models.CatalogVersion
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		v, scanErr = scanCatalogVersion(tx.QueryRow(ctx, `
			UPDATE catalog_versions
			SET status = $2,
			    lease_token = $3,
			    lease_expires_at = $4
			WHERE id = $1
			  AND (
			    status IN ($5, $6)
			    OR (status = $2 AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= $7) OR (lease_expires_at IS NULL AND COALESCE(ready_at, created_at) <= $8)))
			    OR (status = $9 AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= $7) OR (lease_expires_at IS NULL AND created_at <= $8)))
			  )
			RETURNING `+catalogVersionColumns,
			id,
			string(models.CatalogVersionDeleting),
			token,
			leaseExpires,
			string(models.CatalogVersionReady),
			string(models.CatalogVersionFailed),
			now,
			cutoff,
			string(models.CatalogVersionBuilding),
		))
		if isNotFound(scanErr) {
			var exists bool
			if chkErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_versions WHERE id = $1)`, id).Scan(&exists); chkErr == nil && !exists {
				return os.ErrNotExist
			}
			return store.ErrCatalogDeletionConflict
		}
		if scanErr != nil {
			return fmt.Errorf("reserve catalog version for deletion: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ReleaseCatalogVersionReservation atomically restores a deleting catalog version back to ready.
func (s *Store) ReleaseCatalogVersionReservation(ctx context.Context, id, leaseToken string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("release catalog version reservation: ID is required")
	}
	token := strings.TrimSpace(leaseToken)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var releasedID string
		err := tx.QueryRow(ctx, `
			UPDATE catalog_versions
			SET status = $2,
			    lease_token = '',
			    lease_expires_at = NULL
			WHERE id = $1
			  AND status = $3
			  AND ($4 = '' OR lease_token = $4 OR lease_token = '')
			RETURNING id`,
			id,
			string(models.CatalogVersionReady),
			string(models.CatalogVersionDeleting),
			token,
		).Scan(&releasedID)
		if isNotFound(err) {
			return store.ErrCatalogDeletionConflict
		}
		if err != nil {
			return fmt.Errorf("release catalog version reservation: %w", err)
		}
		return nil
	})
}

// IsCatalogVersionReferenced checks if any active songbook (excluding specified songbook IDs)
// points to the catalog version.
func (s *Store) IsCatalogVersionReferenced(ctx context.Context, id string, excludeSongbookIDs ...string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("check catalog version references: ID is required")
	}
	var referenced bool
	var err error
	if len(excludeSongbookIDs) == 0 {
		err = s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM songbooks WHERE catalog_version_id = $1)`,
			id,
		).Scan(&referenced)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM songbooks WHERE catalog_version_id = $1 AND NOT (id = ANY($2::text[])))`,
			id, excludeSongbookIDs,
		).Scan(&referenced)
	}
	if err != nil {
		return false, fmt.Errorf("check catalog version references: %w", err)
	}
	return referenced, nil
}

// PurgeCatalogVersion coordinates the shared deletion protocol.
func (s *Store) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	opt := store.PurgeCatalogOptions{
		LeaseDuration: DefaultBuildLeaseDuration,
	}
	if len(opts) > 0 {
		if opts[0].LeaseDuration > 0 {
			opt.LeaseDuration = opts[0].LeaseDuration
		}
		opt.ExcludeSongbookIDs = opts[0].ExcludeSongbookIDs
		opt.ReleaseLeaseOnError = opts[0].ReleaseLeaseOnError
	}

	version, err := s.ReserveCatalogVersionForDeletion(ctx, id, opt.LeaseDuration)
	if err != nil {
		return err
	}

	referenced, err := s.IsCatalogVersionReferenced(ctx, id, opt.ExcludeSongbookIDs...)
	if err != nil {
		_ = s.ReleaseCatalogVersionReservation(ctx, id, version.LeaseToken)
		return fmt.Errorf("verify catalog version references: %w", err)
	}
	if referenced {
		if relErr := s.ReleaseCatalogVersionReservation(ctx, id, version.LeaseToken); relErr != nil {
			return fmt.Errorf("release catalog version reservation: %w", relErr)
		}
		return store.ErrCatalogVersionInUse
	}

	if purgeAssets != nil && version.AssetPrefix != "" {
		if err := purgeAssets(version.AssetPrefix); err != nil {
			if opt.ReleaseLeaseOnError {
				_ = s.expireCatalogVersionLease(ctx, id, version.LeaseToken)
			}
			return fmt.Errorf("purge catalog assets: %w", err)
		}
	}

	if err := s.DeleteCatalogVersion(ctx, id, version.LeaseToken); err != nil {
		if opt.ReleaseLeaseOnError {
			_ = s.expireCatalogVersionLease(ctx, id, version.LeaseToken)
		}
		return fmt.Errorf("delete catalog version metadata: %w", err)
	}
	return nil
}

func (s *Store) expireCatalogVersionLease(ctx context.Context, id, leaseToken string) error {
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE catalog_versions
			SET lease_expires_at = $1
			WHERE id = $2
			  AND status = $3
			  AND (lease_token = $4 OR lease_token = '')`,
			now, id, string(models.CatalogVersionDeleting), leaseToken,
		)
		return err
	})
}

func extractCatalogIDFromPrefix(prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	trimmed = strings.TrimPrefix(trimmed, "catalogs/")
	parts := strings.Split(trimmed, "/")
	if len(parts) > 1 && parts[0] == "epochs" {
		return parts[1]
	}
	return parts[0]
}

// PurgeOrphanCatalogPrefix coordinates orphan S3 prefix cleanup with the publication exclusion protocol.
func (s *Store) PurgeOrphanCatalogPrefix(ctx context.Context, prefix string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return fmt.Errorf("purge orphan catalog prefix: prefix is required")
	}
	catalogID := extractCatalogIDFromPrefix(prefix)
	opt := resolvePurgeCatalogOptions(opts)

	existing, err := s.GetCatalogVersion(ctx, catalogID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check catalog version for orphan prefix: %w", err)
	}
	if err == nil {
		return s.purgeExistingCatalogPrefix(ctx, catalogID, prefix, existing, purgeAssets, opt)
	}

	token := uuid.NewString()
	if err := s.reserveOrphanCatalogPrefix(ctx, catalogID, prefix, token, opt.LeaseDuration); err != nil {
		return err
	}

	referenced, refErr := s.IsCatalogVersionReferenced(ctx, catalogID)
	if refErr != nil || referenced {
		_ = s.DeleteCatalogVersion(ctx, catalogID, token)
		if referenced {
			return store.ErrCatalogVersionInUse
		}
		return fmt.Errorf("verify references for orphan prefix: %w", refErr)
	}

	if err := callPurgeAssets(purgeAssets, prefix); err != nil {
		return err
	}

	if err := s.DeleteCatalogVersion(ctx, catalogID, token); err != nil {
		return fmt.Errorf("delete orphan reservation: %w", err)
	}
	return nil
}

func resolvePurgeCatalogOptions(opts []store.PurgeCatalogOptions) store.PurgeCatalogOptions {
	opt := store.PurgeCatalogOptions{LeaseDuration: DefaultBuildLeaseDuration}
	if len(opts) > 0 && opts[0].LeaseDuration > 0 {
		opt.LeaseDuration = opts[0].LeaseDuration
	}
	return opt
}

func (s *Store) purgeExistingCatalogPrefix(
	ctx context.Context,
	catalogID string,
	prefix string,
	existing *models.CatalogVersion,
	purgeAssets func(assetPrefix string) error,
	opt store.PurgeCatalogOptions,
) error {
	now := s.currentTime()
	if existing.Status == models.CatalogVersionBuilding && existing.LeaseExpiresAt.After(now) {
		return store.ErrCatalogDeletionConflict
	}
	existingPrefix := strings.Trim(existing.AssetPrefix, "/")
	targetPrefix := strings.Trim(prefix, "/")
	isExact := existingPrefix != "" && existingPrefix == targetPrefix
	isDescendant := existingPrefix != "" && strings.HasPrefix(targetPrefix, existingPrefix+"/")
	isAncestor := targetPrefix != "" && strings.HasPrefix(existingPrefix, targetPrefix+"/")

	if isExact {
		return s.PurgeCatalogVersion(ctx, catalogID, purgeAssets, opt)
	}

	referenced, refErr := s.IsCatalogVersionReferenced(ctx, catalogID)
	if refErr != nil {
		return fmt.Errorf("verify references for orphan prefix: %w", refErr)
	}
	if referenced && (isExact || isDescendant || isAncestor) {
		return store.ErrCatalogVersionInUse
	}
	if existing.Status == models.CatalogVersionReady && (isDescendant || isAncestor) {
		return s.PurgeCatalogVersion(ctx, catalogID, purgeAssets, opt)
	}
	return callPurgeAssets(purgeAssets, prefix)
}

func (s *Store) reserveOrphanCatalogPrefix(
	ctx context.Context,
	catalogID string,
	prefix string,
	token string,
	leaseDuration time.Duration,
) error {
	now := s.currentTime()
	leaseExpires := now.Add(leaseDuration)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `
			INSERT INTO catalog_versions (
				id, schema_version, status, song_count, asset_prefix,
				failure_reason, lease_token, lease_expires_at, created_at, ready_at
			) VALUES ($1, 1, $2, 0, $3, '', $4, $5, $6, NULL)`,
			catalogID, string(models.CatalogVersionDeleting), prefix, token, leaseExpires, now,
		)
		if isUniqueViolation(execErr) {
			return store.ErrCatalogDeletionConflict
		}
		if execErr != nil {
			return fmt.Errorf("reserve orphan prefix placeholder: %w", execErr)
		}
		return nil
	})
}

func callPurgeAssets(purgeAssets func(assetPrefix string) error, prefix string) error {
	if purgeAssets == nil {
		return nil
	}
	if err := purgeAssets(prefix); err != nil {
		return fmt.Errorf("purge orphan assets: %w", err)
	}
	return nil
}
