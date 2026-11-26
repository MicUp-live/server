package dynamo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func helperCreateAndReadyVersion(t *testing.T, s *Store, versionID string, count int) *models.CatalogVersion {
	t.Helper()
	ctx := context.Background()
	version := &models.CatalogVersion{
		ID:            versionID,
		SchemaVersion: 1,
	}
	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version %s: %v", versionID, err)
	}
	ready, err := s.MarkCatalogVersionReady(ctx, versionID, "catalogs/"+versionID, count)
	if err != nil {
		t.Fatalf("mark catalog version %s ready: %v", versionID, err)
	}
	return ready
}

// TestStore_Publication_CompetingImportsFinishingOutOfOrder tests that if two import jobs
// are dispatched (Job 1 with gen 1, Job 2 with gen 2) and Job 2 finishes and publishes first,
// a delayed Job 1 attempting to publish is rejected by the transaction condition, leaving
// Job 2's catalog active and undisturbed.
func TestStore_Publication_CompetingImportsFinishingOutOfOrder(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	songbook := &models.Songbook{
		ID:   "sb-imports-" + uuid.NewString(),
		Name: "Competing Imports Test",
	}
	helperCreateSongbook(t, s, songbook)

	// Allocate generations for Job 1 and Job 2
	gen1, err := s.NextCatalogGeneration(ctx, songbook.ID)
	if err != nil || gen1 != 1 {
		t.Fatalf("expected gen1 = 1, got %d, err: %v", gen1, err)
	}
	gen2, err := s.NextCatalogGeneration(ctx, songbook.ID)
	if err != nil || gen2 != 2 {
		t.Fatalf("expected gen2 = 2, got %d, err: %v", gen2, err)
	}

	v1ID := "sha256:import-job1-" + uuid.NewString()
	v2ID := "sha256:import-job2-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 100)
	helperCreateAndReadyVersion(t, s, v2ID, 200)

	// Controlled interleaving: Job 2 finishes first and publishes
	err = s.PublishCatalogVersion(ctx, songbook.ID, v2ID, store.PublicationOptions{
		JobGeneration: gen2,
	})
	if err != nil {
		t.Fatalf("job 2 publication failed: %v", err)
	}

	// Verify songbook has version 2 and generation 2
	current, err := s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if current.CatalogVersionID != v2ID || current.CatalogGeneration != 2 || current.SongCount != 200 {
		t.Fatalf("unexpected songbook state after job 2: %+v", current)
	}

	// Controlled interleaving: Delayed Job 1 attempts to publish with generation 1
	err = s.PublishCatalogVersion(ctx, songbook.ID, v1ID, store.PublicationOptions{
		JobGeneration: gen1,
	})
	if err == nil {
		t.Fatal("expected delayed Job 1 publication to be rejected, but it succeeded")
	}
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrStalePublication, got: %v", err)
	}

	// Verify songbook remains at version 2 (Job 1 did NOT overwrite)
	current, err = s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if current.CatalogVersionID != v2ID || current.CatalogGeneration != 2 || current.SongCount != 200 {
		t.Fatalf("songbook was corrupted by stale publication: %+v", current)
	}

	// Verify that a newer Job 3 with generation 3 CAN publish
	gen3, err := s.NextCatalogGeneration(ctx, songbook.ID)
	if err != nil || gen3 != 3 {
		t.Fatalf("expected gen3 = 3, got %d, err: %v", gen3, err)
	}
	v3ID := "sha256:import-job3-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v3ID, 300)
	err = s.PublishCatalogVersion(ctx, songbook.ID, v3ID, store.PublicationOptions{
		JobGeneration: gen3,
	})
	if err != nil {
		t.Fatalf("job 3 publication failed: %v", err)
	}
	current, err = s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if current.CatalogVersionID != v3ID || current.CatalogGeneration != 3 || current.SongCount != 300 {
		t.Fatalf("unexpected songbook state after job 3: %+v", current)
	}
}

