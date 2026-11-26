package postgres

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// GetUserFavorites retrieves the bookmarked songs and artists for a user.
func (s *Store) GetUserFavorites(ctx context.Context, userID string) (*models.UserFavorites, error) {
	var songsRaw, artistsRaw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT favorite_songs, favorite_artists FROM users WHERE id = $1`,
		userID,
	).Scan(&songsRaw, &artistsRaw)
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get user favorites: %w", err)
	}

	var songs []models.FavoriteSong
	if err := unmarshalJSON(songsRaw, &songs); err != nil {
		return nil, fmt.Errorf("unmarshal favorite songs: %w", err)
	}
	if songs == nil {
		songs = []models.FavoriteSong{}
	}

	var artists []models.FavoriteArtist
	if err := unmarshalJSON(artistsRaw, &artists); err != nil {
		return nil, fmt.Errorf("unmarshal favorite artists: %w", err)
	}
	if artists == nil {
		artists = []models.FavoriteArtist{}
	}

	return &models.UserFavorites{
		Songs:   songs,
		Artists: artists,
	}, nil
}

// SaveUserFavorites persists the user's bookmarked songs and artists on the users table row.
func (s *Store) SaveUserFavorites(ctx context.Context, userID string, favs *models.UserFavorites) error {
	if favs == nil {
		favs = &models.UserFavorites{
			Songs:   []models.FavoriteSong{},
			Artists: []models.FavoriteArtist{},
		}
	}
	songs := favs.Songs
	if songs == nil {
		songs = []models.FavoriteSong{}
	}
	artists := favs.Artists
	if artists == nil {
		artists = []models.FavoriteArtist{}
	}

	songsJSON, err := marshalJSON(songs)
	if err != nil {
		return fmt.Errorf("marshal favorite songs: %w", err)
	}
	artistsJSON, err := marshalJSON(artists)
	if err != nil {
		return fmt.Errorf("marshal favorite artists: %w", err)
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE users
			 SET favorite_songs = $1, favorite_artists = $2, updated_at = $3
			 WHERE id = $4 AND deleting = FALSE`,
			songsJSON, artistsJSON, s.currentTime(), userID,
		)
		if err != nil {
			return fmt.Errorf("save user favorites: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var deleting bool
		err = tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1`, userID).Scan(&deleting)
		if err == nil && deleting {
			return store.ErrAccountDeleting
		}
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("save user favorites: %w", err)
		}
		return os.ErrNotExist
	})
}
