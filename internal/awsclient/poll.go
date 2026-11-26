package awsclient

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// SQSPoller is the minimal SQS client subset needed for local queue polling.
type SQSPoller interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// PollSQS long-polls an SQS queue for local development, delivering each
// message to handle as a single-record SQSEvent and deleting on success.
func PollSQS(ctx context.Context, client SQSPoller, queueURL, workerName string, handle func(context.Context, events.SQSEvent) error) error {
	log.Printf("%s: listening on %s", workerName, queueURL)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            &queueURL,
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     10,
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("%s: receive error: %v", workerName, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, msg := range out.Messages {
			msgID := aws.ToString(msg.MessageId)
			log.Printf("%s: processing message %s", workerName, msgID)
			event := events.SQSEvent{
				Records: []events.SQSMessage{
					{
						MessageId: msgID,
						Body:      aws.ToString(msg.Body),
					},
				},
			}
			if err := handle(ctx, event); err != nil {
				log.Printf("%s: failed message %s: %v", workerName, msgID, err)
				continue
			}
			if _, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &queueURL,
				ReceiptHandle: msg.ReceiptHandle,
			}); err != nil {
				log.Printf("%s: delete message %s failed: %v", workerName, msgID, err)
				continue
			}
			log.Printf("%s: finished message %s", workerName, msgID)
		}
	}
}
