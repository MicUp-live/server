package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const importJobColumns = `id, songbook_id, manifest_key, status, total_songs, processed_songs,
	prune_missing, generation, catalog_version_id, dispatch_ready, dispatch_claim_token,
	dispatch_claim_expires_at, dispatch_attempts, dispatched_at, error,
	created_at, updated_at, completed_at`

func scanImportJob(row pgx.Row) (*models.ImportJob, error) {
	var (
		job              models.ImportJob
		status           string
		catalogVersionID *string
		claimExpiresAt   *time.Time
		dispatchedAt     *time.Time
		completedAt      *time.Time
	)
	err := row.Scan(
		&job.ID,
		&job.SongbookID,
		&job.ManifestKey,
		&status,
		&job.TotalSongs,
		&job.ProcessedSongs,
		&job.PruneMissing,
		&job.Generation,
		&catalogVersionID,
		&job.DispatchReady,
		&job.DispatchClaimToken,
		&claimExpiresAt,
		&job.DispatchAttempts,
		&dispatchedAt,
		&job.Error,
		&job.CreatedAt,
		&job.UpdatedAt,
		&completedAt,
	)
	if err != nil {
		return nil, err
	}
	job.Status = models.ImportJobStatus(status)
	job.CatalogVersionID = derefString(catalogVersionID)
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	job.CompletedAt = derefTime(completedAt)
	if claimExpiresAt != nil {
		t := claimExpiresAt.UTC()
		job.DispatchClaimExpiresAt = &t
	}
	if dispatchedAt != nil {
		t := dispatchedAt.UTC()
		job.DispatchedAt = &t
	} else if job.Status == models.ImportJobProcessing || job.Status == models.ImportJobCompleted {
		t := job.CreatedAt
		job.DispatchedAt = &t
	}
	return &job, nil
}

func scanImportJobs(rows pgx.Rows) ([]*models.ImportJob, error) {
	defer rows.Close()
	var jobs []*models.ImportJob
	for rows.Next() {
		job, err := scanImportJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func classifyTerminalJobStatus(status models.ImportJobStatus) error {
	switch status {
	case models.ImportJobCompleted:
		return store.ErrImportJobAlreadyCompleted
	case models.ImportJobFailed:
		return store.ErrImportJobTerminal
	default:
		return nil
	}
}

// CreateImportJob inserts a new import job while fencing against deleting songbooks
// and revoked collaborator edit grants.
func (s *Store) CreateImportJob(ctx context.Context, job *models.ImportJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.SongbookID) == "" {
		return fmt.Errorf("create import job: id and songbook are required")
	}
	if job.Status == "" {
		job.Status = models.ImportJobPending
	}
	now := s.currentTime()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	} else {
		job.CreatedAt = job.CreatedAt.UTC()
	}
	job.UpdatedAt = now

	return s.withTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM import_jobs WHERE id = $1)`, job.ID).Scan(&exists); err != nil {
			return fmt.Errorf("create import job: %w", err)
		}
		if exists {
			return os.ErrExist
		}
		if err := checkSongbookWriteFence(ctx, tx, job.SongbookID); err != nil {
			return fmt.Errorf("create import job: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO import_jobs (
				id, songbook_id, manifest_key, status, total_songs, processed_songs,
				prune_missing, generation, catalog_version_id, dispatch_ready, dispatch_claim_token,
				dispatch_claim_expires_at, dispatch_attempts, dispatched_at, error,
				created_at, updated_at, completed_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			job.ID,
			job.SongbookID,
			job.ManifestKey,
			string(job.Status),
			job.TotalSongs,
			job.ProcessedSongs,
			job.PruneMissing,
			job.Generation,
			nullString(job.CatalogVersionID),
			job.DispatchReady,
			job.DispatchClaimToken,
			nullTimePtr(job.DispatchClaimExpiresAt),
			job.DispatchAttempts,
			nullTimePtr(job.DispatchedAt),
			job.Error,
			job.CreatedAt,
			job.UpdatedAt,
			nullTime(job.CompletedAt),
		)
		if isUniqueViolation(err) {
			return os.ErrExist
		}
		if err != nil {
			return fmt.Errorf("create import job: %w", err)
		}
		return nil
	})
}

// GetImportJob loads one import job by ID.
func (s *Store) GetImportJob(ctx context.Context, id string) (*models.ImportJob, error) {
	job, err := scanImportJob(s.pool.QueryRow(ctx,
		`SELECT `+importJobColumns+` FROM import_jobs WHERE id = $1`,
		id,
	))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get import job: %w", err)
	}
	return job, nil
}

// ListImportJobs returns all durable import jobs for garbage-collection audits.
func (s *Store) ListImportJobs(ctx context.Context) ([]*models.ImportJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+importJobColumns+` FROM import_jobs ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list import jobs: %w", err)
	}
	return scanImportJobs(rows)
}

