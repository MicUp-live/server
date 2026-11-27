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
		return app.Dependencies{}, func() {
			// No-op cleanup returned when opening the store fails.
		}, err
	}

	referenceStore := &reference.HybridStore{
		LocalPublicDir: "./web/public",
		LocalDistDir:   "./dist",
	}
	s3Client, sqsClient, emailSender, err := configureAWS(cfg, referenceStore)
	if err != nil {
		return app.Dependencies{}, func() {
			// No-op cleanup returned when AWS configuration fails.
		}, err
	}

	deps := app.Dependencies{
		Auth:           s,
		Favorites:      s,
		Passkeys:       s,
		Songbooks:      s,
		Requests:       s,
		Events:         s,
		CatalogChanges: s,
		Catalogs:       s,
		ReferenceStore: referenceStore,
		DeveloperKeys:  s,
		RateLimits:     s,
		SongbookAccess: s,
		EmailSender:    emailSender,
	}
	if err := configureCatalog(&deps, cfg, s3Client, sqsClient); err != nil {
		return app.Dependencies{}, func() {
			// No-op cleanup returned when catalog configuration fails.
		}, err
	}
	configureImport(&deps, cfg, s, s3Client, sqsClient)

	return deps, func() { _ = s.Close() }, nil
}

func needsAWS(cfg config.Config) bool {
	return cfg.CatalogBucket != "" || cfg.EmailTransport == "ses" || cfg.CatalogBuildQueueURL != "" || cfg.ImportJobQueueURL != ""
}

func requiresSQS(cfg config.Config) bool {
	return cfg.CatalogBuildQueueURL != "" || (cfg.ImportJobQueueURL != "" && cfg.CatalogBucket != "")
}

func configureAWS(cfg config.Config, referenceStore *reference.HybridStore) (*s3.Client, *sqs.Client, email.Sender, error) {
	emailSender := email.Sender(email.NewLogSender())
	if !needsAWS(cfg) {
		return nil, nil, emailSender, nil
	}

	awsCfg, awsErr := awsclient.Load(context.Background(), awsclient.Config{
		Region:      cfg.AWSRegion,
		S3Endpoint:  cfg.S3Endpoint,
		SQSEndpoint: cfg.SQSEndpoint,
		SESEndpoint: cfg.SESEndpoint,
	})
	needsSQS := requiresSQS(cfg)
	if awsErr != nil {
		if needsSQS {
			return nil, nil, nil, awsErr
		}
		if cfg.EmailTransport == "ses" {
			log.Printf("[EMAIL WARN] Failed to load AWS config for SES, falling back to LogSender: %v", awsErr)
		}
		return nil, nil, emailSender, nil
	}

	var s3Client *s3.Client
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
	var sqsClient *sqs.Client
	if needsSQS {
		sqsClient = awsclient.NewSQS(awsCfg, cfg.SQSEndpoint)
	}
	return s3Client, sqsClient, emailSender, nil
}

func configureCatalog(deps *app.Dependencies, cfg config.Config, s3Client *s3.Client, sqsClient *sqs.Client) error {
	if cfg.CatalogBuildQueueURL == "" {
		return nil
	}
	catalogJobs, err := catalog.NewSQSJobQueue(cfg.CatalogBuildQueueURL, sqsClient)
	if err != nil {
		return err
	}
	deps.CatalogJobs = catalogJobs
	if cfg.CatalogBucket != "" && s3Client != nil {
		deps.CatalogReader = &catalog.S3CatalogReader{Bucket: cfg.CatalogBucket, Client: s3Client}
	}
	return nil
}

func configureImport(deps *app.Dependencies, cfg config.Config, importJobs store.ImportJobStore, s3Client *s3.Client, sqsClient *sqs.Client) {
	if cfg.ImportJobQueueURL == "" || cfg.CatalogBucket == "" {
		return
	}
	deps.ImportQueue = &importjob.Queue{URL: cfg.ImportJobQueueURL, Client: sqsClient}
	deps.ManifestWriter = &importjob.ManifestWriter{
		Bucket:    cfg.CatalogBucket,
		Client:    s3Client,
		Presigner: s3.NewPresignClient(s3Client),
	}
	deps.ImportJobs = importJobs
}
