// Package awsclient centralizes AWS SDK client configuration for production
// and local service emulators.
package awsclient

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Config contains the endpoint settings shared by the AWS-backed processes.
// Empty endpoints preserve the SDK's normal AWS endpoint and credential
// resolution behavior.
type Config struct {
	Region         string
	DynamoEndpoint string
	S3Endpoint     string
	SQSEndpoint    string
	SESEndpoint    string
}

// Load loads an AWS SDK configuration. When any local endpoint is configured,
// static dummy credentials are supplied because emulators do not require real
// AWS credentials and the SDK otherwise may try the EC2 metadata service.
func Load(ctx context.Context, cfg Config) (aws.Config, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-2"
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if hasEndpoint(cfg) {
		options = append(options,
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
		)
	}
	return awsconfig.LoadDefaultConfig(ctx, options...)
}

func hasEndpoint(cfg Config) bool {
	return firstEndpoint(cfg.DynamoEndpoint, cfg.S3Endpoint, cfg.SQSEndpoint, cfg.SESEndpoint) != ""
}

// NewS3 creates an S3 client. Local S3-compatible services generally do not
// support virtual-hosted bucket names, so path-style addressing is enabled
// only when a custom endpoint is used.
func NewS3(cfg aws.Config, endpoint string) *s3.Client {
	endpoint = normalize(endpoint)
	if endpoint == "" {
		return s3.NewFromConfig(cfg)
	}
	return s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = &endpoint
		options.UsePathStyle = true
	})
}

// NewSQS creates an SQS client using an optional emulator endpoint.
func NewSQS(cfg aws.Config, endpoint string) *sqs.Client {
	endpoint = normalize(endpoint)
	if endpoint == "" {
		return sqs.NewFromConfig(cfg)
	}
	return sqs.NewFromConfig(cfg, func(options *sqs.Options) {
		options.BaseEndpoint = &endpoint
	})
}

// NewSES creates an SESv2 client using an optional emulator endpoint.
func NewSES(cfg aws.Config, endpoint string) *sesv2.Client {
	endpoint = normalize(endpoint)
	if endpoint == "" {
		return sesv2.NewFromConfig(cfg)
	}
	return sesv2.NewFromConfig(cfg, func(options *sesv2.Options) {
		options.BaseEndpoint = &endpoint
	})
}

func normalize(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

func firstEndpoint(endpoints ...string) string {
	for _, endpoint := range endpoints {
		if endpoint = normalize(endpoint); endpoint != "" {
			return endpoint
		}
	}
	return ""
}
