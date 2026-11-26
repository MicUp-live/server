// Command import-reconciler scans for unconfirmed dispatch-ready import jobs and dispatches them to SQS.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/aws/aws-lambda-go/lambda"
	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store/backend"
)

type reconcilerService interface {
	ReconcileUndispatchedJobs(ctx context.Context, maxAttempts int) (int, error)
	ListExhaustedImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error)
	ResetImportJobDispatch(ctx context.Context, jobID string) error
}

func initService(ctx context.Context) (*catalog.Service, func(), error) {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		return nil, func() {}, fmt.Errorf("load environment: %w", err)
	}
	db, err := backend.Open(ctx, cfg, false)
	if err != nil {
		return nil, func() {}, fmt.Errorf("connect to store: %w", err)
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
		return nil, func() {}, fmt.Errorf("load aws config: %w", err)
	}
	queue := &importjob.Queue{URL: cfg.ImportJobQueueURL, Client: awsclient.NewSQS(awsCfg, cfg.SQSEndpoint)}
	svc := &catalog.Service{
		ImportJobs:  db,
		ImportQueue: queue,
	}
	return svc, func() { _ = db.Close() }, nil
}

func main() {
	ctx := context.Background()
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		svc, _, err := initService(ctx)
		if err != nil {
			log.Fatalf("import-reconciler: init: %v", err)
		}
		maxAttempts := 5
		if envMax := os.Getenv("RECONCILE_MAX_ATTEMPTS"); envMax != "" {
			if val, err := strconv.Atoi(envMax); err == nil && val > 0 {
				maxAttempts = val
			}
		}
		lambda.Start(func(ctx context.Context) error {
			return runReconciliation(ctx, svc, maxAttempts)
		})
		return
	}

	maxAttempts := flag.Int("max-attempts", 5, "maximum dispatch attempts before job is flagged as exhausted")
	resetJobID := flag.String("reset-job", "", "import job ID to reset dispatch attempts and claim state for")
	listExhausted := flag.Bool("list-exhausted", false, "list unconfirmed jobs that reached maximum dispatch attempts")
	flag.Parse()

	svc, closeFn, err := initService(ctx)
	if err != nil {
		log.Fatalf("import-reconciler: %v", err)
	}
	defer closeFn()

	if *resetJobID != "" {
		if err := svc.ResetImportJobDispatch(ctx, *resetJobID); err != nil {
			log.Fatalf("import-reconciler: reset job %s failed: %v", *resetJobID, err)
		}
		log.Printf("import-reconciler: successfully reset dispatch attempts for job %s", *resetJobID)
		return
	}

	if *listExhausted {
		exhausted, err := svc.ListExhaustedImportJobs(ctx, *maxAttempts)
		if err != nil {
			log.Fatalf("import-reconciler: list exhausted jobs failed: %v", err)
		}
		log.Printf("import-reconciler: found %d exhausted jobs (maxAttempts=%d)", len(exhausted), *maxAttempts)
		for _, j := range exhausted {
			log.Printf(" - Job %s (songbook: %s, attempts: %d, status: %s, error: %s)", j.ID, j.SongbookID, j.DispatchAttempts, j.Status, j.Error)
		}
		return
	}

	if err := runReconciliation(ctx, svc, *maxAttempts); err != nil {
		log.Fatalf("import-reconciler: %v", err)
	}
}

func runReconciliation(ctx context.Context, svc reconcilerService, maxAttempts int) error {
	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, maxAttempts)
	if err != nil {
		return fmt.Errorf("reconcile undispatched jobs: %w", err)
	}
	log.Printf("import-reconciler: reconciled and dispatched %d jobs", reconciled)

	exhausted, err := svc.ListExhaustedImportJobs(ctx, maxAttempts)
	if err != nil {
		return fmt.Errorf("list exhausted import jobs: %w", err)
	}
	if len(exhausted) > 0 {
		log.Printf("import-reconciler: ALERT: %d jobs have reached attempt limit (%d) and require operator attention", len(exhausted), maxAttempts)
		for _, j := range exhausted {
			log.Printf(" - Exhausted job %s (songbook %s, %d attempts): %s", j.ID, j.SongbookID, j.DispatchAttempts, j.Error)
		}
	}
	return nil
}
