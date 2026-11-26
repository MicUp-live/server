package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type privacyMockSongbookStore struct {
	store.SongbookStore
	books        []*models.Songbook
	deletedBooks []string
	listErr      error
	deleteErr    error
}

func (m *privacyMockSongbookStore) CreateSongbook(_ context.Context, sb *models.Songbook) error {
	m.books = append(m.books, sb)
	return nil
}
func (m *privacyMockSongbookStore) UpdateSongbook(_ context.Context, _ *models.Songbook) error {
	return nil
}
func (m *privacyMockSongbookStore) DeleteSongbook(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedBooks = append(m.deletedBooks, id)
	remaining := make([]*models.Songbook, 0, len(m.books))
	for _, b := range m.books {
		if b.ID != id {
			remaining = append(remaining, b)
		}
	}
	m.books = remaining
	return nil
}
func (m *privacyMockSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	for _, b := range m.books {
		if b.ID == id {
			return b, nil
		}
	}
	return nil, nil
}
func (m *privacyMockSongbookStore) GetPublicSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (m *privacyMockSongbookStore) GetSongbookBySlug(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (m *privacyMockSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.books, nil
}
func (m *privacyMockSongbookStore) ListSongbooksByOwner(_ context.Context, ownerID string) ([]*models.Songbook, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var res []*models.Songbook
	for _, b := range m.books {
		if b.OwnerID == ownerID {
			res = append(res, b)
		}
	}
	return res, nil
}
func (m *privacyMockSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}
func (m *privacyMockSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	for _, b := range m.books {
		if b.ID == id {
			b.Deleting = true
			copy := *b
			return &copy, nil
		}
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type privacyMockPasskeyStore struct {
	creds           []*models.PasskeyCredential
	deletedPasskeys []string
	listErr         error
	deleteErr       error
}

func (m *privacyMockPasskeyStore) SavePasskeyCredential(_ context.Context, c *models.PasskeyCredential) error {
	m.creds = append(m.creds, c)
	return nil
}
func (m *privacyMockPasskeyStore) GetPasskeyCredential(_ context.Context, _ string) (*models.PasskeyCredential, error) {
	return nil, nil
}
func (m *privacyMockPasskeyStore) ListPasskeyCredentialsForUser(_ context.Context, userID string) ([]*models.PasskeyCredential, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var res []*models.PasskeyCredential
	for _, c := range m.creds {
		if c.UserID == userID {
			res = append(res, c)
		}
	}
	return res, nil
}
func (m *privacyMockPasskeyStore) RecordPasskeyUse(_ context.Context, _ *models.PasskeyCredential, _ store.PasskeyUsage) error {
	panic("unexpected passkey login")
}
func (m *privacyMockPasskeyStore) DeletePasskeyCredential(_ context.Context, id, _ string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedPasskeys = append(m.deletedPasskeys, id)
	return nil
}
func (m *privacyMockPasskeyStore) SaveWebAuthnSession(_ context.Context, _ *models.WebAuthnSession) error {
	return nil
}
func (m *privacyMockPasskeyStore) ConsumeWebAuthnSession(_ context.Context, _ string) (*models.WebAuthnSession, error) {
	return nil, nil
}
func (m *privacyMockPasskeyStore) CreateUserWithPasskey(_ context.Context, _ *models.User, _ *models.PasskeyCredential) error {
	return nil
}

type privacyMockEventStore struct {
	store.EventStore
	events        []*models.Event
	deletedEvents []string
	listErr       error
	deleteErr     error
}

func (m *privacyMockEventStore) CreateEvent(_ context.Context, ev *models.Event) error {
	m.events = append(m.events, ev)
	return nil
}
func (m *privacyMockEventStore) UpdateEvent(_ context.Context, _ *models.Event) error {
	return nil
}
func (m *privacyMockEventStore) UpdateEventWithVersion(_ context.Context, _ *models.Event, _ bool) error {
	return nil
}
func (m *privacyMockEventStore) GetEvent(_ context.Context, id string) (*models.Event, error) {
	for _, e := range m.events {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, nil
}
func (m *privacyMockEventStore) GetPublicEvent(_ context.Context, _ string) (*models.Event, error) {
	return nil, nil
}
func (m *privacyMockEventStore) GetEventByJoinCode(_ context.Context, _ string) (*models.Event, error) {
	return nil, nil
}
func (m *privacyMockEventStore) GetEventByOpenKJKey(_ context.Context, _ string) (*models.Event, error) {
	return nil, nil
}
func (m *privacyMockEventStore) ListEventsByOwner(_ context.Context, ownerID string) ([]*models.Event, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var res []*models.Event
	for _, e := range m.events {
		if e.OwnerID == ownerID {
			res = append(res, e)
		}
	}
	return res, nil
}
func (m *privacyMockEventStore) ListEventsBySongbook(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}
func (m *privacyMockEventStore) DeleteEvent(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedEvents = append(m.deletedEvents, id)
	return nil
}
func (m *privacyMockEventStore) BeginEventDeletion(_ context.Context, _ string) error {
	return nil
}

type privacyMockRequestStore struct {
	store.RequestStore
	requests        []*models.SongRequest
	deletedRequests []string
	deleteErr       error
}

func (m *privacyMockRequestStore) CreateRequest(_ context.Context, req *models.SongRequest) error {
	m.requests = append(m.requests, req)
	return nil
}
func (m *privacyMockRequestStore) GetRequest(_ context.Context, _ string) (*models.SongRequest, error) {
	return nil, nil
}
func (m *privacyMockRequestStore) ListRequestsByEvent(_ context.Context, eventID string) ([]*models.SongRequest, error) {
	var res []*models.SongRequest
	for _, r := range m.requests {
		if r.EventID == eventID {
			res = append(res, r)
		}
	}
	return res, nil
}
func (m *privacyMockRequestStore) UpdateRequest(_ context.Context, _ *models.SongRequest) error {
	return nil
}
func (m *privacyMockRequestStore) DeleteRequest(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedRequests = append(m.deletedRequests, id)
	return nil
}
func (m *privacyMockRequestStore) DeleteRequestsByEvent(_ context.Context, _ string) error {
	return nil
}
func (m *privacyMockRequestStore) ReorderRequests(_ context.Context, _ string, _ []string) error {
	return nil
}

type privacyMockCatalogChangeStore struct {
	changes        map[string][]*models.CatalogChange
	deletedChanges []string
	listErr        error
	deleteErr      error
}

func (m *privacyMockCatalogChangeStore) PutCatalogChange(_ context.Context, change *models.CatalogChange) error {
	if m.changes == nil {
		m.changes = make(map[string][]*models.CatalogChange)
	}
	m.changes[change.SongbookID] = append(m.changes[change.SongbookID], change)
	return nil
}
func (m *privacyMockCatalogChangeStore) ListCatalogChanges(_ context.Context, songbookID string) ([]*models.CatalogChange, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.changes[songbookID], nil
}
func (m *privacyMockCatalogChangeStore) DeleteCatalogChange(_ context.Context, _, songID string, _ time.Time) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedChanges = append(m.deletedChanges, songID)
	return nil
}

type privacyMockImportJobStore struct {
	jobs        []*models.ImportJob
	deletedJobs []string
	deleteErr   error
}

func (m *privacyMockImportJobStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	m.jobs = append(m.jobs, job)
	return nil
}
func (m *privacyMockImportJobStore) GetImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (m *privacyMockImportJobStore) UpdateImportJob(_ context.Context, _ *models.ImportJob) error {
	return nil
}
func (m *privacyMockImportJobStore) DeleteImportJob(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedJobs = append(m.deletedJobs, id)
	return nil
}
func (m *privacyMockImportJobStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return m.jobs, nil
}
func (m *privacyMockImportJobStore) LatestImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (m *privacyMockImportJobStore) AssignImportJobGeneration(_ context.Context, _ string, gen int64) (int64, error) {
	return gen, nil
}
func (m *privacyMockImportJobStore) MarkImportJobDispatchReady(_ context.Context, _ string) error {
	return nil
}
func (m *privacyMockImportJobStore) ClaimImportJobDispatch(_ context.Context, _ string, _ time.Duration) (*models.ImportJob, string, error) {
	return nil, "mock-token", nil
}
func (m *privacyMockImportJobStore) ConfirmImportJobDispatch(_ context.Context, _, _ string) error {
	return nil
}
func (m *privacyMockImportJobStore) RecordDispatchError(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *privacyMockImportJobStore) ListDispatchableImportJobs(_ context.Context, _ int) ([]*models.ImportJob, error) {
	return nil, nil
}
func (m *privacyMockImportJobStore) ClaimImportJobProcessing(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (m *privacyMockImportJobStore) FailImportJob(_ context.Context, _, _ string) error {
	return nil
}
func (m *privacyMockImportJobStore) ListExhaustedImportJobs(_ context.Context, _ int) ([]*models.ImportJob, error) {
	return nil, nil
}
func (m *privacyMockImportJobStore) UpdateImportJobProgress(_ context.Context, _ string, _ int) error {
	return nil
}
func (m *privacyMockImportJobStore) ResetImportJobDispatch(_ context.Context, _ string) error {
	return nil
}

type privacyMockCatalogStore struct {
	sbStore         *privacyMockSongbookStore
	versions        map[string]*models.CatalogVersion
	deletedVersions []string
	getErr          error
	deleteErr       error
}

func (m *privacyMockCatalogStore) CreateCatalogVersion(_ context.Context, version *models.CatalogVersion) error {
	if m.versions == nil {
		m.versions = make(map[string]*models.CatalogVersion)
	}
	m.versions[version.ID] = version
	return nil
}
func (m *privacyMockCatalogStore) GetCatalogVersion(_ context.Context, id string) (*models.CatalogVersion, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.versions[id], nil
}

func (m *privacyMockCatalogStore) MarkCatalogVersionReady(_ context.Context, id, assetPrefix string, songCount int, _ ...string) (*models.CatalogVersion, error) {
	v, ok := m.versions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	v.Status = models.CatalogVersionReady
	v.AssetPrefix = assetPrefix
	v.SongCount = songCount
	return v, nil
}
func (m *privacyMockCatalogStore) FailCatalogVersion(_ context.Context, id, reason string, _ ...string) (*models.CatalogVersion, error) {
	v, ok := m.versions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	v.Status = models.CatalogVersionFailed
	v.FailureReason = reason
	return v, nil
}
func (m *privacyMockCatalogStore) ReclaimCatalogVersion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (m *privacyMockCatalogStore) PublishCatalogVersion(_ context.Context, _, _ string, _ ...store.PublicationOptions) error {
	return nil
}
func (m *privacyMockCatalogStore) DeleteCatalogVersion(_ context.Context, id string, _ ...string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedVersions = append(m.deletedVersions, id)
	return nil
}
func (m *privacyMockCatalogStore) ReserveCatalogVersionForDeletion(_ context.Context, id string, _ time.Duration) (*models.CatalogVersion, error) {
	v, ok := m.versions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	v.Status = models.CatalogVersionDeleting
	return v, nil
}
func (m *privacyMockCatalogStore) ReleaseCatalogVersionReservation(_ context.Context, id, _ string) error {
	v, ok := m.versions[id]
	if !ok {
		return os.ErrNotExist
	}
	v.Status = models.CatalogVersionReady
	return nil
}
func (m *privacyMockCatalogStore) IsCatalogVersionReferenced(_ context.Context, id string, excludeSongbookIDs ...string) (bool, error) {
	exclude := make(map[string]bool)
	for _, ex := range excludeSongbookIDs {
		exclude[ex] = true
	}
	if m.sbStore != nil {
		for _, b := range m.sbStore.books {
			if b.CatalogVersionID == id && !exclude[b.ID] {
				return true, nil
			}
		}
	}
	return false, nil
}
func (m *privacyMockCatalogStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	var exclude []string
	if len(opts) > 0 {
		exclude = opts[0].ExcludeSongbookIDs
	}
	ref, err := m.IsCatalogVersionReferenced(ctx, id, exclude...)
	if err != nil {
		return err
	}
	if ref {
		return store.ErrCatalogVersionInUse
	}
	v, ok := m.versions[id]
	if !ok {
		return os.ErrNotExist
	}
	if purgeAssets != nil && v.AssetPrefix != "" {
		if err := purgeAssets(v.AssetPrefix); err != nil {
			return err
		}
	}
	return m.DeleteCatalogVersion(ctx, id)
}
func (m *privacyMockCatalogStore) PurgeOrphanCatalogPrefix(_ context.Context, prefix string, purgeAssets func(assetPrefix string) error, _ ...store.PurgeCatalogOptions) error {
	if purgeAssets != nil {
		return purgeAssets(prefix)
	}
	return nil
}

type privacyMockAssetPurger struct {
	deletedKeys     []string
	deletedPrefixes []string
	keyErr          error
	prefixErr       error
}

func (m *privacyMockAssetPurger) DeleteKey(_ context.Context, key string) error {
	if m.keyErr != nil {
		return m.keyErr
	}
	m.deletedKeys = append(m.deletedKeys, key)
	return nil
}
func (m *privacyMockAssetPurger) DeletePrefix(_ context.Context, prefix string) error {
	if m.prefixErr != nil {
		return m.prefixErr
	}
	m.deletedPrefixes = append(m.deletedPrefixes, prefix)
	return nil
}

func TestPrivacyExportData(t *testing.T) {
	authStore := newAuthTestStore()
	sbStore := &privacyMockSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-1", OwnerID: "user-export", Name: "Friday Karaoke"},
		},
	}
	pkStore := &privacyMockPasskeyStore{
		creds: []*models.PasskeyCredential{
			{ID: "pk-1", UserID: "user-export", FriendlyName: "MacBook TouchID", CreatedAt: time.Now()},
		},
	}

	authHandler, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	privacyHandler := &PrivacyHandler{
		Auth:      authStore,
		Songbooks: sbStore,
		Passkeys:  pkStore,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", authHandler.Register)
	mux.HandleFunc("GET /auth/me/export", authHandler.RequireAuth(privacyHandler.ExportData))

	// Register host user
	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"host-export@example.com","password":"password123","display_name":"Export Host"}`, "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", reg.Code, http.StatusCreated)
	}
	tokens := decodeTokens(t, reg)
	sbStore.books[0].OwnerID = tokens.User.ID
	pkStore.creds[0].UserID = tokens.User.ID

	// Call export
	req := httptest.NewRequest(http.MethodGet, "/auth/me/export", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment; filename=") {
		t.Fatalf("expected attachment header, got %q", disposition)
	}

	var export UserDataExport
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}

	if export.User == nil || export.User.Email != "host-export@example.com" {
		t.Fatalf("export user email mismatch: %+v", export.User)
	}
	if len(export.Passkeys) != 1 || export.Passkeys[0].ID != "pk-1" {
		t.Fatalf("export passkeys mismatch: %+v", export.Passkeys)
	}
	if len(export.Songbooks) != 1 || export.Songbooks[0].Songbook.ID != "sb-1" {
		t.Fatalf("export songbooks mismatch: %+v", export.Songbooks)
	}
}

func TestPrivacyExportData_FailurePropagation(t *testing.T) {
	authStore := newAuthTestStore()
	authHandler, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", authHandler.Register)

	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"fail-export@example.com","password":"password123","display_name":"Fail Export"}`, "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", reg.Code, http.StatusCreated)
	}
	tokens := decodeTokens(t, reg)
	userID := tokens.User.ID

	injectedErr := errors.New("storage failure")

	tests := []struct {
		name    string
		handler *PrivacyHandler
	}{
		{
			name: "passkeys read failure",
			handler: &PrivacyHandler{
				Auth:     authStore,
				Passkeys: &privacyMockPasskeyStore{listErr: injectedErr},
			},
		},
		{
			name: "songbooks read failure",
			handler: &PrivacyHandler{
				Auth:      authStore,
				Songbooks: &privacyMockSongbookStore{listErr: injectedErr},
			},
		},
		{
			name: "events read failure",
			handler: &PrivacyHandler{
				Auth:   authStore,
				Events: &privacyMockEventStore{listErr: injectedErr},
			},
		},
		{
			name: "catalog version read failure",
			handler: &PrivacyHandler{
				Auth: authStore,
				Songbooks: &privacyMockSongbookStore{
					books: []*models.Songbook{{ID: "sb-1", OwnerID: userID, CatalogVersionID: "cv-fail"}},
				},
				Catalogs: &privacyMockCatalogStore{getErr: injectedErr},
			},
		},
		{
			name: "catalog changes read failure",
			handler: &PrivacyHandler{
				Auth: authStore,
				Songbooks: &privacyMockSongbookStore{
					books: []*models.Songbook{{ID: "sb-1", OwnerID: userID}},
				},
				CatalogChanges: &privacyMockCatalogChangeStore{listErr: injectedErr},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testMux := http.NewServeMux()
			testMux.HandleFunc("GET /auth/me/export", authHandler.RequireAuth(tc.handler.ExportData))

			req := httptest.NewRequest(http.MethodGet, "/auth/me/export", nil)
			req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
			rec := httptest.NewRecorder()
			testMux.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected status %d, got %d: %s", http.StatusInternalServerError, rec.Code, rec.Body.String())
			}

			// Must not return attachment disposition or complete export file
			disposition := rec.Header().Get("Content-Disposition")
			if disposition != "" {
				t.Fatalf("expected empty Content-Disposition on failure, got %q", disposition)
			}

			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if body["detail"] != "failed to export user data" {
				t.Fatalf("expected error detail 'failed to export user data', got %q", body["detail"])
			}
		})
	}
}

