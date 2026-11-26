// Package catalog defines durable catalog build job messages.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// BuildJob identifies the songbook and, when available, its durable source
// manifest. Manifest-backed jobs avoid a second full read from DynamoDB.
type BuildJob struct {
	SongbookID    string `json:"songbook_id"`
	ManifestKey   string `json:"manifest_key,omitempty"`
	Compaction    bool   `json:"compaction,omitempty"`
	PruneMissing  bool   `json:"prune_missing,omitempty"`
	JobGeneration int64  `json:"job_generation,omitempty"`
}

// JobQueue accepts durable catalog build jobs.
type JobQueue interface {
	Enqueue(context.Context, BuildJob) error
}

type sqsSendAPI interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// SQSJobQueue sends catalog build jobs to one SQS queue.
type SQSJobQueue struct {
	url    string
	client sqsSendAPI
}

// NewSQSJobQueue creates a durable job sender.
func NewSQSJobQueue(url string, client sqsSendAPI) (*SQSJobQueue, error) {
	if strings.TrimSpace(url) == "" || client == nil {
		return nil, fmt.Errorf("new catalog job queue: URL and client are required")
	}
	return &SQSJobQueue{url: url, client: client}, nil
}

// Enqueue serializes and sends one build request.
func (q *SQSJobQueue) Enqueue(ctx context.Context, job BuildJob) error {
	if strings.TrimSpace(job.SongbookID) == "" {
		return fmt.Errorf("enqueue catalog job: songbook ID is required")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if _, err := q.client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(q.url), MessageBody: aws.String(string(body))}); err != nil {
		return fmt.Errorf("enqueue catalog job: %w", err)
	}
	return nil
}
