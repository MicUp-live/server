package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const songbookColumns = `id, owner_id, public_id, slug, venue_id, catalog_version_id,
	name, is_public, song_count, catalog_generation,
	deleting, deletion_confirmed, created_at, updated_at`

func scanSongbook(row pgx.Row) (*models.Songbook, error) {
	var (
		sb               models.Songbook
		slug             *string
		catalogVersionID *string
	)
	err := row.Scan(
		&sb.ID,
		&sb.OwnerID,
		&sb.PublicID,
		&slug,
		&sb.VenueID,
		&catalogVersionID,
		&sb.Name,
		&sb.IsPublic,
		&sb.SongCount,
		&sb.CatalogGeneration,
		&sb.Deleting,
		&sb.DeletionConfirmed,
		&sb.CreatedAt,
		&sb.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	sb.Slug = derefString(slug)
	sb.CatalogVersionID = derefString(catalogVersionID)
	sb.CreatedAt = sb.CreatedAt.UTC()
	sb.UpdatedAt = sb.UpdatedAt.UTC()
	return &sb, nil
}

func scanSongbooks(rows pgx.Rows) ([]*models.Songbook, error) {
	defer rows.Close()
	var out []*models.Songbook
	for rows.Next() {
		sb, err := scanSongbook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// checkSongbookWriteFence verifies inside tx that songbookID exists, is not deleting,
// and if a collaborator edit actor is in ctx, still holds can_edit_catalog = TRUE.
func checkSongbookWriteFence(ctx context.Context, tx pgx.Tx, songbookID string) error {
	var deleting bool
	err := tx.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1 FOR UPDATE`, songbookID).Scan(&deleting)
	if isNotFound(err) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if deleting {
		return store.ErrSongbookDeleting
	}
	if actorID := store.SongbookEditActorFromContext(ctx); actorID != "" {
		var canEdit bool
		err := tx.QueryRow(ctx,
			`SELECT can_edit_catalog FROM songbook_grants WHERE songbook_id = $1 AND user_id = $2 FOR UPDATE`,
			songbookID, actorID,
		).Scan(&canEdit)
		if isNotFound(err) || (err == nil && !canEdit) {
			return store.ErrPermissionDenied
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateSongbook stores songbook metadata with initial sync generations.
func (s *Store) CreateSongbook(ctx context.Context, sb *models.Songbook) error {
	if strings.TrimSpace(sb.OwnerID) == "" {
		return fmt.Errorf("create songbook: owner is required: %w", os.ErrInvalid)
	}
	now := s.currentTime()
	if sb.CreatedAt.IsZero() {
		sb.CreatedAt = now
	} else {
		sb.CreatedAt = sb.CreatedAt.UTC()
	}
	if sb.UpdatedAt.IsZero() {
		sb.UpdatedAt = now
	} else {
		sb.UpdatedAt = sb.UpdatedAt.UTC()
	}

	normSlug := models.NormalizeSlug(sb.Slug)

	return s.withTx(ctx, func(tx pgx.Tx) error {
		var ownerDeleting bool
		err := tx.QueryRow(ctx, `SELECT deleting FROM users WHERE id = $1 FOR UPDATE`, sb.OwnerID).Scan(&ownerDeleting)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("create songbook check owner: %w", err)
		}
		if ownerDeleting {
			return store.ErrAccountDeleting
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO songbooks (
				id, owner_id, public_id, slug, venue_id, catalog_version_id,
				name, is_public, song_count, catalog_generation,
				deleting, deletion_confirmed, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			sb.ID,
			sb.OwnerID,
			sb.PublicID,
			nullString(normSlug),
			sb.VenueID,
			nullString(sb.CatalogVersionID),
			sb.Name,
			sb.IsPublic,
			sb.SongCount,
			sb.CatalogGeneration,
			sb.Deleting,
			sb.DeletionConfirmed,
			sb.CreatedAt,
			sb.UpdatedAt,
		)
		if err != nil {
			if isUniqueViolation(err) {
				switch pgConstraintName(err) {
				case "songbooks_pkey":
					return os.ErrExist
				case "idx_songbooks_public_id":
					return fmt.Errorf("create songbook: public_id already exists: %w", os.ErrExist)
				case "idx_songbooks_slug":
					return fmt.Errorf("create songbook: slug already exists: %w", os.ErrExist)
				default:
					return fmt.Errorf("create songbook: %w", os.ErrExist)
				}
			}
			if isForeignKeyViolation(err) {
				return fmt.Errorf("create songbook: %w", os.ErrNotExist)
			}
			return fmt.Errorf("create songbook: %w", err)
		}
		return nil
	})
}

// UpdateSongbook changes owner-editable metadata without replacing sync state.
func (s *Store) UpdateSongbook(ctx context.Context, sb *models.Songbook) error {
	now := s.currentTime()
	normSlug := models.NormalizeSlug(sb.Slug)

	return s.withTx(ctx, func(tx pgx.Tx) error {
		updated, err := scanSongbook(tx.QueryRow(ctx, `
			UPDATE songbooks
			SET name = $1, is_public = $2, slug = $3, updated_at = $4
			WHERE id = $5 AND deleting = FALSE
			RETURNING `+songbookColumns,
			sb.Name, sb.IsPublic, nullString(normSlug), now, sb.ID,
		))
		if isNotFound(err) {
			var deleting bool
			chkErr := tx.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1`, sb.ID).Scan(&deleting)
			if chkErr == nil && deleting {
				return fmt.Errorf("update songbook %s: %w", sb.ID, store.ErrSongbookDeleting)
			}
			return fmt.Errorf("update songbook %s: %w", sb.ID, os.ErrNotExist)
		}
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("update songbook %s: %w", sb.ID, os.ErrExist)
			}
			return fmt.Errorf("update songbook: %w", err)
		}
		*sb = *updated
		return nil
	})
}

