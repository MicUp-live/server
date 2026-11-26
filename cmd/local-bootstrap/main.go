// Command local-bootstrap creates the local DynamoDB schema without contacting AWS.
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"micup.live/server/internal/config"
	dynamostore "micup.live/server/internal/store/dynamo"
)

func main() {
	cfg, err := config.LoadWorkerEnvironment()
	if err != nil {
		log.Fatalf("load local configuration: %v", err)
	}
	if err := requireLocalEndpoint(cfg.DynamoEndpoint); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	store, err := dynamostore.New(ctx, dynamostore.Config{
		Region:      cfg.AWSRegion,
		Endpoint:    cfg.DynamoEndpoint,
		TablePrefix: cfg.DynamoPrefix,
	})
	if err != nil {
		log.Fatalf("connect to DynamoDB Local: %v", err)
	}
	defer store.Close()

	if err := store.EnsureTables(ctx); err != nil {
		log.Fatalf("create local DynamoDB schema: %v", err)
	}
	log.Printf("local DynamoDB schema is ready (prefix %q)", cfg.DynamoPrefix)
}

func requireLocalEndpoint(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("DYNAMO_ENDPOINT is required; refusing to use the AWS default endpoint")
	}
	endpoint, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse DYNAMO_ENDPOINT: %w", err)
	}
	switch strings.ToLower(endpoint.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "dynamodb":
		return nil
	default:
		return fmt.Errorf("DYNAMO_ENDPOINT %q is not local; refusing to continue", raw)
	}
}

func init() {
	// Prevent the AWS SDK from probing EC2 instance metadata if this command is
	// launched outside mise. The explicit endpoint remains the primary guard.
	_ = os.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}
