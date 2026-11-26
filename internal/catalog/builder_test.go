// Package catalog tests catalog build publication coordination.
package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestBuilderBuildsUploadsAndPublishes(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1, ShardSize: 1}}
	version, err := builder.BuildAndPublish(context.Background(), "songbook-1", []*models.Song{{Artist: "Queen", Title: "Don't Stop Me Now", IsAvailable: true}})
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if version.Status != models.CatalogVersionReady || catalogs.published["songbook-1"] != version.ID {
		t.Fatalf("published version = %+v, pointers = %+v", version, catalogs.published)
	}
	if !strings.HasPrefix(assets.prefix, "catalogs/epochs/"+version.ID+"/") || len(assets.assets) == 0 {
		t.Fatalf("uploaded assets = prefix %q, count %d", assets.prefix, len(assets.assets))
	}

	reused, err := builder.BuildAndPublish(context.Background(), "songbook-2", []*models.Song{{Artist: "Queen", Title: "Don't Stop Me Now", IsAvailable: true}})
	if err != nil || reused.ID != version.ID || catalogs.published["songbook-2"] != version.ID {
		t.Fatalf("reused version = %+v, %v, pointers = %+v", reused, err, catalogs.published)
	}
}

func TestBuilderFailsVersionWhenUploadFails(t *testing.T) {
	catalogs := newFakeCatalogStore()
	builder := Builder{Catalogs: catalogs, Assets: &fakeAssetStore{err: errors.New("storage unavailable")}, Options: Options{SchemaVersion: 1}}
	_, err := builder.BuildAndPublish(context.Background(), "songbook-1", []*models.Song{{Artist: "Queen", Title: "Killer Queen", IsAvailable: true}})
	if err == nil {
		t.Fatal("build succeeded after upload error")
	}
	for _, version := range catalogs.versions {
		if version.Status != models.CatalogVersionFailed {
			t.Fatalf("failed upload version = %+v", version)
		}
	}
}

func TestBuilderReportsMatchingBuildInProgress(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 20 * time.Millisecond,
		PollInterval:    5 * time.Millisecond,
	}
	compiled, err := Compile([]*models.Song{{Artist: "Queen", Title: "Radio Ga Ga", IsAvailable: true}}, builder.Options)
	if err != nil {
		t.Fatal(err)
	}
	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  1,
		Status:         models.CatalogVersionBuilding,
		LeaseExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	_, err = builder.BuildAndPublish(context.Background(), "songbook-1", []*models.Song{{Artist: "Queen", Title: "Radio Ga Ga", IsAvailable: true}})
	if !errors.Is(err, ErrBuildInProgress) {
		t.Fatalf("build error = %v; want ErrBuildInProgress", err)
	}
}

func TestReviewRetryCatalogAfterTransientUploadFailure(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{err: errors.New("transient S3 error")}
	builder := Builder{Catalogs: catalogs, Assets: assets, Options: Options{SchemaVersion: 1, ShardSize: 1}}

	songs := []*models.Song{{Artist: "Queen", Title: "Under Pressure", IsAvailable: true}}

	// First attempt fails due to transient upload error
	_, err := builder.BuildAndPublish(context.Background(), "songbook-1", songs)
	if err == nil {
		t.Fatal("expected first build attempt to fail on storage error")
	}

	compiled, compileErr := Compile(songs, builder.Options)
	if compileErr != nil {
		t.Fatal(compileErr)
	}

	failedVer, getErr := catalogs.GetCatalogVersion(context.Background(), compiled.CatalogID)
	if getErr != nil || failedVer.Status != models.CatalogVersionFailed {
		t.Fatalf("expected version in failed status, got: %+v, err: %v", failedVer, getErr)
	}

	// Storage recovers
	assets.err = nil

	// Retry identical content: should reclaim failed version, build, and publish!
	retryVer, retryErr := builder.BuildAndPublish(context.Background(), "songbook-1", songs)
	if retryErr != nil {
		t.Fatalf("expected retry after storage recovery to succeed, got: %v", retryErr)
	}
	if retryVer.Status != models.CatalogVersionReady {
		t.Fatalf("expected retry version to be ready, got: %+v", retryVer)
	}
	if catalogs.published["songbook-1"] != retryVer.ID {
		t.Fatalf("expected songbook-1 published to %s, got: %s", retryVer.ID, catalogs.published["songbook-1"])
	}
}

