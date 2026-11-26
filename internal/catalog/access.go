package catalog

import (
	"context"
	"errors"
	"os"
	"strings"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// WithAccess attaches the songbook access store to the catalog service.
func (s *Service) WithAccess(access store.SongbookAccessStore) *Service {
	s.Access = access
	return s
}

func (s *Service) canEditCatalog(ctx context.Context, actorID string, book *models.Songbook) (bool, error) {
	if book == nil || actorID == "" || book.Deleting {
		return false, nil
	}
	if book.OwnerID == actorID {
		return true, nil
	}
	if s.Access == nil {
		return false, nil
	}
	grant, err := s.Access.GetSongbookGrant(ctx, book.ID, actorID)
	if err != nil {
		if errors.Is(err, store.ErrGrantNotFound) || errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return grant != nil && grant.CanEditCatalog, nil
}

// GetManagedManifest returns static catalog metadata for an owned or edit-granted songbook.
func (s *Service) GetManagedManifest(ctx context.Context, songbookID, ownerID string) (map[string]any, error) {
	if s.Songbooks == nil {
		return nil, ErrSongbookNotFound
	}
	book, err := s.Songbooks.GetSongbook(ctx, songbookID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSongbookNotFound
		}
		return nil, err
	}
	allowed, err := s.canEditCatalog(ctx, ownerID, book)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrSongbookNotFound
	}
	if s.Catalogs == nil || book.CatalogVersionID == "" {
		return s.overlayManifestFallback(ctx, book.ID)
	}
	version, err := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.overlayManifestFallback(ctx, book.ID)
		}
		return nil, err
	}
	if version == nil || version.Status != models.CatalogVersionReady {
		return s.overlayManifestFallback(ctx, book.ID)
	}
	return map[string]any{
		"static_ready": true,
		"total":        version.SongCount,
		"manifest_url": "/" + strings.Trim(version.AssetPrefix, "/") + "/manifest.json",
	}, nil
}
