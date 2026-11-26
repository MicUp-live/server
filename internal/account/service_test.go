package account

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type fakeAuthStore struct {
	users           map[string]*models.User
	deletedSessions string
	deletedUser     string
	beginDeleteErr  error
	sessionsErr     error
	deleteUserErr   error
	lastStep        string
	lastErr         string
	pendingVersions []string
}

func (f *fakeAuthStore) CreateUser(_ context.Context, u *models.User) error {
	if f.users == nil {
		f.users = make(map[string]*models.User)
	}
	copy := *u
	f.users[u.ID] = &copy
	return nil
}
func (f *fakeAuthStore) GetUser(_ context.Context, id string) (*models.User, error) {
	if f.users != nil {
		if u, ok := f.users[id]; ok {
			copy := *u
			return &copy, nil
		}
		return nil, os.ErrNotExist
	}
	return nil, nil
}
func (f *fakeAuthStore) GetUserByEmail(_ context.Context, _ string) (*models.User, error) {
	return nil, nil
}
func (f *fakeAuthStore) UpdateUser(_ context.Context, _ *models.User) error { return nil }
func (f *fakeAuthStore) ChangeUserEmail(_ context.Context, _, _, _ string, _ *models.User) error {
	return nil
}
func (f *fakeAuthStore) BeginAccountDeletion(_ context.Context, userID string, pendingCatalogVersions []string) (*models.User, error) {
	if f.beginDeleteErr != nil {
		return nil, f.beginDeleteErr
	}
	f.pendingVersions = mergeCatalogVersionIDs(f.pendingVersions, pendingCatalogVersions)
	if f.users != nil {
		u, ok := f.users[userID]
		if !ok {
			return nil, os.ErrNotExist
		}
		u.Deleting = true
		if u.DeletionEpoch == "" {
			u.DeletionEpoch = "del-epoch-" + userID
		}
		u.AuthEpoch = "deleting:" + u.DeletionEpoch
		u.PendingCatalogVersions = mergeCatalogVersionIDs(u.PendingCatalogVersions, pendingCatalogVersions)
		copy := *u
		return &copy, nil
	}
	return &models.User{
		ID:                     userID,
		Deleting:               true,
		DeletionEpoch:          "del-epoch-" + userID,
		AuthEpoch:              "deleting:del-epoch-" + userID,
		PendingCatalogVersions: f.pendingVersions,
	}, nil
}

func subtractPendingCatalogVersions(current, resolved []string) []string {
	resolvedSet := make(map[string]bool, len(resolved))
	for _, r := range resolved {
		resolvedSet[r] = true
	}
	var remaining []string
	for _, c := range current {
		if !resolvedSet[c] {
			remaining = append(remaining, c)
		}
	}
	return remaining
}

