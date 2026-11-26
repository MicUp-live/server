package awsclient

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestLoadUsesLocalCredentialsWhenAnEndpointIsConfigured(t *testing.T) {
	cfg, err := Load(context.Background(), Config{Region: "us-east-2", S3Endpoint: "http://localhost:4566/"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve credentials: %v", err)
	}
	if creds.AccessKeyID != "local" || creds.SecretAccessKey != "local" {
		t.Fatalf("credentials = %#v; want local static credentials", creds)
	}
}

func TestLoadKeepsDefaultCredentialResolutionWithoutEndpoints(t *testing.T) {
	cfg, err := Load(context.Background(), Config{Region: "us-east-2"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Credentials == nil {
		t.Fatal("credentials provider is nil")
	}
}

func TestNormalize(t *testing.T) {
	if got := normalize(" https://localhost:4566/// "); got != "https://localhost:4566" {
		t.Fatalf("normalize() = %q", got)
	}
	if got := firstEndpoint(" ", "http://one/", "http://two/"); got != "http://one" {
		t.Fatalf("firstEndpoint() = %q", got)
	}
}

func TestNewClientsConfigureCustomEndpoints(t *testing.T) {
	cfg := aws.Config{Region: "us-east-2"}
	s3c := NewS3(cfg, "http://s3.local/")
	if s3c == nil || s3c.Options().BaseEndpoint == nil || *s3c.Options().BaseEndpoint != "http://s3.local" || !s3c.Options().UsePathStyle {
		t.Fatalf("unexpected S3 options: %#v", s3c.Options())
	}
	sqsc := NewSQS(cfg, "http://sqs.local/")
	if sqsc == nil || sqsc.Options().BaseEndpoint == nil || *sqsc.Options().BaseEndpoint != "http://sqs.local" {
		t.Fatalf("unexpected SQS options: %#v", sqsc.Options())
	}
	sesc := NewSES(cfg, "http://ses.local/")
	if sesc == nil || sesc.Options().BaseEndpoint == nil || *sesc.Options().BaseEndpoint != "http://ses.local" {
		t.Fatalf("unexpected SES options: %#v", sesc.Options())
	}
}

type fakeSQSPoller struct {
	batches  [][]sqstypes.Message
	deleted  []string
	cancelFn context.CancelFunc
}

func (f *fakeSQSPoller) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if len(f.batches) == 0 {
		f.cancelFn()
		return nil, ctx.Err()
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return &sqs.ReceiveMessageOutput{Messages: batch}, nil
}

func (f *fakeSQSPoller) DeleteMessage(_ context.Context, params *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.deleted = append(f.deleted, aws.ToString(params.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

func TestPollSQSDeletesOnSuccessAndPreservesOnFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := &fakeSQSPoller{
		cancelFn: cancel,
		batches: [][]sqstypes.Message{
			{
				{MessageId: aws.String("m1"), Body: aws.String("ok"), ReceiptHandle: aws.String("rh-1")},
				{MessageId: aws.String("m2"), Body: aws.String("fail"), ReceiptHandle: aws.String("rh-2")},
			},
		},
	}

	var seen []string
	err := PollSQS(ctx, poller, "http://sqs.local/q", "test-worker", func(_ context.Context, ev events.SQSEvent) error {
		body := ev.Records[0].Body
		seen = append(seen, body)
		if body == "fail" {
			return errors.New("boom")
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PollSQS err = %v; want context.Canceled", err)
	}
	if len(seen) != 2 || seen[0] != "ok" || seen[1] != "fail" {
		t.Fatalf("seen bodies = %v; want [ok fail]", seen)
	}
	if len(poller.deleted) != 1 || poller.deleted[0] != "rh-1" {
		t.Fatalf("deleted receipts = %v; want [rh-1]", poller.deleted)
	}
}
