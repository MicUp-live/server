package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const requestSelectColumns = `id, event_id, songbook_id, song_id, is_write_in, song_title, song_artist,
	singer_name, key_change, notes, decline_reason, status, rank, round,
	singer_turn, created_at, updated_at, completed_at, expires_at`

func scanRequest(row pgx.Row) (*models.SongRequest, error) {
	var (
		req         models.SongRequest
		status      string
		completedAt *time.Time
		expiresAt   *time.Time
	)
	err := row.Scan(
		&req.ID, &req.EventID, &req.SongbookID, &req.SongID, &req.IsWriteIn, &req.SongTitle, &req.SongArtist,
		&req.SingerName, &req.KeyChange, &req.Notes, &req.DeclineReason, &status, &req.Rank, &req.Round,
		&req.SingerTurn, &req.CreatedAt, &req.UpdatedAt, &completedAt, &expiresAt,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	req.Status = models.RequestStatus(status)
	req.CreatedAt = req.CreatedAt.UTC()
	req.UpdatedAt = req.UpdatedAt.UTC()
	if completedAt != nil {
		t := completedAt.UTC()
		req.CompletedAt = &t
	}
	req.TTL = timePtrToUnix(expiresAt)
	return &req, nil
}

func sortEventRequests(result []*models.SongRequest) {
	sort.SliceStable(result, func(i, j int) bool {
		rI, rJ := result[i].Round, result[j].Round
		if rI > 0 && rJ > 0 && rI != rJ {
			return rI < rJ
		}
		if result[i].Rank != result[j].Rank {
			return result[i].Rank < result[j].Rank
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
}

func prepareNewRequestDefaults(req *models.SongRequest, now time.Time) (string, time.Time, time.Time, models.RequestStatus, int64) {
	id := req.ID
	if id == "" {
		id = uuid.NewString()
	}
	created := req.CreatedAt
	if created.IsZero() {
		created = now
	} else {
		created = created.UTC()
	}
	status := req.Status
	if status == "" {
		status = models.StatusPending
	}
	rank := req.Rank
	if rank == 0 {
		rank = created.UnixMilli()
	}
	return id, created, now, status, rank
}

func insertRequestTx(ctx context.Context, tx pgx.Tx, req *models.SongRequest, id string, status models.RequestStatus, rank int64, created, updated time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO event_requests (
			id, event_id, songbook_id, song_id, is_write_in, song_title, song_artist,
			singer_name, key_change, notes, decline_reason, status, rank, round,
			singer_turn, created_at, updated_at, completed_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		id, req.EventID, req.SongbookID, req.SongID, req.IsWriteIn, req.SongTitle, req.SongArtist,
		req.SingerName, req.KeyChange, req.Notes, req.DeclineReason, string(status), rank, req.Round,
		req.SingerTurn, created, updated, nullTimePtr(req.CompletedAt), unixToNullTime(req.TTL),
	)
	return err
}

func verifyEventAcceptingRequestsTx(ctx context.Context, tx pgx.Tx, eventID string) error {
	var evStatus string
	var restarting, deleting bool
	err := tx.QueryRow(ctx, `SELECT status, restarting, deleting FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&evStatus, &restarting, &deleting)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("create song request: %w", os.ErrNotExist)
		}
		return fmt.Errorf("create song request: %w", err)
	}
	if deleting {
		return fmt.Errorf("create song request: %w", store.ErrEventDeleting)
	}
	if evStatus != string(models.EventStatusActive) {
		return fmt.Errorf("create song request: %w", store.ErrEventNotActive)
	}
	if restarting {
		return fmt.Errorf("create song request: %w", store.ErrAdmissionConflict)
	}
	return nil
}

func classifyCreateRequestInsertError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("create song request: %w", os.ErrExist)
	}
	if isForeignKeyViolation(err) {
		return fmt.Errorf("create song request: %w", os.ErrNotExist)
	}
	return fmt.Errorf("create song request: %w", err)
}

// CreateRequest inserts a song request after confirming the event is active and not deleting/restarting.
func (s *Store) CreateRequest(ctx context.Context, req *models.SongRequest) error {
	if req == nil || req.EventID == "" {
		return fmt.Errorf("event ID is required to create song request: %w", os.ErrInvalid)
	}

	id, created, updated, status, rank := prepareNewRequestDefaults(req, s.currentTime())
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := verifyEventAcceptingRequestsTx(ctx, tx, req.EventID); err != nil {
			return err
		}
		return classifyCreateRequestInsertError(insertRequestTx(ctx, tx, req, id, status, rank, created, updated))
	})
	if err != nil {
		return err
	}

	req.ID, req.CreatedAt, req.UpdatedAt, req.Status, req.Rank = id, created, updated, status, rank
	return nil
}

func countActiveSingerRequestsTx(ctx context.Context, tx pgx.Tx, eventID, normSinger string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT singer_name FROM event_requests WHERE event_id = $1 AND status IN ($2, $3, $4)`,
		eventID, string(models.StatusPending), string(models.StatusAccepted), string(models.StatusUpNext))
	if err != nil {
		return 0, fmt.Errorf("count active singer requests: %w", err)
	}
	defer rows.Close()
	activeCount := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, fmt.Errorf("scan active singer request: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(name)) == normSinger {
			activeCount++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate active singer requests: %w", err)
	}
	return activeCount, nil
}

func bumpExpectedEventVersionTx(ctx context.Context, tx pgx.Tx, eventID string, expectedVersion int64, updated time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE events SET queue_version = queue_version + 1, updated_at = $1
		WHERE id = $2 AND status = $3 AND restarting = FALSE AND deleting = FALSE AND queue_version = $4`,
		updated, eventID, string(models.EventStatusActive), expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("commit request event version: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return classifyEventMutationError(ctx, tx, eventID, &expectedVersion, true)
	}
	return nil
}

func enforceSingerActiveLimitTx(ctx context.Context, tx pgx.Tx, eventID, normSinger string, maxActivePerSinger int) error {
	if maxActivePerSinger <= 0 || normSinger == "" {
		return nil
	}
	activeCount, err := countActiveSingerRequestsTx(ctx, tx, eventID, normSinger)
	if err != nil {
		return err
	}
	if activeCount >= maxActivePerSinger {
		return store.ErrSingerLimitExceeded
	}
	return nil
}

func classifyAdmissionInsertError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return store.ErrAdmissionConflict
	}
	return fmt.Errorf("commit request insert: %w", err)
}

// CreateRequestAtVersion commits a complete admission plan against expectedVersion.
func (s *Store) CreateRequestAtVersion(ctx context.Context, req *models.SongRequest, maxActivePerSinger int, reorderIDs []string, expectedVersion int64) error {
	if req == nil || req.EventID == "" || expectedVersion < 0 {
		return fmt.Errorf("event ID and nonnegative expected queue version are required: %w", os.ErrInvalid)
	}
	if err := validateUniqueIDs(reorderIDs, "reorder list"); err != nil {
		return err
	}

	normSinger := strings.ToLower(strings.TrimSpace(req.SingerName))
	id, created, updated, status, rank := prepareNewRequestDefaults(req, s.currentTime())

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := bumpExpectedEventVersionTx(ctx, tx, req.EventID, expectedVersion, updated); err != nil {
			return err
		}
		if err := enforceSingerActiveLimitTx(ctx, tx, req.EventID, normSinger, maxActivePerSinger); err != nil {
			return err
		}
		if err := classifyAdmissionInsertError(insertRequestTx(ctx, tx, req, id, status, rank, created, updated)); err != nil {
			return err
		}
		if len(reorderIDs) > 0 {
			return applyReorderUpdatesTx(ctx, tx, req.EventID, reorderIDs, id, updated)
		}
		return nil
	})
	if err != nil {
		return err
	}

	req.ID, req.CreatedAt, req.UpdatedAt, req.Status, req.Rank = id, created, updated, status, rank
	return nil
}

// GetRequest fetches a single song request by ID.
func (s *Store) GetRequest(ctx context.Context, id string) (*models.SongRequest, error) {
	if id == "" {
		return nil, os.ErrNotExist
	}
	req, err := scanRequest(s.pool.QueryRow(ctx, `SELECT `+requestSelectColumns+` FROM event_requests WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("get song request %s: %w", id, err)
	}
	return req, nil
}

// ListRequestsByEvent returns all requests for an event in canonical queue order.
func (s *Store) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+requestSelectColumns+` FROM event_requests WHERE event_id = $1 ORDER BY rank ASC, created_at ASC`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list consistent requests for event %s: %w", eventID, err)
	}
	defer rows.Close()
	var result []*models.SongRequest
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, req)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortEventRequests(result)
	return result, nil
}

