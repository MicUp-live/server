// Command catalog-gc reports and removes catalog versions no longer referenced by songbooks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"micup.live/server/internal/account"
	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	"micup.live/server/internal/store/backend"
)

type gcRuntime struct {
	bucket string
	store  backend.Store
	s3c    *s3.Client
}

func initRuntime(ctx context.Context) (*gcRuntime, error) {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}
	if cfg.CatalogBucket == "" {
		return nil, fmt.Errorf("CATALOG_ASSET_BUCKET is required")
	}
	s, err := backend.Open(ctx, cfg, false)
	if err != nil {
		return nil, fmt.Errorf("connect to store: %w", err)
	}
	awsCfg, err := awsclient.Load(ctx, awsclient.Config{
		Region:         cfg.AWSRegion,
		DynamoEndpoint: cfg.DynamoEndpoint,
		S3Endpoint:     cfg.S3Endpoint,
		SQSEndpoint:    cfg.SQSEndpoint,
		SESEndpoint:    cfg.SESEndpoint,
	})
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &gcRuntime{
		bucket: cfg.CatalogBucket,
		store:  s,
		s3c:    awsclient.NewS3(awsCfg, cfg.S3Endpoint),
	}, nil
}

func main() {
	ctx := context.Background()
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		rt, err := initRuntime(ctx)
		if err != nil {
			log.Fatalf("catalog-gc: init runtime: %v", err)
		}
		grace := 24 * time.Hour
		if g := os.Getenv("GC_GRACE_PERIOD"); g != "" {
			if d, err := time.ParseDuration(g); err == nil {
				grace = d
			} else {
				log.Printf("invalid GC_GRACE_PERIOD %q, using default: %v", g, err)
			}
		}
		dryRun := os.Getenv("GC_DRY_RUN") == "true"
		lambda.Start(func(ctx context.Context) error {
			log.Printf("starting catalog-gc (dryRun=%v, grace=%v)", dryRun, grace)
			return executeGC(ctx, rt.store, rt.s3c, rt.bucket, dryRun, grace)
		})
		return
	}

	dryRun := flag.Bool("dry-run", true, "report candidates without deleting (default true)")
	grace := flag.Duration("grace", 24*time.Hour, "minimum age before deletion")
	listDeletingAccounts := flag.Bool("list-deleting-accounts", false, "list all accounts currently undergoing deletion")
	recoverDeletingAccounts := flag.Bool("recover-deleting-accounts", false, "resume and complete cascading deletion for all deleting accounts")
	recoverAccount := flag.String("recover-account", "", "resume and complete cascading deletion for a specific user ID")
	flag.Parse()

	rt, err := initRuntime(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer rt.store.Close()

	opOpts := OperatorOptions{
		ListDeletingAccounts:    *listDeletingAccounts,
		RecoverDeletingAccounts: *recoverDeletingAccounts,
		RecoverAccount:          *recoverAccount,
	}

	if opOpts.ListDeletingAccounts || opOpts.RecoverDeletingAccounts || opOpts.RecoverAccount != "" {
		if err := ExecuteOperatorAction(ctx, rt.store, rt.s3c, rt.bucket, opOpts); err != nil {
			log.Fatal(err)
		}
		return
	}

	log.Printf("starting catalog-gc (dryRun=%v, grace=%v)", *dryRun, *grace)
	if err := executeGC(ctx, rt.store, rt.s3c, rt.bucket, *dryRun, *grace); err != nil {
		log.Fatal(err)
	}
}

