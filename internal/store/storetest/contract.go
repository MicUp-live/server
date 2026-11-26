// Package storetest defines persistence contracts shared by store implementations.
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
)

// AuthStore is the authentication persistence contract under test.
type AuthStore interface {
	CreateUser(context.Context, *models.User) error
	UpdateUser(context.Context, *models.User) error
	ChangeUserEmail(context.Context, string, string, string, *models.User) error
	GetUserByEmail(context.Context, string) (*models.User, error)
	GetUser(context.Context, string) (*models.User, error)
	CreateAuthSession(context.Context, *models.AuthSession) error
	ConsumeAuthSession(context.Context, []byte) (*models.User, error)
	DeleteAuthSessionsForUser(context.Context, string) error
	BeginAccountDeletion(context.Context, string, []string) (*models.User, error)
	RecordAccountDeletionProgress(context.Context, string, string, string, []string) error
	ListDeletingUsers(context.Context) ([]*models.User, error)
	DeleteUser(context.Context, string) error
}

// SongbookStore is the songbook persistence contract under test.
type SongbookStore interface {
	CreateSongbook(context.Context, *models.Songbook) error
	UpdateSongbook(context.Context, *models.Songbook) error
	DeleteSongbook(context.Context, string) error
	GetSongbook(context.Context, string) (*models.Songbook, error)
	GetPublicSongbook(context.Context, string) (*models.Songbook, error)
	GetSongbookBySlug(context.Context, string) (*models.Songbook, error)
	ListSongbooksByOwner(context.Context, string) ([]*models.Songbook, error)
}

// Repositories groups one isolated set of persistence implementations.
type Repositories struct {
	Auth      AuthStore
	Songbooks SongbookStore
}

// Factory creates isolated repositories and registers cleanup with the test.
type Factory func(t *testing.T) Repositories

// Run executes behavior required of every persistence implementation.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("authentication", func(t *testing.T) {
		testAuthentication(t, factory(t))
	})
	t.Run("songbook ownership and publication", func(t *testing.T) {
		testSongbookOwnershipAndPublication(t, factory(t))
	})
}