func (f *fakeAuthStore) RecordAccountDeletionProgress(_ context.Context, userID, step, errMsg string, resolvedCatalogVersions []string) error {
	f.lastStep = step
	f.lastErr = errMsg
	if len(resolvedCatalogVersions) > 0 {
		f.pendingVersions = subtractPendingCatalogVersions(f.pendingVersions, resolvedCatalogVersions)
	}
	if f.users != nil {
		if u, ok := f.users[userID]; ok {
			u.DeletionStep = step
			u.DeletionError = errMsg
			if len(resolvedCatalogVersions) > 0 {
				u.PendingCatalogVersions = subtractPendingCatalogVersions(u.PendingCatalogVersions, resolvedCatalogVersions)
			}
		}
	}
	return nil
}
func (f *fakeAuthStore) ListDeletingUsers(_ context.Context) ([]*models.User, error) {
	var out []*models.User
	for _, u := range f.users {
		if u.Deleting {
			copy := *u
			out = append(out, &copy)
		}
	}
	return out, nil
}
func (f *fakeAuthStore) DeleteUser(_ context.Context, id string) error {
	if f.deleteUserErr != nil {
		return f.deleteUserErr
	}
	f.deletedUser = id
	if f.users != nil {
		delete(f.users, id)
	}
	return nil
}
func (f *fakeAuthStore) CreateAuthSession(_ context.Context, _ *models.AuthSession) error {
	return nil
}
func (f *fakeAuthStore) ConsumeAuthSession(_ context.Context, _ []byte) (*models.User, error) {
	return nil, nil
}
func (f *fakeAuthStore) GetAuthSession(_ context.Context, _ string) (*models.AuthSession, error) {
	return nil, nil
}
func (f *fakeAuthStore) DeleteAuthSession(_ context.Context, _ string) error { return nil }
func (f *fakeAuthStore) DeleteAuthSessionsForUser(_ context.Context, userID string) error {
	if f.sessionsErr != nil {
		return f.sessionsErr
	}
	f.deletedSessions = userID
	return nil
}
func (f *fakeAuthStore) CleanExpiredSessions(_ context.Context) error { return nil }
func (f *fakeAuthStore) GetUserFavorites(_ context.Context, userID string) (*models.UserFavorites, error) {
	if f.users != nil {
		if u, ok := f.users[userID]; ok {
			songs := u.FavoriteSongs
			if songs == nil {
				songs = []models.FavoriteSong{}
			}
			artists := u.FavoriteArtists
			if artists == nil {
				artists = []models.FavoriteArtist{}
			}
			return &models.UserFavorites{Songs: songs, Artists: artists}, nil
		}
	}
	return &models.UserFavorites{Songs: []models.FavoriteSong{}, Artists: []models.FavoriteArtist{}}, nil
}
func (f *fakeAuthStore) SaveUserFavorites(_ context.Context, userID string, favs *models.UserFavorites) error {
	if f.users != nil {
		if u, ok := f.users[userID]; ok {
			if favs == nil {
				u.FavoriteSongs = []models.FavoriteSong{}
				u.FavoriteArtists = []models.FavoriteArtist{}
			} else {
				u.FavoriteSongs = favs.Songs
				u.FavoriteArtists = favs.Artists
			}
			return nil
		}
	}
	return nil
}

