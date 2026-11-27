// Package event provides centralized application services for event lifecycle management.
package event

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/store"
)

var (
	ErrEventNotFound      = errors.New("event not found")
	ErrSongbookNotFound   = errors.New("songbook not found")
	ErrLinkedBookNotFound = errors.New("linked songbook not found")
	ErrInvalidStatus      = errors.New("invalid status")
	ErrFailedTransition   = errors.New("failed to transition queue rotation mode")
	ErrShowEnded          = errors.New("this show has ended")
)

// PINVerificationResult represents the outcome of verifying a kiosk PIN.
type PINVerificationResult int

const (
	PINResultSuccess PINVerificationResult = iota
	PINResultNotConfigured
	PINResultInvalid
)

// PublicSongbookInfo represents public metadata for a songbook join code match.
type PublicSongbookInfo struct {
	ID         string `json:"songbook_id"`
	PublicID   string `json:"public_songbook_id"`
	Slug       string `json:"slug"`
	Name       string `json:"songbook_name"`
	SongCount  int    `json:"song_count"`
	TargetPath string `json:"target_path"`
}

// CodeResolutionResult holds the target resolved by a join code.
type CodeResolutionResult struct {
	Type     string
	Event    *models.PublicEvent
	Songbook *PublicSongbookInfo
}

// Service manages event creation, settings updates, lifecycle transitions, and queue retention.
type Service struct {
	Events         store.EventStore
	Songbooks      store.SongbookStore
	Requests       store.RequestStore
	Access         store.SongbookAccessStore
	QueueRetention time.Duration
}

// NewService constructs an event lifecycle service.
func NewService(events store.EventStore, songbooks store.SongbookStore, requests store.RequestStore, retention time.Duration) *Service {
	return &Service{
		Events:         events,
		Songbooks:      songbooks,
		Requests:       requests,
		QueueRetention: retention,
	}
}

// QueueRetentionDuration returns configured retention duration, defaulting to 14 days.
func (s *Service) QueueRetentionDuration() time.Duration {
	if s.QueueRetention <= 0 {
		return 14 * 24 * time.Hour
	}
	return s.QueueRetention
}

// CreateEvent validates the songbook and starts a new event session.
func (s *Service) CreateEvent(ctx context.Context, ownerID string, req models.EventCreate) (*models.Event, error) {
	if s.Songbooks == nil || s.Events == nil {
		return nil, ErrSongbookNotFound
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Karaoke Night"
	}

	_, fencedCtx, err := s.authorizeHosting(ctx, ownerID, req.SongbookID)
	if err != nil {
		return nil, err
	}

	joinCode := strings.ToUpper(strings.TrimSpace(req.JoinCode))
	if joinCode == "" {
		joinCode = GenerateJoinCode()
	}

	pin := strings.TrimSpace(req.KioskPIN)
	if pin == "" {
		pin = GenerateKioskPIN()
	}

	rotationMode := normalizeRotationMode(req.RotationMode)
	autoAccept := boolOrDefault(req.AutoAccept, false)
	submissionDelay := clampSubmissionDelay(req.SubmissionDelay)
	maxActive := clampMaxActivePerSinger(req.MaxActivePerSinger)
	allowKeyChange := boolOrDefault(req.AllowKeyChange, false)
	allowWriteIns := boolOrDefault(req.AllowWriteIns, false)
	allowSongReplacement := boolOrDefault(req.AllowSongReplacement, true)

	eventID := uuid.NewString()
	event := &models.Event{
		ID:                   eventID,
		OwnerID:              ownerID,
		SongbookID:           req.SongbookID,
		Name:                 name,
		VenueName:            strings.TrimSpace(req.VenueName),
		JoinCode:             joinCode,
		Status:               models.EventStatusActive,
		QueueVersion:         1,
		AllowKeyChange:       allowKeyChange,
		AllowWriteIns:        allowWriteIns,
		AllowSongReplacement: allowSongReplacement,
		RotationMode:         rotationMode,
		CurrentRound:         1,
		TipURL:               strings.TrimSpace(req.TipURL),
		Announcement:         strings.TrimSpace(req.Announcement),
		SubmissionDelay:      submissionDelay,
		MaxActivePerSinger:   maxActive,
		AutoAccept:           autoAccept,
		Recurring:            req.Recurring,
		KioskPIN:             pin,
		OpenKJKey:            models.GenerateOpenKJKey(eventID),
		CreatedAt:            time.Now().UTC(),
	}

	if err := s.Events.CreateEvent(fencedCtx, event); err != nil {
		return nil, err
	}
	return event, nil
}

