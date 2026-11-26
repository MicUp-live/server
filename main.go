package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"micup.live/server/internal/app"
	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/email"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/reference"
	"micup.live/server/internal/store"
	"micup.live/server/internal/store/backend"
)

func main() {
	cfg, err := config.LoadEnvironment()
	if err != nil {
		log.Fatalf("configure application: %v", err)
	}
	dependencies, closeStore, err := loadStore(cfg)
	if err != nil {
		log.Fatalf("configure store: %v", err)
	}
	defer closeStore()

	handler, err := app.NewRouter(cfg, dependencies)
	if err != nil {
		log.Fatalf("configure router: %v", err)
	}

	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		adapter := httpadapter.NewV2(handler)
		lambda.Start(adapter.ProxyWithContext)
		return
	}

	log.Printf("Starting server on port %s", cfg.Port)
	server := app.NewHTTPServer(cfg, handler)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func loadStore(cfg config.Config) (app.Dependencies, func(), error) {
	s, err := backend.Open(context.Background(), cfg, cfg.Environment == config.Development)
	if err != nil {
		return app.Dependencies{}, func() {}, err
	}

	var emailSender email.Sender = email.NewLogSender()
	var catalogJobs catalog.JobQueue
	var catalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
	var importJobs store.ImportJobStore
	var importQueue interface {
		Enqueue(context.Context, importjob.Job) error
	}
	var manifestWriter interface {
		Put(context.Context, string, []byte) error
		Presign(context.Context, string, int64) (string, error)
		DeleteKey(context.Context, string) error
		DeletePrefix(context.Context, string) error
	}
	referenceStore := &reference.HybridStore{
		LocalPublicDir: "./web/public",
		LocalDistDir:   "./dist",
	}
	needsAWS := cfg.CatalogBucket != "" || cfg.EmailTransport == "ses" || cfg.CatalogBuildQueueURL != "" || cfg.ImportJobQueueURL != ""
	var (
		s3Client  *s3.Client
		sqsClient *sqs.Client
	)
	if needsAWS {
		awsCfg, awsErr := awsclient.Load(context.Background(), awsclient.Config{
			Region:      cfg.AWSRegion,
			S3Endpoint:  cfg.S3Endpoint,
			SQSEndpoint: cfg.SQSEndpoint,
			SESEndpoint: cfg.SESEndpoint,
		})
		if awsErr != nil {
			if cfg.CatalogBuildQueueURL != "" || (cfg.ImportJobQueueURL != "" && cfg.CatalogBucket != "") {
				return app.Dependencies{}, func() {}, awsErr
			}
			if cfg.EmailTransport == "ses" {
				log.Printf("[EMAIL WARN] Failed to load AWS config for SES, falling back to LogSender: %v", awsErr)
			}
		} else {
			if cfg.CatalogBucket != "" {
				s3Client = awsclient.NewS3(awsCfg, cfg.S3Endpoint)
				referenceStore.Bucket = cfg.CatalogBucket
				referenceStore.Client = s3Client
				referenceStore.Presigner = s3.NewPresignClient(s3Client)
			}
			if cfg.EmailTransport == "ses" {
				sesClient := awsclient.NewSES(awsCfg, cfg.SESEndpoint)
				emailSender = email.NewSESSender(sesClient, "MicUp.live <noreply@micup.live>")
				log.Printf("[EMAIL] Configured AWS SESv2 sender for region %s", cfg.AWSRegion)
			}
			if cfg.CatalogBuildQueueURL != "" || (cfg.ImportJobQueueURL != "" && cfg.CatalogBucket != "") {
				sqsClient = awsclient.NewSQS(awsCfg, cfg.SQSEndpoint)
			}
		}
	}
	if cfg.CatalogBuildQueueURL != "" {
		catalogJobs, err = catalog.NewSQSJobQueue(cfg.CatalogBuildQueueURL, sqsClient)
		if err != nil {
			return app.Dependencies{}, func() {}, err
		}
		if cfg.CatalogBucket != "" && s3Client != nil {
			catalogReader = &catalog.S3CatalogReader{Bucket: cfg.CatalogBucket, Client: s3Client}
		}
	}
	if cfg.ImportJobQueueURL != "" && cfg.CatalogBucket != "" {
		importQueue = &importjob.Queue{URL: cfg.ImportJobQueueURL, Client: sqsClient}
		manifestWriter = &importjob.ManifestWriter{Bucket: cfg.CatalogBucket, Client: s3Client, Presigner: s3.NewPresignClient(s3Client)}
		importJobs = s
	}

	return app.Dependencies{
		Auth:           s,
		Favorites:      s,
		Passkeys:       s,
		Songbooks:      s,
		Requests:       s,
		Events:         s,
		CatalogJobs:    catalogJobs,
		CatalogReader:  catalogReader,
		CatalogChanges: s,
		Catalogs:       s,
		ImportJobs:     importJobs,
		ImportQueue:    importQueue,
		ManifestWriter: manifestWriter,
		ReferenceStore: referenceStore,
		DeveloperKeys:  s,
		RateLimits:     s,
		SongbookAccess: s,
		EmailSender:    emailSender,
	}, func() { _ = s.Close() }, nil
}
