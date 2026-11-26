package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"micup.live/server/internal/catalog"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type testStore struct {
	store.SongbookStore
	store.EventStore
	mu           sync.Mutex
	songbooks    map[string]*models.Songbook
	songs        map[string]*models.Song
	changes      map[string][]*models.CatalogChange
	fingerprints map[string]string // fingerprint -> song ID (for dedup)
	sbOrder      []string
	songOrder    map[string][]string
	events       map[string]*models.Event
}

type recordingCatalogQueue struct {
	jobs []catalog.BuildJob
}

func (q *recordingCatalogQueue) Enqueue(_ context.Context, job catalog.BuildJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}

func newTestStore() *testStore {
	return &testStore{
		songbooks:    make(map[string]*models.Songbook),
		songs:        make(map[string]*models.Song),
		changes:      make(map[string][]*models.CatalogChange),
		fingerprints: make(map[string]string),
		sbOrder:      []string{},
		songOrder:    make(map[string][]string),
		events:       make(map[string]*models.Event),
	}
}

func (s *testStore) CreateSongbook(ctx context.Context, sb *models.Songbook) error {
	sb.CreatedAt = time.Now()
	sb.UpdatedAt = time.Now()
	s.songbooks[sb.ID] = sb
	s.sbOrder = append(s.sbOrder, sb.ID)
	return nil
}

func (s *testStore) UpdateSongbook(ctx context.Context, songbook *models.Songbook) error {
	if _, ok := s.songbooks[songbook.ID]; !ok {
		return context.DeadlineExceeded
	}
	songbook.UpdatedAt = time.Now()
	s.songbooks[songbook.ID] = songbook
	return nil
}

func (s *testStore) DeleteSongbook(ctx context.Context, id string) error {
	if _, ok := s.songbooks[id]; !ok {
		return context.DeadlineExceeded
	}
	delete(s.songbooks, id)
	return nil
}

func (s *testStore) GetSongbook(ctx context.Context, id string) (*models.Songbook, error) {
	sb, ok := s.songbooks[id]
	if !ok {
		return nil, context.DeadlineExceeded
	}
	return sb, nil
}

func (s *testStore) GetPublicSongbook(ctx context.Context, publicID string) (*models.Songbook, error) {
	norm := strings.ToLower(strings.TrimSpace(publicID))
	for _, songbook := range s.songbooks {
		if (songbook.PublicID == publicID || (songbook.Slug != "" && songbook.Slug == norm)) && songbook.IsPublic {
			return songbook, nil
		}
	}
	return nil, context.DeadlineExceeded
}

func (s *testStore) GetSongbookBySlug(ctx context.Context, slug string) (*models.Songbook, error) {
	norm := strings.ToLower(strings.TrimSpace(slug))
	if norm == "" {
		return nil, context.DeadlineExceeded
	}
	for _, songbook := range s.songbooks {
		if songbook.Slug == norm {
			return songbook, nil
		}
	}
	return nil, context.DeadlineExceeded
}

func (s *testStore) ListSongbooks(ctx context.Context) ([]*models.Songbook, error) {
	var result []*models.Songbook
	for _, id := range s.sbOrder {
		result = append(result, s.songbooks[id])
	}
	return result, nil
}

func (s *testStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}

func (s *testStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	if sb, ok := s.songbooks[id]; ok {
		sb.Deleting = true
		copy := *sb
		return &copy, nil
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

func (s *testStore) ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	var result []*models.Songbook
	for _, id := range s.sbOrder {
		songbook := s.songbooks[id]
		if songbook.OwnerID == ownerID {
			result = append(result, songbook)
		}
	}
	return result, nil
}

func (s *testStore) CreateSong(ctx context.Context, song *models.Song) error {
	now := time.Now().UTC()
	song.CreatedAt = now
	song.UpdatedAt = now
	cp := *song
	return s.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: song.SongbookID,
		SongID:     song.ID,
		Song:       &cp,
		UpdatedAt:  now,
	})
}

func (s *testStore) PutCatalogChange(_ context.Context, change *models.CatalogChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if change.UpdatedAt.IsZero() {
		change.UpdatedAt = time.Now().UTC()
	}
	cp := *change
	if change.Song != nil {
		sc := *change.Song
		cp.Song = &sc
	}
	list := s.changes[change.SongbookID]
	replaced := false
	for i, existing := range list {
		if existing.SongID == change.SongID {
			list[i] = &cp
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, &cp)
	}
	s.changes[change.SongbookID] = list

	if cp.Clear {
		for id, song := range s.songs {
			if song.SongbookID == cp.SongbookID {
				delete(s.songs, id)
			}
		}
		delete(s.songOrder, cp.SongbookID)
	} else if cp.Deleted {
		delete(s.songs, cp.SongID)
	} else if cp.Song != nil {
		if _, exists := s.songs[cp.Song.ID]; !exists {
			s.songOrder[cp.SongbookID] = append(s.songOrder[cp.SongbookID], cp.Song.ID)
		}
		s.songs[cp.Song.ID] = cp.Song
	}
	return nil
}

func (s *testStore) ListCatalogChanges(_ context.Context, songbookID string) ([]*models.CatalogChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.changes[songbookID]
	out := make([]*models.CatalogChange, 0, len(list))
	for _, ch := range list {
		cp := *ch
		if ch.Song != nil {
			sc := *ch.Song
			cp.Song = &sc
		}
		out = append(out, &cp)
	}
	return out, nil
}

func (s *testStore) DeleteCatalogChange(_ context.Context, songbookID, songID string, _ time.Time) error {
	list := s.changes[songbookID]
	out := list[:0]
	for _, ch := range list {
		if ch.SongID != songID {
			out = append(out, ch)
		}
	}
	s.changes[songbookID] = out
	return nil
}

func (s *testStore) ClearCatalogChanges(_ context.Context, songbookID string) error {
	delete(s.changes, songbookID)
	return nil
}

func (s *testStore) UpdateSong(ctx context.Context, song *models.Song) error {
	if _, ok := s.songs[song.ID]; !ok {
		return context.DeadlineExceeded
	}
	s.songs[song.ID] = song
	return nil
}

