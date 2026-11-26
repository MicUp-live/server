package dynamo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"micup.live/server/internal/account"
	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type faultInjectingAccountStore struct {
	*Store
	failStep string
	failErr  error
}

func (f *faultInjectingAccountStore) shouldFail(step string) error {
	if f.failStep == step {
		if f.failErr != nil {
			return f.failErr
		}
		return errors.New("injected fault at " + step)
	}
	return nil
}

func (f *faultInjectingAccountStore) BeginAccountDeletion(ctx context.Context, userID string, pendingCatalogVersions []string) (*models.User, error) {
	if len(pendingCatalogVersions) == 0 {
		if err := f.shouldFail("begin_deletion"); err != nil {
			return nil, err
		}
	} else {
		if err := f.shouldFail("record_catalog_versions"); err != nil {
			return nil, err
		}
	}
	return f.Store.BeginAccountDeletion(ctx, userID, pendingCatalogVersions)
}

func (f *faultInjectingAccountStore) DeleteAuthSessionsForUser(ctx context.Context, userID string) error {
	if err := f.shouldFail("auth_sessions"); err != nil {
		return err
	}
	return f.Store.DeleteAuthSessionsForUser(ctx, userID)
}

func (f *faultInjectingAccountStore) ListPasskeyCredentialsForUser(ctx context.Context, userID string) ([]*models.PasskeyCredential, error) {
	if err := f.shouldFail("list_passkeys"); err != nil {
		return nil, err
	}
	return f.Store.ListPasskeyCredentialsForUser(ctx, userID)
}

func (f *faultInjectingAccountStore) DeletePasskeyCredential(ctx context.Context, id string, userID string) error {
	if err := f.shouldFail("delete_passkeys"); err != nil {
		return err
	}
	return f.Store.DeletePasskeyCredential(ctx, id, userID)
}

func (f *faultInjectingAccountStore) ListDeveloperKeys(ctx context.Context, ownerID string) ([]*models.DeveloperKey, error) {
	if err := f.shouldFail("list_developer_keys"); err != nil {
		return nil, err
	}
	return f.Store.ListDeveloperKeys(ctx, ownerID)
}

func (f *faultInjectingAccountStore) RevokeDeveloperKey(ctx context.Context, id, ownerID string) error {
	if err := f.shouldFail("delete_developer_keys"); err != nil {
		return err
	}
	return f.Store.RevokeDeveloperKey(ctx, id, ownerID)
}

func (f *faultInjectingAccountStore) ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error) {
	if err := f.shouldFail("list_events"); err != nil {
		return nil, err
	}
	return f.Store.ListEventsByOwner(ctx, ownerID)
}

func (f *faultInjectingAccountStore) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	if err := f.shouldFail("list_requests"); err != nil {
		return nil, err
	}
	return f.Store.ListRequestsByEvent(ctx, eventID)
}

func (f *faultInjectingAccountStore) DeleteRequest(ctx context.Context, id string) error {
	if err := f.shouldFail("delete_requests"); err != nil {
		return err
	}
	return f.Store.DeleteRequest(ctx, id)
}

func (f *faultInjectingAccountStore) DeleteEvent(ctx context.Context, id string) error {
	if err := f.shouldFail("delete_events"); err != nil {
		return err
	}
	return f.Store.DeleteEvent(ctx, id)
}

func (f *faultInjectingAccountStore) ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	if err := f.shouldFail("list_songbooks"); err != nil {
		return nil, err
	}
	return f.Store.ListSongbooksByOwner(ctx, ownerID)
}

func (f *faultInjectingAccountStore) BeginSongbookDeletion(ctx context.Context, id string) (*models.Songbook, error) {
	if err := f.shouldFail("begin_songbook_deletion"); err != nil {
		return nil, err
	}
	return f.Store.BeginSongbookDeletion(ctx, id)
}

func (f *faultInjectingAccountStore) ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	if err := f.shouldFail("list_catalog_changes"); err != nil {
		return nil, err
	}
	return f.Store.ListCatalogChanges(ctx, songbookID)
}

func (f *faultInjectingAccountStore) DeleteCatalogChange(ctx context.Context, songbookID, songID string, updatedAt time.Time) error {
	if err := f.shouldFail("delete_catalog_changes"); err != nil {
		return err
	}
	return f.Store.DeleteCatalogChange(ctx, songbookID, songID, updatedAt)
}

func (f *faultInjectingAccountStore) ListImportJobs(ctx context.Context) ([]*models.ImportJob, error) {
	if err := f.shouldFail("list_import_jobs"); err != nil {
		return nil, err
	}
	return f.Store.ListImportJobs(ctx)
}

func (f *faultInjectingAccountStore) DeleteImportJob(ctx context.Context, id string) error {
	if err := f.shouldFail("delete_import_jobs"); err != nil {
		return err
	}
	return f.Store.DeleteImportJob(ctx, id)
}

func (f *faultInjectingAccountStore) DeleteSongbook(ctx context.Context, id string) error {
	if err := f.shouldFail("delete_songbooks"); err != nil {
		return err
	}
	return f.Store.DeleteSongbook(ctx, id)
}

func (f *faultInjectingAccountStore) DeleteUser(ctx context.Context, userID string) error {
	if err := f.shouldFail("delete_user"); err != nil {
		return err
	}
	return f.Store.DeleteUser(ctx, userID)
}

type faultInjectingManifestWriter struct {
	*inMemoryAssetStore
	failDeleteKey    bool
	failDeletePrefix bool
}

func (w *faultInjectingManifestWriter) Put(_ context.Context, key string, data []byte) error {
	w.inMemoryAssetStore.Put("", key, data)
	return nil
}

func (w *faultInjectingManifestWriter) Presign(_ context.Context, key string, _ int64) (string, error) {
	return "https://example.com/" + key, nil
}

func (w *faultInjectingManifestWriter) DeleteKey(ctx context.Context, key string) error {
	if w.failDeleteKey {
		return errors.New("injected S3 DeleteKey failure")
	}
	return w.inMemoryAssetStore.DeleteKey(ctx, key)
}

func (w *faultInjectingManifestWriter) DeletePrefix(ctx context.Context, prefix string) error {
	if w.failDeletePrefix {
		return errors.New("injected S3 DeletePrefix failure")
	}
	return w.inMemoryAssetStore.DeletePrefix(ctx, prefix)
}

func doRouterJSON(t *testing.T, handler http.Handler, method, path, body, bearerToken, deletionToken string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if deletionToken != "" {
		req.Header.Set("X-Deletion-Token", deletionToken)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

var (
	sharedAccountStore     *Store
	sharedAccountStoreOnce sync.Once
)

func getSharedAccountTestStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("DYNAMO_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMO_ENDPOINT is not set")
	}
	sharedAccountStoreOnce.Do(func() {
		s, err := New(context.Background(), Config{Region: "us-east-1", Endpoint: endpoint, TablePrefix: "test-acc-" + uuid.NewString() + "-"})
		if err != nil {
			t.Fatalf("new DynamoDB store: %v", err)
		}
		if err := s.EnsureTables(context.Background()); err != nil {
			t.Fatalf("ensure DynamoDB tables: %v", err)
		}
		sharedAccountStore = s
	})
	if sharedAccountStore == nil {
		t.Skip("shared account test store not initialized")
	}
	return sharedAccountStore
}

