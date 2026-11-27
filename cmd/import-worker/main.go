// Command import-worker consumes durable Assistant Importer jobs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	"micup.live/server/internal/store/backend"
)

type manifest struct {
	Songs        []*models.Song `json:"songs"`
	PruneMissing *bool          `json:"prune_missing,omitempty"`
}

type s3ManifestReader interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type workerRuntime struct {
	cfg  config.Config
	db   backend.Store
	s3c  *s3.Client
	sqsc *sqs.Client
	jobs catalog.JobQueue
}

func initRuntime(ctx context.Context) (*workerRuntime, error) {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		return nil, err
	}
	db, err := backend.Open(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	awsCfg, err := awsclient.Load(ctx, awsclient.Config{
		Region:      cfg.AWSRegion,
		S3Endpoint:  cfg.S3Endpoint,
		SQSEndpoint: cfg.SQSEndpoint,
		SESEndpoint: cfg.SESEndpoint,
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	sqsc := awsclient.NewSQS(awsCfg, cfg.SQSEndpoint)
	jobs, err := catalog.NewSQSJobQueue(cfg.CatalogBuildQueueURL, sqsc)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &workerRuntime{
		cfg:  cfg,
		db:   db,
		s3c:  awsclient.NewS3(awsCfg, cfg.S3Endpoint),
		sqsc: sqsc,
		jobs: jobs,
	}, nil
}

func (w *workerRuntime) handle(ctx context.Context, event events.SQSEvent) error {
	for _, record := range event.Records {
		if err := processRecord(ctx, record.Body, w.db, w.s3c, w.jobs, w.cfg.CatalogBucket); err != nil {
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
			log.Fatalf("import-worker: init runtime: %v", err)
		}
		lambda.Start(rt.handle)
		return
	}

	poll := flag.Bool("poll", false, "poll the import job SQS queue continuously")
	flag.Parse()

	if !*poll {
		log.Fatal("import-worker runs as Lambda (use -poll to poll SQS locally)")
	}

	rt, err := initRuntime(ctx)
	if err != nil {
		log.Fatalf("import-worker: init runtime: %v", err)
	}
	defer rt.db.Close()

	if rt.cfg.ImportJobQueueURL == "" {
		log.Fatal("IMPORT_JOB_QUEUE_URL is required to poll")
	}
	log.Println("import-worker: polling SQS for import jobs...")
	if err := awsclient.PollSQS(ctx, rt.sqsc, rt.cfg.ImportJobQueueURL, "import-worker", rt.handle); err != nil {
		log.Fatalf("poll SQS: %v", err)
	}
}

func processRecord(ctx context.Context, recordBody string, db store.ImportJobStore, s3c s3ManifestReader, jobs catalog.JobQueue, bucket string) error {
	var job importjob.Job
	if err := json.Unmarshal([]byte(recordBody), &job); err != nil {
		return err
	}
	status, err := db.ClaimImportJobProcessing(ctx, job.ID)
	if isImportJobFinishedErr(err) {
		// Completed-job redelivery is an idempotent no-op; terminal failed jobs cannot resume.
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim import job %s processing: %w", job.ID, err)
	}

	m, ok, err := loadManifest(ctx, db, s3c, bucket, job)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// Canonical tracks stay in the source manifest and are compiled directly
	// by the catalog worker; avoid one DynamoDB write per imported song.
	status.ProcessedSongs = len(m.Songs)
	if updateErr := db.UpdateImportJobProgress(ctx, job.ID, len(m.Songs)); updateErr != nil {
		if isImportJobFinishedErr(updateErr) {
			return nil
		}
		return fmt.Errorf("update import job %s progress: %w", job.ID, updateErr)
	}
	pruneMissing := job.PruneMissing || status.PruneMissing
	if m.PruneMissing != nil {
		pruneMissing = *m.PruneMissing
	}
	gen, err := resolveCatalogGeneration(ctx, db, job, status.Generation, status.SongbookID)
	if err != nil {
		return err
	}
	return jobs.Enqueue(ctx, catalog.BuildJob{SongbookID: status.SongbookID, ManifestKey: job.ManifestKey, PruneMissing: pruneMissing, JobGeneration: gen})
}

func isImportJobFinishedErr(err error) bool {
	return errors.Is(err, store.ErrImportJobAlreadyCompleted) || errors.Is(err, store.ErrImportJobTerminal)
}

func loadManifest(ctx context.Context, db store.ImportJobStore, s3c s3ManifestReader, bucket string, job importjob.Job) (manifest, bool, error) {
	obj, err := s3c.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &job.ManifestKey})
	if err != nil {
		// If S3 returns an error (e.g. manifest deleted because job completed concurrently or previously),
		// reload receipt to check if the job was completed or terminal.
		latest, getErr := db.GetImportJob(ctx, job.ID)
		if getErr == nil && latest != nil && (latest.Status == models.ImportJobCompleted || latest.Status == models.ImportJobFailed) {
			return manifest{}, false, nil
		}
		return manifest{}, false, err
	}
	body, err := io.ReadAll(obj.Body)
	obj.Body.Close()
	if err != nil {
		return manifest{}, false, err
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest{}, false, err
	}
	return m, true, nil
}

func resolveCatalogGeneration(ctx context.Context, db store.ImportJobStore, job importjob.Job, statusGen int64, songbookID string) (int64, error) {
	if statusGen != 0 {
		return statusGen, nil
	}
	if job.Generation > 0 {
		return job.Generation, nil
	}
	latestJob, getErr := db.GetImportJob(ctx, job.ID)
	if getErr != nil {
		return 0, fmt.Errorf("load import job %s: %w", job.ID, getErr)
	}
	if latestJob.Generation > 0 {
		return latestJob.Generation, nil
	}
	type generationAllocator interface {
		NextCatalogGeneration(ctx context.Context, songbookID string) (int64, error)
	}
	allocator, ok := db.(generationAllocator)
	if !ok {
		return 0, fmt.Errorf("cannot process legacy import %s without durable generation assignment", job.ID)
	}
	allocatedGen, allocErr := allocator.NextCatalogGeneration(ctx, songbookID)
	if allocErr != nil {
		return 0, fmt.Errorf("allocate legacy catalog generation: %w", allocErr)
	}
	assignedGen, assignErr := db.AssignImportJobGeneration(ctx, job.ID, allocatedGen)
	if assignErr != nil {
		return 0, fmt.Errorf("persist legacy catalog generation: %w", assignErr)
	}
	return assignedGen, nil
}