func TestAccountService_ExportAndCascade(t *testing.T) {
	authStore := &fakeAuthStore{}
	svc := NewService(authStore, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	user := &models.User{
		ID:        "user-1",
		Email:     "singer@example.com",
		CreatedAt: time.Now().UTC(),
	}

	// 1. Export Data
	export, err := svc.ExportData(ctx, user)
	if err != nil {
		t.Fatalf("unexpected export error: %v", err)
	}
	if export.User.ID != "user-1" || export.Service != "MicUp.live" {
		t.Fatalf("unexpected export output: %+v", export)
	}

	// 2. Delete Account Cascade
	if err := svc.DeleteAccount(ctx, user); err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
	if authStore.deletedSessions != "user-1" || authStore.deletedUser != "user-1" {
		t.Fatalf("expected user and sessions deleted, got sessions=%s, user=%s", authStore.deletedSessions, authStore.deletedUser)
	}

	// 3. Partial Failure CascadeError
	authStore.deleteUserErr = errors.New("dynamo timeout")
	err = svc.DeleteAccount(ctx, user)
	var cascadeErr *CascadeError
	if !errors.As(err, &cascadeErr) {
		t.Fatalf("expected CascadeError, got %v", err)
	}
	if cascadeErr.Step != "delete_user" || cascadeErr.Message != "failed to delete account" {
		t.Fatalf("unexpected cascade error step: %+v", cascadeErr)
	}
}

type fakePasskeyStore struct {
	creds   []*models.PasskeyCredential
	listErr error
}

func (f *fakePasskeyStore) CreateUserWithPasskey(_ context.Context, _ *models.User, _ *models.PasskeyCredential) error {
	return nil
}
func (f *fakePasskeyStore) SavePasskeyCredential(_ context.Context, _ *models.PasskeyCredential) error {
	return nil
}
func (f *fakePasskeyStore) GetPasskeyCredential(_ context.Context, _ string) (*models.PasskeyCredential, error) {
	return nil, nil
}
func (f *fakePasskeyStore) ListPasskeyCredentialsForUser(_ context.Context, userID string) ([]*models.PasskeyCredential, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var res []*models.PasskeyCredential
	for _, c := range f.creds {
		if c.UserID == userID {
			res = append(res, c)
		}
	}
	return res, nil
}
func (f *fakePasskeyStore) RecordPasskeyUse(_ context.Context, _ *models.PasskeyCredential, _ store.PasskeyUsage) error {
	panic("unexpected passkey login")
}
func (f *fakePasskeyStore) DeletePasskeyCredential(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakePasskeyStore) SaveWebAuthnSession(_ context.Context, _ *models.WebAuthnSession) error {
	return nil
}
func (f *fakePasskeyStore) ConsumeWebAuthnSession(_ context.Context, _ string) (*models.WebAuthnSession, error) {
	return nil, nil
}

type fakeSongbookStore struct {
	store.SongbookStore
	books   []*models.Songbook
	listErr error
}

func (f *fakeSongbookStore) CreateSongbook(_ context.Context, _ *models.Songbook) error { return nil }
func (f *fakeSongbookStore) UpdateSongbook(_ context.Context, _ *models.Songbook) error { return nil }
func (f *fakeSongbookStore) DeleteSongbook(_ context.Context, _ string) error           { return nil }
func (f *fakeSongbookStore) GetSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) GetPublicSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) GetSongbookBySlug(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return f.books, nil
}
func (f *fakeSongbookStore) ListSongbooksByOwner(_ context.Context, ownerID string) ([]*models.Songbook, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var res []*models.Songbook
	for _, b := range f.books {
		if b.OwnerID == ownerID {
			res = append(res, b)
		}
	}
	return res, nil
}
func (f *fakeSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}
func (f *fakeSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	for _, b := range f.books {
		if b.ID == id {
			b.Deleting = true
			copy := *b
			return &copy, nil
		}
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type fakeEventStore struct {
	store.EventStore
	events  []*models.Event
	listErr error
}

func (f *fakeEventStore) CreateEvent(_ context.Context, _ *models.Event) error { return nil }
func (f *fakeEventStore) GetEvent(_ context.Context, _ string) (*models.Event, error) {
	return nil, nil
}
func (f *fakeEventStore) GetEventByJoinCode(_ context.Context, _ string) (*models.Event, error) {
	return nil, nil
}
func (f *fakeEventStore) ListEventsByOwner(_ context.Context, ownerID string) ([]*models.Event, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var res []*models.Event
	for _, e := range f.events {
		if e.OwnerID == ownerID {
			res = append(res, e)
		}
	}
	return res, nil
}
func (f *fakeEventStore) ListEventsBySongbook(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}
func (f *fakeEventStore) UpdateEvent(_ context.Context, _ *models.Event) error { return nil }
func (f *fakeEventStore) UpdateEventWithVersion(_ context.Context, _ *models.Event, _ bool) error {
	return nil
}
func (f *fakeEventStore) DeleteEvent(_ context.Context, _ string) error        { return nil }
func (f *fakeEventStore) BeginEventDeletion(_ context.Context, _ string) error { return nil }

type fakeCatalogStore struct {
	versions map[string]*models.CatalogVersion
	getErr   error
	purgeErr error
}

func (f *fakeCatalogStore) CreateCatalogVersion(_ context.Context, _ *models.CatalogVersion) error {
	return nil
}
func (f *fakeCatalogStore) GetCatalogVersion(_ context.Context, id string) (*models.CatalogVersion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.versions[id], nil
}
func (f *fakeCatalogStore) MarkCatalogVersionReady(_ context.Context, _, _ string, _ int, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStore) FailCatalogVersion(_ context.Context, _, _ string, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStore) ReclaimCatalogVersion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeCatalogStore) PublishCatalogVersion(_ context.Context, _, _ string, _ ...store.PublicationOptions) error {
	return nil
}
func (f *fakeCatalogStore) DeleteCatalogVersion(_ context.Context, _ string, _ ...string) error {
	return nil
}
func (f *fakeCatalogStore) ReserveCatalogVersionForDeletion(_ context.Context, id string, _ time.Duration) (*models.CatalogVersion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v := f.versions[id]
	if v == nil {
		return nil, os.ErrNotExist
	}
	return v, nil
}
func (f *fakeCatalogStore) ReleaseCatalogVersionReservation(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeCatalogStore) IsCatalogVersionReferenced(_ context.Context, _ string, _ ...string) (bool, error) {
	return false, nil
}
func (f *fakeCatalogStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	if f.purgeErr != nil {
		return f.purgeErr
	}
	v := f.versions[id]
	if v == nil {
		return os.ErrNotExist
	}
	if purgeAssets != nil && v.AssetPrefix != "" {
		if err := purgeAssets(v.AssetPrefix); err != nil {
			return err
		}
	}
	return f.DeleteCatalogVersion(ctx, id)
}
func (f *fakeCatalogStore) PurgeOrphanCatalogPrefix(_ context.Context, prefix string, purgeAssets func(assetPrefix string) error, _ ...store.PurgeCatalogOptions) error {
	if purgeAssets != nil {
		return purgeAssets(prefix)
	}
	return nil
}

type fakeCatalogChangeStore struct {
	changes map[string][]*models.CatalogChange
	listErr error
}

func (f *fakeCatalogChangeStore) PutCatalogChange(_ context.Context, _ *models.CatalogChange) error {
	return nil
}
func (f *fakeCatalogChangeStore) ListCatalogChanges(_ context.Context, songbookID string) ([]*models.CatalogChange, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.changes[songbookID], nil
}
func (f *fakeCatalogChangeStore) DeleteCatalogChange(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func TestExportData_FullSuccessCoverage(t *testing.T) {
	ctx := context.Background()
	user := &models.User{
		ID:        "user-full",
		Email:     "full@example.com",
		CreatedAt: time.Now().UTC(),
		FavoriteSongs: []models.FavoriteSong{
			{Artist: "Queen", Title: "Bohemian Rhapsody", PreferredKey: 1, AddedAt: 100},
		},
		FavoriteArtists: []models.FavoriteArtist{
			{Name: "Queen", AddedAt: 100},
		},
	}

	pkStore := &fakePasskeyStore{
		creds: []*models.PasskeyCredential{
			{ID: "pk-1", UserID: user.ID, FriendlyName: "Laptop", CreatedAt: time.Now().UTC()},
			{ID: "pk-2", UserID: user.ID, FriendlyName: "Phone", CreatedAt: time.Now().UTC()},
		},
	}
	sbStore := &fakeSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-ready", OwnerID: user.ID, Name: "Book Ready", CatalogVersionID: "cv-ready"},
			{ID: "sb-live", OwnerID: user.ID, Name: "Book Live"},
		},
	}
	catStore := &fakeCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"cv-ready": {
				ID:          "cv-ready",
				Status:      models.CatalogVersionReady,
				AssetPrefix: "catalogs/cv-ready/",
				SongCount:   500,
			},
		},
	}
	ccStore := &fakeCatalogChangeStore{
		changes: map[string][]*models.CatalogChange{
			"sb-live": {
				{SongbookID: "sb-live", SongID: "song-1", Song: &models.Song{ID: "song-1", SongbookID: "sb-live", Title: "Song 1", Artist: "Artist 1"}},
				{SongbookID: "sb-live", SongID: "song-2", Song: &models.Song{ID: "song-2", SongbookID: "sb-live", Title: "Song 2", Artist: "Artist 2"}},
				{SongbookID: "sb-live", SongID: "song-3", Song: &models.Song{ID: "song-3", SongbookID: "sb-live", Title: "Song 3", Artist: "Artist 3"}},
			},
		},
	}
	evStore := &fakeEventStore{
		events: []*models.Event{
			{ID: "ev-1", OwnerID: user.ID, Name: "Show 1"},
			{ID: "ev-2", OwnerID: user.ID, Name: "Show 2"},
		},
	}

	svc := NewService(nil, pkStore, sbStore, evStore, nil, catStore, nil, ccStore, nil, nil)
	export, err := svc.ExportData(ctx, user)
	if err != nil {
		t.Fatalf("unexpected export error: %v", err)
	}

	if export.User.ID != user.ID {
		t.Fatalf("user id mismatch: got %s, want %s", export.User.ID, user.ID)
	}
	if export.ComplianceNote == "" {
		t.Fatal("expected non-empty compliance note")
	}
	if len(export.Passkeys) != 2 {
		t.Fatalf("expected 2 passkeys, got %d", len(export.Passkeys))
	}
	if len(export.Songbooks) != 2 {
		t.Fatalf("expected 2 songbooks, got %d", len(export.Songbooks))
	}
	if export.Songbooks[0].CatalogVersion != "cv-ready" || export.Songbooks[0].SongCount != 500 {
		t.Fatalf("expected ready catalog version, got %+v", export.Songbooks[0])
	}
	if len(export.Songbooks[1].Songs) != 3 || export.Songbooks[1].SongCount != 3 {
		t.Fatalf("expected 3 songs layered with changes, got %d songs", len(export.Songbooks[1].Songs))
	}
	if len(export.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(export.Events))
	}
	if len(export.FavoriteSongs) != 1 || export.FavoriteSongs[0].Artist != "Queen" {
		t.Fatalf("expected 1 favorite song, got %#v", export.FavoriteSongs)
	}
	if len(export.FavoriteArtists) != 1 || export.FavoriteArtists[0].Name != "Queen" {
		t.Fatalf("expected 1 favorite artist, got %#v", export.FavoriteArtists)
	}
}