func updateSingleRequestTx(ctx context.Context, tx pgx.Tx, req *models.SongRequest, rank int64, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE event_requests SET
			songbook_id = $1, song_id = $2, is_write_in = $3, song_title = $4, song_artist = $5,
			singer_name = $6, key_change = $7, notes = $8, decline_reason = $9, status = $10,
			rank = $11, round = $12, singer_turn = $13, updated_at = $14, completed_at = $15, expires_at = $16
		WHERE id = $17 AND event_id = $18`,
		req.SongbookID, req.SongID, req.IsWriteIn, req.SongTitle, req.SongArtist,
		req.SingerName, req.KeyChange, req.Notes, req.DeclineReason, string(req.Status),
		rank, req.Round, req.SingerTurn, now, nullTimePtr(req.CompletedAt), unixToNullTime(req.TTL),
		req.ID, req.EventID,
	)
	if err != nil {
		return fmt.Errorf("update song request %s: %w", req.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("request %s not found in event %s: %w", req.ID, req.EventID, os.ErrNotExist)
	}
	return nil
}

func bumpEventVersionForMutationTx(ctx context.Context, tx pgx.Tx, eventID string, expectedVersion *int64, now time.Time) (int64, error) {
	var (
		newVersion int64
		err        error
	)
	if expectedVersion != nil {
		err = tx.QueryRow(ctx, `
			UPDATE events SET queue_version = queue_version + 1, updated_at = $1
			WHERE id = $2 AND restarting = FALSE AND deleting = FALSE AND queue_version = $3
			RETURNING queue_version`, now, eventID, *expectedVersion).Scan(&newVersion)
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE events SET queue_version = queue_version + 1, updated_at = $1
			WHERE id = $2 AND restarting = FALSE AND deleting = FALSE
			RETURNING queue_version`, now, eventID).Scan(&newVersion)
	}
	if err != nil {
		if isNotFound(err) {
			return 0, classifyEventMutationError(ctx, tx, eventID, expectedVersion, false)
		}
		return 0, err
	}
	return newVersion, nil
}