// TestStore_Publication_ImportVersusCompaction tests publication fences between
// an active compaction and an import:
//  1. If compaction starts against base version V1, and an import publishes V2 first,
//     compaction's publication with ExpectedBaseVersion=V1 is rejected.
//  2. If compaction publishes V2 first, an additive import expecting base version V1
//     is rejected, but upon reloading V2 and retrying with ExpectedBaseVersion=V2, it succeeds.
func TestStore_Publication_ImportVersusCompaction(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	v1ID := "sha256:base-v1-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 50)

	songbook := &models.Songbook{
		ID:                "sb-compaction-" + uuid.NewString(),
		Name:              "Compaction Interleaving Test",
		CatalogVersionID:  v1ID,
		SongCount:         50,
		CatalogGeneration: 1,
	}
	helperCreateSongbook(t, s, songbook)

	// Case 1: Import wins, compaction loses.
	// Compaction reads base version V1.
	compactionBase := v1ID
	vCompacted := "sha256:compacted-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vCompacted, 55)

	// An import finishes first, publishing V2 with gen 2
	vImport := "sha256:import-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vImport, 150)
	err := s.PublishCatalogVersion(ctx, songbook.ID, vImport, store.PublicationOptions{
		JobGeneration: 2,
	})
	if err != nil {
		t.Fatalf("import publication failed: %v", err)
	}

	// Now compaction tries to publish against base V1
	err = s.PublishCatalogVersion(ctx, songbook.ID, vCompacted, store.PublicationOptions{
		ExpectedBaseVersion: &compactionBase,
	})
	if err == nil {
		t.Fatal("expected compaction to be rejected due to changed base version")
	}
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrStalePublication, got: %v", err)
	}

	// Songbook still points to VImport
	current, err := s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if current.CatalogVersionID != vImport || current.CatalogGeneration != 2 {
		t.Fatalf("songbook state corrupted: %+v", current)
	}

	// Case 2: Compaction reloads new base VImport and retries
	vCompacted2 := "sha256:compacted-retry-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vCompacted2, 155)
	newCompactionBase := vImport
	err = s.PublishCatalogVersion(ctx, songbook.ID, vCompacted2, store.PublicationOptions{
		ExpectedBaseVersion: &newCompactionBase,
	})
	if err != nil {
		t.Fatalf("compaction retry failed: %v", err)
	}

	current, err = s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if current.CatalogVersionID != vCompacted2 || current.CatalogGeneration != 2 {
		t.Fatalf("unexpected songbook state after compaction retry: %+v", current)
	}
}

// TestStore_Publication_OverridesAddedOrChangedDuringCompaction verifies that DeleteCatalogChange
// conditional delete preserves overrides that were modified or added while compaction was running.
func TestStore_Publication_OverridesAddedOrChangedDuringCompaction(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	songbookID := "sb-overrides-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: songbookID, Name: "Overrides SB"})
	t1 := time.Now().UTC().Add(-10 * time.Second)

	// Two pending overrides initially staged
	change1 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     "song-1",
		Song:       &models.Song{ID: "song-1", Artist: "Artist 1", Title: "Title 1"},
		UpdatedAt:  t1,
	}
	change2 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     "song-2",
		Song:       &models.Song{ID: "song-2", Artist: "Artist 2", Title: "Title 2"},
		UpdatedAt:  t1,
	}
	if err := s.PutCatalogChange(ctx, change1); err != nil {
		t.Fatalf("put change1: %v", err)
	}
	if err := s.PutCatalogChange(ctx, change2); err != nil {
		t.Fatalf("put change2: %v", err)
	}

	// Compaction reads the changes
	staged, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil || len(staged) != 2 {
		t.Fatalf("list changes before compaction: %v, count=%d", err, len(staged))
	}

	// Controlled interleaving: While compaction compiles and publishes,
	// user updates song-1 with a newer edit at t2
	t2 := time.Now().UTC()
	change1Updated := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     "song-1",
		Song:       &models.Song{ID: "song-1", Artist: "Artist 1", Title: "Updated Title 1"},
		UpdatedAt:  t2,
	}
	if err := s.PutCatalogChange(ctx, change1Updated); err != nil {
		t.Fatalf("update change1 at t2: %v", err)
	}

	// Also user adds a brand new song-3 at t2
	change3 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     "song-3",
		Song:       &models.Song{ID: "song-3", Artist: "Artist 3", Title: "Title 3"},
		UpdatedAt:  t2,
	}
	if err := s.PutCatalogChange(ctx, change3); err != nil {
		t.Fatalf("put change3 at t2: %v", err)
	}

	// Compaction completes and retires only the changes it read at t1
	for _, ch := range staged {
		if err := s.DeleteCatalogChange(ctx, ch.SongbookID, ch.SongID, ch.UpdatedAt); err != nil {
			t.Fatalf("delete catalog change %s: %v", ch.SongID, err)
		}
	}

	// Verify remaining changes in store
	remaining, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil {
		t.Fatalf("list remaining changes: %v", err)
	}

	// change2 (which had no edits) should be deleted.
	// change1 (which was edited at t2) and change3 (added at t2) MUST be preserved!
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining changes, got %d", len(remaining))
	}
	foundSong1 := false
	foundSong3 := false
	for _, ch := range remaining {
		if ch.SongID == "song-1" {
			foundSong1 = true
			if ch.Song.Title != "Updated Title 1" {
				t.Fatalf("expected song-1 title 'Updated Title 1', got %q", ch.Song.Title)
			}
		}
		if ch.SongID == "song-3" {
			foundSong3 = true
		}
		if ch.SongID == "song-2" {
			t.Fatalf("song-2 should have been deleted by compaction")
		}
	}
	if !foundSong1 || !foundSong3 {
		t.Fatalf("expected song-1 and song-3 to be preserved, found1=%v, found3=%v", foundSong1, foundSong3)
	}
}

