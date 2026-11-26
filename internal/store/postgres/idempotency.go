package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// Idempotency records are deliberately short-lived: they cover offline replay
// after a host reconnects without retaining request bodies indefinitely.
const idempotencyRetention = 24 * time.Hour

var errIdempotencyRace = errors.New("idempotency insert race")

func computeRequestRank(round, index int) int64 {
	rank := int64(index + 1)
	if round > 0 {
		rank = (int64(round) * models.RoundRankMultiplier) + int64(index+1)
	}
	return rank
}

func validateUniqueIDs(ids []string, op string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate request %s in %s: %w", id, op, os.ErrInvalid)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func classifyEventMutationError(ctx context.Context, tx pgx.Tx, eventID string, expectedVersion *int64, requireActive bool) error {
	var (
		status       string
		deleting     bool
		queueVersion int64
	)
	err := tx.QueryRow(ctx, `SELECT status, deleting, queue_version FROM events WHERE id = $1`, eventID).Scan(&status, &deleting, &queueVersion)
	if err != nil {
		if isNotFound(err) {
			return os.ErrNotExist
		}
		return err
	}
	if deleting {
		return store.ErrEventDeleting
	}
	if requireActive && status != string(models.EventStatusActive) {
		return store.ErrEventNotActive
	}
	if expectedVersion != nil && queueVersion != *expectedVersion {
		return &store.QueueVersionConflictError{Current: queueVersion, Expected: *expectedVersion}
	}
	return store.ErrAdmissionConflict
}

func applyReorderUpdatesTx(ctx context.Context, tx pgx.Tx, eventID string, orderedIDs []string, skipID string, now time.Time) error {
	for i, id := range orderedIDs {
		if id == skipID {
			continue
		}
		var otherEventID string
		var otherRound int
		err := tx.QueryRow(ctx, `SELECT event_id, round FROM event_requests WHERE id = $1`, id).Scan(&otherEventID, &otherRound)
		if err != nil {
			if isNotFound(err) {
				return fmt.Errorf("load request %s for reorder: %w", id, os.ErrNotExist)
			}
			return fmt.Errorf("load request %s for reorder: %w", id, err)
		}
		if otherEventID != eventID {
			return fmt.Errorf("request %s does not belong to event %s: %w", id, eventID, os.ErrPermission)
		}
		rank := computeRequestRank(otherRound, i)
		if _, err := tx.Exec(ctx, `UPDATE event_requests SET rank = $1, updated_at = $2 WHERE id = $3 AND event_id = $4`, rank, now, id, eventID); err != nil {
			return fmt.Errorf("update rank for request %s: %w", id, err)
		}
	}
	return nil
}

func validateIdempotencyArgs(key, ownerID, route, entityID, requestHash string) error {
	if key == "" || ownerID == "" || route == "" || entityID == "" || requestHash == "" {
		return fmt.Errorf("idempotency arguments are incomplete: %w", os.ErrInvalid)
	}
	return nil
}

func validateExpectedVersion(expected *int64) error {
	if expected != nil && *expected < 0 {
		return fmt.Errorf("expected queue version must not be negative: %w", os.ErrInvalid)
	}
	return nil
}