func TestPrivacyDeleteAccount_CompleteCascade(t *testing.T) {
	authStore := newAuthTestStore()
	authHandler, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", authHandler.Register)

	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"delete-all@example.com","password":"password123","display_name":"Full Purge Tester"}`, "")
	tokens := decodeTokens(t, reg)
	userID := tokens.User.ID

	sbStore := &privacyMockSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-1", OwnerID: userID, Name: "Purge Karaoke", CatalogVersionID: "cv-user"},
		},
	}
	pkStore := &privacyMockPasskeyStore{
		creds: []*models.PasskeyCredential{
			{ID: "pk-1", UserID: userID, FriendlyName: "TouchID"},
		},
	}
	evStore := &privacyMockEventStore{
		events: []*models.Event{
			{ID: "ev-1", OwnerID: userID, Name: "Saturday Night Karaoke"},
		},
	}
	reqStore := &privacyMockRequestStore{
		requests: []*models.SongRequest{
			{ID: "req-1", EventID: "ev-1", SingerName: "Jane Doe"},
			{ID: "req-2", EventID: "ev-1", SingerName: "John Smith"},
		},
	}
	ccStore := &privacyMockCatalogChangeStore{
		changes: map[string][]*models.CatalogChange{
			"sb-1": {
				{SongbookID: "sb-1", SongID: "song-1"},
			},
		},
	}
	ijStore := &privacyMockImportJobStore{
		jobs: []*models.ImportJob{
			{ID: "job-1", SongbookID: "sb-1", ManifestKey: "manifests/job-1.json"},
		},
	}
	catStore := &privacyMockCatalogStore{
		sbStore: sbStore,
		versions: map[string]*models.CatalogVersion{
			"cv-user": {ID: "cv-user", AssetPrefix: "catalogs/cv-user/"},
		},
	}
	assetPurger := &privacyMockAssetPurger{}

	privacyHandler := &PrivacyHandler{
		Auth:           authStore,
		Songbooks:      sbStore,
		Passkeys:       pkStore,
		Events:         evStore,
		Requests:       reqStore,
		CatalogChanges: ccStore,
		ImportJobs:     ijStore,
		Catalogs:       catStore,
		Assets:         assetPurger,
	}

	mux.HandleFunc("DELETE /auth/me", authHandler.RequireAuth(privacyHandler.DeleteAccount))

	// 1. Reject without explicit 'DELETE'
	badReq := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(`{"confirm":"no"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad delete status = %d, want %d", badRec.Code, http.StatusBadRequest)
	}

	// 2. Perform valid delete
	goodReq := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(`{"confirm":"DELETE"}`))
	goodReq.Header.Set("Content-Type", "application/json")
	goodReq.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	goodRec := httptest.NewRecorder()
	mux.ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusNoContent {
		t.Fatalf("good delete status = %d, want %d: %s", goodRec.Code, http.StatusNoContent, goodRec.Body.String())
	}

	// Verify complete cascade:
	// A. Passkeys deleted
	if len(pkStore.deletedPasskeys) != 1 || pkStore.deletedPasskeys[0] != "pk-1" {
		t.Fatalf("expected pk-1 deleted, got %+v", pkStore.deletedPasskeys)
	}
	// B. Singer requests deleted
	if len(reqStore.deletedRequests) != 2 {
		t.Fatalf("expected 2 singer requests deleted, got %+v", reqStore.deletedRequests)
	}
	// C. Events deleted
	if len(evStore.deletedEvents) != 1 || evStore.deletedEvents[0] != "ev-1" {
		t.Fatalf("expected ev-1 deleted, got %+v", evStore.deletedEvents)
	}
	// D. Catalog changes cleared
	if len(ccStore.deletedChanges) != 1 || ccStore.deletedChanges[0] != "song-1" {
		t.Fatalf("expected song-1 change deleted, got %+v", ccStore.deletedChanges)
	}
	// E. Import manifest and job deleted
	if len(assetPurger.deletedKeys) != 1 || assetPurger.deletedKeys[0] != "manifests/job-1.json" {
		t.Fatalf("expected manifest key deleted, got %+v", assetPurger.deletedKeys)
	}
	if len(ijStore.deletedJobs) != 1 || ijStore.deletedJobs[0] != "job-1" {
		t.Fatalf("expected job-1 deleted, got %+v", ijStore.deletedJobs)
	}
	// F. Unshared catalog version S3 prefix and catalog record deleted
	if len(assetPurger.deletedPrefixes) != 1 || assetPurger.deletedPrefixes[0] != "catalogs/cv-user/" {
		t.Fatalf("expected catalog prefix deleted, got %+v", assetPurger.deletedPrefixes)
	}
	if len(catStore.deletedVersions) != 1 || catStore.deletedVersions[0] != "cv-user" {
		t.Fatalf("expected cv-user deleted, got %+v", catStore.deletedVersions)
	}
	// G. Songbook deleted
	if len(sbStore.deletedBooks) != 1 || sbStore.deletedBooks[0] != "sb-1" {
		t.Fatalf("expected sb-1 deleted, got %+v", sbStore.deletedBooks)
	}
	// H. User deleted from auth store
	if _, err := authStore.GetUser(context.Background(), userID); err == nil {
		t.Fatalf("expected user to be deleted from auth store")
	}
}

