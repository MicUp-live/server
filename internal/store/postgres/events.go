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

const eventSelectColumns = `id, owner_id, songbook_id, detached_songbook_name, venue_name, name, join_code,
	status, recurring, tip_url, announcement, kiosk_pin, rotation_mode, current_round,
	auto_accept, submission_delay, max_active_per_singer, allow_key_change, allow_write_ins,
	COALESCE(allow_song_replacement, FALSE), singer_weights, queue_version, metadata_version, restarting, detaching,
	pending_detach_expires_at, deleting, openkj_key, created_at, updated_at, ended_at, expires_at`

func scanEvent(row pgx.Row) (*models.Event, error) {
	var (
		ev               models.Event
		songbookID       *string
		status           string
		weightsJSON      []byte
		pendingDetachExp *time.Time
		endedAt          *time.Time
		expiresAt        *time.Time
	)
	err := row.Scan(
		&ev.ID, &ev.OwnerID, &songbookID, &ev.DetachedSongbookName, &ev.VenueName, &ev.Name, &ev.JoinCode,
		&status, &ev.Recurring, &ev.TipURL, &ev.Announcement, &ev.KioskPIN, &ev.RotationMode, &ev.CurrentRound,
		&ev.AutoAccept, &ev.SubmissionDelay, &ev.MaxActivePerSinger, &ev.AllowKeyChange, &ev.AllowWriteIns,
		&ev.AllowSongReplacement, &weightsJSON, &ev.QueueVersion, &ev.MetadataVersion, &ev.Restarting, &ev.Detaching,
		&pendingDetachExp, &ev.Deleting, &ev.OpenKJKey, &ev.CreatedAt, &ev.UpdatedAt, &endedAt, &expiresAt,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	ev.SongbookID = derefString(songbookID)
	ev.Status = models.EventStatus(status)
	if ev.RotationMode == "" {
		ev.RotationMode = models.RotationModeRoundRobin
	}
	if ev.CurrentRound <= 0 {
		ev.CurrentRound = 1
	}
	if ev.MaxActivePerSinger < 0 {
		ev.MaxActivePerSinger = 0
	}
	var rawWeights map[string]int
	if err := unmarshalJSON(weightsJSON, &rawWeights); err != nil {
		return nil, fmt.Errorf("decode event singer weights: %w", err)
	}
	ev.SingerWeights = models.NormalizeSingerWeights(rawWeights)
	ev.PendingDetachTTL = timePtrToUnix(pendingDetachExp)
	ev.TTL = timePtrToUnix(expiresAt)
	ev.CreatedAt = ev.CreatedAt.UTC()
	ev.UpdatedAt = ev.UpdatedAt.UTC()
	if endedAt != nil {
		t := endedAt.UTC()
		ev.EndedAt = &t
	}
	return &ev, nil
}

func (s *Store) queryEvents(ctx context.Context, query string, args ...any) ([]*models.Event, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*models.Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) getEventWhere(ctx context.Context, where string, arg any) (*models.Event, error) {
	ev, err := scanEvent(s.pool.QueryRow(ctx, `SELECT `+eventSelectColumns+` FROM events WHERE `+where, arg))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return ev, nil
}

type createEventParams struct {
	id              string
	created         time.Time
	updated         time.Time
	status          models.EventStatus
	rotationMode    string
	submissionDelay int
	currentRound    int
	queueVersion    int64
	maxActive       int
	openKJKey       string
	joinCode        string
	weights         map[string]int
	weightsJSON     []byte
}

func prepareCreateEventParams(ev *models.Event, now time.Time) (createEventParams, error) {
	id := ev.ID
	if id == "" {
		id = uuid.NewString()
	}
	created := ev.CreatedAt
	if created.IsZero() {
		created = now
	} else {
		created = created.UTC()
	}
	status := ev.Status
	if status == "" {
		status = models.EventStatusActive
	}
	rotationMode := ev.RotationMode
	if rotationMode == "" {
		rotationMode = models.RotationModeRoundRobin
	}
	currentRound := ev.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}
	queueVersion := ev.QueueVersion
	if queueVersion <= 0 {
		queueVersion = 1
	}
	openKJKey := ev.OpenKJKey
	if openKJKey == "" {
		openKJKey = models.GenerateOpenKJKey(id)
	}
	weights := models.NormalizeSingerWeights(ev.SingerWeights)
	weightsToMarshal := weights
	if weightsToMarshal == nil {
		weightsToMarshal = map[string]int{}
	}
	weightsJSON, err := marshalJSON(weightsToMarshal)
	if err != nil {
		return createEventParams{}, fmt.Errorf("create event: marshal singer weights: %w", err)
	}
	return createEventParams{
		id:              id,
		created:         created,
		updated:         now,
		status:          status,
		rotationMode:    rotationMode,
		submissionDelay: max(0, ev.SubmissionDelay),
		currentRound:    currentRound,
		queueVersion:    queueVersion,
		maxActive:       max(0, ev.MaxActivePerSinger),
		openKJKey:       openKJKey,
		joinCode:        strings.ToUpper(strings.TrimSpace(ev.JoinCode)),
		weights:         weights,
		weightsJSON:     weightsJSON,
	}, nil
}

