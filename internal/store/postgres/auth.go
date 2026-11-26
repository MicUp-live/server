package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const (
	userColumns = `id, email, display_name, avatar_url, password_hash, auth_epoch,
	is_host, email_verified, deleting, deletion_epoch, deletion_auth_epoch,
	deletion_started_at, deletion_step, deletion_error,
	pending_catalog_versions, favorite_songs, favorite_artists,
	created_at, updated_at`

	maxAuthCASAttempts = 32
)

var errAuthCASRetry = errors.New("auth CAS retry")

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

type userScanResult struct {
	user       *models.User
	pendingRaw []byte
}

func scanUserWithPendingRaw(row interface{ Scan(...any) error }) (userScanResult, error) {
	var (
		u                            models.User
		deletionStartedAt            *time.Time
		pendingRaw, songsRaw, artRaw []byte
		createdAt, updatedAt         time.Time
	)
	if err := row.Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.PasswordHash, &u.AuthEpoch,
		&u.IsHost, &u.EmailVerified, &u.Deleting, &u.DeletionEpoch, &u.DeletionAuthEpoch,
		&deletionStartedAt, &u.DeletionStep, &u.DeletionError,
		&pendingRaw, &songsRaw, &artRaw, &createdAt, &updatedAt,
	); err != nil {
		return userScanResult{}, err
	}
	u.DeletionStartedAt = derefTime(deletionStartedAt)
	u.CreatedAt, u.UpdatedAt = createdAt.UTC(), updatedAt.UTC()
	if err := unmarshalJSON(pendingRaw, &u.PendingCatalogVersions); err != nil {
		return userScanResult{}, fmt.Errorf("unmarshal pending_catalog_versions: %w", err)
	}
	if err := unmarshalJSON(songsRaw, &u.FavoriteSongs); err != nil {
		return userScanResult{}, fmt.Errorf("unmarshal favorite_songs: %w", err)
	}
	if err := unmarshalJSON(artRaw, &u.FavoriteArtists); err != nil {
		return userScanResult{}, fmt.Errorf("unmarshal favorite_artists: %w", err)
	}
	return userScanResult{user: &u, pendingRaw: pendingRaw}, nil
}

func scanUser(row interface{ Scan(...any) error }) (*models.User, error) {
	res, err := scanUserWithPendingRaw(row)
	if err != nil {
		return nil, err
	}
	return res.user, nil
}

func classifyUserMutationFailure(ctx context.Context, tx pgx.Tx, userID, op, fallback string) error {
	var deleting bool
	err := tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1`, userID).Scan(&deleting)
	if err == nil && deleting {
		return store.ErrAccountDeleting
	}
	if isNotFound(err) {
		return fmt.Errorf("%s: %w", op, os.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: %s", op, fallback)
}

// ResetPassword consumes reset authority, changes the password, and invalidates
// all previous bearer sessions in one conditional update of the user record.
func (s *Store) ResetPassword(ctx context.Context, reset store.PasswordResetCommit) error {
	now := s.currentTime()
	normEmail := normalizedEmail(reset.Email)
	if reset.UserID == "" || normEmail == "" || reset.AuthEpoch == "" ||
		reset.AuthEpoch == reset.ExpectedAuthEpoch || reset.PasswordHash == "" ||
		!reset.ExpiresAt.After(now) {
		return store.ErrInvalidPasswordReset
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		txNow := s.currentTime()
		tag, err := tx.Exec(ctx,
			`UPDATE users SET password_hash = $1, auth_epoch = $2, updated_at = $3
			 WHERE id = $4 AND email = $5 AND deleting = FALSE AND auth_epoch = $6 AND $7 > $8`,
			reset.PasswordHash, reset.AuthEpoch, txNow,
			reset.UserID, normEmail, reset.ExpectedAuthEpoch, reset.ExpiresAt.UTC(), txNow,
		)
		if err != nil {
			return fmt.Errorf("reset password: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return store.ErrInvalidPasswordReset
		}
		return nil
	})
}

// CreateUser atomically reserves the normalized email and creates its user.
func (s *Store) CreateUser(ctx context.Context, user *models.User) error {
	now := s.currentTime()
	created := user.CreatedAt.UTC()
	if user.CreatedAt.IsZero() {
		created = now
	}
	updated := now
	if !user.UpdatedAt.IsZero() {
		updated = user.UpdatedAt.UTC()
	}
	user.CreatedAt, user.UpdatedAt = created, updated

	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (id, email, display_name, avatar_url, password_hash, auth_epoch, is_host, email_verified, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			user.ID, normalizedEmail(user.Email), user.DisplayName, user.AvatarURL, user.PasswordHash,
			user.AuthEpoch, user.IsHost, user.EmailVerified, created, updated,
		); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		return nil
	})
}

func (s *Store) updateUserRecord(ctx context.Context, userID, oldEmail string, checkOldEmail bool, user *models.User, op, staleMsg string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		query := `UPDATE users SET email = $1, display_name = $2, avatar_url = $3, password_hash = $4,
			auth_epoch = $5, is_host = $6, email_verified = $7, updated_at = $8
			WHERE id = $9 AND deleting = FALSE AND auth_epoch = $10`
		args := []any{
			normalizedEmail(user.Email), user.DisplayName, user.AvatarURL, user.PasswordHash,
			user.AuthEpoch, user.IsHost, user.EmailVerified, user.UpdatedAt, userID, user.AuthEpoch,
		}
		if checkOldEmail {
			query += ` AND email = $11`
			args = append(args, oldEmail)
		}
		tag, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		return classifyUserMutationFailure(ctx, tx, userID, op, staleMsg)
	})
}

// UpdateUser persists changes to an existing user profile (display name, is_host, avatar, etc.).
func (s *Store) UpdateUser(ctx context.Context, user *models.User) error {
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	user.UpdatedAt = s.currentTime()
	return s.updateUserRecord(ctx, user.ID, "", false, user, "update user", "auth epoch conflict")
}

// ChangeUserEmail transactionally updates the user's email while ensuring no other
// account holds the new normalized email.
func (s *Store) ChangeUserEmail(ctx context.Context, userID, oldEmail, newEmail string, user *models.User) error {
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	normOld := normalizedEmail(oldEmail)
	normNew := normalizedEmail(newEmail)
	user.UpdatedAt = s.currentTime()
	user.Email = normNew
	if normOld == normNew {
		return s.updateUserRecord(ctx, userID, "", false, user, "update user", "auth epoch conflict")
	}
	return s.updateUserRecord(ctx, userID, normOld, true, user, "change user email", "stale email or auth epoch")
}

func (s *Store) getUserWhere(ctx context.Context, where, arg, op string) (*models.User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE `+where, arg))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return user, nil
}