// TestStore_Publication_StaleRejectionAndSafeRetry tests that additive imports fencing on
// ExpectedBaseVersion and JobGeneration roll back cleanly on conflict and succeed upon safe retry.
func TestStore_Publication_StaleRejectionAndSafeRetry(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	songbook := &models.Songbook{
		ID:   "sb-retry-" + uuid.NewString(),
		Name: "Retry Test",
	}
	helperCreateSongbook(t, s, songbook)

	// Initial publication (empty base version)
	initialBase := ""
	v1ID := "sha256:init-v1-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 10)

	err := s.PublishCatalogVersion(ctx, songbook.ID, v1ID, store.PublicationOptions{
		ExpectedBaseVersion: &initialBase,
		JobGeneration:       1,
	})
	if err != nil {
		t.Fatalf("initial publish failed: %v", err)
	}

	// Two additive imports start: Import A (gen 2) and Import B (gen 3),
	// both observing base version v1ID.
	baseObserved := v1ID
	vAID := "sha256:import-a-" + uuid.NewString()
	vBID := "sha256:import-b-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vAID, 20)
	helperCreateAndReadyVersion(t, s, vBID, 30)

	// Import A publishes first
	err = s.PublishCatalogVersion(ctx, songbook.ID, vAID, store.PublicationOptions{
		ExpectedBaseVersion: &baseObserved,
		JobGeneration:       2,
	})
	if err != nil {
		t.Fatalf("import A publish failed: %v", err)
	}

	// Import B attempts to publish against base v1ID
	err = s.PublishCatalogVersion(ctx, songbook.ID, vBID, store.PublicationOptions{
		ExpectedBaseVersion: &baseObserved,
		JobGeneration:       3,
	})
	if err == nil {
		t.Fatal("expected import B publish to fail against stale base")
	}
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrStalePublication, got: %v", err)
	}

	// Safe reload & recompute retry for Import B:
	// Reload songbook, discover active catalog is now vAID
	reloaded, err := s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if reloaded.CatalogVersionID != vAID {
		t.Fatalf("expected active catalog vAID, got %s", reloaded.CatalogVersionID)
	}

	// Import B merges with vAID content, produces vBRetry, and publishes with new base
	vBRetry := "sha256:import-b-retry-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vBRetry, 40)
	newBase := vAID
	err = s.PublishCatalogVersion(ctx, songbook.ID, vBRetry, store.PublicationOptions{
		ExpectedBaseVersion: &newBase,
		JobGeneration:       3,
	})
	if err != nil {
		t.Fatalf("import B retry failed: %v", err)
	}

	finalBook, err := s.GetSongbook(ctx, songbook.ID)
	if err != nil {
		t.Fatalf("get final songbook: %v", err)
	}
	if finalBook.CatalogVersionID != vBRetry || finalBook.CatalogGeneration != 3 || finalBook.SongCount != 40 {
		t.Fatalf("unexpected final songbook state: %+v", finalBook)
	}
}