// ListEvents lists events owned by the host or all events.
func (s *Service) ListEvents(ctx context.Context, ownerID string) ([]*models.Event, error) {
	if s.Events == nil {
		return []*models.Event{}, nil
	}
	if ownerID != "" {
		events, err := s.Events.ListEventsByOwner(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		if events == nil {
			return []*models.Event{}, nil
		}
		return events, nil
	}
	return []*models.Event{}, nil
}

// GetEvent retrieves an event by ID.
func (s *Service) GetEvent(ctx context.Context, id string) (*models.Event, error) {
	if s.Events == nil {
		return nil, ErrEventNotFound
	}
	event, err := s.Events.GetEvent(ctx, id)
	if errors.Is(err, os.ErrNotExist) || (err == nil && event == nil) {
		return nil, ErrEventNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load event: %w", err)
	}
	return event, nil
}

// GetPublicEvent returns a sanitized public view of an event by ID.
func (s *Service) GetPublicEvent(ctx context.Context, id string) (*models.PublicEvent, error) {
	event, err := s.GetEvent(ctx, id)
	if err != nil {
		return nil, ErrEventNotFound
	}
	return s.resolvePublicEvent(ctx, event)
}

// GetEventByCode resolves an event by its short join code (e.g. "OMALLEY") or falls back to public songbook.
func (s *Service) GetEventByCode(ctx context.Context, code string) (*CodeResolutionResult, error) {
	if s.Events == nil {
		return nil, ErrEventNotFound
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, ErrEventNotFound
	}
	if event, err := s.Events.GetEventByJoinCode(ctx, code); err == nil && event != nil {
		pub, pubErr := s.resolvePublicEvent(ctx, event)
		if pubErr != nil {
			return nil, pubErr
		}
		return &CodeResolutionResult{Type: "event", Event: pub}, nil
	}

	// Fall back to public songbook by slug or public ID
	if res := s.lookupPublicSongbookByCode(ctx, code); res != nil {
		return res, nil
	}

	return nil, ErrEventNotFound
}

func (s *Service) buildPublicEvent(event *models.Event, sb *models.Songbook) *models.PublicEvent {
	return &models.PublicEvent{
		ID:                   event.ID,
		SongbookID:           event.SongbookID,
		PublicSongbookID:     sb.PublicID,
		SongbookName:         sb.Name,
		VenueName:            event.VenueName,
		Name:                 event.Name,
		JoinCode:             event.JoinCode,
		Status:               event.Status,
		Recurring:            event.Recurring,
		TipURL:               event.TipURL,
		Announcement:         event.Announcement,
		RotationMode:         event.RotationMode,
		AutoAccept:           event.AutoAccept,
		SubmissionDelay:      event.SubmissionDelay,
		MaxActivePerSinger:   event.MaxActivePerSinger,
		AllowKeyChange:       event.AllowKeyChange,
		AllowWriteIns:        event.AllowWriteIns,
		AllowSongReplacement: event.AllowSongReplacement,
		CurrentRound:         event.CurrentRound,
		SongCount:            sb.SongCount,
		CreatedAt:            event.CreatedAt,
	}
}

// UpdateEvent updates event settings, rotation policy, status, and retention TTL.
func (s *Service) UpdateEvent(ctx context.Context, id string, update models.EventUpdate) (*models.Event, error) {
	event, err := s.GetEvent(ctx, id)
	if err != nil {
		return nil, err
	}

	fencedCtx, err := s.prepareUpdateHostingFence(ctx, event, update)
	if err != nil {
		return nil, err
	}
	if update.SingerWeights != nil {
		if err := models.ValidateSingerWeights(*update.SingerWeights); err != nil {
			return nil, err
		}
	}
	applyEventSettingsUpdate(event, update)

	modeTransitionRequests, err := s.applyRotationModeUpdate(ctx, event, update.RotationMode)
	if err != nil {
		return nil, err
	}
	if err := s.applyStatusUpdate(event, update.Status); err != nil {
		return nil, err
	}
	if err := s.persistUpdatedEvent(fencedCtx, event, modeTransitionRequests); err != nil {
		return nil, err
	}
	if err := s.syncEventRequestsTTL(ctx, event, update.Status); err != nil {
		return nil, err
	}

	return event, nil
}

// PauseEvent pauses singer requests for an event.
func (s *Service) PauseEvent(ctx context.Context, id string) (*models.Event, error) {
	status := models.EventStatusPaused
	return s.UpdateEvent(ctx, id, models.EventUpdate{Status: &status})
}

// ResumeEvent resumes requests for a paused event.
func (s *Service) ResumeEvent(ctx context.Context, id string) (*models.Event, error) {
	status := models.EventStatusActive
	return s.UpdateEvent(ctx, id, models.EventUpdate{Status: &status})
}

// RestartEvent re-activates an ended show and optionally archives/clears previous requests.
func (s *Service) RestartEvent(ctx context.Context, id string, clearQueue bool) (*models.Event, error) {
	if s.Events == nil {
		return nil, ErrEventNotFound
	}
	event, err := s.loadEventForRestart(ctx, id)
	if err != nil {
		return nil, err
	}
	fencedCtx, err := s.prepareRestartHostingFence(ctx, event)
	if err != nil {
		return nil, err
	}
	if clearQueue {
		return s.restartWithClearedQueue(ctx, fencedCtx, id, event)
	}

	event.Status = models.EventStatusActive
	event.EndedAt = nil
	event.TTL = 0
	event.UpdatedAt = time.Now().UTC()

	if err := s.Events.UpdateEventWithVersion(fencedCtx, event, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrEventNotFound
		}
		return nil, err
	}
	if s.Requests != nil {
		if err := s.Requests.SetRequestsTTLByEvent(ctx, event.ID, time.Time{}); err != nil {
			return nil, fmt.Errorf("clear event request retention: %w", err)
		}
	}

	return event, nil
}

