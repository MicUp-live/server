package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const passkeyColumns = `id, user_id, public_key, attestation_type, aaguid, sign_count,
	transports, backup_eligible, backup_state, user_present, user_verified,
	friendly_name, created_at, last_used_at`

func scanPasskey(row interface{ Scan(...any) error }) (*models.PasskeyCredential, error) {
	var (
		cred          models.PasskeyCredential
		signCount     int64
		transportsRaw []byte
		createdAt     time.Time
		lastUsedAt    *time.Time
	)
	if err := row.Scan(
		&cred.ID,
		&cred.UserID,
		&cred.PublicKey,
		&cred.AttestationType,
		&cred.AAGUID,
		&signCount,
		&transportsRaw,
		&cred.BackupEligible,
		&cred.BackupState,
		&cred.UserPresent,
		&cred.UserVerified,
		&cred.FriendlyName,
		&createdAt,
		&lastUsedAt,
	); err != nil {
		return nil, err
	}
	cred.SignCount = uint32(signCount)
	if err := unmarshalJSON(transportsRaw, &cred.Transports); err != nil {
		return nil, fmt.Errorf("unmarshal passkey transports: %w", err)
	}
	cred.CreatedAt = createdAt.UTC()
	cred.LastUsedAt = derefTime(lastUsedAt)
	return &cred, nil
}

func altPasskeyID(id string) string {
	if strings.HasSuffix(id, "=") {
		return strings.TrimRight(id, "=")
	}
	switch len(id) % 4 {
	case 2:
		return id + "=="
	case 3:
		return id + "="
	default:
		return ""
	}
}

// CreateUserWithPasskey atomically creates a user and their initial WebAuthn credential.
func (s *Store) CreateUserWithPasskey(ctx context.Context, user *models.User, cred *models.PasskeyCredential) error {
	now := s.currentTime()
	created := user.CreatedAt.UTC()
	if user.CreatedAt.IsZero() {
		created = now
	}
	updated := now
	if !user.UpdatedAt.IsZero() {
		updated = user.UpdatedAt.UTC()
	}
	user.CreatedAt = created
	user.UpdatedAt = updated

	credCreated := cred.CreatedAt.UTC()
	if cred.CreatedAt.IsZero() {
		credCreated = now
		cred.CreatedAt = credCreated
	}

	transports := cred.Transports
	if transports == nil {
		transports = []string{}
	}
	transportsJSON, err := marshalJSON(transports)
	if err != nil {
		return fmt.Errorf("marshal passkey transports: %w", err)
	}

	normEmail := normalizedEmail(user.Email)

	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (
				id, email, display_name, avatar_url, password_hash, auth_epoch,
				is_host, email_verified, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			user.ID,
			normEmail,
			user.DisplayName,
			user.AvatarURL,
			user.PasswordHash,
			user.AuthEpoch,
			user.IsHost,
			user.EmailVerified,
			created,
			updated,
		); err != nil {
			return fmt.Errorf("create user with passkey: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO passkey_credentials (
				id, user_id, public_key, attestation_type, aaguid, sign_count,
				transports, backup_eligible, backup_state, user_present, user_verified,
				friendly_name, created_at, last_used_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			cred.ID,
			cred.UserID,
			cred.PublicKey,
			cred.AttestationType,
			cred.AAGUID,
			int64(cred.SignCount),
			transportsJSON,
			cred.BackupEligible,
			cred.BackupState,
			cred.UserPresent,
			cred.UserVerified,
			cred.FriendlyName,
			credCreated,
			nullTime(cred.LastUsedAt),
		); err != nil {
			return fmt.Errorf("create user with passkey: %w", err)
		}
		return nil
	})
}

