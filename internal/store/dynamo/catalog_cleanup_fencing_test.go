package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"micup.live/server/internal/account"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type inMemoryAssetStore struct {
	mu     sync.Mutex
	assets map[string][]byte
}

func newInMemoryAssetStore() *inMemoryAssetStore {
	return &inMemoryAssetStore{assets: make(map[string][]byte)}
}

func (m *inMemoryAssetStore) Put(prefix, path string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.Trim(prefix, "/") + "/" + strings.Trim(path, "/")
	m.assets[key] = append([]byte(nil), data...)
}

func (m *inMemoryAssetStore) Has(prefix, path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.Trim(prefix, "/") + "/" + strings.Trim(path, "/")
	_, ok := m.assets[key]
	return ok
}

func (m *inMemoryAssetStore) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix = strings.Trim(prefix, "/")
	for k := range m.assets {
		if strings.HasPrefix(k, prefix) {
			delete(m.assets, k)
		}
	}
	return nil
}

func (m *inMemoryAssetStore) DeleteKey(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.assets, strings.Trim(key, "/"))
	return nil
}

type dummyCatalogReader struct{}

func (d *dummyCatalogReader) ListSongs(_ context.Context, _, _ string) ([]*models.Song, error) {
	return nil, nil
}

type songbookStoreWithFailure struct {
	store.SongbookStore
	failDeleteID string
	deleteErr    error
}

func (w *songbookStoreWithFailure) DeleteSongbook(ctx context.Context, id string) error {
	if id == w.failDeleteID {
		return w.deleteErr
	}
	return w.SongbookStore.DeleteSongbook(ctx, id)
}