// GetUserByEmail resolves a normalized email to its user record.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	norm := normalizedEmail(email)
	if norm == "" {
		return nil, os.ErrNotExist
	}
	return s.getUserWhere(ctx, "email = $1", norm, "get user by email")
}

// GetUser loads a user by ID.
func (s *Store) GetUser(ctx context.Context, id string) (*models.User, error) {
	return s.getUserWhere(ctx, "id = $1", id, "get user")
}

// CreateAuthSession stores a single-use refresh session conditioned on the user
// existing and not undergoing account deletion.
func (s *Store) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO auth_sessions (token_hash, user_id, auth_epoch, expires_at)
			 SELECT $1, u.id, $3, $4 FROM users u WHERE u.id = $2 AND u.deleting = FALSE`,
			hex.EncodeToString(session.TokenHash), session.UserID, session.AuthEpoch, session.ExpiresAt.UTC(),
		)
		if err != nil {
			return fmt.Errorf("create auth session: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		return classifyUserMutationFailure(ctx, tx, session.UserID, "create auth session", "user check failed")
	})
}

// ConsumeAuthSession atomically deletes a valid, unexpired refresh session and
// verifies the owning user is active and matches the session's auth_epoch.
func (s *Store) ConsumeAuthSession(ctx context.Context, tokenHash []byte) (*models.User, error) {
	if len(tokenHash) == 0 {
		return nil, os.ErrNotExist
	}
	var user *models.User
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var sessionUserID, sessionAuthEpoch string
		err := tx.QueryRow(ctx,
			`DELETE FROM auth_sessions WHERE token_hash = $1 AND expires_at > $2 RETURNING user_id, auth_epoch`,
			hex.EncodeToString(tokenHash), s.currentTime(),
		).Scan(&sessionUserID, &sessionAuthEpoch)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("consume auth session: %w", err)
		}

		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 FOR UPDATE`, sessionUserID))
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		if u.Deleting || u.AuthEpoch != sessionAuthEpoch {
			return os.ErrNotExist
		}
		user = u
		return nil
	})
	return user, err
}

// DeleteAuthSessionsForUser removes all refresh sessions owned by a user.
func (s *Store) DeleteAuthSessionsForUser(ctx context.Context, userID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("delete user auth sessions: %w", err)
		}
		return nil
	})
}

func (s *Store) withAuthCAS(ctx context.Context, op string, fn func(pgx.Tx) error) error {
	for attempt := 0; attempt < maxAuthCASAttempts; attempt++ {
		err := s.withTx(ctx, fn)
		if errors.Is(err, errAuthCASRetry) {
			continue
		}
		return err
	}
	return fmt.Errorf("%s: exceeded max conflict retries", op)
}

