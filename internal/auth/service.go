// Package auth owns password-reset decisions and orchestration.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const maxPasswordBytes = 72

var (
	ErrShortPassword = errors.New("password must be at least 8 characters")
	ErrLongPassword  = errors.New("password must be at most 72 bytes")
)

// ValidatePassword enforces minimum character and bcrypt maximum byte bounds.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrShortPassword
	}
	if len(password) > maxPasswordBytes {
		return ErrLongPassword
	}
	return nil
}

type ResetStore interface {
	GetUserByEmail(context.Context, string) (*models.User, error)
	ResetPassword(context.Context, store.PasswordResetCommit) error
}

type Service struct {
	store  ResetStore
	secret []byte
	now    func() time.Time
}

func NewService(repository ResetStore, secret string) *Service {
	return &Service{store: repository, secret: []byte(secret), now: time.Now}
}

type resetClaims struct {
	UserID    string `json:"sub"`
	Email     string `json:"email"`
	Action    string `json:"act"`
	ExpiresAt int64  `json:"exp"`
	AuthEpoch string `json:"auth_epoch,omitempty"`
}

// IssueResetToken binds a one-hour link to the user's current authentication epoch.
// Links issued before an epoch was introduced remain valid only until the first reset.
func (s *Service) IssueResetToken(user *models.User) (string, error) {
	if user == nil || user.Deleting {
		return "", store.ErrInvalidPasswordReset
	}
	payload, err := json.Marshal(resetClaims{UserID: user.ID, Email: user.Email,
		Action: "reset-password", ExpiresAt: s.now().Add(time.Hour).Unix(), AuthEpoch: user.AuthEpoch})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// RequestPasswordReset reports delivery/storage failures to its caller. The HTTP
// adapter still returns a neutral response so account existence remains private.
func (s *Service) RequestPasswordReset(ctx context.Context, address, baseURL string, sender email.Sender) error {
	user, err := s.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(address)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find reset account: %w", err)
	}
	if user == nil || user.Deleting || sender == nil {
		return nil
	}
	token, err := s.IssueResetToken(user)
	if err != nil {
		return err
	}
	// Complete delivery synchronously, even if the request disconnects in Lambda.
	deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return sender.SendPasswordResetEmail(deliveryCtx, user.Email, user.DisplayName,
		fmt.Sprintf("%s/reset-password?token=%s", baseURL, token))
}

// ConfirmPasswordReset consumes all links for the prior epoch and revokes prior
// bearer sessions in the same conditional user update as password replacement.
// A conflict is terminal: retrying an obsolete authorization cannot be safe.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	claims, err := s.parseResetToken(token)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash reset password: %w", err)
	}
	epoch := make([]byte, 32)
	if _, err := rand.Read(epoch); err != nil {
		return err
	}
	if claims.ExpiresAt <= s.now().Unix() {
		return store.ErrInvalidPasswordReset
	}
	return s.store.ResetPassword(ctx, store.PasswordResetCommit{UserID: claims.UserID,
		Email: claims.Email, ExpectedAuthEpoch: claims.AuthEpoch,
		AuthEpoch: base64.RawURLEncoding.EncodeToString(epoch), PasswordHash: string(hash),
		ExpiresAt: time.Unix(claims.ExpiresAt, 0)})
}

func (s *Service) parseResetToken(token string) (*resetClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, store.ErrInvalidPasswordReset
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, store.ErrInvalidPasswordReset
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, store.ErrInvalidPasswordReset
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, store.ErrInvalidPasswordReset
	}
	var claims resetClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.UserID == "" || claims.Email == "" ||
		claims.Action != "reset-password" || claims.ExpiresAt <= s.now().Unix() {
		return nil, store.ErrInvalidPasswordReset
	}
	return &claims, nil
}
