package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := New(ctx, Config{DSN: dsn})
	if err != nil {
		t.Fatalf("New postgres store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	_, err = s.Pool().Exec(ctx, `TRUNCATE TABLE rate_limits, idempotency_records, event_requests,
		events, import_jobs, catalog_changes, songbook_invitations, songbook_grants, songbooks,
		catalog_versions, developer_keys, webauthn_sessions, passkey_credentials, auth_sessions, users CASCADE`)
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	return s
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func sha256Bytes(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func createTestUser(t *testing.T, s *Store, email string, isHost, verified bool) *models.User {
	t.Helper()
	u := &models.User{
		ID:            uuid.NewString(),
		Email:         email,
		DisplayName:   "Test User",
		PasswordHash:  "hash-v1",
		AuthEpoch:     "epoch-1",
		IsHost:        isHost,
		EmailVerified: verified,
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	return u
}

func createTestSongbook(t *testing.T, s *Store, ownerID, slug string, isPublic bool) *models.Songbook {
	t.Helper()
	sb := &models.Songbook{
		ID:       uuid.NewString(),
		OwnerID:  ownerID,
		PublicID: "pub-" + uuid.NewString()[:8],
		Slug:     slug,
		VenueID:  "venue-1",
		Name:     "Test Book",
		IsPublic: isPublic,
	}
	if err := s.CreateSongbook(context.Background(), sb); err != nil {
		t.Fatalf("CreateSongbook: %v", err)
	}
	return sb
}

func createTestEvent(t *testing.T, s *Store, ownerID, songbookID, joinCode string) *models.Event {
	t.Helper()
	ev := &models.Event{
		ID:           uuid.NewString(),
		OwnerID:      ownerID,
		SongbookID:   songbookID,
		VenueName:    "The Lounge",
		Name:         "Friday Karaoke",
		JoinCode:     joinCode,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		AutoAccept:   true,
	}
	if err := s.CreateEvent(context.Background(), ev); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	return ev
}

func makeTestRequest(eventID, songbookID, singer, title string, round int, rank int64) *models.SongRequest {
	return &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SongbookID: songbookID,
		SongID:     "song-" + uuid.NewString()[:6],
		SongTitle:  title,
		SongArtist: "Artist",
		SingerName: singer,
		Status:     models.StatusAccepted,
		Round:      round,
		Rank:       rank,
	}
}

func TestEnsureSchemaIdempotentAndDSQLRewrite(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// EnsureSchema was already run once in openTestStore; run it twice more.
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema 2nd call: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema 3rd call: %v", err)
	}

	cases := []struct{ in, want string }{
		{"CREATE INDEX IF NOT EXISTS idx_foo ON bar (baz)", "CREATE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)"},
		{"CREATE UNIQUE INDEX IF NOT EXISTS idx_foo ON bar (baz)", "CREATE UNIQUE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)"},
		{"CREATE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)", "CREATE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)"},
		{"CREATE UNIQUE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)", "CREATE UNIQUE INDEX ASYNC IF NOT EXISTS idx_foo ON bar (baz)"},
		{"CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY)", "CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY)"},
	}
	for _, tc := range cases {
		if got := rewriteForDSQL(tc.in); got != tc.want {
			t.Errorf("rewriteForDSQL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWithTxSerializationRetry(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "40001"}, true},
		{&pgconn.PgError{Code: "40P01"}, true},
		{&pgconn.PgError{Code: "23505"}, false},
		{errors.New("plain error"), false},
	} {
		if got := isSerializationFailure(tc.err); got != tc.want {
			t.Fatalf("isSerializationFailure(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}

	s := openTestStore(t)
	ctx := context.Background()

	attempts := 0
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		attempts++
		if attempts < 3 {
			return &pgconn.PgError{Code: sqlStateSerializationFailure, Message: "simulated OCC conflict"}
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("withTx retry: attempts=%d err=%v", attempts, err)
	}

	exhausted := 0
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		exhausted++
		return &pgconn.PgError{Code: sqlStateSerializationFailure, Message: "persistent conflict"}
	})
	if err == nil || exhausted != maxTxRetries {
		t.Fatalf("expected failure after %d attempts, got attempts=%d err=%v", maxTxRetries, exhausted, err)
	}
}

func TestAuthLifecycleAndDeletion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	u := createTestUser(t, s, "  Alice@Example.COM ", true, true)
	got, err := s.GetUserByEmail(ctx, "ALICE@example.com")
	if err != nil || got.ID != u.ID || got.Email != "alice@example.com" {
		t.Fatalf("GetUserByEmail failed: got=%+v err=%v", got, err)
	}

	dup := &models.User{ID: uuid.NewString(), Email: "alice@example.com"}
	if err := s.CreateUser(ctx, dup); err == nil {
		t.Fatal("expected duplicate normalized email to be rejected")
	}

	u.DisplayName = "Alice Host"
	u.AvatarURL = "https://example.com/a.png"
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if err := s.ChangeUserEmail(ctx, u.ID, "alice@example.com", "Alice2@Example.com", u); err != nil {
		t.Fatalf("ChangeUserEmail: %v", err)
	}
	if _, err := s.GetUserByEmail(ctx, "alice@example.com"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old email gone, got %v", err)
	}
	if _, err := s.GetUserByEmail(ctx, "alice2@example.com"); err != nil {
		t.Fatalf("expected new email to resolve: %v", err)
	}

	// Auth sessions: single-use, expired, epoch mismatch, bulk delete.
	tok1, tokExp, tokEpoch, tokBulk := sha256Bytes("s1"), sha256Bytes("sexp"), sha256Bytes("sep"), sha256Bytes("sbulk")
	for _, sess := range []*models.AuthSession{
		{TokenHash: tok1, UserID: u.ID, AuthEpoch: u.AuthEpoch, ExpiresAt: time.Now().Add(time.Hour)},
		{TokenHash: tokExp, UserID: u.ID, AuthEpoch: u.AuthEpoch, ExpiresAt: time.Now().Add(-time.Minute)},
		{TokenHash: tokEpoch, UserID: u.ID, AuthEpoch: u.AuthEpoch, ExpiresAt: time.Now().Add(time.Hour)},
		{TokenHash: tokBulk, UserID: u.ID, AuthEpoch: u.AuthEpoch, ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := s.CreateAuthSession(ctx, sess); err != nil {
			t.Fatalf("CreateAuthSession: %v", err)
		}
	}
	if consumed, err := s.ConsumeAuthSession(ctx, tok1); err != nil || consumed.ID != u.ID {
		t.Fatalf("ConsumeAuthSession failed: user=%+v err=%v", consumed, err)
	}
	if _, err := s.ConsumeAuthSession(ctx, tok1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected single-use session consumed, got %v", err)
	}
	if _, err := s.ConsumeAuthSession(ctx, tokExp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired session ErrNotExist, got %v", err)
	}

	// Password reset: stale epoch and expired rejected, valid advances epoch and invalidates session.
	for _, bad := range []store.PasswordResetCommit{
		{UserID: u.ID, Email: u.Email, ExpectedAuthEpoch: "wrong", AuthEpoch: "epoch-2", PasswordHash: "hash-v2", ExpiresAt: time.Now().Add(time.Hour)},
		{UserID: u.ID, Email: u.Email, ExpectedAuthEpoch: u.AuthEpoch, AuthEpoch: "epoch-2", PasswordHash: "hash-v2", ExpiresAt: time.Now().Add(-time.Minute)},
	} {
		if err := s.ResetPassword(ctx, bad); !errors.Is(err, store.ErrInvalidPasswordReset) {
			t.Fatalf("expected ErrInvalidPasswordReset, got %v", err)
		}
	}
	if err := s.ResetPassword(ctx, store.PasswordResetCommit{
		UserID: u.ID, Email: u.Email, ExpectedAuthEpoch: u.AuthEpoch, AuthEpoch: "epoch-2",
		PasswordHash: "hash-v2", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("ResetPassword valid: %v", err)
	}
	if _, err := s.ConsumeAuthSession(ctx, tokEpoch); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected epoch-mismatched session rejected, got %v", err)
	}
	if err := s.DeleteAuthSessionsForUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteAuthSessionsForUser: %v", err)
	}
	if _, err := s.ConsumeAuthSession(ctx, tokBulk); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected bulk-deleted session rejected, got %v", err)
	}

	// Account deletion lifecycle & ErrAccountDeleting enforcement.
	delUser, err := s.BeginAccountDeletion(ctx, u.ID, []string{"cv-1", "cv-2"})
	if err != nil || !delUser.Deleting || len(delUser.PendingCatalogVersions) != 2 {
		t.Fatalf("BeginAccountDeletion: user=%+v err=%v", delUser, err)
	}
	if err := s.ResetPassword(ctx, store.PasswordResetCommit{
		UserID: u.ID, Email: u.Email, ExpectedAuthEpoch: delUser.AuthEpoch, AuthEpoch: "epoch-3",
		PasswordHash: "hash-v3", ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, store.ErrInvalidPasswordReset) {
		t.Fatalf("expected ResetPassword rejected while deleting, got %v", err)
	}
	if err := s.CreateAuthSession(ctx, &models.AuthSession{
		TokenHash: sha256Bytes("del-sess"), UserID: u.ID, AuthEpoch: delUser.AuthEpoch, ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("expected ErrAccountDeleting on CreateAuthSession, got %v", err)
	}
	delUser.Deleting = false
	if err := s.UpdateUser(ctx, delUser); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("expected ErrAccountDeleting on UpdateUser, got %v", err)
	}

	deletingList, err := s.ListDeletingUsers(ctx)
	if err != nil || len(deletingList) != 1 || deletingList[0].ID != u.ID {
		t.Fatalf("ListDeletingUsers: %+v, err=%v", deletingList, err)
	}
	if err := s.DeleteUser(ctx, u.ID); err == nil {
		t.Fatal("expected DeleteUser to fail while pending catalog versions remain")
	}
	if err := s.RecordAccountDeletionProgress(ctx, u.ID, "purged-catalogs", "", []string{"cv-1", "cv-2"}); err != nil {
		t.Fatalf("RecordAccountDeletionProgress: %v", err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser after resolving catalogs: %v", err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser idempotency: %v", err)
	}
}

func TestUserFavoritesRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	u := createTestUser(t, s, "fav@example.com", false, true)
	emptyFavs, err := s.GetUserFavorites(ctx, u.ID)
	if err != nil || len(emptyFavs.Songs) != 0 || len(emptyFavs.Artists) != 0 {
		t.Fatalf("expected empty favorites, got %+v err=%v", emptyFavs, err)
	}

	want := &models.UserFavorites{
		Songs: []models.FavoriteSong{
			{Artist: "Queen", Title: "Bohemian Rhapsody", PreferredKey: -1, CanonicalID: "can-1", AddedAt: 1000},
		},
		Artists: []models.FavoriteArtist{
			{Name: "Queen", AddedAt: 1000},
		},
	}
	if err := s.SaveUserFavorites(ctx, u.ID, want); err != nil {
		t.Fatalf("SaveUserFavorites: %v", err)
	}
	got, err := s.GetUserFavorites(ctx, u.ID)
	if err != nil || len(got.Songs) != 1 || got.Songs[0].Title != "Bohemian Rhapsody" || len(got.Artists) != 1 {
		t.Fatalf("GetUserFavorites mismatch: %+v err=%v", got, err)
	}

	if _, err := s.BeginAccountDeletion(ctx, u.ID, nil); err != nil {
		t.Fatalf("BeginAccountDeletion: %v", err)
	}
	if err := s.SaveUserFavorites(ctx, u.ID, want); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("expected ErrAccountDeleting on SaveUserFavorites, got %v", err)
	}
}

func TestPasskeysAndWebAuthnSessions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Unpadded base64 credential ID to exercise altPasskeyID lookup ("Y3JlZA" <-> "Y3JlZA==").
	credID := "Y3JlZA"
	user := &models.User{
		ID: uuid.NewString(), Email: "Passkey@Example.com", DisplayName: "PK User", AuthEpoch: "ep-1",
	}
	cred1 := &models.PasskeyCredential{
		ID: credID, UserID: user.ID, PublicKey: []byte("pk-bytes-1"),
		AttestationType: "none", SignCount: 1, Transports: []string{"internal"}, FriendlyName: "Laptop",
	}
	if err := s.CreateUserWithPasskey(ctx, user, cred1); err != nil {
		t.Fatalf("CreateUserWithPasskey: %v", err)
	}

	gotCred, err := s.GetPasskeyCredential(ctx, credID+"==")
	if err != nil || gotCred.ID != credID || gotCred.SignCount != 1 {
		t.Fatalf("GetPasskeyCredential alt padding failed: %+v err=%v", gotCred, err)
	}

	cred2 := &models.PasskeyCredential{
		ID: "cred-2", UserID: user.ID, PublicKey: []byte("pk-bytes-2"), SignCount: 0, FriendlyName: "Phone",
	}
	if err := s.SavePasskeyCredential(ctx, cred2); err != nil {
		t.Fatalf("SavePasskeyCredential: %v", err)
	}
	list, err := s.ListPasskeyCredentialsForUser(ctx, user.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListPasskeyCredentialsForUser: len=%d err=%v", len(list), err)
	}

	if err := s.RecordPasskeyUse(ctx, cred1, store.PasskeyUsage{SignCount: 2, BackupEligible: true, BackupState: true}); err != nil {
		t.Fatalf("RecordPasskeyUse: %v", err)
	}
	// Stale observed SignCount (still 1) -> ErrPasskeyChanged.
	if err := s.RecordPasskeyUse(ctx, cred1, store.PasskeyUsage{SignCount: 3}); !errors.Is(err, store.ErrPasskeyChanged) {
		t.Fatalf("expected ErrPasskeyChanged for stale SignCount, got %v", err)
	}

	if err := s.DeletePasskeyCredential(ctx, cred2.ID, "wrong-user"); err == nil {
		t.Fatal("expected owner mismatch error on DeletePasskeyCredential")
	}
	if err := s.DeletePasskeyCredential(ctx, cred2.ID, user.ID); err != nil {
		t.Fatalf("DeletePasskeyCredential: %v", err)
	}
	if _, err := s.GetPasskeyCredential(ctx, cred2.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deleted cred ErrNotExist, got %v", err)
	}

	// WebAuthn sessions: usernameless / provisional user ID, single-use, expiry.
	provSess := &models.WebAuthnSession{
		SessionID: "wa-prov", UserID: "provisional-unknown-id", Challenge: "ch1",
		SessionData: []byte(`{"c":"ch1"}`), ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	if err := s.SaveWebAuthnSession(ctx, provSess); err != nil {
		t.Fatalf("SaveWebAuthnSession provisional: %v", err)
	}
	gotWA, err := s.ConsumeWebAuthnSession(ctx, "wa-prov")
	if err != nil || gotWA.UserID != "" || gotWA.Challenge != "ch1" {
		t.Fatalf("ConsumeWebAuthnSession provisional: %+v err=%v", gotWA, err)
	}
	if _, err := s.ConsumeWebAuthnSession(ctx, "wa-prov"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected single-use webauthn session ErrNotExist, got %v", err)
	}

	expSess := &models.WebAuthnSession{
		SessionID: "wa-exp", UserID: user.ID, Challenge: "ch2",
		SessionData: []byte(`{}`), ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := s.SaveWebAuthnSession(ctx, expSess); err != nil {
		t.Fatalf("SaveWebAuthnSession expired: %v", err)
	}
	if _, err := s.ConsumeWebAuthnSession(ctx, "wa-exp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired webauthn session ErrNotExist, got %v", err)
	}

	// Deleting user blocks SavePasskeyCredential and RecordPasskeyUse.
	if _, err := s.BeginAccountDeletion(ctx, user.ID, nil); err != nil {
		t.Fatalf("BeginAccountDeletion: %v", err)
	}
	if err := s.SavePasskeyCredential(ctx, cred2); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("expected ErrAccountDeleting on SavePasskeyCredential, got %v", err)
	}
	latestCred1, err := s.GetPasskeyCredential(ctx, credID)
	if err != nil {
		t.Fatalf("GetPasskeyCredential: %v", err)
	}
	if err := s.RecordPasskeyUse(ctx, latestCred1, store.PasskeyUsage{SignCount: 5}); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("expected ErrAccountDeleting on RecordPasskeyUse, got %v", err)
	}
}

func TestDeveloperKeysAndRateLimits(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	u := createTestUser(t, s, "dev@example.com", true, true)
	rawKey := "micup_live_secret_12345"
	dk := &models.DeveloperKey{
		OwnerID: u.ID, Name: "OpenKJ Integration", KeyPrefix: "micup_live_se", Scopes: []string{"events:read", "queue:write"},
	}
	if err := s.CreateDeveloperKey(ctx, rawKey, dk); err != nil {
		t.Fatalf("CreateDeveloperKey: %v", err)
	}

	loaded, err := s.GetDeveloperKey(ctx, rawKey)
	if err != nil || loaded.ID != sha256Hex(rawKey) || len(loaded.Scopes) != 2 {
		t.Fatalf("GetDeveloperKey: %+v err=%v", loaded, err)
	}
	keys, err := s.ListDeveloperKeys(ctx, u.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListDeveloperKeys: len=%d err=%v", len(keys), err)
	}
	if err := s.TouchDeveloperKeyLastUsed(ctx, loaded.ID); err != nil {
		t.Fatalf("TouchDeveloperKeyLastUsed: %v", err)
	}
	touched, _ := s.GetDeveloperKey(ctx, rawKey)
	if touched.LastUsedAt.IsZero() {
		t.Fatal("expected LastUsedAt updated")
	}
	if err := s.RevokeDeveloperKey(ctx, loaded.ID, "other-user"); err == nil {
		t.Fatal("expected owner mismatch error on RevokeDeveloperKey")
	}
	if err := s.RevokeDeveloperKey(ctx, loaded.ID, u.ID); err != nil {
		t.Fatalf("RevokeDeveloperKey: %v", err)
	}
	if _, err := s.GetDeveloperKey(ctx, rawKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected revoked key ErrNotExist, got %v", err)
	}

	// Rate limits: fixed clock to test window increment, limit enforcement, reset.
	fixedNow := time.Date(2026, 11, 26, 12, 0, 10, 0, time.UTC)
	s.now = func() time.Time { return fixedNow }

	res1, err := s.Allow(ctx, "login", "1.2.3.4", 2, time.Minute)
	if err != nil || !res1.Allowed || res1.Remaining != 1 || res1.Limit != 2 {
		t.Fatalf("Allow #1: %+v err=%v", res1, err)
	}
	res2, err := s.Allow(ctx, "login", "1.2.3.4", 2, time.Minute)
	if err != nil || !res2.Allowed || res2.Remaining != 0 {
		t.Fatalf("Allow #2: %+v err=%v", res2, err)
	}
	res3, err := s.Allow(ctx, "login", "1.2.3.4", 2, time.Minute)
	if err != nil || res3.Allowed || res3.Remaining != 0 || !res3.ResetAt.After(fixedNow) {
		t.Fatalf("Allow #3 (over limit): %+v err=%v", res3, err)
	}

	s.now = func() time.Time { return res3.ResetAt.Add(time.Second) }
	resNext, err := s.Allow(ctx, "login", "1.2.3.4", 2, time.Minute)
	if err != nil || !resNext.Allowed || resNext.Remaining != 1 {
		t.Fatalf("Allow next window: %+v err=%v", resNext, err)
	}
}