func TestExportData_Validation(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	if _, err := svc.ExportData(ctx, nil); err == nil || err.Error() != "user is required" {
		t.Fatalf("expected user is required error, got %v", err)
	}
	if _, err := svc.ExportData(ctx, &models.User{ID: ""}); err == nil || err.Error() != "user id is required" {
		t.Fatalf("expected user id is required error, got %v", err)
	}
}

func TestExportData_FailurePropagation(t *testing.T) {
	ctx := context.Background()
	user := &models.User{ID: "user-err", Email: "err@example.com"}
	injectedErr := errors.New("simulated storage failure")

	tests := []struct {
		name        string
		setup       func() *Service
		wantSection string
	}{
		{
			name: "passkeys failure",
			setup: func() *Service {
				pk := &fakePasskeyStore{listErr: injectedErr}
				return NewService(nil, pk, nil, nil, nil, nil, nil, nil, nil, nil)
			},
			wantSection: "passkeys",
		},
		{
			name: "songbooks failure",
			setup: func() *Service {
				sb := &fakeSongbookStore{listErr: injectedErr}
				return NewService(nil, nil, sb, nil, nil, nil, nil, nil, nil, nil)
			},
			wantSection: "songbooks",
		},
		{
			name: "events failure",
			setup: func() *Service {
				ev := &fakeEventStore{listErr: injectedErr}
				return NewService(nil, nil, nil, ev, nil, nil, nil, nil, nil, nil)
			},
			wantSection: "events",
		},
		{
			name: "catalog changes failure",
			setup: func() *Service {
				sb := &fakeSongbookStore{books: []*models.Songbook{{ID: "sb-1", OwnerID: user.ID}}}
				cc := &fakeCatalogChangeStore{listErr: injectedErr}
				return NewService(nil, nil, sb, nil, nil, nil, nil, cc, nil, nil)
			},
			wantSection: "catalog_changes",
		},
		{
			name: "catalog version failure",
			setup: func() *Service {
				sb := &fakeSongbookStore{books: []*models.Songbook{{ID: "sb-1", OwnerID: user.ID, CatalogVersionID: "cv-fail"}}}
				cat := &fakeCatalogStore{getErr: injectedErr}
				return NewService(nil, nil, sb, nil, nil, cat, nil, nil, nil, nil)
			},
			wantSection: "catalog_versions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.setup()
			export, err := svc.ExportData(ctx, user)
			if export != nil {
				t.Fatalf("expected nil export on failure, got %+v", export)
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var expErr *ExportError
			if !errors.As(err, &expErr) {
				t.Fatalf("expected ExportError, got %T: %v", err, err)
			}
			if expErr.Section != tc.wantSection {
				t.Fatalf("section mismatch: got %q, want %q", expErr.Section, tc.wantSection)
			}
			if !errors.Is(err, injectedErr) {
				t.Fatalf("expected unwrapped injected error, got %v", err)
			}
		})
	}
}

