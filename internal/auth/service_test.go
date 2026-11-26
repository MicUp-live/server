package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type resetTestStore struct {
	user               *models.User
	readErr, commitErr error
	commit             *store.PasswordResetCommit
	address            string
}

func (s *resetTestStore) GetUserByEmail(_ context.Context, address string) (*models.User, error) {
	s.address = address
	return s.user, s.readErr
}
func (s *resetTestStore) ResetPassword(_ context.Context, reset store.PasswordResetCommit) error {
	s.commit = &reset
	return s.commitErr
}

type resetTestSender struct {
	deliveryErr error
	delivered   bool
	canceled    bool
	deadline    bool
}

func (s *resetTestSender) SendVerificationEmail(context.Context, string, string, string) error {
	return nil
}
func (s *resetTestSender) SendPasswordResetEmail(ctx context.Context, _, _, _ string) error {
	s.delivered = true
	s.canceled = ctx.Err() != nil
	_, s.deadline = ctx.Deadline()
	return s.deliveryErr
}
func (s *resetTestSender) SendSongbookInvitationEmail(context.Context, email.SongbookInvitationEmail) error {
	return nil
}

func TestRequestPasswordResetFailureAndDelivery(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, tc := range []struct {
		name                            string
		user                            *models.User
		readErr, deliveryErr, errorWant error
		deliver                         bool
	}{
		{name: "missing", readErr: os.ErrNotExist},
		{name: "read failure", readErr: failure, errorWant: failure},
		{name: "delivery failure", user: &models.User{ID: "u", Email: "host@example.com"}, deliveryErr: failure, errorWant: failure, deliver: true},
		{name: "delivery after disconnect", user: &models.User{ID: "u", Email: "host@example.com"}, deliver: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &resetTestStore{user: tc.user, readErr: tc.readErr}
			sender := &resetTestSender{deliveryErr: tc.deliveryErr}
			svc := NewService(repository, "test-signing-secret-with-at-least-32-bytes")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := svc.RequestPasswordReset(ctx, " Host@Example.COM ", "https://example.com", sender)
			if !errors.Is(err, tc.errorWant) {
				t.Fatalf("error=%v; want %v", err, tc.errorWant)
			}
			if repository.address != "host@example.com" || sender.delivered != tc.deliver {
				t.Fatal("lookup or delivery behavior changed")
			}
			if sender.delivered && (sender.canceled || !sender.deadline) {
				t.Fatal("delivery must survive disconnect with bounded timeout")
			}
		})
	}
}
func TestConfirmPasswordResetExpiresBeforeCommit(t *testing.T) {
	repository := &resetTestStore{}
	svc := NewService(repository, "test-signing-secret-with-at-least-32-bytes")
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	token, err := svc.IssueResetToken(&models.User{ID: "u", Email: "host@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.now = func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return now.Add(time.Hour)
	}
	err = svc.ConfirmPasswordReset(context.Background(), token, "new-password")
	if !errors.Is(err, store.ErrInvalidPasswordReset) || repository.commit != nil {
		t.Fatalf("expired reset reached commit: %v", err)
	}
}
func TestConfirmPasswordResetForwardsAtomicCommitFailure(t *testing.T) {
	failure := errors.New("commit failed")
	repository := &resetTestStore{commitErr: failure}
	svc := NewService(repository, "test-signing-secret-with-at-least-32-bytes")
	user := &models.User{ID: "u", Email: "host@example.com", AuthEpoch: "old-epoch"}
	token, err := svc.IssueResetToken(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), token, "new-password"); !errors.Is(err, failure) {
		t.Fatalf("commit failure=%v", err)
	}
	if repository.commit == nil || repository.commit.ExpectedAuthEpoch != user.AuthEpoch || repository.commit.AuthEpoch == "" || repository.commit.AuthEpoch == user.AuthEpoch {
		t.Fatal("reset did not bind and advance authority")
	}
}

func TestValidatePasswordRejectsShortAndOverlongPasswords(t *testing.T) {
	if err := ValidatePassword("short"); !errors.Is(err, ErrShortPassword) {
		t.Fatalf("short password err = %v, want ErrShortPassword", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 72)); err != nil {
		t.Fatalf("72-byte password err = %v, want nil", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 73)); !errors.Is(err, ErrLongPassword) {
		t.Fatalf("73-byte password err = %v, want ErrLongPassword", err)
	}
}