// DeleteImportJob removes terminal import metadata after its retention window.
func (s *Store) DeleteImportJob(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		return err
	})
}

func getImportJobTx(ctx context.Context, tx pgx.Tx, id string) (*models.ImportJob, error) {
	job, err := scanImportJob(tx.QueryRow(ctx,
		`SELECT `+importJobColumns+` FROM import_jobs WHERE id = $1`,
		id,
	))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get import job: %w", err)
	}
	return job, nil
}

// LatestImportJob returns the newest job for a songbook.
func (s *Store) LatestImportJob(ctx context.Context, songbookID string) (*models.ImportJob, error) {
	job, err := scanImportJob(s.pool.QueryRow(ctx,
		`SELECT `+importJobColumns+` FROM import_jobs WHERE songbook_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		songbookID,
	))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("latest import job: %w", err)
	}
	return job, nil
}

// UpdateImportJob updates mutable non-terminal fields while protecting committed generations.
func (s *Store) UpdateImportJob(ctx context.Context, job *models.ImportJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("update import job: id is required")
	}
	if job.Status == models.ImportJobCompleted {
		return fmt.Errorf("update import job %s: completion requires atomic publication transaction: %w", job.ID, store.ErrImportJobNotReady)
	}
	now := s.currentTime()
	job.UpdatedAt = now

	return s.withTx(ctx, func(tx pgx.Tx) error {
		var returnedGen int64
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET status = CASE WHEN $2 <> '' THEN $2 ELSE status END,
			    songbook_id = CASE WHEN $3 <> '' THEN $3 ELSE songbook_id END,
			    manifest_key = CASE WHEN $4 <> '' THEN $4 ELSE manifest_key END,
			    total_songs = CASE WHEN $5 > 0 THEN $5 ELSE total_songs END,
			    processed_songs = CASE WHEN $6 >= 0 THEN $6 ELSE processed_songs END,
			    prune_missing = CASE WHEN $7 THEN TRUE ELSE prune_missing END,
			    error = $8,
			    completed_at = $9,
			    generation = CASE WHEN $10 > 0 THEN $10 ELSE generation END,
			    updated_at = $11
			WHERE id = $1
			  AND status <> $12
			  AND ($2 = '' OR status <> $13)
			  AND ($10 = 0 OR generation = 0 OR generation = $10)
			RETURNING generation`,
			job.ID,
			string(job.Status),
			job.SongbookID,
			job.ManifestKey,
			job.TotalSongs,
			job.ProcessedSongs,
			job.PruneMissing,
			job.Error,
			nullTime(job.CompletedAt),
			job.Generation,
			now,
			string(models.ImportJobFailed),
			string(models.ImportJobCompleted),
		).Scan(&returnedGen)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, job.ID)
			if getErr != nil {
				return getErr
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return fmt.Errorf("update import job %s: %w", job.ID, termErr)
			}
			if job.Generation > 0 && existing.Generation > 0 && existing.Generation != job.Generation {
				return fmt.Errorf("update import job %s: %w", job.ID, store.ErrJobGenerationConflict)
			}
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("update import job: %w", err)
		}
		if returnedGen > 0 {
			job.Generation = returnedGen
		}
		return nil
	})
}

// AssignImportJobGeneration assigns a positive catalog generation to an import job once.
func (s *Store) AssignImportJobGeneration(ctx context.Context, jobID string, generation int64) (int64, error) {
	if strings.TrimSpace(jobID) == "" {
		return 0, fmt.Errorf("assign import job generation: id is required")
	}
	if generation <= 0 {
		return 0, fmt.Errorf("assign import job generation: generation must be positive")
	}
	now := s.currentTime()
	var assigned int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET generation = $2, updated_at = $3
			WHERE id = $1 AND (generation = 0 OR generation = $2)
			RETURNING generation`,
			jobID, generation, now,
		).Scan(&assigned)
		if err == nil {
			return nil
		}
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return fmt.Errorf("assign import job generation %s: %w", jobID, getErr)
			}
			if existing.Generation > 0 {
				assigned = existing.Generation
				return nil
			}
			return fmt.Errorf("assign import job generation %s: conditional check failed", jobID)
		}
		return fmt.Errorf("assign import job generation %s: %w", jobID, err)
	})
	if err != nil {
		return 0, err
	}
	return assigned, nil
}

// UpdateImportJobProgress updates the processed song count on an active processing job.
func (s *Store) UpdateImportJobProgress(ctx context.Context, jobID string, processedSongs int) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("update import job progress: id is required")
	}
	if processedSongs < 0 {
		processedSongs = 0
	}
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET processed_songs = $2, updated_at = $3
			WHERE id = $1 AND status = $4
			RETURNING id`,
			jobID, processedSongs, now, string(models.ImportJobProcessing),
		).Scan(&id)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("update import job progress: %w", err)
		}
		return nil
	})
}