func (s *Store) updateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion *int64) error {
	if req == nil || req.EventID == "" {
		return fmt.Errorf("event ID is required for request update: %w", os.ErrInvalid)
	}
	if expectedVersion != nil && *expectedVersion < 0 {
		return fmt.Errorf("expected queue version must not be negative: %w", os.ErrInvalid)
	}
	newRank := req.Rank
	if len(reorderIDs) > 0 {
		if err := validateUniqueIDs(reorderIDs, "reorder"); err != nil {
			return err
		}
		reqIndex := -1
		for i, id := range reorderIDs {
			if id == req.ID {
				reqIndex = i
				break
			}
		}
		if reqIndex < 0 {
			return fmt.Errorf("request %s missing from reorder list: %w", req.ID, os.ErrInvalid)
		}
		newRank = computeRequestRank(req.Round, reqIndex)
	}

	now := s.currentTime()
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := bumpEventVersionForMutationTx(ctx, tx, req.EventID, expectedVersion, now); err != nil {
			return err
		}
		if err := updateSingleRequestTx(ctx, tx, req, newRank, now); err != nil {
			return err
		}
		if len(reorderIDs) > 0 {
			return applyReorderUpdatesTx(ctx, tx, req.EventID, reorderIDs, req.ID, now)
		}
		return nil
	})
	if err != nil {
		return err
	}

	req.UpdatedAt = now
	req.Rank = newRank
	return nil
}

// UpdateRequest atomically writes a request and advances its event queue version.
func (s *Store) UpdateRequest(ctx context.Context, req *models.SongRequest) error {
	return s.updateRequestAndReorder(ctx, req, nil, nil)
}

// UpdateRequestWithQueueVersion atomically writes a request and advances its event queue version at expectedVersion.
func (s *Store) UpdateRequestWithQueueVersion(ctx context.Context, req *models.SongRequest, expectedVersion int64) error {
	return s.updateRequestAndReorder(ctx, req, nil, &expectedVersion)
}

// UpdateRequestAndReorder atomically updates a request and reorders the queue.
func (s *Store) UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string) error {
	return s.updateRequestAndReorder(ctx, req, reorderIDs, nil)
}

// UpdateRequestAndReorderWithQueueVersion atomically updates a request, reorders the queue, and checks expectedVersion.
func (s *Store) UpdateRequestAndReorderWithQueueVersion(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion int64) error {
	return s.updateRequestAndReorder(ctx, req, reorderIDs, &expectedVersion)
}

func (s *Store) reorderRequests(ctx context.Context, eventID string, orderedIDs []string, expectedVersion *int64) error {
	if len(orderedIDs) == 0 {
		return nil
	}
	if err := validateUniqueIDs(orderedIDs, "reorder"); err != nil {
		return err
	}
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := bumpEventVersionForMutationTx(ctx, tx, eventID, expectedVersion, now); err != nil {
			return err
		}
		return applyReorderUpdatesTx(ctx, tx, eventID, orderedIDs, "", now)
	})
}