func (s *testStore) GetSong(ctx context.Context, songID string) (*models.Song, error) {
	song, ok := s.songs[songID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return song, nil
}

func (s *testStore) ListSongsBySongbook(ctx context.Context, songbookID string) ([]*models.Song, error) {
	var result []*models.Song
	for _, id := range s.songOrder[songbookID] {
		if song, ok := s.songs[id]; ok {
			result = append(result, song)
		}
	}
	return result, nil
}

func (s *testStore) DeleteSong(ctx context.Context, songID string) error {
	if _, ok := s.songs[songID]; !ok {
		return context.DeadlineExceeded
	}
	delete(s.songs, songID)
	return nil
}

func (s *testStore) SearchSongs(ctx context.Context, songbookID, query string) ([]*models.Song, error) {
	var result []*models.Song
	queryLower := strings.ToLower(query)
	for _, id := range s.songOrder[songbookID] {
		if song, ok := s.songs[id]; ok {
			if strings.Contains(strings.ToLower(song.Artist), queryLower) ||
				strings.Contains(strings.ToLower(song.Title), queryLower) ||
				strings.Contains(strings.ToLower(song.SearchText), queryLower) {
				result = append(result, song)
			}
		}
	}
	return result, nil
}

func (s *testStore) BulkUpsertSongs(ctx context.Context, songs []*models.Song) error {
	for _, song := range songs {
		song.CreatedAt = time.Now()
		song.UpdatedAt = time.Now()

		existingID, exists := s.fingerprints[song.Fingerprint]
		if exists {
			existing := s.songs[existingID]
			existing.Artist = song.Artist
			existing.Title = song.Title
			existing.Genre = song.Genre
			existing.Disc = song.Disc
			existing.Source = song.Source
			existing.Location = song.Location
			existing.VideoURL = song.VideoURL
			existing.IsAvailable = song.IsAvailable
			existing.Label = song.Label
			existing.SearchText = song.SearchText
			existing.UpdatedAt = time.Now()
			continue
		}

		s.songs[song.ID] = song
		if song.Fingerprint != "" {
			s.fingerprints[song.Fingerprint] = song.ID
		}
		order, ok := s.songOrder[song.SongbookID]
		if !ok {
			order = []string{}
		}
		found := false
		for _, id := range order {
			if id == song.ID {
				found = true
				break
			}
		}
		if !found {
			order = append(order, song.ID)
		}
		s.songOrder[song.SongbookID] = order
	}
	return nil
}

func (s *testStore) MarkSongsUnavailable(ctx context.Context, songbookID string, activeFPs []string) (int, error) {
	marked := 0
	for _, id := range s.songOrder[songbookID] {
		song := s.songs[id]
		if song == nil {
			continue
		}
		if song.IsAvailable {
			marked++
			song.IsAvailable = false
		}
	}
	for _, fp := range activeFPs {
		for _, id := range s.songOrder[songbookID] {
			song := s.songs[id]
			if song != nil && song.Fingerprint == fp {
				song.IsAvailable = true
				marked--
			}
		}
	}
	return marked, nil
}

func (s *testStore) SyncSongbook(ctx context.Context, songbookID string, songs []*models.Song, retainedFingerprints []string, pruneMissing bool) (int, error) {
	if err := s.BulkUpsertSongs(ctx, songs); err != nil {
		return 0, err
	}
	if !pruneMissing {
		return 0, nil
	}
	return s.MarkSongsUnavailable(ctx, songbookID, retainedFingerprints)
}

func (s *testStore) CreateEvent(ctx context.Context, ev *models.Event) error {
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *testStore) GetEvent(ctx context.Context, id string) (*models.Event, error) {
	if ev, ok := s.events[id]; ok {
		cp := *ev
		return &cp, nil
	}
	return nil, context.DeadlineExceeded
}

func (s *testStore) GetEventByJoinCode(ctx context.Context, joinCode string) (*models.Event, error) {
	for _, ev := range s.events {
		if ev.JoinCode == joinCode {
			return ev, nil
		}
	}
	return nil, context.DeadlineExceeded
}

func (s *testStore) ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error) {
	res := make([]*models.Event, 0)
	for _, ev := range s.events {
		if ev.OwnerID == ownerID {
			res = append(res, ev)
		}
	}
	return res, nil
}

func (s *testStore) ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error) {
	res := make([]*models.Event, 0)
	for _, ev := range s.events {
		if ev.SongbookID == songbookID {
			res = append(res, ev)
		}
	}
	return res, nil
}

func (s *testStore) UpdateEvent(ctx context.Context, ev *models.Event) error {
	return s.UpdateEventWithVersion(ctx, ev, false)
}

func (s *testStore) UpdateEventWithVersion(ctx context.Context, ev *models.Event, bumpVersion bool) error {
	current, ok := s.events[ev.ID]
	if !ok {
		return os.ErrNotExist
	}
	if current.QueueVersion != ev.QueueVersion || current.MetadataVersion != ev.MetadataVersion || current.Restarting {
		return &store.QueueVersionConflictError{Current: current.QueueVersion, Expected: ev.QueueVersion}
	}
	ev.MetadataVersion++

	if bumpVersion {
		ev.QueueVersion++
	}
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *testStore) DeleteEvent(ctx context.Context, id string) error {
	delete(s.events, id)
	return nil
}

func (s *testStore) BeginEventDeletion(_ context.Context, _ string) error {
	return nil
}

func (s *testStore) ConfirmSongbookDeletion(_ context.Context, _ string) error {
	return nil
}

func (s *testStore) AbortSongbookDeletion(_ context.Context, _ string) error {
	return nil
}

var _ store.EventStore = (*testStore)(nil)

// newTestServer sets up a ServeMux with all routes so PathValue works correctly.
func newTestServer(st *testStore) (*http.ServeMux, *SongbookHandler) {
	h := &SongbookHandler{Songbooks: st, CatalogChanges: st, Events: st}
	mux := http.NewServeMux()

	// Songbooks
	mux.HandleFunc("POST /api/v1/songbooks", h.CreateSongbook)
	mux.HandleFunc("GET /api/v1/songbooks", h.ListSongbooks)
	mux.HandleFunc("GET /api/v1/songbooks/{id}", h.GetSongbook)
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}", h.UpdateSongbook)
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}", h.DeleteSongbook)

	// Songs
	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs", h.CreateSong)
	mux.HandleFunc("GET /api/v1/songbooks/{id}/songs", h.ListSongs)
	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs/batch-delete", h.BatchDeleteSongs)
	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs/clear", h.ClearSongs)
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}/songs/{songID}", h.UpdateSong)
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/songs/{songID}", h.DeleteSong)
	mux.HandleFunc("POST /api/v1/songbooks/{id}/parse-files", h.ParseFiles)

	return mux, h
}

func doRequest(t *testing.T, mux *http.ServeMux, method, url string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// -- Songbook CRUD contract tests --

func TestCreateSongbook_WithAllFields(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks", `{"venue_id":"venue-1","name":"Main Stage","is_public":true}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var sb models.Songbook
	json.NewDecoder(w.Body).Decode(&sb)

	if sb.Name != "Main Stage" || sb.VenueID != "venue-1" || !sb.IsPublic {
		t.Errorf("unexpected songbook fields: %+v", sb)
	}
	if sb.ID == "" {
		t.Error("expected generated ID")
	}
	if sb.CreatedAt.IsZero() || sb.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be set")
	}
}

