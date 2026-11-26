// Package catalog runs a catalog build for one existing songbook.
package catalog

import (
	"context"
	"errors"
	"fmt"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// Runner loads a songbook snapshot only when an asynchronous worker executes.
type Runner struct {
	Builder Builder
	Books   store.SongbookStore
	Changes store.CatalogChangeStore
	Imports store.ImportJobStore
	Reader  interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
}

const (
	maxCompactionRetries = 3
	maxImportRetries     = 3
)

// CompactSongbook folds pending DynamoDB deltas into the active S3 catalog,
// or publishes an initial S3 catalog when no active version exists yet.
func (r Runner) CompactSongbook(ctx context.Context, songbookID string) (*models.CatalogVersion, error) {
	if r.Books == nil || r.Changes == nil || r.Reader == nil {
		return nil, fmt.Errorf("compact songbook: books, changes, and reader are required")
	}

	for attempt := 0; attempt < maxCompactionRetries; attempt++ {
		book, err := r.Books.GetSongbook(ctx, songbookID)
		if err != nil {
			return nil, err
		}
		var (
			version *models.CatalogVersion
			songs   []*models.Song
		)
		if book.CatalogVersionID != "" {
			version, err = r.Builder.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
			if err != nil {
				return nil, err
			}
			songs, err = r.Reader.ListSongs(ctx, version.AssetPrefix, songbookID)
			if err != nil {
				return nil, err
			}
		}
		changes, err := r.Changes.ListCatalogChanges(ctx, songbookID)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			// All pending changes were already incorporated
			return version, nil
		}
		byID := models.ApplyCatalogChanges(songs, changes)
		merged := make([]*models.Song, 0, len(byID))
		for _, song := range byID {
			merged = append(merged, song)
		}

		baseVersion := book.CatalogVersionID
		pubOpts := store.PublicationOptions{
			ExpectedBaseVersion: &baseVersion,
		}

		result, err := r.Builder.BuildAndPublish(ctx, songbookID, merged, pubOpts)
		if err != nil {
			if errors.Is(err, store.ErrPublicationSuperseded) {
				return nil, err
			}
			if errors.Is(err, store.ErrPublicationBaseConflict) || errors.Is(err, store.ErrStalePublication) {
				// Concurrently modified: active catalog changed during build. Reload and retry.
				continue
			}
			return nil, err
		}

		// Only retire overrides after successful publication
		for _, change := range changes {
			if err := r.Changes.DeleteCatalogChange(ctx, change.SongbookID, change.SongID, change.UpdatedAt); err != nil {
				return nil, err
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("compact songbook %s: %w: %w", songbookID, store.ErrPublicationBaseConflict, store.ErrStalePublication)
}

// BuildManifestCatalog compiles and publishes a catalog from manifest songs,
// merging with the active catalog version when pruneMissing is false.
func (r Runner) BuildManifestCatalog(ctx context.Context, songbookID string, manifestSongs []*models.Song, pruneMissing bool, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	var jobGen int64
	if len(opts) > 0 {
		jobGen = opts[0].JobGeneration
	}

	if pruneMissing {
		if r.Books != nil {
			book, err := r.Books.GetSongbook(ctx, songbookID)
			if err != nil {
				return nil, fmt.Errorf("load songbook for catalog replace: %w", err)
			}
			if jobGen > 0 && book.CatalogGeneration > jobGen {
				return nil, fmt.Errorf("import for songbook %s superseded (songbook generation %d > job generation %d): %w: %w",
					songbookID, book.CatalogGeneration, jobGen, store.ErrPublicationSuperseded, store.ErrStalePublication)
			}
		}
		pubOpts := store.PublicationOptions{
			JobGeneration: jobGen,
		}
		if len(opts) > 0 {
			pubOpts.JobID = opts[0].JobID
			pubOpts.ProcessedSongs = opts[0].ProcessedSongs
		}
		result, err := r.Builder.BuildAndPublish(ctx, songbookID, manifestSongs, pubOpts)
		if err != nil {
			if errors.Is(err, store.ErrPublicationSuperseded) || errors.Is(err, store.ErrImportJobAlreadyCompleted) || errors.Is(err, store.ErrImportJobTerminal) {
				return nil, err
			}
			if r.Books != nil && errors.Is(err, store.ErrStalePublication) {
				if currentBook, getErr := r.Books.GetSongbook(ctx, songbookID); getErr == nil {
					if jobGen > 0 && currentBook.CatalogGeneration > jobGen {
						return nil, fmt.Errorf("import for songbook %s superseded (songbook generation %d > job generation %d): %w: %w",
							songbookID, currentBook.CatalogGeneration, jobGen, store.ErrPublicationSuperseded, store.ErrStalePublication)
					}
				}
			}
			return nil, err
		}
		return result, nil
	}

	for attempt := 0; attempt < maxImportRetries; attempt++ {
		var existing []*models.Song
		var baseVersion string

		if r.Books != nil && r.Reader != nil && r.Builder.Catalogs != nil {
			book, err := r.Books.GetSongbook(ctx, songbookID)
			if err != nil {
				return nil, fmt.Errorf("load songbook for catalog merge: %w", err)
			}
			if jobGen > 0 && book.CatalogGeneration > jobGen {
				return nil, fmt.Errorf("import for songbook %s superseded (songbook generation %d > job generation %d): %w: %w",
					songbookID, book.CatalogGeneration, jobGen, store.ErrPublicationSuperseded, store.ErrStalePublication)
			}
			if book.CatalogVersionID != "" {
				baseVersion = book.CatalogVersionID
				version, err := r.Builder.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
				if err != nil {
					return nil, fmt.Errorf("load catalog version for merge: %w", err)
				}
				existingSongs, err := r.Reader.ListSongs(ctx, version.AssetPrefix, songbookID)
				if err != nil {
					return nil, fmt.Errorf("load existing songs for catalog merge: %w", err)
				}
				existing = existingSongs
			}
		}

		songs := manifestSongs
		if len(existing) > 0 {
			songs = MergeSongs(existing, manifestSongs)
		}

		pubOpts := store.PublicationOptions{
			ExpectedBaseVersion: &baseVersion,
			JobGeneration:       jobGen,
		}
		if len(opts) > 0 {
			pubOpts.JobID = opts[0].JobID
			pubOpts.ProcessedSongs = opts[0].ProcessedSongs
		}

		result, err := r.Builder.BuildAndPublish(ctx, songbookID, songs, pubOpts)
		if err != nil {
			if errors.Is(err, store.ErrPublicationSuperseded) || errors.Is(err, store.ErrImportJobAlreadyCompleted) || errors.Is(err, store.ErrImportJobTerminal) {
				return nil, err
			}
			if errors.Is(err, store.ErrPublicationBaseConflict) || errors.Is(err, store.ErrStalePublication) {
				if r.Books != nil {
					if currentBook, getErr := r.Books.GetSongbook(ctx, songbookID); getErr == nil {
						if jobGen > 0 && currentBook.CatalogGeneration > jobGen {
							return nil, fmt.Errorf("import for songbook %s superseded (songbook generation %d > job generation %d): %w: %w",
								songbookID, currentBook.CatalogGeneration, jobGen, store.ErrPublicationSuperseded, store.ErrStalePublication)
						}
					}
				}
				continue
			}
			return nil, err
		}
		return result, nil
	}

	return nil, fmt.Errorf("import merge for songbook %s: %w: %w", songbookID, store.ErrPublicationBaseConflict, store.ErrStalePublication)
}
