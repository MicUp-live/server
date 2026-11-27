package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// DefaultDeferPositions is the number of active queue spots a deferred absent
// singer is bumped down (or to the end of the active queue if fewer remain).
const DefaultDeferPositions = 3

var (
	ErrSongReplacementDisabled = store.ErrSongReplacementDisabled
	ErrRequestAlreadyCalled    = store.ErrRequestAlreadyCalled
	ErrRequestNotDeferrable    = errors.New("only active or called requests can be deferred")
)

// ReplaceRequestSongCmd encapsulates inputs for singer-initiated pre-call song replacement.
type ReplaceRequestSongCmd struct {
	EventID    string
	RequestID  string
	EditToken  string
	SongID     string
	IsWriteIn  bool
	SongTitle  string
	SongArtist string
	KeyChange  int
	Notes      string
}

func mapReplaceSongError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrRequestNotFound
	case errors.Is(err, store.ErrEventEnded), errors.Is(err, store.ErrEventDeleting):
		return ErrShowEnded
	case errors.Is(err, store.ErrWriteInsDisabled):
		return ErrWriteInsDisabled
	case errors.Is(err, store.ErrSongReplacementDisabled):
		return ErrSongReplacementDisabled
	case errors.Is(err, store.ErrRequestAlreadyCalled):
		return ErrRequestAlreadyCalled
	default:
		return err
	}
}

// ReplaceRequestSong allows an authorized singer holding a domain-separated edit
// capability token to replace their queued song before being called to stage,
// preserving request ID, status, rank, round, and singer slot.
func (s *Service) ReplaceRequestSong(ctx context.Context, cmd ReplaceRequestSongCmd) (*models.SongRequest, int64, error) {
	if s.Requests == nil || s.Events == nil {
		return nil, 0, ErrEventNotFound
	}
	if !ValidateEditToken(s.Secret, cmd.EditToken, cmd.RequestID, cmd.EventID) {
		return nil, 0, ErrUnauthorized
	}

	songTitle := truncateUTF8(strings.TrimSpace(cmd.SongTitle), 150)
	if songTitle == "" {
		return nil, 0, fmt.Errorf("song title is required: %w", os.ErrInvalid)
	}
	songID := strings.TrimSpace(cmd.SongID)
	if cmd.IsWriteIn {
		songID = ""
	}

	updated, newVersion, err := s.Requests.ReplaceRequestSongAtomic(ctx, cmd.EventID, cmd.RequestID, store.SongReplacementUpdate{
		SongID:     songID,
		IsWriteIn:  cmd.IsWriteIn,
		SongTitle:  songTitle,
		SongArtist: truncateUTF8(strings.TrimSpace(cmd.SongArtist), 150),
		KeyChange:  cmd.KeyChange,
		Notes:      truncateUTF8(strings.TrimSpace(cmd.Notes), 300),
	})
	if err != nil {
		return nil, 0, mapReplaceSongError(err)
	}
	return updated, newVersion, nil
}

// DeferPlan describes the computed status, round, and reorder IDs for deferring
// an absent singer without recording a completed performance.
type DeferPlan struct {
	Target     *models.SongRequest
	ReorderIDs []string
	Changed    bool
}

func isActiveRequestStatus(status models.RequestStatus) bool {
	return status == models.StatusAccepted || status == models.StatusUpNext
}

func sortActiveRequests(event *models.Event, active []*models.SongRequest) {
	isRoundRobin := event != nil && event.RotationMode == models.RotationModeRoundRobin
	sort.SliceStable(active, func(i, j int) bool {
		if isRoundRobin {
			rI := models.ResolveRound(active[i].Round, active[i].Rank)
			rJ := models.ResolveRound(active[j].Round, active[j].Rank)
			if rI != rJ {
				return rI < rJ
			}
		}
		if active[i].Rank != active[j].Rank {
			return active[i].Rank < active[j].Rank
		}
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})
}

func extractActiveQueueAndTarget(targetID string, allReqs []*models.SongRequest) (*models.SongRequest, []*models.SongRequest) {
	var target *models.SongRequest
	var active []*models.SongRequest
	for _, r := range allReqs {
		if r.ID == targetID {
			cp := *r
			target = &cp
		}
		if isActiveRequestStatus(r.Status) {
			cp := *r
			active = append(active, &cp)
		}
	}
	return target, active
}

