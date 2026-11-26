// Command catalog-worker consumes durable catalog build jobs.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	"micup.live/server/internal/store/backend"
)

type s3ManifestAPI interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type sourceManifest struct {
	Songs        []*models.Song `json:"songs"`
	PruneMissing *bool          `json:"prune_missing,omitempty"`
}

type workerRuntime struct {
	cfg      config.Config
	db       backend.Store
	s3Client *s3.Client
	sqsQueue string
	runner   *catalog.Runner
}

func initRuntime(ctx context.Context) (*workerRuntime, error) {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.CatalogBucket) == "" {
		return nil, fmt.Errorf("CATALOG_ASSET_BUCKET is required")
	}
	db, err := backend.Open(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	awsCfg, err := awsclient.Load(ctx, awsclient.Config{
		Region:         cfg.AWSRegion,
		DynamoEndpoint: cfg.DynamoEndpoint,
		S3Endpoint:     cfg.S3Endpoint,
		SQSEndpoint:    cfg.SQSEndpoint,
		SESEndpoint:    cfg.SESEndpoint,
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	s3Client := awsclient.NewS3(awsCfg, cfg.S3Endpoint)
	assets, err := catalog.NewS3AssetStore(cfg.CatalogBucket, s3Client)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	runner := &catalog.Runner{
		Books:   db,
		Changes: db,
		Imports: db,
		Reader:  &catalog.S3CatalogReader{Bucket: cfg.CatalogBucket, Client: s3Client},
		Builder: catalog.Builder{
			Catalogs:        db,
			Assets:          assets,
			Options:         catalog.Options{SchemaVersion: 1},
			LeaseDuration:   10 * time.Minute,
			MaxWaitDuration: 30 * time.Second,
			PollInterval:    250 * time.Millisecond,
		},
	}
	return &workerRuntime{
		cfg:      cfg,
		db:       db,
		s3Client: s3Client,
		sqsQueue: cfg.CatalogBuildQueueURL,
		runner:   runner,
	}, nil
}

func (w *workerRuntime) handle(ctx context.Context, event events.SQSEvent) error {
	for _, record := range event.Records {
		var job catalog.BuildJob
		if err := json.Unmarshal([]byte(record.Body), &job); err != nil {
			return fmt.Errorf("decode catalog job: %w", err)
		}
		var err error
		if job.Compaction {
			_, err = w.runner.CompactSongbook(ctx, job.SongbookID)
		} else if strings.TrimSpace(job.ManifestKey) != "" {
			err = buildFromManifestWithClient(ctx, w.runner, w.s3Client, w.cfg.CatalogBucket, job.SongbookID, job.ManifestKey, job.PruneMissing, job.JobGeneration)
		} else {
			_, err = w.runner.CompactSongbook(ctx, job.SongbookID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx := context.Background()
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		rt, err := initRuntime(ctx)
		if err != nil {
			log.Fatalf("catalog-worker: init runtime: %v", err)
		}
		lambda.Start(rt.handle)
		return
	}

	poll := flag.Bool("poll", false, "poll the catalog build SQS queue continuously")
	songbookID := flag.String("songbook", "", "songbook ID to build catalog for")
	flag.Parse()

	if !*poll && strings.TrimSpace(*songbookID) == "" {
		fmt.Println("Usage: catalog-worker [-poll] [-songbook <songbook-id>]")
		os.Exit(1)
	}

	rt, err := initRuntime(ctx)
	if err != nil {
		log.Fatalf("init runner: %v", err)
	}
	defer rt.db.Close()

	if *poll {
		if rt.sqsQueue == "" {
			log.Fatal("CATALOG_BUILD_QUEUE_URL is required to poll")
		}
		awsCfg, err := awsclient.Load(ctx, awsclient.Config{
			Region:         rt.cfg.AWSRegion,
			DynamoEndpoint: rt.cfg.DynamoEndpoint,
			S3Endpoint:     rt.cfg.S3Endpoint,
			SQSEndpoint:    rt.cfg.SQSEndpoint,
			SESEndpoint:    rt.cfg.SESEndpoint,
		})
		if err != nil {
			log.Fatalf("load aws config: %v", err)
		}
		sqsc := awsclient.NewSQS(awsCfg, rt.cfg.SQSEndpoint)
		log.Println("catalog-worker: polling SQS for catalog build jobs...")
		if err := awsclient.PollSQS(ctx, sqsc, rt.sqsQueue, "catalog-worker", rt.handle); err != nil {
			log.Fatalf("poll SQS: %v", err)
		}
		return
	}

	log.Printf("Starting catalog compaction for songbook %s...", *songbookID)
	version, err := rt.runner.CompactSongbook(ctx, *songbookID)
	if err != nil {
		log.Fatalf("compact songbook %s failed: %v", *songbookID, err)
	}
	if version == nil {
		log.Printf("No pending catalog changes for songbook %s.", *songbookID)
		return
	}
	log.Printf("Success! Catalog version %s is ready (%d songs, prefix: %s)", version.ID, version.SongCount, version.AssetPrefix)
}

func buildFromManifestWithClient(ctx context.Context, runner *catalog.Runner, client s3ManifestAPI, bucket, songbookID, key string, jobPruneMissing bool, jobGeneration int64) error {
	jobID := strings.TrimSuffix(strings.TrimPrefix(key, "imports/"), ".json")
	var storedJob *models.ImportJob
	if runner.Imports != nil {
		job, getErr := runner.Imports.GetImportJob(ctx, jobID)
		if getErr == nil && job != nil {
			if job.Status == models.ImportJobCompleted {
				log.Printf("catalog-worker: import %s already completed; skipping manifest build", jobID)
				if _, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); delErr != nil {
					return fmt.Errorf("delete completed source manifest: %w", delErr)
				}
				return nil
			}
			if job.Status == models.ImportJobFailed {
				log.Printf("catalog-worker: import %s is terminal failed; skipping manifest build", jobID)
				if _, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); delErr != nil {
					return fmt.Errorf("delete failed source manifest: %w", delErr)
				}
				return nil
			}
			storedJob = job
		} else if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
			return fmt.Errorf("load import job %s: %w", jobID, getErr)
		}
	}

	obj, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		// If S3 returns an error (e.g. manifest deleted because job completed previously or concurrently),
		// reload receipt to check if the job was completed or terminal.
		if runner.Imports != nil {
			latest, getErr := runner.Imports.GetImportJob(ctx, jobID)
			if getErr == nil && latest != nil && (latest.Status == models.ImportJobCompleted || latest.Status == models.ImportJobFailed) {
				return nil
			}
		}
		return fmt.Errorf("read source manifest: %w", err)
	}
	body, readErr := io.ReadAll(obj.Body)
	obj.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read source manifest: %w", readErr)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("decode source manifest: %w", err)
	}
	for i, song := range manifest.Songs {
		if song == nil {
			continue
		}
		song.SongbookID = songbookID
		if song.ID == "" {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", song.Fingerprint, i)))
			song.ID = "import-" + hex.EncodeToString(sum[:])[:24]
		}
	}
	pruneMissing := jobPruneMissing
	if manifest.PruneMissing != nil {
		pruneMissing = *manifest.PruneMissing
	}

	gen := jobGeneration
	if gen == 0 && storedJob != nil {
		gen = storedJob.Generation
	}
	if gen == 0 {
		// Legacy job queued before generation tracking was introduced.
		// Allocate a sequential generation from Songbooks and persist it durably.
		if runner.Books != nil && runner.Imports != nil {
			allocatedGen, allocErr := runner.Books.NextCatalogGeneration(ctx, songbookID)
			if allocErr != nil {
				return fmt.Errorf("allocate legacy catalog generation: %w", allocErr)
			}
			assignedGen, assignErr := runner.Imports.AssignImportJobGeneration(ctx, jobID, allocatedGen)
			if assignErr != nil {
				return fmt.Errorf("persist legacy catalog generation: %w", assignErr)
			}
			gen = assignedGen
			if storedJob != nil {
				storedJob.Generation = gen
			}
		} else if gen == 0 {
			return fmt.Errorf("cannot publish legacy import %s without durable generation assignment", jobID)
		}
	}

	pubOpts := store.PublicationOptions{
		JobGeneration:  gen,
		JobID:          jobID,
		ProcessedSongs: len(manifest.Songs),
	}
	if _, err := runner.BuildManifestCatalog(ctx, songbookID, manifest.Songs, pruneMissing, pubOpts); err != nil {
		if errors.Is(err, store.ErrImportJobAlreadyCompleted) {
			log.Printf("catalog-worker: import %s already completed; acknowledging redelivery", jobID)
			if _, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); delErr != nil {
				return fmt.Errorf("delete completed source manifest: %w", delErr)
			}
			return nil
		}
		if errors.Is(err, store.ErrImportJobTerminal) {
			log.Printf("catalog-worker: import %s is terminal failed; discarding", jobID)
			if _, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); delErr != nil {
				return fmt.Errorf("delete failed source manifest: %w", delErr)
			}
			return nil
		}
		if errors.Is(err, store.ErrPublicationSuperseded) {
			log.Printf("catalog-worker: import %s for songbook %s was superseded by newer catalog publication; marking failed", jobID, songbookID)
			if runner.Imports != nil {
				if failErr := runner.Imports.FailImportJob(ctx, jobID, "import superseded by newer catalog publication"); failErr != nil {
					return fmt.Errorf("record import job supersession: %w", failErr)
				}
			}
			if _, delErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); delErr != nil {
				return fmt.Errorf("delete superseded source manifest: %w", delErr)
			}
			return nil
		}
		// Base version conflict (or transient storage error): preserve manifest in S3 and return error for SQS retry.
		return err
	}
	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); err != nil {
		return fmt.Errorf("delete source manifest: %w", err)
	}
	return nil
}
