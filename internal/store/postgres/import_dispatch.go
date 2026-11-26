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

// MarkImportJobDispatchReady marks an import job as ready to be dispatched.
func (s *Store) MarkImportJobDispatchReady(ctx context.Context, jobID string) error {
	now := s.currentTime()
	markFn := func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET dispatch_ready = TRUE, status = $2, updated_at = $3
			WHERE id = $1 AND status IN ($4, $2)
			RETURNING id`,
			jobID, string(models.ImportJobQueued), now, string(models.ImportJobPending),
		).Scan(&id)
		if !isNotFound(err) {
			return err
		}
		existing, getErr := scanImportJob(tx.QueryRow(ctx, `SELECT `+importJobColumns+` FROM import_jobs WHERE id = $1`, jobID))
		if getErr != nil {
			return os.ErrNotExist
		}
		if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
			return termErr
		}
		if existing.Status == models.ImportJobProcessing || existing.DispatchedAt != nil {
			return store.ErrImportJobAlreadyDispatched
		}
		return os.ErrNotExist
	}

	if store.SongbookEditActorFromContext(ctx) != "" {
		return s.withTx(ctx, func(tx pgx.Tx) error {
			var songbookID string
			err := tx.QueryRow(ctx, `SELECT songbook_id FROM import_jobs WHERE id = $1`, jobID).Scan(&songbookID)
			if isNotFound(err) {
				return os.ErrNotExist
			}
			if err != nil {
				return err
			}
			if err := checkSongbookWriteFence(ctx, tx, songbookID); err != nil {
				return err
			}
			return markFn(tx)
		})
	}

	return s.withTx(ctx, markFn)
}

// ClaimImportJobDispatch acquires a time-bounded lease to dispatch an import job to SQS.
func (s *Store) ClaimImportJobDispatch(ctx context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}
	now := s.currentTime()
	expires := now.Add(leaseDuration)
	token := uuid.NewString()

	var job *models.ImportJob
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		job, scanErr = scanImportJob(tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET dispatch_claim_token = $2,
			    dispatch_claim_expires_at = $3,
			    dispatch_attempts = dispatch_attempts + 1,
			    updated_at = $4
			WHERE id = $1
			  AND dispatch_ready = TRUE
			  AND status IN ($5, $6)
			  AND dispatched_at IS NULL
			  AND (dispatch_claim_expires_at IS NULL OR dispatch_claim_expires_at <= $4 OR dispatch_claim_token = '')
			RETURNING `+importJobColumns,
			jobID, token, expires, now,
			string(models.ImportJobPending), string(models.ImportJobQueued),
		))
		if isNotFound(scanErr) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if !existing.DispatchReady {
				return store.ErrImportJobNotReady
			}
			if existing.DispatchedAt != nil {
				return store.ErrImportJobAlreadyDispatched
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			if existing.Status == models.ImportJobProcessing {
				return store.ErrImportJobAlreadyDispatched
			}
			return store.ErrImportJobClaimConflict
		}
		if scanErr != nil {
			return fmt.Errorf("claim import job dispatch: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return job, token, nil
}

// ConfirmImportJobDispatch records confirmed SQS dispatch using the caller's matching claim token.
func (s *Store) ConfirmImportJobDispatch(ctx context.Context, jobID string, claimToken string) error {
	if strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("confirm import job dispatch: claim token is required")
	}
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET status = $2,
			    dispatched_at = $3,
			    updated_at = $3,
			    dispatch_claim_token = '',
			    dispatch_claim_expires_at = NULL,
			    error = ''
			WHERE id = $1
			  AND status IN ($4, $2)
			  AND dispatch_claim_token = $5
			RETURNING id`,
			jobID, string(models.ImportJobQueued), now, string(models.ImportJobPending), claimToken,
		).Scan(&id)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			if existing.Status == models.ImportJobProcessing {
				return nil
			}
			return store.ErrImportJobStaleClaim
		}
		if err != nil {
			return fmt.Errorf("confirm import job dispatch: %w", err)
		}
		return nil
	})
}

// RecordDispatchError releases the active claim and logs the dispatch error.
func (s *Store) RecordDispatchError(ctx context.Context, jobID string, claimToken string, errMsg string) error {
	if strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("record dispatch error: claim token is required")
	}
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET error = $2,
			    updated_at = $3,
			    dispatch_claim_token = '',
			    dispatch_claim_expires_at = NULL
			WHERE id = $1
			  AND status IN ($4, $5)
			  AND dispatch_claim_token = $6
			RETURNING id`,
			jobID, errMsg, now,
			string(models.ImportJobPending), string(models.ImportJobQueued), claimToken,
		).Scan(&id)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			return store.ErrImportJobStaleClaim
		}
		if err != nil {
			return fmt.Errorf("record dispatch error: %w", err)
		}
		return nil
	})
}

