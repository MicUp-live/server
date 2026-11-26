// Package catalog tests songbook build execution.
package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestCompactSongbook_PublishesInitialCatalogWhenNoActiveVersion(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	books := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"songbook-1": {ID: "songbook-1"},
		},
	}
	changes := newFakeCatalogChangeStore()
	_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: "songbook-1",
		SongID:     "song-1",
		Song:       &models.Song{ID: "song-1", SongbookID: "songbook-1", Artist: "Adele", Title: "Hello", IsAvailable: true},
		UpdatedAt:  time.Now().UTC(),
	})
	runner := Runner{
		Books:   books,
		Changes: changes,
		Reader:  &fakeCatalogReader{},
		Builder: Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}},
	}
	version, err := runner.CompactSongbook(ctx, "songbook-1")
	if err != nil {
		t.Fatalf("compact initial songbook: %v", err)
	}
	if version == nil || version.SongCount != 1 || catalogs.published["songbook-1"] != version.ID {
		t.Fatalf("runner result = %+v, published = %+v", version, catalogs.published)
	}
	if len(changes.deletedWithTime) != 1 {
		t.Fatalf("expected 1 retired change, got %d", len(changes.deletedWithTime))
	}
}

func TestBuildManifestCatalog_PruneMissingFalse_MergesExistingCatalog(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	// First, build a prior catalog version with Queen
	priorVersion, err := builder.BuildAndPublish(ctx, "book-1", []*models.Song{
		{Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("setup prior catalog: %v", err)
	}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: priorVersion.ID},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			priorVersion.AssetPrefix: {
				{ID: "trk_queen", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
			},
		},
	}

	runner := Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
	}

	// Incoming manifest has The Beatles
	incoming := []*models.Song{
		{Artist: "The Beatles", Title: "Yesterday", Disc: "SC02", IsAvailable: true},
	}

	// Build with pruneMissing = false
	newVersion, err := runner.BuildManifestCatalog(ctx, "book-1", incoming, false)
	if err != nil {
		t.Fatalf("BuildManifestCatalog failed: %v", err)
	}

	if newVersion.SongCount != 2 {
		t.Fatalf("expected 2 songs in merged catalog, got %d", newVersion.SongCount)
	}
}

func TestBuildManifestCatalog_PruneMissingTrue_ReplacesCatalog(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	priorVersion, err := builder.BuildAndPublish(ctx, "book-1", []*models.Song{
		{Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("setup prior catalog: %v", err)
	}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: priorVersion.ID},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			priorVersion.AssetPrefix: {
				{ID: "trk_queen", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
			},
		},
	}

	runner := Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
	}

	incoming := []*models.Song{
		{Artist: "The Beatles", Title: "Yesterday", Disc: "SC02", IsAvailable: true},
	}

	// Build with pruneMissing = true (replacement)
	newVersion, err := runner.BuildManifestCatalog(ctx, "book-1", incoming, true)
	if err != nil {
		t.Fatalf("BuildManifestCatalog failed: %v", err)
	}

	if newVersion.SongCount != 1 {
		t.Fatalf("expected 1 song in replaced catalog, got %d", newVersion.SongCount)
	}
}

func TestBuildManifestCatalog_PruneMissingFalse_NoPriorCatalog(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: ""},
		},
	}
	reader := &fakeCatalogReader{}

	runner := Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
	}

	incoming := []*models.Song{
		{Artist: "ABBA", Title: "Dancing Queen", IsAvailable: true},
	}

	newVersion, err := runner.BuildManifestCatalog(ctx, "book-1", incoming, false)
	if err != nil {
		t.Fatalf("BuildManifestCatalog failed: %v", err)
	}

	if newVersion.SongCount != 1 {
		t.Fatalf("expected 1 song, got %d", newVersion.SongCount)
	}
}

