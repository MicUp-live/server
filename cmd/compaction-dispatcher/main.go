// Command compaction-dispatcher enqueues catalog compactions for changed books.
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/store/backend"
)

type dispatcherRuntime struct {
	db    backend.Store
	queue catalog.JobQueue
}

func initRuntime(ctx context.Context) (*dispatcherRuntime, error) {
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
	queue, err := catalog.NewSQSJobQueue(cfg.CatalogBuildQueueURL, awsclient.NewSQS(awsCfg, cfg.SQSEndpoint))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &dispatcherRuntime{db: db, queue: queue}, nil
}

func (d *dispatcherRuntime) handle(ctx context.Context) error {
	books, err := d.db.ListSongbooks(ctx)
	if err != nil {
		return err
	}
	for _, book := range books {
		changes, err := d.db.ListCatalogChanges(ctx, book.ID)
		if err != nil {
			return err
		}
		if len(changes) > 0 {
			if err := d.queue.Enqueue(ctx, catalog.BuildJob{SongbookID: book.ID, Compaction: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	ctx := context.Background()
	rt, err := initRuntime(ctx)
	if err != nil {
		log.Fatalf("compaction-dispatcher: init runtime: %v", err)
	}
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(rt.handle)
		return
	}
	defer rt.db.Close()
	log.Println("Starting catalog compaction check...")
	if err := rt.handle(ctx); err != nil {
		log.Fatalf("compaction failed: %v", err)
	}
	log.Println("Compaction check completed successfully.")
}
