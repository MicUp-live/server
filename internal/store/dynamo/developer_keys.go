package dynamo

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// developerKeyItem is the DynamoDB projection of a scoped Personal Access Token.
// Only the SHA-256 hash of the raw key is persisted; the raw secret is shown
// once at creation and never stored.
type developerKeyItem struct {
	KeyHash      string   `dynamodbav:"key_hash"`
	OwnerID      string   `dynamodbav:"owner_id"`
	Name         string   `dynamodbav:"name"`
	KeyPrefix    string   `dynamodbav:"key_prefix"`
	Scopes       []string `dynamodbav:"scopes"`
	ScopeVersion int      `dynamodbav:"scope_version,omitempty"`
	CreatedAt    string   `dynamodbav:"created_at"`
	CreatedKey   string   `dynamodbav:"created_key"`
	LastUsedAt   string   `dynamodbav:"last_used_at,omitempty"`
	ExpiresAt    string   `dynamodbav:"expires_at,omitempty"`
	Revoked      bool     `dynamodbav:"revoked"`
	RevokedAt    string   `dynamodbav:"revoked_at,omitempty"`
}

// hashRawKey returns the SHA-256 hex digest of a raw API key token.
func hashRawKey(rawKey string) string {
	digest := sha256.Sum256([]byte(rawKey))
	return tokenKey(digest[:])
}

// CreateDeveloperKey hashes rawKey (SHA-256 hex) and persists it as the key's ID.
func (s *Store) CreateDeveloperKey(ctx context.Context, rawKey string, key *models.DeveloperKey) error {
	created := key.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
		key.CreatedAt = created
	}
	item := developerKeyItem{
		KeyHash:      hashRawKey(rawKey),
		OwnerID:      key.OwnerID,
		Name:         key.Name,
		KeyPrefix:    key.KeyPrefix,
		Scopes:       key.Scopes,
		ScopeVersion: 1,
		CreatedAt:    timestamp(created),
		CreatedKey:   fmt.Sprintf("%d-%s", created.UnixNano(), created.Format("150405.000000")),
		Revoked:      key.Revoked,
	}
	if !key.LastUsedAt.IsZero() {
		item.LastUsedAt = timestamp(key.LastUsedAt)
	}
	if !key.ExpiresAt.IsZero() {
		item.ExpiresAt = timestamp(key.ExpiresAt)
	}
	if !key.RevokedAt.IsZero() {
		item.RevokedAt = timestamp(key.RevokedAt)
	}
	av, err := marshal(item)
	if err != nil {
		return err
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(s.tables.developerKeys),
					Item:                av,
					ConditionExpression: aws.String("attribute_not_exists(key_hash)"),
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.users),
					Key:                 stringKey("id", key.OwnerID),
					ConditionExpression: aws.String("attribute_exists(id) AND " + notDeletingCondition),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":not_deleting": avBool(false),
					},
				},
			},
		},
	})
	if err != nil {
		if isConditional(err) {
			if u, getErr := s.GetUser(ctx, key.OwnerID); getErr == nil && u.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("create developer key: %w", err)
	}
	return nil
}

// GetDeveloperKey resolves a raw key token to its stored record by hashing it.
func (s *Store) GetDeveloperKey(ctx context.Context, rawKey string) (*models.DeveloperKey, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.developerKeys),
		Key:            stringKey("key_hash", hashRawKey(rawKey)),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get developer key: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item developerKeyItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return itemToModel(item), nil
}

// ListDeveloperKeys returns every key issued to a single owner using an authoritative
// strongly consistent scan on the base table.
func (s *Store) ListDeveloperKeys(ctx context.Context, ownerID string) ([]*models.DeveloperKey, error) {
	var keys []*models.DeveloperKey
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.developerKeys),
			FilterExpression:          aws.String("owner_id = :oid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":oid": avString(ownerID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list developer keys: %w", err)
		}
		for _, raw := range result.Items {
			var item developerKeyItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			keys = append(keys, itemToModel(item))
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return keys, nil
}

// RevokeDeveloperKey deletes a key after verifying it belongs to ownerID.
func (s *Store) RevokeDeveloperKey(ctx context.Context, id, ownerID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(s.tables.developerKeys),
		Key:                 stringKey("key_hash", id),
		ConditionExpression: aws.String("owner_id = :oid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":oid": &types.AttributeValueMemberS{Value: ownerID},
		},
	})
	if err != nil {
		if isConditional(err) {
			res, getErr := s.db.GetItem(ctx, &dynamodb.GetItemInput{
				TableName:      aws.String(s.tables.developerKeys),
				Key:            stringKey("key_hash", id),
				ConsistentRead: boolValue(true),
			})
			if getErr == nil && len(res.Item) == 0 {
				return os.ErrNotExist
			}
		}
		return fmt.Errorf("revoke developer key: %w", err)
	}
	return nil
}

// TouchDeveloperKeyLastUsed records the last use of a key by its stored ID.
func (s *Store) TouchDeveloperKeyLastUsed(ctx context.Context, id string) error {
	nowStr := timestamp(time.Now().UTC())
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.developerKeys),
		Key:                 stringKey("key_hash", id),
		ConditionExpression: aws.String("attribute_exists(key_hash) AND revoked = :false"),
		UpdateExpression:    aws.String("SET last_used_at = :lua"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":lua":   &types.AttributeValueMemberS{Value: nowStr},
			":false": avBool(false),
		},
	})
	if err != nil {
		return fmt.Errorf("touch developer key last used: %w", err)
	}
	return nil
}

func itemToModel(item developerKeyItem) *models.DeveloperKey {
	key := &models.DeveloperKey{
		ID:           item.KeyHash,
		OwnerID:      item.OwnerID,
		Name:         item.Name,
		KeyPrefix:    item.KeyPrefix,
		Scopes:       item.Scopes,
		ScopeVersion: item.ScopeVersion,
		Revoked:      item.Revoked,
	}
	if t, err := parseTime(item.CreatedAt); err == nil {
		key.CreatedAt = t
	}
	if t, err := parseTime(item.LastUsedAt); err == nil {
		key.LastUsedAt = t
	}
	if t, err := parseTime(item.ExpiresAt); err == nil {
		key.ExpiresAt = t
	}
	if t, err := parseTime(item.RevokedAt); err == nil {
		key.RevokedAt = t
	}
	return key
}