func TestCreateSongbook_WithMinimalBody(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks", `{"name":"Minimal"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var sb models.Songbook
	json.NewDecoder(w.Body).Decode(&sb)

	if sb.Name != "Minimal" {
		t.Errorf("unexpected name: %q", sb.Name)
	}
}

func TestCreateSongbook_InvalidJSON(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks", `{invalid json`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestCreateSongbook_EmptyBody(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks", ``)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestListSongbooks_Empty(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks", nil)
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Songbooks []models.Songbook `json:"songbooks"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Songbooks) != 0 {
		t.Errorf("expected empty list, got %d items", len(result.Songbooks))
	}
}

func TestListSongbooks_WithMultipleSongbooks(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb1 := &models.Songbook{ID: "sb-1", Name: "Venue Alpha", VenueID: "v-1"}
	sb2 := &models.Songbook{ID: "sb-2", Name: "Venue Beta", VenueID: "v-2"}
	sb3 := &models.Songbook{ID: "sb-3", Name: "Venue Gamma", VenueID: "v-3", IsPublic: true}
	st.CreateSongbook(context.Background(), sb1)
	st.CreateSongbook(context.Background(), sb2)
	st.CreateSongbook(context.Background(), sb3)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks", nil)
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Songbooks []models.Songbook `json:"songbooks"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Songbooks) != 3 {
		t.Fatalf("expected 3 songbooks, got %d", len(result.Songbooks))
	}

	if result.Songbooks[0].Name != "Venue Alpha" || result.Songbooks[1].Name != "Venue Beta" || result.Songbooks[2].Name != "Venue Gamma" {
		t.Error("unexpected songbook order")
	}
}

func TestGetSongbook_Valid(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Main Room", IsPublic: true}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result models.Songbook
	json.NewDecoder(w.Body).Decode(&result)

	if result.ID != "sb-1" || result.Name != "Main Room" {
		t.Errorf("unexpected songbook: %+v", result)
	}
}

func TestGetSongbook_NotFound(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/nonexistent", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestGetSongbook_JSONContentType(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", PublicID: "public-1", Name: "Test", IsPublic: true}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1", "")

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %q", w.Header().Get("Content-Type"))
	}
}

func TestUpdateSongbook(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)
	st.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-1", Name: "Before", IsPublic: false})

	w := doRequest(t, mux, http.MethodPatch, "/api/v1/songbooks/sb-1", `{"name":"After","is_public":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	var songbook models.Songbook
	json.NewDecoder(w.Body).Decode(&songbook)
	if songbook.Name != "After" || !songbook.IsPublic {
		t.Errorf("unexpected update: %+v", songbook)
	}
}

func TestDeleteSongbook(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)
	st.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-1", Name: "Delete me"})

	w := doRequest(t, mux, http.MethodDelete, "/api/v1/songbooks/sb-1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", w.Code)
	}
	if _, err := st.GetSongbook(context.Background(), "sb-1"); err == nil {
		t.Error("expected songbook to be deleted")
	}
}

func TestDeleteSongbook_BlockedByLinkedEvent(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)
	st.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-1", Name: "Main Songbook"})
	st.CreateEvent(context.Background(), &models.Event{ID: "ev-1", SongbookID: "sb-1", Name: "Friday Karaoke", JoinCode: "FRIDAY"})

	// Attempting to delete songbook while event is linked must return 409 Conflict
	w := doRequest(t, mux, http.MethodDelete, "/api/v1/songbooks/sb-1", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected status 409 Conflict, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Friday Karaoke") {
		t.Errorf("expected response to mention linked event name, got: %s", w.Body.String())
	}

	// Verify songbook was NOT deleted
	if _, err := st.GetSongbook(context.Background(), "sb-1"); err != nil {
		t.Error("songbook should still exist while linked to event")
	}

	// Delete the linked event first
	st.DeleteEvent(context.Background(), "ev-1")

	// Now deletion must succeed
	w2 := doRequest(t, mux, http.MethodDelete, "/api/v1/songbooks/sb-1", "")
	if w2.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 No Content after event deletion, got %d", w2.Code)
	}
	if _, err := st.GetSongbook(context.Background(), "sb-1"); err == nil {
		t.Error("expected songbook to be deleted after events were removed")
	}
}

// -- Song CRUD contract tests --

func TestCreateSong_MinimalFields(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", PublicID: "public-1", Name: "Test", IsPublic: true}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{"artist":"Queen","title":"Bohemian Rhapsody"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	if song.SongbookID != "sb-1" {
		t.Errorf("expected songbook_id = sb-1, got %q", song.SongbookID)
	}
	if song.Artist != "Queen" {
		t.Errorf("expected artist = Queen, got %q", song.Artist)
	}
	if song.Title != "Bohemian Rhapsody" {
		t.Errorf("expected title = Bohemian Rhapsody, got %q", song.Title)
	}
	if song.Label != "Queen — Bohemian Rhapsody" {
		t.Errorf("expected auto-generated label, got %q", song.Label)
	}
	if !song.IsAvailable {
		t.Error("expected is_available = true by default")
	}
	if song.SearchText == "" {
		t.Error("expected auto-generated search_text")
	}
}

func TestCreateSong_WithOptionalFields(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", PublicID: "public-1", Name: "Test", IsPublic: true}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{
		"artist":"Adele",
		"title":"Rolling in the Deep",
		"genre":"Pop",
		"disc":"Disc 3",
		"source":"KaraokePro",
		"location":"/media/songs/adele_ritd.mp4",
		"video_url":"https://example.com/video",
		"fingerprint":"adele-disk3-hash123"
	}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	if song.Genre != "Pop" || song.Disc != "Disc 3" || song.Source != "KaraokePro" {
		t.Errorf("optional fields missing: %+v", song)
	}
	if song.Location != "/media/songs/adele_ritd.mp4" {
		t.Errorf("expected location path, got %q", song.Location)
	}
	if song.VideoURL != "https://example.com/video" {
		t.Errorf("expected video_url, got %q", song.VideoURL)
	}
	if song.Fingerprint != "adele-disk3-hash123" {
		t.Errorf("expected fingerprint, got %q", song.Fingerprint)
	}
}

func TestCreateSong_InvalidJSON(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test", IsPublic: true}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{not-json`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestListSongs_Empty(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/songs", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 0 {
		t.Errorf("expected empty list, got %d items", len(result))
	}
}

