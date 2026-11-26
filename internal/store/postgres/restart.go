package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func checkSongbookHostFence(ctx context.Context, tx pgx.Tx) error {
	fence, ok := store.SongbookHostFenceFromContext(ctx)
	if !ok {
		return nil
	}
	var ownerID string
	var deleting bool
	err := tx.QueryRow(ctx, `SELECT owner_id, deleting FROM songbooks WHERE id = $1 FOR UPDATE`, fence.SongbookID).Scan(&ownerID, &deleting)
	if err != nil {
		if isNotFound(err) {
			return os.ErrNotExist
		}
		return fmt.Errorf("check songbook host fence: %w", err)
	}
	if deleting {
		return store.ErrSongbookDeleting
	}
	if fence.IsOwner {
		if ownerID != fence.ActorID {
			return store.ErrPermissionDenied
		}
		return nil
	}
	var canHost bool
	err = tx.QueryRow(ctx, `SELECT can_host_shows FROM songbook_grants WHERE songbook_id = $1 AND user_id = $2 FOR UPDATE`, fence.SongbookID, fence.ActorID).Scan(&canHost)
	if err != nil {
		if isNotFound(err) {
			return store.ErrPermissionDenied
		}
		return fmt.Errorf("check songbook host grant: %w", err)
	}
	if !canHost {
		return store.ErrPermissionDenied
	}
	return nil
}

func classifyRestartWriteError(ctx context.Context, tx pgx.Tx, eventID string, expectedVersion int64) error {
	var deleting bool
	var queueVersion int64
	err := tx.QueryRow(ctx, `SELECT deleting, queue_version FROM events WHERE id = $1`, eventID).Scan(&deleting, &queueVersion)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("queue restart write: %w", os.ErrNotExist)
		}
		return fmt.Errorf("queue restart write: %w", err)
	}
	if deleting {
		return store.ErrEventDeleting
	}
	if queueVersion != expectedVersion {
		return &store.QueueVersionConflictError{Current: queueVersion, Expected: expectedVersion}
	}
	return fmt.Errorf("queue restart write: %w", store.ErrAdmissionConflict)
}