func TestBuilderReclaimsAbandonedBuild(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:      catalogs,
		Assets:        assets,
		Options:       Options{SchemaVersion: 1, ShardSize: 1},
		LeaseDuration: 10 * time.Minute,
	}

	songs := []*models.Song{{Artist: "David Bowie", Title: "Heroes", IsAvailable: true}}
	compiled, err := Compile(songs, builder.Options)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an abandoned build whose lease expired 5 minutes ago
	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  1,
		Status:         models.CatalogVersionBuilding,
		LeaseExpiresAt: time.Now().UTC().Add(-5 * time.Minute),
	}

	reclaimedVer, buildErr := builder.BuildAndPublish(context.Background(), "songbook-abandoned", songs)
	if buildErr != nil {
		t.Fatalf("expected abandoned build reclamation to succeed, got: %v", buildErr)
	}
	if reclaimedVer.Status != models.CatalogVersionReady {
		t.Fatalf("expected reclaimed version to be ready, got: %+v", reclaimedVer)
	}
	if catalogs.published["songbook-abandoned"] != reclaimedVer.ID {
		t.Fatalf("expected songbook published to %s, got: %s", reclaimedVer.ID, catalogs.published["songbook-abandoned"])
	}
}

func TestBuilderConcurrentReusePublishesAllSongbooks(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}

	songs := []*models.Song{{Artist: "Queen", Title: "Somebody to Love", IsAvailable: true}}
	compiled, err := Compile(songs, Options{SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}

	// Worker 1 starts building for songbook-1
	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  1,
		Status:         models.CatalogVersionBuilding,
		LeaseToken:     "worker-1-token",
		LeaseExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}

	builder2 := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 500 * time.Millisecond,
		PollInterval:    10 * time.Millisecond,
	}

	// In background, worker 1 finishes after 30ms
	go func() {
		time.Sleep(30 * time.Millisecond)
		catalogs.mu.Lock()
		v := catalogs.versions[compiled.CatalogID]
		v.Status = models.CatalogVersionReady
		v.AssetPrefix = "catalogs/" + compiled.CatalogID
		v.SongCount = len(songs)
		catalogs.published["songbook-1"] = compiled.CatalogID
		catalogs.mu.Unlock()
	}()

	// Worker 2 builds for songbook-2 with identical content
	ver2, err := builder2.BuildAndPublish(context.Background(), "songbook-2", songs)
	if err != nil {
		t.Fatalf("expected concurrent reuse to succeed, got: %v", err)
	}
	if ver2.ID != compiled.CatalogID || ver2.Status != models.CatalogVersionReady {
		t.Fatalf("unexpected version 2 result: %+v", ver2)
	}

	// BOTH songbook-1 and songbook-2 must be published!
	if catalogs.published["songbook-1"] != compiled.CatalogID {
		t.Fatalf("songbook-1 was not published: %s", catalogs.published["songbook-1"])
	}
	if catalogs.published["songbook-2"] != compiled.CatalogID {
		t.Fatalf("songbook-2 was not published: %s", catalogs.published["songbook-2"])
	}
}

func TestBuilderFailureStatusWriteErrorRecovery(t *testing.T) {
	catalogs := newFakeCatalogStore()
	catalogs.failErr = errors.New("dynamodb timeout when recording failure")
	assets := &fakeAssetStore{err: errors.New("s3 upload dropped")}

	builder := Builder{
		Catalogs:      catalogs,
		Assets:        assets,
		Options:       Options{SchemaVersion: 1},
		LeaseDuration: 50 * time.Millisecond,
	}

	songs := []*models.Song{{Artist: "David Bowie", Title: "Starman", IsAvailable: true}}

	// Build attempt fails on upload AND FailCatalogVersion fails
	_, err := builder.BuildAndPublish(context.Background(), "songbook-crashed", songs)
	if err == nil {
		t.Fatal("expected build failure")
	}

	compiled, _ := Compile(songs, builder.Options)
	// Version was left in building state
	v, _ := catalogs.GetCatalogVersion(context.Background(), compiled.CatalogID)
	if v.Status != models.CatalogVersionBuilding {
		t.Fatalf("expected version in building state, got: %s", v.Status)
	}

	// Wait for lease to expire
	time.Sleep(60 * time.Millisecond)

	// S3 and DynamoDB recover
	assets.err = nil
	catalogs.failErr = nil

	// Retry should detect abandoned build and recover
	recovered, err := builder.BuildAndPublish(context.Background(), "songbook-crashed", songs)
	if err != nil {
		t.Fatalf("expected recovery after crash, got: %v", err)
	}
	if recovered.Status != models.CatalogVersionReady {
		t.Fatalf("expected recovered version to be ready, got: %+v", recovered)
	}
	if catalogs.published["songbook-crashed"] != recovered.ID {
		t.Fatalf("expected songbook-crashed published to %s", recovered.ID)
	}
}