func (f *fakeAuthStore) ResetPassword(context.Context, store.PasswordResetCommit) error {
	return errors.New("password reset unsupported by account test store")
}

type fakeDeveloperKeyStore struct {
	keys      []*models.DeveloperKey
	listErr   error
	revokeErr error
}

func (f *fakeDeveloperKeyStore) CreateDeveloperKey(_ context.Context, _ string, k *models.DeveloperKey) error {
	f.keys = append(f.keys, k)
	return nil
}
func (f *fakeDeveloperKeyStore) GetDeveloperKey(_ context.Context, _ string) (*models.DeveloperKey, error) {
	return nil, os.ErrNotExist
}
func (f *fakeDeveloperKeyStore) ListDeveloperKeys(_ context.Context, ownerID string) ([]*models.DeveloperKey, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var res []*models.DeveloperKey
	for _, k := range f.keys {
		if k.OwnerID == ownerID {
			res = append(res, k)
		}
	}
	return res, nil
}
func (f *fakeDeveloperKeyStore) RevokeDeveloperKey(_ context.Context, id, ownerID string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	var next []*models.DeveloperKey
	for _, k := range f.keys {
		if !(k.ID == id && k.OwnerID == ownerID) {
			next = append(next, k)
		}
	}
	f.keys = next
	return nil
}
func (f *fakeDeveloperKeyStore) TouchDeveloperKeyLastUsed(_ context.Context, _ string) error {
	return nil
}