func TestDynamoAccountDeletionRecovery_PasswordAndPasskeyAndSharedCatalog(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()
	authSecret := "m7-test-signing-secret-with-at-least-32-bytes"

	t.Run("PasswordBackedAccount_RevokesAllCredentials_PreservesSharedCatalog_RecoversOnRetry", func(t *testing.T) {
		faultStore := &faultInjectingAccountStore{Store: s}
		assetStore := &faultInjectingManifestWriter{inMemoryAssetStore: newInMemoryAssetStore()}

		router, err := app.NewRouter(config.Config{
			Environment:   config.Development,
			AuthSecret:    authSecret,
			AuthRateLimit: 1000,
		}, app.Dependencies{
			Auth:           faultStore,
			Passkeys:       faultStore,
			Songbooks:      faultStore,
			Requests:       faultStore,
			Events:         faultStore,
			Catalogs:       faultStore,
			CatalogReader:  &dummyCatalogReader{},
			CatalogChanges: faultStore,
			ImportJobs:     faultStore,
			ManifestWriter: assetStore,
			DeveloperKeys:  faultStore,
			SongbookAccess: faultStore,
		})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}

		// 1. Register User A (host) and User B (other tenant) via real router
		emailA := "host-a-" + uuid.NewString() + "@example.com"
		regA := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/register",
			`{"email":"`+emailA+`","password":"password12345","display_name":"Host A","is_host":true}`, "", "")
		if regA.Code != http.StatusCreated {
			t.Fatalf("register User A = %d: %s", regA.Code, regA.Body.String())
		}
		var sessionA struct {
			AccessToken string       `json:"access_token"`
			User        *models.User `json:"user"`
		}
		if err := json.Unmarshal(regA.Body.Bytes(), &sessionA); err != nil {
			t.Fatalf("unmarshal session A: %v", err)
		}
		var refreshCookieA *http.Cookie
		for _, c := range regA.Result().Cookies() {
			if c.Name == "micup_refresh" {
				refreshCookieA = c
			}
		}
		if refreshCookieA == nil {
			t.Fatal("expected micup_refresh cookie for User A")
		}

		emailB := "host-b-" + uuid.NewString() + "@example.com"
		regB := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/register",
			`{"email":"`+emailB+`","password":"password12345","display_name":"Host B","is_host":true}`, "", "")
		if regB.Code != http.StatusCreated {
			t.Fatalf("register User B = %d: %s", regB.Code, regB.Body.String())
		}
		var sessionB struct {
			AccessToken string       `json:"access_token"`
			User        *models.User `json:"user"`
		}
		_ = json.Unmarshal(regB.Body.Bytes(), &sessionB)

		// Pre-issue a password reset token and email verification token for User A before deletion starts
		authHandler, err := handlers.NewAuthHandler(faultStore, authSecret)
		if err != nil {
			t.Fatalf("NewAuthHandler: %v", err)
		}
		storedUserA, err := s.GetUser(ctx, sessionA.User.ID)
		if err != nil {
			t.Fatalf("GetUser A: %v", err)
		}
		preIssuedResetToken, err := authHandler.PasswordReset.IssueResetToken(storedUserA)
		if err != nil {
			t.Fatalf("IssueResetToken: %v", err)
		}

		// Attach a passkey credential and a developer key to User A
		passkeyA := &models.PasskeyCredential{
			ID:           "pk-a-" + uuid.NewString(),
			UserID:       sessionA.User.ID,
			PublicKey:    []byte("pub-key"),
			FriendlyName: "Host A Key",
		}
		if err := s.SavePasskeyCredential(ctx, passkeyA); err != nil {
			t.Fatalf("SavePasskeyCredential: %v", err)
		}
		rawDevKeyA := "mk_live_" + uuid.NewString()
		devKeyA := &models.DeveloperKey{
			OwnerID:   sessionA.User.ID,
			Name:      "Host A Dev Key",
			KeyPrefix: rawDevKeyA[:12],
			Scopes:    []string{handlers.DeveloperScopeCatalogsWrite, handlers.DeveloperScopeEventsWrite},
		}
		if err := s.CreateDeveloperKey(ctx, rawDevKeyA, devKeyA); err != nil {
			t.Fatalf("CreateDeveloperKey: %v", err)
		}

		// Create owned event + request, and two songbooks for User A (one sole catalog, one shared with User B)
		sbSole := &models.Songbook{ID: "sb-sole-" + uuid.NewString(), OwnerID: sessionA.User.ID, Name: "Sole Book"}
		sbSharedA := &models.Songbook{ID: "sb-shared-a-" + uuid.NewString(), OwnerID: sessionA.User.ID, Name: "Shared Book A"}
		sbSharedB := &models.Songbook{ID: "sb-shared-b-" + uuid.NewString(), OwnerID: sessionB.User.ID, Name: "Shared Book B"}
		for _, sb := range []*models.Songbook{sbSole, sbSharedA, sbSharedB} {
			if err := s.CreateSongbook(ctx, sb); err != nil {
				t.Fatalf("CreateSongbook %s: %v", sb.ID, err)
			}
		}
		if err := s.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: sbSole.ID, SongID: "chg-1", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("PutCatalogChange: %v", err)
		}
		manifestKey := "imports/" + sbSole.ID + "/manifest.json"
		assetStore.inMemoryAssetStore.Put("imports/"+sbSole.ID, "manifest.json", []byte(`[]`))
		if err := s.CreateImportJob(ctx, &models.ImportJob{
			ID:          "job-a-" + uuid.NewString(),
			SongbookID:  sbSole.ID,
			Status:      models.ImportJobCompleted,
			ManifestKey: manifestKey,
		}); err != nil {
			t.Fatalf("CreateImportJob: %v", err)
		}

		soleVersionID := "sha256:sole-" + uuid.NewString()
		sharedVersionID := "sha256:shared-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, soleVersionID, 25)
		helperCreateAndReadyVersion(t, s, sharedVersionID, 50)
		assetStore.inMemoryAssetStore.Put("catalogs/"+soleVersionID, "manifest.json", []byte(`{"sole":true}`))
		assetStore.inMemoryAssetStore.Put("catalogs/"+soleVersionID, "search.json.gz", []byte(`{"sole":true}`))
		assetStore.inMemoryAssetStore.Put("catalogs/"+sharedVersionID, "manifest.json", []byte(`{"shared":true}`))
		assetStore.inMemoryAssetStore.Put("catalogs/"+sharedVersionID, "search.json.gz", []byte(`{"shared":true}`))

		if err := s.PublishCatalogVersion(ctx, sbSole.ID, soleVersionID); err != nil {
			t.Fatalf("PublishCatalogVersion sole: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sbSharedA.ID, sharedVersionID); err != nil {
			t.Fatalf("PublishCatalogVersion shared A: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sbSharedB.ID, sharedVersionID); err != nil {
			t.Fatalf("PublishCatalogVersion shared B: %v", err)
		}

		evA := &models.Event{
			ID:         "ev-a-" + uuid.NewString(),
			OwnerID:    sessionA.User.ID,
			SongbookID: sbSole.ID,
			Name:       "Host A Show",
			JoinCode:   "M7" + uuid.NewString()[:4],
			Status:     models.EventStatusActive,
		}
		if err := s.CreateEvent(ctx, evA); err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}
		reqA := &models.SongRequest{
			ID:         "req-a-" + uuid.NewString(),
			EventID:    evA.ID,
			SongbookID: sbSole.ID,
			SingerName: "Alice",
			SongTitle:  "Song",
			Status:     models.StatusPending,
		}
		if err := s.CreateRequest(ctx, reqA); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		// 2. Inject S3 asset purge failure during catalog version purge (AFTER credentials and songbooks are deleted!)
		assetStore.failDeletePrefix = true
		delAttempt1 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, sessionA.AccessToken, "")
		if delAttempt1.Code != http.StatusInternalServerError {
			t.Fatalf("attempt 1 status = %d, want 500: %s", delAttempt1.Code, delAttempt1.Body.String())
		}
		deletionToken := delAttempt1.Header().Get("X-Deletion-Token")
		if deletionToken == "" {
			t.Fatal("expected X-Deletion-Token header on partial deletion failure")
		}
		var errBody map[string]interface{}
		if err := json.Unmarshal(delAttempt1.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("unmarshal error body: %v", err)
		}
		if errBody["step"] != "purge_catalog_version" || errBody["deleting"] != true || errBody["deletion_token"] != deletionToken {
			t.Fatalf("unexpected error response payload: %+v", errBody)
		}

		// Verify songbooks are already deleted in DynamoDB while sole catalog version is fenced in 'deleting' status
		if _, err := s.GetSongbook(ctx, sbSole.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected sole songbook deleted before catalog purge, got %v", err)
		}
		fencedSole, err := s.GetCatalogVersion(ctx, soleVersionID)
		if err != nil || fencedSole.Status != models.CatalogVersionDeleting {
			t.Fatalf("expected sole catalog version fenced in deleting status, got %+v, %v", fencedSole, err)
		}

		// 3. Verify ALL credentials for User A are revoked and NEVER restored during the interrupted state
		if res := doRouterJSON(t, router, http.MethodGet, "/api/v1/auth/me", "", sessionA.AccessToken, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("GET /auth/me with pre-deletion bearer token = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Hacked"}`, sessionA.AccessToken, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("PATCH /auth/me with pre-deletion bearer token = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodGet, "/api/v1/auth/me/export", "", sessionA.AccessToken, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("GET /auth/me/export with pre-deletion bearer token = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", `{}`, "", "", refreshCookieA); res.Code != http.StatusUnauthorized {
			t.Fatalf("POST /auth/refresh after deletion started = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"email":"`+emailA+`","password":"password12345"}`, "", ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("POST /auth/login after deletion started = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", `{"token":"`+preIssuedResetToken+`","password":"newpassword999"}`, "", ""); res.Code != http.StatusBadRequest {
			t.Fatalf("POST /auth/reset-password with pre-issued token = %d, want 400", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodGet, "/api/v1/songbooks", "", rawDevKeyA, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("GET /songbooks with developer key after deletion started = %d, want 401", res.Code)
		}
		// Scoped deletion recovery token cannot be used as a general bearer token on other routes
		if res := doRouterJSON(t, router, http.MethodGet, "/api/v1/auth/me", "", deletionToken, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("GET /auth/me with deletion recovery token = %d, want 401", res.Code)
		}
		if res := doRouterJSON(t, router, http.MethodGet, "/api/v1/auth/me/export", "", deletionToken, ""); res.Code != http.StatusUnauthorized {
			t.Fatalf("GET /auth/me/export with deletion recovery token = %d, want 401", res.Code)
		}

		// 4. Cross-tenant denial: User B cannot pair their bearer token with User A's deletion token
		if res := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, sessionB.AccessToken, deletionToken); res.Code != http.StatusForbidden {
			t.Fatalf("cross-tenant DELETE /auth/me = %d, want 403: %s", res.Code, res.Body.String())
		}

		// 5. Clear transient S3 fault and retry DELETE /api/v1/auth/me using X-Deletion-Token (no session or password needed)
		assetStore.failDeletePrefix = false
		delRetry := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, "", deletionToken)
		if delRetry.Code != http.StatusNoContent {
			t.Fatalf("retry DELETE /auth/me with X-Deletion-Token = %d, want 204: %s", delRetry.Code, delRetry.Body.String())
		}

		// 6. Verify complete removal of User A and preservation of shared catalog & User B
		if _, err := s.GetUser(ctx, sessionA.User.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected User A deleted, got %v", err)
		}
		if _, err := s.GetUserByEmail(ctx, emailA); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected User A email identity claim released, got %v", err)
		}
		if _, err := s.GetCatalogVersion(ctx, soleVersionID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected sole catalog version purged on retry, got %v", err)
		}
		if assetStore.inMemoryAssetStore.Has("catalogs/"+soleVersionID, "manifest.json") {
			t.Fatal("expected sole catalog S3 manifest purged on retry")
		}
		sharedVer, err := s.GetCatalogVersion(ctx, sharedVersionID)
		if err != nil || sharedVer.Status != models.CatalogVersionReady {
			t.Fatalf("expected shared catalog version ready and intact, got %+v, %v", sharedVer, err)
		}
		if !assetStore.inMemoryAssetStore.Has("catalogs/"+sharedVersionID, "manifest.json") ||
			!assetStore.inMemoryAssetStore.Has("catalogs/"+sharedVersionID, "search.json.gz") {
			t.Fatal("shared catalog S3 assets were purged!")
		}
		if _, err := s.GetUser(ctx, sessionB.User.ID); err != nil {
			t.Fatalf("expected User B untouched, got %v", err)
		}

		// 7. Repeated DELETE /api/v1/auth/me with X-Deletion-Token is idempotent (204 No Content)
		delRepeat := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, "", deletionToken)
		if delRepeat.Code != http.StatusNoContent {
			t.Fatalf("repeated DELETE /auth/me = %d, want 204: %s", delRepeat.Code, delRepeat.Body.String())
		}

		// 8. Released email can be registered by a new account
		reReg := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/register",
			`{"email":"`+emailA+`","password":"password12345","display_name":"Reclaimed Host","is_host":true}`, "", "")
		if reReg.Code != http.StatusCreated {
			t.Fatalf("re-register released email = %d, want 201: %s", reReg.Code, reReg.Body.String())
		}
	})

	t.Run("PasskeyOnlyAccount_RecoversAfterPasskeyAndSessionRevocation", func(t *testing.T) {
		faultStore := &faultInjectingAccountStore{Store: s}
		assetStore := &faultInjectingManifestWriter{inMemoryAssetStore: newInMemoryAssetStore()}

		router, err := app.NewRouter(config.Config{
			Environment:   config.Development,
			AuthSecret:    authSecret,
			AuthRateLimit: 1000,
		}, app.Dependencies{
			Auth:           faultStore,
			Passkeys:       faultStore,
			Songbooks:      faultStore,
			Requests:       faultStore,
			Events:         faultStore,
			Catalogs:       faultStore,
			CatalogReader:  &dummyCatalogReader{},
			CatalogChanges: faultStore,
			ImportJobs:     faultStore,
			ManifestWriter: assetStore,
			DeveloperKeys:  faultStore,
			SongbookAccess: faultStore,
		})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}

		pkUser := &models.User{
			ID:          "user-pk-" + uuid.NewString(),
			Email:       "pkonly-" + uuid.NewString() + "@example.com",
			DisplayName: "Passkey Singer",
			IsHost:      true,
		}
		pkCred := &models.PasskeyCredential{
			ID:           "cred-pk-" + uuid.NewString(),
			UserID:       pkUser.ID,
			PublicKey:    []byte("pk-public-key"),
			FriendlyName: "iPhone Passkey",
		}
		if err := s.CreateUserWithPasskey(ctx, pkUser, pkCred); err != nil {
			t.Fatalf("CreateUserWithPasskey: %v", err)
		}
		ev := &models.Event{
			ID:       "ev-pk-" + uuid.NewString(),
			OwnerID:  pkUser.ID,
			Name:     "Passkey Show",
			JoinCode: "PK" + uuid.NewString()[:4],
			Status:   models.EventStatusActive,
		}
		if err := s.CreateEvent(ctx, ev); err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}

		authHandler, err := handlers.NewAuthHandler(faultStore, authSecret)
		if err != nil {
			t.Fatalf("NewAuthHandler: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login-verify", nil)
		authHandler.WriteSession(rec, req, pkUser, http.StatusOK)
		var sess struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &sess)

		// Fail on delete_events AFTER sessions and passkeys are deleted
		faultStore.failStep = "delete_events"
		attempt1 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, sess.AccessToken, "")
		if attempt1.Code != http.StatusInternalServerError {
			t.Fatalf("passkey account delete attempt 1 = %d, want 500: %s", attempt1.Code, attempt1.Body.String())
		}
		delToken := attempt1.Header().Get("X-Deletion-Token")
		if delToken == "" {
			t.Fatal("expected X-Deletion-Token header")
		}

		// Passkey credential is already deleted and passkey login options are rejected
		creds, err := s.ListPasskeyCredentialsForUser(ctx, pkUser.ID)
		if err != nil || len(creds) != 0 {
			t.Fatalf("expected 0 passkeys remaining after step 2, got %d (%v)", len(creds), err)
		}
		loginOpts := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/passkey/login-options", `{"email":"`+pkUser.Email+`"}`, "", "")
		if loginOpts.Code != http.StatusUnauthorized {
			t.Fatalf("passkey login-options during deletion = %d, want 401", loginOpts.Code)
		}

		// Clear fault and retry DELETE /api/v1/auth/me using Authorization: Bearer <deletion_token>
		faultStore.failStep = ""
		attempt2 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, delToken, "")
		if attempt2.Code != http.StatusNoContent {
			t.Fatalf("passkey account delete retry = %d, want 204: %s", attempt2.Code, attempt2.Body.String())
		}
		if _, err := s.GetUser(ctx, pkUser.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected passkey user deleted, got %v", err)
		}
		if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected passkey user event deleted, got %v", err)
		}
	})
}