// TestStore_Publication_ReadyVersionReuseAndM12RecoveryFencing tests that ready version reuse
// and M12 reclaimed builds properly enforce publication fencing when assigned to multiple songbooks.
func TestStore_Publication_ReadyVersionReuseAndM12RecoveryFencing(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sb1 := &models.Songbook{ID: "sb-reuse-1-" + uuid.NewString(), Name: "Reuse 1"}
	sb2 := &models.Songbook{ID: "sb-reuse-2-" + uuid.NewString(), Name: "Reuse 2"}
	helperCreateSongbook(t, s, sb1)
	helperCreateSongbook(t, s, sb2)

	// Build a version that initially fails transiently (M12 scenario)
	sharedID := "sha256:shared-m12-" + uuid.NewString()
	version := &models.CatalogVersion{ID: sharedID, SchemaVersion: 1}
	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create version: %v", err)
	}
	_, err := s.FailCatalogVersion(ctx, sharedID, "transient error")
	if err != nil {
		t.Fatalf("fail version: %v", err)
	}

	// Reclaim the version
	reclaimed, err := s.ReclaimCatalogVersion(ctx, sharedID, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim version: %v", err)
	}
	ready, err := s.MarkCatalogVersionReady(ctx, sharedID, "catalogs/"+sharedID, 123, reclaimed.LeaseToken)
	if err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if ready.Status != models.CatalogVersionReady {
		t.Fatalf("expected ready, got: %s", ready.Status)
	}

	// Publish to sb1 with gen 1
	err = s.PublishCatalogVersion(ctx, sb1.ID, sharedID, store.PublicationOptions{JobGeneration: 1})
	if err != nil {
		t.Fatalf("publish to sb1 failed: %v", err)
	}

	// Reuse existing ready version for sb2 with gen 5
	err = s.PublishCatalogVersion(ctx, sb2.ID, sharedID, store.PublicationOptions{JobGeneration: 5})
	if err != nil {
		t.Fatalf("publish reuse to sb2 failed: %v", err)
	}

	// Verify sb1 and sb2 both point to sharedID with independent generations
	cur1, _ := s.GetSongbook(ctx, sb1.ID)
	cur2, _ := s.GetSongbook(ctx, sb2.ID)
	if cur1.CatalogVersionID != sharedID || cur1.CatalogGeneration != 1 {
		t.Fatalf("unexpected sb1 state: %+v", cur1)
	}
	if cur2.CatalogVersionID != sharedID || cur2.CatalogGeneration != 5 {
		t.Fatalf("unexpected sb2 state: %+v", cur2)
	}

	// Verify an older build (gen 3) attempting to publish to sb2 is rejected
	oldVersionID := "sha256:older-sb2-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, oldVersionID, 50)
	err = s.PublishCatalogVersion(ctx, sb2.ID, oldVersionID, store.PublicationOptions{JobGeneration: 3})
	if err == nil {
		t.Fatal("expected older generation publication to be rejected on sb2")
	}
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected ErrStalePublication, got: %v", err)
	}
}

