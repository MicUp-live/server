package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"micup.live/server/internal/config"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type songLocSongbookStore struct {
	store.SongbookStore
	books map[string]*models.Songbook
}

func (s *songLocSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	b, ok := s.books[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

type songLocChangesStore struct {
	store.CatalogChangeStore
	changes []*models.CatalogChange
}

func (s *songLocChangesStore) ListCatalogChanges(_ context.Context, _ string) ([]*models.CatalogChange, error) {
	return s.changes, nil
}

func TestRouterGetSongLocationAuthAndLifecycle(t *testing.T) {
	const secret = "test-signing-secret-with-at-least-32-bytes"
	authStore := &keyRouteAuthStore{users: map[string]*models.User{
		"host":       {ID: "host", IsHost: true},
		"other-host": {ID: "other-host", IsHost: true},
	}}
	keys := &keyRouteStore{keys: map[string]*models.DeveloperKey{
		"mk_live_read":  {ID: "read-key", OwnerID: "host", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeRequestsRead}},
		"mk_live_write": {ID: "write-key", OwnerID: "host", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeCatalogsWrite}},
		"mk_live_other": {ID: "other-key", OwnerID: "other-host", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeCatalogsWrite}},
	}}
	books := &songLocSongbookStore{books: map[string]*models.Songbook{
		"sb-1": {ID: "sb-1", OwnerID: "host", Name: "Host Book"},
	}}
	song1 := &models.Song{
		ID:         "song-1",
		SongbookID: "sb-1",
		Artist:     "Velvet Revolver",
		Title:      "Fall To Pieces",
		Location:   `C:\Karaoke\Rock\CB40123-05 - Velvet Revolver - Fall To Pieces.zip`,
	}
	changes := &songLocChangesStore{
		changes: []*models.CatalogChange{
			{SongbookID: "sb-1", SongID: "song-1", Song: song1, UpdatedAt: time.Now().UTC().Add(-2 * time.Minute)},
		},
	}

	router, err := NewRouter(config.Config{
		Environment:           config.Development,
		Port:                  "8080",
		AuthSecret:            secret,
		DeveloperKeyScopeMode: config.DeveloperKeyScopeEnforce,
	}, Dependencies{
		Auth:           authStore,
		DeveloperKeys:  keys,
		Songbooks:      books,
		CatalogChanges: changes,
	})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	// 1. Unauthenticated -> 401
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/song-1", "", ""); resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d; want 401", resp.Code)
	}

	// 2. Insufficient developer scope -> 403
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/song-1", "", "mk_live_read"); resp.Code != http.StatusForbidden {
		t.Fatalf("wrong scope status = %d; want 403", resp.Code)
	}

	// 3. Cross-tenant host -> 404 (prevents cross-tenant songbook ID enumeration)
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/song-1", "", "mk_live_other"); resp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status = %d; want 404", resp.Code)
	}

	// 4. Authorized owner -> 200 with host-only location
	resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/song-1", "", "mk_live_write")
	if resp.Code != http.StatusOK {
		t.Fatalf("authorized status = %d; want 200 (body: %s)", resp.Code, resp.Body.String())
	}
	var got models.Song
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode song: %v", err)
	}
	if got.Location != `C:\Karaoke\Rock\CB40123-05 - Velvet Revolver - Fall To Pieces.zip` {
		t.Fatalf("got location %q; want original path", got.Location)
	}

	// 5. Missing song -> 404
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/missing", "", "mk_live_write"); resp.Code != http.StatusNotFound {
		t.Fatalf("missing song status = %d; want 404", resp.Code)
	}

	// 6. After __clear__ tombstone (even with pre-clear change sorting before "__clear__") -> 404
	now := time.Now().UTC()
	changes.changes = []*models.CatalogChange{
		{SongbookID: "sb-1", SongID: "song-1", Song: song1, UpdatedAt: now.Add(-time.Minute)},
		{SongbookID: "sb-1", SongID: "__clear__", Clear: true, UpdatedAt: now},
	}
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/songbooks/sb-1/songs/song-1", "", "mk_live_write"); resp.Code != http.StatusNotFound {
		t.Fatalf("cleared song status = %d; want 404", resp.Code)
	}
}
