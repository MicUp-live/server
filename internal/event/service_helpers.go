package event

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const maxRestartAttempts = 3

func isValidRotationMode(mode string) bool {
	return mode == models.RotationModeRoundRobin ||
		mode == models.RotationModeFairPriority ||
		mode == models.RotationModeFCFS ||
		mode == models.RotationModeManual
}

func normalizeRotationMode(raw string) string {
	if raw == "" {
		return models.RotationModeRoundRobin
	}
	mode := strings.ToLower(strings.TrimSpace(raw))
	if isValidRotationMode(mode) {
		return mode
	}
	return models.RotationModeRoundRobin
}

func clampSubmissionDelay(v *int) int {
	if v == nil {
		return 0
	}
	if *v < 0 {
		return 0
	}
	if *v > 600 {
		return 600
	}
	return *v
}

func clampMaxActivePerSinger(v *int) int {
	if v == nil {
		return 1
	}
	if *v < 0 {
		return 0
	}
	return *v
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func (s *Service) resolvePublicEvent(ctx context.Context, event *models.Event) (*models.PublicEvent, error) {
	if event.SongbookID == "" {
		return s.buildPublicEvent(event, s.detachedPublicSongbook(event)), nil
	}
	if s.Songbooks == nil {
		return nil, ErrLinkedBookNotFound
	}
	sb, err := s.Songbooks.GetSongbook(ctx, event.SongbookID)
	if err != nil || sb == nil {
		if event.Status == models.EventStatusEnded {
			return s.buildPublicEvent(event, s.detachedPublicSongbook(event)), nil
		}
		return nil, ErrLinkedBookNotFound
	}
	return s.buildPublicEvent(event, sb), nil
}

func (s *Service) lookupPublicSongbookByCode(ctx context.Context, code string) *CodeResolutionResult {
	if s.Songbooks == nil {
		return nil
	}
	sb, err := s.Songbooks.GetPublicSongbook(ctx, code)
	if err != nil || sb == nil {
		return nil
	}
	slugOrID := sb.Slug
	if slugOrID == "" {
		slugOrID = sb.PublicID
	}
	return &CodeResolutionResult{
		Type: "songbook",
		Songbook: &PublicSongbookInfo{
			ID:         sb.ID,
			PublicID:   sb.PublicID,
			Slug:       sb.Slug,
			Name:       sb.Name,
			SongCount:  sb.SongCount,
			TargetPath: "/book/" + slugOrID,
		},
	}
}

func (s *Service) applyRotationModeUpdate(ctx context.Context, event *models.Event, rawMode *string) ([]*models.SongRequest, error) {
	if rawMode == nil {
		return nil, nil
	}
	mode := strings.TrimSpace(*rawMode)
	if !isValidRotationMode(mode) || mode == event.RotationMode {
		return nil, nil
	}
	oldMode := event.RotationMode
	reqs, err := s.planQueueRotationTransition(ctx, event, oldMode, mode)
	if err != nil {
		return nil, ErrFailedTransition
	}
	event.RotationMode = mode
	return reqs, nil
}

func (s *Service) applyStatusUpdate(event *models.Event, rawStatus *models.EventStatus) error {
	if rawStatus == nil {
		return nil
	}
	switch *rawStatus {
	case models.EventStatusActive, models.EventStatusPaused:
		event.Status = *rawStatus
		event.EndedAt = nil
		event.TTL = 0
	case models.EventStatusEnded:
		event.Status = *rawStatus
		if event.EndedAt == nil || event.TTL == 0 {
			now := time.Now().UTC()
			event.EndedAt = &now
			event.TTL = now.Add(s.QueueRetentionDuration()).Unix()
		}
	default:
		return ErrInvalidStatus
	}
	return nil
}

func (s *Service) persistUpdatedEvent(fencedCtx context.Context, event *models.Event, modeTransitionRequests []*models.SongRequest) error {
	var err error
	if len(modeTransitionRequests) > 0 {
		err = s.Events.TransitionQueueRotation(fencedCtx, event, modeTransitionRequests)
	} else {
		err = s.Events.UpdateEventWithVersion(fencedCtx, event, true)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrEventNotFound
		}
		return err
	}
	return nil
}

func (s *Service) syncEventRequestsTTL(ctx context.Context, event *models.Event, status *models.EventStatus) error {
	if status == nil || s.Requests == nil {
		return nil
	}
	var expiresAt time.Time
	if event.TTL > 0 {
		expiresAt = time.Unix(event.TTL, 0)
	}
	if err := s.Requests.SetRequestsTTLByEvent(ctx, event.ID, expiresAt); err != nil {
		return fmt.Errorf("set event request retention: %w", err)
	}
	return nil
}

