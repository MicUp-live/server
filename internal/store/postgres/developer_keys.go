package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const developerKeyColumns = `id, owner_id, name, key_prefix, scopes, scope_version,
	revoked, revoked_at, last_used_at, expires_at, created_at`

func hashRawKey(rawKey string) string {
	digest := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(digest[:])
}

func scanDeveloperKey(row interface{ Scan(...any) error }) (*models.DeveloperKey, error) {
	var (
		key        models.DeveloperKey
		scopesRaw  []byte
		revokedAt  *time.Time
		lastUsedAt *time.Time
		expiresAt  *time.Time
		createdAt  time.Time
	)
	if err := row.Scan(
		&key.ID,
		&key.OwnerID,
		&key.Name,
		&key.KeyPrefix,
		&scopesRaw,
		&key.ScopeVersion,
		&key.Revoked,
		&revokedAt,
		&lastUsedAt,
		&expiresAt,
		&createdAt,
	); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(scopesRaw, &key.Scopes); err != nil {
		return nil, fmt.Errorf("unmarshal developer key scopes: %w", err)
	}
	key.CreatedAt = createdAt.UTC()
	key.RevokedAt = derefTime(revokedAt)
	key.LastUsedAt = derefTime(lastUsedAt)
	key.ExpiresAt = derefTime(expiresAt)
	return &key, nil
}

// CreateDeveloperKey hashes rawKey (SHA-256 hex) and persists it as the key's ID.
func (s *Store) CreateDeveloperKey(ctx context.Context, rawKey string, key *models.DeveloperKey) error {
	created := key.CreatedAt.UTC()
	if key.CreatedAt.IsZero() {
		created = s.currentTime()
		key.CreatedAt = created
	}
	scopes := key.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	scopesJSON, err := marshalJSON(scopes)
	if err != nil {
		return fmt.Errorf("marshal developer key scopes: %w", err)
	}
	keyHash := hashRawKey(rawKey)

	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO developer_keys (
				id, owner_id, name, key_prefix, scopes, scope_version,
				revoked, revoked_at, last_used_at, expires_at, created_at
			)
			SELECT $1, u.id, $3, $4, $5, $6, $7, $8, $9, $10, $11
			FROM users u
			WHERE u.id = $2 AND u.deleting = FALSE`,
			keyHash,
			key.OwnerID,
			key.Name,
			key.KeyPrefix,
			scopesJSON,
			1,
			key.Revoked,
			nullTime(key.RevokedAt),
			nullTime(key.LastUsedAt),
			nullTime(key.ExpiresAt),
			created,
		)
		if err != nil {
			return fmt.Errorf("create developer key: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var deleting bool
		err = tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1`, key.OwnerID).Scan(&deleting)
		if err == nil && deleting {
			return store.ErrAccountDeleting
		}
		if isNotFound(err) {
			return fmt.Errorf("create developer key: %w", os.ErrNotExist)
		}
		if err != nil {
			return fmt.Errorf("create developer key: %w", err)
		}
		return fmt.Errorf("create developer key: owner check failed")
	})
}

// GetDeveloperKey resolves a raw key token to its stored record by hashing it.
func (s *Store) GetDeveloperKey(ctx context.Context, rawKey string) (*models.DeveloperKey, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+developerKeyColumns+` FROM developer_keys WHERE id = $1`,
		hashRawKey(rawKey),
	)
	key, err := scanDeveloperKey(row)
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get developer key: %w", err)
	}
	return key, nil
}

// ListDeveloperKeys returns every key issued to a single owner.
func (s *Store) ListDeveloperKeys(ctx context.Context, ownerID string) ([]*models.DeveloperKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+developerKeyColumns+` FROM developer_keys WHERE owner_id = $1 ORDER BY created_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list developer keys: %w", err)
	}
	defer rows.Close()

	var keys []*models.DeveloperKey
	for rows.Next() {
		key, err := scanDeveloperKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan developer key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list developer keys: %w", err)
	}
	return keys, nil
}

// RevokeDeveloperKey deletes a key after verifying it belongs to ownerID.
func (s *Store) RevokeDeveloperKey(ctx context.Context, id, ownerID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM developer_keys WHERE id = $1 AND owner_id = $2`,
			id, ownerID,
		)
		if err != nil {
			return fmt.Errorf("revoke developer key: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var exists int
		err = tx.QueryRow(ctx, `SELECT 1 FROM developer_keys WHERE id = $1`, id).Scan(&exists)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("revoke developer key: %w", err)
		}
		return fmt.Errorf("revoke developer key: owner mismatch")
	})
}

// TouchDeveloperKeyLastUsed records the last use of an unrevoked key by its stored ID.
func (s *Store) TouchDeveloperKeyLastUsed(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE developer_keys SET last_used_at = $1 WHERE id = $2 AND revoked = FALSE`,
			s.currentTime(), id,
		)
		if err != nil {
			return fmt.Errorf("touch developer key last used: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("touch developer key last used: key not found or revoked")
		}
		return nil
	})
}
