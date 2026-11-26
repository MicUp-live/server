package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// DefaultBuildLeaseDuration is the default lease granted to a building catalog version.
const DefaultBuildLeaseDuration = 10 * time.Minute

const catalogVersionColumns = `id, schema_version, status, song_count, asset_prefix,
	failure_reason, lease_token, lease_expires_at, created_at, ready_at`

func scanCatalogVersion(row pgx.Row) (*models.CatalogVersion, error) {
	var (
		v            models.CatalogVersion
		status       string
		leaseExpires *time.Time
		readyAt      *time.Time
	)
	err := row.Scan(
		&v.ID,
		&v.SchemaVersion,
		&status,
		&v.SongCount,
		&v.AssetPrefix,
		&v.FailureReason,
		&v.LeaseToken,
		&leaseExpires,
		&v.CreatedAt,
		&readyAt,
	)
	if err != nil {
		return nil, err
	}
	v.Status = models.CatalogVersionStatus(status)
	v.CreatedAt = v.CreatedAt.UTC()
	v.LeaseExpiresAt = derefTime(leaseExpires)
	v.ReadyAt = derefTime(readyAt)
	return &v, nil
}

// CreateCatalogVersion records a building catalog version.
func (s *Store) CreateCatalogVersion(ctx context.Context, version *models.CatalogVersion) error {
	if strings.TrimSpace(version.ID) == "" {
		return fmt.Errorf("create catalog version: ID is required")
	}
	if version.SchemaVersion <= 0 {
		return fmt.Errorf("create catalog version: schema version is required")
	}
	if version.Status == "" {
		version.Status = models.CatalogVersionBuilding
	}
	if version.Status != models.CatalogVersionBuilding {
		return fmt.Errorf("create catalog version: status must be building")
	}
	created := version.CreatedAt.UTC()
	if created.IsZero() {
		created = s.currentTime()
	}
	version.CreatedAt = created
	if version.LeaseExpiresAt.IsZero() {
		version.LeaseExpiresAt = created.Add(DefaultBuildLeaseDuration)
	} else {
		version.LeaseExpiresAt = version.LeaseExpiresAt.UTC()
	}
	if version.LeaseToken == "" {
		version.LeaseToken = uuid.NewString()
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO catalog_versions (
				id, schema_version, status, song_count, asset_prefix,
				failure_reason, lease_token, lease_expires_at, created_at, ready_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			version.ID,
			version.SchemaVersion,
			string(version.Status),
			version.SongCount,
			version.AssetPrefix,
			version.FailureReason,
			version.LeaseToken,
			nullTime(version.LeaseExpiresAt),
			version.CreatedAt,
			nullTime(version.ReadyAt),
		)
		if isUniqueViolation(err) {
			return os.ErrExist
		}
		if err != nil {
			return fmt.Errorf("create catalog version: %w", err)
		}
		return nil
	})
}

// GetCatalogVersion loads one catalog version by its content hash.
func (s *Store) GetCatalogVersion(ctx context.Context, id string) (*models.CatalogVersion, error) {
	v, err := scanCatalogVersion(s.pool.QueryRow(ctx,
		`SELECT `+catalogVersionColumns+` FROM catalog_versions WHERE id = $1`,
		id,
	))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get catalog version: %w", err)
	}
	return v, nil
}

// ListCatalogVersions returns all catalog version metadata for maintenance jobs.
func (s *Store) ListCatalogVersions(ctx context.Context) ([]*models.CatalogVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+catalogVersionColumns+` FROM catalog_versions ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list catalog versions: %w", err)
	}
	defer rows.Close()
	var versions []*models.CatalogVersion
	for rows.Next() {
		v, err := scanCatalogVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// DeleteCatalogVersion removes metadata after its immutable assets are removed.
func (s *Store) DeleteCatalogVersion(ctx context.Context, id string, leaseToken ...string) error {
	token := ""
	if len(leaseToken) > 0 {
		token = strings.TrimSpace(leaseToken[0])
	}
	if token != "" {
		return s.withTx(ctx, func(tx pgx.Tx) error {
			var deletedID string
			err := tx.QueryRow(ctx, `
				DELETE FROM catalog_versions
				WHERE id = $1
				  AND status = $2
				  AND (lease_token = $3 OR lease_token = '')
				RETURNING id`,
				id, string(models.CatalogVersionDeleting), token,
			).Scan(&deletedID)
			if isNotFound(err) || isForeignKeyViolation(err) {
				return store.ErrCatalogDeletionConflict
			}
			return err
		})
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM catalog_versions WHERE id = $1`, id)
		if isForeignKeyViolation(err) {
			return store.ErrCatalogVersionInUse
		}
		return err
	})
}