// ReorderRequests atomically reorders event requests and advances queue_version.
func (s *Store) ReorderRequests(ctx context.Context, eventID string, orderedIDs []string) error {
	if len(orderedIDs) == 0 {
		return nil
	}
	if eventID == "" {
		return fmt.Errorf("event ID is required to reorder requests: %w", os.ErrInvalid)
	}
	return s.reorderRequests(ctx, eventID, orderedIDs, nil)
}

// ReorderRequestsWithQueueVersion atomically reorders event requests at expectedVersion.
func (s *Store) ReorderRequestsWithQueueVersion(ctx context.Context, eventID string, orderedIDs []string, expectedVersion int64) error {
	if expectedVersion < 0 || eventID == "" {
		return fmt.Errorf("invalid conditional reorder arguments: %w", os.ErrInvalid)
	}
	if len(orderedIDs) == 0 {
		return nil
	}
	return s.reorderRequests(ctx, eventID, orderedIDs, &expectedVersion)
}

// DeleteEventRequestAtomic enforces event scope and commits request deletion and queue_version bump atomically.
func (s *Store) DeleteEventRequestAtomic(ctx context.Context, eventID, requestID string) error {
	if eventID == "" {
		return fmt.Errorf("event ID is required for atomic request deletion: %w", os.ErrInvalid)
	}
	return s.deleteRequest(ctx, requestID, eventID, true)
}

// DeleteRequest removes a request and advances the parent event's queue_version if present.
func (s *Store) DeleteRequest(ctx context.Context, id string) error {
	return s.deleteRequest(ctx, id, "", false)
}

func notFoundForDelete(enforceEventScope bool) error {
	if enforceEventScope {
		return os.ErrNotExist
	}
	return nil
}

func loadRequestEventIDForDeleteTx(ctx context.Context, tx pgx.Tx, id, expectedEventID string, enforceEventScope bool) (string, bool, error) {
	var reqEventID string
	err := tx.QueryRow(ctx, `SELECT event_id FROM event_requests WHERE id = $1`, id).Scan(&reqEventID)
	if isNotFound(err) {
		return "", false, notFoundForDelete(enforceEventScope)
	}
	if err != nil {
		return "", false, fmt.Errorf("load song request for delete: %w", err)
	}
	if enforceEventScope && reqEventID != expectedEventID {
		return "", false, os.ErrNotExist
	}
	return reqEventID, true, nil
}

func bumpEventVersionOnRequestDeleteTx(ctx context.Context, tx pgx.Tx, reqEventID string, enforceEventScope bool, now time.Time) error {
	if enforceEventScope {
		_, err := bumpEventVersionForMutationTx(ctx, tx, reqEventID, nil, now)
		return err
	}
	if reqEventID == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE events SET queue_version = queue_version + 1, updated_at = $1 WHERE id = $2`, now, reqEventID); err != nil {
		return fmt.Errorf("bump event version on request delete: %w", err)
	}
	return nil
}

func (s *Store) deleteRequest(ctx context.Context, id, expectedEventID string, enforceEventScope bool) error {
	if strings.HasPrefix(id, "slot#") {
		return nil
	}
	if id == "" {
		return notFoundForDelete(enforceEventScope)
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		reqEventID, found, err := loadRequestEventIDForDeleteTx(ctx, tx, id, expectedEventID, enforceEventScope)
		if err != nil || !found {
			return err
		}
		if err := bumpEventVersionOnRequestDeleteTx(ctx, tx, reqEventID, enforceEventScope, s.currentTime()); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM event_requests WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete song request: %w", err)
		}
		if tag.RowsAffected() == 0 && enforceEventScope {
			return os.ErrNotExist
		}
		return nil
	})
}

// SetRequestsTTLByEvent sets or clears the retention expiration for an event's requests.
func (s *Store) SetRequestsTTLByEvent(ctx context.Context, eventID string, expiresAt time.Time) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var status string
		var restarting bool
		err := tx.QueryRow(ctx, `SELECT status, restarting FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&status, &restarting)
		if err == nil {
			if restarting {
				return fmt.Errorf("set request retention for %s: restart in progress: %w", eventID, store.ErrAdmissionConflict)
			}
			if !expiresAt.IsZero() && status == string(models.EventStatusActive) {
				return nil
			}
		} else if !isNotFound(err) {
			return fmt.Errorf("get event for retention: %w", err)
		}

		if _, err := tx.Exec(ctx, `UPDATE event_requests SET expires_at = $1 WHERE event_id = $2`, nullTime(expiresAt), eventID); err != nil {
			return fmt.Errorf("set request retention for %s: %w", eventID, err)
		}
		return nil
	})
}
