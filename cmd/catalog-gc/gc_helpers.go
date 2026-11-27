package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type gcTarget struct {
	store  gcStore
	s3c    s3Client
	bucket string
	dryRun bool
	cutoff time.Time
	grace  time.Duration
}

func (t gcTarget) pruneUnreferencedCatalogs(ctx context.Context, books []*models.Songbook, versions []*models.CatalogVersion) int {
	refs := map[string]bool{}
	for _, b := range books {
		if b.CatalogVersionID != "" {
			refs[b.CatalogVersionID] = true
		}
	}

	if len(books) == 0 && len(versions) > 0 {
		log.Printf("[GC SAFETY WARNING] 0 songbooks found but %d catalog versions exist; skipping unreferenced catalog pruning to avoid accidental data loss", len(versions))
		return 0
	}

	unrefCount := 0
	now := time.Now().UTC()
	for _, v := range versions {
		if !isUnreferencedCatalogCandidate(v, refs, t.cutoff, now) {
			continue
		}
		prefix := strings.Trim(v.AssetPrefix, "/")
		if prefix == "" {
			continue
		}
		unrefCount++
		log.Printf("candidate unreferenced catalog=%s status=%s prefix=%s", v.ID, v.Status, prefix)
		if !t.dryRun {
			t.purgeCatalogVersion(ctx, v.ID)
		}
	}
	return unrefCount
}

func isUnreferencedCatalogCandidate(v *models.CatalogVersion, refs map[string]bool, cutoff, now time.Time) bool {
	if refs[v.ID] || v.Status == models.CatalogVersionBuilding {
		return false
	}
	if v.Status == models.CatalogVersionReady && v.CreatedAt.After(cutoff) {
		return false
	}
	if v.Status == models.CatalogVersionDeleting && v.LeaseExpiresAt.After(now) {
		return false
	}
	return true
}

func (t gcTarget) purgeCatalogVersion(ctx context.Context, versionID string) {
	err := t.store.PurgeCatalogVersion(ctx, versionID, func(assetPrefix string) error {
		return deletePrefix(ctx, t.s3c, t.bucket, assetPrefix)
	})
	if err == nil {
		return
	}
	if errors.Is(err, store.ErrCatalogVersionInUse) {
		log.Printf("candidate catalog=%s is in use, skipped deletion", versionID)
		return
	}
	log.Printf("purge catalog %s: %v", versionID, err)
}

func (t gcTarget) pruneOrphanPrefixes(ctx context.Context, versions []*models.CatalogVersion) (int, error) {
	// Inventory S3 directly as well: an interrupted publish or a deleted
	// metadata record can leave a complete catalog prefix with no DynamoDB row.
	known := map[string]bool{}
	for _, v := range versions {
		known[strings.Trim(v.AssetPrefix, "/")] = true
	}
	prefixes, err := listCatalogPrefixes(ctx, t.s3c, t.bucket)
	if err != nil {
		return 0, fmt.Errorf("list catalog prefixes: %w", err)
	}

	orphanPrefixCount := 0
	for _, prefix := range prefixes {
		if known[prefix] {
			continue
		}
		withinGrace, err := isPrefixWithinGrace(ctx, t.s3c, t.bucket, prefix, t.cutoff)
		if err != nil {
			log.Printf("[GC WARN] check grace for prefix %s: %v; skipping", prefix, err)
			continue
		}
		if withinGrace {
			log.Printf("orphan catalog prefix %s is within grace period (%v); skipping", prefix, t.grace)
			continue
		}
		orphanPrefixCount++
		log.Printf("candidate orphan catalog prefix=%s", prefix)
		if !t.dryRun {
			t.purgeOrphanPrefix(ctx, prefix)
		}
	}
	return orphanPrefixCount, nil
}

func (t gcTarget) purgeOrphanPrefix(ctx context.Context, prefix string) {
	err := t.store.PurgeOrphanCatalogPrefix(ctx, prefix, func(assetPrefix string) error {
		return deletePrefix(ctx, t.s3c, t.bucket, assetPrefix)
	})
	if err == nil {
		return
	}
	if errors.Is(err, store.ErrCatalogDeletionConflict) || errors.Is(err, store.ErrCatalogVersionInUse) {
		log.Printf("orphan catalog prefix %s is active or referenced, skipped deletion: %v", prefix, err)
		return
	}
	log.Printf("delete orphan assets %s: %v", prefix, err)
}

func (t gcTarget) pruneOrphanImports(ctx context.Context, jobs []*models.ImportJob) (int, error) {
	activeImports := map[string]bool{}
	for _, job := range jobs {
		if job.Status == models.ImportJobQueued || job.Status == models.ImportJobProcessing {
			activeImports[job.ManifestKey] = true
		}
	}
	objects, err := listObjects(ctx, t.s3c, t.bucket, "imports/")
	if err != nil {
		return 0, fmt.Errorf("list imports objects: %w", err)
	}

	orphanImportCount := 0
	for _, object := range objects {
		if activeImports[object.Key] {
			continue
		}
		if object.LastModified.After(t.cutoff) {
			log.Printf("orphan import %s is within grace period (%v); skipping", object.Key, t.grace)
			continue
		}
		orphanImportCount++
		log.Printf("candidate orphan import=%s", object.Key)
		if t.dryRun {
			continue
		}
		if err := deleteObject(ctx, t.s3c, t.bucket, object.Key); err != nil {
			log.Printf("delete orphan import %s: %v", object.Key, err)
		}
	}
	return orphanImportCount, nil
}

func (t gcTarget) pruneStaleImportJobs(ctx context.Context, jobs []*models.ImportJob) int {
	staleJobCount := 0
	for _, job := range jobs {
		if (job.Status == models.ImportJobQueued || job.Status == models.ImportJobProcessing) || job.UpdatedAt.After(t.cutoff) {
			continue
		}
		staleJobCount++
		log.Printf("candidate stale import job=%s status=%s manifest=%s", job.ID, job.Status, job.ManifestKey)
		if t.dryRun {
			continue
		}
		if err := t.store.DeleteImportJob(ctx, job.ID); err != nil {
			log.Printf("delete stale import job %s: %v", job.ID, err)
		}
	}
	return staleJobCount
}

func (t gcTarget) pruneOrphanPrivateObjects(ctx context.Context, books []*models.Songbook, versions []*models.CatalogVersion) (int, error) {
	if len(books) == 0 && len(versions) > 0 {
		return 0, nil
	}
	activePrivateLocations := buildActivePrivateLocations(books)
	privObjs, err := listObjects(ctx, t.s3c, t.bucket, "private/songbooks/")
	if err != nil {
		return 0, fmt.Errorf("list private songbook objects: %w", err)
	}
	orphanPrivateCount := 0
	for _, object := range privObjs {
		if activePrivateLocations[object.Key] || object.LastModified.After(t.cutoff) {
			continue
		}
		orphanPrivateCount++
		log.Printf("candidate orphan private=%s", object.Key)
		if t.dryRun {
			continue
		}
		if err := deleteObject(ctx, t.s3c, t.bucket, object.Key); err != nil {
			log.Printf("delete orphan private object %s: %v", object.Key, err)
		}
	}
	return orphanPrivateCount, nil
}

func buildActivePrivateLocations(books []*models.Songbook) map[string]bool {
	active := map[string]bool{}
	for _, book := range books {
		if book != nil && book.ID != "" && book.CatalogVersionID != "" {
			active["private/songbooks/"+book.ID+"/"+book.CatalogVersionID+"/locations.json.gz"] = true
		}
	}
	return active
}
