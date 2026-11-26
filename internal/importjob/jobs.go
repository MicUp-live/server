package importjob

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Job identifies a manifest-backed Assistant Importer job.
type Job struct {
	ID           string `json:"id"`
	ManifestKey  string `json:"manifest_key"`
	PruneMissing bool   `json:"prune_missing"`
	Generation   int64  `json:"generation,omitempty"`
}

// Queue submits durable import jobs to SQS.
type Queue struct {
	URL    string
	Client *sqs.Client
}

func (q *Queue) Enqueue(ctx context.Context, job Job) error {
	if q == nil || q.Client == nil || q.URL == "" || job.ID == "" || job.ManifestKey == "" {
		return fmt.Errorf("enqueue import job: invalid queue or job")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode import job: %w", err)
	}
	_, err = q.Client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(q.URL), MessageBody: aws.String(string(body))})
	if err != nil {
		return fmt.Errorf("enqueue import job: %w", err)
	}
	return nil
}
