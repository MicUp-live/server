package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micup.live/server/internal/models"
)

func TestFavoritesHandler_Lifecycle(t *testing.T) {
	store := newAuthTestStore()
	authHandler, err := NewAuthHandler(store, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	favHandler := NewFavoritesHandler(store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth/favorites", authHandler.RequireAuth(favHandler.GetFavorites))
	mux.HandleFunc("PUT /api/v1/auth/favorites", authHandler.RequireAuth(favHandler.SaveFavorites))

	// Create a singer user and a host user
	singer := &models.User{
		ID:          "usr_singer_1",
		Email:       "singer@example.com",
		DisplayName: "Singer Jane",
		IsHost:      false,
	}
	if err := store.CreateUser(context.Background(), singer); err != nil {
		t.Fatalf("create singer: %v", err)
	}

	host := &models.User{
		ID:          "usr_host_1",
		Email:       "host@example.com",
		DisplayName: "Host Dave",
		IsHost:      true,
	}
	if err := store.CreateUser(context.Background(), host); err != nil {
		t.Fatalf("create host: %v", err)
	}

	singerToken, err := authHandler.newAccessToken(singer.ID, singer.AuthEpoch)
	if err != nil {
		t.Fatalf("singer token: %v", err)
	}
	hostToken, err := authHandler.newAccessToken(host.ID, host.AuthEpoch)
	if err != nil {
		t.Fatalf("host token: %v", err)
	}

	t.Run("unauthenticated requests return 401", func(t *testing.T) {
		getReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/favorites", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for GET without token, got %d", getRec.Code)
		}

		putReq := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader(`{}`))
		putRec := httptest.NewRecorder()
		mux.ServeHTTP(putRec, putReq)
		if putRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for PUT without token, got %d", putRec.Code)
		}
	})

	t.Run("initial GET returns empty non-null arrays", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/favorites", nil)
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body models.UserFavorites
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Songs == nil || len(body.Songs) != 0 {
			t.Fatalf("expected empty non-nil songs, got %#v", body.Songs)
		}
		if body.Artists == nil || len(body.Artists) != 0 {
			t.Fatalf("expected empty non-nil artists, got %#v", body.Artists)
		}
	})

	t.Run("PUT saves and normalizes singer favorites", func(t *testing.T) {
		payload := `{
			"songs": [
				{"artist": "  Queen ", "title": " Bohemian Rhapsody ", "preferred_key": 2, "canonical_id": "c_queen_br"},
				{"artist": "David Bowie", "title": "Heroes", "preferred_key": -1}
			],
			"artists": [
				{"name": " Queen "},
				{"name": "David Bowie"}
			]
		}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+singerToken)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var saved models.UserFavorites
		if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(saved.Songs) != 2 || len(saved.Artists) != 2 {
			t.Fatalf("expected 2 songs and 2 artists, got %d songs, %d artists", len(saved.Songs), len(saved.Artists))
		}
		if saved.Songs[0].Artist != "Queen" || saved.Songs[0].PreferredKey != 2 {
			t.Fatalf("unexpected song 0: %#v", saved.Songs[0])
		}
	})

	t.Run("subsequent GET returns persisted favorites", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/favorites", nil)
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var fetched models.UserFavorites
		if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(fetched.Songs) != 2 || len(fetched.Artists) != 2 {
			t.Fatalf("expected 2 songs and 2 artists, got %d songs, %d artists", len(fetched.Songs), len(fetched.Artists))
		}
	})

	t.Run("works for authenticated host as well", func(t *testing.T) {
		payload := `{"songs":[{"artist":"ABBA","title":"Dancing Queen","preferred_key":0}],"artists":[{"name":"ABBA"}]}`
		putReq := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader(payload))
		putReq.Header.Set("Authorization", "Bearer "+hostToken)
		putRec := httptest.NewRecorder()
		mux.ServeHTTP(putRec, putReq)
		if putRec.Code != http.StatusOK {
			t.Fatalf("host PUT expected 200, got %d", putRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/favorites", nil)
		getReq.Header.Set("Authorization", "Bearer "+hostToken)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("host GET expected 200, got %d", getRec.Code)
		}
	})

	t.Run("PUT validates key shift bounds", func(t *testing.T) {
		payload := `{"songs":[{"artist":"Queen","title":"Bohemian Rhapsody","preferred_key":7}],"artists":[]}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for key > 6, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT validates songs limit", func(t *testing.T) {
		tooManySongs := make([]models.FavoriteSong, 501)
		for i := range tooManySongs {
			tooManySongs[i] = models.FavoriteSong{
				Artist: fmt.Sprintf("Artist %d", i),
				Title:  fmt.Sprintf("Title %d", i),
			}
		}
		bodyBytes, _ := json.Marshal(models.UserFavorites{Songs: tooManySongs})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for > 500 songs, got %d", rec.Code)
		}
	})

	t.Run("PUT invalid JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader("invalid-json"))
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("PUT exceeding body limit is rejected", func(t *testing.T) {
		hugeString := strings.Repeat("A", 300*1024) // 300KB > 256KB
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/favorites", strings.NewReader(hugeString))
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for > 256KB, got %d", rec.Code)
		}
	})

	t.Run("deleting user returns 401", func(t *testing.T) {
		singer.Deleting = true
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/favorites", nil)
		req.Header.Set("Authorization", "Bearer "+singerToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for deleting user, got %d", rec.Code)
		}
	})
}