func TestDynamoAccountDeletion_AllDestructiveBoundariesAndOperatorRecovery(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()
	authSecret := "m7-boundary-signing-secret-with-at-least-32-bytes"

	boundaries := []string{
		"begin_deletion",
		"auth_sessions",
		"list_passkeys",
		"delete_passkeys",
		"list_developer_keys",
		"delete_developer_keys",
		"list_events",
		"list_requests",
		"delete_requests",
		"delete_events",
		"list_songbooks",
		"record_catalog_versions",
		"list_catalog_changes",
		"delete_catalog_changes",
		"list_import_jobs",
		"delete_import_assets",
		"delete_import_jobs",
		"delete_songbooks",
		"purge_catalog_version",
		"delete_user",
	}

	for _, boundary := range boundaries {
		t.Run("Boundary_"+boundary, func(t *testing.T) {
			faultStore := &faultInjectingAccountStore{Store: s}
			assetStore := &faultInjectingManifestWriter{inMemoryAssetStore: newInMemoryAssetStore()}

			router, err := app.NewRouter(config.Config{
				Environment:   config.Development,
				AuthSecret:    authSecret,
				AuthRateLimit: 1000,
			}, app.Dependencies{
				Auth:           faultStore,
				Passkeys:       faultStore,
				Songbooks:      faultStore,
				Requests:       faultStore,
				Events:         faultStore,
				Catalogs:       faultStore,
				CatalogReader:  &dummyCatalogReader{},
				CatalogChanges: faultStore,
				ImportJobs:     faultStore,
				ManifestWriter: assetStore,
				DeveloperKeys:  faultStore,
				SongbookAccess: faultStore,
			})
			if err != nil {
				t.Fatalf("NewRouter: %v", err)
			}

			pwdHash, _ := bcrypt.GenerateFromPassword([]byte("password12345"), bcrypt.MinCost)
			user := &models.User{
				ID:           "u-" + boundary + "-" + uuid.NewString(),
				Email:        boundary + "-" + uuid.NewString() + "@example.com",
				DisplayName:  "Boundary User",
				PasswordHash: string(pwdHash),
				IsHost:       true,
			}
			if err := s.CreateUser(ctx, user); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			if err := s.SavePasskeyCredential(ctx, &models.PasskeyCredential{
				ID:           "pk-" + uuid.NewString(),
				UserID:       user.ID,
				PublicKey:    []byte("pk"),
				FriendlyName: "Key",
			}); err != nil {
				t.Fatalf("SavePasskeyCredential: %v", err)
			}
			rawDevKey := "mk_live_" + uuid.NewString()
			if err := s.CreateDeveloperKey(ctx, rawDevKey, &models.DeveloperKey{
				OwnerID:   user.ID,
				Name:      "DevKey",
				KeyPrefix: rawDevKey[:12],
				Scopes:    []string{handlers.DeveloperScopeCatalogsWrite},
			}); err != nil {
				t.Fatalf("CreateDeveloperKey: %v", err)
			}

			sb := &models.Songbook{ID: "sb-" + uuid.NewString(), OwnerID: user.ID, Name: "Boundary Book"}
			if err := s.CreateSongbook(ctx, sb); err != nil {
				t.Fatalf("CreateSongbook: %v", err)
			}
			if err := s.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: sb.ID, SongID: "song-1", UpdatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("PutCatalogChange: %v", err)
			}
			manifestKey := "imports/" + sb.ID + "/manifest.json"
			assetStore.inMemoryAssetStore.Put("imports/"+sb.ID, "manifest.json", []byte(`[]`))
			if err := s.CreateImportJob(ctx, &models.ImportJob{
				ID:          "job-" + uuid.NewString(),
				SongbookID:  sb.ID,
				Status:      models.ImportJobCompleted,
				ManifestKey: manifestKey,
			}); err != nil {
				t.Fatalf("CreateImportJob: %v", err)
			}
			versionID := "sha256:ver-" + uuid.NewString()
			helperCreateAndReadyVersion(t, s, versionID, 10)
			assetStore.inMemoryAssetStore.Put("catalogs/"+versionID, "manifest.json", []byte(`{}`))
			if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
				t.Fatalf("PublishCatalogVersion: %v", err)
			}

			ev := &models.Event{
				ID:         "ev-" + uuid.NewString(),
				OwnerID:    user.ID,
				SongbookID: sb.ID,
				Name:       "Boundary Event",
				JoinCode:   "BD" + uuid.NewString()[:4],
				Status:     models.EventStatusActive,
			}
			if err := s.CreateEvent(ctx, ev); err != nil {
				t.Fatalf("CreateEvent: %v", err)
			}
			if err := s.CreateRequest(ctx, &models.SongRequest{
				ID:         "req-" + uuid.NewString(),
				EventID:    ev.ID,
				SongbookID: sb.ID,
				SingerName: "Bob",
				SongTitle:  "Track",
				Status:     models.StatusPending,
			}); err != nil {
				t.Fatalf("CreateRequest: %v", err)
			}

			loginRes := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/login",
				`{"email":"`+user.Email+`","password":"password12345"}`, "", "")
			if loginRes.Code != http.StatusOK {
				t.Fatalf("login = %d: %s", loginRes.Code, loginRes.Body.String())
			}
			var loginData struct {
				AccessToken string `json:"access_token"`
			}
			_ = json.Unmarshal(loginRes.Body.Bytes(), &loginData)

			// Inject failure at the target boundary
			switch boundary {
			case "delete_import_assets":
				assetStore.failDeleteKey = true
			case "purge_catalog_version":
				assetStore.failDeletePrefix = true
			default:
				faultStore.failStep = boundary
			}

			res1 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, loginData.AccessToken, "")
			if res1.Code != http.StatusInternalServerError {
				t.Fatalf("boundary %s: status = %d, want 500: %s", boundary, res1.Code, res1.Body.String())
			}
			var errPayload map[string]interface{}
			_ = json.Unmarshal(res1.Body.Bytes(), &errPayload)
			if errPayload["step"] != boundary {
				t.Fatalf("boundary %s: step = %v, want %s", boundary, errPayload["step"], boundary)
			}

			if boundary != "begin_deletion" {
				// After BeginAccountDeletion commits, the account is durably fenced in deleting state
				persisted, err := s.GetUser(ctx, user.ID)
				if err != nil || !persisted.Deleting || persisted.DeletionStep != boundary {
					t.Fatalf("boundary %s: expected durable deleting state with step=%s, got %+v, %v", boundary, boundary, persisted, err)
				}
			}

			// Clear fault and verify recovery succeeds
			faultStore.failStep = ""
			assetStore.failDeleteKey = false
			assetStore.failDeletePrefix = false

			if boundary == "purge_catalog_version" {
				// Exercise unattended operator/service recovery via RecoverDeletingAccounts
				svc := account.NewService(faultStore, faultStore, faultStore, faultStore, faultStore, faultStore, &dummyCatalogReader{}, faultStore, faultStore, assetStore).
					WithDeveloperKeys(faultStore).WithAccess(faultStore)
				completed, recErr := svc.RecoverDeletingAccounts(ctx)
				if recErr != nil || completed < 1 {
					t.Fatalf("RecoverDeletingAccounts = (%d, %v), want (>=1, nil)", completed, recErr)
				}
			} else {
				delToken := res1.Header().Get("X-Deletion-Token")
				res2 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, loginData.AccessToken, delToken)
				if res2.Code != http.StatusNoContent {
					t.Fatalf("boundary %s retry: status = %d, want 204: %s", boundary, res2.Code, res2.Body.String())
				}
			}

			if _, err := s.GetUser(ctx, user.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("boundary %s: expected user purged after retry, got %v", boundary, err)
			}
			if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("boundary %s: expected songbook purged after retry, got %v", boundary, err)
			}
			if _, err := s.GetCatalogVersion(ctx, versionID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("boundary %s: expected catalog version purged after retry, got %v", boundary, err)
			}
			if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("boundary %s: expected event purged after retry, got %v", boundary, err)
			}
		})
	}
}