func mapCreateEventInsertError(err error) error {
	if isUniqueViolation(err) {
		switch pgConstraintName(err) {
		case "events_pkey":
			return os.ErrExist
		case "idx_events_join_code":
			return fmt.Errorf("create event: join code conflict: %w", os.ErrExist)
		default:
			return fmt.Errorf("create event: %w", os.ErrExist)
		}
	}
	if isForeignKeyViolation(err) {
		return fmt.Errorf("create event: %w", os.ErrNotExist)
	}
	return fmt.Errorf("create event: %w", err)
}

func insertEventTx(ctx context.Context, tx pgx.Tx, ev *models.Event, p createEventParams) error {
	if err := checkSongbookHostFence(ctx, tx); err != nil {
		return err
	}
	var userDeleting bool
	if err := tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1 FOR UPDATE`, ev.OwnerID).Scan(&userDeleting); err != nil {
		if isNotFound(err) {
			return os.ErrNotExist
		}
		return fmt.Errorf("create event: check owner: %w", err)
	}
	if userDeleting {
		return store.ErrAccountDeleting
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO events (
			id, owner_id, songbook_id, detached_songbook_name, venue_name, name, join_code,
			status, recurring, tip_url, announcement, kiosk_pin, rotation_mode, current_round,
			auto_accept, submission_delay, max_active_per_singer, allow_key_change, allow_write_ins,
			allow_song_replacement, singer_weights, queue_version, metadata_version, restarting, detaching,
			pending_detach_expires_at, deleting, openkj_key, created_at, updated_at, ended_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32
		)`,
		p.id, ev.OwnerID, nullString(ev.SongbookID), strings.TrimSpace(ev.DetachedSongbookName), ev.VenueName, ev.Name, p.joinCode,
		string(p.status), ev.Recurring, ev.TipURL, ev.Announcement, ev.KioskPIN, p.rotationMode, p.currentRound,
		ev.AutoAccept, p.submissionDelay, p.maxActive, ev.AllowKeyChange, ev.AllowWriteIns,
		ev.AllowSongReplacement, p.weightsJSON, p.queueVersion, ev.MetadataVersion, ev.Restarting, ev.Detaching,
		unixToNullTime(ev.PendingDetachTTL), ev.Deleting, p.openKJKey, p.created, p.updated, nullTimePtr(ev.EndedAt), unixToNullTime(ev.TTL),
	)
	if err != nil {
		return mapCreateEventInsertError(err)
	}
	return nil
}

