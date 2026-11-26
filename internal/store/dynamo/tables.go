package dynamo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// EnsureTables creates development tables when an explicit endpoint is configured.
func (s *Store) EnsureTables(ctx context.Context) error {
	if s.endpoint == "" {
		return fmt.Errorf("EnsureTables is restricted to an explicit DynamoDB Local/dev endpoint")
	}
	definitions := []struct {
		name       string
		attributes []types.AttributeDefinition
		key        []types.KeySchemaElement
		indexes    []types.GlobalSecondaryIndex
	}{
		{name: s.tables.users, attributes: attrs("id"), key: hashKey("id")},
		{name: s.tables.identities, attributes: attrs("email"), key: hashKey("email")},
		{name: s.tables.sessions, attributes: attrs("token_hash"), key: hashKey("token_hash")},
		{name: s.tables.songbooks, attributes: attrs("id", "public_id", "slug"), key: hashKey("id"), indexes: []types.GlobalSecondaryIndex{
			gsi(publicIndex, hashKey("public_id")), gsi(slugIndex, hashKey("slug")),
		}},
		{name: s.tables.eventRequests, attributes: attrs("event_id", "id"), key: hashRangeKey("event_id", "id"), indexes: []types.GlobalSecondaryIndex{
			gsi(requestIndex, hashKey("id")),
		}},
		{name: s.tables.events, attributes: attrs("id", "join_code"), key: hashKey("id"), indexes: []types.GlobalSecondaryIndex{
			gsi(joinCodeIndex, hashKey("join_code")),
		}},
		{name: s.tables.passkeys, attributes: attrs("id"), key: hashKey("id")},
		{name: s.tables.webauthn, attributes: attrs("session_id"), key: hashKey("session_id")},
		{name: s.tables.catalogs, attributes: attrs("id"), key: hashKey("id")},
		{name: s.tables.importJobs, attributes: attrs("id"), key: hashKey("id")},
		{name: s.tables.changes, attributes: attrs("songbook_id", "song_id"), key: hashRangeKey("songbook_id", "song_id")},
		{name: s.tables.developerKeys, attributes: attrs("key_hash"), key: hashKey("key_hash")},
		{name: s.tables.rateLimits, attributes: attrs("key"), key: hashKey("key")},
		{name: s.tables.idempotency, attributes: attrs("idempotency_key"), key: hashKey("idempotency_key")},
		{name: s.tables.songbookGrants, attributes: attrs("songbook_id", "user_id"), key: hashRangeKey("songbook_id", "user_id")},
		{name: s.tables.songbookInvitations, attributes: attrs("id", "token_hash"), key: hashKey("id"), indexes: []types.GlobalSecondaryIndex{
			gsi(tokenHashIndex, hashKey("token_hash")),
		}},
	}
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	setErr := func(err error) {
		if err != nil {
			errOnce.Do(func() { firstErr = err })
		}
	}
	for _, definition := range definitions {
		wg.Add(1)
		go func(d struct {
			name       string
			attributes []types.AttributeDefinition
			key        []types.KeySchemaElement
			indexes    []types.GlobalSecondaryIndex
		}) {
			defer wg.Done()
			out, err := s.db.CreateTable(ctx, &dynamodb.CreateTableInput{TableName: aws.String(d.name), BillingMode: types.BillingModePayPerRequest,
				AttributeDefinitions: d.attributes, KeySchema: d.key, GlobalSecondaryIndexes: d.indexes})
			if err != nil {
				var inUse *types.ResourceInUseException
				if !errors.As(err, &inUse) {
					setErr(fmt.Errorf("create table %s: %w", d.name, err))
					return
				}
			} else if out.TableDescription != nil && out.TableDescription.TableStatus == types.TableStatusActive {
				return
			}
			waiter := dynamodb.NewTableExistsWaiter(s.db)
			if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(d.name)}, time.Minute); err != nil {
				setErr(fmt.Errorf("wait for table %s: %w", d.name, err))
				return
			}
		}(definition)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	for _, tbl := range []string{s.tables.sessions, s.tables.webauthn, s.tables.eventRequests, s.tables.events, s.tables.rateLimits, s.tables.idempotency} {
		wg.Add(1)
		go func(table string) {
			defer wg.Done()
			ttl, err := s.db.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{TableName: aws.String(table)})
			if err != nil {
				setErr(fmt.Errorf("describe TTL for %s: %w", table, err))
				return
			}
			if ttl.TimeToLiveDescription == nil || ttl.TimeToLiveDescription.TimeToLiveStatus == types.TimeToLiveStatusDisabled {
				_, err = s.db.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{TableName: aws.String(table),
					TimeToLiveSpecification: &types.TimeToLiveSpecification{AttributeName: aws.String("ttl"), Enabled: boolValue(true)}})
				if err != nil {
					setErr(fmt.Errorf("enable TTL for %s: %w", table, err))
					return
				}
			}
		}(tbl)
	}
	wg.Wait()
	return firstErr
}

func attrs(names ...string) []types.AttributeDefinition {
	result := make([]types.AttributeDefinition, 0, len(names))
	for _, name := range names {
		result = append(result, types.AttributeDefinition{AttributeName: aws.String(name), AttributeType: types.ScalarAttributeTypeS})
	}
	return result
}

func hashKey(name string) []types.KeySchemaElement {
	return []types.KeySchemaElement{{AttributeName: aws.String(name), KeyType: types.KeyTypeHash}}
}

func hashRangeKey(hash, ranged string) []types.KeySchemaElement {
	return []types.KeySchemaElement{{AttributeName: aws.String(hash), KeyType: types.KeyTypeHash}, {AttributeName: aws.String(ranged), KeyType: types.KeyTypeRange}}
}

func gsi(name string, key []types.KeySchemaElement) types.GlobalSecondaryIndex {
	return types.GlobalSecondaryIndex{IndexName: aws.String(name), KeySchema: key, Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll}}
}