func TestDynamoAccountDeletion_ConcurrentInitializationAndProgress(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	user := &models.User{
		ID:          "u-concurrent-" + uuid.NewString(),
		Email:       "concurrent-" + uuid.NewString() + "@example.com",
		DisplayName: "Concurrent User",
		IsHost:      true,
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 1. Concurrent BeginAccountDeletion callers must converge on one deletion epoch
	// and record set union of pending catalog versions without lost updates.
	const numCallers = 10
	var wg sync.WaitGroup
	epochs := make([]string, numCallers)
	errs := make([]error, numCallers)

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			verID := fmt.Sprintf("sha256:ver-%d-%s", idx, uuid.NewString()[:8])
			u, err := s.BeginAccountDeletion(ctx, user.ID, []string{verID})
			if err != nil {
				errs[idx] = err
				return
			}
			epochs[idx] = u.DeletionEpoch
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("BeginAccountDeletion [%d] failed: %v", i, err)
		}
	}

	canonicalEpoch := epochs[0]
	if canonicalEpoch == "" {
		t.Fatal("expected non-empty canonical deletion epoch")
	}
	for i, ep := range epochs {
		if ep != canonicalEpoch {
			t.Fatalf("epoch mismatch at [%d]: %s != canonical %s", i, ep, canonicalEpoch)
		}
	}

	persisted, err := s.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !persisted.Deleting {
		t.Fatal("expected user to be marked deleting")
	}
	if persisted.DeletionEpoch != canonicalEpoch {
		t.Fatalf("persisted deletion epoch %s != %s", persisted.DeletionEpoch, canonicalEpoch)
	}
	if len(persisted.PendingCatalogVersions) != numCallers {
		t.Fatalf("expected all %d catalog versions preserved in pending set, got %d", numCallers, len(persisted.PendingCatalogVersions))
	}

	// 2. Calling DeleteUser must be rejected while pending catalog versions remain
	if err := s.DeleteUser(ctx, user.ID); err == nil {
		t.Fatal("expected DeleteUser to fail while pending catalog versions exist")
	}

	// 3. Concurrently resolve all pending catalog versions via RecordAccountDeletionProgress
	resolveErrs := make([]error, numCallers)
	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			vID := persisted.PendingCatalogVersions[idx]
			resolveErrs[idx] = s.RecordAccountDeletionProgress(ctx, user.ID, "purge_catalog_version", "", []string{vID})
		}(i)
	}
	wg.Wait()

	for i, err := range resolveErrs {
		if err != nil {
			t.Fatalf("RecordAccountDeletionProgress [%d] failed: %v", i, err)
		}
	}

	clearedUser, err := s.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser after resolution: %v", err)
	}
	if len(clearedUser.PendingCatalogVersions) != 0 {
		t.Fatalf("expected 0 pending catalog versions remaining, got %d", len(clearedUser.PendingCatalogVersions))
	}

	// 4. Now DeleteUser must succeed
	if err := s.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser after all versions resolved: %v", err)
	}

	// Idempotent repeat returns nil
	if err := s.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("idempotent repeated DeleteUser: %v", err)
	}
}

func TestDynamoAccountDeletion_InFlightWriterCommitTimeFence(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	user := &models.User{
		ID:          "u-inflight-" + uuid.NewString(),
		Email:       "inflight-" + uuid.NewString() + "@example.com",
		DisplayName: "In Flight User",
		IsHost:      true,
		AuthEpoch:   "epoch-init",
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Mark user as deleting
	if _, err := s.BeginAccountDeletion(ctx, user.ID, nil); err != nil {
		t.Fatalf("BeginAccountDeletion: %v", err)
	}

	// In-flight writer 1: CreateAuthSession
	err := s.CreateAuthSession(ctx, &models.AuthSession{
		TokenHash: []byte("hash-session-test"),
		UserID:    user.ID,
		AuthEpoch: "epoch-init",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("CreateAuthSession on deleting user: got %v, want store.ErrAccountDeleting", err)
	}

	// In-flight writer 2: SavePasskeyCredential
	err = s.SavePasskeyCredential(ctx, &models.PasskeyCredential{
		ID:           "pk-" + uuid.NewString(),
		UserID:       user.ID,
		PublicKey:    []byte("pk-pub"),
		FriendlyName: "Fenced Passkey",
	})
	if !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("SavePasskeyCredential on deleting user: got %v, want store.ErrAccountDeleting", err)
	}

	// In-flight writer 3: CreateDeveloperKey
	rawDevKey := "mk_live_" + uuid.NewString()
	err = s.CreateDeveloperKey(ctx, rawDevKey, &models.DeveloperKey{
		OwnerID:   user.ID,
		Name:      "Fenced Key",
		KeyPrefix: rawDevKey[:12],
		Scopes:    []string{handlers.DeveloperScopeCatalogsWrite},
	})
	if !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("CreateDeveloperKey on deleting user: got %v, want store.ErrAccountDeleting", err)
	}

	// In-flight writer 4: CreateSongbook
	err = s.CreateSongbook(ctx, &models.Songbook{
		ID:      "sb-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Fenced Songbook",
	})
	if !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("CreateSongbook on deleting user: got %v, want store.ErrAccountDeleting", err)
	}

	// In-flight writer 5: CreateEvent
	err = s.CreateEvent(ctx, &models.Event{
		ID:       "ev-" + uuid.NewString(),
		OwnerID:  user.ID,
		Name:     "Fenced Event",
		JoinCode: "FC" + uuid.NewString()[:4],
		Status:   models.EventStatusActive,
	})
	if !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("CreateEvent on deleting user: got %v, want store.ErrAccountDeleting", err)
	}
}

