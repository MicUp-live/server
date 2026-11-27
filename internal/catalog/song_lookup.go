package catalog

import (
	"context"
	"errors"
	"os"

	"micup.live/server/internal/models"
)

// ResolveCatalogSong resolves a song by ID from pending catalog overrides or the active
// static catalog version (with private host-only locations).
func (s *Service) ResolveCatalogSong(ctx context.Context, songbookID, songID string) (*models.Song, error) {
	song, resolved, err := s.resolvePendingCatalogSong(ctx, songbookID, songID)
	if err != nil {
		return nil, err
	}
	if resolved {
		if song == nil {
			return nil, os.ErrNotExist
		}
		return song, nil
	}

	songs, err := s.listStaticCatalogSongs(ctx, songbookID)
	if err != nil {
		return nil, err
	}
	for _, song := range songs {
		if song.ID == songID {
			return song, nil
		}
	}
	return nil, os.ErrNotExist
}

func (s *Service) resolvePendingCatalogSong(ctx context.Context, songbookID, songID string) (*models.Song, bool, error) {
	if s.CatalogChanges == nil {
		return nil, false, nil
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
	if err != nil {
		return nil, false, err
	}
	song, resolved := models.ResolveCatalogChange(changes, songID)
	return song, resolved, nil
}

func (s *Service) listStaticCatalogSongs(ctx context.Context, songbookID string) ([]*models.Song, error) {
	if s.CatalogReader == nil || s.Catalogs == nil || s.Songbooks == nil {
		return nil, nil
	}
	book, err := s.Songbooks.GetSongbook(ctx, songbookID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if book.CatalogVersionID == "" {
		return nil, nil
	}
	version, err := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
	if err != nil {
		return nil, err
	}
	return s.CatalogReader.ListSongs(ctx, version.AssetPrefix, songbookID)
}

// GetSong returns a single song within a songbook, including host-only fields such as location.
func (s *Service) GetSong(ctx context.Context, songbookID, songID string) (*models.Song, error) {
	song, err := s.ResolveCatalogSong(ctx, songbookID, songID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrSongNotFound) {
			return nil, ErrSongNotFound
		}
		return nil, err
	}
	if song == nil || song.SongbookID != songbookID {
		return nil, ErrSongNotFound
	}
	return song, nil
}

// resolveBatchCatalogSongs resolves a set of song IDs within a songbook in
// deduplicated input order while loading pending overrides and the active
// static catalog at most once.
func (s *Service) resolveBatchCatalogSongs(ctx context.Context, songbookID string, songIDs []string) ([]*models.Song, error) {
	uniqueIDs := deduplicateSongIDs(songIDs)
	if len(uniqueIDs) == 0 {
		return nil, nil
	}

	resolved, unresolved, err := s.resolvePendingBatch(ctx, songbookID, uniqueIDs)
	if err != nil {
		return nil, err
	}

	if err := s.resolveStaticBatch(ctx, songbookID, unresolved, resolved); err != nil {
		return nil, err
	}

	return collectResolvedSongs(uniqueIDs, resolved), nil
}

func deduplicateSongIDs(songIDs []string) []string {
	uniqueIDs := make([]string, 0, len(songIDs))
	seen := make(map[string]struct{}, len(songIDs))
	for _, id := range songIDs {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	return uniqueIDs
}

func (s *Service) resolvePendingBatch(ctx context.Context, songbookID string, songIDs []string) (map[string]*models.Song, []string, error) {
	resolved := make(map[string]*models.Song, len(songIDs))
	if s.CatalogChanges == nil {
		return resolved, songIDs, nil
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
	if err != nil {
		return nil, nil, err
	}
	unresolved := make([]string, 0, len(songIDs))
	for _, id := range songIDs {
		song, ok := models.ResolveCatalogChange(changes, id)
		if !ok {
			unresolved = append(unresolved, id)
			continue
		}
		if song != nil && song.SongbookID == songbookID {
			resolved[id] = song
		}
	}
	return resolved, unresolved, nil
}

func (s *Service) resolveStaticBatch(ctx context.Context, songbookID string, unresolved []string, resolved map[string]*models.Song) error {
	if len(unresolved) == 0 {
		return nil
	}
	catalogSongs, err := s.listStaticCatalogSongs(ctx, songbookID)
	if err != nil {
		return err
	}
	byID := make(map[string]*models.Song, len(catalogSongs))
	for _, song := range catalogSongs {
		if song != nil && song.SongbookID == songbookID {
			byID[song.ID] = song
		}
	}
	for _, id := range unresolved {
		if song, ok := byID[id]; ok {
			resolved[id] = song
		}
	}
	return nil
}

func collectResolvedSongs(uniqueIDs []string, resolved map[string]*models.Song) []*models.Song {
	ordered := make([]*models.Song, 0, len(resolved))
	for _, id := range uniqueIDs {
		if song, ok := resolved[id]; ok {
			ordered = append(ordered, song)
		}
	}
	return ordered
}