// BeginAccountDeletion atomically transitions a user into irreversible deletion state,
// rotating auth_epoch and merging any pending catalog versions.
func (s *Store) BeginAccountDeletion(ctx context.Context, userID string, pendingCatalogVersions []string) (*models.User, error) {
	var result *models.User
	err := s.withAuthCAS(ctx, "begin account deletion", func(tx pgx.Tx) error {
		cur, err := scanUserWithPendingRaw(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("begin account deletion: %w", err)
		}

		now := s.currentTime()
		mergedPending := mergePendingCatalogVersions(cur.user.PendingCatalogVersions, pendingCatalogVersions)
		pendingJSON, err := marshalStringList(mergedPending)
		if err != nil {
			return fmt.Errorf("marshal pending catalog versions: %w", err)
		}

		var updated *models.User
		if !cur.user.Deleting {
			deletionEpoch := uuid.NewString()
			updated, err = scanUser(tx.QueryRow(ctx,
				`UPDATE users
				 SET deleting = TRUE, deletion_epoch = $1, deletion_auth_epoch = $2,
				     deletion_started_at = $3, auth_epoch = $4, password_hash = '',
				     pending_catalog_versions = $5, updated_at = $6
				 WHERE id = $7 AND deleting = FALSE AND deletion_epoch = '' AND auth_epoch = $8
				 RETURNING `+userColumns,
				deletionEpoch, cur.user.AuthEpoch, now, "deleting:"+deletionEpoch,
				pendingJSON, now, userID, cur.user.AuthEpoch,
			))
		} else {
			if slices.Equal(cur.user.PendingCatalogVersions, mergedPending) {
				result = cur.user
				return nil
			}
			updated, err = scanUser(tx.QueryRow(ctx,
				`UPDATE users SET pending_catalog_versions = $1, updated_at = $2
				 WHERE id = $3 AND deleting = TRUE AND deletion_epoch = $4 AND pending_catalog_versions = $5::jsonb
				 RETURNING `+userColumns,
				pendingJSON, now, userID, cur.user.DeletionEpoch, cur.pendingRaw,
			))
		}
		if isNotFound(err) {
			return errAuthCASRetry
		}
		if err != nil {
			return fmt.Errorf("begin account deletion: %w", err)
		}
		result = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RecordAccountDeletionProgress updates the durable step/error and removes resolved
// catalog versions using compare-and-swap retry to prevent lost updates.
func (s *Store) RecordAccountDeletionProgress(ctx context.Context, userID, step, errMsg string, resolvedCatalogVersions []string) error {
	return s.withAuthCAS(ctx, "record account deletion progress", func(tx pgx.Tx) error {
		cur, err := scanUserWithPendingRaw(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("record account deletion progress: %w", err)
		}
		if !cur.user.Deleting || cur.user.DeletionEpoch == "" {
			return errors.New("account is not marked for deletion")
		}

		now := s.currentTime()
		nextPendingRaw := cur.pendingRaw
		if len(resolvedCatalogVersions) > 0 {
			remaining := subtractPendingCatalogVersions(cur.user.PendingCatalogVersions, resolvedCatalogVersions)
			if nextPendingRaw, err = marshalStringList(remaining); err != nil {
				return fmt.Errorf("marshal pending catalog versions: %w", err)
			}
		}
		tag, err := tx.Exec(ctx,
			`UPDATE users
			 SET deletion_step = $1, deletion_error = $2, pending_catalog_versions = $3, updated_at = $4
			 WHERE id = $5 AND deleting = TRUE AND deletion_epoch = $6 AND pending_catalog_versions = $7::jsonb`,
			step, errMsg, nextPendingRaw, now, userID, cur.user.DeletionEpoch, cur.pendingRaw,
		)
		if err != nil {
			return fmt.Errorf("record account deletion progress: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errAuthCASRetry
		}
		return nil
	})
}

// ListDeletingUsers returns all user records currently marked as undergoing deletion.
func (s *Store) ListDeletingUsers(ctx context.Context) ([]*models.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users WHERE deleting = TRUE`)
	if err != nil {
		return nil, fmt.Errorf("list deleting users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deleting user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deleting users: %w", err)
	}
	return users, nil
}

// DeleteUser atomically removes a user record once deleting = TRUE and all pending
// catalog versions are resolved. Deleting an already-absent user is idempotent.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		if !u.Deleting || u.DeletionEpoch == "" {
			return errors.New("delete user: account is not in deleting state")
		}
		if len(u.PendingCatalogVersions) > 0 {
			return fmt.Errorf("delete user: %d pending catalog versions remain unpurged", len(u.PendingCatalogVersions))
		}

		tag, err := tx.Exec(ctx,
			`DELETE FROM users WHERE id = $1 AND deleting = TRUE AND deletion_epoch = $2 AND pending_catalog_versions = '[]'::jsonb`,
			userID, u.DeletionEpoch,
		)
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var exists int
		err = tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id = $1`, userID).Scan(&exists)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return fmt.Errorf("delete user: concurrent modification")
	})
}

func marshalStringList(items []string) ([]byte, error) {
	if items == nil {
		items = []string{}
	}
	return marshalJSON(items)
}

func mergePendingCatalogVersions(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	var merged []string
	for _, list := range [][]string{existing, incoming} {
		for _, raw := range list {
			if id := strings.TrimSpace(raw); id != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					merged = append(merged, id)
				}
			}
		}
	}
	return merged
}

func subtractPendingCatalogVersions(current, resolved []string) []string {
	resolvedSet := make(map[string]struct{}, len(resolved))
	for _, id := range resolved {
		if clean := strings.TrimSpace(id); clean != "" {
			resolvedSet[clean] = struct{}{}
		}
	}
	var remaining []string
	for _, id := range current {
		if clean := strings.TrimSpace(id); clean != "" {
			if _, ok := resolvedSet[clean]; !ok {
				remaining = append(remaining, clean)
			}
		}
	}
	return remaining
}