func TestDynamoAccountDeletion_PreResetTokenDenial(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()
	authSecret := "m7-prereset-signing-secret-with-at-least-32-bytes"

	faultStore := &faultInjectingAccountStore{Store: s}
	assetStore := &faultInjectingManifestWriter{inMemoryAssetStore: newInMemoryAssetStore()}

	router, err := app.NewRouter(config.Config{
		Environment:   config.Development,
		AuthSecret:    authSecret,
		AuthRateLimit: 1000,
	}, app.Dependencies{
		Auth:           faultStore,
		Passkeys:       faultStore,
		Songbooks:      faultStore,
		Requests:       faultStore,
		Events:         faultStore,
		Catalogs:       faultStore,
		CatalogReader:  &dummyCatalogReader{},
		CatalogChanges: faultStore,
		ImportJobs:     faultStore,
		ManifestWriter: assetStore,
		DeveloperKeys:  faultStore,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// 1. Register user -> issue Token 1 (epoch 1)
	email := "prereset-" + uuid.NewString() + "@example.com"
	reg := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"email":"`+email+`","password":"initialpassword123","display_name":"PreReset User","is_host":true}`, "", "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d: %s", reg.Code, reg.Body.String())
	}
	var sess1 struct {
		AccessToken string       `json:"access_token"`
		User        *models.User `json:"user"`
	}
	_ = json.Unmarshal(reg.Body.Bytes(), &sess1)
	token1 := sess1.AccessToken

	// 2. Perform password reset -> advances user AuthEpoch (epoch 2)
	authHandler, _ := handlers.NewAuthHandler(faultStore, authSecret)
	userRec, err := s.GetUser(ctx, sess1.User.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	resetToken, err := authHandler.PasswordReset.IssueResetToken(userRec)
	if err != nil {
		t.Fatalf("IssueResetToken: %v", err)
	}
	resetRes := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password",
		`{"token":"`+resetToken+`","password":"newpassword456"}`, "", "")
	if resetRes.Code != http.StatusOK {
		t.Fatalf("reset password: %d: %s", resetRes.Code, resetRes.Body.String())
	}

	// 3. Login with new password -> issue Token 2 (epoch 2)
	loginRes := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"newpassword456"}`, "", "")
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login with new password: %d: %s", loginRes.Code, loginRes.Body.String())
	}
	var sess2 struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(loginRes.Body.Bytes(), &sess2)
	token2 := sess2.AccessToken

	// 4. Begin account deletion with Token 2, but inject fault after begin_deletion
	faultStore.failStep = "delete_events"
	ev := &models.Event{ID: "ev-" + uuid.NewString(), OwnerID: sess1.User.ID, Name: "Ev", JoinCode: "PR" + uuid.NewString()[:4], Status: models.EventStatusActive}
	_ = s.CreateEvent(ctx, ev)

	delRes1 := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, token2, "")
	if delRes1.Code != http.StatusInternalServerError {
		t.Fatalf("attempt 1: %d: %s", delRes1.Code, delRes1.Body.String())
	}

	// Stored user now has deleting = true and deletion_auth_epoch matching epoch 2
	persisted, err := s.GetUser(ctx, sess1.User.ID)
	if err != nil || !persisted.Deleting {
		t.Fatalf("expected deleting user, got %+v, %v", persisted, err)
	}

	// 5. Attempt DELETE /api/v1/auth/me presenting Token 1 (revoked before deletion began!)
	// Must be rejected with 401 Unauthorized
	preresetDel := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, token1, "")
	if preresetDel.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE /auth/me with pre-reset token = %d, want 401: %s", preresetDel.Code, preresetDel.Body.String())
	}

	// 6. Token 2 (the initiating authority) or X-Deletion-Token is accepted
	faultStore.failStep = ""
	delToken := delRes1.Header().Get("X-Deletion-Token")
	if delToken == "" {
		t.Fatal("expected X-Deletion-Token")
	}
	retryDel := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, token2, delToken)
	if retryDel.Code != http.StatusNoContent {
		t.Fatalf("retry with valid initiating token = %d, want 204: %s", retryDel.Code, retryDel.Body.String())
	}
}

func TestDynamoAccountDeletion_LostResponseOperatorRecovery(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()
	authSecret := "m7-lostresp-signing-secret-with-at-least-32-bytes"

	faultStore := &faultInjectingAccountStore{Store: s}
	assetStore := &faultInjectingManifestWriter{inMemoryAssetStore: newInMemoryAssetStore()}

	router, err := app.NewRouter(config.Config{
		Environment:   config.Development,
		AuthSecret:    authSecret,
		AuthRateLimit: 1000,
	}, app.Dependencies{
		Auth:           faultStore,
		Passkeys:       faultStore,
		Songbooks:      faultStore,
		Requests:       faultStore,
		Events:         faultStore,
		Catalogs:       faultStore,
		CatalogReader:  &dummyCatalogReader{},
		CatalogChanges: faultStore,
		ImportJobs:     faultStore,
		ManifestWriter: assetStore,
		DeveloperKeys:  faultStore,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	email := "lostresp-" + uuid.NewString() + "@example.com"
	reg := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"email":"`+email+`","password":"password12345","display_name":"Lost Response User","is_host":true}`, "", "")
	var sess struct {
		AccessToken string       `json:"access_token"`
		User        *models.User `json:"user"`
	}
	_ = json.Unmarshal(reg.Body.Bytes(), &sess)

	sb := &models.Songbook{ID: "sb-lost-" + uuid.NewString(), OwnerID: sess.User.ID, Name: "Lost Book"}
	_ = s.CreateSongbook(ctx, sb)
	vID := "sha256:v-lost-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vID, 10)
	assetStore.inMemoryAssetStore.Put("catalogs/"+vID, "manifest.json", []byte(`{}`))
	_ = s.PublishCatalogVersion(ctx, sb.ID, vID)

	ev := &models.Event{ID: "ev-lost-" + uuid.NewString(), OwnerID: sess.User.ID, SongbookID: sb.ID, Name: "Ev", JoinCode: "LR" + uuid.NewString()[:4], Status: models.EventStatusActive}
	_ = s.CreateEvent(ctx, ev)

	// Inject S3 failure on catalog purge
	assetStore.failDeletePrefix = true
	res := doRouterJSON(t, router, http.MethodDelete, "/api/v1/auth/me", `{"confirm":"DELETE"}`, sess.AccessToken, "")
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("delete attempt: %d, want 500", res.Code)
	}

	// Client lost response / disconnected! No deletion token held by client.
	// User credentials are gone.
	loginCheck := doRouterJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"password12345"}`, "", "")
	if loginCheck.Code != http.StatusUnauthorized {
		t.Fatalf("login during deleting state = %d, want 401", loginCheck.Code)
	}

	// Operator discovers account via ListDeletingUsers
	deletingUsers, err := s.ListDeletingUsers(ctx)
	if err != nil {
		t.Fatalf("ListDeletingUsers: %v", err)
	}
	var found *models.User
	for _, u := range deletingUsers {
		if u.ID == sess.User.ID {
			found = u
			break
		}
	}
	if found == nil {
		t.Fatalf("expected user %s in deleting accounts list", sess.User.ID)
	}

	// Operator clears transient infrastructure fault and triggers recovery
	assetStore.failDeletePrefix = false
	opSvc := account.NewService(s, s, s, s, s, s, &dummyCatalogReader{}, s, s, assetStore).
		WithDeveloperKeys(s).WithAccess(s)
	if err := opSvc.ResumeAccountDeletion(ctx, sess.User.ID); err != nil {
		t.Fatalf("ResumeAccountDeletion: %v", err)
	}

	// Verify all resources are cleanly finalized
	if _, err := s.GetUser(ctx, sess.User.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected user deleted, got %v", err)
	}
	if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected songbook deleted, got %v", err)
	}
	if _, err := s.GetCatalogVersion(ctx, vID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected catalog version deleted, got %v", err)
	}
	if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected event deleted, got %v", err)
	}

	// Operator listing confirms account is no longer deleting
	afterUsers, err := s.ListDeletingUsers(ctx)
	if err != nil {
		t.Fatalf("ListDeletingUsers after recovery: %v", err)
	}
	for _, u := range afterUsers {
		if u.ID == sess.User.ID {
			t.Fatalf("user %s still in deleting list after operator recovery", sess.User.ID)
		}
	}
}

func TestDynamoAccountDeletion_StaleCatalogCleanupAttempt(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	// 1. Create catalog version v1 in ready status
	v1ID := "sha256:v1-stale-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 100)

	// User A owns songbook A referencing v1
	userA := &models.User{ID: "user-a-" + uuid.NewString(), Email: "usera-" + uuid.NewString() + "@example.com", DisplayName: "User A"}
	_ = s.CreateUser(ctx, userA)
	sbA := &models.Songbook{ID: "sb-a-" + uuid.NewString(), OwnerID: userA.ID, Name: "Book A"}
	_ = s.CreateSongbook(ctx, sbA)
	_ = s.PublishCatalogVersion(ctx, sbA.ID, v1ID)

	// User B owns songbook B (will adopt reclaimed version later)
	userB := &models.User{ID: "user-b-" + uuid.NewString(), Email: "userb-" + uuid.NewString() + "@example.com", DisplayName: "User B"}
	_ = s.CreateUser(ctx, userB)
	sbB := &models.Songbook{ID: "sb-b-" + uuid.NewString(), OwnerID: userB.ID, Name: "Book B"}
	_ = s.CreateSongbook(ctx, sbB)

	// 2. User A begins account deletion, songbook A is deleted
	_, err := s.BeginAccountDeletion(ctx, userA.ID, []string{v1ID})
	if err != nil {
		t.Fatalf("BeginAccountDeletion: %v", err)
	}
	_ = s.DeleteSongbook(ctx, sbA.ID)

	// 3. Worker 1 reserves v1 for deletion with a very short lease (40ms)
	w1Ver, err := s.ReserveCatalogVersionForDeletion(ctx, v1ID, 40*time.Millisecond)
	if err != nil {
		t.Fatalf("ReserveCatalogVersionForDeletion: %v", err)
	}
	if w1Ver.LeaseToken == "" {
		t.Fatal("expected non-empty lease token for Worker 1")
	}

	// 4. Worker 1 pauses; lease expires
	time.Sleep(60 * time.Millisecond)

	// 5. Concurrent builder reclaims v1
	reclaimed, err := s.ReclaimCatalogVersion(ctx, v1ID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReclaimCatalogVersion: %v", err)
	}
	if reclaimed.Status != models.CatalogVersionBuilding {
		t.Fatalf("expected status building after reclaim, got %s", reclaimed.Status)
	}

	// 6. Stale Worker 1 wakes up and attempts to commit DeleteCatalogVersion with old lease token
	// Must be rejected!
	staleErr := s.DeleteCatalogVersion(ctx, v1ID, w1Ver.LeaseToken)
	if staleErr == nil {
		t.Fatal("expected stale Worker 1 DeleteCatalogVersion to be rejected, got nil")
	}

	// 7. Builder completes compilation and publishes v1 to User B's songbook B
	_, err = s.MarkCatalogVersionReady(ctx, v1ID, "catalogs/epochs/"+v1ID+"/ep-new", 120, reclaimed.LeaseToken)
	if err != nil {
		t.Fatalf("builder mark ready: %v", err)
	}
	if err := s.PublishCatalogVersion(ctx, sbB.ID, v1ID); err != nil {
		t.Fatalf("PublishCatalogVersion to sbB: %v", err)
	}

	// 8. Subsequent account deletion retry for User A:
	// PurgeCatalogVersion detects v1 is referenced by User B, returns ErrCatalogVersionInUse,
	// preserves v1, removes v1 from pending_catalog_versions, and finalizes User A cleanly!
	assetStore := newInMemoryAssetStore()
	svc := account.NewService(s, s, s, s, s, s, &dummyCatalogReader{}, s, s, assetStore).
		WithDeveloperKeys(s).WithAccess(s)
	if err := svc.DeleteAccount(ctx, userA); err != nil {
		t.Fatalf("DeleteAccount retry for User A: %v", err)
	}

	// Verify User A is deleted
	if _, err := s.GetUser(ctx, userA.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected User A deleted, got %v", err)
	}
	// Verify v1 and User B remain intact
	activeV1, err := s.GetCatalogVersion(ctx, v1ID)
	if err != nil || activeV1.Status != models.CatalogVersionReady {
		t.Fatalf("expected active v1 in ready status, got %+v, %v", activeV1, err)
	}
	bookB, err := s.GetSongbook(ctx, sbB.ID)
	if err != nil || bookB.CatalogVersionID != v1ID {
		t.Fatalf("expected book B referencing v1, got %+v, %v", bookB, err)
	}
}