func applyCreatedEventParams(ev *models.Event, p createEventParams) {
	ev.ID = p.id
	ev.CreatedAt = p.created
	ev.UpdatedAt = p.updated
	ev.Status = p.status
	ev.RotationMode = p.rotationMode
	ev.SubmissionDelay = p.submissionDelay
	ev.CurrentRound = p.currentRound
	ev.QueueVersion = p.queueVersion
	ev.MaxActivePerSinger = p.maxActive
	ev.OpenKJKey = p.openKJKey
	ev.JoinCode = p.joinCode
	ev.SingerWeights = p.weights
}

// CreateEvent persists a new karaoke event with atomic owner and host-fence checks.
func (s *Store) CreateEvent(ctx context.Context, ev *models.Event) error {
	if ev == nil || strings.TrimSpace(ev.OwnerID) == "" {
		return fmt.Errorf("create event: owner is required: %w", os.ErrInvalid)
	}

	p, err := prepareCreateEventParams(ev, s.currentTime())
	if err != nil {
		return err
	}

	err = s.withTx(ctx, func(tx pgx.Tx) error {
		return insertEventTx(ctx, tx, ev, p)
	})
	if err != nil {
		return err
	}

	applyCreatedEventParams(ev, p)
	return nil
}

// GetEvent fetches an event by ID.
func (s *Store) GetEvent(ctx context.Context, id string) (*models.Event, error) {
	if id == "" {
		return nil, os.ErrNotExist
	}
	ev, err := s.getEventWhere(ctx, `id = $1`, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("get event %s: %w", id, err)
	}
	return ev, err
}

// GetEventByJoinCode fetches an event by case-insensitive join code.
func (s *Store) GetEventByJoinCode(ctx context.Context, joinCode string) (*models.Event, error) {
	code := strings.ToUpper(strings.TrimSpace(joinCode))
	if code == "" {
		return nil, os.ErrNotExist
	}
	ev, err := s.getEventWhere(ctx, `join_code = $1 LIMIT 1`, code)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("get event by code %s: %w", code, err)
	}
	return ev, err
}