// TestStore_Publication_DistinguishesSupersededFromBaseConflict verifies that
// PublishCatalogVersion distinguishes confirmed generation supersession from base version conflicts.
func TestStore_Publication_DistinguishesSupersededFromBaseConflict(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sb := &models.Songbook{
		ID:                "sb-distinguish-" + uuid.NewString(),
		Name:              "Distinguish Test",
		CatalogGeneration: 5,
	}
	helperCreateSongbook(t, s, sb)

	v1ID := "sha256:dist-v1-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 10)

	// Publish V1 with generation 5
	err := s.PublishCatalogVersion(ctx, sb.ID, v1ID, store.PublicationOptions{
		JobGeneration: 5,
	})
	if err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	vSupersededID := "sha256:dist-sup-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vSupersededID, 10)

	// 1. Attempt to publish with older generation 3 -> ErrPublicationSuperseded
	err = s.PublishCatalogVersion(ctx, sb.ID, vSupersededID, store.PublicationOptions{
		JobGeneration: 3,
	})
	if err == nil {
		t.Fatal("expected superseded publication to fail, got nil")
	}
	if !errors.Is(err, store.ErrPublicationSuperseded) {
		t.Fatalf("expected ErrPublicationSuperseded, got: %v", err)
	}
	if errors.Is(err, store.ErrPublicationBaseConflict) {
		t.Fatalf("did not expect ErrPublicationBaseConflict on generation supersession: %v", err)
	}
	// Also matches generic ErrStalePublication
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected to also match ErrStalePublication, got: %v", err)
	}

	vConflictID := "sha256:dist-conflict-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vConflictID, 10)

	// 2. Attempt to publish with expected base "wrong-base" but matching generation 6 -> ErrPublicationBaseConflict
	wrongBase := "sha256:non-existent-base"
	err = s.PublishCatalogVersion(ctx, sb.ID, vConflictID, store.PublicationOptions{
		ExpectedBaseVersion: &wrongBase,
		JobGeneration:       6,
	})
	if err == nil {
		t.Fatal("expected base version conflict to fail, got nil")
	}
	if !errors.Is(err, store.ErrPublicationBaseConflict) {
		t.Fatalf("expected ErrPublicationBaseConflict, got: %v", err)
	}
	if errors.Is(err, store.ErrPublicationSuperseded) {
		t.Fatalf("did not expect ErrPublicationSuperseded on base version conflict: %v", err)
	}
	// Also matches generic ErrStalePublication
	if !errors.Is(err, store.ErrStalePublication) {
		t.Fatalf("expected to also match ErrStalePublication, got: %v", err)
	}
}

