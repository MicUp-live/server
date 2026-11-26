package account

import (
	"context"
	"fmt"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// UserDataExport defines the structured machine-readable export package.
type UserDataExport struct {
	ExportedAt      time.Time               `json:"exported_at"`
	Service         string                  `json:"service"`
	ComplianceNote  string                  `json:"compliance_note,omitempty"`
	User            *models.User            `json:"user"`
	FavoriteSongs   []models.FavoriteSong   `json:"favorite_songs,omitempty"`
	FavoriteArtists []models.FavoriteArtist `json:"favorite_artists,omitempty"`
	Passkeys        []PasskeyExportItem     `json:"passkeys,omitempty"`
	Songbooks       []SongbookExportItem    `json:"songbooks,omitempty"`
	Events          []*models.Event         `json:"events,omitempty"`
}

// PasskeyExportItem represents passkey metadata in an export.
type PasskeyExportItem struct {
	ID           string    `json:"id"`
	FriendlyName string    `json:"friendly_name"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
}

// SongbookExportItem represents songbook metadata, changes, and songs in an export.
type SongbookExportItem struct {
	Songbook       *models.Songbook        `json:"songbook"`
	CatalogVersion string                  `json:"catalog_version_id,omitempty"`
	CatalogURL     string                  `json:"catalog_url,omitempty"`
	ManifestURL    string                  `json:"manifest_url,omitempty"`
	PendingChanges []*models.CatalogChange `json:"pending_changes,omitempty"`
	Songs          []*models.Song          `json:"songs,omitempty"`
	SongCount      int                     `json:"song_count"`
}

// ExportError captures a failure to export a specific section of user data.
type ExportError struct {
	Section string
	Message string
	Err     error
}

func (e *ExportError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *ExportError) Unwrap() error {
	return e.Err
}

func populateFavorites(ctx context.Context, export *UserDataExport, user *models.User, auth store.AuthStore) {
	if len(user.FavoriteSongs) > 0 {
		export.FavoriteSongs = user.FavoriteSongs
	}
	if len(user.FavoriteArtists) > 0 {
		export.FavoriteArtists = user.FavoriteArtists
	}
	if len(export.FavoriteSongs) == 0 && len(export.FavoriteArtists) == 0 && auth != nil {
		if fs, ok := auth.(store.FavoritesStore); ok && fs != nil {
			if favs, err := fs.GetUserFavorites(ctx, user.ID); err == nil && favs != nil {
				if len(favs.Songs) > 0 {
					export.FavoriteSongs = favs.Songs
					user.FavoriteSongs = favs.Songs
				}
				if len(favs.Artists) > 0 {
					export.FavoriteArtists = favs.Artists
					user.FavoriteArtists = favs.Artists
				}
			}
		}
	}
}