func TestListSongs_WithMultipleSongs(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	st.CreateSong(context.Background(), &models.Song{ID: "song-1", SongbookID: "sb-1", Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true})
	st.CreateSong(context.Background(), &models.Song{ID: "song-2", SongbookID: "sb-1", Artist: "Adele", Title: "Hello", IsAvailable: true})
	st.CreateSong(context.Background(), &models.Song{ID: "song-3", SongbookID: "sb-1", Artist: "Eagles", Title: "Hotel California", IsAvailable: false})

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/songs", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 3 {
		t.Fatalf("expected 3 songs, got %d", len(result))
	}
}

func TestListSongs_WithSearchQuery(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	st.CreateSong(context.Background(), &models.Song{ID: "s1", SongbookID: "sb-1", Artist: "Queen", Title: "Bohemian Rhapsody", SearchText: "queen bohemian rhapsody"})
	st.CreateSong(context.Background(), &models.Song{ID: "s2", SongbookID: "sb-1", Artist: "Adele", Title: "Hello", SearchText: "adele hello"})
	st.CreateSong(context.Background(), &models.Song{ID: "s3", SongbookID: "sb-1", Artist: "Beatles", Title: "Hey Jude", SearchText: "beatles hey jude"})

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/songs?q=queen", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 1 {
		t.Fatalf("expected 1 result for 'queen', got %d", len(result))
	}
	if result[0].Artist != "Queen" {
		t.Errorf("expected first result to be Queen, got %q", result[0].Artist)
	}
}

func TestListSongs_SearchCaseInsensitive(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	st.CreateSong(context.Background(), &models.Song{ID: "s1", SongbookID: "sb-1", Artist: "Queen", Title: "Bohemian Rhapsody", SearchText: "queen bohemian rhapsody"})

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/songs?q=QUEEN", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 1 {
		t.Fatalf("expected 1 result for 'QUEEN', got %d", len(result))
	}
}

func TestListSongs_SearchByTitle(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	st.CreateSong(context.Background(), &models.Song{ID: "s1", SongbookID: "sb-1", Artist: "Queen", Title: "Bohemian Rhapsody", SearchText: "queen bohemian rhapsody"})
	st.CreateSong(context.Background(), &models.Song{ID: "s2", SongbookID: "sb-1", Artist: "Eagles", Title: "Hotel California", SearchText: "eagles hotel california"})

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/songs?q=california", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 1 {
		t.Fatalf("expected 1 result for 'california', got %d", len(result))
	}
	if result[0].Artist != "Eagles" {
		t.Errorf("expected first result to be Eagles, got %q", result[0].Artist)
	}
}

func TestUpdateSong_PreservesOmittedFields(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)
	st.CreateSong(context.Background(), &models.Song{
		ID: "song-1", SongbookID: "sb-1", Artist: "Artist", Title: "Title",
		Genre: "Pop", Location: "/music/song.mp3", Fingerprint: "fp-1", IsAvailable: true,
	})

	w := doRequest(t, mux, http.MethodPatch, "/api/v1/songbooks/sb-1/songs/song-1", `{"genre":"Rock"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	updated := st.songs["song-1"]
	if updated.Artist != "Artist" || updated.Title != "Title" || updated.Location != "/music/song.mp3" || updated.Fingerprint != "fp-1" {
		t.Fatalf("partial update erased fields: %+v", updated)
	}
	if updated.Genre != "Rock" {
		t.Fatalf("genre = %q; want Rock", updated.Genre)
	}
}

func TestUpdateSongbook_CanPatchPublicationOnly(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)
	st.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-1", Name: "Main", IsPublic: false})

	w := doRequest(t, mux, http.MethodPatch, "/api/v1/songbooks/sb-1", `{"is_public":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if st.songbooks["sb-1"].Name != "Main" || !st.songbooks["sb-1"].IsPublic {
		t.Fatalf("unexpected songbook: %+v", st.songbooks["sb-1"])
	}
}

// -- Parse Files contract tests --

func TestParseFiles_ArtistTitle(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen - Bohemian Rhapsody.mp4", "path": "/media/Q1", "size": 9876543},
			{"name": "Adele \u2013 Hello.mp4", "path": "/media/A1", "size": 3456789}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}

	// Verify first result was parsed correctly
	if result.Results[0]["artist"] != "Queen" {
		t.Errorf("expected artist 'Queen', got %q", result.Results[0]["artist"])
	}
	if result.Results[0]["title"] != "Bohemian Rhapsody" {
		t.Errorf("expected title 'Bohemian Rhapsody', got %q", result.Results[0]["title"])
	}
}

func TestParseFiles_TitleArtist(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "title-artist",
		"files": [
			{"name": "Bohemian Rhapsody - Queen.mp4", "path": "/media/Q1", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0]["artist"] != "Queen" {
		t.Errorf("expected artist 'Queen', got %q", result.Results[0]["artist"])
	}
	if result.Results[0]["title"] != "Bohemian Rhapsody" {
		t.Errorf("expected title 'Bohemian Rhapsody', got %q", result.Results[0]["title"])
	}
}

func TestParseFiles_DiscArtistTitle(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "disc-artist-title",
		"files": [
			{"name": "D01 - Queen - Bohemian Rhapsody.mp4", "path": "/media/Q1", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0]["disc"] != "D01" {
		t.Errorf("expected disc 'D01', got %q", result.Results[0]["disc"])
	}
	if result.Results[0]["artist"] != "Queen" {
		t.Errorf("expected artist 'Queen', got %q", result.Results[0]["artist"])
	}
	if result.Results[0]["title"] != "Bohemian Rhapsody" {
		t.Errorf("expected title 'Bohemian Rhapsody', got %q", result.Results[0]["title"])
	}
}

func TestParseFiles_HandlesParentheticals(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen - Bohemian Rhapsody [Extended].mp4", "path": "/media/Q1", "size": 9876543},
			{"name": "Adele - Hello (Live).mp4", "path": "/media/A1", "size": 3456789}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}

	// Parentheticals should be stripped from titles
	title1 := result.Results[0]["title"].(string)
	title2 := result.Results[1]["title"].(string)
	if strings.Contains(title1, "[") || strings.Contains(title1, "]") {
		t.Errorf("expected brackets stripped from title, got %q", title1)
	}
	if strings.Contains(title2, "(") || strings.Contains(title2, ")") {
		t.Errorf("expected parens stripped from title, got %q", title2)
	}
}

func TestParseFiles_GeneratesConfidence(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen - Bohemian Rhapsody.mp4", "path": "/media/Q1", "size": 9876543},
			{"name": "Bohemian Rhapsody.mp4", "path": "/media/Q2", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}

	// First file has both artist and title -> high confidence
	conf1 := result.Results[0]["confidence"].(string)
	if conf1 != "high" {
		t.Errorf("expected 'high' confidence, got %q", conf1)
	}
	// Second file has no separator -> only title parsed -> low confidence
	conf2 := result.Results[1]["confidence"].(string)
	if conf2 != "low" {
		t.Errorf("expected 'low' confidence, got %q", conf2)
	}
}

func TestParseFiles_WithUnderscoreSeparator(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen_Bohemian Rhapsody.mp4", "path": "/media/Q1", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0]["artist"] != "Queen" {
		t.Errorf("expected artist 'Queen', got %q", result.Results[0]["artist"])
	}
}

