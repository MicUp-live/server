// Package dynamo implements rate limit persistence using atomic DynamoDB counters.
package dynamo

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	parent "micup.live/server/internal/store"
)

// Allow atomically increments and evaluates a fixed-window token counter.
// Uses DynamoDB UpdateItem with ReturnValues=ALL_NEW and auto-expiring TTL.
func (s *Store) Allow(ctx context.Context, scope, key string, limit int, window time.Duration) (parent.RateLimitResult, error) {
	if limit < 1 {
		limit = 1
	}
	if window < time.Second {
		window = time.Minute
	}

	now := time.Now().UTC()
	windowSecs := int64(window.Seconds())
	bucket := now.Unix() / windowSecs
	resetAt := time.Unix((bucket+1)*windowSecs, 0).UTC()
	ttl := (bucket + 2) * windowSecs

	pk := fmt.Sprintf("rl#%s#%s#%d", scope, key, bucket)

	input := &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tables.rateLimits),
		Key: map[string]types.AttributeValue{
			"key": &types.AttributeValueMemberS{Value: pk},
		},
		UpdateExpression: aws.String("ADD #hits :one SET #ttl = if_not_exists(#ttl, :ttl)"),
		ExpressionAttributeNames: map[string]string{
			"#hits": "hits",
			"#ttl":  "ttl",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
			":ttl": &types.AttributeValueMemberN{Value: strconv.FormatInt(ttl, 10)},
		},
		ReturnValues: types.ReturnValueAllNew,
	}

	out, err := s.db.UpdateItem(ctx, input)
	if err != nil {
		return parent.RateLimitResult{}, fmt.Errorf("update rate limit counter: %w", err)
	}

	var hits int
	if val, ok := out.Attributes["hits"].(*types.AttributeValueMemberN); ok {
		hits, _ = strconv.Atoi(val.Value)
	}

	remaining := limit - hits
	if remaining < 0 {
		remaining = 0
	}

	return parent.RateLimitResult{
		Allowed:   hits <= limit,
		Limit:     limit,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}