func TestDeleteAccount_RecoveryAfterSongbookDeletedAndCatalogPurgeFails(t *testing.T) {
	ctx := context.Background()
	authStore := &fakeAuthStore{users: make(map[string]*models.User)}
	user := &models.User{ID: "user-rec", Email: "rec@example.com", DisplayName: "Rec Host", IsHost: true}
	_ = authStore.CreateUser(ctx, user)

	sbStore := &fakeSongbookStore{
		books: []*models.Songbook{
			{ID: "sb-1", OwnerID: user.ID, Name: "Owned Book", CatalogVersionID: "cv-sole"},
			{ID: "sb-2", OwnerID: user.ID, Name: "Shared Book", CatalogVersionID: "cv-shared"},
		},
	}
	purgedPrefixes := make(map[string]int)
	catStore := &fakeCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"cv-sole":   {ID: "cv-sole", Status: models.CatalogVersionReady, AssetPrefix: "catalogs/cv-sole/"},
			"cv-shared": {ID: "cv-shared", Status: models.CatalogVersionReady, AssetPrefix: "catalogs/cv-shared/"},
		},
		purgeErr: errors.New("simulated transient S3 failure"),
	}
	devKeyStore := &fakeDeveloperKeyStore{
		keys: []*models.DeveloperKey{
			{ID: "dk-1", OwnerID: user.ID, Name: "CLI Key"},
		},
	}

	// Make DeleteSongbook actually remove the songbook from fakeSongbookStore so retry sees 0 songbooks!
	deletingSBStore := &statefulFakeSongbookStore{fakeSongbookStore: sbStore}
	sharingCatStore := &statefulFakeCatalogStore{
		fakeCatalogStore: catStore,
		inUseVersions:    map[string]bool{"cv-shared": true},
		purgedPrefixes:   purgedPrefixes,
	}

	svc := NewService(authStore, nil, deletingSBStore, nil, nil, sharingCatStore, nil, nil, nil, nil).
		WithDeveloperKeys(devKeyStore)

	// Attempt 1 fails on purge_catalog_version AFTER songbooks and developer keys were deleted
	err := svc.DeleteAccount(ctx, user)
	var cascadeErr *CascadeError
	if !errors.As(err, &cascadeErr) || cascadeErr.Step != "purge_catalog_version" {
		t.Fatalf("expected CascadeError at purge_catalog_version, got: %v", err)
	}
	if len(devKeyStore.keys) != 0 {
		t.Fatalf("expected developer keys revoked on attempt 1, got %d", len(devKeyStore.keys))
	}
	if len(deletingSBStore.books) != 0 {
		t.Fatalf("expected songbooks deleted before catalog purge, got %d remaining", len(deletingSBStore.books))
	}

	// Verify user is still in authStore with Deleting=true and PendingCatalogVersions preserved
	storedUser, err := authStore.GetUser(ctx, user.ID)
	if err != nil || !storedUser.Deleting {
		t.Fatalf("expected user marked deleting after partial failure, got %+v, %v", storedUser, err)
	}
	if len(storedUser.PendingCatalogVersions) == 0 {
		t.Fatalf("expected PendingCatalogVersions persisted on user record, got empty")
	}

	// Clear transient purge failure and resume deletion via RecoverDeletingAccounts
	catStore.purgeErr = nil
	completed, err := svc.RecoverDeletingAccounts(ctx)
	if err != nil || completed != 1 {
		t.Fatalf("RecoverDeletingAccounts = (%d, %v), want (1, nil)", completed, err)
	}
	if _, ok := catStore.versions["cv-sole"]; ok {
		t.Fatal("expected sole catalog version cv-sole to be purged on recovery")
	}
	if _, ok := catStore.versions["cv-shared"]; !ok {
		t.Fatal("expected shared catalog version cv-shared to be preserved")
	}
	if _, err := authStore.GetUser(ctx, user.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected user deleted after recovery, got %v", err)
	}

	// Repeated ResumeAccountDeletion after completion is idempotent
	if err := svc.ResumeAccountDeletion(ctx, user.ID); err != nil {
		t.Fatalf("expected idempotent ResumeAccountDeletion after user deleted, got %v", err)
	}
}

