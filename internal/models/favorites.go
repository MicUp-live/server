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

	normalizedSongs, err := normalizeFavoriteSongs(favs.Songs, nowMs)
	if err != nil {
		return nil, err
	}

	normalizedArtists, err := normalizeFavoriteArtists(favs.Artists, nowMs)
	if err != nil {
		return nil, err
	}

	return &UserFavorites{
		Songs:   normalizedSongs,
		Artists: normalizedArtists,
	}, nil
}

func defaultAddedAt(addedAt, nowMs int64) int64 {
	if addedAt <= 0 {
		return nowMs
	}
	return addedAt
}

func normalizeFavoriteSong(s FavoriteSong, nowMs int64) (FavoriteSong, bool, error) {
	artist := strings.TrimSpace(s.Artist)
	title := strings.TrimSpace(s.Title)
	if artist == "" || title == "" {
		return FavoriteSong{}, false, nil
	}

	if s.PreferredKey < MinPreferredKey || s.PreferredKey > MaxPreferredKey {
		return FavoriteSong{}, false, ErrInvalidPreferredKey
	}

	return FavoriteSong{
		Artist:       artist,
		Title:        title,
		PreferredKey: s.PreferredKey,
		CanonicalID:  strings.TrimSpace(s.CanonicalID),
		AddedAt:      defaultAddedAt(s.AddedAt, nowMs),
	}, true, nil
}

func markSongSeen(song FavoriteSong, seenCanonical, seenSongKey map[string]bool) bool {
	canonicalKey := strings.ToLower(song.CanonicalID)
	songKey := strings.ToLower(song.Artist) + "::" + strings.ToLower(song.Title)

	if canonicalKey != "" && seenCanonical[canonicalKey] {
		return true
	}
	if seenSongKey[songKey] {
		return true
	}

	if canonicalKey != "" {
		seenCanonical[canonicalKey] = true
	}
	seenSongKey[songKey] = true
	return false
}

func normalizeFavoriteSongs(songs []FavoriteSong, nowMs int64) ([]FavoriteSong, error) {
	normalized := make([]FavoriteSong, 0, len(songs))
	seenCanonical := make(map[string]bool)
	seenSongKey := make(map[string]bool)

	for _, s := range songs {
		song, ok, err := normalizeFavoriteSong(s, nowMs)
		if err != nil {
			return nil, err
		}
		if !ok || markSongSeen(song, seenCanonical, seenSongKey) {
			continue
		}
		normalized = append(normalized, song)
	}

	if len(normalized) > MaxFavoriteSongs {
		return nil, ErrFavoriteSongsLimitExceeded
	}
	return normalized, nil
}

func normalizeFavoriteArtists(artists []FavoriteArtist, nowMs int64) ([]FavoriteArtist, error) {
	normalized := make([]FavoriteArtist, 0, len(artists))
	seenArtistKey := make(map[string]bool)

	for _, a := range artists {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}

		artistKey := strings.ToLower(name)
		if seenArtistKey[artistKey] {
			continue
		}
		seenArtistKey[artistKey] = true

		normalized = append(normalized, FavoriteArtist{
			Name:    name,
			AddedAt: defaultAddedAt(a.AddedAt, nowMs),
		})
	}

	if len(normalized) > MaxFavoriteArtists {
		return nil, ErrFavoriteArtistsLimitExceeded
	}
	return normalized, nil
}
