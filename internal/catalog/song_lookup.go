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
	if s.CatalogChanges != nil {
		changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
		if err != nil {
			return nil, err
		}
		if song, resolved := models.ResolveCatalogChange(changes, songID); resolved {
			if song == nil {
				return nil, os.ErrNotExist
			}
			return song, nil
		}
	}
	if s.CatalogReader != nil && s.Catalogs != nil && s.Songbooks != nil {
		book, err := s.Songbooks.GetSongbook(ctx, songbookID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, os.ErrNotExist
			}
			return nil, err
		}
		if book.CatalogVersionID != "" {
			version, err := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
			if err != nil {
				return nil, err
			}
			songs, err := s.CatalogReader.ListSongs(ctx, version.AssetPrefix, songbookID)
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
	}
	return nil, os.ErrNotExist
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
	if len(uniqueIDs) == 0 {
		return nil, nil
	}

	resolved := make(map[string]*models.Song, len(uniqueIDs))
	unresolved := make([]string, 0, len(uniqueIDs))

	if s.CatalogChanges != nil {
		changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
		if err != nil {
			return nil, err
		}
		for _, id := range uniqueIDs {
			if song, ok := models.ResolveCatalogChange(changes, id); ok {
				if song != nil && song.SongbookID == songbookID {
					resolved[id] = song
				}
			} else {
				unresolved = append(unresolved, id)
			}
		}
	} else {
		unresolved = uniqueIDs
	}

	if len(unresolved) > 0 && s.CatalogReader != nil && s.Catalogs != nil && s.Songbooks != nil {
		book, err := s.Songbooks.GetSongbook(ctx, songbookID)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else if book.CatalogVersionID != "" {
			version, err := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
			if err != nil {
				return nil, err
			}
			catalogSongs, err := s.CatalogReader.ListSongs(ctx, version.AssetPrefix, songbookID)
			if err != nil {
				return nil, err
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
		}
	}

	ordered := make([]*models.Song, 0, len(resolved))
	for _, id := range uniqueIDs {
		if song, ok := resolved[id]; ok {
			ordered = append(ordered, song)
		}
	}
	return ordered, nil
}