func TestParseFiles_PrefersSpacedDelimiterAndNormalizesCleanup(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Third_Example - Underscore Separator.wav", "size": 1},
			{"name": "Second Example - Song With [Live] Note.cdg", "size": 1}
		]
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)
	if result.Results[0]["artist"] != "Third Example" || result.Results[0]["title"] != "Underscore Separator" {
		t.Errorf("underscore/hyphen parse = %#v", result.Results[0])
	}
	if result.Results[1]["title"] != "Song With Note" {
		t.Errorf("cleanup parse = %#v", result.Results[1])
	}
}

func TestParseFiles_GeneratesFingerprint(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen - Bohemian Rhapsody.mp4", "path": "/media/Q1", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	fp := result.Results[0]["fingerprint"].(string)
	if fp != "Queen - Bohemian Rhapsody.mp4-9876543" {
		t.Errorf("expected fingerprint 'filename-size', got %q", fp)
	}
}

func TestParseFiles_InvalidJSON(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{bad`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

// -- Edge cases --

func TestLabelGeneration_EmptyArtist(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{"title":"Unknown Artist Song"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	if song.Label != "Unknown Artist Song" {
		t.Errorf("expected label to be just title when artist is empty, got %q", song.Label)
	}
}

func TestLabelGeneration_EmptyTitle(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{"artist":"Queen"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	if song.Label != "Queen" {
		t.Errorf("expected label to be just artist when title is empty, got %q", song.Label)
	}
}

func TestLabelGeneration_BothEmpty(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	if song.Label != "" {
		t.Errorf("expected empty label when both artist and title are empty, got %q", song.Label)
	}
}

func TestSearchText_Generation(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs", `{
		"artist": "Queen",
		"title": "Bohemian Rhapsody",
		"disc": "Disc 1",
		"location": "/media/queen-br.mp4"
	}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	var song models.Song
	json.NewDecoder(w.Body).Decode(&song)

	// Search text should contain artist, title, disc, label, location (lowercased, space-separated)
	if !strings.Contains(song.SearchText, "queen") {
		t.Errorf("search_text should contain 'queen', got: %q", song.SearchText)
	}
	if !strings.Contains(song.SearchText, "bohemian rhapsody") {
		t.Errorf("search_text should contain 'bohemian rhapsody', got: %q", song.SearchText)
	}
}

func TestCreateSongbook_JSONContentType(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks", `{"name":"Test"}`)

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %q", w.Header().Get("Content-Type"))
	}
}

func TestListSongbooks_JSONContentType(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks", nil)
	mux.ServeHTTP(w, req)

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %q", w.Header().Get("Content-Type"))
	}
}

func TestSearch_MultipleWords(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	sb := &models.Songbook{ID: "sb-1", Name: "Test"}
	st.CreateSongbook(context.Background(), sb)

	st.CreateSong(context.Background(), &models.Song{ID: "s1", SongbookID: "sb-1", Artist: "Queen", Title: "Bohemian Rhapsody", SearchText: "queen bohemian rhapsody queen — bohemian rhapsody"})
	st.CreateSong(context.Background(), &models.Song{ID: "s2", SongbookID: "sb-1", Artist: "Queen", Title: "Another One Bites the Dust", SearchText: "queen another one bites the dust"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/sb-1/songs?q=bohemian+rhapsody", nil)
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result []models.Song
	json.NewDecoder(w.Body).Decode(&result)

	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
}

func TestParseFiles_FallbackLocationWhenPathEmpty(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": [
			{"name": "Queen - Bohemian Rhapsody.mp4", "size": 9876543}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	loc := result.Results[0]["location"].(string)
	if loc != "Queen - Bohemian Rhapsody.mp4" {
		t.Errorf("expected location fallback to filename, got %q", loc)
	}
}

func TestParseFiles_EmptyFilesArray(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/parse-files", `{
		"pattern": "artist-title",
		"files": []
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.NewDecoder(w.Body).Decode(&result)

	if len(result.Results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(result.Results))
	}
}

func TestBatchDeleteSongs(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	st.songbooks["sb-1"] = &models.Songbook{ID: "sb-1", Name: "Test SB"}
	_ = st.CreateSong(context.Background(), &models.Song{ID: "s-1", SongbookID: "sb-1", Artist: "Artist 1", Title: "Title 1"})
	_ = st.CreateSong(context.Background(), &models.Song{ID: "s-2", SongbookID: "sb-1", Artist: "Artist 2", Title: "Title 2"})
	_ = st.CreateSong(context.Background(), &models.Song{ID: "s-3", SongbookID: "sb-1", Artist: "Artist 3", Title: "Title 3"})

	payload := `{"song_ids":["s-1","s-3"]}`
	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs/batch-delete", payload)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Deleted int `json:"deleted"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", resp.Deleted)
	}

	remaining, _ := st.ListSongsBySongbook(context.Background(), "sb-1")
	if len(remaining) != 1 || remaining[0].ID != "s-2" {
		t.Fatalf("expected only s-2 to remain, got %v", remaining)
	}
}

func TestClearSongs(t *testing.T) {
	st := newTestStore()
	mux, _ := newTestServer(st)

	st.songbooks["sb-1"] = &models.Songbook{ID: "sb-1", Name: "Test SB"}
	_ = st.CreateSong(context.Background(), &models.Song{ID: "c-1", SongbookID: "sb-1", Artist: "Artist 1", Title: "Title 1"})
	_ = st.CreateSong(context.Background(), &models.Song{ID: "c-2", SongbookID: "sb-1", Artist: "Artist 2", Title: "Title 2"})

	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/songs/clear", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Cleared bool `json:"cleared"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Cleared {
		t.Fatalf("expected cleared=true, got %+v", resp)
	}

	remaining, _ := st.ListSongsBySongbook(context.Background(), "sb-1")
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining songs, got %d", len(remaining))
	}
}

type recordingImportQueue struct {
	jobs []importjob.Job
}

func (q *recordingImportQueue) Enqueue(_ context.Context, job importjob.Job) error {
	q.jobs = append(q.jobs, job)
	return nil
}

type fakeManifestWriter struct {
	putKeys map[string][]byte
}

func (w *fakeManifestWriter) Put(_ context.Context, key string, data []byte) error {
	if w.putKeys == nil {
		w.putKeys = make(map[string][]byte)
	}
	w.putKeys[key] = data
	return nil
}

func (w *fakeManifestWriter) Presign(_ context.Context, key string, _ int64) (string, error) {
	return "https://upload.example.com/" + key, nil
}