// RotateOpenKJKey generates and saves a new OpenKJ key for the event.
func (s *Service) RotateOpenKJKey(ctx context.Context, id string) (*models.Event, error) {
	event, err := s.GetEvent(ctx, id)
	if err != nil {
		return nil, err
	}
	event.OpenKJKey = models.GenerateOpenKJKey(event.ID)
	if err := s.Events.UpdateEventWithVersion(ctx, event, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrEventNotFound
		}
		return nil, err
	}
	return event, nil
}

// VerifyKioskPIN validates the kiosk PIN in constant time.
func (s *Service) VerifyKioskPIN(ctx context.Context, id, rawPIN string) (PINVerificationResult, error) {
	event, err := s.GetEvent(ctx, id)
	if err != nil {
		return PINResultInvalid, ErrEventNotFound
	}
	expectedPIN := strings.TrimSpace(event.KioskPIN)
	if expectedPIN == "" {
		return PINResultNotConfigured, nil
	}

	providedPIN := strings.TrimSpace(rawPIN)
	hExpected := sha256.Sum256([]byte(expectedPIN))
	hProvided := sha256.Sum256([]byte(providedPIN))
	if subtle.ConstantTimeCompare(hExpected[:], hProvided[:]) != 1 {
		return PINResultInvalid, nil
	}
	return PINResultSuccess, nil
}

// DeleteEvent deletes an event and all associated singer requests.
func (s *Service) DeleteEvent(ctx context.Context, id string) error {
	if s.Requests != nil {
		requests, err := s.Requests.ListRequestsByEvent(ctx, id)
		if err != nil {
			return err
		}
		for _, req := range requests {
			if err := s.Requests.DeleteRequest(ctx, req.ID); err != nil {
				return err
			}
		}
	}
	if s.Events != nil {
		return s.Events.DeleteEvent(ctx, id)
	}
	return nil
}

func (s *Service) planQueueRotationTransition(ctx context.Context, event *models.Event, oldMode, newMode string) ([]*models.SongRequest, error) {
	if s.Requests == nil {
		return nil, nil
	}
	requests, err := s.Requests.ListRequestsByEvent(ctx, event.ID)
	if err != nil {
		return nil, err
	}
	return queue.PlanRotationTransition(event, requests, oldMode, newMode)
}