func testAuthentication(t *testing.T, repositories Repositories) {
	t.Helper()
	ctx := context.Background()
	user := newUser("contract-auth@example.com")
	if err := repositories.Auth.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	byID, err := repositories.Auth.GetUser(ctx, user.ID)
	if err != nil || byID.Email != user.Email {
		t.Fatalf("get user by ID = %+v, %v", byID, err)
	}
	byEmail, err := repositories.Auth.GetUserByEmail(ctx, user.Email)
	if err != nil || byEmail.ID != user.ID {
		t.Fatalf("get user by email = %+v, %v", byEmail, err)
	}

	tokenHash := []byte("contract-single-use-refresh-token")
	if err := repositories.Auth.CreateAuthSession(ctx, &models.AuthSession{
		TokenHash: tokenHash,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create auth session: %v", err)
	}
	consumed, err := repositories.Auth.ConsumeAuthSession(ctx, tokenHash)
	if err != nil || consumed.ID != user.ID {
		t.Fatalf("consume auth session = %+v, %v", consumed, err)
	}
	if _, err := repositories.Auth.ConsumeAuthSession(ctx, tokenHash); err == nil {
		t.Fatal("consumed refresh session was reusable")
	}

	preDeleteSession := []byte("contract-pre-delete-refresh-token")
	if err := repositories.Auth.CreateAuthSession(ctx, &models.AuthSession{
		TokenHash: preDeleteSession,
		UserID:    user.ID,
		AuthEpoch: user.AuthEpoch,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create pre-delete session: %v", err)
	}
	deletingUser, err := repositories.Auth.BeginAccountDeletion(ctx, user.ID, []string{"cat-v1"})
	if err != nil {
		t.Fatalf("begin account deletion: %v", err)
	}
	if !deletingUser.Deleting || deletingUser.DeletionEpoch == "" || len(deletingUser.PendingCatalogVersions) != 1 {
		t.Fatalf("unexpected deleting user state: %+v", deletingUser)
	}
	if _, err := repositories.Auth.ConsumeAuthSession(ctx, preDeleteSession); err == nil {
		t.Fatal("pre-deletion refresh session was accepted after BeginAccountDeletion")
	}
	if err := repositories.Auth.DeleteUser(ctx, user.ID); err == nil {
		t.Fatal("expected DeleteUser to fail while pending catalog versions remain")
	}
	if err := repositories.Auth.RecordAccountDeletionProgress(ctx, user.ID, "purge_catalogs", "", []string{"cat-v1"}); err != nil {
		t.Fatalf("record deletion progress: %v", err)
	}
	deletingUsers, err := repositories.Auth.ListDeletingUsers(ctx)
	if err != nil || len(deletingUsers) == 0 {
		t.Fatalf("list deleting users = %+v, %v", deletingUsers, err)
	}
	if err := repositories.Auth.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := repositories.Auth.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("idempotent delete user: %v", err)
	}
}

func testSongbookOwnershipAndPublication(t *testing.T, repositories Repositories) {
	t.Helper()
	ctx := context.Background()
	owner := newUser("contract-owner@example.com")
	other := newUser("contract-other@example.com")
	for _, user := range []*models.User{owner, other} {
		if err := repositories.Auth.CreateUser(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	songbook := &models.Songbook{
		ID:       uuid.NewString(),
		OwnerID:  owner.ID,
		PublicID: uuid.NewString(),
		Slug:     "contract-slug",
		Name:     "Contract Book",
		IsPublic: true,
	}
	if err := repositories.Songbooks.CreateSongbook(ctx, songbook); err != nil {
		t.Fatalf("create songbook: %v", err)
	}
	owned, err := repositories.Songbooks.ListSongbooksByOwner(ctx, owner.ID)
	if err != nil || len(owned) != 1 || owned[0].ID != songbook.ID {
		t.Fatalf("owner songbooks = %+v, %v", owned, err)
	}
	notOwned, err := repositories.Songbooks.ListSongbooksByOwner(ctx, other.ID)
	if err != nil || len(notOwned) != 0 {
		t.Fatalf("other owner songbooks = %+v, %v", notOwned, err)
	}
	if _, err := repositories.Songbooks.GetPublicSongbook(ctx, songbook.PublicID); err != nil {
		t.Fatalf("get published songbook by public ID: %v", err)
	}
	if bySlug, err := repositories.Songbooks.GetSongbookBySlug(ctx, "contract-slug"); err != nil || bySlug.ID != songbook.ID {
		t.Fatalf("get songbook by slug = %+v, %v", bySlug, err)
	}
	if bySlugPub, err := repositories.Songbooks.GetPublicSongbook(ctx, "contract-slug"); err != nil || bySlugPub.ID != songbook.ID {
		t.Fatalf("get public songbook by slug = %+v, %v", bySlugPub, err)
	}
	songbook.IsPublic = false
	if err := repositories.Songbooks.UpdateSongbook(ctx, songbook); err != nil {
		t.Fatalf("unpublish songbook: %v", err)
	}
	if _, err := repositories.Songbooks.GetPublicSongbook(ctx, songbook.PublicID); err == nil {
		t.Fatal("private songbook remained publicly retrievable")
	}
	if _, err := repositories.Songbooks.GetPublicSongbook(ctx, "contract-slug"); err == nil {
		t.Fatal("private songbook remained publicly retrievable by slug")
	}
}

func newUser(email string) *models.User {
	return &models.User{
		ID:           uuid.NewString(),
		Email:        email,
		DisplayName:  "Contract User",
		PasswordHash: "contract-password-hash",
		IsHost:       true,
	}
}

func newSongbook(ownerID string) *models.Songbook {
	return &models.Songbook{
		ID:       uuid.NewString(),
		OwnerID:  ownerID,
		PublicID: uuid.NewString(),
		Name:     "Contract Book",
	}
}