type statefulFakeSongbookStore struct {
	*fakeSongbookStore
}

func (s *statefulFakeSongbookStore) DeleteSongbook(_ context.Context, id string) error {
	var next []*models.Songbook
	for _, b := range s.books {
		if b.ID != id {
			next = append(next, b)
		}
	}
	s.books = next
	return nil
}

type statefulFakeCatalogStore struct {
	*fakeCatalogStore
	inUseVersions  map[string]bool
	purgedPrefixes map[string]int
}

func (s *statefulFakeCatalogStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	if s.purgeErr != nil {
		return s.purgeErr
	}
	if s.inUseVersions[id] {
		return store.ErrCatalogVersionInUse
	}
	v := s.versions[id]
	if v == nil {
		return os.ErrNotExist
	}
	if purgeAssets != nil && v.AssetPrefix != "" {
		if err := purgeAssets(v.AssetPrefix); err != nil {
			return err
		}
		s.purgedPrefixes[v.AssetPrefix]++
	}
	delete(s.versions, id)
	return nil
}

type nilBarrierSongbookStore struct {
	*fakeSongbookStore
	deletedSongbookID string
}

func (s *nilBarrierSongbookStore) BeginSongbookDeletion(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil // contract violation: nil frozen state without error
}

func (s *nilBarrierSongbookStore) DeleteSongbook(_ context.Context, id string) error {
	s.deletedSongbookID = id
	return s.fakeSongbookStore.DeleteSongbook(context.Background(), id)
}

type trackingCatalogChangeStore struct {
	*fakeCatalogChangeStore
	deletedCount int
}

func (t *trackingCatalogChangeStore) DeleteCatalogChange(ctx context.Context, bookID, songID string, updated time.Time) error {
	t.deletedCount++
	return t.fakeCatalogChangeStore.DeleteCatalogChange(ctx, bookID, songID, updated)
}

func TestDeleteAccount_BeginSongbookDeletionNilReturnsExplicitErrorAndBlocksChildren(t *testing.T) {
	ctx := context.Background()
	authStore := &fakeAuthStore{users: make(map[string]*models.User)}
	user := &models.User{ID: "user-nil-barrier", Email: "nil-barrier@example.com"}
	_ = authStore.CreateUser(ctx, user)

	sb := &models.Songbook{ID: "sb-nil-barrier", OwnerID: user.ID, Name: "Book with Nil Barrier", CatalogVersionID: "cv-nil"}
	sbStore := &nilBarrierSongbookStore{
		fakeSongbookStore: &fakeSongbookStore{
			books: []*models.Songbook{sb},
		},
	}
	ccStore := &trackingCatalogChangeStore{
		fakeCatalogChangeStore: &fakeCatalogChangeStore{
			changes: map[string][]*models.CatalogChange{
				sb.ID: {{SongID: "song-1", SongbookID: sb.ID, Song: &models.Song{Artist: "Artist", Title: "Title"}}},
			},
		},
	}

	svc := NewService(authStore, nil, sbStore, nil, nil, nil, nil, ccStore, nil, nil)

	err := svc.DeleteAccount(ctx, user)
	if err == nil {
		t.Fatal("expected error when BeginSongbookDeletion returns nil, got nil")
	}

	var cascadeErr *CascadeError
	if !errors.As(err, &cascadeErr) {
		t.Fatalf("expected CascadeError, got %T: %v", err, err)
	}
	if cascadeErr.Step != "begin_songbook_deletion" {
		t.Fatalf("expected cascade step begin_songbook_deletion, got %s", cascadeErr.Step)
	}
	if cascadeErr.Err == nil || !strings.Contains(cascadeErr.Err.Error(), "nil frozen state") {
		t.Fatalf("expected underlying error mentioning nil frozen state, got: %v", cascadeErr.Err)
	}

	// Verify no children were deleted
	if ccStore.deletedCount != 0 {
		t.Fatalf("expected 0 catalog changes deleted, got %d", ccStore.deletedCount)
	}
	if sbStore.deletedSongbookID != "" {
		t.Fatalf("expected songbook not deleted, got deleted ID: %s", sbStore.deletedSongbookID)
	}

	// Verify user is not deleted
	storedUser, getErr := authStore.GetUser(ctx, user.ID)
	if getErr != nil || storedUser == nil {
		t.Fatalf("expected user still present in auth store, got %v", getErr)
	}
}