func indexOfRequest(list []*models.SongRequest, id string) int {
	for i, r := range list {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// PlanDeferRequest computes the new placement for an absent singer bumped down
// by positions spots (defaulting to DefaultDeferPositions) in the active queue.
// If the singer was called (up_next), they return to accepted status without
// recording a completed performance.
func PlanDeferRequest(event *models.Event, targetID string, allReqs []*models.SongRequest, positions int) (DeferPlan, error) {
	if positions <= 0 {
		positions = DefaultDeferPositions
	}
	target, active := extractActiveQueueAndTarget(targetID, allReqs)
	if target == nil {
		return DeferPlan{}, ErrRequestNotFound
	}
	if !isActiveRequestStatus(target.Status) {
		return DeferPlan{}, ErrRequestNotDeferrable
	}

	sortActiveRequests(event, active)
	idx := indexOfRequest(active, targetID)
	if idx < 0 {
		return DeferPlan{}, ErrRequestNotDeferrable
	}

	dest := min(idx+positions, len(active)-1)
	statusChanged := target.Status == models.StatusUpNext
	if statusChanged {
		target.Status = models.StatusAccepted
		target.CompletedAt = nil
	}
	if dest == idx {
		return DeferPlan{Target: target, Changed: statusChanged}, nil
	}

	reordered := make([]*models.SongRequest, 0, len(active))
	reordered = append(reordered, active[:idx]...)
	reordered = append(reordered, active[idx+1:dest+1]...)
	reordered = append(reordered, target)
	reordered = append(reordered, active[dest+1:]...)

	if event != nil && event.RotationMode == models.RotationModeRoundRobin && dest > 0 {
		prevRound := models.ResolveRound(reordered[dest-1].Round, reordered[dest-1].Rank)
		if prevRound > models.ResolveRound(target.Round, target.Rank) {
			target.Round = prevRound
		}
	}

	reorderIDs := make([]string, len(reordered))
	for i, r := range reordered {
		reorderIDs[i] = r.ID
	}
	return DeferPlan{Target: target, ReorderIDs: reorderIDs, Changed: true}, nil
}

// DeferEventRequestCmd encapsulates host inputs for deferring an absent singer.
type DeferEventRequestCmd struct {
	EventID        string
	RequestID      string
	Positions      int
	ExpectedVer    *int64
	IdempotencyKey string
	HostOwnerID    string
}

func (s *Service) deferEventRequestOnce(ctx context.Context, cmd DeferEventRequestCmd, positions int, route, reqHash string) (*models.SongRequest, *store.QueueMutationResult, error) {
	event, err := s.Events.GetEvent(ctx, cmd.EventID)
	if errors.Is(err, os.ErrNotExist) || (err == nil && event == nil) {
		return nil, nil, ErrEventNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	allReqs, err := s.Requests.ListRequestsByEvent(ctx, cmd.EventID)
	if err != nil {
		return nil, nil, err
	}

	plan, err := PlanDeferRequest(event, cmd.RequestID, allReqs, positions)
	if err != nil {
		return nil, nil, err
	}
	if len(plan.ReorderIDs) > 99 {
		return nil, nil, fmt.Errorf("defer reorder exceeds atomic limit of 99: %w", os.ErrInvalid)
	}

	plan.Target.UpdatedAt = time.Now().UTC()
	commitVer := event.QueueVersion
	if cmd.ExpectedVer != nil {
		commitVer = *cmd.ExpectedVer
	}

	if cmd.IdempotencyKey != "" {
		result, err := s.Requests.UpdateRequestAndReorderWithIdempotency(ctx, plan.Target, plan.ReorderIDs, &commitVer, cmd.IdempotencyKey, cmd.HostOwnerID, route, reqHash)
		return plan.Target, result, err
	}
	if len(plan.ReorderIDs) > 0 || plan.Changed || cmd.ExpectedVer != nil {
		err := s.Requests.UpdateRequestAndReorderWithQueueVersion(ctx, plan.Target, plan.ReorderIDs, commitVer)
		return plan.Target, nil, err
	}
	return plan.Target, nil, nil
}

// DeferEventRequest defers an absent singer down the active queue without
// incrementing completed performance history, committing atomically with
// queue_version and optional idempotency replay protection.
func (s *Service) DeferEventRequest(ctx context.Context, cmd DeferEventRequestCmd) (*models.SongRequest, *store.QueueMutationResult, error) {
	if s.Events == nil || s.Requests == nil {
		return nil, nil, ErrEventNotFound
	}
	positions := cmd.Positions
	if positions <= 0 {
		positions = DefaultDeferPositions
	}

	route := fmt.Sprintf("events/%s/requests/defer", cmd.EventID)
	reqHash := mutationHash(struct {
		RequestID string `json:"request_id"`
		Positions int    `json:"positions"`
	}{RequestID: cmd.RequestID, Positions: positions})

	if cmd.IdempotencyKey != "" {
		if cmd.HostOwnerID == "" {
			return nil, nil, ErrUnauthorized
		}
		result, found, replayErr := s.Requests.GetIdempotencyResult(ctx, cmd.IdempotencyKey, cmd.HostOwnerID, route, cmd.RequestID, reqHash)
		if replayErr != nil || found {
			return nil, result, replayErr
		}
	}

	const maxDeferAttempts = 3
	for attempt := 0; attempt < maxDeferAttempts; attempt++ {
		target, result, commitErr := s.deferEventRequestOnce(ctx, cmd, positions, route, reqHash)
		if commitErr == nil {
			return target, result, nil
		}
		var versionConflict *store.QueueVersionConflictError
		if cmd.ExpectedVer == nil && (errors.As(commitErr, &versionConflict) || errors.Is(commitErr, store.ErrAdmissionConflict)) {
			continue
		}
		return nil, nil, commitErr
	}
	return nil, nil, store.ErrAdmissionConflict
}