type fakeImportJobStore struct {
	jobs map[string]*models.ImportJob
}

func (f *fakeImportJobStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportJobStore) GetImportJob(_ context.Context, id string) (*models.ImportJob, error) {
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return nil, context.DeadlineExceeded
}
func (f *fakeImportJobStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeImportJobStore) DeleteImportJob(_ context.Context, id string) error { return nil }
func (f *fakeImportJobStore) UpdateImportJob(_ context.Context, job *models.ImportJob) error {
	if existing, ok := f.jobs[job.ID]; ok {
		if job.Generation == 0 && existing.Generation > 0 {
			job.Generation = existing.Generation
		}
	}
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportJobStore) LatestImportJob(_ context.Context, sbID string) (*models.ImportJob, error) {
	for _, j := range f.jobs {
		if j.SongbookID == sbID {
			return j, nil
		}
	}
	return nil, os.ErrNotExist
}
func (f *fakeImportJobStore) AssignImportJobGeneration(_ context.Context, jobID string, generation int64) (int64, error) {
	if j, ok := f.jobs[jobID]; ok {
		if j.Generation > 0 {
			return j.Generation, nil
		}
		j.Generation = generation
		return generation, nil
	}
	return 0, os.ErrNotExist
}

func (f *fakeImportJobStore) MarkImportJobDispatchReady(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status == models.ImportJobProcessing || j.DispatchedAt != nil {
		return store.ErrImportJobAlreadyDispatched
	}
	j.DispatchReady = true
	j.Status = models.ImportJobQueued
	return nil
}

func (f *fakeImportJobStore) ClaimImportJobDispatch(_ context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, "", store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, "", store.ErrImportJobTerminal
	}
	if j.DispatchedAt != nil {
		return nil, "", store.ErrImportJobAlreadyDispatched
	}
	if !j.DispatchReady {
		return nil, "", store.ErrImportJobNotReady
	}
	token := "claim-token-" + jobID
	exp := time.Now().Add(leaseDuration)
	j.DispatchClaimToken = token
	j.DispatchClaimExpiresAt = &exp
	j.DispatchAttempts++
	return j, token, nil
}

func (f *fakeImportJobStore) ConfirmImportJobDispatch(_ context.Context, jobID string, claimToken string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.DispatchedAt = &now
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportJobStore) RecordDispatchError(_ context.Context, jobID string, claimToken string, errMsg string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	j.Error = errMsg
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportJobStore) ListDispatchableImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts < maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportJobStore) ClaimImportJobProcessing(_ context.Context, jobID string) (*models.ImportJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, store.ErrImportJobTerminal
	}
	j.Status = models.ImportJobProcessing
	if j.DispatchedAt == nil {
		now := time.Now().UTC()
		j.DispatchedAt = &now
	}
	return j, nil
}

func (f *fakeImportJobStore) FailImportJob(_ context.Context, jobID string, reason string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	j.Status = models.ImportJobFailed
	j.Error = reason
	return nil
}

func (f *fakeImportJobStore) ListExhaustedImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts >= maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportJobStore) UpdateImportJobProgress(_ context.Context, jobID string, processedSongs int) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status != models.ImportJobProcessing {
		return os.ErrNotExist
	}
	j.ProcessedSongs = processedSongs
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeImportJobStore) ResetImportJobDispatch(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if !j.DispatchReady {
		return store.ErrImportJobNotReady
	}
	if j.DispatchedAt != nil || j.Status == models.ImportJobProcessing {
		return store.ErrImportJobAlreadyDispatched
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.DispatchClaimExpiresAt != nil && j.DispatchClaimExpiresAt.After(time.Now().UTC()) {
		return store.ErrImportJobClaimConflict
	}
	j.DispatchAttempts = 0
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	j.Error = ""
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func TestCreateImportJob_PruneMissingFalse(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-1"] = &models.Songbook{ID: "sb-1", Name: "Test SB"}
	importQueue := &recordingImportQueue{}
	importStore := &fakeImportJobStore{jobs: make(map[string]*models.ImportJob)}
	manifestWriter := &fakeManifestWriter{}

	h := &SongbookHandler{
		Songbooks:      st,
		ImportJobs:     importStore,
		ImportQueue:    importQueue,
		ManifestWriter: manifestWriter,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/songbooks/{id}/imports", h.CreateImportJob)

	body := `{"songs":[{"artist":"Queen","title":"Bohemian Rhapsody","fingerprint":"fp-queen"}],"prune_missing":false}`
	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/imports", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if len(importQueue.jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(importQueue.jobs))
	}
	enqueued := importQueue.jobs[0]
	if enqueued.PruneMissing {
		t.Errorf("expected enqueued job PruneMissing to be false, got true")
	}

	job := importStore.jobs[enqueued.ID]
	if job == nil {
		t.Fatalf("expected import job in store for ID %q", enqueued.ID)
	}
	if job.PruneMissing {
		t.Errorf("expected stored job PruneMissing to be false, got true")
	}

	// Verify the manifest stored in manifestWriter has prune_missing: false
	manifestBytes := manifestWriter.putKeys[enqueued.ManifestKey]
	if !strings.Contains(string(manifestBytes), `"prune_missing":false`) {
		t.Errorf("expected manifest to contain prune_missing:false, got: %s", string(manifestBytes))
	}
}

func TestCreateImportJob_PruneMissingTrue(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-1"] = &models.Songbook{ID: "sb-1", Name: "Test SB"}
	importQueue := &recordingImportQueue{}
	importStore := &fakeImportJobStore{jobs: make(map[string]*models.ImportJob)}
	manifestWriter := &fakeManifestWriter{}

	h := &SongbookHandler{
		Songbooks:      st,
		ImportJobs:     importStore,
		ImportQueue:    importQueue,
		ManifestWriter: manifestWriter,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/songbooks/{id}/imports", h.CreateImportJob)

	body := `{"songs":[{"artist":"Queen","title":"Bohemian Rhapsody","fingerprint":"fp-queen"}],"prune_missing":true}`
	w := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/imports", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if len(importQueue.jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(importQueue.jobs))
	}
	enqueued := importQueue.jobs[0]
	if !enqueued.PruneMissing {
		t.Errorf("expected enqueued job PruneMissing to be true, got false")
	}

	job := importStore.jobs[enqueued.ID]
	if !job.PruneMissing {
		t.Errorf("expected stored job PruneMissing to be true, got false")
	}
}

func TestCompleteImportUpload_PreservesPruneMissing(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-1"] = &models.Songbook{ID: "sb-1", Name: "Test SB"}
	importQueue := &recordingImportQueue{}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-false": {
				ID:           "job-false",
				SongbookID:   "sb-1",
				ManifestKey:  "imports/job-false.json",
				Status:       models.ImportJobQueued,
				PruneMissing: false,
			},
			"job-true": {
				ID:           "job-true",
				SongbookID:   "sb-1",
				ManifestKey:  "imports/job-true.json",
				Status:       models.ImportJobQueued,
				PruneMissing: true,
			},
		},
	}

	h := &SongbookHandler{
		Songbooks:   st,
		ImportJobs:  importStore,
		ImportQueue: importQueue,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/songbooks/{id}/import-jobs/{jobID}/complete", h.CompleteImportUpload)

	// Complete job with PruneMissing = false
	w1 := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/import-jobs/job-false/complete", "")
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w1.Code, w1.Body.String())
	}
	if len(importQueue.jobs) != 1 || importQueue.jobs[0].PruneMissing {
		t.Fatalf("expected enqueued job with PruneMissing=false, got: %+v", importQueue.jobs)
	}

	// Complete job with PruneMissing = true
	w2 := doRequest(t, mux, http.MethodPost, "/api/v1/songbooks/sb-1/import-jobs/job-true/complete", "")
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if len(importQueue.jobs) != 2 || !importQueue.jobs[1].PruneMissing {
		t.Fatalf("expected second enqueued job with PruneMissing=true, got: %+v", importQueue.jobs)
	}
}

