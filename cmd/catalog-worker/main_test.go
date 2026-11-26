package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type fakeS3Client struct {
	objects   map[string][]byte
	deleted   map[string]bool
	deleteErr error
}

func (f *fakeS3Client) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := *params.Key
	data, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func (f *fakeS3Client) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	key := *params.Key
	f.deleted[key] = true
	delete(f.objects, key)
	return &s3.DeleteObjectOutput{}, nil
}

type fakeImportJobStore struct {
	jobs      map[string]*models.ImportJob
	updateErr error
	getErr    error
	assignErr error
}

func (f *fakeImportJobStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportJobStore) GetImportJob(_ context.Context, id string) (*models.ImportJob, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeImportJobStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeImportJobStore) DeleteImportJob(_ context.Context, id string) error {
	delete(f.jobs, id)
	return nil
}
func (f *fakeImportJobStore) UpdateImportJob(_ context.Context, job *models.ImportJob) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	if existing, ok := f.jobs[job.ID]; ok {
		if job.Generation > 0 && existing.Generation > 0 && existing.Generation != job.Generation {
			return store.ErrJobGenerationConflict
		}
		if job.Generation == 0 && existing.Generation > 0 {
			job.Generation = existing.Generation
		}
	}
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportJobStore) LatestImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeImportJobStore) AssignImportJobGeneration(_ context.Context, jobID string, generation int64) (int64, error) {
	if f.assignErr != nil {
		return 0, f.assignErr
	}
	if j, ok := f.jobs[jobID]; ok {
		if j.Generation > 0 {
			return j.Generation, nil
		}
		j.Generation = generation
		return generation, nil
	}
	return 0, os.ErrNotExist
}

func (f *fakeImportJobStore) MarkImportJobDispatchReady(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status == models.ImportJobProcessing || j.DispatchedAt != nil {
		return store.ErrImportJobAlreadyDispatched
	}
	j.DispatchReady = true
	j.Status = models.ImportJobQueued
	return nil
}

func (f *fakeImportJobStore) ClaimImportJobDispatch(_ context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, "", store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, "", store.ErrImportJobTerminal
	}
	if j.DispatchedAt != nil {
		return nil, "", store.ErrImportJobAlreadyDispatched
	}
	if !j.DispatchReady {
		return nil, "", store.ErrImportJobNotReady
	}
	token := "claim-token-" + jobID
	exp := time.Now().Add(leaseDuration)
	j.DispatchClaimToken = token
	j.DispatchClaimExpiresAt = &exp
	j.DispatchAttempts++
	return j, token, nil
}

func (f *fakeImportJobStore) ConfirmImportJobDispatch(_ context.Context, jobID string, claimToken string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.DispatchedAt = &now
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportJobStore) RecordDispatchError(_ context.Context, jobID string, claimToken string, errMsg string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	j.Error = errMsg
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportJobStore) ListDispatchableImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts < maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportJobStore) ClaimImportJobProcessing(_ context.Context, jobID string) (*models.ImportJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, store.ErrImportJobTerminal
	}
	j.Status = models.ImportJobProcessing
	if j.DispatchedAt == nil {
		now := time.Now().UTC()
		j.DispatchedAt = &now
	}
	return j, nil
}

func (f *fakeImportJobStore) FailImportJob(_ context.Context, jobID string, reason string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	j.Status = models.ImportJobFailed
	j.Error = reason
	return nil
}

