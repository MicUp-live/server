package event

import (
	"context"
	"errors"
	"os"
	"strings"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// ErrSongbookDetached indicates that an ended show's original songbook was deleted
// and the host must select an authorized replacement songbook before restarting.
var ErrSongbookDetached = errors.New("this show's songbook was removed; select a replacement songbook before restarting")

// WithAccess attaches the songbook collaboration access store to the event service.
func (s *Service) WithAccess(access store.SongbookAccessStore) *Service {
	s.Access = access
	return s
}

func (s *Service) authorizeHosting(ctx context.Context, actorID, songbookID string) (*models.Songbook, context.Context, error) {
	if s.Songbooks == nil || strings.TrimSpace(actorID) == "" || strings.TrimSpace(songbookID) == "" {
		return nil, ctx, ErrSongbookNotFound
	}
	sb, err := s.Songbooks.GetSongbook(ctx, songbookID)
	if err != nil || sb == nil {
		return nil, ctx, ErrSongbookNotFound
	}
	if sb.Deleting {
		return nil, ctx, store.ErrSongbookDeleting
	}
	if sb.OwnerID == actorID {
		fenced := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
			SongbookID: sb.ID,
			ActorID:    actorID,
			IsOwner:    true,
		})
		return sb, fenced, nil
	}
	if s.Access == nil {
		return nil, ctx, ErrSongbookNotFound
	}
	grant, gErr := s.Access.GetSongbookGrant(ctx, sb.ID, actorID)
	if gErr != nil {
		if errors.Is(gErr, store.ErrGrantNotFound) || errors.Is(gErr, os.ErrNotExist) {
			return nil, ctx, ErrSongbookNotFound
		}
		return nil, ctx, gErr
	}
	if !grant.CanHostShows {
		return nil, ctx, store.ErrPermissionDenied
	}
	fenced := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
		SongbookID: sb.ID,
		ActorID:    actorID,
		IsOwner:    false,
	})
	return sb, fenced, nil
}

func (s *Service) detachedPublicSongbook(ev *models.Event) *models.Songbook {
	return &models.Songbook{
		Name: ev.DetachedSongbookName,
	}
}

func (s *Service) prepareUpdateHostingFence(ctx context.Context, event *models.Event, update models.EventUpdate) (context.Context, error) {
	needHostAuth := false
	targetBookID := event.SongbookID
	if update.SongbookID != nil {
		trimmed := strings.TrimSpace(*update.SongbookID)
		if trimmed == "" {
			return ctx, os.ErrInvalid
		}
		if trimmed != event.SongbookID {
			needHostAuth = true
			targetBookID = trimmed
		}
	}
	if update.Status != nil && event.Status == models.EventStatusEnded &&
		(*update.Status == models.EventStatusActive || *update.Status == models.EventStatusPaused) {
		needHostAuth = true
	}
	if !needHostAuth {
		return ctx, nil
	}
	if targetBookID == "" {
		if s.Songbooks != nil || event.DetachedSongbookName != "" {
			return ctx, ErrSongbookDetached
		}
		return ctx, nil
	}
	_, fencedCtx, err := s.authorizeHosting(ctx, event.OwnerID, targetBookID)
	if err != nil {
		return ctx, err
	}
	if update.SongbookID != nil && targetBookID != event.SongbookID {
		event.SongbookID = targetBookID
		event.DetachedSongbookName = ""
	}
	return fencedCtx, nil
}

func (s *Service) prepareRestartHostingFence(ctx context.Context, event *models.Event) (context.Context, error) {
	if strings.TrimSpace(event.SongbookID) == "" {
		if s.Songbooks != nil || event.DetachedSongbookName != "" {
			return ctx, ErrSongbookDetached
		}
		return ctx, nil
	}
	if event.Status == models.EventStatusEnded && s.Songbooks != nil {
		_, fencedCtx, err := s.authorizeHosting(ctx, event.OwnerID, event.SongbookID)
		if err != nil {
			return ctx, err
		}
		return fencedCtx, nil
	}
	return ctx, nil
}
