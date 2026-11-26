package dynamo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	parent "micup.live/server/internal/store"
)

func TestFavorites_Persistence(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	t.Run("non-existent user returns os.ErrNotExist", func(t *testing.T) {
		_, err := s.GetUserFavorites(ctx, "usr_does_not_exist_"+uuid.NewString())
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist, got %v", err)
		}

		err = s.SaveUserFavorites(ctx, "usr_does_not_exist_"+uuid.NewString(), &models.UserFavorites{})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist on save for missing user, got %v", err)
		}
	})

	t.Run("fresh user returns empty non-nil favorites", func(t *testing.T) {
		user := helperCreateUser(t, s, "")
		favs, err := s.GetUserFavorites(ctx, user.ID)
		if err != nil {
			t.Fatalf("get favorites for fresh user: %v", err)
		}
		if favs.Songs == nil || len(favs.Songs) != 0 {
			t.Fatalf("expected empty non-nil songs, got %#v", favs.Songs)
		}
		if favs.Artists == nil || len(favs.Artists) != 0 {
			t.Fatalf("expected empty non-nil artists, got %#v", favs.Artists)
		}
	})

	t.Run("save and round-trip retrieval", func(t *testing.T) {
		user := helperCreateUser(t, s, "")
		favsToSave := &models.UserFavorites{
			Songs: []models.FavoriteSong{
				{
					Artist:       "Queen",
					Title:        "Bohemian Rhapsody",
					PreferredKey: 2,
					CanonicalID:  "c_br_01",
					AddedAt:      1700000000000,
				},
				{
					Artist:       "David Bowie",
					Title:        "Space Oddity",
					PreferredKey: -1,
					AddedAt:      1700000001000,
				},
			},
			Artists: []models.FavoriteArtist{
				{Name: "Queen", AddedAt: 1700000000000},
				{Name: "David Bowie", AddedAt: 1700000001000},
			},
		}

		if err := s.SaveUserFavorites(ctx, user.ID, favsToSave); err != nil {
			t.Fatalf("save favorites: %v", err)
		}

		retrieved, err := s.GetUserFavorites(ctx, user.ID)
		if err != nil {
			t.Fatalf("get favorites: %v", err)
		}
		if len(retrieved.Songs) != 2 {
			t.Fatalf("expected 2 songs, got %d", len(retrieved.Songs))
		}
		if retrieved.Songs[0].Artist != "Queen" || retrieved.Songs[0].PreferredKey != 2 || retrieved.Songs[0].CanonicalID != "c_br_01" {
			t.Fatalf("unexpected song 0: %#v", retrieved.Songs[0])
		}
		if len(retrieved.Artists) != 2 {
			t.Fatalf("expected 2 artists, got %d", len(retrieved.Artists))
		}
		if retrieved.Artists[0].Name != "Queen" {
			t.Fatalf("unexpected artist 0: %#v", retrieved.Artists[0])
		}

		// Also verify GetUser loads favorites
		reloadedUser, err := s.GetUser(ctx, user.ID)
		if err != nil {
			t.Fatalf("reload user: %v", err)
		}
		if len(reloadedUser.FavoriteSongs) != 2 || len(reloadedUser.FavoriteArtists) != 2 {
			t.Fatalf("expected GetUser to populate favorites, got %d songs, %d artists",
				len(reloadedUser.FavoriteSongs), len(reloadedUser.FavoriteArtists))
		}
	})

	t.Run("overwrite favorites", func(t *testing.T) {
		user := helperCreateUser(t, s, "")
		initial := &models.UserFavorites{
			Songs: []models.FavoriteSong{
				{Artist: "Old Artist", Title: "Old Title", PreferredKey: 0, AddedAt: 100},
			},
			Artists: []models.FavoriteArtist{
				{Name: "Old Artist", AddedAt: 100},
			},
		}
		if err := s.SaveUserFavorites(ctx, user.ID, initial); err != nil {
			t.Fatalf("save initial: %v", err)
		}

		updated := &models.UserFavorites{
			Songs: []models.FavoriteSong{
				{Artist: "New Artist", Title: "New Title", PreferredKey: -3, AddedAt: 200},
			},
			Artists: []models.FavoriteArtist{},
		}
		if err := s.SaveUserFavorites(ctx, user.ID, updated); err != nil {
			t.Fatalf("save updated: %v", err)
		}

		retrieved, err := s.GetUserFavorites(ctx, user.ID)
		if err != nil {
			t.Fatalf("get updated: %v", err)
		}
		if len(retrieved.Songs) != 1 || retrieved.Songs[0].Artist != "New Artist" {
			t.Fatalf("expected 1 new song, got %#v", retrieved.Songs)
		}
		if len(retrieved.Artists) != 0 {
			t.Fatalf("expected 0 artists, got %d", len(retrieved.Artists))
		}
	})

	t.Run("deleting account rejects save", func(t *testing.T) {
		user := helperCreateUser(t, s, "")
		// Begin deletion
		_, err := s.BeginAccountDeletion(ctx, user.ID, nil)
		if err != nil {
			t.Fatalf("begin account deletion: %v", err)
		}

		err = s.SaveUserFavorites(ctx, user.ID, &models.UserFavorites{
			Songs: []models.FavoriteSong{{Artist: "A", Title: "T", PreferredKey: 0, AddedAt: time.Now().UnixMilli()}},
		})
		if !errors.Is(err, parent.ErrAccountDeleting) {
			t.Fatalf("expected ErrAccountDeleting, got %v", err)
		}
	})
}
