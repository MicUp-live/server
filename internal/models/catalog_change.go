package models

import (
	"sort"
	"time"
)

// CatalogChange is a temporary songbook delta waiting for S3 compaction.
type CatalogChange struct {
	SongbookID string    `json:"songbook_id"`
	SongID     string    `json:"song_id"`
	Song       *Song     `json:"song"`
	Deleted    bool      `json:"deleted"`
	Clear      bool      `json:"clear"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func latestClearTimestamp(changes []*CatalogChange) (bool, time.Time) {
	var hasClear bool
	var latestClearAt time.Time
	for _, change := range changes {
		if change != nil && change.Clear {
			hasClear = true
			if change.UpdatedAt.After(latestClearAt) {
				latestClearAt = change.UpdatedAt
			}
		}
	}
	return hasClear, latestClearAt
}

// ApplyCatalogChanges replays pending catalog overrides onto base songs in a
// timestamp-safe manner so Clear markers never depend on query or map order.
func ApplyCatalogChanges(base []*Song, changes []*CatalogChange) map[string]*Song {
	hasClear, latestClearAt := latestClearTimestamp(changes)

	byID := make(map[string]*Song, len(base))
	if !hasClear {
		for _, song := range base {
			if song != nil {
				byID[song.ID] = song
			}
		}
	}

	surviving := make([]*CatalogChange, 0, len(changes))
	for _, change := range changes {
		if change == nil || change.Clear {
			continue
		}
		if hasClear && !latestClearAt.IsZero() && !change.UpdatedAt.After(latestClearAt) {
			continue
		}
		surviving = append(surviving, change)
	}
	sort.SliceStable(surviving, func(i, j int) bool {
		return surviving[i].UpdatedAt.Before(surviving[j].UpdatedAt)
	})

	for _, change := range surviving {
		if change.Deleted {
			delete(byID, change.SongID)
		} else if change.Song != nil {
			byID[change.SongID] = change.Song
		}
	}
	return byID
}

// ResolveCatalogChange resolves a single songID against pending catalog changes.
// When resolved is true, song is the authoritative result (nil means deleted or cleared).
// When resolved is false, callers should fall back to the base catalog.
func ResolveCatalogChange(changes []*CatalogChange, songID string) (*Song, bool) {
	hasClear, latestClearAt := latestClearTimestamp(changes)

	var matched *CatalogChange
	for _, change := range changes {
		if change == nil || change.Clear || change.SongID != songID {
			continue
		}
		if !change.Deleted && change.Song == nil {
			continue
		}
		if hasClear && !latestClearAt.IsZero() && !change.UpdatedAt.After(latestClearAt) {
			continue
		}
		if matched == nil || !change.UpdatedAt.Before(matched.UpdatedAt) {
			matched = change
		}
	}
	if matched != nil {
		if matched.Deleted || matched.Song == nil {
			return nil, true
		}
		return matched.Song, true
	}
	if hasClear {
		return nil, true
	}
	return nil, false
}