// SavePasskeyCredential writes a WebAuthn credential record if the owning user is active.
func (s *Store) SavePasskeyCredential(ctx context.Context, cred *models.PasskeyCredential) error {
	created := cred.CreatedAt.UTC()
	if cred.CreatedAt.IsZero() {
		created = s.currentTime()
		cred.CreatedAt = created
	}
	transports := cred.Transports
	if transports == nil {
		transports = []string{}
	}
	transportsJSON, err := marshalJSON(transports)
	if err != nil {
		return fmt.Errorf("marshal passkey transports: %w", err)
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO passkey_credentials (
				id, user_id, public_key, attestation_type, aaguid, sign_count,
				transports, backup_eligible, backup_state, user_present, user_verified,
				friendly_name, created_at, last_used_at
			)
			SELECT $1, u.id, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
			FROM users u
			WHERE u.id = $2 AND u.deleting = FALSE
			ON CONFLICT (id) DO UPDATE SET
				user_id = EXCLUDED.user_id,
				public_key = EXCLUDED.public_key,
				attestation_type = EXCLUDED.attestation_type,
				aaguid = EXCLUDED.aaguid,
				sign_count = EXCLUDED.sign_count,
				transports = EXCLUDED.transports,
				backup_eligible = EXCLUDED.backup_eligible,
				backup_state = EXCLUDED.backup_state,
				user_present = EXCLUDED.user_present,
				user_verified = EXCLUDED.user_verified,
				friendly_name = EXCLUDED.friendly_name,
				created_at = EXCLUDED.created_at,
				last_used_at = EXCLUDED.last_used_at`,
			cred.ID,
			cred.UserID,
			cred.PublicKey,
			cred.AttestationType,
			cred.AAGUID,
			int64(cred.SignCount),
			transportsJSON,
			cred.BackupEligible,
			cred.BackupState,
			cred.UserPresent,
			cred.UserVerified,
			cred.FriendlyName,
			created,
			nullTime(cred.LastUsedAt),
		)
		if err != nil {
			return fmt.Errorf("save passkey credential: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var deleting bool
		err = tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1`, cred.UserID).Scan(&deleting)
		if err == nil && deleting {
			return store.ErrAccountDeleting
		}
		if isNotFound(err) {
			return fmt.Errorf("save passkey credential: %w", os.ErrNotExist)
		}
		if err != nil {
			return fmt.Errorf("save passkey credential: %w", err)
		}
		return fmt.Errorf("save passkey credential: user check failed")
	})
}

// GetPasskeyCredential loads a passkey credential by its credential ID, falling back
// to alternate base64 padding variants.
func (s *Store) GetPasskeyCredential(ctx context.Context, id string) (*models.PasskeyCredential, error) {
	query := `SELECT ` + passkeyColumns + ` FROM passkey_credentials WHERE id = $1`
	cred, err := scanPasskey(s.pool.QueryRow(ctx, query, id))
	if isNotFound(err) {
		if altID := altPasskeyID(id); altID != "" {
			cred, err = scanPasskey(s.pool.QueryRow(ctx, query, altID))
		}
	}
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get passkey credential: %w", err)
	}
	return cred, nil
}

// ListPasskeyCredentialsForUser returns all passkeys registered to a user.
func (s *Store) ListPasskeyCredentialsForUser(ctx context.Context, userID string) ([]*models.PasskeyCredential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+passkeyColumns+` FROM passkey_credentials WHERE user_id = $1 ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list passkeys for user: %w", err)
	}
	defer rows.Close()

	var creds []*models.PasskeyCredential
	for rows.Next() {
		cred, err := scanPasskey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan passkey credential: %w", err)
		}
		creds = append(creds, cred)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list passkeys for user: %w", err)
	}
	return creds, nil
}

// RecordPasskeyUse commits assertion metadata only while the verified credential and
// its owner are still active.
func (s *Store) RecordPasskeyUse(ctx context.Context, observed *models.PasskeyCredential, usage store.PasskeyUsage) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE passkey_credentials AS p
			 SET sign_count = $1,
			     last_used_at = $2,
			     backup_eligible = $3,
			     backup_state = $4
			 FROM users AS u
			 WHERE p.id = $5
			   AND p.user_id = $6
			   AND p.public_key = $7
			   AND p.sign_count = $8
			   AND p.backup_eligible = $9
			   AND u.id = p.user_id
			   AND u.deleting = FALSE`,
			int64(usage.SignCount),
			s.currentTime(),
			usage.BackupEligible,
			usage.BackupState,
			observed.ID,
			observed.UserID,
			observed.PublicKey,
			int64(observed.SignCount),
			observed.BackupEligible,
		)
		if err != nil {
			return fmt.Errorf("record passkey use: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		return classifyPasskeyUseFailure(ctx, tx, observed)
	})
}

