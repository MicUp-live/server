package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

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
	if s.Events == nil {
		return nil, ErrEventNotFound
	}
	event, err := s.Events.GetEvent(ctx, cmd.EventID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("load event for request admission: %w", err)
	}
	if event == nil {
		return nil, ErrEventNotFound
	}

	if event.Status == models.EventStatusPaused {
		return nil, ErrShowPaused
	}
	if event.Status == models.EventStatusEnded {
		return nil, ErrShowEnded
	}
	if cmd.IsWriteIn && !event.AllowWriteIns {
		return nil, ErrWriteInsDisabled
	}

	singerName := truncateUTF8(strings.TrimSpace(cmd.SingerName), 60)
	if singerName == "" {
		return nil, fmt.Errorf("singer name is required: %w", os.ErrInvalid)
	}

	songTitle := truncateUTF8(strings.TrimSpace(cmd.SongTitle), 150)
	if songTitle == "" {
		return nil, fmt.Errorf("song title is required: %w", os.ErrInvalid)
	}

	songArtist := truncateUTF8(strings.TrimSpace(cmd.SongArtist), 150)

	keyChange := cmd.KeyChange
	if !event.AllowKeyChange {
		keyChange = 0
	} else if keyChange < -6 {
		keyChange = -6
	} else if keyChange > 6 {
		keyChange = 6
	}

	notes := truncateUTF8(strings.TrimSpace(cmd.Notes), 300)

	existingReqs, err := s.Requests.ListRequestsByEvent(ctx, event.ID)
	if err != nil {
		return nil, fmt.Errorf("load queue for request admission: %w", err)
	}

	normSinger := strings.ToLower(singerName)

	// Submission delay check (burst protection)
	if event.SubmissionDelay > 0 {
		for _, er := range existingReqs {
			if strings.ToLower(strings.TrimSpace(er.SingerName)) == normSinger &&
				(er.Status == models.StatusPending || er.Status == models.StatusAccepted || er.Status == models.StatusUpNext) {
				elapsed := time.Since(er.CreatedAt)
				delay := time.Duration(event.SubmissionDelay) * time.Second
				if elapsed < delay {
					remaining := int((delay - elapsed).Seconds()) + 1
					return nil, &ErrCooldownActive{RemainingSeconds: remaining}
				}
			}
		}
	}

	// Singer turn and active count calculation
	completedCount := 0
	activeAhead := 0
	for _, er := range existingReqs {
		if strings.ToLower(strings.TrimSpace(er.SingerName)) == normSinger {
			if er.Status == models.StatusCompleted {
				completedCount++
			} else if er.Status == models.StatusPending || er.Status == models.StatusAccepted || er.Status == models.StatusUpNext {
				activeAhead++
			}
		}
	}

	if event.MaxActivePerSinger > 0 && activeAhead >= event.MaxActivePerSinger {
		return nil, ErrSingerLimitExceeded
	}

	initialStatus := models.StatusPending
	if event.RotationMode == models.RotationModeManual {
		initialStatus = models.StatusPending
	} else if event.AutoAccept {
		initialStatus = models.StatusAccepted
	}

	now := time.Now().UTC()
	nowMilli := now.UnixMilli()
	seq := int64(len(existingReqs) + 1)
	singerTurn := completedCount + activeAhead + 1

	songID := strings.TrimSpace(cmd.SongID)
	if cmd.IsWriteIn {
		songID = ""
	}
	songReq := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    event.ID,
		SongbookID: event.SongbookID,
		SongID:     songID,
		IsWriteIn:  cmd.IsWriteIn,
		SongTitle:  songTitle,
		SongArtist: songArtist,
		SingerName: singerName,
		KeyChange:  keyChange,
		Notes:      notes,
		Status:     initialStatus,
		SingerTurn: singerTurn,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	songReq.CancelToken = GenerateCancelToken(s.Secret, songReq.ID, songReq.EventID)

	var reorderIDs []string

	if initialStatus == models.StatusAccepted {
		placement := PlaceAcceptedRequest(event, songReq, existingReqs, now)
		reorderIDs = placement.ReorderIDs
	} else {
		songReq.Rank = nowMilli + seq
		songReq.Round = 0
	}

	// Atomic commit against the event version used to plan this placement.
	if err := s.Requests.CreateRequestAtVersion(ctx, songReq, event.MaxActivePerSinger, reorderIDs, event.QueueVersion); err != nil {
		if errors.Is(err, store.ErrEventNotActive) {
			current, readErr := s.Events.GetEvent(ctx, event.ID)
			if readErr != nil {
				return nil, fmt.Errorf("reload event after admission conflict: %w", readErr)
			}
			if current == nil {
				return nil, ErrEventNotFound
			}
			switch current.Status {
			case models.EventStatusPaused:
				return nil, ErrShowPaused
			case models.EventStatusEnded:
				return nil, ErrShowEnded
			}
		}
		return nil, err
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
	route := fmt.Sprintf("events/%s/requests", cmd.EventID)
	reqHash := mutationHash(cmd.Update)

	// Idempotency check
	if cmd.IdempotencyKey != "" {
		if cmd.HostOwnerID == "" {
			return nil, nil, ErrUnauthorized
		}
		result, found, replayErr := s.Requests.GetIdempotencyResult(ctx, cmd.IdempotencyKey, cmd.HostOwnerID, route, cmd.RequestID, reqHash)
		if replayErr != nil {
			return nil, nil, replayErr
		}
		if found {
			return nil, result, nil
		}
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

	var reorderIDs []string

	isEnteringActiveQueue := (oldStatus == models.StatusPending || oldStatus == models.StatusRejected || oldStatus == models.StatusCancelled) &&
		(req.Status == models.StatusAccepted || req.Status == models.StatusUpNext)

	if isEnteringActiveQueue && req.Status == models.StatusAccepted && cmd.Update.Rank == nil && cmd.Update.Round == nil {
		event, err := s.Events.GetEvent(ctx, cmd.EventID)
		if err != nil || event == nil {
			return nil, nil, ErrEventNotFound
		}
		allReqs, err := s.Requests.ListRequestsByEvent(ctx, cmd.EventID)
		if err != nil {
			return nil, nil, err
		}
		placement := PlaceAcceptedRequest(event, req, allReqs, time.Now())
		reorderIDs = placement.ReorderIDs
	} else if (oldStatus == models.StatusPending || oldStatus == models.StatusRejected || oldStatus == models.StatusCancelled) && req.Status == models.StatusUpNext && cmd.Update.Rank == nil {
		allReqs, err := s.Requests.ListRequestsByEvent(ctx, cmd.EventID)
		if err != nil {
			return nil, nil, err
		}
		var activeQueue []*models.SongRequest
		for _, er := range allReqs {
			if er.ID != req.ID && (er.Status == models.StatusAccepted || er.Status == models.StatusUpNext) {
				activeQueue = append(activeQueue, er)
			}
		}
		sort.SliceStable(activeQueue, func(i, j int) bool {
			if activeQueue[i].Rank != activeQueue[j].Rank {
				return activeQueue[i].Rank < activeQueue[j].Rank
			}
			return activeQueue[i].CreatedAt.Before(activeQueue[j].CreatedAt)
		})
		if len(activeQueue) == 0 {
			req.Rank = 1
		} else if len(activeQueue) == 1 {
			req.Rank = activeQueue[0].Rank + 1
		} else {
			prevRank := activeQueue[0].Rank
			nextRank := activeQueue[1].Rank
			if nextRank-prevRank > 1 {
				req.Rank = prevRank + (nextRank-prevRank)/2
			} else {
				req.Rank = prevRank + 1
				reorderIDs = make([]string, 0, len(activeQueue)+1)
				reorderIDs = append(reorderIDs, activeQueue[0].ID, req.ID)
				for _, ar := range activeQueue[1:] {
					reorderIDs = append(reorderIDs, ar.ID)
				}
			}
		}
		req.Round = 0
	} else {
		if cmd.Update.Rank != nil {
			req.Rank = *cmd.Update.Rank
		}
		if cmd.Update.Round != nil && *cmd.Update.Round > 0 {
			req.Round = *cmd.Update.Round
			if cmd.Update.Rank == nil {
				intraRound := req.Rank % models.RoundRankMultiplier
				if intraRound <= 0 || intraRound < 1_000_000_000_000 {
					if !req.CreatedAt.IsZero() {
						intraRound = req.CreatedAt.UnixMilli()
					} else {
						intraRound = time.Now().UTC().UnixMilli()
					}
				}
				req.Rank = (int64(req.Round) * models.RoundRankMultiplier) + intraRound
			}
		}
	}

	if cmd.Update.SongID != nil {
		req.SongID = strings.TrimSpace(*cmd.Update.SongID)
	}
	if cmd.Update.SongTitle != nil && strings.TrimSpace(*cmd.Update.SongTitle) != "" {
		req.SongTitle = strings.TrimSpace(*cmd.Update.SongTitle)
	}
	if cmd.Update.SongArtist != nil && strings.TrimSpace(*cmd.Update.SongArtist) != "" {
		req.SongArtist = strings.TrimSpace(*cmd.Update.SongArtist)
	}
	if cmd.Update.SingerName != nil && strings.TrimSpace(*cmd.Update.SingerName) != "" {
		req.SingerName = strings.TrimSpace(*cmd.Update.SingerName)
	}
	if cmd.Update.KeyChange != nil {
		req.KeyChange = *cmd.Update.KeyChange
	}
	if cmd.Update.Notes != nil {
		req.Notes = *cmd.Update.Notes
	}
	if cmd.Update.DeclineReason != nil {
		req.DeclineReason = *cmd.Update.DeclineReason
	}
	now := time.Now().UTC()
	syncCompletedAt(req, oldStatus, now)
	req.UpdatedAt = now

	// Persist via atomic order store
	if cmd.IdempotencyKey != "" {
		result, err := s.Requests.UpdateRequestAndReorderWithIdempotency(ctx, req, reorderIDs, cmd.ExpectedVer, cmd.IdempotencyKey, cmd.HostOwnerID, route, reqHash)
		if err != nil {
			return nil, nil, err
		}
		return req, result, nil
	}

	if cmd.ExpectedVer != nil {
		if err := s.Requests.UpdateRequestAndReorderWithQueueVersion(ctx, req, reorderIDs, *cmd.ExpectedVer); err != nil {
			return nil, nil, err
		}
		return req, nil, nil
	}

	if isEnteringActiveQueue || len(reorderIDs) > 0 {
		if err := s.Requests.UpdateRequestAndReorder(ctx, req, reorderIDs); err != nil {
			return nil, nil, err
		}
		return req, nil, nil
	}

	if err := s.Requests.UpdateRequest(ctx, req); err != nil {
		return nil, nil, err
	}

	return req, nil, nil
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