type fakeCatalogStoreForHandlerTest struct {
	versions map[string]*models.CatalogVersion
}

func (f *fakeCatalogStoreForHandlerTest) CreateCatalogVersion(_ context.Context, v *models.CatalogVersion) error {
	f.versions[v.ID] = v
	return nil
}
func (f *fakeCatalogStoreForHandlerTest) GetCatalogVersion(_ context.Context, id string) (*models.CatalogVersion, error) {
	if v, ok := f.versions[id]; ok {
		return v, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeCatalogStoreForHandlerTest) MarkCatalogVersionReady(_ context.Context, id, prefix string, count int, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStoreForHandlerTest) FailCatalogVersion(_ context.Context, id, reason string, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStoreForHandlerTest) ReclaimCatalogVersion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStoreForHandlerTest) PublishCatalogVersion(_ context.Context, sbID, vID string, _ ...store.PublicationOptions) error {
	return nil
}
func (f *fakeCatalogStoreForHandlerTest) DeleteCatalogVersion(_ context.Context, id string, _ ...string) error {
	return nil
}
func (f *fakeCatalogStoreForHandlerTest) ReserveCatalogVersionForDeletion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStoreForHandlerTest) ReleaseCatalogVersionReservation(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeCatalogStoreForHandlerTest) IsCatalogVersionReferenced(_ context.Context, _ string, _ ...string) (bool, error) {
	return false, nil
}
func (f *fakeCatalogStoreForHandlerTest) PurgeCatalogVersion(_ context.Context, _ string, _ func(string) error, _ ...store.PurgeCatalogOptions) error {
	return nil
}
func (f *fakeCatalogStoreForHandlerTest) PurgeOrphanCatalogPrefix(_ context.Context, _ string, _ func(string) error, _ ...store.PurgeCatalogOptions) error {
	return nil
}

type fakeCatalogChangeStoreForHandlerTest struct {
	changes map[string][]*models.CatalogChange
}

func (f *fakeCatalogChangeStoreForHandlerTest) PutCatalogChange(_ context.Context, change *models.CatalogChange) error {
	f.changes[change.SongbookID] = append(f.changes[change.SongbookID], change)
	return nil
}
func (f *fakeCatalogChangeStoreForHandlerTest) ListCatalogChanges(_ context.Context, sbID string) ([]*models.CatalogChange, error) {
	return f.changes[sbID], nil
}
func (f *fakeCatalogChangeStoreForHandlerTest) DeleteCatalogChange(_ context.Context, sbID, sID string, _ time.Time) error {
	return nil
}

func TestDeleteSong_Success(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-del"] = &models.Songbook{ID: "sb-del", Name: "Del SB", OwnerID: "user-1"}
	_ = st.CreateSong(context.Background(), &models.Song{ID: "song-del-1", SongbookID: "sb-del", Title: "Song 1", Artist: "Artist 1"})

	h := &SongbookHandler{Songbooks: st, CatalogChanges: st}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/songs/{songID}", h.DeleteSong)

	w := doRequest(t, mux, http.MethodDelete, "/api/v1/songbooks/sb-del/songs/song-del-1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := st.songs["song-del-1"]; ok {
		t.Fatalf("expected song-del-1 to be deleted from store")
	}
}

func TestDeleteSong_NotFound(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-del"] = &models.Songbook{ID: "sb-del", Name: "Del SB", OwnerID: "user-1"}

	h := &SongbookHandler{Songbooks: st, CatalogChanges: st}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/songs/{songID}", h.DeleteSong)

	w := doRequest(t, mux, http.MethodDelete, "/api/v1/songbooks/sb-del/songs/song-nonexistent", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", w.Code)
	}
}

func TestGetImportJob_Success(t *testing.T) {
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-123": {ID: "job-123", SongbookID: "sb-1", Status: models.ImportJobCompleted},
		},
	}
	h := &SongbookHandler{ImportJobs: importStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/import-jobs/{jobID}", h.GetImportJob)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-1/import-jobs/job-123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var job models.ImportJob
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if job.ID != "job-123" || job.Status != models.ImportJobCompleted {
		t.Errorf("unexpected job: %+v", job)
	}
}

func TestGetLatestImportJob_Success(t *testing.T) {
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-latest": {ID: "job-latest", SongbookID: "sb-latest-sb", Status: models.ImportJobQueued},
		},
	}
	h := &SongbookHandler{ImportJobs: importStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/import-jobs/latest", h.GetLatestImportJob)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-latest-sb/import-jobs/latest", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var job models.ImportJob
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if job.ID != "job-latest" {
		t.Errorf("expected job-latest, got %s", job.ID)
	}
}

func TestGetManagedManifest_Ready(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-manifest"] = &models.Songbook{
		ID:               "sb-manifest",
		OwnerID:          "host-owner",
		CatalogVersionID: "cat-ver-1",
	}
	catStore := &fakeCatalogStoreForHandlerTest{
		versions: map[string]*models.CatalogVersion{
			"cat-ver-1": {
				ID:          "cat-ver-1",
				Status:      models.CatalogVersionReady,
				SongCount:   1500,
				AssetPrefix: "catalogs/cat-ver-1",
			},
		},
	}
	h := &SongbookHandler{Songbooks: st, Catalogs: catStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/managed-manifest", h.GetManagedManifest)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/sb-manifest/managed-manifest", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-owner"}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res["static_ready"] != true || res["total"] != float64(1500) {
		t.Errorf("unexpected response: %+v", res)
	}
}