func classifyPasskeyUseFailure(ctx context.Context, tx pgx.Tx, observed *models.PasskeyCredential) error {
	// Classify failure matching DynamoDB transaction cancellation order:
	// credential mismatch/absence returns ErrPasskeyChanged before checking user deletion.
	var (
		curUserID         string
		curPubKey         []byte
		curSignCount      int64
		curBackupEligible bool
	)
	err := tx.QueryRow(ctx,
		`SELECT user_id, public_key, sign_count, backup_eligible
		 FROM passkey_credentials WHERE id = $1`,
		observed.ID,
	).Scan(&curUserID, &curPubKey, &curSignCount, &curBackupEligible)
	if isNotFound(err) {
		return store.ErrPasskeyChanged
	}
	if err != nil {
		return fmt.Errorf("record passkey use: %w", err)
	}
	if curUserID != observed.UserID ||
		!bytes.Equal(curPubKey, observed.PublicKey) ||
		curSignCount != int64(observed.SignCount) ||
		curBackupEligible != observed.BackupEligible {
		return store.ErrPasskeyChanged
	}

	var deleting bool
	err = tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1`, observed.UserID).Scan(&deleting)
	if isNotFound(err) || (err == nil && deleting) {
		return store.ErrAccountDeleting
	}
	if err != nil {
		return fmt.Errorf("record passkey use: %w", err)
	}
	return store.ErrPasskeyChanged
}

// DeletePasskeyCredential deletes a passkey ensuring it belongs to the given user.
func (s *Store) DeletePasskeyCredential(ctx context.Context, id string, userID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM passkey_credentials WHERE id = $1 AND user_id = $2`,
			id, userID,
		)
		if err != nil {
			return fmt.Errorf("delete passkey credential: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var exists int
		err = tx.QueryRow(ctx, `SELECT 1 FROM passkey_credentials WHERE id = $1`, id).Scan(&exists)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("delete passkey credential: %w", err)
		}
		return fmt.Errorf("delete passkey credential: owner mismatch")
	})
}

// SaveWebAuthnSession writes a temporary ceremony challenge session.
func (s *Store) SaveWebAuthnSession(ctx context.Context, session *models.WebAuthnSession) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		// user_id has an FK to users(id), while new-user registration ceremonies generate
		// a provisional user ID before the user row exists and discoverable logins pass "".
		// Resolve to NULL unless the user already exists.
		if _, err := tx.Exec(ctx,
			`INSERT INTO webauthn_sessions (session_id, user_id, challenge, session_data, expires_at)
			 VALUES ($1, (SELECT id FROM users WHERE id = $2), $3, $4, $5)
			 ON CONFLICT (session_id) DO UPDATE SET
			     user_id = EXCLUDED.user_id,
			     challenge = EXCLUDED.challenge,
			     session_data = EXCLUDED.session_data,
			     expires_at = EXCLUDED.expires_at`,
			session.SessionID,
			session.UserID,
			session.Challenge,
			session.SessionData,
			session.ExpiresAt.UTC(),
		); err != nil {
			return fmt.Errorf("save webauthn session: %w", err)
		}
		return nil
	})
}

// ConsumeWebAuthnSession atomically deletes and returns a ceremony challenge session
// only if it has not expired.
func (s *Store) ConsumeWebAuthnSession(ctx context.Context, sessionID string) (*models.WebAuthnSession, error) {
	var out *models.WebAuthnSession
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var (
			sessID      string
			userID      *string
			challenge   string
			sessionData []byte
			expiresAt   time.Time
		)
		err := tx.QueryRow(ctx,
			`DELETE FROM webauthn_sessions
			 WHERE session_id = $1 AND expires_at > $2
			 RETURNING session_id, user_id, challenge, session_data, expires_at`,
			sessionID, s.currentTime(),
		).Scan(&sessID, &userID, &challenge, &sessionData, &expiresAt)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("consume webauthn session: %w", err)
		}
		out = &models.WebAuthnSession{
			SessionID:   sessID,
			UserID:      derefString(userID),
			Challenge:   challenge,
			SessionData: sessionData,
			ExpiresAt:   expiresAt.UTC(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