// ResetImportJobDispatch clears the dispatch claim, error, and attempts for an unconfirmed job.
func (s *Store) ResetImportJobDispatch(ctx context.Context, jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("reset import job dispatch: id is required")
	}
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET dispatch_attempts = 0,
			    updated_at = $2,
			    error = '',
			    dispatch_claim_token = '',
			    dispatch_claim_expires_at = NULL
			WHERE id = $1
			  AND dispatch_ready = TRUE
			  AND dispatched_at IS NULL
			  AND status IN ($3, $4)
			  AND (dispatch_claim_expires_at IS NULL OR dispatch_claim_expires_at <= $2 OR dispatch_claim_token = '')
			RETURNING id`,
			jobID, now, string(models.ImportJobPending), string(models.ImportJobQueued),
		).Scan(&id)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if !existing.DispatchReady {
				return store.ErrImportJobNotReady
			}
			if existing.DispatchedAt != nil || existing.Status == models.ImportJobProcessing {
				return store.ErrImportJobAlreadyDispatched
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			return store.ErrImportJobClaimConflict
		}
		if err != nil {
			return fmt.Errorf("reset import job dispatch: %w", err)
		}
		return nil
	})
}

// ListDispatchableImportJobs returns unconfirmed ready jobs with no active claim, bounded by maxAttempts.
func (s *Store) ListDispatchableImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	now := s.currentTime()
	rows, err := s.pool.Query(ctx, `
		SELECT `+importJobColumns+`
		FROM import_jobs
		WHERE dispatch_ready = TRUE
		  AND dispatched_at IS NULL
		  AND status IN ($1, $2)
		  AND dispatch_attempts < $3
		  AND (dispatch_claim_expires_at IS NULL OR dispatch_claim_expires_at <= $4)
		ORDER BY created_at ASC, id ASC`,
		string(models.ImportJobPending), string(models.ImportJobQueued), maxAttempts, now,
	)
	if err != nil {
		return nil, fmt.Errorf("list dispatchable import jobs: %w", err)
	}
	return scanImportJobs(rows)
}

// ListExhaustedImportJobs returns unconfirmed ready jobs that have reached or exceeded maxAttempts.
func (s *Store) ListExhaustedImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+importJobColumns+`
		FROM import_jobs
		WHERE dispatch_ready = TRUE
		  AND dispatched_at IS NULL
		  AND status IN ($1, $2)
		  AND dispatch_attempts >= $3
		ORDER BY created_at ASC, id ASC`,
		string(models.ImportJobPending), string(models.ImportJobQueued), maxAttempts,
	)
	if err != nil {
		return nil, fmt.Errorf("list exhausted import jobs: %w", err)
	}
	return scanImportJobs(rows)
}

// ClaimImportJobProcessing transitions a job to processing status upon worker delivery.
func (s *Store) ClaimImportJobProcessing(ctx context.Context, jobID string) (*models.ImportJob, error) {
	now := s.currentTime()
	var job *models.ImportJob
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		job, scanErr = scanImportJob(tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET status = $2,
			    dispatched_at = COALESCE(dispatched_at, $3),
			    updated_at = $3,
			    error = '',
			    dispatch_claim_token = '',
			    dispatch_claim_expires_at = NULL
			WHERE id = $1
			  AND status IN ($4, $5, $2)
			RETURNING `+importJobColumns,
			jobID, string(models.ImportJobProcessing), now,
			string(models.ImportJobPending), string(models.ImportJobQueued),
		))
		if isNotFound(scanErr) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if termErr := classifyTerminalJobStatus(existing.Status); termErr != nil {
				return termErr
			}
			return os.ErrNotExist
		}
		if scanErr != nil {
			return fmt.Errorf("claim import job processing: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// FailImportJob transitions an active import job to terminal failed status.
func (s *Store) FailImportJob(ctx context.Context, jobID string, reason string) error {
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE import_jobs
			SET status = $2,
			    error = $3,
			    updated_at = $4,
			    dispatch_claim_token = '',
			    dispatch_claim_expires_at = NULL
			WHERE id = $1
			  AND status IN ($5, $6, $7)
			RETURNING id`,
			jobID, string(models.ImportJobFailed), reason, now,
			string(models.ImportJobPending), string(models.ImportJobQueued), string(models.ImportJobProcessing),
		).Scan(&id)
		if isNotFound(err) {
			existing, getErr := getImportJobTx(ctx, tx, jobID)
			if getErr != nil {
				return getErr
			}
			if existing.Status == models.ImportJobCompleted {
				return store.ErrImportJobAlreadyCompleted
			}
			if existing.Status == models.ImportJobFailed {
				return nil
			}
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("fail import job: %w", err)
		}
		return nil
	})
}
