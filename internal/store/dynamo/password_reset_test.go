package dynamo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type resetCommitStore struct {
	*Store
	entered chan struct{}
	release chan struct{}
	failure error
}

func (s *resetCommitStore) ResetPassword(ctx context.Context, reset store.PasswordResetCommit) error {
	if s.entered != nil {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.failure != nil {
		return s.failure
	}
	return s.Store.ResetPassword(ctx, reset)
}
func signedResetClaims(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(resetTestSecret))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func resetRouter(t *testing.T, repository store.AuthStore, sender *resetEmailCapture) http.Handler {
	t.Helper()
	router, err := app.NewRouter(config.Config{Environment: config.Development, AuthSecret: resetTestSecret}, app.Dependencies{Auth: repository, EmailSender: sender})
	if err != nil {
		t.Fatal(err)
	}
	return router
}
func resetUser(t *testing.T, s *Store) *models.User {
	t.Helper()
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.com", PasswordHash: "original-hash", DisplayName: "Original", IsHost: true}
	if err := s.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	return user
}
func assertResetUnchanged(t *testing.T, s *Store, user *models.User) {
	t.Helper()
	actual, err := s.GetUser(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.PasswordHash != user.PasswordHash || actual.AuthEpoch != user.AuthEpoch || actual.Email != user.Email {
		t.Fatal("rejected reset changed password, epoch, or identity")
	}
}
func TestDynamoHTTPPasswordResetCommitBoundary(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	t.Run("concurrent replay", func(t *testing.T) {
		user := resetUser(t, s)
		barrier := &resetCommitStore{Store: s, entered: make(chan struct{}, 2), release: make(chan struct{})}
		sender := &resetEmailCapture{links: make(chan string, 1)}
		router := resetRouter(t, barrier, sender)
		token := requestResetLink(t, router, sender, user.Email)
		type result struct {
			status   int
			password string
		}
		results := make(chan result, 2)
		for _, password := range []string{"winning-password-a", "winning-password-b"} {
			go func() {
				response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", map[string]interface{}{"token": token, "password": password}, "")
				results <- result{response.Code, password}
			}()
		}
		// Both requests have validated and hashed the same authority before either commits.
		for range 2 {
			select {
			case <-barrier.entered:
			case <-time.After(10 * time.Second):
				close(barrier.release)
				t.Fatal("reset did not reach commit barrier")
			}
		}
		close(barrier.release)
		successes, rejected := 0, 0
		var winner string
		for range 2 {
			result := <-results
			switch result.status {
			case 200:
				successes++
				winner = result.password
			case 400:
				rejected++
			default:
				t.Fatalf("reset status: %d", result.status)
			}
		}
		if successes != 1 || rejected != 1 {
			t.Fatalf("successes=%d rejected=%d; want one each", successes, rejected)
		}
		actual, err := s.GetUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.AuthEpoch == "" || bcrypt.CompareHashAndPassword([]byte(actual.PasswordHash), []byte(winner)) != nil {
			t.Fatal("winner did not commit password and epoch")
		}
	})
	t.Run("invalid authority", func(t *testing.T) {
		user := resetUser(t, s)
		sender := &resetEmailCapture{links: make(chan string, 1)}
		router := resetRouter(t, s, sender)
		for _, tc := range []struct {
			name, id, email, action string
			expires                 int64
		}{
			{"expired", user.ID, user.Email, "reset-password", time.Now().Add(-time.Second).Unix()},
			{"wrong email", user.ID, "wrong@example.com", "reset-password", time.Now().Add(time.Hour).Unix()},
			{"missing user", uuid.NewString(), user.Email, "reset-password", time.Now().Add(time.Hour).Unix()},
			{"wrong action", user.ID, user.Email, "verify-email", time.Now().Add(time.Hour).Unix()},
		} {
			t.Run(tc.name, func(t *testing.T) {
				token := signedResetClaims(t, map[string]interface{}{"sub": tc.id, "email": tc.email, "act": tc.action, "exp": tc.expires})
				response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", map[string]interface{}{"token": token, "password": "new-password"}, "")
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status=%d %s", response.Code, response.Body.String())
				}
				assertResetUnchanged(t, s, user)
			})
		}
		token := requestResetLink(t, router, sender, user.Email)
		for _, body := range []map[string]interface{}{{"token": token + "x", "password": "new-password"}, {"token": token, "password": "short"}, {"password": "new-password"}} {
			response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", body, "")
			if response.Code != 400 {
				t.Fatalf("invalid input status=%d", response.Code)
			}
			assertResetUnchanged(t, s, user)
		}
	})
	t.Run("storage failure preserves authority", func(t *testing.T) {
		user := resetUser(t, s)
		repository := &resetCommitStore{Store: s, failure: errors.New("injected storage failure")}
		sender := &resetEmailCapture{links: make(chan string, 1)}
		router := resetRouter(t, repository, sender)
		token := requestResetLink(t, router, sender, user.Email)
		response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", map[string]interface{}{"token": token, "password": "new-password"}, "")
		if response.Code != 500 {
			t.Fatalf("storage failure status=%d", response.Code)
		}
		assertResetUnchanged(t, s, user)
		repository.failure = nil
		response = requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password?token="+token, map[string]interface{}{"password": "new-password"}, "")
		if response.Code != 200 {
			t.Fatalf("retry after failed commit status=%d", response.Code)
		}
	})
	t.Run("stale profile and email writes", func(t *testing.T) {
		user := resetUser(t, s)
		stale := *user
		reset := store.PasswordResetCommit{UserID: user.ID, Email: user.Email, AuthEpoch: "reset-epoch", PasswordHash: "reset-hash", ExpiresAt: time.Now().Add(time.Hour)}
		if err := s.ResetPassword(ctx, reset); err != nil {
			t.Fatal(err)
		}
		stale.DisplayName = "Stale edit"
		if err := s.UpdateUser(ctx, &stale); err == nil {
			t.Fatal("stale profile write succeeded")
		}
		newEmail := uuid.NewString() + "@example.com"
		if err := s.ChangeUserEmail(ctx, stale.ID, stale.Email, newEmail, &stale); err == nil {
			t.Fatal("stale email transaction succeeded")
		}
		actual, err := s.GetUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.AuthEpoch != reset.AuthEpoch || actual.PasswordHash != reset.PasswordHash || actual.DisplayName != user.DisplayName || actual.Email != user.Email {
			t.Fatal("stale writer reverted reset or partially edited user")
		}
		if _, err := s.GetUserByEmail(ctx, newEmail); err == nil {
			t.Fatal("failed email transaction left new identity claim")
		}
		actual.DisplayName = "Fresh edit"
		if err := s.UpdateUser(ctx, actual); err != nil {
			t.Fatalf("fresh profile: %v", err)
		}
		if err := s.ChangeUserEmail(ctx, actual.ID, actual.Email, newEmail, actual); err != nil {
			t.Fatalf("fresh email: %v", err)
		}
		assertResetUnchanged(t, s, actual)
	})
	t.Run("expired commit preserves state", func(t *testing.T) {
		user := resetUser(t, s)
		err := s.ResetPassword(ctx, store.PasswordResetCommit{UserID: user.ID, Email: user.Email, AuthEpoch: "next", PasswordHash: "new", ExpiresAt: time.Now().Add(-time.Second)})
		if !errors.Is(err, store.ErrInvalidPasswordReset) {
			t.Fatalf("expired commit: %v", err)
		}
		assertResetUnchanged(t, s, user)
	})
	t.Run("session issued from pre-reset snapshot", func(t *testing.T) {
		user := resetUser(t, s)
		reset := store.PasswordResetCommit{UserID: user.ID, Email: user.Email, AuthEpoch: "next-epoch", PasswordHash: "new-hash", ExpiresAt: time.Now().Add(time.Hour)}
		if err := s.ResetPassword(ctx, reset); err != nil {
			t.Fatal(err)
		}
		// Model an in-flight login/refresh that already loaded the old user before
		// reset, then persists its session after reset has committed.
		session := &models.AuthSession{TokenHash: []byte(uuid.NewString()), UserID: user.ID, AuthEpoch: user.AuthEpoch, ExpiresAt: time.Now().Add(time.Hour)}
		if err := s.CreateAuthSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConsumeAuthSession(ctx, session.TokenHash); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale session authenticated: %v", err)
		}
	})
	t.Run("failed DynamoDB call leaves reset available", func(t *testing.T) {
		user := resetUser(t, s)
		reset := store.PasswordResetCommit{UserID: user.ID, Email: user.Email, AuthEpoch: "next-epoch", PasswordHash: "new-hash", ExpiresAt: time.Now().Add(time.Hour)}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.ResetPassword(canceled, reset); err == nil {
			t.Fatal("canceled write succeeded")
		}
		assertResetUnchanged(t, s, user)
		if err := s.ResetPassword(ctx, reset); err != nil {
			t.Fatalf("retry failed: %v", err)
		}
	})
}
