package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// PutCatalogChange stores or replaces one pending songbook delta.
func (s *Store) PutCatalogChange(ctx context.Context, change *models.CatalogChange) error {
	if change == nil || strings.TrimSpace(change.SongbookID) == "" || strings.TrimSpace(change.SongID) == "" {
		return fmt.Errorf("put catalog change: songbook and song are required")
	}
	if change.UpdatedAt.IsZero() {
		change.UpdatedAt = s.currentTime()
	} else {
		change.UpdatedAt = change.UpdatedAt.UTC()
	}
	change.UpdatedAt = change.UpdatedAt.Truncate(time.Microsecond)

	var songJSON []byte
	if change.Song != nil {
		var err error
		songJSON, err = marshalJSON(change.Song)
		if err != nil {
			return fmt.Errorf("put catalog change marshal song: %w", err)
		}
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		if err := checkSongbookWriteFence(ctx, tx, change.SongbookID); err != nil {
			return fmt.Errorf("put catalog change: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO catalog_changes (songbook_id, song_id, song, deleted, clear, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (songbook_id, song_id) DO UPDATE
			SET song = EXCLUDED.song,
			    deleted = EXCLUDED.deleted,
			    clear = EXCLUDED.clear,
			    updated_at = EXCLUDED.updated_at`,
			change.SongbookID,
			change.SongID,
			songJSON,
			change.Deleted,
			change.Clear,
			change.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("put catalog change: %w", err)
		}
		return nil
	})
}

// ListCatalogChanges lists pending deltas for one songbook.
func (s *Store) ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT songbook_id, song_id, song, deleted, clear, updated_at
		FROM catalog_changes
		WHERE songbook_id = $1
		ORDER BY updated_at ASC, song_id ASC`,
		songbookID,
	)
	if err != nil {
		return nil, fmt.Errorf("list catalog changes: %w", err)
	}
	defer rows.Close()

	var changes []*models.CatalogChange
	for rows.Next() {
		var (
			ch       models.CatalogChange
			songJSON []byte
		)
		if err := rows.Scan(&ch.SongbookID, &ch.SongID, &songJSON, &ch.Deleted, &ch.Clear, &ch.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan catalog change: %w", err)
		}
		ch.UpdatedAt = ch.UpdatedAt.UTC()
		if len(songJSON) > 0 {
			var song models.Song
			if err := unmarshalJSON(songJSON, &song); err != nil {
				return nil, fmt.Errorf("unmarshal catalog change song: %w", err)
			}
			ch.Song = &song
		}
		changes = append(changes, &ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog changes: %w", err)
	}
	return changes, nil
}

// DeleteCatalogChange removes a pending delta after compaction or deletion cleanup.
// If updatedAt is non-zero, it preserves newer changes written during compaction.
func (s *Store) DeleteCatalogChange(ctx context.Context, songbookID, songID string, updatedAt time.Time) error {
	deleteFn := func(execFn func(context.Context, string, ...any) error) error {
		if !updatedAt.IsZero() {
			ts := updatedAt.UTC().Truncate(time.Microsecond)
			return execFn(ctx, `
				DELETE FROM catalog_changes
				WHERE songbook_id = $1 AND song_id = $2 AND updated_at = $3`,
				songbookID, songID, ts,
			)
		}
		return execFn(ctx, `
			DELETE FROM catalog_changes
			WHERE songbook_id = $1 AND song_id = $2`,
			songbookID, songID,
		)
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		if store.SongbookEditActorFromContext(ctx) != "" {
			if err := checkSongbookWriteFence(ctx, tx, songbookID); err != nil {
				return fmt.Errorf("delete catalog change: %w", err)
			}
		}
		if err := deleteFn(func(c context.Context, sql string, args ...any) error {
			_, err := tx.Exec(c, sql, args...)
			return err
		}); err != nil {
			return fmt.Errorf("delete catalog change: %w", err)
		}
		return nil
	})
}