func isQueueVersionConflict(err error) bool {
	var conflict *store.QueueVersionConflictError
	return errors.As(err, &conflict)
}

func (s *Service) loadEventForRestart(ctx context.Context, id string) (*models.Event, error) {
	event, err := s.Events.GetEvent(ctx, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("load event for restart: %w", err)
	}
	if event == nil {
		return nil, ErrEventNotFound
	}
	return event, nil
}

func (s *Service) reloadEventBeforeRestart(ctx context.Context, id string) (*models.Event, context.Context, error) {
	event, err := s.Events.GetEvent(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("reload event before restart: %w", err)
	}
	if event == nil {
		return nil, nil, ErrEventNotFound
	}
	fencedCtx, err := s.prepareRestartHostingFence(ctx, event)
	if err != nil {
		return nil, nil, err
	}
	return event, fencedCtx, nil
}

func (s *Service) beginQueueRestart(ctx, fencedCtx context.Context, id string, event *models.Event) (context.Context, error) {
	for conflicts := 0; !event.Restarting; conflicts++ {
		if conflicts == maxRestartAttempts {
			return nil, fmt.Errorf("queue restart could not begin after conflicts: %w", store.ErrAdmissionConflict)
		}
		err := s.Events.BeginQueueRestart(fencedCtx, event)
		if err == nil {
			break
		}
		if !isQueueVersionConflict(err) {
			return nil, err
		}
		event, fencedCtx, err = s.reloadEventBeforeRestart(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	return fencedCtx, nil
}

func isRestartArchivable(req *models.SongRequest) bool {
	return req.Status == models.StatusPending ||
		req.Status == models.StatusAccepted ||
		req.Status == models.StatusUpNext ||
		req.TTL != 0
}

func selectRestartArchiveBatch(existing []*models.SongRequest) []*models.SongRequest {
	batch := make([]*models.SongRequest, 0, 49)
	for _, req := range existing {
		if !isRestartArchivable(req) {
			continue
		}
		batch = append(batch, req)
		if len(batch) == 99 {
			break
		}
	}
	return batch
}

func (s *Service) stepQueueRestart(ctx, fencedCtx context.Context, id string) (*models.Event, bool, error) {
	current, err := s.Events.GetEvent(ctx, id)
	if err != nil {
		return nil, false, fmt.Errorf("reload restarting event: %w", err)
	}
	if !current.Restarting {
		return nil, false, fmt.Errorf("queue restart state changed")
	}
	existing, err := s.Requests.ListRequestsByEvent(ctx, id)
	if err != nil {
		return nil, false, fmt.Errorf("list requests during restart: %w", err)
	}
	if batch := selectRestartArchiveBatch(existing); len(batch) > 0 {
		return nil, true, s.Events.ArchiveRestartBatch(ctx, id, current.QueueVersion, batch)
	}
	slots, err := s.Events.ListRestartSlots(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if len(slots) > 0 {
		if len(slots) > 99 {
			slots = slots[:99]
		}
		return nil, true, s.Events.DeleteRestartSlots(ctx, id, current.QueueVersion, slots)
	}
	if err := s.Events.FinishQueueRestart(fencedCtx, id, current.QueueVersion); err != nil {
		return nil, false, err
	}
	event, err := s.Events.GetEvent(ctx, id)
	return event, false, err
}

func (s *Service) restartWithClearedQueue(ctx, fencedCtx context.Context, id string, event *models.Event) (*models.Event, error) {
	if s.Requests == nil {
		return nil, fmt.Errorf("atomic queue restart store and request reader are required")
	}
	fencedCtx, err := s.beginQueueRestart(ctx, fencedCtx, id, event)
	if err != nil {
		return nil, err
	}
	for conflicts := 0; conflicts < maxRestartAttempts; {
		restarted, retry, err := s.stepQueueRestart(ctx, fencedCtx, id)
		if err != nil {
			if isQueueVersionConflict(err) {
				conflicts++
				continue
			}
			return nil, err
		}
		if !retry {
			return restarted, nil
		}
		conflicts = 0 // successful progress resets the bounded conflict budget
	}
	return nil, fmt.Errorf("queue restart exhausted conflict retries: %w", store.ErrAdmissionConflict)
}