func (f *fakeImportJobStore) ListExhaustedImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts >= maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportJobStore) UpdateImportJobProgress(_ context.Context, jobID string, processedSongs int) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status != models.ImportJobProcessing {
		return os.ErrNotExist
	}
	j.ProcessedSongs = processedSongs
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeImportJobStore) ResetImportJobDispatch(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if !j.DispatchReady {
		return store.ErrImportJobNotReady
	}
	if j.DispatchedAt != nil || j.Status == models.ImportJobProcessing {
		return store.ErrImportJobAlreadyDispatched
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.DispatchClaimExpiresAt != nil && j.DispatchClaimExpiresAt.After(time.Now().UTC()) {
		return store.ErrImportJobClaimConflict
	}
	j.DispatchAttempts = 0
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	j.Error = ""
	j.UpdatedAt = time.Now().UTC()
	return nil
}

type fakeSongbookStore struct {
	store.SongbookStore
	books        map[string]*models.Songbook
	nextGen      int64
	nextGenErr   error
	nextGenCalls int
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
	f.nextGenCalls++
	if f.nextGenErr != nil {
		return 0, f.nextGenErr
	}
	if f.nextGen > 0 {
		return f.nextGen, nil
	}
	return 1, nil
}
func (f *fakeSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type fakeCatalogStore struct {
	versions     map[string]*models.CatalogVersion
	published    map[string]string
	publishErr   error
	publishCalls int
	importJobs   store.ImportJobStore
}

func (s *fakeCatalogStore) CreateCatalogVersion(_ context.Context, version *models.CatalogVersion) error {
	if _, exists := s.versions[version.ID]; exists {
		return os.ErrExist
	}
	copy := *version
	copy.Status = models.CatalogVersionBuilding
	if copy.LeaseToken == "" {
		copy.LeaseToken = "token-created"
	}
	s.versions[copy.ID] = &copy
	return nil
}
func (s *fakeCatalogStore) GetCatalogVersion(_ context.Context, id string) (*models.CatalogVersion, error) {
	version, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
	}
	copy := *version
	return &copy, nil
}
func (s *fakeCatalogStore) MarkCatalogVersionReady(_ context.Context, id, prefix string, count int, leaseToken ...string) (*models.CatalogVersion, error) {
	version, err := s.GetCatalogVersion(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if len(leaseToken) > 0 && leaseToken[0] != "" && version.LeaseToken != "" && version.LeaseToken != leaseToken[0] {
		return nil, os.ErrNotExist
	}
	version.Status, version.AssetPrefix, version.SongCount = models.CatalogVersionReady, prefix, count
	version.LeaseToken = ""
	s.versions[id] = version
	return version, nil
}
func (s *fakeCatalogStore) FailCatalogVersion(_ context.Context, id, reason string, leaseToken ...string) (*models.CatalogVersion, error) {
	version, err := s.GetCatalogVersion(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if len(leaseToken) > 0 && leaseToken[0] != "" && version.LeaseToken != "" && version.LeaseToken != leaseToken[0] {
		return nil, os.ErrNotExist
	}
	version.Status, version.FailureReason = models.CatalogVersionFailed, reason
	version.LeaseToken = ""
	s.versions[id] = version
	return version, nil
}
func (s *fakeCatalogStore) ReclaimCatalogVersion(_ context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	version, err := s.GetCatalogVersion(context.Background(), id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	isExpired := (!version.LeaseExpiresAt.IsZero() && now.After(version.LeaseExpiresAt)) ||
		(version.LeaseExpiresAt.IsZero() && !version.CreatedAt.IsZero() && now.Sub(version.CreatedAt) > leaseDuration)
	if version.Status != models.CatalogVersionFailed && !(version.Status == models.CatalogVersionBuilding && isExpired) {
		return nil, store.ErrReclaimConflict
	}
	version.Status = models.CatalogVersionBuilding
	version.FailureReason = ""
	version.LeaseToken = "token-reclaimed"
	version.LeaseExpiresAt = now.Add(leaseDuration)
	s.versions[id] = version
	return version, nil
}
func (s *fakeCatalogStore) PublishCatalogVersion(_ context.Context, songbookID, catalogID string, opts ...store.PublicationOptions) error {
	s.publishCalls++
	if s.publishErr != nil {
		return s.publishErr
	}
	version, err := s.GetCatalogVersion(context.Background(), catalogID)
	if err != nil || version.Status != models.CatalogVersionReady {
		return os.ErrNotExist
	}
	if len(opts) > 0 && opts[0].JobID != "" && s.importJobs != nil {
		stored, getErr := s.importJobs.GetImportJob(context.Background(), opts[0].JobID)
		if getErr == nil && stored != nil {
			if stored.Status == models.ImportJobCompleted {
				return store.ErrImportJobAlreadyCompleted
			}
			if stored.Status == models.ImportJobFailed {
				return store.ErrImportJobTerminal
			}
			stored.Status = models.ImportJobCompleted
			stored.ProcessedSongs = opts[0].ProcessedSongs
			stored.CatalogVersionID = catalogID
			now := time.Now().UTC()
			stored.CompletedAt = now
			stored.UpdatedAt = now
		}
	}
	s.published[songbookID] = catalogID
	return nil
}
func (s *fakeCatalogStore) DeleteCatalogVersion(_ context.Context, id string, _ ...string) error {
	delete(s.versions, id)
	return nil
}
func (s *fakeCatalogStore) ReserveCatalogVersionForDeletion(_ context.Context, id string, _ time.Duration) (*models.CatalogVersion, error) {
	v, ok := s.versions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	v.Status = models.CatalogVersionDeleting
	return v, nil
}
func (s *fakeCatalogStore) ReleaseCatalogVersionReservation(_ context.Context, id, _ string) error {
	v, ok := s.versions[id]
	if !ok {
		return os.ErrNotExist
	}
	v.Status = models.CatalogVersionReady
	return nil
}
func (s *fakeCatalogStore) IsCatalogVersionReferenced(_ context.Context, _ string, _ ...string) (bool, error) {
	return false, nil
}
func (s *fakeCatalogStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	v, ok := s.versions[id]
	if !ok {
		return os.ErrNotExist
	}
	if purgeAssets != nil && v.AssetPrefix != "" {
		if err := purgeAssets(v.AssetPrefix); err != nil {
			return err
		}
	}
	return s.DeleteCatalogVersion(ctx, id)
}
func (s *fakeCatalogStore) PurgeOrphanCatalogPrefix(_ context.Context, prefix string, purgeAssets func(assetPrefix string) error, _ ...store.PurgeCatalogOptions) error {
	if purgeAssets != nil {
		return purgeAssets(prefix)
	}
	return nil
}

type fakeAssetStore struct {
	assets   []catalog.Asset
	putCalls int
}

func (s *fakeAssetStore) Put(_ context.Context, _ string, assets []catalog.Asset) error {
	s.putCalls++
	s.assets = append([]catalog.Asset(nil), assets...)
	return nil
}

type fakeCatalogReader struct {
	songsByPrefix map[string][]*models.Song
}

func (f *fakeCatalogReader) ListSongs(_ context.Context, assetPrefix, _ string) ([]*models.Song, error) {
	return f.songsByPrefix[assetPrefix], nil
}

func TestBuildFromManifest_PreservesExistingCatalogWhenPruneMissingFalse(t *testing.T) {
	ctx := context.Background()

	// Prior catalog had Queen - Bohemian Rhapsody
	catalogs := &fakeCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"cat-prior": {
				ID:          "cat-prior",
				Status:      models.CatalogVersionReady,
				AssetPrefix: "catalogs/cat-prior",
				SongCount:   1,
			},
		},
		published: map[string]string{
			"book-1": "cat-prior",
		},
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: "cat-prior"},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			"catalogs/cat-prior": {
				{ID: "trk_queen", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
			},
		},
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-1": {ID: "job-1", SongbookID: "book-1", Status: models.ImportJobProcessing},
		},
	}
	catalogs.importJobs = importStore

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	// Incoming manifest in S3 has The Beatles - Yesterday, and prune_missing = false
	manifestJSON := []byte(`{
		"songs": [
			{"artist": "The Beatles", "title": "Yesterday", "disc": "SC02", "fingerprint": "fp-beatles", "is_available": true}
		],
		"prune_missing": false
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-1.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-1.json", false, 0)
	if err != nil {
		t.Fatalf("buildFromManifestWithClient failed: %v", err)
	}

	// 1. Verify manifest was deleted from S3
	if !s3Client.deleted["imports/job-1.json"] {
		t.Errorf("expected manifest to be deleted from S3")
	}

	// 2. Verify import job status was marked completed with 1 processed song
	job := importStore.jobs["job-1"]
	if job.Status != models.ImportJobCompleted || job.ProcessedSongs != 1 {
		t.Errorf("unexpected import job state: %+v", job)
	}

	// 3. Verify the published catalog has 2 songs (Queen was NOT pruned)
	publishedCatID := catalogs.published["book-1"]
	if publishedCatID == "" || publishedCatID == "cat-prior" {
		t.Fatalf("expected new catalog version published, got %q", publishedCatID)
	}
	publishedCat := catalogs.versions[publishedCatID]
	if publishedCat.SongCount != 2 {
		t.Fatalf("expected 2 songs in merged catalog (Queen + Beatles), got %d", publishedCat.SongCount)
	}
}

func TestBuildFromManifest_PrunesWhenPruneMissingTrue(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"cat-prior": {
				ID:          "cat-prior",
				Status:      models.CatalogVersionReady,
				AssetPrefix: "catalogs/cat-prior",
				SongCount:   1,
			},
		},
		published: map[string]string{
			"book-1": "cat-prior",
		},
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-1": {ID: "book-1", CatalogVersionID: "cat-prior"},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: map[string][]*models.Song{
			"catalogs/cat-prior": {
				{ID: "trk_queen", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
			},
		},
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-2": {ID: "job-2", SongbookID: "book-1", Status: models.ImportJobProcessing},
		},
	}
	catalogs.importJobs = importStore

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "The Beatles", "title": "Yesterday", "disc": "SC02", "fingerprint": "fp-beatles", "is_available": true}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-2.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-2.json", true, 0)
	if err != nil {
		t.Fatalf("buildFromManifestWithClient failed: %v", err)
	}

	publishedCatID := catalogs.published["book-1"]
	publishedCat := catalogs.versions[publishedCatID]
	if publishedCat.SongCount != 1 {
		t.Fatalf("expected 1 song in replaced catalog, got %d", publishedCat.SongCount)
	}
}

func TestBuildFromManifest_PublishesExistingReadyVersion(t *testing.T) {
	ctx := context.Background()

	songs := []*models.Song{
		{Artist: "Queen", Title: "Radio Ga Ga", Disc: "SC03", Fingerprint: "fp-queen-radio", IsAvailable: true},
	}
	compiled, err := catalog.Compile(songs, catalog.Options{SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}

	// Catalog version was already compiled and marked ready by another songbook
	catalogs := &fakeCatalogStore{
		versions: map[string]*models.CatalogVersion{
			compiled.CatalogID: {
				ID:          compiled.CatalogID,
				Status:      models.CatalogVersionReady,
				AssetPrefix: "catalogs/" + compiled.CatalogID,
				SongCount:   1,
			},
		},
		published: make(map[string]string),
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-reuse": {ID: "book-reuse"},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-reuse": {ID: "job-reuse", SongbookID: "book-reuse", Status: models.ImportJobProcessing},
		},
	}
	catalogs.importJobs = importStore

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Queen", "title": "Radio Ga Ga", "disc": "SC03", "fingerprint": "fp-queen-radio", "is_available": true}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-reuse.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err = buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-reuse", "imports/job-reuse.json", true, 0)
	if err != nil {
		t.Fatalf("buildFromManifestWithClient failed: %v", err)
	}

	// Manifest deleted
	if !s3Client.deleted["imports/job-reuse.json"] {
		t.Error("expected manifest to be deleted from S3")
	}

	// Import job completed
	job := importStore.jobs["job-reuse"]
	if job.Status != models.ImportJobCompleted {
		t.Errorf("expected import job completed, got %s", job.Status)
	}

	// book-reuse published to the ready catalog version!
	if catalogs.published["book-reuse"] != compiled.CatalogID {
		t.Fatalf("expected book-reuse published to %s, got %s", compiled.CatalogID, catalogs.published["book-reuse"])
	}
}

func TestBuildFromManifest_SupersededStalePublication_FailsGracefullyAndDeletesManifest(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions:   make(map[string]*models.CatalogVersion),
		published:  make(map[string]string),
		publishErr: store.ErrStalePublication,
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-stale": {ID: "book-stale", CatalogGeneration: 10},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-stale": {
				ID:         "job-stale",
				SongbookID: "book-stale",
				Status:     models.ImportJobProcessing,
				Generation: 5,
			},
		},
	}

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Stale Artist", "title": "Stale Song", "fingerprint": "fp-stale"}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-stale.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	// buildFromManifestWithClient should gracefully return nil when stale, acknowledging the queue message
	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-stale", "imports/job-stale.json", true, 5)
	if err != nil {
		t.Fatalf("expected nil error on superseded import, got: %v", err)
	}

	// Manifest deleted from S3
	if !s3Client.deleted["imports/job-stale.json"] {
		t.Error("expected manifest to be deleted from S3")
	}

	// Import job marked failed
	job := importStore.jobs["job-stale"]
	if job.Status != models.ImportJobFailed {
		t.Errorf("expected import job failed, got %s", job.Status)
	}
	if !strings.Contains(job.Error, "superseded") {
		t.Errorf("expected error to mention superseded, got %q", job.Error)
	}

	// Songbook was NOT published to stale version
	if _, ok := catalogs.published["book-stale"]; ok {
		t.Error("expected songbook NOT to be published")
	}
}

func TestBuildFromManifest_LegacyJob_AllocatesAndPersistsSequentialGeneration(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions:  make(map[string]*models.CatalogVersion),
		published: make(map[string]string),
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-legacy": {ID: "book-legacy", CatalogGeneration: 3},
		},
		nextGen: 4,
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-legacy": {
				ID:         "job-legacy",
				SongbookID: "book-legacy",
				Status:     models.ImportJobProcessing,
				Generation: 0, // legacy job
				CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	catalogs.importJobs = importStore

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Legacy Artist", "title": "Legacy Song", "fingerprint": "fp-leg"}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-legacy.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-legacy", "imports/job-legacy.json", true, 0)
	if err != nil {
		t.Fatalf("expected buildFromManifestWithClient to succeed, got: %v", err)
	}

	// Job generation must be allocated as sequential 4 and persisted, NOT a timestamp
	job := importStore.jobs["job-legacy"]
	if job.Generation != 4 {
		t.Fatalf("expected job generation to be sequential 4, got %d", job.Generation)
	}
}

func TestBuildFromManifest_BaseConflict_PreservesManifestAndReturnsErrorForRetry(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions:   make(map[string]*models.CatalogVersion),
		published:  make(map[string]string),
		publishErr: store.ErrPublicationBaseConflict,
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-conflict": {ID: "book-conflict", CatalogGeneration: 2},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-conflict": {
				ID:         "job-conflict",
				SongbookID: "book-conflict",
				Status:     models.ImportJobProcessing,
				Generation: 5,
			},
		},
	}

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Artist", "title": "Song", "fingerprint": "fp-c"}
		],
		"prune_missing": false
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-conflict.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-conflict", "imports/job-conflict.json", false, 5)
	if err == nil {
		t.Fatal("expected error on base version conflict, got nil")
	}
	if !errors.Is(err, store.ErrPublicationBaseConflict) && !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrPublicationBaseConflict or ErrStalePublication, got: %v", err)
	}

	// Manifest MUST NOT be deleted from S3 on retryable base conflict!
	if s3Client.deleted["imports/job-conflict.json"] {
		t.Error("expected manifest NOT to be deleted from S3 on retryable base conflict")
	}

	// Job MUST NOT be marked failed on retryable conflict
	job := importStore.jobs["job-conflict"]
	if job.Status == models.ImportJobFailed {
		t.Errorf("expected job not to be marked failed on retryable conflict, got status: %s", job.Status)
	}
}

func TestBuildFromManifest_Superseded_WhenStatusWriteFails_PreservesManifestAndReturnsError(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions:   make(map[string]*models.CatalogVersion),
		published:  make(map[string]string),
		publishErr: store.ErrPublicationSuperseded,
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-sup": {ID: "book-sup", CatalogGeneration: 10},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	statusWriteErr := errors.New("dynamodb outage during status write")
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-sup": {
				ID:         "job-sup",
				SongbookID: "book-sup",
				Status:     models.ImportJobProcessing,
				Generation: 5,
			},
		},
		updateErr: statusWriteErr,
	}

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Artist", "title": "Song", "fingerprint": "fp-s"}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-sup.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-sup", "imports/job-sup.json", true, 5)
	if err == nil {
		t.Fatal("expected error when status write fails during supersession, got nil")
	}

	// Manifest MUST NOT be deleted from S3 if status write failed!
	if s3Client.deleted["imports/job-sup.json"] {
		t.Error("expected manifest NOT to be deleted from S3 when status write failed")
	}
}

func TestBuildFromManifest_GetImportJobFailure_AbortsWithoutAllocationOrPublication(t *testing.T) {
	ctx := context.Background()

	catalogs := &fakeCatalogStore{
		versions:  make(map[string]*models.CatalogVersion),
		published: make(map[string]string),
	}
	bookStore := &fakeSongbookStore{
		books: map[string]*models.Songbook{
			"book-err": {ID: "book-err", CatalogGeneration: 1},
		},
	}
	reader := &fakeCatalogReader{
		songsByPrefix: make(map[string][]*models.Song),
	}
	assets := &fakeAssetStore{}
	builder := catalog.Builder{
		Catalogs: catalogs,
		Assets:   assets,
		Options:  catalog.Options{SchemaVersion: 1},
	}
	jobReadErr := errors.New("dynamodb outage loading import job")
	importStore := &fakeImportJobStore{
		jobs:   make(map[string]*models.ImportJob),
		getErr: jobReadErr,
	}

	runner := &catalog.Runner{
		Books:   bookStore,
		Builder: builder,
		Reader:  reader,
		Imports: importStore,
	}

	manifestJSON := []byte(`{
		"songs": [
			{"artist": "Artist", "title": "Song", "fingerprint": "fp-err"}
		],
		"prune_missing": true
	}`)

	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-read-err.json": manifestJSON,
		},
		deleted: make(map[string]bool),
	}

	// Test with legacy job (jobGeneration = 0)
	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-err", "imports/job-read-err.json", true, 0)
	if err == nil {
		t.Fatal("expected error when GetImportJob fails, got nil (acknowledging SQS message!)")
	}
	if !errors.Is(err, jobReadErr) {
		t.Fatalf("expected error wrapping jobReadErr, got: %v", err)
	}

	// Invariant checks:
	// 1. No generation allocation
	if bookStore.nextGenCalls != 0 {
		t.Errorf("expected 0 NextCatalogGeneration calls, got %d", bookStore.nextGenCalls)
	}
	// 2. No publication
	if catalogs.publishCalls != 0 {
		t.Errorf("expected 0 PublishCatalogVersion calls, got %d", catalogs.publishCalls)
	}
	// 3. No catalog build assets written
	if assets.putCalls != 0 {
		t.Errorf("expected 0 asset Put calls, got %d", assets.putCalls)
	}
	// 4. No manifest deletion (must preserve in S3 for retry)
	if s3Client.deleted["imports/job-read-err.json"] {
		t.Error("expected manifest NOT to be deleted from S3 when GetImportJob fails")
	}
}

func TestBuildFromManifest_CompletedJobRedelivery_DeletesManifestAndReturnsNil(t *testing.T) {
	ctx := context.Background()
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-done": {ID: "job-done", SongbookID: "book-1", Status: models.ImportJobCompleted},
		},
	}
	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-done.json": []byte(`{"songs": []}`),
		},
		deleted: make(map[string]bool),
	}
	runner := &catalog.Runner{
		Imports: importStore,
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-done.json", false, 1)
	if err != nil {
		t.Fatalf("expected nil for completed job redelivery, got: %v", err)
	}
	if !s3Client.deleted["imports/job-done.json"] {
		t.Error("expected completed job manifest to be deleted from S3")
	}
}

func TestBuildFromManifest_CompletedJobWithDeletedManifest_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-done-nomanifest": {ID: "job-done-nomanifest", SongbookID: "book-1", Status: models.ImportJobCompleted},
		},
	}
	s3Client := &fakeS3Client{
		objects: map[string][]byte{},
		deleted: make(map[string]bool),
	}
	runner := &catalog.Runner{
		Imports: importStore,
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-done-nomanifest.json", false, 1)
	if err != nil {
		t.Fatalf("expected nil for completed job with deleted manifest, got: %v", err)
	}
}

func TestBuildFromManifest_TerminalFailedJob_DeletesManifestAndReturnsNil(t *testing.T) {
	ctx := context.Background()
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-term": {ID: "job-term", SongbookID: "book-1", Status: models.ImportJobFailed, Error: "fatal"},
		},
	}
	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-term.json": []byte(`{"songs": []}`),
		},
		deleted: make(map[string]bool),
	}
	runner := &catalog.Runner{
		Imports: importStore,
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-term.json", false, 1)
	if err != nil {
		t.Fatalf("expected nil for terminal failed job, got: %v", err)
	}
	if !s3Client.deleted["imports/job-term.json"] {
		t.Error("expected failed job manifest to be deleted from S3")
	}
}

func TestBuildFromManifest_CompletedJobRedelivery_DeleteErrorPropagated(t *testing.T) {
	ctx := context.Background()
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-del-err": {ID: "job-del-err", SongbookID: "book-1", Status: models.ImportJobCompleted},
		},
	}
	delErr := errors.New("s3 delete outage")
	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-del-err.json": []byte(`{"songs": []}`),
		},
		deleted:   make(map[string]bool),
		deleteErr: delErr,
	}
	runner := &catalog.Runner{
		Imports: importStore,
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-del-err.json", false, 1)
	if err == nil {
		t.Fatal("expected error when manifest deletion fails on completed job, got nil")
	}
	if !errors.Is(err, delErr) {
		t.Fatalf("expected error wrapping s3 delete error, got: %v", err)
	}
}

func TestBuildFromManifest_TerminalFailedJob_DeleteErrorPropagated(t *testing.T) {
	ctx := context.Background()
	importStore := &fakeImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-term-del-err": {ID: "job-term-del-err", SongbookID: "book-1", Status: models.ImportJobFailed, Error: "fatal"},
		},
	}
	delErr := errors.New("s3 delete outage")
	s3Client := &fakeS3Client{
		objects: map[string][]byte{
			"imports/job-term-del-err.json": []byte(`{"songs": []}`),
		},
		deleted:   make(map[string]bool),
		deleteErr: delErr,
	}
	runner := &catalog.Runner{
		Imports: importStore,
	}

	err := buildFromManifestWithClient(ctx, runner, s3Client, "test-bucket", "book-1", "imports/job-term-del-err.json", false, 1)
	if err == nil {
		t.Fatal("expected error when manifest deletion fails on terminal failed job, got nil")
	}
	if !errors.Is(err, delErr) {
		t.Fatalf("expected error wrapping s3 delete error, got: %v", err)
	}
}