// ListEventsByOwner returns all events owned by ownerID ordered newest first.
func (s *Store) ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error) {
	events, err := s.queryEvents(ctx, `SELECT `+eventSelectColumns+` FROM events WHERE owner_id = $1 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list events by owner %s: %w", ownerID, err)
	}
	return events, nil
}

// ListEventsBySongbook returns all events linked to songbookID ordered newest first.
func (s *Store) ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error) {
	var (
		events []*models.Event
		err    error
	)
	if songbookID == "" {
		events, err = s.queryEvents(ctx, `SELECT `+eventSelectColumns+` FROM events WHERE songbook_id IS NULL ORDER BY created_at DESC`)
	} else {
		events, err = s.queryEvents(ctx, `SELECT `+eventSelectColumns+` FROM events WHERE songbook_id = $1 ORDER BY created_at DESC`, songbookID)
	}
	if err != nil {
		return nil, fmt.Errorf("list events for songbook %s: %w", songbookID, err)
	}
	return events, nil
}

// UpdateEvent commits a current event snapshot without bumping queue_version.
func (s *Store) UpdateEvent(ctx context.Context, ev *models.Event) error {
	return s.UpdateEventWithVersion(ctx, ev, false)
}

func classifyEventSnapshotError(ctx context.Context, tx pgx.Tx, ev *models.Event) error {
	var (
		deleting        bool
		queueVersion    int64
		metadataVersion int64
		restarting      bool
		detaching       bool
	)
	err := tx.QueryRow(ctx, `SELECT deleting, queue_version, metadata_version, restarting, detaching FROM events WHERE id = $1`, ev.ID).
		Scan(&deleting, &queueVersion, &metadataVersion, &restarting, &detaching)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("read event after rejected update: %w", os.ErrNotExist)
		}
		return fmt.Errorf("read event after rejected update: %w", err)
	}
	if deleting {
		return store.ErrEventDeleting
	}
	if queueVersion != ev.QueueVersion || metadataVersion != ev.MetadataVersion || restarting || detaching {
		return &store.QueueVersionConflictError{Current: queueVersion, Expected: ev.QueueVersion}
	}
	return fmt.Errorf("commit event snapshot %s: conflict", ev.ID)
}

func updateEventSnapshotTx(ctx context.Context, tx pgx.Tx, ev *models.Event, bumpVersion bool, now time.Time) (int64, int64, time.Time, error) {
	currentRound := ev.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}
	maxActive := max(0, ev.MaxActivePerSinger)
	weights := models.NormalizeSingerWeights(ev.SingerWeights)
	if weights == nil {
		weights = map[string]int{}
	}
	weightsJSON, err := marshalJSON(weights)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("marshal singer weights: %w", err)
	}
	var qvInc int64
	if bumpVersion {
		qvInc = 1
	}

	var newQV, newMV int64
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE events SET
			name = $1, venue_name = $2, songbook_id = $3, status = $4, recurring = $5,
			tip_url = $6, announcement = $7, kiosk_pin = $8, rotation_mode = $9, current_round = $10,
			auto_accept = $11, submission_delay = $12, max_active_per_singer = $13, allow_key_change = $14,
			allow_write_ins = $15, allow_song_replacement = $16, openkj_key = $17, updated_at = $18,
			expires_at = $19, ended_at = $20, detached_songbook_name = $21, singer_weights = $22,
			metadata_version = metadata_version + 1, queue_version = queue_version + $23
		WHERE id = $24 AND restarting = FALSE AND detaching = FALSE AND deleting = FALSE
			AND queue_version = $25 AND metadata_version = $26
		RETURNING queue_version, metadata_version, updated_at`,
		ev.Name, ev.VenueName, nullString(ev.SongbookID), string(ev.Status), ev.Recurring,
		ev.TipURL, ev.Announcement, ev.KioskPIN, ev.RotationMode, currentRound,
		ev.AutoAccept, ev.SubmissionDelay, maxActive, ev.AllowKeyChange,
		ev.AllowWriteIns, ev.AllowSongReplacement, ev.OpenKJKey, now,
		unixToNullTime(ev.TTL), nullTimePtr(ev.EndedAt), strings.TrimSpace(ev.DetachedSongbookName), weightsJSON, qvInc,
		ev.ID, ev.QueueVersion, ev.MetadataVersion,
	).Scan(&newQV, &newMV, &updatedAt)
	if err != nil {
		if isNotFound(err) {
			return 0, 0, time.Time{}, classifyEventSnapshotError(ctx, tx, ev)
		}
		if isForeignKeyViolation(err) {
			return 0, 0, time.Time{}, fmt.Errorf("commit event snapshot %s: %w", ev.ID, os.ErrNotExist)
		}
		return 0, 0, time.Time{}, fmt.Errorf("commit event snapshot %s: %w", ev.ID, err)
	}
	return newQV, newMV, updatedAt.UTC(), nil
}

// UpdateEventWithVersion compares both snapshot versions, advances metadata_version,
// and optionally increments queue_version atomically in the same transaction.
func (s *Store) UpdateEventWithVersion(ctx context.Context, ev *models.Event, bumpVersion bool) error {
	if ev == nil || strings.TrimSpace(ev.ID) == "" {
		return fmt.Errorf("update event: invalid event")
	}

	var newQV, newMV int64
	var updatedAt time.Time
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookHostFence(ctx, tx); err != nil {
			return err
		}
		var txErr error
		newQV, newMV, updatedAt, txErr = updateEventSnapshotTx(ctx, tx, ev, bumpVersion, s.currentTime())
		return txErr
	})
	if err != nil {
		return err
	}

	ev.MetadataVersion = newMV
	if bumpVersion {
		ev.QueueVersion = newQV
	}
	ev.UpdatedAt = updatedAt
	return nil
}