// TestStore_Publication_LegacyJobAndAllocationFailureDoNotPoisonGenerations verifies
// that sequential DynamoDB generation allocation for legacy jobs increments safely without
// timestamps or gaps, and future imports continue to publish without being poisoned.
func TestStore_Publication_LegacyJobAndAllocationFailureDoNotPoisonGenerations(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sb := &models.Songbook{
		ID:   "sb-legacy-poison-" + uuid.NewString(),
		Name: "Legacy Poison Test",
	}
	helperCreateSongbook(t, s, sb)

	// 1. Initial import allocated generation 1
	gen1, err := s.NextCatalogGeneration(ctx, sb.ID)
	if err != nil || gen1 != 1 {
		t.Fatalf("expected gen1 = 1, got %d, err: %v", gen1, err)
	}
	v1ID := "sha256:leg-v1-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 10)
	if err := s.PublishCatalogVersion(ctx, sb.ID, v1ID, store.PublicationOptions{JobGeneration: gen1}); err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	// 2. Legacy job encountered (was queued with generation 0).
	// Allocate sequential generation for it:
	legacyGen, err := s.NextCatalogGeneration(ctx, sb.ID)
	if err != nil || legacyGen != 2 {
		t.Fatalf("expected legacyGen = 2, got %d, err: %v", legacyGen, err)
	}
	v2ID := "sha256:leg-v2-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v2ID, 20)
	if err := s.PublishCatalogVersion(ctx, sb.ID, v2ID, store.PublicationOptions{JobGeneration: legacyGen}); err != nil {
		t.Fatalf("publish v2 failed: %v", err)
	}

	// 3. Next normal import arrives and allocates generation 3:
	gen3, err := s.NextCatalogGeneration(ctx, sb.ID)
	if err != nil || gen3 != 3 {
		t.Fatalf("expected gen3 = 3, got %d, err: %v", gen3, err)
	}
	v3ID := "sha256:leg-v3-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v3ID, 30)
	// Publish job 3: must succeed! If legacy generation had been a timestamp (~1.7e18),
	// songbook generation would be 1.7e18, causing Job 3 (gen 3) to be rejected as superseded!
	if err := s.PublishCatalogVersion(ctx, sb.ID, v3ID, store.PublicationOptions{JobGeneration: gen3}); err != nil {
		t.Fatalf("publish v3 failed (future import was poisoned!): %v", err)
	}

	cur, err := s.GetSongbook(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if cur.CatalogVersionID != v3ID || cur.CatalogGeneration != 3 {
		t.Fatalf("unexpected songbook state: %+v", cur)
	}
}

// TestCatalogPublicationFencing_LegacyJobDurableAssignOnceInterleaving tests the controlled interleaving:
// 1. Two deliveries read the same generation-zero job.
// 2. One assigns its generation (e.g. gen 2).
// 3. A newer import receives a generation (e.g. gen 3) and publishes.
// 4. The delayed duplicate resumes and cannot acquire newer authority or overwrite that import.
func TestCatalogPublicationFencing_LegacyJobDurableAssignOnceInterleaving(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-interleave-" + uuid.NewString()
	sb := &models.Songbook{ID: sbID, Name: "Interleaving Legacy Fencing Test"}
	helperCreateSongbook(t, s, sb)

	// Baseline version at generation 1
	gen1, err := s.NextCatalogGeneration(ctx, sbID)
	if err != nil || gen1 != 1 {
		t.Fatalf("expected gen1 = 1, got %d, err: %v", gen1, err)
	}
	v1ID := "sha256:base-v1-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, v1ID, 10)
	if err := s.PublishCatalogVersion(ctx, sbID, v1ID, store.PublicationOptions{JobGeneration: gen1}); err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	// 1. Create a legacy job with Generation: 0.
	jobID := "legacy-job-" + uuid.NewString()
	legacyJob := &models.ImportJob{
		ID:          jobID,
		SongbookID:  sbID,
		ManifestKey: "imports/" + jobID + ".json",
		Status:      models.ImportJobQueued,
		Generation:  0, // legacy job
	}
	if err := s.CreateImportJob(ctx, legacyJob); err != nil {
		t.Fatalf("create legacy job: %v", err)
	}

	// Two deliveries read the same generation-zero job
	d1Job, err := s.GetImportJob(ctx, jobID)
	if err != nil || d1Job.Generation != 0 {
		t.Fatalf("delivery 1 read failed: gen=%d, err=%v", d1Job.Generation, err)
	}
	d2Job, err := s.GetImportJob(ctx, jobID)
	if err != nil || d2Job.Generation != 0 {
		t.Fatalf("delivery 2 read failed: gen=%d, err=%v", d2Job.Generation, err)
	}

	// 2. Delivery 1 assigns its generation.
	allocatedGen1, err := s.NextCatalogGeneration(ctx, sbID)
	if err != nil || allocatedGen1 != 2 {
		t.Fatalf("expected allocatedGen1 = 2, got %d, err: %v", allocatedGen1, err)
	}
	assignedGen1, err := s.AssignImportJobGeneration(ctx, jobID, allocatedGen1)
	if err != nil || assignedGen1 != 2 {
		t.Fatalf("delivery 1 assign failed: assigned=%d, err=%v", assignedGen1, err)
	}

	// 3. A newer import receives a generation and publishes.
	newerGen, err := s.NextCatalogGeneration(ctx, sbID)
	if err != nil || newerGen != 3 {
		t.Fatalf("expected newerGen = 3, got %d, err: %v", newerGen, err)
	}
	vNewerID := "sha256:newer-v2-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vNewerID, 50)
	if err := s.PublishCatalogVersion(ctx, sbID, vNewerID, store.PublicationOptions{JobGeneration: newerGen}); err != nil {
		t.Fatalf("publish newer version failed: %v", err)
	}

	// Verify songbook is at newer version and generation 3
	sbCheck, err := s.GetSongbook(ctx, sbID)
	if err != nil || sbCheck.CatalogVersionID != vNewerID || sbCheck.CatalogGeneration != 3 {
		t.Fatalf("unexpected songbook state: %+v, err: %v", sbCheck, err)
	}

	// 4. The delayed duplicate (Delivery 2) resumes.
	// 4a. Stale whole-job update with Generation == 0 must NOT clear persisted generation 2.
	d2Job.Status = models.ImportJobProcessing
	d2Job.ProcessedSongs = 5
	if err := s.UpdateImportJob(ctx, d2Job); err != nil {
		t.Fatalf("delivery 2 update failed: %v", err)
	}

	// Verify generation in DynamoDB is still 2 and d2Job.Generation was updated to 2
	d2Reloaded, err := s.GetImportJob(ctx, jobID)
	if err != nil || d2Reloaded.Generation != 2 {
		t.Fatalf("expected generation preserved as 2, got %d, err: %v", d2Reloaded.Generation, err)
	}
	if d2Job.Generation != 2 {
		t.Fatalf("expected d2Job in-memory Generation updated to 2, got %d", d2Job.Generation)
	}

	// 4b. If Delivery 2 attempts to allocate a new generation (e.g. 4), AssignImportJobGeneration
	// must reject replacement and converge on the persisted generation 2.
	leapfrogGen, err := s.NextCatalogGeneration(ctx, sbID)
	if err != nil || leapfrogGen != 4 {
		t.Fatalf("expected leapfrogGen = 4, got %d, err: %v", leapfrogGen, err)
	}
	assignedGen2, err := s.AssignImportJobGeneration(ctx, jobID, leapfrogGen)
	if err != nil {
		t.Fatalf("delivery 2 assign failed: %v", err)
	}
	if assignedGen2 != 2 {
		t.Fatalf("expected delivery 2 to converge on 2, got %d", assignedGen2)
	}

	// 4c. UpdateImportJob with a mismatched generation (4) must be rejected with ErrJobGenerationConflict.
	conflictJob := &models.ImportJob{
		ID:         jobID,
		Generation: leapfrogGen,
	}
	if err := s.UpdateImportJob(ctx, conflictJob); !errors.Is(err, store.ErrJobGenerationConflict) {
		t.Fatalf("expected ErrJobGenerationConflict, got %v", err)
	}

	// 4d. When Delivery 2 attempts to publish its catalog using its assigned generation (2),
	// publication must be rejected as superseded by generation 3.
	vDelayedID := "sha256:delayed-v-" + uuid.NewString()
	helperCreateAndReadyVersion(t, s, vDelayedID, 20)
	pubErr := s.PublishCatalogVersion(ctx, sbID, vDelayedID, store.PublicationOptions{JobGeneration: assignedGen2})
	if !errors.Is(pubErr, store.ErrPublicationSuperseded) {
		t.Fatalf("expected ErrPublicationSuperseded for delayed publication, got %v", pubErr)
	}

	// Verify the songbook still retains newer version and generation 3 untouched!
	sbFinal, err := s.GetSongbook(ctx, sbID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if sbFinal.CatalogVersionID != vNewerID || sbFinal.CatalogGeneration != 3 {
		t.Fatalf("songbook was overwritten by delayed duplicate! state: %+v", sbFinal)
	}
}

func TestStore_UpdateImportJob_PreservesAndProtectsGeneration(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	jobID := "gen-protect-" + uuid.NewString()
	sbID := "sb-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Protect Gen SB"})
	job := &models.ImportJob{
		ID:          jobID,
		SongbookID:  sbID,
		ManifestKey: "imports/" + jobID + ".json",
		Status:      models.ImportJobQueued,
		Generation:  5,
	}
	if err := s.CreateImportJob(ctx, job); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	// 1. Update with Generation: 0 must not clear generation
	updateZero := &models.ImportJob{
		ID:             jobID,
		Status:         models.ImportJobProcessing,
		ProcessedSongs: 10,
		Generation:     0,
	}
	if err := s.UpdateImportJob(ctx, updateZero); err != nil {
		t.Fatalf("update with gen 0 failed: %v", err)
	}
	loaded, err := s.GetImportJob(ctx, jobID)
	if err != nil || loaded.Generation != 5 {
		t.Fatalf("expected generation 5 preserved, got %d, err: %v", loaded.Generation, err)
	}
	if updateZero.Generation != 5 {
		t.Fatalf("expected in-memory generation updated to 5, got %d", updateZero.Generation)
	}

	// 2. Update with matching Generation: 5 succeeds
	updateSame := &models.ImportJob{
		ID:             jobID,
		Status:         models.ImportJobProcessing,
		ProcessedSongs: 20,
		Generation:     5,
	}
	if err := s.UpdateImportJob(ctx, updateSame); err != nil {
		t.Fatalf("update with same gen failed: %v", err)
	}

	// 3. Update with mismatched Generation: 6 fails with ErrJobGenerationConflict
	updateDifferent := &models.ImportJob{
		ID:             jobID,
		Status:         models.ImportJobProcessing,
		ProcessedSongs: 30,
		Generation:     6,
	}
	if err := s.UpdateImportJob(ctx, updateDifferent); !errors.Is(err, store.ErrJobGenerationConflict) {
		t.Fatalf("expected ErrJobGenerationConflict, got %v", err)
	}
}
