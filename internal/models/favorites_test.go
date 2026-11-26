package models

import (
	"fmt"
	"testing"
	"time"
)

func TestNormalizeFavorites_Nil(t *testing.T) {
	favs, err := NormalizeFavorites(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if favs.Songs == nil || len(favs.Songs) != 0 {
		t.Fatalf("expected empty non-nil songs slice, got: %#v", favs.Songs)
	}
	if favs.Artists == nil || len(favs.Artists) != 0 {
		t.Fatalf("expected empty non-nil artists slice, got: %#v", favs.Artists)
	}
}

func TestNormalizeFavorites_TrimAndDropEmpty(t *testing.T) {
	input := &UserFavorites{
		Songs: []FavoriteSong{
			{Artist: "  Queen  ", Title: "  Bohemian Rhapsody  ", PreferredKey: 1, CanonicalID: "  c123  "},
			{Artist: "   ", Title: "Valid Title"},
			{Artist: "Valid Artist", Title: "   "},
			{Artist: "", Title: ""},
		},
		Artists: []FavoriteArtist{
			{Name: "  Queen  "},
			{Name: "   "},
			{Name: ""},
		},
	}

	norm, err := NormalizeFavorites(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(norm.Songs) != 1 {
		t.Fatalf("expected 1 song, got %d", len(norm.Songs))
	}
	if norm.Songs[0].Artist != "Queen" || norm.Songs[0].Title != "Bohemian Rhapsody" || norm.Songs[0].CanonicalID != "c123" {
		t.Fatalf("unexpected song: %#v", norm.Songs[0])
	}
	if norm.Songs[0].AddedAt <= 0 {
		t.Fatalf("expected positive added_at timestamp, got %d", norm.Songs[0].AddedAt)
	}
	if len(norm.Artists) != 1 {
		t.Fatalf("expected 1 artist, got %d", len(norm.Artists))
	}
	if norm.Artists[0].Name != "Queen" {
		t.Fatalf("unexpected artist: %#v", norm.Artists[0])
	}
	if norm.Artists[0].AddedAt <= 0 {
		t.Fatalf("expected positive added_at timestamp, got %d", norm.Artists[0].AddedAt)
	}
}

func TestNormalizeFavorites_Deduplication(t *testing.T) {
	input := &UserFavorites{
		Songs: []FavoriteSong{
			{Artist: "Queen", Title: "Bohemian Rhapsody", PreferredKey: 0, CanonicalID: "canon-1", AddedAt: 100},
			{Artist: "queen", Title: "bohemian rhapsody", PreferredKey: 2, CanonicalID: "canon-2", AddedAt: 200}, // same artist::title
			{Artist: "Different", Title: "Track", PreferredKey: -1, CanonicalID: "CANON-1", AddedAt: 300},        // same canonical_id
			{Artist: "David Bowie", Title: "Heroes", PreferredKey: 0, AddedAt: 400},
			{Artist: "david bowie", Title: "heroes", PreferredKey: 0, AddedAt: 500},
		},
		Artists: []FavoriteArtist{
			{Name: "Queen", AddedAt: 100},
			{Name: "QUEEN", AddedAt: 200},
			{Name: "David Bowie", AddedAt: 300},
			{Name: "david bowie", AddedAt: 400},
		},
	}

	norm, err := NormalizeFavorites(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(norm.Songs) != 2 {
		t.Fatalf("expected 2 deduped songs, got %d: %#v", len(norm.Songs), norm.Songs)
	}
	if norm.Songs[0].Artist != "Queen" || norm.Songs[1].Artist != "David Bowie" {
		t.Fatalf("unexpected songs: %#v", norm.Songs)
	}
	if len(norm.Artists) != 2 {
		t.Fatalf("expected 2 deduped artists, got %d: %#v", len(norm.Artists), norm.Artists)
	}
	if norm.Artists[0].Name != "Queen" || norm.Artists[1].Name != "David Bowie" {
		t.Fatalf("unexpected artists: %#v", norm.Artists)
	}
}

func TestNormalizeFavorites_PreferredKeyBounds(t *testing.T) {
	for _, key := range []int{-6, -3, 0, 3, 6} {
		input := &UserFavorites{
			Songs: []FavoriteSong{
				{Artist: "Artist", Title: "Title", PreferredKey: key},
			},
		}
		norm, err := NormalizeFavorites(input)
		if err != nil {
			t.Fatalf("key %d should be valid, got: %v", key, err)
		}
		if norm.Songs[0].PreferredKey != key {
			t.Fatalf("expected key %d, got %d", key, norm.Songs[0].PreferredKey)
		}
	}

	for _, invalidKey := range []int{-7, -100, 7, 12} {
		input := &UserFavorites{
			Songs: []FavoriteSong{
				{Artist: "Artist", Title: "Title", PreferredKey: invalidKey},
			},
		}
		if _, err := NormalizeFavorites(input); err == nil {
			t.Fatalf("key %d should have failed validation", invalidKey)
		}
	}
}

func TestNormalizeFavorites_LimitEnforcement(t *testing.T) {
	tooManySongs := make([]FavoriteSong, MaxFavoriteSongs+1)
	for i := 0; i <= MaxFavoriteSongs; i++ {
		tooManySongs[i] = FavoriteSong{
			Artist: fmt.Sprintf("Artist %d", i),
			Title:  fmt.Sprintf("Title %d", i),
		}
	}
	if _, err := NormalizeFavorites(&UserFavorites{Songs: tooManySongs}); err == nil {
		t.Fatal("expected songs limit exceeded error")
	}

	tooManyArtists := make([]FavoriteArtist, MaxFavoriteArtists+1)
	for i := 0; i <= MaxFavoriteArtists; i++ {
		tooManyArtists[i] = FavoriteArtist{
			Name: fmt.Sprintf("Artist %d", i),
		}
	}
	if _, err := NormalizeFavorites(&UserFavorites{Artists: tooManyArtists}); err == nil {
		t.Fatal("expected artists limit exceeded error")
	}
}

func TestNormalizeFavorites_PreservesExistingAddedAt(t *testing.T) {
	addedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	input := &UserFavorites{
		Songs: []FavoriteSong{
			{Artist: "Artist", Title: "Title", AddedAt: addedAt},
		},
		Artists: []FavoriteArtist{
			{Name: "Artist", AddedAt: addedAt},
		},
	}
	norm, err := NormalizeFavorites(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if norm.Songs[0].AddedAt != addedAt {
		t.Fatalf("expected added_at %d, got %d", addedAt, norm.Songs[0].AddedAt)
	}
	if norm.Artists[0].AddedAt != addedAt {
		t.Fatalf("expected added_at %d, got %d", addedAt, norm.Artists[0].AddedAt)
	}
}
