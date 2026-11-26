package catalog

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"micup.live/server/internal/models"
)

// triggerCompaction enqueues a best-effort compaction build job for a songbook.
func (s *Service) triggerCompaction(ctx context.Context, songbookID string) {
	if s.CatalogJobs == nil {
		return
	}
	if err := s.CatalogJobs.Enqueue(ctx, BuildJob{
		SongbookID: songbookID,
		Compaction: true,
	}); err != nil {
		log.Printf("catalog: failed to enqueue compaction job for songbook %s: %v", songbookID, err)
	}
}

// overlayManifestFallback returns the manifest status when no static version is ready.
// If active catalog change overrides exist, it signals overlay readiness for singer and kiosk clients.
func (s *Service) overlayManifestFallback(ctx context.Context, songbookID string) (map[string]any, error) {
	if s.CatalogChanges != nil {
		changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
		if err != nil {
			return nil, err
		}
		active := models.ApplyCatalogChanges(nil, changes)
		if len(active) > 0 {
			return map[string]any{
				"static_ready":  false,
				"overlay_ready": true,
				"song_count":    len(active),
			}, nil
		}
	}
	return map[string]any{
		"static_ready":  false,
		"overlay_ready": false,
	}, nil
}

// resolvePublicManifest builds public manifest metadata, falling back to overlay-ready status when not yet compiled.
func (s *Service) resolvePublicManifest(ctx context.Context, songbook *models.Songbook) (map[string]any, error) {
	if s.Catalogs == nil || songbook.CatalogVersionID == "" {
		return s.overlayManifestFallback(ctx, songbook.ID)
	}
	version, err := s.Catalogs.GetCatalogVersion(ctx, songbook.CatalogVersionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.overlayManifestFallback(ctx, songbook.ID)
		}
		return nil, err
	}
	if version == nil || version.Status != models.CatalogVersionReady {
		return s.overlayManifestFallback(ctx, songbook.ID)
	}
	return map[string]any{
		"static_ready": true,
		"name":         songbook.Name,
		"catalog_id":   version.ID,
		"total":        version.SongCount,
		"manifest_url": "/" + strings.Trim(version.AssetPrefix, "/") + "/manifest.json",
	}, nil
}