func loadIdempotencyTx(ctx context.Context, tx pgx.Tx, key, ownerID, route, entityID, requestHash string, now time.Time) (*store.QueueMutationResult, bool, error) {
	var (
		recOwner   string
		recRoute   string
		recEntity  string
		recHash    string
		statusCode int
		body       []byte
		queueVer   int64
		expiresAt  time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT owner_id, route, entity_id, request_hash, status_code, response_body, queue_version, expires_at
		FROM idempotency_records
		WHERE idempotency_key = $1`,
		key,
	).Scan(&recOwner, &recRoute, &recEntity, &recHash, &statusCode, &body, &queueVer, &expiresAt)
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("load idempotency record: %w", err)
	}
	if !expiresAt.After(now) {
		if _, delErr := tx.Exec(ctx, `DELETE FROM idempotency_records WHERE idempotency_key = $1 AND expires_at = $2`, key, expiresAt); delErr != nil {
			return nil, false, fmt.Errorf("remove expired idempotency record: %w", delErr)
		}
		return nil, false, nil
	}
	if recOwner != ownerID || recRoute != route || recEntity != entityID || recHash != requestHash {
		return nil, true, &store.IdempotencyConflictError{}
	}
	return &store.QueueMutationResult{
		StatusCode:   statusCode,
		Body:         body,
		QueueVersion: queueVer,
		Replay:       true,
	}, true, nil
}

func insertIdempotencyTx(ctx context.Context, tx pgx.Tx, key, ownerID, route, entityID, requestHash string, res *store.QueueMutationResult, expiresAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO idempotency_records (
			idempotency_key, owner_id, route, entity_id, request_hash,
			status_code, response_body, queue_version, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		key, ownerID, route, entityID, requestHash,
		res.StatusCode, res.Body, res.QueueVersion, expiresAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return errIdempotencyRace
		}
		return fmt.Errorf("insert idempotency record: %w", err)
	}
	return nil
}

// GetIdempotencyResult checks for an active replayable idempotency record.
func (s *Store) GetIdempotencyResult(ctx context.Context, key, ownerID, route, entityID, requestHash string) (*store.QueueMutationResult, bool, error) {
	if err := validateIdempotencyArgs(key, ownerID, route, entityID, requestHash); err != nil {
		return nil, false, err
	}
	var (
		res   *store.QueueMutationResult
		found bool
	)
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		res, found, txErr = loadIdempotencyTx(ctx, tx, key, ownerID, route, entityID, requestHash, s.currentTime())
		return txErr
	})
	return res, found, err
}

// UpdateRequestWithIdempotency atomically updates a request and records an idempotency result.
func (s *Store) UpdateRequestWithIdempotency(ctx context.Context, req *models.SongRequest, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	return s.UpdateRequestAndReorderWithIdempotency(ctx, req, nil, expected, key, ownerID, route, requestHash)
}

// UpdateRequestAndReorderWithIdempotency atomically updates a request, optionally reorders the queue,
// advances queue_version, and persists the replayable idempotency record.
func (s *Store) UpdateRequestAndReorderWithIdempotency(ctx context.Context, req *models.SongRequest, reorderIDs []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if req == nil || req.EventID == "" {
		return nil, fmt.Errorf("event ID is required for idempotent request update: %w", os.ErrInvalid)
	}
	if err := validateIdempotencyArgs(key, ownerID, route, req.ID, requestHash); err != nil {
		return nil, err
	}
	if err := validateExpectedVersion(expected); err != nil {
		return nil, err
	}
	if len(reorderIDs) > 0 {
		if err := validateUniqueIDs(reorderIDs, "reorder"); err != nil {
			return nil, err
		}
		reqInReorder := false
		for _, id := range reorderIDs {
			if id == req.ID {
				reqInReorder = true
				break
			}
		}
		if !reqInReorder {
			return nil, fmt.Errorf("request %s missing from reorder list: %w", req.ID, os.ErrInvalid)
		}
	}

	var (
		replayResult *store.QueueMutationResult
		commitResult *store.QueueMutationResult
		commitReq    models.SongRequest
	)

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.currentTime()
		if replay, found, err := loadIdempotencyTx(ctx, tx, key, ownerID, route, req.ID, requestHash, now); found || err != nil {
			replayResult = replay
			return err
		}

		newVer, err := bumpEventVersionForMutationTx(ctx, tx, req.EventID, expected, now)
		if err != nil {
			return err
		}

		commitReq = *req
		commitReq.UpdatedAt = now
		if len(reorderIDs) > 0 {
			for i, id := range reorderIDs {
				if id == req.ID {
					commitReq.Rank = computeRequestRank(commitReq.Round, i)
					break
				}
			}
		}

		if err := updateSingleRequestTx(ctx, tx, &commitReq, commitReq.Rank, now); err != nil {
			return err
		}
		if len(reorderIDs) > 0 {
			if err := applyReorderUpdatesTx(ctx, tx, req.EventID, reorderIDs, req.ID, now); err != nil {
				return err
			}
		}

		body, err := json.Marshal(&commitReq)
		if err != nil {
			return err
		}
		commitResult = &store.QueueMutationResult{
			StatusCode:   200,
			Body:         body,
			QueueVersion: newVer,
		}
		return insertIdempotencyTx(ctx, tx, key, ownerID, route, req.ID, requestHash, commitResult, now.Add(idempotencyRetention))
	})
	if errors.Is(err, errIdempotencyRace) {
		replay, found, replayErr := s.GetIdempotencyResult(ctx, key, ownerID, route, req.ID, requestHash)
		if replayErr != nil {
			return nil, replayErr
		}
		if found {
			return replay, nil
		}
		return nil, fmt.Errorf("idempotent request update: concurrent key conflict")
	}
	if err != nil {
		return nil, err
	}
	if replayResult != nil {
		return replayResult, nil
	}

	*req = commitReq
	return commitResult, nil
}

// ReorderRequestsWithIdempotency atomically reorders requests, advances queue_version when non-empty,
// and records an idempotency result.
func (s *Store) ReorderRequestsWithIdempotency(ctx context.Context, eventID string, orderedIDs []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if err := validateIdempotencyArgs(key, ownerID, route, eventID, requestHash); err != nil {
		return nil, err
	}
	if err := validateExpectedVersion(expected); err != nil {
		return nil, err
	}
	if err := validateUniqueIDs(orderedIDs, "idempotent reorder"); err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]string{"status": "ok"})
	if err != nil {
		return nil, err
	}

	var (
		replayResult *store.QueueMutationResult
		commitResult *store.QueueMutationResult
	)

	err = s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.currentTime()
		if replay, found, err := loadIdempotencyTx(ctx, tx, key, ownerID, route, eventID, requestHash, now); found || err != nil {
			replayResult = replay
			return err
		}

		var resultVersion int64
		if len(orderedIDs) == 0 {
			var (
				restarting bool
				deleting   bool
				currentVer int64
			)
			err := tx.QueryRow(ctx, `SELECT restarting, deleting, queue_version FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&restarting, &deleting, &currentVer)
			if err != nil {
				if isNotFound(err) {
					return os.ErrNotExist
				}
				return err
			}
			if deleting {
				return store.ErrEventDeleting
			}
			if expected != nil && currentVer != *expected {
				return &store.QueueVersionConflictError{Current: currentVer, Expected: *expected}
			}
			if restarting {
				return store.ErrAdmissionConflict
			}
			resultVersion = currentVer
		} else {
			newVer, err := bumpEventVersionForMutationTx(ctx, tx, eventID, expected, now)
			if err != nil {
				return err
			}
			if err := applyReorderUpdatesTx(ctx, tx, eventID, orderedIDs, "", now); err != nil {
				return err
			}
			resultVersion = newVer
		}

		commitResult = &store.QueueMutationResult{
			StatusCode:   200,
			Body:         body,
			QueueVersion: resultVersion,
		}
		return insertIdempotencyTx(ctx, tx, key, ownerID, route, eventID, requestHash, commitResult, now.Add(idempotencyRetention))
	})
	if errors.Is(err, errIdempotencyRace) {
		replay, found, replayErr := s.GetIdempotencyResult(ctx, key, ownerID, route, eventID, requestHash)
		if replayErr != nil {
			return nil, replayErr
		}
		if found {
			return replay, nil
		}
		return nil, fmt.Errorf("idempotent reorder: concurrent key conflict")
	}
	if err != nil {
		return nil, err
	}
	if replayResult != nil {
		return replayResult, nil
	}
	return commitResult, nil
}
