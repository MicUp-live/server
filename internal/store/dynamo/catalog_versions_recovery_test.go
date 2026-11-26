package dynamo

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestStore_ReclaimFailedCatalogVersionSucceeds(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:failed-recovery-" + uuid.NewString()
	version := &models.CatalogVersion{
		ID:            versionID,
		SchemaVersion: 1,
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}

	failed, err := s.FailCatalogVersion(ctx, versionID, "transient upload network timeout")
	if err != nil {
		t.Fatalf("fail catalog version: %v", err)
	}
	if failed.Status != models.CatalogVersionFailed {
		t.Fatalf("expected status failed, got: %s", failed.Status)
	}

	// Reclaim the failed version
	reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim catalog version: %v", err)
	}
	if reclaimed.Status != models.CatalogVersionBuilding {
		t.Fatalf("expected status building after reclaim, got: %s", reclaimed.Status)
	}
	if reclaimed.FailureReason != "" {
		t.Fatalf("expected failure reason cleared, got: %q", reclaimed.FailureReason)
	}
	if reclaimed.LeaseToken == "" {
		t.Fatal("expected new lease token")
	}
	if reclaimed.LeaseExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("expected lease expiration in future, got: %v", reclaimed.LeaseExpiresAt)
	}

	// Mark ready with the reclaimed lease token
	ready, err := s.MarkCatalogVersionReady(ctx, versionID, "catalogs/"+versionID, 100, reclaimed.LeaseToken)
	if err != nil {
		t.Fatalf("mark catalog ready: %v", err)
	}
	if ready.Status != models.CatalogVersionReady || ready.SongCount != 100 {
		t.Fatalf("unexpected ready version: %+v", ready)
	}
}

func TestStore_ReclaimAbandonedCatalogVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:abandoned-" + uuid.NewString()
	expired := time.Now().UTC().Add(-10 * time.Minute)
	version := &models.CatalogVersion{
		ID:             versionID,
		SchemaVersion:  1,
		LeaseExpiresAt: expired,
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}

	// Reclaim the abandoned build
	reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim abandoned build: %v", err)
	}
	if reclaimed.Status != models.CatalogVersionBuilding {
		t.Fatalf("expected building status, got: %s", reclaimed.Status)
	}
	if reclaimed.LeaseExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("expected fresh lease expiration, got: %v", reclaimed.LeaseExpiresAt)
	}
}

func TestStore_ReclaimActiveCatalogVersionRejects(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:active-lease-" + uuid.NewString()
	future := time.Now().UTC().Add(10 * time.Minute)
	version := &models.CatalogVersion{
		ID:             versionID,
		SchemaVersion:  1,
		LeaseExpiresAt: future,
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}

	// Reclaim while active build holds a valid lease must be rejected
	_, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
	if !errors.Is(err, store.ErrReclaimConflict) {
		t.Fatalf("expected ErrReclaimConflict, got: %v", err)
	}
}

func TestStore_ReclaimReadyCatalogVersionRejects(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:ready-ver-" + uuid.NewString()
	version := &models.CatalogVersion{
		ID:            versionID,
		SchemaVersion: 1,
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}
	if _, err := s.MarkCatalogVersionReady(ctx, versionID, "catalogs/"+versionID, 50); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	// Ready catalog version cannot be reclaimed
	_, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
	if !errors.Is(err, store.ErrReclaimConflict) {
		t.Fatalf("expected ErrReclaimConflict, got: %v", err)
	}
}

func TestStore_ReclaimConcurrentExclusive(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:concurrent-reclaim-" + uuid.NewString()
	version := &models.CatalogVersion{
		ID:            versionID,
		SchemaVersion: 1,
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}
	if _, err := s.FailCatalogVersion(ctx, versionID, "simulated failure"); err != nil {
		t.Fatalf("fail catalog version: %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	var successCount int32
	var conflictCount int32

	barrier := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			_, err := s.ReclaimCatalogVersion(ctx, versionID, 5*time.Minute)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			} else if errors.Is(err, store.ErrReclaimConflict) {
				atomic.AddInt32(&conflictCount, 1)
			}
		}()
	}

	close(barrier)
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 reclaim winner, got %d", successCount)
	}
	if conflictCount != concurrency-1 {
		t.Fatalf("expected %d conflict errors, got %d", concurrency-1, conflictCount)
	}
}

func TestStore_StaleLeaseTokenCannotFailOrMarkReady(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	versionID := "sha256:stale-token-" + uuid.NewString()
	version := &models.CatalogVersion{
		ID:             versionID,
		SchemaVersion:  1,
		LeaseToken:     "worker-1-token",
		LeaseExpiresAt: time.Now().UTC().Add(-1 * time.Minute), // expired
	}

	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}

	// Worker 2 reclaims the expired version
	reclaimed, err := s.ReclaimCatalogVersion(ctx, versionID, 10*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.LeaseToken == "worker-1-token" {
		t.Fatal("expected new lease token for worker 2")
	}

	// Worker 1 wakes up with stale lease token and attempts to fail the version: must be rejected!
	_, err = s.FailCatalogVersion(ctx, versionID, "worker 1 late failure", "worker-1-token")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist on stale token failure, got: %v", err)
	}

	// Worker 1 attempts to mark ready with stale lease token: must be rejected!
	_, err = s.MarkCatalogVersionReady(ctx, versionID, "catalogs/stale", 10, "worker-1-token")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist on stale token mark ready, got: %v", err)
	}

	// Worker 2 completes with valid lease token: must succeed!
	ready, err := s.MarkCatalogVersionReady(ctx, versionID, "catalogs/valid", 20, reclaimed.LeaseToken)
	if err != nil {
		t.Fatalf("expected worker 2 mark ready to succeed, got: %v", err)
	}
	if ready.Status != models.CatalogVersionReady || ready.SongCount != 20 {
		t.Fatalf("unexpected ready version: %+v", ready)
	}
}
