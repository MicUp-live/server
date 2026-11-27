package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

var (
	ErrShowPaused          = errors.New("requests are temporarily paused for this show")
	ErrShowEnded           = errors.New("this show has ended")
	ErrWriteInsDisabled    = errors.New("write-in requests are not enabled for this show")
	ErrRequestNotFound     = errors.New("request not found")
	ErrEventNotFound       = errors.New("event not found")
	ErrSingerLimitExceeded = store.ErrSingerLimitExceeded
	ErrUnauthorized        = errors.New("unauthorized")
	ErrInvalidOrder        = errors.New("invalid reorder list")
)

// ErrCooldownActive is returned when a singer submits faster than the event's SubmissionDelay.
type ErrCooldownActive struct {
	RemainingSeconds int
}

func (e *ErrCooldownActive) Error() string {
	return fmt.Sprintf("please wait %d second(s) before submitting another request", e.RemainingSeconds)
}

func truncateUTF8(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// Service provides the canonical application interface for queue intake, rotation, and lifecycle.
type Service struct {
	Requests store.RequestStore
	Events   store.EventStore
	Secret   []byte
}

// NewService constructs a new canonical Queue Service.
func NewService(requests store.RequestStore, events store.EventStore, secret []byte) *Service {
	return &Service{
		Requests: requests,
		Events:   events,
		Secret:   secret,
	}
}

// SubmitRequestCmd encapsulates inputs for a singer submitting a song request.
type SubmitRequestCmd struct {
	EventID    string
	SongID     string
	IsWriteIn  bool
	SongTitle  string
	SongArtist string
	SingerName string
	KeyChange  int
	Notes      string
}

// SubmitRequest processes request intake, validates limits and delays, plans placement,
// and commits the request atomically.
func (s *Service) SubmitRequest(ctx context.Context, cmd SubmitRequestCmd) (*models.SongRequest, error) {
	const maxAdmissionAttempts = 3
	for attempt := 0; attempt < maxAdmissionAttempts; attempt++ {
		req, err := s.submitRequestOnce(ctx, cmd)
		if err == nil {
			return req, nil
		}
		var versionConflict *store.QueueVersionConflictError
		if !errors.Is(err, store.ErrAdmissionConflict) && !errors.As(err, &versionConflict) {
			return nil, err
		}
	}
	return nil, store.ErrAdmissionConflict
}

func (s *Service) submitRequestOnce(ctx context.Context, cmd SubmitRequestCmd) (*models.SongRequest, error) {
	event, err := s.loadAdmissibleEvent(ctx, cmd.EventID)
	if err != nil {
		return nil, err
	}

	input, err := validateSubmitInput(cmd, event)
	if err != nil {
		return nil, err
	}

	existingReqs, err := s.Requests.ListRequestsByEvent(ctx, event.ID)
	if err != nil {
		return nil, fmt.Errorf("load queue for request admission: %w", err)
	}

	normSinger := strings.ToLower(input.singerName)
	if err := checkSubmissionDelay(existingReqs, normSinger, event.SubmissionDelay); err != nil {
		return nil, err
	}

	completedCount, activeAhead := countSingerRequests(existingReqs, normSinger)
	if event.MaxActivePerSinger > 0 && activeAhead >= event.MaxActivePerSinger {
		return nil, ErrSingerLimitExceeded
	}

	songReq, reorderIDs := s.buildSubmittedRequest(event, input, existingReqs, completedCount, activeAhead)

	// Atomic commit against the event version used to plan this placement.
	if err := s.Requests.CreateRequestAtVersion(ctx, songReq, event.MaxActivePerSinger, reorderIDs, event.QueueVersion); err != nil {
		return nil, s.mapCreateRequestError(ctx, event.ID, err)
	}

	return songReq, nil
}

// UpdateEventRequestCmd encapsulates host queue updates.
type UpdateEventRequestCmd struct {
	EventID        string
	RequestID      string
	Update         models.RequestUpdate
	ExpectedVer    *int64
	IdempotencyKey string
	HostOwnerID    string
}

// UpdateEventRequest updates request status, round, notes, or key change with atomic versioning and idempotency.
func (s *Service) UpdateEventRequest(ctx context.Context, cmd UpdateEventRequestCmd) (*models.SongRequest, *store.QueueMutationResult, error) {
	meta := idempotencyMeta{
		route:   fmt.Sprintf("events/%s/requests", cmd.EventID),
		reqHash: mutationHash(cmd.Update),
	}

	if result, found, replayErr := s.checkUpdateIdempotency(ctx, cmd, meta); replayErr != nil {
		return nil, nil, replayErr
	} else if found {
		return nil, result, nil
	}

	req, err := s.Requests.GetRequest(ctx, cmd.RequestID)
	if err != nil || (req.EventID != "" && req.EventID != cmd.EventID) {
		return nil, nil, ErrRequestNotFound
	}

	reqCopy := *req
	req = &reqCopy

	oldStatus := req.Status
	if cmd.Update.Status != nil {
		req.Status = *cmd.Update.Status
	}

	enteringActive := isEnteringActiveQueueStatus(oldStatus, req.Status)
	reorderIDs, err := s.planRequestPlacement(ctx, cmd, req, enteringActive)
	if err != nil {
		return nil, nil, err
	}

	applyRequestFieldUpdates(req, cmd.Update, oldStatus)

	return s.persistUpdatedRequest(ctx, cmd, req, reorderIDs, enteringActive, meta)
}

// ReorderEventRequestsCmd encapsulates host manual reorder inputs.
type ReorderEventRequestsCmd struct {
	EventID        string
	Order          []string
	ExpectedVer    *int64
	IdempotencyKey string
	HostOwnerID    string
}

// ReorderEventRequests validates and commits manual queue reordering.
func (s *Service) ReorderEventRequests(ctx context.Context, cmd ReorderEventRequestsCmd) (*store.QueueMutationResult, error) {
	if len(cmd.Order) > 99 {
		return nil, fmt.Errorf("reorder list exceeds atomic limit of 99: %w", os.ErrInvalid)
	}
	seen := make(map[string]struct{}, len(cmd.Order))
	for _, id := range cmd.Order {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate request ID %s in reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
	}

	route := fmt.Sprintf("events/%s/requests/reorder", cmd.EventID)
	reqHash := mutationHash(cmd.Order)

	if cmd.IdempotencyKey != "" {
		if cmd.HostOwnerID == "" {
			return nil, ErrUnauthorized
		}
		if result, found, replayErr := s.Requests.GetIdempotencyResult(ctx, cmd.IdempotencyKey, cmd.HostOwnerID, route, cmd.EventID, reqHash); replayErr != nil {
			return nil, replayErr
		} else if found {
			return result, nil
		}
		return s.Requests.ReorderRequestsWithIdempotency(ctx, cmd.EventID, cmd.Order, cmd.ExpectedVer, cmd.IdempotencyKey, cmd.HostOwnerID, route, reqHash)
	}

	if cmd.ExpectedVer != nil {
		err := s.Requests.ReorderRequestsWithQueueVersion(ctx, cmd.EventID, cmd.Order, *cmd.ExpectedVer)
		return nil, err
	}

	err := s.Requests.ReorderRequests(ctx, cmd.EventID, cmd.Order)
	return nil, err
}

// DeleteRequestCmd encapsulates request deletion.
type DeleteRequestCmd struct {
	EventID   string
	RequestID string
}

// DeleteRequest removes an already-authorized request. Event-backed deletion
// requires a store operation that commits removal and queue version together.
func (s *Service) DeleteRequest(ctx context.Context, cmd DeleteRequestCmd) error {
	req, err := s.Requests.GetRequest(ctx, cmd.RequestID)
	if errors.Is(err, os.ErrNotExist) {
		return ErrRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("load request for deletion: %w", err)
	}
	if req == nil || req.EventID != cmd.EventID {
		return ErrRequestNotFound
	}

	if req.EventID != "" {
		return s.Requests.DeleteEventRequestAtomic(ctx, req.EventID, req.ID)
	}
	return s.Requests.DeleteRequest(ctx, req.ID)
}

// GetPublicQueue returns sanitized queue items and event metadata.
func (s *Service) GetPublicQueue(ctx context.Context, eventID string) (*models.Event, []models.PublicQueueItem, error) {
	if s.Events == nil {
		return nil, nil, ErrEventNotFound
	}
	event, err := s.Events.GetEvent(ctx, eventID)
	if err != nil || event == nil {
		return nil, nil, ErrEventNotFound
	}

	requests, err := s.Requests.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, nil, err
	}

	items := make([]models.PublicQueueItem, 0, len(requests))
	for _, req := range requests {
		if req.Status == models.StatusAccepted || req.Status == models.StatusUpNext {
			round := req.Round
			if round == 0 && event.RotationMode == models.RotationModeRoundRobin {
				round = models.ResolveRound(req.Round, req.Rank)
			}
			items = append(items, models.PublicQueueItem{
				ID:         req.ID,
				SingerName: req.SingerName,
				SongTitle:  req.SongTitle,
				SongArtist: req.SongArtist,
				IsWriteIn:  req.IsWriteIn,
				KeyChange:  req.KeyChange,
				Status:     req.Status,
				Rank:       req.Rank,
				Round:      round,
				SingerTurn: req.SingerTurn,
				CreatedAt:  req.CreatedAt,
			})
		}
	}

	return event, items, nil
}

// ListRequests returns all requests and the current queue version for an event.
func (s *Service) ListRequests(ctx context.Context, eventID string) (*models.Event, []*models.SongRequest, error) {
	if s.Events == nil {
		return nil, nil, ErrEventNotFound
	}
	event, err := s.Events.GetEvent(ctx, eventID)
	if err != nil || event == nil {
		return nil, nil, ErrEventNotFound
	}

	requests, err := s.Requests.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if requests == nil {
		requests = []*models.SongRequest{}
	}
	for _, req := range requests {
		req.Round = models.ResolveRound(req.Round, req.Rank)
	}

	return event, requests, nil
}

func mutationHash(payload interface{}) string {
	data, _ := json.Marshal(payload)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:])
}