type gcStore interface {
	ListSongbooks(ctx context.Context) ([]*models.Songbook, error)
	ListCatalogVersions(ctx context.Context) ([]*models.CatalogVersion, error)
	ListImportJobs(ctx context.Context) ([]*models.ImportJob, error)
	DeleteCatalogVersion(ctx context.Context, id string, leaseToken ...string) error
	DeleteImportJob(ctx context.Context, id string) error
	PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error
	PurgeOrphanCatalogPrefix(ctx context.Context, prefix string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error
}

type s3Client interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// OperatorOptions configures operator-driven inspection and recovery of accounts undergoing deletion.
type OperatorOptions struct {
	ListDeletingAccounts    bool
	RecoverDeletingAccounts bool
	RecoverAccount          string
}

type s3AssetPurger struct {
	client s3Client
	bucket string
}

func (p *s3AssetPurger) DeleteKey(ctx context.Context, key string) error {
	return deleteObject(ctx, p.client, p.bucket, key)
}

func (p *s3AssetPurger) DeletePrefix(ctx context.Context, prefix string) error {
	return deletePrefix(ctx, p.client, p.bucket, prefix)
}

type operatorAccountStore interface {
	store.AuthStore
	store.PasskeyStore
	store.DeveloperKeyStore
	store.SongbookStore
	store.SongbookAccessStore
	store.EventStore
	store.RequestStore
	store.CatalogStore
	store.CatalogChangeStore
	store.ImportJobStore
}

// ExecuteOperatorAction provides the runnable operator entry point to inspect or recover deleting accounts.
func ExecuteOperatorAction(ctx context.Context, s operatorAccountStore, s3c s3Client, bucket string, opts OperatorOptions) error {
	purger := &s3AssetPurger{client: s3c, bucket: bucket}
	svc := account.NewService(s, s, s, s, s, s, nil, s, s, purger).WithDeveloperKeys(s).WithAccess(s)

	if opts.ListDeletingAccounts {
		users, err := s.ListDeletingUsers(ctx)
		if err != nil {
			return fmt.Errorf("list deleting users: %w", err)
		}
		if len(users) == 0 {
			log.Printf("[OPERATOR] No accounts currently undergoing deletion")
			return nil
		}
		log.Printf("[OPERATOR] Found %d accounts undergoing deletion:", len(users))
		for _, u := range users {
			log.Printf("  - User ID: %s | Email: %s | Step: %s | Error: %s | StartedAt: %s | PendingCatalogs: %d",
				u.ID, u.Email, u.DeletionStep, u.DeletionError, u.DeletionStartedAt.Format(time.RFC3339), len(u.PendingCatalogVersions))
		}
		return nil
	}

	if opts.RecoverAccount != "" {
		userID := strings.TrimSpace(opts.RecoverAccount)
		log.Printf("[OPERATOR] Resuming account deletion for user: %s", userID)
		if err := svc.ResumeAccountDeletion(ctx, userID); err != nil {
			return fmt.Errorf("recover account %s: %w", userID, err)
		}
		log.Printf("[OPERATOR] Successfully completed account deletion for user: %s", userID)
		return nil
	}

	if opts.RecoverDeletingAccounts {
		log.Printf("[OPERATOR] Recovering all deleting accounts...")
		completed, err := svc.RecoverDeletingAccounts(ctx)
		if err != nil {
			log.Printf("[OPERATOR WARNING] RecoverDeletingAccounts partially completed (%d accounts purged): %v", completed, err)
			return err
		}
		log.Printf("[OPERATOR] Successfully completed deletion recovery for %d accounts", completed)
		return nil
	}

	return nil
}

func executeGC(ctx context.Context, s gcStore, s3c s3Client, bucket string, dryRun bool, grace time.Duration) error {
	books, err := s.ListSongbooks(ctx)
	if err != nil {
		return fmt.Errorf("list songbooks: %w", err)
	}
	versions, err := s.ListCatalogVersions(ctx)
	if err != nil {
		return fmt.Errorf("list catalog versions: %w", err)
	}
	jobs, err := s.ListImportJobs(ctx)
	if err != nil {
		return fmt.Errorf("list import jobs: %w", err)
	}

	cutoff := time.Now().UTC().Add(-grace)

	refs := map[string]bool{}
	for _, b := range books {
		if b.CatalogVersionID != "" {
			refs[b.CatalogVersionID] = true
		}
	}

	unrefCount := 0
	if len(books) == 0 && len(versions) > 0 {
		log.Printf("[GC SAFETY WARNING] 0 songbooks found but %d catalog versions exist; skipping unreferenced catalog pruning to avoid accidental data loss", len(versions))
	} else {
		now := time.Now().UTC()
		for _, v := range versions {
			if refs[v.ID] || v.Status == models.CatalogVersionBuilding {
				continue
			}
			if v.Status == models.CatalogVersionReady && v.CreatedAt.After(cutoff) {
				continue
			}
			if v.Status == models.CatalogVersionDeleting && v.LeaseExpiresAt.After(now) {
				continue
			}
			prefix := strings.Trim(v.AssetPrefix, "/")
			if prefix == "" {
				continue
			}
			unrefCount++
			log.Printf("candidate unreferenced catalog=%s status=%s prefix=%s", v.ID, v.Status, prefix)
			if !dryRun {
				err := s.PurgeCatalogVersion(ctx, v.ID, func(assetPrefix string) error {
					return deletePrefix(ctx, s3c, bucket, assetPrefix)
				})
				if err != nil {
					if errors.Is(err, store.ErrCatalogVersionInUse) {
						log.Printf("candidate catalog=%s is in use, skipped deletion", v.ID)
						continue
					}
					log.Printf("purge catalog %s: %v", v.ID, err)
					continue
				}
			}
		}
	}

	// Inventory S3 directly as well: an interrupted publish or a deleted
	// metadata record can leave a complete catalog prefix with no DynamoDB row.
	known := map[string]bool{}
	for _, v := range versions {
		known[strings.Trim(v.AssetPrefix, "/")] = true
	}
	prefixes, err := listCatalogPrefixes(ctx, s3c, bucket)
	if err != nil {
		return fmt.Errorf("list catalog prefixes: %w", err)
	}

	orphanPrefixCount := 0
	for _, prefix := range prefixes {
		if known[prefix] {
			continue
		}
		withinGrace, err := isPrefixWithinGrace(ctx, s3c, bucket, prefix, cutoff)
		if err != nil {
			log.Printf("[GC WARN] check grace for prefix %s: %v; skipping", prefix, err)
			continue
		}
		if withinGrace {
			log.Printf("orphan catalog prefix %s is within grace period (%v); skipping", prefix, grace)
			continue
		}
		orphanPrefixCount++
		log.Printf("candidate orphan catalog prefix=%s", prefix)
		if !dryRun {
			err := s.PurgeOrphanCatalogPrefix(ctx, prefix, func(assetPrefix string) error {
				return deletePrefix(ctx, s3c, bucket, assetPrefix)
			})
			if err != nil {
				if errors.Is(err, store.ErrCatalogDeletionConflict) || errors.Is(err, store.ErrCatalogVersionInUse) {
					log.Printf("orphan catalog prefix %s is active or referenced, skipped deletion: %v", prefix, err)
					continue
				}
				log.Printf("delete orphan assets %s: %v", prefix, err)
			}
		}
	}

	activeImports := map[string]bool{}
	for _, job := range jobs {
		if job.Status == models.ImportJobQueued || job.Status == models.ImportJobProcessing {
			activeImports[job.ManifestKey] = true
		}
	}
	objects, err := listObjects(ctx, s3c, bucket, "imports/")
	if err != nil {
		return fmt.Errorf("list imports objects: %w", err)
	}

	orphanImportCount := 0
	for _, object := range objects {
		if activeImports[object.Key] {
			continue
		}
		if object.LastModified.After(cutoff) {
			log.Printf("orphan import %s is within grace period (%v); skipping", object.Key, grace)
			continue
		}
		orphanImportCount++
		log.Printf("candidate orphan import=%s", object.Key)
		if !dryRun {
			if err := deleteObject(ctx, s3c, bucket, object.Key); err != nil {
				log.Printf("delete orphan import %s: %v", object.Key, err)
			}
		}
	}

	staleJobCount := 0
	for _, job := range jobs {
		if (job.Status == models.ImportJobQueued || job.Status == models.ImportJobProcessing) || job.UpdatedAt.After(cutoff) {
			continue
		}
		staleJobCount++
		log.Printf("candidate stale import job=%s status=%s manifest=%s", job.ID, job.Status, job.ManifestKey)
		if !dryRun {
			if err := s.DeleteImportJob(ctx, job.ID); err != nil {
				log.Printf("delete stale import job %s: %v", job.ID, err)
			}
		}
	}

	orphanPrivateCount := 0
	if !(len(books) == 0 && len(versions) > 0) {
		activePrivateLocations := map[string]bool{}
		for _, book := range books {
			if book != nil && book.ID != "" && book.CatalogVersionID != "" {
				activePrivateLocations["private/songbooks/"+book.ID+"/"+book.CatalogVersionID+"/locations.json.gz"] = true
			}
		}
		privObjs, err := listObjects(ctx, s3c, bucket, "private/songbooks/")
		if err != nil {
			return fmt.Errorf("list private songbook objects: %w", err)
		}
		for _, object := range privObjs {
			if activePrivateLocations[object.Key] || object.LastModified.After(cutoff) {
				continue
			}
			orphanPrivateCount++
			log.Printf("candidate orphan private=%s", object.Key)
			if !dryRun {
				if err := deleteObject(ctx, s3c, bucket, object.Key); err != nil {
					log.Printf("delete orphan private object %s: %v", object.Key, err)
				}
			}
		}
	}

	log.Printf("catalog-gc completed (dryRun=%v, grace=%v): %d unreferenced catalogs, %d orphan prefixes, %d orphan imports, %d stale import jobs, %d orphan private objects",
		dryRun, grace, unrefCount, orphanPrefixCount, orphanImportCount, staleJobCount, orphanPrivateCount)
	return nil
}

type s3ObjectInfo struct {
	Key          string
	LastModified time.Time
}

func isPrefixWithinGrace(ctx context.Context, client s3Client, bucket, prefix string, cutoff time.Time) (bool, error) {
	var token *string
	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix + "/"),
			ContinuationToken: token,
		})
		if err != nil {
			return false, err
		}
		for _, obj := range page.Contents {
			if obj.LastModified != nil && obj.LastModified.After(cutoff) {
				return true, nil
			}
		}
		if !aws.ToBool(page.IsTruncated) || page.NextContinuationToken == nil {
			break
		}
		token = page.NextContinuationToken
	}
	return false, nil
}