func TestBuilderPropagatesNonConflictReclaimError(t *testing.T) {
	catalogs := newFakeCatalogStore()
	dbErr := errors.New("dynamodb reclaim outage")
	catalogs.reclaimErr = dbErr
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 50 * time.Millisecond,
		PollInterval:    10 * time.Millisecond,
	}

	songs := []*models.Song{{Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true}}
	compiled, err := Compile(songs, builder.Options)
	if err != nil {
		t.Fatal(err)
	}

	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:            compiled.CatalogID,
		SchemaVersion: 1,
		Status:        models.CatalogVersionFailed,
	}

	_, err = builder.BuildAndPublish(context.Background(), "songbook-1", songs)
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
}

func TestBuilderPropagatesNonConflictReclaimErrorDuringPolling(t *testing.T) {
	catalogs := newFakeCatalogStore()
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 200 * time.Millisecond,
		PollInterval:    5 * time.Millisecond,
	}

	songs := []*models.Song{{Artist: "Queen", Title: "Radio Ga Ga", IsAvailable: true}}
	compiled, err := Compile(songs, builder.Options)
	if err != nil {
		t.Fatal(err)
	}

	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  1,
		Status:         models.CatalogVersionBuilding,
		LeaseExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}

	dbErr := errors.New("storage unreachable during polling")
	go func() {
		time.Sleep(15 * time.Millisecond)
		catalogs.mu.Lock()
		catalogs.versions[compiled.CatalogID].Status = models.CatalogVersionFailed
		catalogs.reclaimErr = dbErr
		catalogs.mu.Unlock()
	}()

	_, err = builder.BuildAndPublish(context.Background(), "songbook-1", songs)
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr from polling loop, got %v", err)
	}
}

func TestBuilderFailedBuildReclamationEnforcesDeadlineAcrossContext(t *testing.T) {
	catalogs := newFakeCatalogStore()
	catalogs.reclaimErr = store.ErrReclaimConflict
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 10 * time.Millisecond,
		PollInterval:    2 * time.Millisecond,
	}

	songs := []*models.Song{{Artist: "Queen", Title: "Under Pressure", IsAvailable: true}}
	compiled, err := Compile(songs, builder.Options)
	if err != nil {
		t.Fatal(err)
	}

	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:            compiled.CatalogID,
		SchemaVersion: 1,
		Status:        models.CatalogVersionFailed,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = builder.BuildAndPublish(ctx, "songbook-1", songs)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected build error for failed version with reclaim conflicts, got nil")
	}
	if ctx.Err() != nil {
		t.Fatalf("context expired (%v); wait loop must respect MaxWaitDuration before outer context deadline", ctx.Err())
	}
	if elapsed >= 80*time.Millisecond {
		t.Fatalf("elapsed time %v exceeded expected bound (<80ms) for MaxWaitDuration (10ms) under outer 100ms deadline", elapsed)
	}
}