func bumpRestartVersionTx(ctx context.Context, tx pgx.Tx, eventID string, expectedVersion int64, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE events SET queue_version = queue_version + 1, updated_at = $1
		WHERE id = $2 AND restarting = TRUE AND deleting = FALSE AND queue_version = $3`,
		now, eventID, expectedVersion)
	if err != nil {
		return fmt.Errorf("queue restart write: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return classifyRestartWriteError(ctx, tx, eventID, expectedVersion)
	}
	return nil
}

// BeginQueueRestart closes admission before any old request is archived.
func (s *Store) BeginQueueRestart(ctx context.Context, event *models.Event) error {
	if event == nil || event.ID == "" {
		return fmt.Errorf("begin queue restart: %w", os.ErrInvalid)
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookHostFence(ctx, tx); err != nil {
			return err
		}
		now := s.currentTime()
		tag, err := tx.Exec(ctx, `
			UPDATE events
			SET status = $1, restarting = TRUE, updated_at = $2, expires_at = NULL, queue_version = queue_version + 1
			WHERE id = $3 AND restarting = FALSE AND deleting = FALSE AND queue_version = $4`,
			string(models.EventStatusEnded), now, event.ID, event.QueueVersion)
		if err != nil {
			return fmt.Errorf("queue restart write: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return classifyRestartWriteError(ctx, tx, event.ID, event.QueueVersion)
		}
		return nil
	})
}

// ArchiveRestartBatch transitions a batch of requests to archived/completed state
// and advances queue_version atomically while the restart barrier is held.
func (s *Store) ArchiveRestartBatch(ctx context.Context, eventID string, expectedVersion int64, requests []*models.SongRequest) error {
	if eventID == "" || len(requests) == 0 {
		return fmt.Errorf("archive restart batch: %w", os.ErrInvalid)
	}
	seen := make(map[string]bool, len(requests))
	for _, req := range requests {
		if req == nil || req.ID == "" || req.EventID != eventID || seen[req.ID] {
			return fmt.Errorf("archive restart request: %w", os.ErrInvalid)
		}
		seen[req.ID] = true
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.currentTime()
		if err := bumpRestartVersionTx(ctx, tx, eventID, expectedVersion, now); err != nil {
			return err
		}
		for _, req := range requests {
			oldStatus := req.Status
			newStatus := req.Status
			var completedFallback *time.Time
			if newStatus == models.StatusPending || newStatus == models.StatusAccepted || newStatus == models.StatusUpNext {
				newStatus = models.StatusCompleted
				completedFallback = &now
			} else if newStatus == models.StatusCompleted && (req.CompletedAt == nil || req.CompletedAt.IsZero()) {
				fallback := req.UpdatedAt
				if fallback.IsZero() {
					fallback = req.CreatedAt
				}
				if !fallback.IsZero() {
					u := fallback.UTC()
					completedFallback = &u
				}
			}

			var (
				rowsAffected int64
				err          error
			)
			if completedFallback != nil {
				tag, execErr := tx.Exec(ctx, `
					UPDATE event_requests
					SET status = $1, updated_at = $2, completed_at = COALESCE(completed_at, $3), expires_at = NULL
					WHERE id = $4 AND event_id = $5 AND status = $6`,
					string(newStatus), now, *completedFallback, req.ID, eventID, string(oldStatus))
				rowsAffected, err = tag.RowsAffected(), execErr
			} else {
				tag, execErr := tx.Exec(ctx, `
					UPDATE event_requests
					SET status = $1, updated_at = $2, expires_at = NULL
					WHERE id = $3 AND event_id = $4 AND status = $5`,
					string(newStatus), now, req.ID, eventID, string(oldStatus))
				rowsAffected, err = tag.RowsAffected(), execErr
			}
			if err != nil {
				return fmt.Errorf("queue restart write: %w", err)
			}
			if rowsAffected == 0 {
				return fmt.Errorf("queue restart write: request %s status conflict or missing: %w", req.ID, store.ErrAdmissionConflict)
			}
		}
		return nil
	})
}

// ListRestartSlots returns nil because PostgreSQL stores no separate singer-slot items.
func (s *Store) ListRestartSlots(ctx context.Context, eventID string) ([]string, error) {
	if eventID == "" {
		return nil, fmt.Errorf("list restart slots: %w", os.ErrInvalid)
	}
	return nil, nil
}

// DeleteRestartSlots verifies event restart state and version and advances queue_version.
func (s *Store) DeleteRestartSlots(ctx context.Context, eventID string, expectedVersion int64, slotIDs []string) error {
	if eventID == "" || len(slotIDs) == 0 {
		return fmt.Errorf("delete restart slots: %w", os.ErrInvalid)
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return bumpRestartVersionTx(ctx, tx, eventID, expectedVersion, s.currentTime())
	})
}

// FinishQueueRestart reopens the event and clears the restart barrier atomically.
func (s *Store) FinishQueueRestart(ctx context.Context, eventID string, expectedVersion int64) error {
	if eventID == "" {
		return fmt.Errorf("finish queue restart: %w", os.ErrInvalid)
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookHostFence(ctx, tx); err != nil {
			return err
		}
		now := s.currentTime()
		tag, err := tx.Exec(ctx, `
			UPDATE events
			SET status = $1, restarting = FALSE, ended_at = NULL, expires_at = NULL, updated_at = $2, queue_version = queue_version + 1
			WHERE id = $3 AND restarting = TRUE AND deleting = FALSE AND queue_version = $4`,
			string(models.EventStatusActive), now, eventID, expectedVersion)
		if err != nil {
			return fmt.Errorf("queue restart write: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return classifyRestartWriteError(ctx, tx, eventID, expectedVersion)
		}
		return nil
	})
}

// DetachEventFromSongbook ends and detaches a collaborator's event from a songbook being deleted,
// preserving event and queue history under the event host's retention policy.
func (s *Store) DetachEventFromSongbook(ctx context.Context, eventID, songbookID, songbookName string, retention time.Duration) (*models.Event, error) {
	if retention <= 0 {
		retention = 14 * 24 * time.Hour
	}
	trimmedName := strings.TrimSpace(songbookName)
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var result *models.Event
		var conflict bool

		err := s.withTx(ctx, func(tx pgx.Tx) error {
			conflict = false
			result = nil
			row := tx.QueryRow(ctx, `SELECT `+eventSelectColumns+` FROM events WHERE id = $1`, eventID)
			ev, err := scanEvent(row)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if ev.SongbookID != songbookID {
				result = ev
				return nil
			}

			now := s.currentTime()
			endedAt := ev.EndedAt
			if endedAt == nil {
				t := now
				endedAt = &t
			}
			ttl := ev.PendingDetachTTL
			if ttl == 0 {
				ttl = ev.TTL
			}
			if ttl == 0 {
				ttl = endedAt.Add(retention).Unix()
			}
			expiresAt := time.Unix(ttl, 0).UTC()

			tag, err := tx.Exec(ctx, `
				UPDATE events
				SET status = $1,
					songbook_id = NULL,
					detached_songbook_name = $2,
					ended_at = $3,
					restarting = FALSE,
					detaching = FALSE,
					pending_detach_expires_at = NULL,
					expires_at = $4,
					updated_at = $5,
					metadata_version = metadata_version + 1,
					queue_version = queue_version + 1
				WHERE id = $6
					AND songbook_id = $7
					AND queue_version = $8
					AND metadata_version = $9`,
				string(models.EventStatusEnded),
				trimmedName,
				*endedAt,
				expiresAt,
				now,
				ev.ID,
				songbookID,
				ev.QueueVersion,
				ev.MetadataVersion,
			)
			if err != nil {
				return fmt.Errorf("detach event %s from songbook %s: %w", eventID, songbookID, err)
			}
			if tag.RowsAffected() == 0 {
				conflict = true
				return nil
			}

			if _, err := tx.Exec(ctx, `UPDATE event_requests SET expires_at = $1 WHERE event_id = $2`, expiresAt, ev.ID); err != nil {
				return fmt.Errorf("set retention on detached event %s: %w", ev.ID, err)
			}

			ev.Status = models.EventStatusEnded
			ev.SongbookID = ""
			ev.DetachedSongbookName = trimmedName
			ev.EndedAt = endedAt
			ev.Restarting = false
			ev.Detaching = false
			ev.PendingDetachTTL = 0
			ev.TTL = ttl
			ev.UpdatedAt = now
			ev.MetadataVersion++
			ev.QueueVersion++
			result = ev
			return nil
		})
		if err != nil {
			return nil, err
		}
		if conflict {
			continue
		}
		return result, nil
	}
	return nil, fmt.Errorf("detach event %s from songbook %s: exceeded conflict retries", eventID, songbookID)
}