// DeleteSongbook removes songbook metadata.
func (s *Store) DeleteSongbook(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var deletedID string
		err := tx.QueryRow(ctx, `DELETE FROM songbooks WHERE id = $1 RETURNING id`, id).Scan(&deletedID)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("delete songbook: %w", err)
		}
		return nil
	})
}

// GetSongbook loads songbook metadata.
func (s *Store) GetSongbook(ctx context.Context, id string) (*models.Songbook, error) {
	sb, err := scanSongbook(s.pool.QueryRow(ctx, `SELECT `+songbookColumns+` FROM songbooks WHERE id = $1`, id))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get songbook: %w", err)
	}
	return sb, nil
}

// GetPublicSongbook resolves a public ID or slug and rejects unpublished records.
func (s *Store) GetPublicSongbook(ctx context.Context, publicID string) (*models.Songbook, error) {
	sb, err := scanSongbook(s.pool.QueryRow(ctx,
		`SELECT `+songbookColumns+` FROM songbooks WHERE public_id = $1 AND is_public = TRUE LIMIT 1`,
		publicID,
	))
	if err == nil {
		return sb, nil
	}
	if !isNotFound(err) {
		return nil, fmt.Errorf("get public songbook: %w", err)
	}

	norm := models.NormalizeSlug(publicID)
	if norm != "" {
		sb, err = scanSongbook(s.pool.QueryRow(ctx,
			`SELECT `+songbookColumns+` FROM songbooks WHERE slug = $1 AND is_public = TRUE LIMIT 1`,
			norm,
		))
		if err == nil {
			return sb, nil
		}
		if !isNotFound(err) {
			return nil, fmt.Errorf("get public songbook: %w", err)
		}
	}
	return nil, os.ErrNotExist
}

// GetSongbookBySlug resolves a songbook by its unique slug.
func (s *Store) GetSongbookBySlug(ctx context.Context, slug string) (*models.Songbook, error) {
	norm := models.NormalizeSlug(slug)
	if norm == "" {
		return nil, os.ErrNotExist
	}
	sb, err := scanSongbook(s.pool.QueryRow(ctx,
		`SELECT `+songbookColumns+` FROM songbooks WHERE slug = $1`,
		norm,
	))
	if isNotFound(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("get songbook by slug: %w", err)
	}
	return sb, nil
}

// ListSongbooks returns all songbook metadata in creation order.
func (s *Store) ListSongbooks(ctx context.Context) ([]*models.Songbook, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+songbookColumns+` FROM songbooks ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list songbooks: %w", err)
	}
	return scanSongbooks(rows)
}

// ListSongbooksByOwner returns all songbooks owned by an account.
func (s *Store) ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+songbookColumns+` FROM songbooks WHERE owner_id = $1 ORDER BY created_at ASC, id ASC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list owner songbooks: %w", err)
	}
	return scanSongbooks(rows)
}

// NextCatalogGeneration atomically increments and returns the next import job generation for a songbook.
func (s *Store) NextCatalogGeneration(ctx context.Context, songbookID string) (int64, error) {
	var next int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookWriteFence(ctx, tx, songbookID); err != nil {
			return fmt.Errorf("next catalog generation: %w", err)
		}
		err := tx.QueryRow(ctx, `
			UPDATE songbooks
			SET last_import_generation = last_import_generation + 1
			WHERE id = $1 AND deleting = FALSE
			RETURNING last_import_generation`,
			songbookID,
		).Scan(&next)
		if isNotFound(err) {
			var deleting bool
			if chkErr := tx.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1`, songbookID).Scan(&deleting); chkErr == nil && deleting {
				return fmt.Errorf("next catalog generation: %w", store.ErrSongbookDeleting)
			}
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("next catalog generation: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// BeginSongbookDeletion marks a songbook as undergoing irreversible deletion.
func (s *Store) BeginSongbookDeletion(ctx context.Context, id string) (*models.Songbook, error) {
	now := s.currentTime()
	var sb *models.Songbook
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		sb, scanErr = scanSongbook(tx.QueryRow(ctx, `
			UPDATE songbooks
			SET deleting = TRUE, updated_at = $1
			WHERE id = $2
			RETURNING `+songbookColumns,
			now, id,
		))
		if isNotFound(scanErr) {
			return os.ErrNotExist
		}
		if scanErr != nil {
			return fmt.Errorf("begin songbook deletion: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sb, nil
}

// ConfirmSongbookDeletion marks a deleting songbook as confirmed after post-barrier dependency checks succeed.
func (s *Store) ConfirmSongbookDeletion(ctx context.Context, id string) error {
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var confirmedID string
		err := tx.QueryRow(ctx, `
			UPDATE songbooks
			SET deletion_confirmed = TRUE, updated_at = $1
			WHERE id = $2 AND deleting = TRUE
			RETURNING id`,
			now, id,
		).Scan(&confirmedID)
		if isNotFound(err) {
			return os.ErrNotExist
		}
		if err != nil {
			return fmt.Errorf("confirm songbook deletion: %w", err)
		}
		return nil
	})
}

// AbortSongbookDeletion clears an unconfirmed deletion barrier if new dependencies raced before confirmation.
func (s *Store) AbortSongbookDeletion(ctx context.Context, id string) error {
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE songbooks
			SET deleting = FALSE, updated_at = $1
			WHERE id = $2 AND deletion_confirmed = FALSE`,
			now, id,
		)
		if err != nil {
			return fmt.Errorf("abort songbook deletion: %w", err)
		}
		return nil
	})
}
