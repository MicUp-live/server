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
		result, retry, err := r.compactAttempt(ctx, songbookID)
		if retry {
			continue
		}
		return result, err
	}

	return nil, fmt.Errorf("compact songbook %s: %w: %w", songbookID, store.ErrPublicationBaseConflict, store.ErrStalePublication)
}

func (r Runner) compactAttempt(ctx context.Context, songbookID string) (*models.CatalogVersion, bool, error) {
	baseVersion, version, songs, err := r.loadActiveCatalog(ctx, songbookID)
	if err != nil {
		return nil, false, err
	}
	changes, err := r.Changes.ListCatalogChanges(ctx, songbookID)
	if err != nil {
		return nil, false, err
	}
	if len(changes) == 0 {
		// All pending changes were already incorporated
		return version, false, nil
	}

	merged := mergeCatalogChanges(songs, changes)
	pubOpts := store.PublicationOptions{
		ExpectedBaseVersion: &baseVersion,
	}

	result, err := r.Builder.BuildAndPublish(ctx, songbookID, merged, pubOpts)
	if err != nil {
		if errors.Is(err, store.ErrPublicationSuperseded) {
			return nil, false, err
		}
		if errors.Is(err, store.ErrPublicationBaseConflict) || errors.Is(err, store.ErrStalePublication) {
			// Concurrently modified: active catalog changed during build. Reload and retry.
			return nil, true, nil
		}
		return nil, false, err
	}

	// Only retire overrides after successful publication
	if err := r.retireCatalogChanges(ctx, changes); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (r Runner) loadActiveCatalog(ctx context.Context, songbookID string) (string, *models.CatalogVersion, []*models.Song, error) {
	book, err := r.Books.GetSongbook(ctx, songbookID)
	if err != nil {
		return "", nil, nil, err
	}
	if book.CatalogVersionID == "" {
		return "", nil, nil, nil
	}
	version, err := r.Builder.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
	if err != nil {
		return "", nil, nil, err
	}
	songs, err := r.Reader.ListSongs(ctx, version.AssetPrefix, songbookID)
	if err != nil {
		return "", nil, nil, err
	}
	return book.CatalogVersionID, version, songs, nil
}

func mergeCatalogChanges(songs []*models.Song, changes []*models.CatalogChange) []*models.Song {
	byID := models.ApplyCatalogChanges(songs, changes)
	merged := make([]*models.Song, 0, len(byID))
	for _, song := range byID {
		merged = append(merged, song)
	}
	return merged
}

func (r Runner) retireCatalogChanges(ctx context.Context, changes []*models.CatalogChange) error {
	for _, change := range changes {
		if err := r.Changes.DeleteCatalogChange(ctx, change.SongbookID, change.SongID, change.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

// BuildManifestCatalog compiles and publishes a catalog from manifest songs,
// merging with the active catalog version when pruneMissing is false.
func (r Runner) BuildManifestCatalog(ctx context.Context, songbookID string, manifestSongs []*models.Song, pruneMissing bool, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	if pruneMissing {
		return r.buildReplaceCatalog(ctx, songbookID, manifestSongs, opts)
	}
	return r.buildMergeCatalog(ctx, songbookID, manifestSongs, opts)
}

func (r Runner) buildReplaceCatalog(ctx context.Context, songbookID string, manifestSongs []*models.Song, opts []store.PublicationOptions) (*models.CatalogVersion, error) {
	pubOpts := buildPublicationOptions(opts, nil)
	if r.Books != nil {
		book, err := r.Books.GetSongbook(ctx, songbookID)
		if err != nil {
			return nil, fmt.Errorf("load songbook for catalog replace: %w", err)
		}
		if err := checkJobSuperseded(songbookID, book, pubOpts.JobGeneration); err != nil {
			return nil, err
		}
	}

	result, err := r.Builder.BuildAndPublish(ctx, songbookID, manifestSongs, pubOpts)
	if err == nil {
		return result, nil
	}
	if isTerminalPublishError(err) {
		return nil, err
	}
	if errors.Is(err, store.ErrStalePublication) {
		if supErr := r.checkCurrentBookSuperseded(ctx, songbookID, pubOpts.JobGeneration); supErr != nil {
			return nil, supErr
		}
	}
	return nil, err
}

func (r Runner) buildMergeCatalog(ctx context.Context, songbookID string, manifestSongs []*models.Song, opts []store.PublicationOptions) (*models.CatalogVersion, error) {
	for attempt := 0; attempt < maxImportRetries; attempt++ {
		result, retry, err := r.buildMergeAttempt(ctx, songbookID, manifestSongs, opts)
		if retry {
			continue
		}
		return result, err
	}

	return nil, fmt.Errorf("import merge for songbook %s: %w: %w", songbookID, store.ErrPublicationBaseConflict, store.ErrStalePublication)
}

func (r Runner) buildMergeAttempt(ctx context.Context, songbookID string, manifestSongs []*models.Song, opts []store.PublicationOptions) (*models.CatalogVersion, bool, error) {
	baseOpts := buildPublicationOptions(opts, nil)
	baseVersion, existing, err := r.loadSongsForMerge(ctx, songbookID, baseOpts.JobGeneration)
	if err != nil {
		return nil, false, err
	}

	songs := manifestSongs
	if len(existing) > 0 {
		songs = MergeSongs(existing, manifestSongs)
	}

	pubOpts := buildPublicationOptions(opts, &baseVersion)
	result, err := r.Builder.BuildAndPublish(ctx, songbookID, songs, pubOpts)
	if err == nil {
		return result, false, nil
	}
	if isTerminalPublishError(err) {
		return nil, false, err
	}
	if errors.Is(err, store.ErrPublicationBaseConflict) || errors.Is(err, store.ErrStalePublication) {
		if supErr := r.checkCurrentBookSuperseded(ctx, songbookID, pubOpts.JobGeneration); supErr != nil {
			return nil, false, supErr
		}
		return nil, true, nil
	}
	return nil, false, err
}

func (r Runner) loadSongsForMerge(ctx context.Context, songbookID string, jobGen int64) (string, []*models.Song, error) {
	if r.Books == nil || r.Reader == nil || r.Builder.Catalogs == nil {
		return "", nil, nil
	}
	book, err := r.Books.GetSongbook(ctx, songbookID)
	if err != nil {
		return "", nil, fmt.Errorf("load songbook for catalog merge: %w", err)
	}
	if err := checkJobSuperseded(songbookID, book, jobGen); err != nil {
		return "", nil, err
	}
	if book.CatalogVersionID == "" {
		return "", nil, nil
	}
	version, err := r.Builder.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
	if err != nil {
		return "", nil, fmt.Errorf("load catalog version for merge: %w", err)
	}
	existingSongs, err := r.Reader.ListSongs(ctx, version.AssetPrefix, songbookID)
	if err != nil {
		return "", nil, fmt.Errorf("load existing songs for catalog merge: %w", err)
	}
	return book.CatalogVersionID, existingSongs, nil
}

func buildPublicationOptions(opts []store.PublicationOptions, baseVersion *string) store.PublicationOptions {
	pubOpts := store.PublicationOptions{
		ExpectedBaseVersion: baseVersion,
	}
	if len(opts) > 0 {
		pubOpts.JobGeneration = opts[0].JobGeneration
		pubOpts.JobID = opts[0].JobID
		pubOpts.ProcessedSongs = opts[0].ProcessedSongs
	}
	return pubOpts
}

func checkJobSuperseded(songbookID string, book *models.Songbook, jobGen int64) error {
	if jobGen > 0 && book.CatalogGeneration > jobGen {
		return fmt.Errorf("import for songbook %s superseded (songbook generation %d > job generation %d): %w: %w",
			songbookID, book.CatalogGeneration, jobGen, store.ErrPublicationSuperseded, store.ErrStalePublication)
	}
	return nil
}

func (r Runner) checkCurrentBookSuperseded(ctx context.Context, songbookID string, jobGen int64) error {
	if r.Books == nil {
		return nil
	}
	currentBook, err := r.Books.GetSongbook(ctx, songbookID)
	if err != nil {
		return nil
	}
	return checkJobSuperseded(songbookID, currentBook, jobGen)
}

func isTerminalPublishError(err error) bool {
	return errors.Is(err, store.ErrPublicationSuperseded) ||
		errors.Is(err, store.ErrImportJobAlreadyCompleted) ||
		errors.Is(err, store.ErrImportJobTerminal)
}
