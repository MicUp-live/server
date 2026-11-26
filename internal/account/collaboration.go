package account

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

func mergeCatalogVersionIDs(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	var out []string
	for _, list := range [][]string{existing, incoming} {
		for _, raw := range list {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// WithAccess attaches the songbook collaboration access store to the account service.
func (s *Service) WithAccess(access store.SongbookAccessStore) *Service {
	s.Access = access
	return s
}

func (s *Service) cleanupCollaboratorGrants(ctx context.Context, userID string) error {
	if s.Access == nil {
		return nil
	}
	grants, err := s.Access.ListSongbookGrantsByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list collaborator grants: %w", err)
	}
	for _, g := range grants {
		if err := s.Access.DeleteSongbookGrant(ctx, g.SongbookID, userID); err != nil && !errors.Is(err, store.ErrGrantNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete collaborator grant for songbook %s: %w", g.SongbookID, err)
		}
	}
	return nil
}

func (s *Service) cleanupOwnedSongbookCollaboration(ctx context.Context, book *models.Songbook) error {
	if book == nil {
		return nil
	}
	retention := s.QueueRetention
	if retention <= 0 {
		retention = 14 * 24 * time.Hour
	}
	if s.Events != nil {
		events, err := s.Events.ListEventsBySongbook(ctx, book.ID)
		if err != nil {
			return fmt.Errorf("list events for songbook %s: %w", book.ID, err)
		}
		for _, ev := range events {
			if ev.OwnerID == book.OwnerID {
				continue
			}
			if _, err := s.Events.DetachEventFromSongbook(ctx, ev.ID, book.ID, book.Name, retention); err != nil {
				return fmt.Errorf("detach foreign event %s from songbook %s: %w", ev.ID, book.ID, err)
			}
		}
	}
	if s.Access != nil {
		grants, err := s.Access.ListSongbookGrantsBySongbook(ctx, book.ID)
		if err != nil {
			return fmt.Errorf("list grants for songbook %s: %w", book.ID, err)
		}
		for _, g := range grants {
			if err := s.Access.DeleteSongbookGrant(ctx, book.ID, g.UserID); err != nil && !errors.Is(err, store.ErrGrantNotFound) && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete grant %s for songbook %s: %w", g.UserID, book.ID, err)
			}
		}
		invitations, err := s.Access.ListSongbookInvitationsBySongbook(ctx, book.ID)
		if err != nil {
			return fmt.Errorf("list invitations for songbook %s: %w", book.ID, err)
		}
		for _, inv := range invitations {
			if err := s.Access.DeleteSongbookInvitation(ctx, inv.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete invitation %s for songbook %s: %w", inv.ID, book.ID, err)
			}
		}
	}
	return nil
}