func TestDynamoAccountDeletion_FinalizationRetryExhaustionFailsExplicitly(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	user := &models.User{
		ID:          "user-exhaust-" + uuid.NewString(),
		Email:       "exhaust-" + uuid.NewString() + "@example.com",
		DisplayName: "Exhaust User",
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	faultStore := &faultInjectingAccountStore{
		Store:    s,
		failStep: "delete_user",
	}
	assetStore := newInMemoryAssetStore()
	svc := account.NewService(
		faultStore, faultStore, faultStore, faultStore, faultStore, faultStore,
		&dummyCatalogReader{},
		faultStore, faultStore, assetStore,
	).WithDeveloperKeys(faultStore).WithAccess(faultStore)

	// Attempt deletion where every DeleteUser attempt fails
	err := svc.DeleteAccount(ctx, user)
	if err == nil {
		t.Fatal("expected DeleteAccount to fail after finalization retry exhaustion, got nil")
	}

	var cascadeErr *account.CascadeError
	if !errors.As(err, &cascadeErr) {
		t.Fatalf("expected CascadeError, got: %v", err)
	}
	if cascadeErr.Step != "delete_user" {
		t.Fatalf("expected CascadeError step 'delete_user', got %s", cascadeErr.Step)
	}

	// Confirm user deletion was NOT falsely reported or confirmed:
	// User record MUST still exist in DynamoDB in deleting status.
	persisted, err := s.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser after failed finalization: %v", err)
	}
	if persisted == nil || !persisted.Deleting {
		t.Fatalf("expected user to still exist with Deleting=true, got %+v", persisted)
	}
}

func TestDynamoAccountDeletion_DelayedChildCommitsFencedAfterParentDeletion(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	// Setup: create user, songbook, and event
	user := &models.User{
		ID:          "user-delayed-" + uuid.NewString(),
		Email:       "delayed-" + uuid.NewString() + "@example.com",
		DisplayName: "Delayed User",
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sb := &models.Songbook{
		ID:      "sb-delayed-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Delayed Songbook",
	}
	if err := s.CreateSongbook(ctx, sb); err != nil {
		t.Fatalf("CreateSongbook: %v", err)
	}

	ev := &models.Event{
		ID:       "ev-delayed-" + uuid.NewString(),
		OwnerID:  user.ID,
		Name:     "Delayed Event",
		JoinCode: "DE" + uuid.NewString()[:4],
		Status:   models.EventStatusActive,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	// In-flight writers preflight-read parent resources here (they exist and are active).
	// Now account deletion executes and cascades, deleting user, songbook, and event.
	assetStore := newInMemoryAssetStore()
	svc := account.NewService(s, s, s, s, s, s, &dummyCatalogReader{}, s, s, assetStore).
		WithDeveloperKeys(s).WithAccess(s)
	if err := svc.DeleteAccount(ctx, user); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	// Verify parent user and resources are gone
	if _, err := s.GetUser(ctx, user.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected user deleted, got: %v", err)
	}
	if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected songbook deleted, got: %v", err)
	}
	if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected event deleted, got: %v", err)
	}

	// Delayed child write 1: PutCatalogChange against deleted songbook
	changeErr := s.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: sb.ID,
		SongID:     "song-delayed-1",
		UpdatedAt:  time.Now().UTC(),
	})
	if !errors.Is(changeErr, os.ErrNotExist) {
		t.Fatalf("PutCatalogChange on deleted songbook: got %v, want os.ErrNotExist", changeErr)
	}

	// Delayed child write 2: CreateImportJob against deleted songbook
	importJobErr := s.CreateImportJob(ctx, &models.ImportJob{
		ID:         "job-delayed-" + uuid.NewString(),
		SongbookID: sb.ID,
		Status:     models.ImportJobQueued,
	})
	if !errors.Is(importJobErr, os.ErrNotExist) {
		t.Fatalf("CreateImportJob on deleted songbook: got %v, want os.ErrNotExist", importJobErr)
	}

	// Delayed child write 3: CreateRequest against deleted event
	reqErr := s.CreateRequest(ctx, &models.SongRequest{
		ID:         "req-" + uuid.NewString(),
		EventID:    ev.ID,
		SingerName: "Singer",
		SongTitle:  "Title",
		Status:     models.StatusPending,
	})
	if !errors.Is(reqErr, os.ErrNotExist) {
		t.Fatalf("CreateRequest on deleted event: got %v, want os.ErrNotExist", reqErr)
	}

	// Delayed child write 5: CreateSongbook against deleted owner user
	newSBErr := s.CreateSongbook(ctx, &models.Songbook{
		ID:      "sb-new-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Orphan Songbook",
	})
	if !errors.Is(newSBErr, os.ErrNotExist) {
		t.Fatalf("CreateSongbook on deleted user: got %v, want os.ErrNotExist", newSBErr)
	}

	// Delayed child write 6: CreateEvent against deleted owner user
	newEvErr := s.CreateEvent(ctx, &models.Event{
		ID:       "ev-new-" + uuid.NewString(),
		OwnerID:  user.ID,
		Name:     "Orphan Event",
		JoinCode: "OE" + uuid.NewString()[:4],
		Status:   models.EventStatusActive,
	})
	if !errors.Is(newEvErr, os.ErrNotExist) {
		t.Fatalf("CreateEvent on deleted user: got %v, want os.ErrNotExist", newEvErr)
	}
}

type pausingAccountStore struct {
	*Store
	afterListRequests       func(ctx context.Context, eventID string)
	afterListCatalogChanges func(ctx context.Context, songbookID string)
}

func (p *pausingAccountStore) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	reqs, err := p.Store.ListRequestsByEvent(ctx, eventID)
	if err == nil && p.afterListRequests != nil {
		p.afterListRequests(ctx, eventID)
	}
	return reqs, err
}

func (p *pausingAccountStore) ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	changes, err := p.Store.ListCatalogChanges(ctx, songbookID)
	if err == nil && p.afterListCatalogChanges != nil {
		p.afterListCatalogChanges(ctx, songbookID)
	}
	return changes, err
}

