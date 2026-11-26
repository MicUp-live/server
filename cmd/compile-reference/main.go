// Command compile-reference runs the v2 immutable/rolling canonical reference catalog pipeline.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"micup.live/server/internal/awsclient"
	"micup.live/server/internal/reference"
)

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	offline := flag.Bool("offline", false, "Offline mode: re-parse all sources from S3 or local archive cache with zero upstream HTTP requests")
	outDir := flag.String("out", "./web/public/reference/v1", "Output directory for compiled dictionary.json.gz and disc_map.json.gz")
	archiveDir := flag.String("archive-dir", "./dist/reference/v1/sources", "Local archive directory for raw files, normalized CSVs, and manifest.json")
	flag.Parse()

	ctx := context.Background()
	start := time.Now()

	var store reference.Store
	bucketName := envOrDefault("REFERENCE_S3_BUCKET", os.Getenv("CATALOG_ASSET_BUCKET"))
	if bucketName != "" {
		s3Endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
		if !strings.Contains(strings.ToLower(bucketName), "local") &&
			(strings.Contains(s3Endpoint, "127.0.0.1") || strings.Contains(s3Endpoint, "localhost")) {
			s3Endpoint = ""
		}
		awsCfg, err := awsclient.Load(ctx, awsclient.Config{
			Region:     envOrDefault("AWS_REGION", "us-east-2"),
			S3Endpoint: s3Endpoint,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading AWS config: %v\n", err)
			os.Exit(1)
		}
		s3Client := awsclient.NewS3(awsCfg, s3Endpoint)
		store = &reference.HybridStore{
			Bucket:    bucketName,
			Client:    s3Client,
			Presigner: s3.NewPresignClient(s3Client),
		}
	}

	res, err := reference.RunPipeline(ctx, reference.PipelineOptions{
		SourcesPath: "./internal/reference/sources.json",
		OutDir:      *outDir,
		ArchiveDir:  *archiveDir,
		Offline:     *offline,
		Store:       store,
		Logf: func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Reference pipeline failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Compiled %d dictionary tracks and %d mapped discs across %d sources (build=%s, degraded=%v) in %v\n",
		res.Manifest.DictionaryTracks,
		res.Manifest.MappedDiscs,
		len(res.Manifest.Sources),
		res.Manifest.BuildID,
		res.Manifest.Degraded,
		time.Since(start).Round(time.Millisecond),
	)
}
