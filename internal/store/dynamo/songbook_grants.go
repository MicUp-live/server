package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type songbookGrantItem struct {
	SongbookID     string `dynamodbav:"songbook_id"`
	UserID         string `dynamodbav:"user_id"`
	CanEditCatalog bool   `dynamodbav:"can_edit_catalog"`
	CanHostShows   bool   `dynamodbav:"can_host_shows"`
	GrantedBy      string `dynamodbav:"granted_by"`
	CreatedAt      string `dynamodbav:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at"`
}

func songbookGrantKey(songbookID, userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"songbook_id": avString(songbookID),
		"user_id":     avString(userID),
	}
}

func (s *Store) editGrantConditionCheck(songbookID, actorID string) types.TransactWriteItem {
	return types.TransactWriteItem{
		ConditionCheck: &types.ConditionCheck{
			TableName:           aws.String(s.tables.songbookGrants),
			Key:                 songbookGrantKey(songbookID, actorID),
			ConditionExpression: aws.String("attribute_exists(songbook_id) AND can_edit_catalog = :can_edit"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":can_edit": avBool(true),
			},
		},
	}
}

func (s *Store) hostGrantConditionCheck(songbookID, actorID string) types.TransactWriteItem {
	return types.TransactWriteItem{
		ConditionCheck: &types.ConditionCheck{
			TableName:           aws.String(s.tables.songbookGrants),
			Key:                 songbookGrantKey(songbookID, actorID),
			ConditionExpression: aws.String("attribute_exists(songbook_id) AND can_host_shows = :can_host"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":can_host": avBool(true),
			},
		},
	}
}

func (s *Store) songbookActiveConditionCheck(songbookID string) types.TransactWriteItem {
	return types.TransactWriteItem{
		ConditionCheck: &types.ConditionCheck{
			TableName:           aws.String(s.tables.songbooks),
			Key:                 stringKey("id", songbookID),
			ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":not_deleting": avBool(false),
			},
		},
	}
}

func modelSongbookGrant(item songbookGrantItem) (*models.SongbookGrant, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &models.SongbookGrant{
		SongbookID:     item.SongbookID,
		UserID:         item.UserID,
		CanEditCatalog: item.CanEditCatalog,
		CanHostShows:   item.CanHostShows,
		GrantedBy:      item.GrantedBy,
		CreatedAt:      created,
		UpdatedAt:      updated,
	}, nil
}

// GetSongbookGrant returns an accepted collaborator grant using a strongly consistent read.
func (s *Store) GetSongbookGrant(ctx context.Context, songbookID, userID string) (*models.SongbookGrant, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.songbookGrants),
		Key:            songbookGrantKey(songbookID, userID),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get songbook grant: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, store.ErrGrantNotFound
	}
	var item songbookGrantItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelSongbookGrant(item)
}

// ListSongbookGrantsBySongbook lists all accepted grants for a songbook with strong consistency.
func (s *Store) ListSongbookGrantsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookGrant, error) {
	var grants []*models.SongbookGrant
	var start map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(s.tables.songbookGrants),
			KeyConditionExpression:    aws.String("songbook_id = :sb"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":sb": avString(songbookID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list songbook grants: %w", err)
		}
		for _, raw := range out.Items {
			var item songbookGrantItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			g, err := modelSongbookGrant(item)
			if err != nil {
				return nil, err
			}
			grants = append(grants, g)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].CreatedAt.Before(grants[j].CreatedAt) })
	return grants, nil
}

// ListSongbookGrantsByUser lists all accepted grants for a user with strong consistency.
func (s *Store) ListSongbookGrantsByUser(ctx context.Context, userID string) ([]*models.SongbookGrant, error) {
	var grants []*models.SongbookGrant
	var start map[string]types.AttributeValue
	for {
		out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.songbookGrants),
			FilterExpression:          aws.String("user_id = :uid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":uid": avString(userID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list user songbook grants: %w", err)
		}
		for _, raw := range out.Items {
			var item songbookGrantItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			g, err := modelSongbookGrant(item)
			if err != nil {
				return nil, err
			}
			grants = append(grants, g)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].CreatedAt.Before(grants[j].CreatedAt) })
	return grants, nil
}

// UpdateSongbookGrant updates permissions on an existing collaborator grant.
func (s *Store) UpdateSongbookGrant(ctx context.Context, grant *models.SongbookGrant) error {
	now := time.Now().UTC()
	grant.UpdatedAt = now
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:           aws.String(s.tables.songbookGrants),
					Key:                 songbookGrantKey(grant.SongbookID, grant.UserID),
					UpdateExpression:    aws.String("SET can_edit_catalog = :edit, can_host_shows = :host, updated_at = :updated"),
					ConditionExpression: aws.String("attribute_exists(songbook_id)"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":edit":    avBool(grant.CanEditCatalog),
						":host":    avBool(grant.CanHostShows),
						":updated": avString(timestamp(now)),
					},
				},
			},
			s.songbookActiveConditionCheck(grant.SongbookID),
		},
	})
	if err != nil {
		if isConditional(err) {
			if sb, sbErr := s.getSongbookItem(ctx, grant.SongbookID); sbErr == nil && sb.Deleting {
				return store.ErrSongbookDeleting
			}
			if _, gErr := s.GetSongbookGrant(ctx, grant.SongbookID, grant.UserID); errors.Is(gErr, store.ErrGrantNotFound) {
				return store.ErrGrantNotFound
			}
			return os.ErrNotExist
		}
		return fmt.Errorf("update songbook grant: %w", err)
	}
	return nil
}

// DeleteSongbookGrant revokes a collaborator's grant on a songbook.
func (s *Store) DeleteSongbookGrant(ctx context.Context, songbookID, userID string) error {
	out, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:    aws.String(s.tables.songbookGrants),
		Key:          songbookGrantKey(songbookID, userID),
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return fmt.Errorf("delete songbook grant: %w", err)
	}
	if len(out.Attributes) == 0 {
		return store.ErrGrantNotFound
	}
	return nil
}