func listCommonPrefixes(ctx context.Context, client s3Client, bucket, prefix string) ([]string, error) {
	var token *string
	var prefixes []string
	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list prefixes under %s: %w", prefix, err)
		}
		for _, cp := range page.CommonPrefixes {
			p := strings.TrimSuffix(aws.ToString(cp.Prefix), "/")
			if p != "" {
				prefixes = append(prefixes, p)
			}
		}
		if !aws.ToBool(page.IsTruncated) || page.NextContinuationToken == nil {
			break
		}
		token = page.NextContinuationToken
	}
	return prefixes, nil
}

func listCatalogPrefixes(ctx context.Context, client s3Client, bucket string) ([]string, error) {
	topPrefixes, err := listCommonPrefixes(ctx, client, bucket, "catalogs/")
	if err != nil {
		return nil, fmt.Errorf("list catalog top prefixes: %w", err)
	}

	prefixes := make([]string, 0)
	for _, top := range topPrefixes {
		if top == "catalogs/epochs" {
			// Disjoint epoch namespace: catalogs/epochs/<catalog-id>/<epoch-token>/
			idPrefixes, err := listCommonPrefixes(ctx, client, bucket, "catalogs/epochs/")
			if err != nil {
				return nil, fmt.Errorf("list epoch catalog IDs: %w", err)
			}
			for _, idPrefix := range idPrefixes {
				epochPrefixes, err := listCommonPrefixes(ctx, client, bucket, idPrefix+"/")
				if err != nil {
					return nil, fmt.Errorf("list epochs under %s: %w", idPrefix, err)
				}
				prefixes = append(prefixes, epochPrefixes...)
			}
		} else {
			// Legacy layout: catalogs/<catalog-id>/
			// Legacy catalogs store assets directly under catalogs/<id>/ (manifest.json, search.json.gz)
			// and letter shards under catalogs/<id>/letters/.
			// The catalog prefix is top ("catalogs/<id>"). Internal subdirectories like "letters"
			// are NOT treated as epochs or separate prefixes.
			prefixes = append(prefixes, top)
		}
	}
	return prefixes, nil
}

func listObjects(ctx context.Context, client s3Client, bucket, prefix string) ([]s3ObjectInfo, error) {
	var token *string
	objects := make([]s3ObjectInfo, 0)
	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, object := range page.Contents {
			if object.Key != nil {
				var lastModified time.Time
				if object.LastModified != nil {
					lastModified = *object.LastModified
				}
				objects = append(objects, s3ObjectInfo{
					Key:          *object.Key,
					LastModified: lastModified,
				})
			}
		}
		if !aws.ToBool(page.IsTruncated) || page.NextContinuationToken == nil {
			break
		}
		token = page.NextContinuationToken
	}
	return objects, nil
}

func deleteObject(ctx context.Context, client s3Client, bucket, key string) error {
	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}

func deletePrefix(ctx context.Context, client s3Client, bucket, prefix string) error {
	var token *string
	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix + "/"),
			ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		if len(page.Contents) > 0 {
			objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
			for _, obj := range page.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
			}
			if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
			}); err != nil {
				return err
			}
		}
		if !aws.ToBool(page.IsTruncated) || page.NextContinuationToken == nil {
			return nil
		}
		token = page.NextContinuationToken
	}
}