// MarkCatalogVersionReady makes a fully uploaded catalog eligible for publishing.
func (s *Store) MarkCatalogVersionReady(ctx context.Context, id, assetPrefix string, songCount int, leaseToken ...string) (*models.CatalogVersion, error) {
	if strings.TrimSpace(assetPrefix) == "" || songCount < 0 {
		return nil, fmt.Errorf("mark catalog version ready: asset prefix and non-negative song count are required")
	}
	now := s.currentTime()
	token := ""
	if len(leaseToken) > 0 {
		token = strings.TrimSpace(leaseToken[0])
	}
	var v *models.CatalogVersion
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		v, scanErr = scanCatalogVersion(tx.QueryRow(ctx, `
			UPDATE catalog_versions
			SET status = $2,
			    asset_prefix = $3,
			    song_count = $4,
			    ready_at = $5,
			    failure_reason = '',
			    lease_token = '',
			    lease_expires_at = NULL
			WHERE id = $1
			  AND status = $6
			  AND ($7 = '' OR lease_token = $7 OR lease_token = '')
			RETURNING `+catalogVersionColumns,
			id, string(models.CatalogVersionReady), assetPrefix, songCount, now,
			string(models.CatalogVersionBuilding), token,
		))
		if isNotFound(scanErr) {
			return os.ErrNotExist
		}
		if scanErr != nil {
			return fmt.Errorf("mark catalog version ready: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// FailCatalogVersion records a terminal build failure without publishing it.
func (s *Store) FailCatalogVersion(ctx context.Context, id, reason string, leaseToken ...string) (*models.CatalogVersion, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("fail catalog version: reason is required")
	}
	token := ""
	if len(leaseToken) > 0 {
		token = strings.TrimSpace(leaseToken[0])
	}
	var v *models.CatalogVersion
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		v, scanErr = scanCatalogVersion(tx.QueryRow(ctx, `
			UPDATE catalog_versions
			SET status = $2,
			    failure_reason = $3,
			    lease_token = '',
			    lease_expires_at = NULL
			WHERE id = $1
			  AND status = $4
			  AND ($5 = '' OR lease_token = $5 OR lease_token = '')
			RETURNING `+catalogVersionColumns,
			id, string(models.CatalogVersionFailed), reason,
			string(models.CatalogVersionBuilding), token,
		))
		if isNotFound(scanErr) {
			return os.ErrNotExist
		}
		if scanErr != nil {
			return fmt.Errorf("fail catalog version: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ReclaimCatalogVersion atomically resets a failed or abandoned catalog version to building status
// with a new lease token and expiration.
func (s *Store) ReclaimCatalogVersion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("reclaim catalog version: ID is required")
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
			    lease_expires_at = $4,
			    failure_reason = '',
			    asset_prefix = '',
			    song_count = 0,
			    ready_at = NULL
			WHERE id = $1
			  AND (
			    status = $5
			    OR (status = $2 AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= $6) OR (lease_expires_at IS NULL AND created_at <= $7)))
			    OR (status = $8 AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= $6) OR (lease_expires_at IS NULL AND COALESCE(ready_at, created_at) <= $7)))
			  )
			RETURNING `+catalogVersionColumns,
			id,
			string(models.CatalogVersionBuilding),
			token,
			leaseExpires,
			string(models.CatalogVersionFailed),
			now,
			cutoff,
			string(models.CatalogVersionDeleting),
		))
		if isNotFound(scanErr) {
			return store.ErrReclaimConflict
		}
		if scanErr != nil {
			return fmt.Errorf("reclaim catalog version: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// PublishCatalogVersion atomically points a songbook at an already-ready version.
func (s *Store) PublishCatalogVersion(ctx context.Context, songbookID, catalogVersionID string, opts ...store.PublicationOptions) error {
	var opt store.PublicationOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	hasExpectedBase := opt.ExpectedBaseVersion != nil
	expectedBase := ""
	if hasExpectedBase {
		expectedBase = *opt.ExpectedBaseVersion
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		if opt.JobID != "" {
			if err := checkJobReceiptBeforePublish(ctx, tx, opt.JobID); err != nil {
				return err
			}
		}

		var (
			verStatus string
			songCount int
		)
		err := tx.QueryRow(ctx,
			`SELECT status, song_count FROM catalog_versions WHERE id = $1 FOR UPDATE`,
			catalogVersionID,
		).Scan(&verStatus, &songCount)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("get catalog version: %w", err)
		}
		if verStatus != string(models.CatalogVersionReady) {
			return fmt.Errorf("publish catalog version: catalog %s status is %s: %w", catalogVersionID, verStatus, os.ErrNotExist)
		}

		now := s.currentTime()
		var updatedSBID string
		err = tx.QueryRow(ctx, `
			UPDATE songbooks
			SET catalog_version_id = $1,
			    song_count = $2,
			    updated_at = $3,
			    catalog_generation = CASE WHEN $4 > 0 THEN $4 ELSE catalog_generation END
			WHERE id = $5
			  AND deleting = FALSE
			  AND ($4 = 0 OR catalog_generation <= $4)
			  AND ($6 = FALSE OR COALESCE(catalog_version_id, '') = $7)
			RETURNING id`,
			catalogVersionID, songCount, now, opt.JobGeneration,
			songbookID, hasExpectedBase, expectedBase,
		).Scan(&updatedSBID)
		if isNotFound(err) {
			return classifySongbookPublishConflict(ctx, tx, songbookID, opt)
		}
		if err != nil {
			return fmt.Errorf("publish catalog version: %w", err)
		}

		if opt.JobID != "" {
			return completeImportJobOnPublish(ctx, tx, songbookID, catalogVersionID, now, opt)
		}
		return nil
	})
}

func checkJobReceiptBeforePublish(ctx context.Context, tx pgx.Tx, jobID string) error {
	var jobStatus string
	err := tx.QueryRow(ctx, `SELECT status FROM import_jobs WHERE id = $1`, jobID).Scan(&jobStatus)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read import job receipt %s: %w", jobID, err)
	}
	if jobStatus == string(models.ImportJobCompleted) {
		return store.ErrImportJobAlreadyCompleted
	}
	if jobStatus == string(models.ImportJobFailed) {
		return store.ErrImportJobTerminal
	}
	return nil
}

func completeImportJobOnPublish(ctx context.Context, tx pgx.Tx, songbookID, catalogVersionID string, now time.Time, opt store.PublicationOptions) error {
	var updatedJobID string
	err := tx.QueryRow(ctx, `
		UPDATE import_jobs
		SET status = $1,
		    completed_at = $2,
		    catalog_version_id = $3,
		    processed_songs = $4,
		    updated_at = $2,
		    error = '',
		    dispatch_claim_token = '',
		    dispatch_claim_expires_at = NULL
		WHERE id = $5
		  AND songbook_id = $6
		  AND generation = $7
		  AND status IN ($8, $9)
		RETURNING id`,
		string(models.ImportJobCompleted), now, catalogVersionID, opt.ProcessedSongs,
		opt.JobID, songbookID, opt.JobGeneration,
		string(models.ImportJobProcessing), string(models.ImportJobQueued),
	).Scan(&updatedJobID)
	if isNotFound(err) {
		var jobStatus string
		jErr := tx.QueryRow(ctx, `SELECT status FROM import_jobs WHERE id = $1`, opt.JobID).Scan(&jobStatus)
		if jErr != nil && !isNotFound(jErr) {
			return fmt.Errorf("reload import job receipt %s: %w", opt.JobID, jErr)
		}
		if jErr == nil {
			if jobStatus == string(models.ImportJobCompleted) {
				return store.ErrImportJobAlreadyCompleted
			}
			if jobStatus == string(models.ImportJobFailed) {
				return store.ErrImportJobTerminal
			}
		}
		return store.ErrStalePublication
	}
	if err != nil {
		return fmt.Errorf("complete import job on publish: %w", err)
	}
	return nil
}

func classifySongbookPublishConflict(ctx context.Context, tx pgx.Tx, songbookID string, opt store.PublicationOptions) error {
	var (
		catalogVersionID *string
		catalogGen       int64
		deleting         bool
	)
	err := tx.QueryRow(ctx,
		`SELECT catalog_version_id, catalog_generation, deleting FROM songbooks WHERE id = $1`,
		songbookID,
	).Scan(&catalogVersionID, &catalogGen, &deleting)
	if isNotFound(err) {
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("load songbook on publish conflict: %w", err)
	}
	if deleting {
		return store.ErrSongbookDeleting
	}
	if opt.JobGeneration > 0 && catalogGen > opt.JobGeneration {
		return fmt.Errorf("%w (songbook generation %d > job generation %d): %w",
			store.ErrPublicationSuperseded, catalogGen, opt.JobGeneration, store.ErrStalePublication)
	}
	if opt.ExpectedBaseVersion != nil {
		base := *opt.ExpectedBaseVersion
		cur := derefString(catalogVersionID)
		if base != cur {
			return fmt.Errorf("%w (songbook catalog %s != expected %s): %w",
				store.ErrPublicationBaseConflict, cur, base, store.ErrStalePublication)
		}
	}
	return store.ErrStalePublication
}