func updateRequestRotationTx(ctx context.Context, tx pgx.Tx, eventID string, req *models.SongRequest, now time.Time) error {
	if req == nil || req.ID == "" {
		return fmt.Errorf("transition queue rotation: invalid request: %w", os.ErrInvalid)
	}
	tag, err := tx.Exec(ctx, `UPDATE event_requests SET rank = $1, round = $2, updated_at = $3 WHERE id = $4 AND event_id = $5`,
		req.Rank, req.Round, now, req.ID, eventID)
	if err != nil {
		return fmt.Errorf("update request %s during rotation transition: %w", req.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("request %s not found in event %s: %w", req.ID, eventID, os.ErrNotExist)
	}
	return nil
}

// TransitionQueueRotation atomically applies a queue rotation mode transition,
// persisting the updated requests and event metadata in a single transaction.
func (s *Store) TransitionQueueRotation(ctx context.Context, ev *models.Event, reqs []*models.SongRequest) error {
	if ev == nil || strings.TrimSpace(ev.ID) == "" {
		return fmt.Errorf("transition queue rotation: invalid event")
	}

	var newQV, newMV int64
	var updatedAt time.Time
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookHostFence(ctx, tx); err != nil {
			return err
		}
		now := s.currentTime()
		var txErr error
		newQV, newMV, updatedAt, txErr = updateEventSnapshotTx(ctx, tx, ev, true, now)
		if txErr != nil {
			return txErr
		}
		for _, req := range reqs {
			if err := updateRequestRotationTx(ctx, tx, ev.ID, req, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	ev.QueueVersion = newQV
	ev.MetadataVersion = newMV
	ev.UpdatedAt = updatedAt
	return nil
}

func classifyBumpQueueVersionNotFound(ctx context.Context, tx pgx.Tx, eventID string) error {
	var deleting, restarting bool
	if err := tx.QueryRow(ctx, `SELECT deleting, restarting FROM events WHERE id = $1`, eventID).Scan(&deleting, &restarting); err != nil {
		if isNotFound(err) {
			return fmt.Errorf("bump queue version for event %s: %w", eventID, os.ErrNotExist)
		}
		return fmt.Errorf("bump queue version for event %s: %w", eventID, err)
	}
	if deleting {
		return fmt.Errorf("bump queue version for event %s: %w", eventID, store.ErrEventDeleting)
	}
	return fmt.Errorf("bump queue version for event %s: %w", eventID, store.ErrAdmissionConflict)
}

// BumpQueueVersion atomically marks an event's public queue snapshot stale.
func (s *Store) BumpQueueVersion(ctx context.Context, eventID string) (int64, error) {
	var newVersion int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.currentTime()
		err := tx.QueryRow(ctx, `
			UPDATE events SET queue_version = queue_version + 1, updated_at = $1
			WHERE id = $2 AND restarting = FALSE AND deleting = FALSE
			RETURNING queue_version`, now, eventID).Scan(&newVersion)
		if err == nil {
			return nil
		}
		if isNotFound(err) {
			return classifyBumpQueueVersionNotFound(ctx, tx, eventID)
		}
		return fmt.Errorf("bump queue version for event %s: %w", eventID, err)
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}

// BeginEventDeletion marks an event as ended and deleting before child enumeration.
func (s *Store) BeginEventDeletion(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.currentTime()
		tag, err := tx.Exec(ctx, `
			UPDATE events SET status = $1, deleting = TRUE, updated_at = $2, ended_at = COALESCE(ended_at, $2)
			WHERE id = $3`, string(models.EventStatusEnded), now, id)
		if err != nil {
			return fmt.Errorf("begin event deletion: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return os.ErrNotExist
		}
		return nil
	})
}

// DeleteEvent removes an event and all of its requests atomically.
func (s *Store) DeleteEvent(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM event_requests WHERE event_id = $1`, id); err != nil {
			return fmt.Errorf("delete requests for event %s: %w", id, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM events WHERE id = $1`, id); err != nil {
			return fmt.Errorf("delete event %s: %w", id, err)
		}
		return nil
	})
}
