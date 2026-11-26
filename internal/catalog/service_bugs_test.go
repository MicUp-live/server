package catalog

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type stubGrantAccessStore struct {
	store.SongbookAccessStore
	grant *models.SongbookGrant
	err   error
}

func (s *stubGrantAccessStore) GetSongbookGrant(context.Context, string, string) (*models.SongbookGrant, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.grant, nil
}

type countingCatalogReader struct {
	songs []*models.Song
	calls int
}

func (c *countingCatalogReader) ListSongs(_ context.Context, _, _ string) ([]*models.Song, error) {
	c.calls++
	return c.songs, nil
}

type countingBatchChangeStore struct {
	changes   []*models.CatalogChange
	listCalls int
	putCalls  []*models.CatalogChange
}

func (c *countingBatchChangeStore) PutCatalogChange(_ context.Context, ch *models.CatalogChange) error {
	c.putCalls = append(c.putCalls, ch)
	return nil
}
func (c *countingBatchChangeStore) ListCatalogChanges(_ context.Context, _ string) ([]*models.CatalogChange, error) {
	c.listCalls++
	return c.changes, nil
}
func (c *countingBatchChangeStore) DeleteCatalogChange(context.Context, string, string, time.Time) error {
	return nil
}

func TestGetManagedManifest_PropagatesAccessStorageError(t *testing.T) {
	ctx := context.Background()
	dbErr := errors.New("dynamodb throughput exceeded")
	books := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			"sb-1": {ID: "sb-1", OwnerID: "owner-1"},
		},
	}

	svc := NewService(books, nil, nil, nil, nil, nil, nil, nil, nil).
		WithAccess(&stubGrantAccessStore{err: dbErr})

	if _, err := svc.GetManagedManifest(ctx, "sb-1", "collab-1"); !errors.Is(err, dbErr) {
		t.Fatalf("expected storage error %v to propagate, got %v", dbErr, err)
	}

	svcNotFound := NewService(books, nil, nil, nil, nil, nil, nil, nil, nil).
		WithAccess(&stubGrantAccessStore{err: store.ErrGrantNotFound})

	if _, err := svcNotFound.GetManagedManifest(ctx, "sb-1", "collab-1"); !errors.Is(err, ErrSongbookNotFound) {
		t.Fatalf("expected ErrSongbookNotFound on ErrGrantNotFound, got %v", err)
	}
}

func TestBatchDeleteSongs_LoadsStaticCatalogOncePerBatch(t *testing.T) {
	ctx := context.Background()
	books := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			"sb-1": {ID: "sb-1", OwnerID: "owner-1", CatalogVersionID: "ver-1"},
		},
	}
	catalogs := &fakeServiceCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"ver-1": {ID: "ver-1", Status: models.CatalogVersionReady, AssetPrefix: "catalogs/ver-1"},
		},
	}
	reader := &countingCatalogReader{
		songs: []*models.Song{
			{ID: "s-1", SongbookID: "sb-1", Title: "One"},
			{ID: "s-2", SongbookID: "sb-1", Title: "Two"},
			{ID: "s-3", SongbookID: "sb-1", Title: "Three"},
			{ID: "s-other", SongbookID: "sb-2", Title: "Wrong Book"},
		},
	}
	changes := &countingBatchChangeStore{
		changes: []*models.CatalogChange{
			{SongbookID: "sb-1", SongID: "s-pending", Song: &models.Song{ID: "s-pending", SongbookID: "sb-1", Title: "Pending"}},
		},
	}

	svc := NewService(books, nil, nil, catalogs, reader, changes, nil, nil, nil)

	deleted, err := svc.BatchDeleteSongs(ctx, "sb-1", []string{"s-1", "s-2", "s-1", "s-pending", "s-missing", "s-other"})
	if err != nil {
		t.Fatalf("BatchDeleteSongs failed: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted (s-1, s-2, s-pending), got %d", deleted)
	}
	if reader.calls != 1 {
		t.Fatalf("expected CatalogReader.ListSongs to be called once, got %d", reader.calls)
	}
	if changes.listCalls != 1 {
		t.Fatalf("expected ListCatalogChanges to be called once, got %d", changes.listCalls)
	}
	if len(changes.putCalls) != 3 {
		t.Fatalf("expected 3 tombstone puts, got %d", len(changes.putCalls))
	}
}

type missingBookStore struct {
	store.SongbookStore
}

func (m *missingBookStore) GetSongbook(context.Context, string) (*models.Songbook, error) {
	return nil, os.ErrNotExist
}

func TestBatchDeleteSongs_SkipsMissingBook(t *testing.T) {
	ctx := context.Background()
	catalogs := &fakeServiceCatalogStore{versions: map[string]*models.CatalogVersion{}}
	reader := &countingCatalogReader{}
	changes := &countingBatchChangeStore{}
	svcMissing := NewService(&missingBookStore{}, nil, nil, catalogs, reader, changes, nil, nil, nil)
	deleted, err := svcMissing.BatchDeleteSongs(ctx, "sb-missing", []string{"s-1"})
	if err != nil {
		t.Fatalf("expected nil error for missing songbook, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted for missing songbook, got %d", deleted)
	}
}

type failingCatalogChangeListStore struct {
	store.CatalogChangeStore
	listErr error
}

func (f *failingCatalogChangeListStore) ListCatalogChanges(_ context.Context, _ string) ([]*models.CatalogChange, error) {
	return nil, f.listErr
}

func TestListSongs_PropagatesCatalogChangesStorageError(t *testing.T) {
	ctx := context.Background()
	dbErr := errors.New("catalog changes unavailable")
	svc := NewService(nil, nil, nil, nil, nil, &failingCatalogChangeListStore{listErr: dbErr}, nil, nil, nil)
	if _, err := svc.ListSongs(ctx, "sb-1", "", 0); !errors.Is(err, dbErr) {
		t.Fatalf("expected ListSongs to propagate %v, got %v", dbErr, err)
	}
}
