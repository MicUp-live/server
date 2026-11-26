// Package catalog tests SQS catalog job submission.
package catalog

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestSQSJobQueueEnqueuesSongbookID(t *testing.T) {
	client := &fakeSQSClient{}
	queue, err := NewSQSJobQueue("https://example.test/queue", client)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(context.Background(), BuildJob{SongbookID: "book-1"}); err != nil {
		t.Fatal(err)
	}
	if client.input == nil || *client.input.MessageBody != `{"songbook_id":"book-1"}` {
		t.Fatalf("input = %+v", client.input)
	}
}

type fakeSQSClient struct{ input *sqs.SendMessageInput }

func (c *fakeSQSClient) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	c.input = in
	return &sqs.SendMessageOutput{}, nil
}
