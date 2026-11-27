package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type eventReplacementPolicy struct {
	allowKeyChange bool
	allowWriteIns  bool
}

func lockEventForReplacementTx(ctx context.Context, tx pgx.Tx, eventID string, isWriteIn bool) (eventReplacementPolicy, error) {
	var (
		evStatus             string
		restarting           bool
		deleting             bool
		allowSongReplacement bool
		policy               eventReplacementPolicy
	)
	err := tx.QueryRow(ctx, `
		SELECT status, restarting, deleting, COALESCE(allow_song_replacement, FALSE), allow_key_change, allow_write_ins
		FROM events WHERE id = $1 FOR UPDATE`, eventID,
	).Scan(&evStatus, &restarting, &deleting, &allowSongReplacement, &policy.allowKeyChange, &policy.allowWriteIns)
	if err != nil {
		if isNotFound(err) {
			return policy, os.ErrNotExist
		}
		return policy, fmt.Errorf("load event for song replacement: %w", err)
	}
	if deleting {
		return policy, store.ErrEventDeleting
	}
	if restarting {
		return policy, store.ErrAdmissionConflict
	}
	if evStatus == string(models.EventStatusEnded) {
		return policy, store.ErrEventEnded
	}
	if !allowSongReplacement {
		return policy, store.ErrSongReplacementDisabled
	}
	if isWriteIn && !policy.allowWriteIns {
		return policy, store.ErrWriteInsDisabled
	}
	return policy, nil
}

func normalizeReplacementKeyChange(allowKeyChange bool, keyChange int) int {
	if !allowKeyChange {
		return 0
	}
	if keyChange < -6 {
		return -6
	}
	if keyChange > 6 {
		return 6
	}
	return keyChange
}

func applySongReplacementTx(ctx context.Context, tx pgx.Tx, eventID, requestID string, replacement store.SongReplacementUpdate, policy eventReplacementPolicy, now time.Time) (*models.SongRequest, int64, error) {
	req, err := scanRequest(tx.QueryRow(ctx, `SELECT `+requestSelectColumns+` FROM event_requests WHERE id = $1 AND event_id = $2 FOR UPDATE`, requestID, eventID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, os.ErrNotExist
		}
		return nil, 0, fmt.Errorf("load request for song replacement: %w", err)
	}
	if req.Status != models.StatusPending && req.Status != models.StatusAccepted {
		return nil, 0, store.ErrRequestAlreadyCalled
	}
	songChanged := replacement.SongTitle != req.SongTitle || replacement.SongArtist != req.SongArtist
	if songChanged && replacement.SongID == "" && !policy.allowWriteIns {
		return nil, 0, store.ErrWriteInsDisabled
	}

	keyChange := normalizeReplacementKeyChange(policy.allowKeyChange, replacement.KeyChange)
	songID := replacement.SongID
	if replacement.IsWriteIn {
		songID = ""
	}

	var newVersion int64
	if err := tx.QueryRow(ctx, `
		UPDATE events SET queue_version = queue_version + 1, updated_at = $1
		WHERE id = $2 RETURNING queue_version`, now, eventID,
	).Scan(&newVersion); err != nil {
		return nil, 0, fmt.Errorf("bump queue version for song replacement: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE event_requests SET
			song_id = $1, is_write_in = $2, song_title = $3, song_artist = $4,
			key_change = $5, notes = $6, updated_at = $7
		WHERE id = $8 AND event_id = $9`,
		songID, replacement.IsWriteIn, replacement.SongTitle, replacement.SongArtist,
		keyChange, replacement.Notes, now, requestID, eventID,
	); err != nil {
		return nil, 0, fmt.Errorf("update request song %s: %w", requestID, err)
	}

	req.SongID = songID
	req.IsWriteIn = replacement.IsWriteIn
	req.SongTitle = replacement.SongTitle
	req.SongArtist = replacement.SongArtist
	req.KeyChange = keyChange
	req.Notes = replacement.Notes
	req.UpdatedAt = now
	return req, newVersion, nil
}

// ReplaceRequestSongAtomic replaces the song details on a pre-call (pending or accepted)
// request in a single transaction, fencing against concurrent call-to-stage, policy disable,
// show end, restart, or deletion while preserving request identity, rank, round, and status.
func (s *Store) ReplaceRequestSongAtomic(ctx context.Context, eventID, requestID string, replacement store.SongReplacementUpdate) (*models.SongRequest, int64, error) {
	if eventID == "" || requestID == "" {
		return nil, 0, fmt.Errorf("event ID and request ID are required: %w", os.ErrInvalid)
	}

	var (
		updatedReq *models.SongRequest
		newVersion int64
	)
	now := s.currentTime()
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		policy, txErr := lockEventForReplacementTx(ctx, tx, eventID, replacement.IsWriteIn)
		if txErr != nil {
			return txErr
		}
		updatedReq, newVersion, txErr = applySongReplacementTx(ctx, tx, eventID, requestID, replacement, policy, now)
		return txErr
	})
	if err != nil {
		return nil, 0, err
	}
	return updatedReq, newVersion, nil
}
