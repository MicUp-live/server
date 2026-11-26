package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	MaxFavoriteSongs   = 500
	MaxFavoriteArtists = 200
	MinPreferredKey    = -6
	MaxPreferredKey    = 6
)

var (
	ErrFavoriteSongsLimitExceeded   = fmt.Errorf("favorite songs limit exceeded (max %d)", MaxFavoriteSongs)
	ErrFavoriteArtistsLimitExceeded = fmt.Errorf("favorite artists limit exceeded (max %d)", MaxFavoriteArtists)
	ErrInvalidPreferredKey          = fmt.Errorf("preferred key must be between %d and %d", MinPreferredKey, MaxPreferredKey)
)

// FavoriteSong represents a singer's bookmarked song with pitch preference.
type FavoriteSong struct {
	Artist       string `json:"artist" dynamodbav:"artist"`
	Title        string `json:"title" dynamodbav:"title"`
	PreferredKey int    `json:"preferred_key" dynamodbav:"preferred_key"`
	CanonicalID  string `json:"canonical_id,omitempty" dynamodbav:"canonical_id,omitempty"`
	AddedAt      int64  `json:"added_at" dynamodbav:"added_at"`
}

// FavoriteArtist represents a bookmarked artist name.
type FavoriteArtist struct {
	Name    string `json:"name" dynamodbav:"name"`
	AddedAt int64  `json:"added_at" dynamodbav:"added_at"`
}

// UserFavorites wraps a user's bookmarked songs and artists.
type UserFavorites struct {
	Songs   []FavoriteSong   `json:"songs"`
	Artists []FavoriteArtist `json:"artists"`
}

// NormalizeFavorites trims whitespace, drops empty entries, deduplicates songs and artists,
// validates key shifts and list limits, and ensures added_at timestamps are present.
func NormalizeFavorites(favs *UserFavorites) (*UserFavorites, error) {
	if favs == nil {
		return &UserFavorites{
			Songs:   []FavoriteSong{},
			Artists: []FavoriteArtist{},
		}, nil
	}

	nowMs := time.Now().UnixMilli()

	normalizedSongs := make([]FavoriteSong, 0, len(favs.Songs))
	seenCanonical := make(map[string]bool)
	seenSongKey := make(map[string]bool)

	for _, s := range favs.Songs {
		artist := strings.TrimSpace(s.Artist)
		title := strings.TrimSpace(s.Title)
		canonicalID := strings.TrimSpace(s.CanonicalID)

		if artist == "" || title == "" {
			continue
		}

		if s.PreferredKey < MinPreferredKey || s.PreferredKey > MaxPreferredKey {
			return nil, ErrInvalidPreferredKey
		}

		canonicalKey := strings.ToLower(canonicalID)
		songKey := strings.ToLower(artist) + "::" + strings.ToLower(title)

		if canonicalKey != "" && seenCanonical[canonicalKey] {
			continue
		}
		if seenSongKey[songKey] {
			continue
		}

		if canonicalKey != "" {
			seenCanonical[canonicalKey] = true
		}
		seenSongKey[songKey] = true

		addedAt := s.AddedAt
		if addedAt <= 0 {
			addedAt = nowMs
		}

		normalizedSongs = append(normalizedSongs, FavoriteSong{
			Artist:       artist,
			Title:        title,
			PreferredKey: s.PreferredKey,
			CanonicalID:  canonicalID,
			AddedAt:      addedAt,
		})
	}

	if len(normalizedSongs) > MaxFavoriteSongs {
		return nil, ErrFavoriteSongsLimitExceeded
	}

	normalizedArtists := make([]FavoriteArtist, 0, len(favs.Artists))
	seenArtistKey := make(map[string]bool)

	for _, a := range favs.Artists {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}

		artistKey := strings.ToLower(name)
		if seenArtistKey[artistKey] {
			continue
		}
		seenArtistKey[artistKey] = true

		addedAt := a.AddedAt
		if addedAt <= 0 {
			addedAt = nowMs
		}

		normalizedArtists = append(normalizedArtists, FavoriteArtist{
			Name:    name,
			AddedAt: addedAt,
		})
	}

	if len(normalizedArtists) > MaxFavoriteArtists {
		return nil, ErrFavoriteArtistsLimitExceeded
	}

	return &UserFavorites{
		Songs:   normalizedSongs,
		Artists: normalizedArtists,
	}, nil
}
