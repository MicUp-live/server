package queue

import (
	"context"
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

type validatedSubmitInput struct {
	singerName string
	songTitle  string
	songArtist string
	keyChange  int
	notes      string
	songID     string
	isWriteIn  bool
}

type idempotencyMeta struct {
	route   string
	reqHash string
}

func (s *Service) loadAdmissibleEvent(ctx context.Context, eventID string) (*models.Event, error) {
	if s.Events == nil {
		return nil, ErrEventNotFound
	}
	event, err := s.Events.GetEvent(ctx, eventID)
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
	return event, nil
}

func validateSubmitInput(cmd SubmitRequestCmd, event *models.Event) (validatedSubmitInput, error) {
	if cmd.IsWriteIn && !event.AllowWriteIns {
		return validatedSubmitInput{}, ErrWriteInsDisabled
	}

	singerName := truncateUTF8(strings.TrimSpace(cmd.SingerName), 60)
	if singerName == "" {
		return validatedSubmitInput{}, fmt.Errorf("singer name is required: %w", os.ErrInvalid)
	}

	songTitle := truncateUTF8(strings.TrimSpace(cmd.SongTitle), 150)
	if songTitle == "" {
		return validatedSubmitInput{}, fmt.Errorf("song title is required: %w", os.ErrInvalid)
	}

	songID := strings.TrimSpace(cmd.SongID)
	if cmd.IsWriteIn {
		songID = ""
	}

	return validatedSubmitInput{
		singerName: singerName,
		songTitle:  songTitle,
		songArtist: truncateUTF8(strings.TrimSpace(cmd.SongArtist), 150),
		keyChange:  clampKeyChange(cmd.KeyChange, event.AllowKeyChange),
		notes:      truncateUTF8(strings.TrimSpace(cmd.Notes), 300),
		songID:     songID,
		isWriteIn:  cmd.IsWriteIn,
	}, nil
}

func clampKeyChange(keyChange int, allowKeyChange bool) int {
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

func isActiveQueueStatus(status models.RequestStatus) bool {
	return status == models.StatusPending || status == models.StatusAccepted || status == models.StatusUpNext
}

func isInactiveQueueStatus(status models.RequestStatus) bool {
	return status == models.StatusPending || status == models.StatusRejected || status == models.StatusCancelled
}

func isEnteringActiveQueueStatus(oldStatus, newStatus models.RequestStatus) bool {
	return isInactiveQueueStatus(oldStatus) && (newStatus == models.StatusAccepted || newStatus == models.StatusUpNext)
}

func checkSubmissionDelay(existingReqs []*models.SongRequest, normSinger string, submissionDelay int) error {
	if submissionDelay <= 0 {
		return nil
	}
	delay := time.Duration(submissionDelay) * time.Second
	for _, er := range existingReqs {
		if strings.ToLower(strings.TrimSpace(er.SingerName)) != normSinger || !isActiveQueueStatus(er.Status) {
			continue
		}
		elapsed := time.Since(er.CreatedAt)
		if elapsed < delay {
			remaining := int((delay - elapsed).Seconds()) + 1
			return &ErrCooldownActive{RemainingSeconds: remaining}
		}
	}
	return nil
}

func countSingerRequests(existingReqs []*models.SongRequest, normSinger string) (completedCount, activeAhead int) {
	for _, er := range existingReqs {
		if strings.ToLower(strings.TrimSpace(er.SingerName)) != normSinger {
			continue
		}
		if er.Status == models.StatusCompleted {
			completedCount++
		} else if isActiveQueueStatus(er.Status) {
			activeAhead++
		}
	}
	return completedCount, activeAhead
}

func resolveInitialStatus(event *models.Event) models.RequestStatus {
	if event.RotationMode != models.RotationModeManual && event.AutoAccept {
		return models.StatusAccepted
	}
	return models.StatusPending
}

func (s *Service) buildSubmittedRequest(
	event *models.Event,
	input validatedSubmitInput,
	existingReqs []*models.SongRequest,
	completedCount, activeAhead int,
) (*models.SongRequest, []string) {
	initialStatus := resolveInitialStatus(event)
	now := time.Now().UTC()
	nowMilli := now.UnixMilli()
	seq := int64(len(existingReqs) + 1)
	singerTurn := completedCount + activeAhead + 1

	songReq := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    event.ID,
		SongbookID: event.SongbookID,
		SongID:     input.songID,
		IsWriteIn:  input.isWriteIn,
		SongTitle:  input.songTitle,
		SongArtist: input.songArtist,
		SingerName: input.singerName,
		KeyChange:  input.keyChange,
		Notes:      input.notes,
		Status:     initialStatus,
		SingerTurn: singerTurn,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	songReq.CancelToken = GenerateCancelToken(s.Secret, songReq.ID, songReq.EventID)
	songReq.EditToken = GenerateEditToken(s.Secret, songReq.ID, songReq.EventID)

	var reorderIDs []string
	if initialStatus == models.StatusAccepted {
		placement := PlaceAcceptedRequest(event, songReq, existingReqs, now)
		reorderIDs = placement.ReorderIDs
	} else {
		songReq.Rank = nowMilli + seq
		songReq.Round = 0
	}

	return songReq, reorderIDs
}

func (s *Service) mapCreateRequestError(ctx context.Context, eventID string, err error) error {
	if !errors.Is(err, store.ErrEventNotActive) {
		return err
	}
	current, readErr := s.Events.GetEvent(ctx, eventID)
	if readErr != nil {
		return fmt.Errorf("reload event after admission conflict: %w", readErr)
	}
	if current == nil {
		return ErrEventNotFound
	}
	switch current.Status {
	case models.EventStatusPaused:
		return ErrShowPaused
	case models.EventStatusEnded:
		return ErrShowEnded
	}
	return err
}

func (s *Service) checkUpdateIdempotency(ctx context.Context, cmd UpdateEventRequestCmd, meta idempotencyMeta) (*store.QueueMutationResult, bool, error) {
	if cmd.IdempotencyKey == "" {
		return nil, false, nil
	}
	if cmd.HostOwnerID == "" {
		return nil, false, ErrUnauthorized
	}
	return s.Requests.GetIdempotencyResult(ctx, cmd.IdempotencyKey, cmd.HostOwnerID, meta.route, cmd.RequestID, meta.reqHash)
}

func (s *Service) planRequestPlacement(ctx context.Context, cmd UpdateEventRequestCmd, req *models.SongRequest, enteringActive bool) ([]string, error) {
	if enteringActive && req.Status == models.StatusAccepted && cmd.Update.Rank == nil && cmd.Update.Round == nil {
		return s.placeAcceptedOnUpdate(ctx, cmd.EventID, req)
	}
	if enteringActive && req.Status == models.StatusUpNext && cmd.Update.Rank == nil {
		return s.placeUpNextOnUpdate(ctx, cmd.EventID, req)
	}
	applyExplicitRankAndRound(req, cmd.Update)
	return nil, nil
}

func (s *Service) placeAcceptedOnUpdate(ctx context.Context, eventID string, req *models.SongRequest) ([]string, error) {
	event, err := s.Events.GetEvent(ctx, eventID)
	if err != nil || event == nil {
		return nil, ErrEventNotFound
	}
	allReqs, err := s.Requests.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	placement := PlaceAcceptedRequest(event, req, allReqs, time.Now())
	return placement.ReorderIDs, nil
}

func (s *Service) placeUpNextOnUpdate(ctx context.Context, eventID string, req *models.SongRequest) ([]string, error) {
	allReqs, err := s.Requests.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	activeQueue := filterAndSortActiveQueue(allReqs, req.ID)
	req.Round = 0
	if len(activeQueue) == 0 {
		req.Rank = 1
		return nil, nil
	}
	if len(activeQueue) == 1 {
		req.Rank = activeQueue[0].Rank + 1
		return nil, nil
	}
	prevRank := activeQueue[0].Rank
	nextRank := activeQueue[1].Rank
	if nextRank-prevRank > 1 {
		req.Rank = prevRank + (nextRank-prevRank)/2
		return nil, nil
	}
	req.Rank = prevRank + 1
	reorderIDs := make([]string, 0, len(activeQueue)+1)
	reorderIDs = append(reorderIDs, activeQueue[0].ID, req.ID)
	for _, ar := range activeQueue[1:] {
		reorderIDs = append(reorderIDs, ar.ID)
	}
	return reorderIDs, nil
}

func filterAndSortActiveQueue(allReqs []*models.SongRequest, excludeID string) []*models.SongRequest {
	var activeQueue []*models.SongRequest
	for _, er := range allReqs {
		if er.ID != excludeID && (er.Status == models.StatusAccepted || er.Status == models.StatusUpNext) {
			activeQueue = append(activeQueue, er)
		}
	}
	sort.SliceStable(activeQueue, func(i, j int) bool {
		if activeQueue[i].Rank != activeQueue[j].Rank {
			return activeQueue[i].Rank < activeQueue[j].Rank
		}
		return activeQueue[i].CreatedAt.Before(activeQueue[j].CreatedAt)
	})
	return activeQueue
}

func applyExplicitRankAndRound(req *models.SongRequest, update models.RequestUpdate) {
	if update.Rank != nil {
		req.Rank = *update.Rank
	}
	if update.Round == nil || *update.Round <= 0 {
		return
	}
	req.Round = *update.Round
	if update.Rank != nil {
		return
	}
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

func applyRequestFieldUpdates(req *models.SongRequest, update models.RequestUpdate, oldStatus models.RequestStatus) {
	if update.SongID != nil {
		req.SongID = strings.TrimSpace(*update.SongID)
	}
	if update.SongTitle != nil && strings.TrimSpace(*update.SongTitle) != "" {
		req.SongTitle = strings.TrimSpace(*update.SongTitle)
	}
	if update.SongArtist != nil && strings.TrimSpace(*update.SongArtist) != "" {
		req.SongArtist = strings.TrimSpace(*update.SongArtist)
	}
	if update.SingerName != nil && strings.TrimSpace(*update.SingerName) != "" {
		req.SingerName = strings.TrimSpace(*update.SingerName)
	}
	if update.KeyChange != nil {
		req.KeyChange = *update.KeyChange
	}
	if update.Notes != nil {
		req.Notes = *update.Notes
	}
	if update.DeclineReason != nil {
		req.DeclineReason = *update.DeclineReason
	}
	now := time.Now().UTC()
	syncCompletedAt(req, oldStatus, now)
	req.UpdatedAt = now
}

func reqOrNil(req *models.SongRequest, err error) *models.SongRequest {
	if err != nil {
		return nil
	}
	return req
}

func (s *Service) persistUpdatedRequest(
	ctx context.Context,
	cmd UpdateEventRequestCmd,
	req *models.SongRequest,
	reorderIDs []string,
	enteringActive bool,
	meta idempotencyMeta,
) (*models.SongRequest, *store.QueueMutationResult, error) {
	if cmd.IdempotencyKey != "" {
		result, err := s.Requests.UpdateRequestAndReorderWithIdempotency(ctx, req, reorderIDs, cmd.ExpectedVer, cmd.IdempotencyKey, cmd.HostOwnerID, meta.route, meta.reqHash)
		if err != nil {
			return nil, nil, err
		}
		return req, result, nil
	}
	if cmd.ExpectedVer != nil {
		err := s.Requests.UpdateRequestAndReorderWithQueueVersion(ctx, req, reorderIDs, *cmd.ExpectedVer)
		return reqOrNil(req, err), nil, err
	}
	if enteringActive || len(reorderIDs) > 0 {
		err := s.Requests.UpdateRequestAndReorder(ctx, req, reorderIDs)
		return reqOrNil(req, err), nil, err
	}
	err := s.Requests.UpdateRequest(ctx, req)
	return reqOrNil(req, err), nil, err
}
