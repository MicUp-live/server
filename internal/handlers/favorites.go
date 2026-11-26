package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const maxFavoritesBodyBytes = 256 * 1024 // 256KB

// FavoritesHandler manages HTTP endpoints for account-synced favorites.
type FavoritesHandler struct {
	Store store.FavoritesStore
}

// NewFavoritesHandler creates a new FavoritesHandler.
func NewFavoritesHandler(s store.FavoritesStore) *FavoritesHandler {
	return &FavoritesHandler{Store: s}
}

// GetFavorites returns the authenticated user's account-synced favorite songs and artists.
func (h *FavoritesHandler) GetFavorites(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	favs, err := h.Store.GetUserFavorites(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeAuthJSON(w, http.StatusOK, &models.UserFavorites{
				Songs:   []models.FavoriteSong{},
				Artists: []models.FavoriteArtist{},
			})
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "failed to get favorites")
		return
	}

	if favs == nil {
		favs = &models.UserFavorites{
			Songs:   []models.FavoriteSong{},
			Artists: []models.FavoriteArtist{},
		}
	}
	if favs.Songs == nil {
		favs.Songs = []models.FavoriteSong{}
	}
	if favs.Artists == nil {
		favs.Artists = []models.FavoriteArtist{}
	}

	writeAuthJSON(w, http.StatusOK, favs)
}

// SaveFavorites validates, normalizes, deduplicates, and saves the authenticated user's favorites.
func (h *FavoritesHandler) SaveFavorites(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFavoritesBodyBytes)

	var payload models.UserFavorites
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	normalized, err := models.NormalizeFavorites(&payload)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.Store.SaveUserFavorites(r.Context(), user.ID, normalized); err != nil {
		if errors.Is(err, store.ErrAccountDeleting) {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "failed to save favorites")
		return
	}

	writeAuthJSON(w, http.StatusOK, normalized)
}
