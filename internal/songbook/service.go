// Package songbook provides application service operations for songbook metadata management.
package songbook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// AssetPurger deletes object storage files and prefixes during songbook cleanup.
type AssetPurger interface {
	DeleteKey(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

var (
	ErrSongbookNotFound = errors.New("songbook not found")
	ErrSlugTaken        = errors.New("slug is already taken")
)

// EventsAssociatedError reports that a songbook cannot be deleted while events reference it.
type EventsAssociatedError struct {
	EventNames []string
}

func (e *EventsAssociatedError) Error() string {
	return fmt.Sprintf("cannot delete songbook: please delete associated event(s) first: %s", strings.Join(e.EventNames, ", "))
}

// EventLister lists and detaches events associated with a songbook.
type EventLister interface {
	ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error)
	DetachEventFromSongbook(ctx context.Context, eventID, songbookID, songbookName string, retention time.Duration) (*models.Event, error)
}

// Service coordinates songbook metadata CRUD, collaboration access, and lifecycle policies.
type Service struct {
	Songbooks      store.SongbookStore
	Events         EventLister
	Access         store.SongbookAccessStore
	Auth           store.AuthStore
	CatalogChanges store.CatalogChangeStore
	ImportJobs     store.ImportJobStore
	Requests       store.RequestStore
	Catalogs       store.CatalogStore
	Assets         AssetPurger
	EmailSender    email.Sender
	QueueRetention time.Duration
	InvitationTTL  time.Duration
	Now            func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// NewService constructs a songbook application service.
func NewService(songbooks store.SongbookStore, events EventLister) *Service {
	return &Service{
		Songbooks: songbooks,
		Events:    events,
	}
}

func (s *Service) resolveSlug(ctx context.Context, rawSlug, excludeID string) (string, error) {
	slug := models.NormalizeSlug(rawSlug)
	if slug == "" {
		return "", nil
	}
	if err := models.ValidateSlug(slug); err != nil {
		return "", fmt.Errorf("%w: %s", os.ErrInvalid, err.Error())
	}
	existing, err := s.Songbooks.GetSongbookBySlug(ctx, slug)
	if err == nil && existing != nil && (excludeID == "" || existing.ID != excludeID) {
		return "", ErrSlugTaken
	}
	return slug, nil
}

// CreateSongbook validates and stores a new songbook.
func (s *Service) CreateSongbook(ctx context.Context, ownerID string, req models.SongbookCreate) (*models.Songbook, error) {
	if s.Songbooks == nil {
		return nil, ErrSongbookNotFound
	}

	slug, err := s.resolveSlug(ctx, req.Slug, "")
	if err != nil {
		return nil, err
	}

	sb := &models.Songbook{
		ID:       uuid.NewString(),
		PublicID: uuid.NewString(),
		Slug:     slug,
		VenueID:  req.VenueID,
		Name:     req.Name,
		IsPublic: req.IsPublic,
		OwnerID:  ownerID,
	}

	if err := s.Songbooks.CreateSongbook(ctx, sb); err != nil {
		return nil, err
	}
	return sb, nil
}

// ListSongbooks returns songbooks for the specified owner or all songbooks.
func (s *Service) ListSongbooks(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	if s.Songbooks == nil {
		return []*models.Songbook{}, nil
	}
	if ownerID != "" {
		return s.Songbooks.ListSongbooksByOwner(ctx, ownerID)
	}
	return s.Songbooks.ListSongbooks(ctx)
}

// GetSongbook retrieves a single songbook by ID.
func (s *Service) GetSongbook(ctx context.Context, id string) (*models.Songbook, error) {
	if s.Songbooks == nil {
		return nil, ErrSongbookNotFound
	}
	sb, err := s.Songbooks.GetSongbook(ctx, id)
	if err != nil {
		return nil, ErrSongbookNotFound
	}
	return sb, nil
}

func validateSongbookUpdate(req models.SongbookUpdate) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return fmt.Errorf("name is required: %w", os.ErrInvalid)
	}
	if req.Name == nil && req.IsPublic == nil && req.Slug == nil {
		return fmt.Errorf("at least one field is required: %w", os.ErrInvalid)
	}
	return nil
}

// UpdateSongbook updates the name, slug, and publication state of an existing songbook.
func (s *Service) UpdateSongbook(ctx context.Context, id string, req models.SongbookUpdate) (*models.Songbook, error) {
	if s.Songbooks == nil {
		return nil, ErrSongbookNotFound
	}
	if err := validateSongbookUpdate(req); err != nil {
		return nil, err
	}

	songbook, err := s.Songbooks.GetSongbook(ctx, id)
	if err != nil || songbook == nil {
		return nil, ErrSongbookNotFound
	}

	if req.Name != nil {
		songbook.Name = strings.TrimSpace(*req.Name)
	}
	if req.IsPublic != nil {
		songbook.IsPublic = *req.IsPublic
	}
	if req.Slug != nil {
		slug, err := s.resolveSlug(ctx, *req.Slug, songbook.ID)
		if err != nil {
			return nil, err
		}
		songbook.Slug = slug
	}

	if err := s.Songbooks.UpdateSongbook(ctx, songbook); err != nil {
		return nil, err
	}
	return songbook, nil
}

// DeleteSongbook removes an owned songbook if no owner events or unconfirmed foreign events are associated with it.
func (s *Service) DeleteSongbook(ctx context.Context, songbookID string) error {
	return s.DeleteSongbookWithConfirmation(ctx, "", songbookID, "")
}
