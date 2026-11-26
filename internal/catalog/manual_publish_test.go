package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"micup.live/server/internal/models"
)

type testCatalogJobQueue struct {
	jobs []BuildJob
	err  error
}

func (q *testCatalogJobQueue) Enqueue(_ context.Context, job BuildJob) error {
	if q.err != nil {
		return q.err
	}
	q.jobs = append(q.jobs, job)
	return nil
}

type errCatalogChangeStore struct {
	*fakeCatalogChangeStore
	listErr error
}

func (s *errCatalogChangeStore) ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.fakeCatalogChangeStore.ListCatalogChanges(ctx, songbookID)
}

func TestCreateSong_EnqueuesCompactionJob(t *testing.T) {
	ctx := context.Background()

	t.Run("enqueues compaction job on success when catalog jobs configured", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		jobs := &testCatalogJobQueue{}
		svc := &Service{
			CatalogChanges: changes,
			CatalogJobs:    jobs,
		}

		song, err := svc.CreateSong(ctx, "sb-123", models.SongCreate{
			Artist:      "Queen",
			Title:       "Bohemian Rhapsody",
			Fingerprint: "fp-123",
		})
		if err != nil {
			t.Fatalf("unexpected error creating song: %v", err)
		}
		if song == nil || song.ID == "" {
			t.Fatalf("expected created song with ID, got %+v", song)
		}

		// Verify override persisted in CatalogChanges
		storedChanges, err := changes.ListCatalogChanges(ctx, "sb-123")
		if err != nil {
			t.Fatalf("unexpected error listing changes: %v", err)
		}
		if len(storedChanges) != 1 || storedChanges[0].SongID != song.ID {
			t.Fatalf("expected 1 stored change for song %s, got %+v", song.ID, storedChanges)
		}

		// Verify compaction job enqueued
		if len(jobs.jobs) != 1 {
			t.Fatalf("expected 1 compaction job enqueued, got %d", len(jobs.jobs))
		}
		if jobs.jobs[0].SongbookID != "sb-123" || !jobs.jobs[0].Compaction {
			t.Fatalf("expected compaction job for sb-123, got %+v", jobs.jobs[0])
		}
	})

	t.Run("succeeds when catalog jobs is nil", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		svc := &Service{
			CatalogChanges: changes,
			CatalogJobs:    nil,
		}

		song, err := svc.CreateSong(ctx, "sb-123", models.SongCreate{
			Artist:      "Queen",
			Title:       "Under Pressure",
			Fingerprint: "fp-456",
		})
		if err != nil {
			t.Fatalf("unexpected error creating song: %v", err)
		}
		if song == nil {
			t.Fatal("expected song returned")
		}

		storedChanges, err := changes.ListCatalogChanges(ctx, "sb-123")
		if err != nil {
			t.Fatalf("unexpected error listing changes: %v", err)
		}
		if len(storedChanges) != 1 {
			t.Fatalf("expected 1 stored change, got %d", len(storedChanges))
		}
	})

	t.Run("succeeds best-effort when enqueue returns error", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		jobs := &testCatalogJobQueue{err: errors.New("sqs throttled")}
		svc := &Service{
			CatalogChanges: changes,
			CatalogJobs:    jobs,
		}

		song, err := svc.CreateSong(ctx, "sb-123", models.SongCreate{
			Artist:      "Queen",
			Title:       "Radio Ga Ga",
			Fingerprint: "fp-789",
		})
		if err != nil {
			t.Fatalf("CreateSong should succeed best-effort even if enqueue fails, got: %v", err)
		}
		if song == nil {
			t.Fatal("expected song returned")
		}

		storedChanges, err := changes.ListCatalogChanges(ctx, "sb-123")
		if err != nil {
			t.Fatalf("unexpected error listing changes: %v", err)
		}
		if len(storedChanges) != 1 {
			t.Fatalf("expected 1 stored change, got %d", len(storedChanges))
		}
	})
}