func TestBuildManifestCatalog_PruneMissingFalse_ReaderErrorFails(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: "cat-1"},
		},
	}
	catalogs.versions["cat-1"] = &models.CatalogVersion{ID: "cat-1", Status: models.CatalogVersionReady, AssetPrefix: "catalogs/cat-1"}
	reader := &fakeCatalogReader{err: errors.New("s3 download failed")}

	runner := Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
	}

	incoming := []*models.Song{
		{Artist: "ABBA", Title: "Dancing Queen", IsAvailable: true},
	}

	_, err := runner.BuildManifestCatalog(ctx, "book-1", incoming, false)
	if err == nil {
		t.Fatal("expected error when reader fails, got nil")
	}
}

type fakeSongbookStore struct {
	store.SongbookStore
	books map[string]*models.Songbook
}

func (f *fakeSongbookStore) CreateSongbook(_ context.Context, sb *models.Songbook) error { return nil }
func (f *fakeSongbookStore) UpdateSongbook(_ context.Context, sb *models.Songbook) error { return nil }
func (f *fakeSongbookStore) DeleteSongbook(_ context.Context, id string) error           { return nil }
func (f *fakeSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	if b, ok := f.books[id]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeSongbookStore) GetPublicSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) GetSongbookBySlug(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) ListSongbooksByOwner(_ context.Context, _ string) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}
func (f *fakeSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	if b, ok := f.books[id]; ok {
		b.Deleting = true
		copy := *b
		return &copy, nil
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type fakeCatalogReader struct {
	songsByPrefix map[string][]*models.Song
	err           error
}

func (f *fakeCatalogReader) ListSongs(_ context.Context, assetPrefix, _ string) ([]*models.Song, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.songsByPrefix[assetPrefix], nil
}

type fakeCatalogChangeStore struct {
	changes         map[string][]*models.CatalogChange
	deletedWithTime map[string]time.Time
}

func newFakeCatalogChangeStore() *fakeCatalogChangeStore {
	return &fakeCatalogChangeStore{
		changes:         make(map[string][]*models.CatalogChange),
		deletedWithTime: make(map[string]time.Time),
	}
}

func (f *fakeCatalogChangeStore) PutCatalogChange(_ context.Context, change *models.CatalogChange) error {
	f.changes[change.SongbookID] = append(f.changes[change.SongbookID], change)
	return nil
}

func (f *fakeCatalogChangeStore) ListCatalogChanges(_ context.Context, songbookID string) ([]*models.CatalogChange, error) {
	return f.changes[songbookID], nil
}

func (f *fakeCatalogChangeStore) DeleteCatalogChange(_ context.Context, songbookID, songID string, updatedAt time.Time) error {
	f.deletedWithTime[songID] = updatedAt
	current := f.changes[songbookID]
	var remaining []*models.CatalogChange
	for _, c := range current {
		if c.SongID == songID {
			// Conditional delete: if a newer update was written, preserve it
			if !updatedAt.IsZero() && c.UpdatedAt.After(updatedAt) {
				remaining = append(remaining, c)
				continue
			}
		} else {
			remaining = append(remaining, c)
		}
	}
	f.changes[songbookID] = remaining
	return nil
}

func TestCompactSongbook_AppliesChangesAndConditionalDeletes(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	// Prior catalog version
	priorVersion, err := builder.BuildAndPublish(ctx, "sb-compact", []*models.Song{
		{ID: "song-1", Artist: "Original Artist", Title: "Original Title", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("setup prior catalog: %v", err)
	}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"sb-compact": {ID: "sb-compact", CatalogVersionID: priorVersion.ID},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			priorVersion.AssetPrefix: {
				{ID: "song-1", Artist: "Original Artist", Title: "Original Title", IsAvailable: true},
			},
		},
	}

	changeStore := newFakeCatalogChangeStore()
	t1 := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	changeStore.changes["sb-compact"] = []*models.CatalogChange{
		{
			SongbookID: "sb-compact",
			SongID:     "song-1",
			Song:       &models.Song{ID: "song-1", Artist: "Updated Artist", Title: "Updated Title", IsAvailable: true},
			UpdatedAt:  t1,
		},
	}

	runner := Runner{
		Books:   bookStore,
		Reader:  reader,
		Changes: changeStore,
		Builder: builder,
	}

	// 1. Run compaction
	newVersion, err := runner.CompactSongbook(ctx, "sb-compact")
	if err != nil {
		t.Fatalf("compact songbook: %v", err)
	}
	if newVersion == nil || newVersion.ID == priorVersion.ID {
		t.Fatalf("expected new catalog version after compaction, got %v", newVersion)
	}

	// Verify change was deleted with snapshot timestamp t1
	if deletedAt, ok := changeStore.deletedWithTime["song-1"]; !ok || !deletedAt.Equal(t1) {
		t.Fatalf("expected DeleteCatalogChange to be called with timestamp %v, got %v", t1, deletedAt)
	}
	if len(changeStore.changes["sb-compact"]) != 0 {
		t.Fatalf("expected 0 changes remaining, got %d", len(changeStore.changes["sb-compact"]))
	}

	// 2. Test race condition: newer write during compaction survives
	tSnapshot := time.Date(2026, 9, 13, 11, 5, 0, 0, time.UTC)
	tNewer := time.Date(2026, 9, 13, 11, 5, 10, 0, time.UTC)

	changeStore.changes["sb-compact"] = []*models.CatalogChange{
		{
			SongbookID: "sb-compact",
			SongID:     "song-2",
			Song:       &models.Song{ID: "song-2", Title: "Snapshot Edit"},
			UpdatedAt:  tSnapshot,
		},
	}

	// Simulate concurrent write arriving during compaction
	changeStore.changes["sb-compact"][0].Song.Title = "Concurrent Newer Edit"
	changeStore.changes["sb-compact"][0].UpdatedAt = tNewer

	// Attempt conditional delete using the older snapshot timestamp
	if err := changeStore.DeleteCatalogChange(ctx, "sb-compact", "song-2", tSnapshot); err != nil {
		t.Fatalf("delete catalog change: %v", err)
	}

	// Newer edit MUST still remain in the change store
	if len(changeStore.changes["sb-compact"]) != 1 {
		t.Fatalf("expected newer change to survive conditional delete, got %d", len(changeStore.changes["sb-compact"]))
	}
	if changeStore.changes["sb-compact"][0].Song.Title != "Concurrent Newer Edit" {
		t.Fatalf("expected concurrent edit to be preserved, got %s", changeStore.changes["sb-compact"][0].Song.Title)
	}
}

func TestCompactSongbook_StaleBaseVersionRetriesAndSucceeds(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	priorVersion, err := builder.BuildAndPublish(ctx, "sb-retry", []*models.Song{
		{Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("build prior version: %v", err)
	}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"sb-retry": {ID: "sb-retry", CatalogVersionID: priorVersion.ID},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			priorVersion.AssetPrefix: {
				{ID: "song-1", Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true},
			},
		},
	}
	changeStore := newFakeCatalogChangeStore()
	changeStore.changes["sb-retry"] = []*models.CatalogChange{
		{
			SongbookID: "sb-retry",
			SongID:     "song-2",
			Song:       &models.Song{ID: "song-2", Artist: "Queen", Title: "Don't Stop Me Now", IsAvailable: true},
			UpdatedAt:  time.Now().UTC(),
		},
	}

	publishAttempts := 0
	catalogs.publishErrFunc = func(songbookID, catalogID string, opts ...store.PublicationOptions) error {
		publishAttempts++
		if publishAttempts == 1 {
			// First attempt fails with ErrStalePublication
			return store.ErrStalePublication
		}
		// Subsequent attempts succeed
		return nil
	}

	runner := Runner{
		Books:   bookStore,
		Changes: changeStore,
		Builder: builder,
		Reader:  reader,
	}

	result, err := runner.CompactSongbook(ctx, "sb-retry")
	if err != nil {
		t.Fatalf("compact songbook should have succeeded on retry, got err: %v", err)
	}
	if result == nil {
		t.Fatal("expected result version, got nil")
	}
	if publishAttempts != 2 {
		t.Fatalf("expected 2 publish attempts, got %d", publishAttempts)
	}
	// Overrides deleted after successful retry
	if len(changeStore.changes["sb-retry"]) != 0 {
		t.Fatalf("expected overrides deleted after successful compaction, got %d", len(changeStore.changes["sb-retry"]))
	}
}

func TestBuildManifestCatalog_SupersededImportRejected(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"sb-stale": {
				ID:                "sb-stale",
				CatalogGeneration: 5, // Songbook is already at generation 5
			},
		},
	}
	reader := &fakeCatalogReader{}

	runner := Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
	}

	incoming := []*models.Song{
		{Artist: "Queen", Title: "Somebody to Love", IsAvailable: true},
	}

	// Incoming import has older generation 2
	_, err := runner.BuildManifestCatalog(ctx, "sb-stale", incoming, false, store.PublicationOptions{
		JobGeneration: 2,
	})
	if err == nil {
		t.Fatal("expected superseded import to be rejected, but it succeeded")
	}
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrStalePublication, got: %v", err)
	}
}