func TestDynamoAccountDeletion_PauseAfterEnumeration_FencesDelayedChildCommits_LeavesNoOrphans(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	// 1. Setup valid non-deleting user, songbook, and active event
	user := &models.User{
		ID:          "user-pause-" + uuid.NewString(),
		Email:       "pause-" + uuid.NewString() + "@example.com",
		DisplayName: "Pause Test User",
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sb := &models.Songbook{
		ID:      "sb-pause-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Pause Songbook",
	}
	if err := s.CreateSongbook(ctx, sb); err != nil {
		t.Fatalf("CreateSongbook: %v", err)
	}

	ev := &models.Event{
		ID:       "ev-pause-" + uuid.NewString(),
		OwnerID:  user.ID,
		Name:     "Pause Event",
		JoinCode: "PE" + uuid.NewString()[:4],
		Status:   models.EventStatusActive,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	// 2. Populate initial child resources under songbook and event
	initialChange := &models.CatalogChange{
		SongbookID: sb.ID,
		SongID:     "song-init-" + uuid.NewString(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.PutCatalogChange(ctx, initialChange); err != nil {
		t.Fatalf("PutCatalogChange: %v", err)
	}

	initialJob := &models.ImportJob{
		ID:         "job-init-" + uuid.NewString(),
		SongbookID: sb.ID,
		Status:     models.ImportJobQueued,
	}
	if err := s.CreateImportJob(ctx, initialJob); err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}

	initialReq := &models.SongRequest{
		ID:         "req-init-" + uuid.NewString(),
		EventID:    ev.ID,
		SingerName: "Initial Singer",
		SongTitle:  "Initial Song",
		Status:     models.StatusPending,
	}
	if err := s.CreateRequest(ctx, initialReq); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// 3. Configure pause hooks after child enumeration
	pausedAtReqs := make(chan struct{})
	resumeReqs := make(chan struct{})
	var pauseReqsOnce, resumeReqsOnce sync.Once

	pausedAtChanges := make(chan struct{})
	resumeChanges := make(chan struct{})
	var pauseChangesOnce, resumeChangesOnce sync.Once

	pausingStore := &pausingAccountStore{
		Store: s,
		afterListRequests: func(ctx context.Context, eventID string) {
			pauseReqsOnce.Do(func() {
				close(pausedAtReqs)
				<-resumeReqs
			})
		},
		afterListCatalogChanges: func(ctx context.Context, songbookID string) {
			pauseChangesOnce.Do(func() {
				close(pausedAtChanges)
				<-resumeChanges
			})
		},
	}

	assetStore := newInMemoryAssetStore()
	svc := account.NewService(
		pausingStore, pausingStore, pausingStore,
		pausingStore, pausingStore, pausingStore,
		&dummyCatalogReader{},
		pausingStore, pausingStore, assetStore,
	)

	deleteErrCh := make(chan error, 1)
	go func() {
		deleteErrCh <- svc.DeleteAccount(ctx, user)
	}()

	// 4. Wait for pause after request enumeration
	select {
	case <-pausedAtReqs:
	case err := <-deleteErrCh:
		t.Fatalf("DeleteAccount failed early before request pause: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for pause after request enumeration")
	}

	// Verify parent event still exists in DynamoDB with deleting barrier set
	persistedEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent while paused: %v", err)
	}
	if !persistedEv.Deleting {
		t.Fatalf("expected event deleting barrier = true, got %+v", persistedEv)
	}

	// Attempt delayed commits on event while parent still exists
	delayedReq := &models.SongRequest{
		ID:         "req-delayed-" + uuid.NewString(),
		EventID:    ev.ID,
		SingerName: "Delayed Singer",
		SongTitle:  "Delayed Song",
		Status:     models.StatusPending,
	}
	if createReqErr := s.CreateRequest(ctx, delayedReq); !errors.Is(createReqErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed CreateRequest: got %v, want ErrAccountDeleting", createReqErr)
	}

	initialReq.SingerName = "Updated Singer"
	if updateReqErr := s.UpdateRequestWithQueueVersion(ctx, initialReq, persistedEv.QueueVersion); !errors.Is(updateReqErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed UpdateRequestWithQueueVersion: got %v, want ErrAccountDeleting", updateReqErr)
	}

	if _, bumpErr := s.BumpQueueVersion(ctx, ev.ID); !errors.Is(bumpErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed BumpQueueVersion: got %v, want ErrAccountDeleting", bumpErr)
	}

	if restartErr := s.BeginQueueRestart(ctx, persistedEv); !errors.Is(restartErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed BeginQueueRestart: got %v, want ErrAccountDeleting", restartErr)
	}

	if delAtomicErr := s.DeleteEventRequestAtomic(ctx, ev.ID, initialReq.ID); !errors.Is(delAtomicErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed DeleteEventRequestAtomic: got %v, want ErrAccountDeleting", delAtomicErr)
	}

	// Resume request cleanup
	resumeReqsOnce.Do(func() { close(resumeReqs) })

	// 5. Wait for pause after catalog changes enumeration
	select {
	case <-pausedAtChanges:
	case err := <-deleteErrCh:
		t.Fatalf("DeleteAccount failed before catalog changes pause: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for pause after catalog changes enumeration")
	}

	// Verify parent songbook still exists in DynamoDB with deleting barrier set
	persistedSb, err := s.GetSongbook(ctx, sb.ID)
	if err != nil {
		t.Fatalf("GetSongbook while paused: %v", err)
	}
	if !persistedSb.Deleting {
		t.Fatalf("expected songbook deleting barrier = true, got %+v", persistedSb)
	}

	// Attempt delayed commits on songbook while parent still exists
	delayedChange := &models.CatalogChange{
		SongbookID: sb.ID,
		SongID:     "song-delayed-" + uuid.NewString(),
		UpdatedAt:  time.Now().UTC(),
	}
	if putChangeErr := s.PutCatalogChange(ctx, delayedChange); !errors.Is(putChangeErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed PutCatalogChange: got %v, want ErrAccountDeleting", putChangeErr)
	}

	delayedJob := &models.ImportJob{
		ID:         "job-delayed-" + uuid.NewString(),
		SongbookID: sb.ID,
		Status:     models.ImportJobQueued,
	}
	if createJobErr := s.CreateImportJob(ctx, delayedJob); !errors.Is(createJobErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed CreateImportJob: got %v, want ErrAccountDeleting", createJobErr)
	}

	if _, genErr := s.NextCatalogGeneration(ctx, sb.ID); !errors.Is(genErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed NextCatalogGeneration: got %v, want ErrAccountDeleting", genErr)
	}

	updateSb := &models.Songbook{
		ID:      sb.ID,
		OwnerID: user.ID,
		Name:    "Updated Songbook While Deleting",
	}
	if updateSbErr := s.UpdateSongbook(ctx, updateSb); !errors.Is(updateSbErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed UpdateSongbook: got %v, want ErrAccountDeleting", updateSbErr)
	}

	// Attempt delayed creation under deleting account
	delayedNewSB := &models.Songbook{
		ID:      "sb-new-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Orphan Songbook",
	}
	if createNewSBErr := s.CreateSongbook(ctx, delayedNewSB); !errors.Is(createNewSBErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed CreateSongbook under deleting user: got %v, want ErrAccountDeleting", createNewSBErr)
	}

	delayedNewEv := &models.Event{
		ID:       "ev-new-" + uuid.NewString(),
		OwnerID:  user.ID,
		Name:     "Orphan Event",
		JoinCode: "NE" + uuid.NewString()[:4],
		Status:   models.EventStatusActive,
	}
	if createNewEvErr := s.CreateEvent(ctx, delayedNewEv); !errors.Is(createNewEvErr, store.ErrAccountDeleting) {
		t.Fatalf("delayed CreateEvent under deleting user: got %v, want ErrAccountDeleting", createNewEvErr)
	}

	// Resume catalog changes and songbook cleanup
	resumeChangesOnce.Do(func() { close(resumeChanges) })

	// 6. Wait for account deletion to finish successfully
	select {
	case err := <-deleteErrCh:
		if err != nil {
			t.Fatalf("DeleteAccount failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for DeleteAccount to complete")
	}

	// 7. Verify successful deletion leaves NO ORPHANS in DynamoDB
	if _, err := s.GetUser(ctx, user.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected user deleted, got: %v", err)
	}
	if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected event deleted, got: %v", err)
	}
	if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected songbook deleted, got: %v", err)
	}

	remainingReqs, err := s.ListRequestsByEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("ListRequestsByEvent after deletion: %v", err)
	}
	if len(remainingReqs) > 0 {
		t.Fatalf("expected 0 requests remaining, found %d", len(remainingReqs))
	}

	remainingChanges, err := s.ListCatalogChanges(ctx, sb.ID)
	if err != nil {
		t.Fatalf("ListCatalogChanges after deletion: %v", err)
	}
	if len(remainingChanges) > 0 {
		t.Fatalf("expected 0 catalog changes remaining, found %d", len(remainingChanges))
	}

	allJobs, err := s.ListImportJobs(ctx)
	if err != nil {
		t.Fatalf("ListImportJobs after deletion: %v", err)
	}
	for _, job := range allJobs {
		if job.SongbookID == sb.ID {
			t.Fatalf("found orphan import job %s for deleted songbook %s", job.ID, sb.ID)
		}
	}
}

type racingPublicationStore struct {
	*Store
	afterListSongbooks       func(ctx context.Context, ownerID string)
	afterBeginSongbookDelete func(ctx context.Context, songbookID string)
	failStep                 string
}

func (r *racingPublicationStore) ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	books, err := r.Store.ListSongbooksByOwner(ctx, ownerID)
	if err == nil && r.afterListSongbooks != nil {
		r.afterListSongbooks(ctx, ownerID)
	}
	return books, err
}

func (r *racingPublicationStore) BeginSongbookDeletion(ctx context.Context, id string) (*models.Songbook, error) {
	sb, err := r.Store.BeginSongbookDeletion(ctx, id)
	if err == nil && r.afterBeginSongbookDelete != nil {
		r.afterBeginSongbookDelete(ctx, id)
	}
	return sb, err
}

func (r *racingPublicationStore) DeleteSongbook(ctx context.Context, id string) error {
	if r.failStep == "delete_songbooks" {
		return errors.New("injected fault at delete_songbooks")
	}
	return r.Store.DeleteSongbook(ctx, id)
}