func TestGetManagedManifest_NotReady(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-no-cat"] = &models.Songbook{
		ID:      "sb-no-cat",
		OwnerID: "host-owner",
	}
	h := &SongbookHandler{Songbooks: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/managed-manifest", h.GetManagedManifest)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/sb-no-cat/managed-manifest", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-owner"}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res["static_ready"] != false {
		t.Errorf("expected static_ready false, got %+v", res)
	}
}

func TestGetCatalogOverrides_Success(t *testing.T) {
	changesStore := &fakeCatalogChangeStoreForHandlerTest{
		changes: map[string][]*models.CatalogChange{
			"sb-changes": {
				{SongbookID: "sb-changes", SongID: "s-1", Deleted: false},
				{SongbookID: "sb-changes", SongID: "s-2", Deleted: true},
			},
		},
	}
	h := &SongbookHandler{CatalogChanges: changesStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/catalog-overrides", h.GetCatalogOverrides)

	w := doRequest(t, mux, http.MethodGet, "/api/v1/songbooks/sb-changes/catalog-overrides", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []*models.CatalogChange
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(list))
	}
}

func TestSongbookSlug_CreateAndValidation(t *testing.T) {
	st := newTestStore()
	h := &SongbookHandler{Songbooks: st}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/songbooks", h.CreateSongbook)

	// Valid creation with uppercase slug -> normalized to lowercase
	body := `{"name":"Dave's Karaoke","slug":"DAVEMIC"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/songbooks", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "user-1"}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created models.Songbook
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if created.Slug != "davemic" {
		t.Errorf("expected slug 'davemic', got %q", created.Slug)
	}

	// Duplicate slug should return 409 Conflict
	reqDup := httptest.NewRequest(http.MethodPost, "/api/v1/songbooks", strings.NewReader(`{"name":"Another Book","slug":"davemic"}`))
	reqDup = reqDup.WithContext(context.WithValue(reqDup.Context(), userContextKey, &models.User{ID: "user-2"}))
	wDup := httptest.NewRecorder()
	mux.ServeHTTP(wDup, reqDup)
	if wDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate slug, got %d", wDup.Code)
	}

	// Reserved slug should return 400 Bad Request
	reqRes := httptest.NewRequest(http.MethodPost, "/api/v1/songbooks", strings.NewReader(`{"name":"Admin Book","slug":"admin"}`))
	reqRes = reqRes.WithContext(context.WithValue(reqRes.Context(), userContextKey, &models.User{ID: "user-1"}))
	wRes := httptest.NewRecorder()
	mux.ServeHTTP(wRes, reqRes)
	if wRes.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for reserved slug, got %d", wRes.Code)
	}

	// Invalid slug format (too short, or special characters)
	reqInv := httptest.NewRequest(http.MethodPost, "/api/v1/songbooks", strings.NewReader(`{"name":"Invalid Book","slug":"hi!"}`))
	reqInv = reqInv.WithContext(context.WithValue(reqInv.Context(), userContextKey, &models.User{ID: "user-1"}))
	wInv := httptest.NewRecorder()
	mux.ServeHTTP(wInv, reqInv)
	if wInv.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid slug format, got %d", wInv.Code)
	}
}

func TestSongbookSlug_Update(t *testing.T) {
	st := newTestStore()
	sb := &models.Songbook{
		ID:       "sb-slug-1",
		OwnerID:  "user-1",
		PublicID: "pub-1",
		Slug:     "old-slug",
		Name:     "Old Name",
		IsPublic: true,
	}
	_ = st.CreateSongbook(context.Background(), sb)

	h := &SongbookHandler{Songbooks: st}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}", h.UpdateSongbook)

	// Update slug successfully
	body := `{"slug":"new-slug"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/songbooks/sb-slug-1", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.Songbook
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if updated.Slug != "new-slug" {
		t.Errorf("expected slug 'new-slug', got %q", updated.Slug)
	}

	// Retrieve by new slug via public endpoint
	pub, err := st.GetPublicSongbook(context.Background(), "NEW-SLUG")
	if err != nil || pub.ID != sb.ID {
		t.Fatalf("expected to retrieve by slug 'NEW-SLUG', got err %v", err)
	}
}

func TestGetPublicManifest_TemporaryPublicViaActiveEvent(t *testing.T) {
	st := newTestStore()
	st.songbooks["sb-priv-1"] = &models.Songbook{
		ID:               "sb-priv-1",
		PublicID:         "pub-priv-1",
		Slug:             "privateshow",
		Name:             "Secret Club",
		IsPublic:         false,
		CatalogVersionID: "cat-ver-priv",
	}
	st.events["evt-active-1"] = &models.Event{
		ID:         "evt-active-1",
		JoinCode:   "SECRET1",
		SongbookID: "sb-priv-1",
		Status:     models.EventStatusActive,
	}
	st.events["evt-ended-1"] = &models.Event{
		ID:         "evt-ended-1",
		JoinCode:   "OLDSHOW",
		SongbookID: "sb-priv-1",
		Status:     models.EventStatusEnded,
	}
	catStore := &fakeCatalogStoreForHandlerTest{
		versions: map[string]*models.CatalogVersion{
			"cat-ver-priv": {
				ID:          "cat-ver-priv",
				Status:      models.CatalogVersionReady,
				SongCount:   50,
				AssetPrefix: "catalogs/priv",
			},
		},
	}
	h := &SongbookHandler{Songbooks: st, Events: st, Catalogs: catStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/songbooks/{id}/public-manifest", h.GetPublicManifest)

	// 1. Without event code, private songbook returns 404
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/pub-priv-1/public-manifest", nil))
	if w1.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for private songbook without event code, got %d", w1.Code)
	}

	// 2. With active join code (?code=SECRET1), private songbook returns 200 OK (temporarily public)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/pub-priv-1/public-manifest?code=SECRET1", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with active event join code, got %d: %s", w2.Code, w2.Body.String())
	}
	var res2 map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res2["static_ready"] != true || res2["name"] != "Secret Club" {
		t.Fatalf("unexpected manifest response: %+v", res2)
	}

	// 3. With active event ID (?event=evt-active-1), private songbook returns 200 OK
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/pub-priv-1/public-manifest?event=evt-active-1", nil))
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 with active event ID, got %d: %s", w3.Code, w3.Body.String())
	}

	// 4. With ended join code (?code=OLDSHOW), private songbook returns 404 (temporary access ended)
	w4 := httptest.NewRecorder()
	mux.ServeHTTP(w4, httptest.NewRequest(http.MethodGet, "/api/v1/songbooks/pub-priv-1/public-manifest?code=OLDSHOW", nil))
	if w4.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ended event code, got %d", w4.Code)
	}
}