func TestPrivacyDeleteAccount_PreservesSharedCatalog(t *testing.T) {
	authStore := newAuthTestStore()
	authHandler, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", authHandler.Register)

	regA := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"user-a@example.com","password":"password123","display_name":"User A"}`, "")
	tokensA := decodeTokens(t, regA)

	regB := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"user-b@example.com","password":"password123","display_name":"User B"}`, "")
	tokensB := decodeTokens(t, regB)

	// Both share the same catalog version
	sbStore := &privacyMockSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-a", OwnerID: tokensA.User.ID, Name: "Book A", CatalogVersionID: "shared-cv"},
			{ID: "sb-b", OwnerID: tokensB.User.ID, Name: "Book B", CatalogVersionID: "shared-cv"},
		},
	}
	catStore := &privacyMockCatalogStore{
		sbStore: sbStore,
		versions: map[string]*models.CatalogVersion{
			"shared-cv": {ID: "shared-cv", AssetPrefix: "catalogs/shared-cv/"},
		},
	}
	assetPurger := &privacyMockAssetPurger{}

	privacyHandler := &PrivacyHandler{
		Auth:      authStore,
		Songbooks: sbStore,
		Catalogs:  catStore,
		Assets:    assetPurger,
	}

	mux.HandleFunc("DELETE /auth/me", authHandler.RequireAuth(privacyHandler.DeleteAccount))

	goodReq := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(`{"confirm":"DELETE"}`))
	goodReq.Header.Set("Content-Type", "application/json")
	goodReq.Header.Set("Authorization", "Bearer "+tokensA.AccessToken)
	goodRec := httptest.NewRecorder()
	mux.ServeHTTP(goodRec, goodReq)

	if goodRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d: %s", goodRec.Code, http.StatusNoContent, goodRec.Body.String())
	}

	// Verify User A songbook deleted, but shared catalog was NOT touched
	if len(sbStore.deletedBooks) != 1 || sbStore.deletedBooks[0] != "sb-a" {
		t.Fatalf("expected sb-a deleted, got %+v", sbStore.deletedBooks)
	}
	if len(assetPurger.deletedPrefixes) != 0 {
		t.Fatalf("expected shared catalog prefix NOT to be deleted, got %+v", assetPurger.deletedPrefixes)
	}
	if len(catStore.deletedVersions) != 0 {
		t.Fatalf("expected shared catalog version NOT to be deleted, got %+v", catStore.deletedVersions)
	}
}

func TestPrivacyDeleteAccount_FailsLoudlyOnError(t *testing.T) {
	authStore := newAuthTestStore()
	authHandler, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", authHandler.Register)

	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"fail-delete@example.com","password":"password123","display_name":"Fail Delete"}`, "")
	tokens := decodeTokens(t, reg)

	sbStore := &privacyMockSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-1", OwnerID: tokens.User.ID, Name: "Fail Book"},
		},
		deleteErr: errors.New("dynamo write failure"),
	}

	privacyHandler := &PrivacyHandler{
		Auth:      authStore,
		Songbooks: sbStore,
	}

	mux.HandleFunc("DELETE /auth/me", authHandler.RequireAuth(privacyHandler.DeleteAccount))

	req := httptest.NewRequest(http.MethodDelete, "/auth/me", strings.NewReader(`{"confirm":"DELETE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	// User should NOT be deleted if cascade failed
	if _, err := authStore.GetUser(context.Background(), tokens.User.ID); err != nil {
		t.Fatalf("expected user to still exist after failed delete cascade: %v", err)
	}
}