func TestDynamoAccountDeletion_RacingPublicationBeforeBarrier_TrackedSurvivesAndPurgedOrPreserved(t *testing.T) {
	s := getSharedAccountTestStore(t)
	ctx := context.Background()

	t.Run("SoleCatalogVersion_PurgedOnResume", func(t *testing.T) {
		userA := &models.User{
			ID:          "user-race-sole-" + uuid.NewString(),
			Email:       "race-sole-" + uuid.NewString() + "@example.com",
			DisplayName: "Racing Sole User",
		}
		if err := s.CreateUser(ctx, userA); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		sbA := &models.Songbook{
			ID:      "sb-race-sole-" + uuid.NewString(),
			OwnerID: userA.ID,
			Name:    "Racing Sole Songbook",
		}
		if err := s.CreateSongbook(ctx, sbA); err != nil {
			t.Fatalf("CreateSongbook: %v", err)
		}

		vNewSole := "sha256:v-race-sole-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, vNewSole, 45)

		vAfterBarrier := "sha256:v-after-barrier-sole-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, vAfterBarrier, 15)

		assetStore := newInMemoryAssetStore()
		assetStore.Put("catalogs/"+vNewSole, "manifest.json", []byte(`{"sole": true}`))
		assetStore.Put("catalogs/"+vNewSole, "search.json.gz", []byte(`{"sole": true}`))

		var postBarrierPubErr error
		var publicationCommitted bool

		racingStore := &racingPublicationStore{
			Store:    s,
			failStep: "delete_songbooks",
			afterListSongbooks: func(ctx context.Context, ownerID string) {
				// Publication commits after initial songbook listing but before deletion barrier:
				if err := s.PublishCatalogVersion(ctx, sbA.ID, vNewSole); err != nil {
					t.Fatalf("PublishCatalogVersion before barrier failed: %v", err)
				}
				publicationCommitted = true
			},
			afterBeginSongbookDelete: func(ctx context.Context, songbookID string) {
				// Verify publication after the barrier is rejected:
				postBarrierPubErr = s.PublishCatalogVersion(ctx, songbookID, vAfterBarrier)
			},
		}

		svc := account.NewService(
			racingStore, racingStore, racingStore,
			racingStore, racingStore, racingStore,
			&dummyCatalogReader{},
			racingStore, racingStore, assetStore,
		)

		// 1. Initial deletion attempt fails at delete_songbooks after setting barrier and persisting catalog version
		err := svc.DeleteAccount(ctx, userA)
		if err == nil {
			t.Fatal("expected DeleteAccount to fail at injected delete_songbooks step, got nil")
		}
		var cascadeErr *account.CascadeError
		if !errors.As(err, &cascadeErr) || cascadeErr.Step != "delete_songbooks" {
			t.Fatalf("expected CascadeError step delete_songbooks, got: %v", err)
		}

		if !publicationCommitted {
			t.Fatal("expected racing publication to commit before barrier")
		}

		// 2. Verify publication after barrier was rejected:
		if postBarrierPubErr == nil {
			t.Fatal("expected publication after barrier to fail, but it succeeded")
		}
		if !errors.Is(postBarrierPubErr, store.ErrAccountDeleting) && !errors.Is(postBarrierPubErr, store.ErrSongbookDeleting) {
			t.Fatalf("expected ErrAccountDeleting or ErrSongbookDeleting for post-barrier publication, got: %v", postBarrierPubErr)
		}

		// 3. Verify the newly published version is durably tracked and survives an interrupted cascade as pending cleanup:
		persistedUser, err := s.GetUser(ctx, userA.ID)
		if err != nil {
			t.Fatalf("GetUser after interrupted cascade: %v", err)
		}
		if !persistedUser.Deleting {
			t.Fatalf("expected user Deleting=true, got %+v", persistedUser)
		}
		found := false
		for _, v := range persistedUser.PendingCatalogVersions {
			if v == vNewSole {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s in persisted PendingCatalogVersions, got: %v", vNewSole, persistedUser.PendingCatalogVersions)
		}

		// 4. Resume deletion to completion:
		racingStore.failStep = ""
		racingStore.afterListSongbooks = nil       // Do not attempt duplicate racing publication on resume
		racingStore.afterBeginSongbookDelete = nil // Barrier is already set

		if err := svc.DeleteAccount(ctx, userA); err != nil {
			t.Fatalf("resumed DeleteAccount failed: %v", err)
		}

		// 5. Verify sole version was subsequently purged from DynamoDB and S3:
		_, err = s.GetCatalogVersion(ctx, vNewSole)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected catalog version %s purged (os.ErrNotExist), got: %v", vNewSole, err)
		}
		if assetStore.Has("catalogs/"+vNewSole, "manifest.json") {
			t.Fatalf("expected assets for sole catalog version %s purged from asset store", vNewSole)
		}

		// Verify user and songbook are deleted:
		if _, err := s.GetUser(ctx, userA.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected user deleted, got: %v", err)
		}
		if _, err := s.GetSongbook(ctx, sbA.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected songbook deleted, got: %v", err)
		}
	})

	t.Run("SharedCatalogVersion_PreservedOnResume", func(t *testing.T) {
		userB := &models.User{
			ID:          "user-race-share-b-" + uuid.NewString(),
			Email:       "race-share-b-" + uuid.NewString() + "@example.com",
			DisplayName: "User B Active",
		}
		if err := s.CreateUser(ctx, userB); err != nil {
			t.Fatalf("create user B: %v", err)
		}
		sbB := &models.Songbook{
			ID:      "sb-race-share-b-" + uuid.NewString(),
			OwnerID: userB.ID,
			Name:    "User B Active Songbook",
		}
		if err := s.CreateSongbook(ctx, sbB); err != nil {
			t.Fatalf("create songbook B: %v", err)
		}

		userA := &models.User{
			ID:          "user-race-share-a-" + uuid.NewString(),
			Email:       "race-share-a-" + uuid.NewString() + "@example.com",
			DisplayName: "User A Deleting",
		}
		if err := s.CreateUser(ctx, userA); err != nil {
			t.Fatalf("create user A: %v", err)
		}
		sbA := &models.Songbook{
			ID:      "sb-race-share-a-" + uuid.NewString(),
			OwnerID: userA.ID,
			Name:    "User A Racing Songbook",
		}
		if err := s.CreateSongbook(ctx, sbA); err != nil {
			t.Fatalf("create songbook A: %v", err)
		}

		vShared := "sha256:v-race-shared-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, vShared, 100)

		// User B already points to vShared
		if err := s.PublishCatalogVersion(ctx, sbB.ID, vShared); err != nil {
			t.Fatalf("publish vShared to sbB: %v", err)
		}

		vAfterBarrier := "sha256:v-after-barrier-shared-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, vAfterBarrier, 25)

		assetStore := newInMemoryAssetStore()
		assetStore.Put("catalogs/"+vShared, "manifest.json", []byte(`{"shared": true}`))
		assetStore.Put("catalogs/"+vShared, "search.json.gz", []byte(`{"shared": true}`))

		var postBarrierPubErr error
		var publicationCommitted bool

		racingStore := &racingPublicationStore{
			Store:    s,
			failStep: "delete_songbooks",
			afterListSongbooks: func(ctx context.Context, ownerID string) {
				// Publication commits after initial songbook listing but before deletion barrier:
				if err := s.PublishCatalogVersion(ctx, sbA.ID, vShared); err != nil {
					t.Fatalf("PublishCatalogVersion before barrier failed: %v", err)
				}
				publicationCommitted = true
			},
			afterBeginSongbookDelete: func(ctx context.Context, songbookID string) {
				// Verify publication after the barrier is rejected:
				postBarrierPubErr = s.PublishCatalogVersion(ctx, songbookID, vAfterBarrier)
			},
		}

		svc := account.NewService(
			racingStore, racingStore, racingStore,
			racingStore, racingStore, racingStore,
			&dummyCatalogReader{},
			racingStore, racingStore, assetStore,
		)

		// 1. Initial deletion attempt fails at delete_songbooks
		err := svc.DeleteAccount(ctx, userA)
		if err == nil {
			t.Fatal("expected DeleteAccount to fail at injected delete_songbooks step, got nil")
		}
		var cascadeErr *account.CascadeError
		if !errors.As(err, &cascadeErr) || cascadeErr.Step != "delete_songbooks" {
			t.Fatalf("expected CascadeError step delete_songbooks, got: %v", err)
		}

		if !publicationCommitted {
			t.Fatal("expected racing publication to commit before barrier")
		}

		// 2. Verify publication after barrier was rejected:
		if postBarrierPubErr == nil {
			t.Fatal("expected publication after barrier to fail, but it succeeded")
		}
		if !errors.Is(postBarrierPubErr, store.ErrAccountDeleting) && !errors.Is(postBarrierPubErr, store.ErrSongbookDeleting) {
			t.Fatalf("expected ErrAccountDeleting or ErrSongbookDeleting for post-barrier publication, got: %v", postBarrierPubErr)
		}

		// 3. Verify the newly published version is durably tracked and survives an interrupted cascade as pending cleanup:
		persistedUser, err := s.GetUser(ctx, userA.ID)
		if err != nil {
			t.Fatalf("GetUser after interrupted cascade: %v", err)
		}
		if !persistedUser.Deleting {
			t.Fatalf("expected user Deleting=true, got %+v", persistedUser)
		}
		found := false
		for _, v := range persistedUser.PendingCatalogVersions {
			if v == vShared {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s in persisted PendingCatalogVersions, got: %v", vShared, persistedUser.PendingCatalogVersions)
		}

		// 4. Resume deletion to completion:
		racingStore.failStep = ""
		racingStore.afterListSongbooks = nil
		racingStore.afterBeginSongbookDelete = nil

		if err := svc.DeleteAccount(ctx, userA); err != nil {
			t.Fatalf("resumed DeleteAccount failed: %v", err)
		}

		// 5. Verify shared version is PRESERVED:
		vRecord, err := s.GetCatalogVersion(ctx, vShared)
		if err != nil {
			t.Fatalf("GetCatalogVersion for shared version %s: %v", vShared, err)
		}
		if vRecord.Status != models.CatalogVersionReady {
			t.Fatalf("expected shared catalog version status ready, got: %s", vRecord.Status)
		}
		if !assetStore.Has("catalogs/"+vShared, "manifest.json") {
			t.Fatalf("expected shared assets for %s preserved in asset store", vShared)
		}

		// Verify user A and sbA are deleted:
		if _, err := s.GetUser(ctx, userA.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected user A deleted, got: %v", err)
		}
		if _, err := s.GetSongbook(ctx, sbA.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected songbook A deleted, got: %v", err)
		}

		// Verify user B and sbB are undisturbed:
		persistedSBB, err := s.GetSongbook(ctx, sbB.ID)
		if err != nil {
			t.Fatalf("GetSongbook for sbB: %v", err)
		}
		if persistedSBB.CatalogVersionID != vShared {
			t.Fatalf("expected sbB to still point to %s, got: %s", vShared, persistedSBB.CatalogVersionID)
		}
	})
}