func TestCompactSongbook_NoActiveVersionAndNoChanges_ReturnsNilWithoutPublish(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	books := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"sb-empty": {ID: "sb-empty"},
		},
	}
	runner := Runner{
		Books:   books,
		Changes: newFakeCatalogChangeStore(),
		Reader:  &fakeCatalogReader{},
		Builder: Builder{Catalogs: catalogs, Assets: &fakeAssetStore{}, Options: Options{SchemaVersion: 1}},
	}
	version, err := runner.CompactSongbook(ctx, "sb-empty")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if version != nil {
		t.Fatalf("expected nil version when no active catalog and no changes, got %+v", version)
	}
	if len(catalogs.published) != 0 {
		t.Fatalf("expected no catalog publication, got %+v", catalogs.published)
	}
}

func TestCompactSongbook_InitialPublishConflictRetriesWithNewBase(t *testing.T) {
	ctx := context.Background()
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1}}

	concurrentVersion, err := builder.BuildAndPublish(ctx, "sb-init-race", []*models.Song{
		{ID: "s-base", SongbookID: "sb-init-race", Artist: "Base", Title: "Track", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("setup concurrent version: %v", err)
	}

	books := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"sb-init-race": {ID: "sb-init-race"}, // Initially no active catalog
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			concurrentVersion.AssetPrefix: {
				{ID: "s-base", SongbookID: "sb-init-race", Artist: "Base", Title: "Track", IsAvailable: true},
			},
		},
	}
	changes := newFakeCatalogChangeStore()
	_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: "sb-init-race",
		SongID:     "s-delta",
		Song:       &models.Song{ID: "s-delta", SongbookID: "sb-init-race", Artist: "Delta", Title: "Track", IsAvailable: true},
		UpdatedAt:  time.Now().UTC(),
	})

	attempts := 0
	catalogs.publishErrFunc = func(songbookID, catalogID string, opts ...store.PublicationOptions) error {
		attempts++
		if attempts == 1 {
			// Concurrent import published concurrentVersion while we started from empty base
			books.books["sb-init-race"].CatalogVersionID = concurrentVersion.ID
			return store.ErrPublicationBaseConflict
		}
		return nil
	}

	runner := Runner{Books: books, Changes: changes, Reader: reader, Builder: builder}
	result, err := runner.CompactSongbook(ctx, "sb-init-race")
	if err != nil {
		t.Fatalf("CompactSongbook retry failed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 publish attempts, got %d", attempts)
	}
	if result == nil || result.SongCount != 2 {
		t.Fatalf("expected merged 2-song catalog after retry, got %+v", result)
	}
}