func TestGetPublicManifest_OverlayReady(t *testing.T) {
	ctx := context.Background()
	publicBook := &models.Songbook{
		ID:               "sb-manual",
		PublicID:         "manual-show",
		Name:             "Manual Show",
		IsPublic:         true,
		CatalogVersionID: "", // No compiled static catalog version
	}

	sbStore := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			publicBook.ID: publicBook,
		},
	}

	t.Run("returns overlay_ready true with active song count when overrides exist", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		now := time.Now().UTC()
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: publicBook.ID,
			SongID:     "s-1",
			Song:       &models.Song{ID: "s-1", Title: "Song 1", Artist: "Artist A"},
			UpdatedAt:  now,
		})
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: publicBook.ID,
			SongID:     "s-2",
			Song:       &models.Song{ID: "s-2", Title: "Song 2", Artist: "Artist B"},
			UpdatedAt:  now.Add(time.Second),
		})

		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		manifest, err := svc.GetPublicManifest(ctx, publicBook.PublicID, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != false {
			t.Fatalf("expected static_ready false, got %v", manifest["static_ready"])
		}
		if manifest["overlay_ready"] != true {
			t.Fatalf("expected overlay_ready true, got %v", manifest["overlay_ready"])
		}
		if manifest["song_count"] != 2 {
			t.Fatalf("expected song_count 2, got %v", manifest["song_count"])
		}
	})

	t.Run("returns overlay_ready false when all manual songs are deleted", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		now := time.Now().UTC()
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: publicBook.ID,
			SongID:     "s-1",
			Song:       &models.Song{ID: "s-1", Title: "Song 1", Artist: "Artist A"},
			UpdatedAt:  now,
		})
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: publicBook.ID,
			SongID:     "s-1",
			Deleted:    true,
			UpdatedAt:  now.Add(time.Second),
		})

		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		manifest, err := svc.GetPublicManifest(ctx, publicBook.PublicID, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != false {
			t.Fatalf("expected static_ready false, got %v", manifest["static_ready"])
		}
		if manifest["overlay_ready"] != false {
			t.Fatalf("expected overlay_ready false, got %v", manifest["overlay_ready"])
		}
		if manifest["song_count"] != nil {
			t.Fatalf("expected no song_count when overlay_ready is false, got %v", manifest["song_count"])
		}
	})

	t.Run("returns overlay_ready false when no manual songs exist", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		manifest, err := svc.GetPublicManifest(ctx, publicBook.PublicID, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != false {
			t.Fatalf("expected static_ready false, got %v", manifest["static_ready"])
		}
		if manifest["overlay_ready"] != false {
			t.Fatalf("expected overlay_ready false, got %v", manifest["overlay_ready"])
		}
	})

	t.Run("propagates storage error from ListCatalogChanges", func(t *testing.T) {
		dbErr := errors.New("dynamo failure")
		changes := &errCatalogChangeStore{
			fakeCatalogChangeStore: newFakeCatalogChangeStore(),
			listErr:                dbErr,
		}
		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		_, err := svc.GetPublicManifest(ctx, publicBook.PublicID, "")
		if !errors.Is(err, dbErr) {
			t.Fatalf("expected propagated dbErr, got %v", err)
		}
	})

	t.Run("returns static_ready true when static version is ready", func(t *testing.T) {
		readyBook := &models.Songbook{
			ID:               "sb-ready",
			PublicID:         "ready-show",
			Name:             "Ready Show",
			IsPublic:         true,
			CatalogVersionID: "v-ready",
		}
		sbReadyStore := &fakeServiceSongbookStore{
			books: map[string]*models.Songbook{
				readyBook.ID: readyBook,
			},
		}
		catStore := &fakeServiceCatalogStore{
			versions: map[string]*models.CatalogVersion{
				"v-ready": {
					ID:          "v-ready",
					Status:      models.CatalogVersionReady,
					SongCount:   100,
					AssetPrefix: "catalogs/ready",
				},
			},
		}
		svc := &Service{
			Songbooks: sbReadyStore,
			Catalogs:  catStore,
		}

		manifest, err := svc.GetPublicManifest(ctx, readyBook.PublicID, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != true {
			t.Fatalf("expected static_ready true, got %v", manifest["static_ready"])
		}
		if manifest["total"] != 100 {
			t.Fatalf("expected total 100, got %v", manifest["total"])
		}
	})
}

func TestGetManagedManifest_OverlayReady(t *testing.T) {
	ctx := context.Background()
	ownerID := "host-1"
	book := &models.Songbook{
		ID:               "sb-managed-1",
		OwnerID:          ownerID,
		Name:             "Host Show",
		IsPublic:         false,
		CatalogVersionID: "", // No compiled static catalog version
	}

	sbStore := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			book.ID: book,
		},
	}

	t.Run("returns overlay_ready true with song count for owner when manual songs exist", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		now := time.Now().UTC()
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: book.ID,
			SongID:     "s-1",
			Song:       &models.Song{ID: "s-1", Title: "Song 1", Artist: "Artist A"},
			UpdatedAt:  now,
		})

		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		manifest, err := svc.GetManagedManifest(ctx, book.ID, ownerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != false {
			t.Fatalf("expected static_ready false, got %v", manifest["static_ready"])
		}
		if manifest["overlay_ready"] != true {
			t.Fatalf("expected overlay_ready true, got %v", manifest["overlay_ready"])
		}
		if manifest["song_count"] != 1 {
			t.Fatalf("expected song_count 1, got %v", manifest["song_count"])
		}
	})

	t.Run("returns overlay_ready false when all manual songs are deleted", func(t *testing.T) {
		changes := newFakeCatalogChangeStore()
		now := time.Now().UTC()
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: book.ID,
			SongID:     "s-1",
			Song:       &models.Song{ID: "s-1", Title: "Song 1", Artist: "Artist A"},
			UpdatedAt:  now,
		})
		_ = changes.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: book.ID,
			SongID:     "s-1",
			Deleted:    true,
			UpdatedAt:  now.Add(time.Second),
		})

		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		manifest, err := svc.GetManagedManifest(ctx, book.ID, ownerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manifest["static_ready"] != false {
			t.Fatalf("expected static_ready false, got %v", manifest["static_ready"])
		}
		if manifest["overlay_ready"] != false {
			t.Fatalf("expected overlay_ready false, got %v", manifest["overlay_ready"])
		}
	})

	t.Run("propagates storage error from ListCatalogChanges in managed manifest", func(t *testing.T) {
		dbErr := errors.New("dynamo failure")
		changes := &errCatalogChangeStore{
			fakeCatalogChangeStore: newFakeCatalogChangeStore(),
			listErr:                dbErr,
		}
		svc := &Service{
			Songbooks:      sbStore,
			CatalogChanges: changes,
		}

		_, err := svc.GetManagedManifest(ctx, book.ID, ownerID)
		if !errors.Is(err, dbErr) {
			t.Fatalf("expected propagated dbErr, got %v", err)
		}
	})
}
