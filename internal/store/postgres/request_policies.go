package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

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
		var (
			evStatus             string
			restarting           bool
			deleting             bool
			allowSongReplacement bool
			allowKeyChange       bool
			allowWriteIns        bool
		)
		err := tx.QueryRow(ctx, `
			SELECT status, restarting, deleting, COALESCE(allow_song_replacement, FALSE), allow_key_change, allow_write_ins
			FROM events WHERE id = $1 FOR UPDATE`, eventID,
		).Scan(&evStatus, &restarting, &deleting, &allowSongReplacement, &allowKeyChange, &allowWriteIns)
		if err != nil {
			if isNotFound(err) {
				return os.ErrNotExist
			}
			return fmt.Errorf("load event for song replacement: %w", err)
		}
		if deleting {
			return store.ErrEventDeleting
		}
		if restarting {
			return store.ErrAdmissionConflict
		}
		if evStatus == string(models.EventStatusEnded) {
			return store.ErrEventEnded
		}
		if !allowSongReplacement {
			return store.ErrSongReplacementDisabled
		}
		if replacement.IsWriteIn && !allowWriteIns {
			return store.ErrWriteInsDisabled
		}

		req, err := scanRequest(tx.QueryRow(ctx, `SELECT `+requestSelectColumns+` FROM event_requests WHERE id = $1 AND event_id = $2 FOR UPDATE`, requestID, eventID))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return os.ErrNotExist
			}
			return fmt.Errorf("load request for song replacement: %w", err)
		}
		if req.Status != models.StatusPending && req.Status != models.StatusAccepted {
			return store.ErrRequestAlreadyCalled
		}

		songChanged := replacement.SongTitle != req.SongTitle || replacement.SongArtist != req.SongArtist
		if songChanged && replacement.SongID == "" && !allowWriteIns {
			return store.ErrWriteInsDisabled
		}

		keyChange := replacement.KeyChange
		if !allowKeyChange {
			keyChange = 0
		} else if keyChange < -6 {
			keyChange = -6
		} else if keyChange > 6 {
			keyChange = 6
		}

		songID := replacement.SongID
		if replacement.IsWriteIn {
			songID = ""
		}

		if err := tx.QueryRow(ctx, `
			UPDATE events SET queue_version = queue_version + 1, updated_at = $1
			WHERE id = $2 RETURNING queue_version`, now, eventID,
		).Scan(&newVersion); err != nil {
			return fmt.Errorf("bump queue version for song replacement: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE event_requests SET
				song_id = $1, is_write_in = $2, song_title = $3, song_artist = $4,
				key_change = $5, notes = $6, updated_at = $7
			WHERE id = $8 AND event_id = $9`,
			songID, replacement.IsWriteIn, replacement.SongTitle, replacement.SongArtist,
			keyChange, replacement.Notes, now, requestID, eventID,
		); err != nil {
			return fmt.Errorf("update request song %s: %w", requestID, err)
		}

		req.SongID = songID
		req.IsWriteIn = replacement.IsWriteIn
		req.SongTitle = replacement.SongTitle
		req.SongArtist = replacement.SongArtist
		req.KeyChange = keyChange
		req.Notes = replacement.Notes
		req.UpdatedAt = now
		updatedReq = req
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return updatedReq, newVersion, nil
}