func TestCatalogCleanupSuite(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	t.Run("PublicationBlockedDuringGC", func(t *testing.T) {
		sb := &models.Songbook{ID: "sb-gc-block-" + uuid.NewString(), Name: "GC Block Test"}
		helperCreateSongbook(t, s, sb)

		versionID := "sha256:gc-block-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, versionID, 50)

		res, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("reserve version for deletion: %v", err)
		}
		if res.Status != models.CatalogVersionDeleting {
			t.Fatalf("expected status deleting, got %s", res.Status)
		}
		if res.LeaseToken == "" {
			t.Fatal("expected non-empty lease token")
		}

		pubErr := s.PublishCatalogVersion(ctx, sb.ID, versionID)
		if pubErr == nil {
			t.Fatal("expected publication to fail while version is in deleting status, but it succeeded")
		}
		if !errors.Is(pubErr, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist due to condition failure, got: %v", pubErr)
		}

		fetchedSB, err := s.GetSongbook(ctx, sb.ID)
		if err != nil {
			t.Fatalf("get songbook: %v", err)
		}
		if fetchedSB.CatalogVersionID == versionID {
			t.Fatalf("songbook was published to deleting version: %+v", fetchedSB)
		}

		if err := s.DeleteCatalogVersion(ctx, versionID, res.LeaseToken); err != nil {
			t.Fatalf("delete catalog version: %v", err)
		}

		_, err = s.GetCatalogVersion(ctx, versionID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist for deleted version, got: %v", err)
		}
	})

	t.Run("GCSkipsPublicationCommittedBeforeReservation", func(t *testing.T) {
		sb := &models.Songbook{ID: "sb-gc-rollback-" + uuid.NewString(), Name: "GC Rollback Test"}
		helperCreateSongbook(t, s, sb)

		versionID := "sha256:gc-rollback-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, versionID, 75)

		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("publish catalog version: %v", err)
		}

		assetsPurged := false
		purgeAssets := func(prefix string) error {
			assetsPurged = true
			return nil
		}

		err := s.PurgeCatalogVersion(ctx, versionID, purgeAssets)
		if err == nil {
			t.Fatal("expected PurgeCatalogVersion to fail for referenced version, but got nil")
		}
		if !errors.Is(err, store.ErrCatalogVersionInUse) {
			t.Fatalf("expected ErrCatalogVersionInUse, got: %v", err)
		}
		if assetsPurged {
			t.Fatal("assets were purged even though catalog version is in use by a songbook")
		}

		v, err := s.GetCatalogVersion(ctx, versionID)
		if err != nil {
			t.Fatalf("get catalog version: %v", err)
		}
		if v.Status != models.CatalogVersionReady {
			t.Fatalf("expected status rolled back to ready, got %s", v.Status)
		}
		if v.LeaseToken != "" {
			t.Fatalf("expected lease token cleared after rollback, got %q", v.LeaseToken)
		}

		sb2 := &models.Songbook{ID: "sb2-gc-rollback-" + uuid.NewString(), Name: "GC Rollback Test 2"}
		helperCreateSongbook(t, s, sb2)
		if err := s.PublishCatalogVersion(ctx, sb2.ID, versionID); err != nil {
			t.Fatalf("publish catalog version to sb2 failed: %v", err)
		}
	})

	t.Run("AccountDeletionSharedCatalogPreserved", func(t *testing.T) {
		userA := &models.User{ID: "user-a-" + uuid.NewString(), Email: "usera-" + uuid.NewString() + "@example.com", DisplayName: "User A"}
		userB := &models.User{ID: "user-b-" + uuid.NewString(), Email: "userb-" + uuid.NewString() + "@example.com", DisplayName: "User B"}
		if err := s.CreateUser(ctx, userA); err != nil {
			t.Fatalf("create user A: %v", err)
		}
		if err := s.CreateUser(ctx, userB); err != nil {
			t.Fatalf("create user B: %v", err)
		}

		sbA := &models.Songbook{ID: "sb-a-" + uuid.NewString(), OwnerID: userA.ID, Name: "Songbook A"}
		sbB := &models.Songbook{ID: "sb-b-" + uuid.NewString(), OwnerID: userB.ID, Name: "Songbook B"}
		if err := s.CreateSongbook(ctx, sbA); err != nil {
			t.Fatalf("create songbook A: %v", err)
		}
		if err := s.CreateSongbook(ctx, sbB); err != nil {
			t.Fatalf("create songbook B: %v", err)
		}

		sharedVersionID := "sha256:shared-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, sharedVersionID, 120)

		if err := s.PublishCatalogVersion(ctx, sbA.ID, sharedVersionID); err != nil {
			t.Fatalf("publish to sbA: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sbB.ID, sharedVersionID); err != nil {
			t.Fatalf("publish to sbB: %v", err)
		}

		assetStore := newInMemoryAssetStore()
		assetStore.Put("catalogs/"+sharedVersionID, "manifest.json", []byte(`{"shared": true}`))
		assetStore.Put("catalogs/"+sharedVersionID, "search.json.gz", []byte(`{"shared": true}`))

		svc := account.NewService(
			s, s, s, s, s, s,
			&dummyCatalogReader{},
			s, s, assetStore,
		)

		if err := svc.DeleteAccount(ctx, userA); err != nil {
			t.Fatalf("DeleteAccount failed: %v", err)
		}

		_, err := s.GetUser(ctx, userA.ID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected user A deleted, got err: %v", err)
		}
		_, err = s.GetSongbook(ctx, sbA.ID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected songbook A deleted, got err: %v", err)
		}

		v, err := s.GetCatalogVersion(ctx, sharedVersionID)
		if err != nil {
			t.Fatalf("get shared catalog version: %v", err)
		}
		if v.Status != models.CatalogVersionReady {
			t.Fatalf("expected shared catalog status ready, got %s", v.Status)
		}

		// Verify asset availability: shared assets remain accessible
		if !assetStore.Has("catalogs/"+sharedVersionID, "manifest.json") {
			t.Fatal("shared catalog manifest was purged from asset store!")
		}
		if !assetStore.Has("catalogs/"+sharedVersionID, "search.json.gz") {
			t.Fatal("shared catalog search index was purged from asset store!")
		}

		fetchedSBB, err := s.GetSongbook(ctx, sbB.ID)
		if err != nil {
			t.Fatalf("get songbook B: %v", err)
		}
		if fetchedSBB.CatalogVersionID != sharedVersionID {
			t.Fatalf("songbook B lost catalog reference: %+v", fetchedSBB)
		}
	})

	t.Run("AccountDeletionSoleOwnerPurgesAndBlocksConcurrentPublication", func(t *testing.T) {
		userA := &models.User{ID: "user-sole-" + uuid.NewString(), Email: "usersole-" + uuid.NewString() + "@example.com", DisplayName: "User Sole"}
		if err := s.CreateUser(ctx, userA); err != nil {
			t.Fatalf("create user A: %v", err)
		}

		sbA := &models.Songbook{ID: "sb-sole-" + uuid.NewString(), OwnerID: userA.ID, Name: "Songbook Sole"}
		if err := s.CreateSongbook(ctx, sbA); err != nil {
			t.Fatalf("create songbook A: %v", err)
		}

		soleVersionID := "sha256:sole-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, soleVersionID, 60)

		if err := s.PublishCatalogVersion(ctx, sbA.ID, soleVersionID); err != nil {
			t.Fatalf("publish to sbA: %v", err)
		}

		sbB := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-racing-" + uuid.NewString(), Name: "Racing Songbook"})

		assetStore := newInMemoryAssetStore()
		assetStore.Put("catalogs/"+soleVersionID, "manifest.json", []byte(`{"sole": true}`))

		var racingPubErr error
		racingPublicationExecuted := false

		customPurger := &interleavingAssetStore{
			inMemoryAssetStore: assetStore,
			onDeletePrefix: func(prefix string) {
				if prefix == "catalogs/"+soleVersionID {
					racingPublicationExecuted = true
					racingPubErr = s.PublishCatalogVersion(ctx, sbB.ID, soleVersionID)
				}
			},
		}

		svc := account.NewService(
			s, s, s, s, s, s,
			&dummyCatalogReader{},
			s, s, customPurger,
		)

		if err := svc.DeleteAccount(ctx, userA); err != nil {
			t.Fatalf("DeleteAccount: %v", err)
		}

		if !racingPublicationExecuted {
			t.Fatal("racing publication was not executed during purge window")
		}
		if racingPubErr == nil {
			t.Fatal("racing publication succeeded against deleting catalog version, want failure")
		}
		if !errors.Is(racingPubErr, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist condition failure on racing publication, got: %v", racingPubErr)
		}

		_, err := s.GetCatalogVersion(ctx, soleVersionID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected catalog version deleted, got err: %v", err)
		}

		// Verify asset availability: assets are purged
		if assetStore.Has("catalogs/"+soleVersionID, "manifest.json") {
			t.Fatal("sole catalog assets were not purged from asset store")
		}

		fetchedSBB, err := s.GetSongbook(ctx, sbB.ID)
		if err != nil {
			t.Fatalf("get songbook B: %v", err)
		}
		if fetchedSBB.CatalogVersionID == soleVersionID {
			t.Fatalf("racing songbook B was attached to deleted catalog: %+v", fetchedSBB)
		}
	})

	t.Run("StaleWorkerResumingS3DeletionCannotTouchReclaimedPublishedAssets", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-stale-deleter-" + uuid.NewString(), Name: "Stale Deleter Test"})

		versionID := "sha256:stale-resumer-" + uuid.NewString()
		epoch1Prefix := "catalogs/epochs/" + versionID + "/epoch-1"
		epoch2Prefix := "catalogs/epochs/" + versionID + "/epoch-2"

		assetStore := newInMemoryAssetStore()
		assetStore.Put(epoch1Prefix, "manifest.json", []byte(`{"epoch": 1}`))
		assetStore.Put(epoch1Prefix, "search.json.gz", []byte(`{"epoch": 1}`))

		// Version 1 is marked ready with epoch 1 prefix
		version := &models.CatalogVersion{ID: versionID, SchemaVersion: 1}
		if err := s.CreateCatalogVersion(ctx, version); err != nil {
			t.Fatalf("create catalog version: %v", err)
		}
		if _, err := s.MarkCatalogVersionReady(ctx, versionID, epoch1Prefix, 50); err != nil {
			t.Fatalf("mark ready epoch 1: %v", err)
		}

		// Worker 1 reserves version with very short lease (50ms)
		worker1Res, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("worker 1 reserve: %v", err)
		}

		// Worker 1 is paused before deleting S3 assets.
		// Wait for Worker 1's lease to expire.
		time.Sleep(100 * time.Millisecond)

		// Builder comes along, reclaims the version, uploads to epoch 2, marks ready, and publishes
		reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("builder reclaim: %v", err)
		}
		assetStore.Put(epoch2Prefix, "manifest.json", []byte(`{"epoch": 2}`))
		assetStore.Put(epoch2Prefix, "search.json.gz", []byte(`{"epoch": 2}`))

		ready, err := s.MarkCatalogVersionReady(ctx, versionID, epoch2Prefix, 50, reclaimed.LeaseToken)
		if err != nil {
			t.Fatalf("builder mark ready: %v", err)
		}
		if ready.AssetPrefix != epoch2Prefix {
			t.Fatalf("expected ready prefix %s, got %s", epoch2Prefix, ready.AssetPrefix)
		}

		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("builder publish: %v", err)
		}

		// Now Worker 1 RESUMES its stale S3 deletion!
		// Worker 1 deletes what it was assigned (epoch1Prefix)
		if err := assetStore.DeletePrefix(ctx, epoch1Prefix); err != nil {
			t.Fatalf("worker 1 delete prefix: %v", err)
		}
		// Worker 1 attempts to commit metadata deletion in DynamoDB -> rejected!
		commitErr := s.DeleteCatalogVersion(ctx, versionID, worker1Res.LeaseToken)
		if commitErr == nil {
			t.Fatal("expected stale commit to be rejected, but it succeeded")
		}
		if !errors.Is(commitErr, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected ErrCatalogDeletionConflict for stale worker commit, got: %v", commitErr)
		}

		// CRITICAL ASSET AVAILABILITY VERIFICATION:
		// Epoch 1 assets are gone (deleted by Worker 1)
		if assetStore.Has(epoch1Prefix, "manifest.json") {
			t.Fatal("epoch 1 manifest was not deleted")
		}
		// Epoch 2 assets (newly published) MUST REMAIN 100% AVAILABLE AND INTACT!
		if !assetStore.Has(epoch2Prefix, "manifest.json") {
			t.Fatal("CRITICAL BUG: newly published epoch 2 manifest was deleted by stale worker!")
		}
		if !assetStore.Has(epoch2Prefix, "search.json.gz") {
			t.Fatal("CRITICAL BUG: newly published epoch 2 search index was deleted by stale worker!")
		}

		// Verify DynamoDB state is undisturbed
		currentV, err := s.GetCatalogVersion(ctx, versionID)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		if currentV.Status != models.CatalogVersionReady || currentV.AssetPrefix != epoch2Prefix {
			t.Fatalf("catalog version corrupted: %+v", currentV)
		}

		currentSB, err := s.GetSongbook(ctx, sb.ID)
		if err != nil {
			t.Fatalf("get songbook: %v", err)
		}
		if currentSB.CatalogVersionID != versionID {
			t.Fatalf("songbook lost reference: %+v", currentSB)
		}
	})

	t.Run("PausedLegacyPrefixDeleterResumesCannotTouchNewEpochAssets", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-legacy-deleter-" + uuid.NewString(), Name: "Legacy Deleter Test"})

		versionID := "sha256:legacy-race-" + uuid.NewString()
		legacyPrefix := "catalogs/" + versionID
		newEpochPrefix := "catalogs/epochs/" + versionID + "/epoch-recovered"

		assetStore := newInMemoryAssetStore()
		assetStore.Put(legacyPrefix, "manifest.json", []byte(`{"legacy": true}`))
		assetStore.Put(legacyPrefix, "search.json.gz", []byte(`{"legacy": true}`))
		assetStore.Put(legacyPrefix+"/letters", "a.json", []byte(`{"letter": "a"}`))

		// Version starts as a legacy catalog pointing to catalogs/<id>
		version := &models.CatalogVersion{ID: versionID, SchemaVersion: 1}
		if err := s.CreateCatalogVersion(ctx, version); err != nil {
			t.Fatalf("create catalog version: %v", err)
		}
		if _, err := s.MarkCatalogVersionReady(ctx, versionID, legacyPrefix, 50); err != nil {
			t.Fatalf("mark ready legacy: %v", err)
		}

		// Legacy deleter Worker 1 reserves version with short lease (50ms)
		worker1Res, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("worker 1 reserve: %v", err)
		}

		// Worker 1 pauses before deleting S3 assets.
		// Wait for Worker 1's lease to expire.
		time.Sleep(100 * time.Millisecond)

		// Builder comes along, reclaims the version, uploads to new disjoint epoch namespace, marks ready, and publishes
		reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("builder reclaim: %v", err)
		}
		assetStore.Put(newEpochPrefix, "manifest.json", []byte(`{"epoch": "recovered"}`))
		assetStore.Put(newEpochPrefix, "search.json.gz", []byte(`{"epoch": "recovered"}`))

		ready, err := s.MarkCatalogVersionReady(ctx, versionID, newEpochPrefix, 50, reclaimed.LeaseToken)
		if err != nil {
			t.Fatalf("builder mark ready: %v", err)
		}
		if ready.AssetPrefix != newEpochPrefix {
			t.Fatalf("expected ready prefix %s, got %s", newEpochPrefix, ready.AssetPrefix)
		}

		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("builder publish: %v", err)
		}

		// Now legacy Worker 1 RESUMES its stale S3 deletion!
		// Worker 1 deletes its assigned legacy prefix (catalogs/<id>)
		if err := assetStore.DeletePrefix(ctx, legacyPrefix); err != nil {
			t.Fatalf("worker 1 delete prefix: %v", err)
		}
		// Worker 1 attempts to commit metadata deletion in DynamoDB -> rejected!
		commitErr := s.DeleteCatalogVersion(ctx, versionID, worker1Res.LeaseToken)
		if commitErr == nil {
			t.Fatal("expected stale legacy commit to be rejected, but it succeeded")
		}
		if !errors.Is(commitErr, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected ErrCatalogDeletionConflict for stale legacy worker commit, got: %v", commitErr)
		}

		// CRITICAL ASSET AVAILABILITY VERIFICATION:
		// Legacy assets are gone (deleted by Worker 1)
		if assetStore.Has(legacyPrefix, "manifest.json") {
			t.Fatal("legacy manifest was not deleted")
		}
		if assetStore.Has(legacyPrefix+"/letters", "a.json") {
			t.Fatal("legacy letter shard was not deleted")
		}

		// EVERY NEW ASSET UNDER catalogs/epochs/<id>/... MUST SURVIVE AND REMAIN 100% INTACT!
		if !assetStore.Has(newEpochPrefix, "manifest.json") {
			t.Fatal("CRITICAL BUG: newly published epoch manifest was deleted by stale legacy worker!")
		}
		if !assetStore.Has(newEpochPrefix, "search.json.gz") {
			t.Fatal("CRITICAL BUG: newly published epoch search index was deleted by stale legacy worker!")
		}

		// Verify DynamoDB state is undisturbed and points to the new epoch prefix
		currentV, err := s.GetCatalogVersion(ctx, versionID)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		if currentV.Status != models.CatalogVersionReady || currentV.AssetPrefix != newEpochPrefix {
			t.Fatalf("catalog version corrupted: %+v", currentV)
		}

		currentSB, err := s.GetSongbook(ctx, sb.ID)
		if err != nil {
			t.Fatalf("get songbook: %v", err)
		}
		if currentSB.CatalogVersionID != versionID {
			t.Fatalf("songbook lost reference: %+v", currentSB)
		}
	})

	t.Run("ReferencedLegacyCatalogProtectsLetterShardsFromDirectPurge", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-legacy-letters-" + uuid.NewString(), Name: "Legacy Letters Protection Test"})

		versionID := "sha256:legacy-protect-" + uuid.NewString()
		legacyPrefix := "catalogs/" + versionID
		lettersPrefix := legacyPrefix + "/letters"

		assetStore := newInMemoryAssetStore()
		assetStore.Put(legacyPrefix, "manifest.json", []byte(`{"legacy": true}`))
		assetStore.Put(legacyPrefix, "search.json.gz", []byte(`{"legacy": true}`))
		assetStore.Put(lettersPrefix, "a.json", []byte(`{"letter": "a"}`))
		assetStore.Put(lettersPrefix, "b.json", []byte(`{"letter": "b"}`))

		version := &models.CatalogVersion{ID: versionID, SchemaVersion: 1}
		if err := s.CreateCatalogVersion(ctx, version); err != nil {
			t.Fatalf("create catalog version: %v", err)
		}
		if _, err := s.MarkCatalogVersionReady(ctx, versionID, legacyPrefix, 50); err != nil {
			t.Fatalf("mark ready: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("publish: %v", err)
		}

		// Attempt to directly purge the "letters" sub-prefix as if it were an orphan
		purgeCalled := false
		err := s.PurgeOrphanCatalogPrefix(ctx, lettersPrefix, func(prefix string) error {
			purgeCalled = true
			return assetStore.DeletePrefix(ctx, prefix)
		})

		// Purge MUST be rejected because the parent catalog is referenced!
		if err == nil {
			t.Fatal("expected PurgeOrphanCatalogPrefix to fail for letters shard of referenced legacy catalog, but got nil")
		}
		if !errors.Is(err, store.ErrCatalogVersionInUse) {
			t.Fatalf("expected ErrCatalogVersionInUse, got: %v", err)
		}
		if purgeCalled {
			t.Fatal("purgeAssets was called on letters shard of referenced legacy catalog!")
		}

		// Verify asset availability: all manifest, search, and letter shards survive!
		if !assetStore.Has(legacyPrefix, "manifest.json") {
			t.Fatal("legacy manifest was purged!")
		}
		if !assetStore.Has(legacyPrefix, "search.json.gz") {
			t.Fatal("legacy search was purged!")
		}
		if !assetStore.Has(lettersPrefix, "a.json") {
			t.Fatal("legacy letter shard a.json was purged!")
		}
		if !assetStore.Has(lettersPrefix, "b.json") {
			t.Fatal("legacy letter shard b.json was purged!")
		}
	})

	t.Run("RaceOrphanPrefixDeletionWithRebuilding", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-orphan-race-" + uuid.NewString(), Name: "Orphan Race Test"})

		orphanID := "sha256:orphan-v1-" + uuid.NewString()
		orphanPrefix := "catalogs/epochs/" + orphanID + "/epoch-orphan"
		newPrefix := "catalogs/epochs/" + orphanID + "/epoch-new"

		assetStore := newInMemoryAssetStore()
		assetStore.Put(orphanPrefix, "manifest.json", []byte(`{"orphan": true}`))

		var builderCreateErr, builderPubErr error
		builderAttempted := false

		// GC purges orphan prefix with hook during asset deletion
		err := s.PurgeOrphanCatalogPrefix(ctx, orphanPrefix, func(prefix string) error {
			builderAttempted = true
			// Concurrent builder tries to build for this catalog ID while GC is holding deletion reservation
			builderCreateErr = s.CreateCatalogVersion(ctx, &models.CatalogVersion{
				ID:            orphanID,
				SchemaVersion: 1,
			})
			builderPubErr = s.PublishCatalogVersion(ctx, sb.ID, orphanID)

			return assetStore.DeletePrefix(ctx, prefix)
		})
		if err != nil {
			t.Fatalf("PurgeOrphanCatalogPrefix failed: %v", err)
		}

		if !builderAttempted {
			t.Fatal("builder race was not executed")
		}
		// Builder was fenced: CreateCatalogVersion failed with os.ErrExist
		if !errors.Is(builderCreateErr, os.ErrExist) {
			t.Fatalf("expected os.ErrExist for concurrent builder create during orphan purge, got: %v", builderCreateErr)
		}
		// Builder was fenced: PublishCatalogVersion failed with os.ErrNotExist
		if !errors.Is(builderPubErr, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist for concurrent builder publish during orphan purge, got: %v", builderPubErr)
		}

		// Orphan assets are gone
		if assetStore.Has(orphanPrefix, "manifest.json") {
			t.Fatal("orphan assets were not deleted")
		}

		// After GC finishes, builder retry succeeds
		newVersion := &models.CatalogVersion{ID: orphanID, SchemaVersion: 1}
		if err := s.CreateCatalogVersion(ctx, newVersion); err != nil {
			t.Fatalf("builder retry create failed: %v", err)
		}
		assetStore.Put(newPrefix, "manifest.json", []byte(`{"new": true}`))
		if _, err := s.MarkCatalogVersionReady(ctx, orphanID, newPrefix, 10); err != nil {
			t.Fatalf("builder retry mark ready: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sb.ID, orphanID); err != nil {
			t.Fatalf("builder retry publish: %v", err)
		}

		// Verify asset availability: new assets are present
		if !assetStore.Has(newPrefix, "manifest.json") {
			t.Fatal("new builder assets are not available in asset store")
		}

		// Scenario B: Active builder in progress rejects orphan deletion
		activeID := "sha256:active-b-" + uuid.NewString()
		activeVersion := &models.CatalogVersion{
			ID:             activeID,
			SchemaVersion:  1,
			LeaseExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		}
		if err := s.CreateCatalogVersion(ctx, activeVersion); err != nil {
			t.Fatalf("create active version: %v", err)
		}
		activePrefix := "catalogs/epochs/" + activeID + "/epoch-active"
		assetStore.Put(activePrefix, "manifest.json", []byte(`{"active": true}`))

		conflictErr := s.PurgeOrphanCatalogPrefix(ctx, activePrefix, func(prefix string) error {
			return assetStore.DeletePrefix(ctx, prefix)
		})
		if !errors.Is(conflictErr, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected ErrCatalogDeletionConflict for active building prefix, got: %v", conflictErr)
		}
		// Active assets were NOT deleted
		if !assetStore.Has(activePrefix, "manifest.json") {
			t.Fatal("active building assets were deleted by orphan purge!")
		}
	})

	t.Run("AccountDeletionSongbookFailurePreservesLiveAssets", func(t *testing.T) {
		user := &models.User{ID: "user-inj-" + uuid.NewString(), Email: "userinj-" + uuid.NewString() + "@example.com", DisplayName: "User Inj"}
		if err := s.CreateUser(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}

		sb := &models.Songbook{ID: "sb-inj-" + uuid.NewString(), OwnerID: user.ID, Name: "Songbook Inj"}
		if err := s.CreateSongbook(ctx, sb); err != nil {
			t.Fatalf("create songbook: %v", err)
		}

		versionID := "sha256:inj-" + uuid.NewString()
		prefix := "catalogs/epochs/" + versionID + "/epoch-inj"
		version := &models.CatalogVersion{ID: versionID, SchemaVersion: 1}
		if err := s.CreateCatalogVersion(ctx, version); err != nil {
			t.Fatalf("create catalog version: %v", err)
		}
		if _, err := s.MarkCatalogVersionReady(ctx, versionID, prefix, 50); err != nil {
			t.Fatalf("mark ready: %v", err)
		}
		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("publish: %v", err)
		}

		assetStore := newInMemoryAssetStore()
		assetStore.Put(prefix, "manifest.json", []byte(`{"inj": true}`))
		assetStore.Put(prefix, "search.json.gz", []byte(`{"inj": true}`))

		// Wrap songbook store to inject failure into DeleteSongbook
		failingSBStore := &songbookStoreWithFailure{
			SongbookStore: s,
			failDeleteID:  sb.ID,
			deleteErr:     errors.New("simulated dynamodb timeout during songbook deletion"),
		}

		svc := account.NewService(
			s, s, failingSBStore, s, s, s,
			&dummyCatalogReader{},
			s, s, assetStore,
		)

		err := svc.DeleteAccount(ctx, user)
		if err == nil {
			t.Fatal("expected DeleteAccount to fail due to injected songbook deletion error, got nil")
		}
		var cascadeErr *account.CascadeError
		if !errors.As(err, &cascadeErr) || cascadeErr.Step != "delete_songbooks" {
			t.Fatalf("expected CascadeError on delete_songbooks, got: %v", err)
		}

		// VERIFY ASSET AVAILABILITY:
		// Because songbook deletion failed, NO catalog assets were touched!
		if !assetStore.Has(prefix, "manifest.json") {
			t.Fatal("CRITICAL BUG: catalog manifest was purged despite songbook deletion failure!")
		}
		if !assetStore.Has(prefix, "search.json.gz") {
			t.Fatal("CRITICAL BUG: catalog search index was purged despite songbook deletion failure!")
		}

		// Verify catalog version remains ready in DynamoDB
		v, err := s.GetCatalogVersion(ctx, versionID)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		if v.Status != models.CatalogVersionReady {
			t.Fatalf("expected catalog status ready, got: %s", v.Status)
		}

		// Verify songbook remains in DynamoDB and still points to the intact catalog
		fetchedSB, err := s.GetSongbook(ctx, sb.ID)
		if err != nil {
			t.Fatalf("get songbook: %v", err)
		}
		if fetchedSB.CatalogVersionID != versionID {
			t.Fatalf("songbook lost catalog version: %+v", fetchedSB)
		}
	})

	t.Run("PartialS3FailureLeavesVersionInDeletingForRecovery", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-partial-" + uuid.NewString(), Name: "Partial Test"})

		versionID := "sha256:partial-s3-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, versionID, 80)

		s3Err := errors.New("simulated S3 transient error")
		purgeErr := s.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
			return s3Err
		}, store.PurgeCatalogOptions{LeaseDuration: 5 * time.Minute})

		if purgeErr == nil || !errors.Is(purgeErr, s3Err) {
			t.Fatalf("expected S3 error, got: %v", purgeErr)
		}

		v, err := s.GetCatalogVersion(ctx, versionID)
		if err != nil {
			t.Fatalf("get catalog version: %v", err)
		}
		if v.Status != models.CatalogVersionDeleting {
			t.Fatalf("expected status deleting, got %s", v.Status)
		}
		if v.LeaseToken == "" {
			t.Fatal("expected lease token to remain intact")
		}

		pubErr := s.PublishCatalogVersion(ctx, sb.ID, versionID)
		if pubErr == nil {
			t.Fatal("expected publication to fail while version is deleting")
		}

		conflictErr := s.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
			return nil
		}, store.PurgeCatalogOptions{LeaseDuration: 5 * time.Minute})
		if !errors.Is(conflictErr, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected ErrCatalogDeletionConflict during active lease, got: %v", conflictErr)
		}

		// Explicitly expire the owned lease in the test fixture
		past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        aws.String(s.tables.catalogs),
			Key:              stringKey("id", versionID),
			UpdateExpression: aws.String("SET lease_expires_at = :expired"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":expired": avString(past),
			},
		})
		if err != nil {
			t.Fatalf("expire lease in fixture: %v", err)
		}

		assetsCleaned := false
		reclaimPurgeErr := s.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
			assetsCleaned = true
			return nil
		}, store.PurgeCatalogOptions{LeaseDuration: 5 * time.Minute})

		if reclaimPurgeErr != nil {
			t.Fatalf("reclaimed purge failed: %v", reclaimPurgeErr)
		}
		if !assetsCleaned {
			t.Fatal("expected asset purge callback to execute on retry")
		}

		_, err = s.GetCatalogVersion(ctx, versionID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected catalog version deleted after retry, got: %v", err)
		}
	})

	t.Run("WorkerCrashAndReclaimInterruptedDeletion", func(t *testing.T) {
		versionID := "sha256:crash-worker-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, versionID, 90)

		worker1Res, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("worker 1 reserve: %v", err)
		}

		_, err = s.ReserveCatalogVersionForDeletion(ctx, versionID, 5*time.Minute)
		if !errors.Is(err, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected conflict while worker 1 lease active, got: %v", err)
		}

		// Explicitly expire worker 1's lease in the test fixture
		past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        aws.String(s.tables.catalogs),
			Key:              stringKey("id", versionID),
			UpdateExpression: aws.String("SET lease_expires_at = :expired"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":expired": avString(past),
			},
		})
		if err != nil {
			t.Fatalf("expire lease in fixture: %v", err)
		}

		worker2Res, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("worker 2 reserve after lease expiry: %v", err)
		}
		if worker2Res.LeaseToken == worker1Res.LeaseToken {
			t.Fatal("worker 2 received identical lease token")
		}

		zombieErr := s.DeleteCatalogVersion(ctx, versionID, worker1Res.LeaseToken)
		if zombieErr == nil {
			t.Fatal("expected zombie delete with stale token to be rejected, but it succeeded")
		}
		if !errors.Is(zombieErr, store.ErrCatalogDeletionConflict) {
			t.Fatalf("expected ErrCatalogDeletionConflict for stale lease token, got: %v", zombieErr)
		}

		if err := s.DeleteCatalogVersion(ctx, versionID, worker2Res.LeaseToken); err != nil {
			t.Fatalf("worker 2 delete failed: %v", err)
		}

		_, err = s.GetCatalogVersion(ctx, versionID)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected catalog deleted, got: %v", err)
		}
	})

	t.Run("ReclaimAbandonedDeletingVersionByBuilder", func(t *testing.T) {
		sb := helperCreateSongbook(t, s, &models.Songbook{ID: "sb-builder-reclaim-" + uuid.NewString(), Name: "Builder Reclaim Test"})

		versionID := "sha256:builder-reclaim-" + uuid.NewString()
		helperCreateAndReadyVersion(t, s, versionID, 40)

		_, err := s.ReserveCatalogVersionForDeletion(ctx, versionID, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("reserve version: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
		if err != nil {
			t.Fatalf("reclaim deleting version: %v", err)
		}
		if reclaimed.Status != models.CatalogVersionBuilding {
			t.Fatalf("expected status building after reclaim, got %s", reclaimed.Status)
		}

		ready, err := s.MarkCatalogVersionReady(ctx, versionID, "catalogs/"+versionID+"/reclaim-token", 40, reclaimed.LeaseToken)
		if err != nil {
			t.Fatalf("mark catalog ready: %v", err)
		}
		if ready.Status != models.CatalogVersionReady {
			t.Fatalf("expected status ready, got %s", ready.Status)
		}

		if err := s.PublishCatalogVersion(ctx, sb.ID, versionID); err != nil {
			t.Fatalf("publish reclaimed version: %v", err)
		}

		fetchedSB, err := s.GetSongbook(ctx, sb.ID)
		if err != nil {
			t.Fatalf("get songbook: %v", err)
		}
		if fetchedSB.CatalogVersionID != versionID {
			t.Fatalf("songbook was not updated to reclaimed version: %+v", fetchedSB)
		}
	})
}

type interleavingAssetStore struct {
	*inMemoryAssetStore
	onDeletePrefix func(prefix string)
}

func (i *interleavingAssetStore) DeletePrefix(ctx context.Context, prefix string) error {
	if i.onDeletePrefix != nil {
		i.onDeletePrefix(prefix)
	}
	return i.inMemoryAssetStore.DeletePrefix(ctx, prefix)
}

func TestCatalogChanges_ConsistentPagination_MultiPage(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	user := helperCreateUser(t, s, "changes-pagination@example.com")
	sb := helperCreateSongbook(t, s, &models.Songbook{
		ID:      "sb-changes-paginated-" + uuid.NewString(),
		OwnerID: user.ID,
		Name:    "Paginated Changes Book",
	})

	// Insert 25 changes each with ~60KB song data (~1.5MB total).
	// DynamoDB Query has a strict 1MB response size limit, forcing pagination into multiple pages.
	const count = 25
	largePayload := strings.Repeat("A", 60*1024)
	for i := 0; i < count; i++ {
		songID := fmt.Sprintf("song-change-%03d", i)
		err := s.PutCatalogChange(ctx, &models.CatalogChange{
			SongbookID: sb.ID,
			SongID:     songID,
			Song: &models.Song{
				ID:         songID,
				SongbookID: sb.ID,
				Artist:     fmt.Sprintf("Artist %d", i),
				Title:      fmt.Sprintf("Title %d", i),
				SearchText: largePayload,
			},
			UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("put catalog change %d: %v", i, err)
		}
	}

	// Verify that a single unpaginated Query with DynamoDB limit returns fewer than 25 items due to the 1MB limit.
	rawQuery, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.changes),
		KeyConditionExpression: aws.String("songbook_id = :book"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":book": avString(sb.ID),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("raw query changes: %v", err)
	}
	if len(rawQuery.LastEvaluatedKey) == 0 {
		t.Fatalf("expected raw query to hit 1MB page limit and return LastEvaluatedKey")
	}
	if len(rawQuery.Items) >= count {
		t.Fatalf("expected raw single-page query to return fewer than %d items, got %d", count, len(rawQuery.Items))
	}

	// Verify that ListCatalogChanges (which uses dynamodb.NewQueryPaginator with ConsistentRead: true)
	// traverses all pages and retrieves all 25 changes without dropping any.
	changes, err := s.ListCatalogChanges(ctx, sb.ID)
	if err != nil {
		t.Fatalf("ListCatalogChanges: %v", err)
	}
	if len(changes) != count {
		t.Fatalf("expected all %d changes from paginated query, got %d", count, len(changes))
	}
}