func TestBuilderDeadlineBranchPropagatesStorageError(t *testing.T) {
	catalogs := newFakeCatalogStore()
	dbErr := errors.New("dynamodb read error on deadline")
	catalogs.getErr = dbErr
	catalogs.getErrAfter = 1
	assets := &fakeAssetStore{}
	builder := Builder{
		Catalogs:        catalogs,
		Assets:          assets,
		Options:         Options{SchemaVersion: 1},
		MaxWaitDuration: 10 * time.Millisecond,
		PollInterval:    1 * time.Second,
	}

	songs := []*models.Song{{Artist: "Queen", Title: "Innuendo", IsAvailable: true}}
	compiled, err := Compile(songs, builder.Options)
	if err != nil {
		t.Fatal(err)
	}

	catalogs.versions[compiled.CatalogID] = &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  1,
		Status:         models.CatalogVersionBuilding,
		LeaseExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}

	_, err = builder.BuildAndPublish(context.Background(), "songbook-1", songs)
	if err == nil {
		t.Fatal("expected build error on deadline storage failure, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
	if errors.Is(err, ErrBuildInProgress) {
		t.Fatalf("error masked as ErrBuildInProgress: %v", err)
	}
}

type fakeAssetStore struct {
	mu     sync.Mutex
	prefix string
	assets []Asset
	err    error
}

func (s *fakeAssetStore) Put(_ context.Context, prefix string, assets []Asset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.prefix, s.assets = prefix, append([]Asset(nil), assets...)
	return nil
}

type fakeCatalogStore struct {
	mu             sync.Mutex
	versions       map[string]*models.CatalogVersion
	published      map[string]string
	failErr        error
	reclaimErr     error
	getErr         error
	getErrAfter    int
	getCalls       int
	publishErr     error
	publishErrFunc func(songbookID, catalogID string, opts ...store.PublicationOptions) error
}

func newFakeCatalogStore() *fakeCatalogStore {
	return &fakeCatalogStore{versions: make(map[string]*models.CatalogVersion), published: make(map[string]string)}
}

func (s *fakeCatalogStore) CreateCatalogVersion(_ context.Context, version *models.CatalogVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getErr != nil && s.getCalls > s.getErrAfter {
		return nil, s.getErr
	}
	version, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
	}
	copy := *version
	return &copy, nil
}

func (s *fakeCatalogStore) MarkCatalogVersionReady(_ context.Context, id, prefix string, count int, leaseToken ...string) (*models.CatalogVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return nil, s.failErr
	}
	version, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reclaimErr != nil {
		return nil, s.reclaimErr
	}
	version, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
	}
	now := time.Now().UTC()
	isExpired := (!version.LeaseExpiresAt.IsZero() && now.After(version.LeaseExpiresAt)) ||
		(version.LeaseExpiresAt.IsZero() && !version.CreatedAt.IsZero() && now.Sub(version.CreatedAt) > leaseDuration)
	if version.Status != models.CatalogVersionFailed && !((version.Status == models.CatalogVersionBuilding || version.Status == models.CatalogVersionDeleting) && isExpired) {
		return nil, store.ErrReclaimConflict
	}
	version.Status = models.CatalogVersionBuilding
	version.FailureReason = ""
	version.LeaseToken = "token-reclaimed-" + time.Now().Format(time.RFC3339Nano)
	version.LeaseExpiresAt = now.Add(leaseDuration)
	s.versions[id] = version
	return version, nil
}

func (s *fakeCatalogStore) PublishCatalogVersion(_ context.Context, songbookID, catalogID string, opts ...store.PublicationOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publishErrFunc != nil {
		if err := s.publishErrFunc(songbookID, catalogID, opts...); err != nil {
			return err
		}
	} else if s.publishErr != nil {
		return s.publishErr
	}
	version, exists := s.versions[catalogID]
	if !exists || version.Status != models.CatalogVersionReady {
		return os.ErrNotExist
	}
	s.published[songbookID] = catalogID
	return nil
}

func (s *fakeCatalogStore) DeleteCatalogVersion(_ context.Context, id string, _ ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.versions, id)
	return nil
}

func (s *fakeCatalogStore) ReserveCatalogVersionForDeletion(_ context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, exists := s.versions[id]
	if !exists {
		return nil, os.ErrNotExist
	}
	now := time.Now().UTC()
	isExpired := (!v.LeaseExpiresAt.IsZero() && now.After(v.LeaseExpiresAt))
	if v.Status != models.CatalogVersionReady && v.Status != models.CatalogVersionFailed && !isExpired {
		return nil, store.ErrCatalogDeletionConflict
	}
	v.Status = models.CatalogVersionDeleting
	v.LeaseToken = "token-deleting-" + time.Now().Format(time.RFC3339Nano)
	v.LeaseExpiresAt = now.Add(leaseDuration)
	return v, nil
}

func (s *fakeCatalogStore) ReleaseCatalogVersionReservation(_ context.Context, id, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, exists := s.versions[id]
	if !exists {
		return os.ErrNotExist
	}
	if v.Status == models.CatalogVersionDeleting {
		v.Status = models.CatalogVersionReady
		v.LeaseToken = ""
	}
	return nil
}

func (s *fakeCatalogStore) IsCatalogVersionReferenced(_ context.Context, _ string, _ ...string) (bool, error) {
	return false, nil
}

func (s *fakeCatalogStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	s.mu.Lock()
	v, exists := s.versions[id]
	s.mu.Unlock()
	if !exists {
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
