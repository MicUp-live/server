package postgres

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const grantColumns = `songbook_id, user_id, can_edit_catalog, can_host_shows, COALESCE(granted_by, ''), created_at, updated_at`

type grantScanner interface {
	Scan(dest ...any) error
}

func scanSongbookGrant(row grantScanner) (*models.SongbookGrant, error) {
	var g models.SongbookGrant
	if err := row.Scan(
		&g.SongbookID,
		&g.UserID,
		&g.CanEditCatalog,
		&g.CanHostShows,
		&g.GrantedBy,
		&g.CreatedAt,
		&g.UpdatedAt,
	); err != nil {
		return nil, err
	}
	g.CreatedAt = g.CreatedAt.UTC()
	g.UpdatedAt = g.UpdatedAt.UTC()
	return &g, nil
}

// GetSongbookGrant returns an accepted collaborator grant.
func (s *Store) GetSongbookGrant(ctx context.Context, songbookID, userID string) (*models.SongbookGrant, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+grantColumns+`
		FROM songbook_grants
		WHERE songbook_id = $1 AND user_id = $2`,
		songbookID, userID,
	)
	g, err := scanSongbookGrant(row)
	if err != nil {
		if isNotFound(err) {
			return nil, store.ErrGrantNotFound
		}
		return nil, fmt.Errorf("get songbook grant: %w", err)
	}
	return g, nil
}

// ListSongbookGrantsBySongbook lists all accepted grants for a songbook ordered by creation time.
func (s *Store) ListSongbookGrantsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookGrant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+grantColumns+`
		FROM songbook_grants
		WHERE songbook_id = $1
		ORDER BY created_at ASC`,
		songbookID,
	)
	if err != nil {
		return nil, fmt.Errorf("list songbook grants: %w", err)
	}
	defer rows.Close()

	var grants []*models.SongbookGrant
	for rows.Next() {
		g, err := scanSongbookGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan songbook grant: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate songbook grants: %w", err)
	}
	return grants, nil
}

// ListSongbookGrantsByUser lists all accepted grants for a user ordered by creation time.
func (s *Store) ListSongbookGrantsByUser(ctx context.Context, userID string) ([]*models.SongbookGrant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+grantColumns+`
		FROM songbook_grants
		WHERE user_id = $1
		ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user songbook grants: %w", err)
	}
	defer rows.Close()

	var grants []*models.SongbookGrant
	for rows.Next() {
		g, err := scanSongbookGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user songbook grant: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user songbook grants: %w", err)
	}
	return grants, nil
}

// UpdateSongbookGrant updates permissions on an existing collaborator grant while the songbook is not deleting.
func (s *Store) UpdateSongbookGrant(ctx context.Context, grant *models.SongbookGrant) error {
	now := s.currentTime()
	grant.UpdatedAt = now

	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE songbook_grants AS g
			SET can_edit_catalog = $1, can_host_shows = $2, updated_at = $3
			FROM songbooks AS sb
			WHERE g.songbook_id = $4
			  AND g.user_id = $5
			  AND sb.id = g.songbook_id
			  AND sb.deleting = FALSE`,
			grant.CanEditCatalog, grant.CanHostShows, now, grant.SongbookID, grant.UserID,
		)
		if err != nil {
			return fmt.Errorf("update songbook grant: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}

		var deleting bool
		if err := tx.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1`, grant.SongbookID).Scan(&deleting); err != nil {
			if isNotFound(err) {
				return os.ErrNotExist
			}
			return fmt.Errorf("check songbook state for grant update: %w", err)
		}
		if deleting {
			return store.ErrSongbookDeleting
		}

		var exists int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM songbook_grants WHERE songbook_id = $1 AND user_id = $2`, grant.SongbookID, grant.UserID).Scan(&exists); err != nil {
			if isNotFound(err) {
				return store.ErrGrantNotFound
			}
			return fmt.Errorf("check songbook grant existence: %w", err)
		}
		return os.ErrNotExist
	})
}

// DeleteSongbookGrant revokes a collaborator's grant on a songbook.
func (s *Store) DeleteSongbookGrant(ctx context.Context, songbookID, userID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM songbook_grants
			WHERE songbook_id = $1 AND user_id = $2`,
			songbookID, userID,
		)
		if err != nil {
			return fmt.Errorf("delete songbook grant: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return store.ErrGrantNotFound
		}
		return nil
	})
}
