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

func indexBaseSongs(base []*Song) map[string]*Song {
	byID := make(map[string]*Song, len(base))
	for _, song := range base {
		if song != nil {
			byID[song.ID] = song
		}
	}
	return byID
}

func isSurvivingChange(change *CatalogChange, hasClear bool, latestClearAt time.Time) bool {
	if change == nil || change.Clear {
		return false
	}
	if hasClear && !latestClearAt.IsZero() && !change.UpdatedAt.After(latestClearAt) {
		return false
	}
	return true
}

func isApplicableSongChange(change *CatalogChange, songID string, hasClear bool, latestClearAt time.Time) bool {
	if !isSurvivingChange(change, hasClear, latestClearAt) || change.SongID != songID {
		return false
	}
	return change.Deleted || change.Song != nil
}

// ApplyCatalogChanges replays pending catalog overrides onto base songs in a
// timestamp-safe manner so Clear markers never depend on query or map order.
func ApplyCatalogChanges(base []*Song, changes []*CatalogChange) map[string]*Song {
	hasClear, latestClearAt := latestClearTimestamp(changes)

	byID := make(map[string]*Song)
	if !hasClear {
		byID = indexBaseSongs(base)
	}

	surviving := make([]*CatalogChange, 0, len(changes))
	for _, change := range changes {
		if !isSurvivingChange(change, hasClear, latestClearAt) {
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
		if !isApplicableSongChange(change, songID, hasClear, latestClearAt) {
			continue
		}
		if matched == nil || !change.UpdatedAt.Before(matched.UpdatedAt) {
			matched = change
		}
	}
	if matched == nil {
		return nil, hasClear
	}
	if matched.Deleted || matched.Song == nil {
		return nil, true
	}
	return matched.Song, true
}
