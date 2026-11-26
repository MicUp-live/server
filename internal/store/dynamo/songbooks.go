package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// CreateSongbook stores songbook metadata with initial sync generations.
func (s *Store) CreateSongbook(ctx context.Context, sb *models.Songbook) error {
	if strings.TrimSpace(sb.OwnerID) == "" {
		return fmt.Errorf("create songbook: owner is required: %w", os.ErrInvalid)
	}
	created, updated := nowForCreate(sb.CreatedAt)
	if !sb.UpdatedAt.IsZero() {
		updated = sb.UpdatedAt
	}
	sb.CreatedAt, sb.UpdatedAt = created, updated
	createdText := timestamp(created)
	item, err := marshal(songbookItem{ID: sb.ID, OwnerID: sb.OwnerID, PublicID: sb.PublicID, Slug: models.NormalizeSlug(sb.Slug), VenueID: sb.VenueID, CatalogVersionID: sb.CatalogVersionID,
		SongCount: sb.SongCount, CatalogGeneration: sb.CatalogGeneration, Name: sb.Name, IsPublic: sb.IsPublic, CreatedAt: createdText, CreatedKey: createdText + "#" + sb.ID, UpdatedAt: timestamp(updated)})
	if err != nil {
		return err
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(s.tables.songbooks),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(id)"),
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.users),
					Key:                 stringKey("id", sb.OwnerID),
					ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":not_deleting": avBool(false),
					},
				},
			},
		},
	})
	if err != nil {
		if isConditional(err) {
			if _, getErr := s.getSongbookItem(ctx, sb.ID); getErr == nil {
				return os.ErrExist
			}
			u, getErr := s.GetUser(ctx, sb.OwnerID)
			if errors.Is(getErr, os.ErrNotExist) {
				return os.ErrNotExist
			}
			if getErr == nil && u.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("create songbook: %w", err)
	}
	return nil
}

// UpdateSongbook changes owner-editable metadata without replacing sync state.
func (s *Store) UpdateSongbook(ctx context.Context, sb *models.Songbook) error {
	now := time.Now().UTC()
	normSlug := models.NormalizeSlug(sb.Slug)
	updateExpr := "SET #name = :name, is_public = :public, updated_at = :updated"
	exprVals := map[string]types.AttributeValue{
		":name": avString(sb.Name), ":public": avBool(sb.IsPublic), ":updated": avString(timestamp(now)),
	}
	if normSlug != "" {
		updateExpr += ", slug = :slug"
		exprVals[":slug"] = avString(normSlug)
	} else {
		updateExpr += " REMOVE slug"
	}
	exprVals[":not_deleting"] = avBool(false)
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.tables.songbooks), Key: stringKey("id", sb.ID),
		UpdateExpression:    aws.String(updateExpr),
		ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"), ReturnValues: types.ReturnValueAllNew,
		ExpressionAttributeNames: map[string]string{"#name": "name"}, ExpressionAttributeValues: exprVals})
	if isConditional(err) {
		if current, getErr := s.getSongbookItem(ctx, sb.ID); getErr == nil && current.Deleting {
			return fmt.Errorf("update songbook %s: %w", sb.ID, store.ErrSongbookDeleting)
		}
		return fmt.Errorf("update songbook %s: %w", sb.ID, errNotExist())
	}
	if err != nil {
		return fmt.Errorf("update songbook: %w", err)
	}
	var item songbookItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return err
	}
	model, err := modelSongbook(item)
	if err != nil {
		return err
	}
	*sb = *model
	return nil
}

// DeleteSongbook removes songbook metadata.
func (s *Store) DeleteSongbook(ctx context.Context, id string) error {
	result, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(s.tables.songbooks), Key: stringKey("id", id), ReturnValues: types.ReturnValueAllOld})
	if err != nil {
		return fmt.Errorf("delete songbook: %w", err)
	}
	if err := missing(result.Attributes); err != nil {
		return err
	}
	return nil
}

// GetSongbook loads songbook metadata.
func (s *Store) GetSongbook(ctx context.Context, id string) (*models.Songbook, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.songbooks), Key: stringKey("id", id), ConsistentRead: boolValue(true)})
	if err != nil {
		return nil, fmt.Errorf("get songbook: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item songbookItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelSongbook(item)
}

// GetPublicSongbook resolves a public ID or slug and rejects unpublished records.
func (s *Store) GetPublicSongbook(ctx context.Context, publicID string) (*models.Songbook, error) {
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.tables.songbooks), IndexName: aws.String(publicIndex),
			KeyConditionExpression: aws.String("public_id = :public_id"), ExclusiveStartKey: start,
			ExpressionAttributeValues: map[string]types.AttributeValue{":public_id": avString(publicID)}})
		if err != nil {
			return nil, fmt.Errorf("get public songbook: %w", err)
		}
		for _, raw := range result.Items {
			var item songbookItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			if !item.IsPublic {
				continue
			}
			return modelSongbook(item)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}

	norm := models.NormalizeSlug(publicID)
	if norm != "" {
		result, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(s.tables.songbooks),
			IndexName:                 aws.String(slugIndex),
			KeyConditionExpression:    aws.String("slug = :slug"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":slug": avString(norm)},
		})
		if err == nil {
			for _, raw := range result.Items {
				var item songbookItem
				if err := unmarshal(raw, &item); err != nil {
					return nil, err
				}
				if !item.IsPublic {
					continue
				}
				return modelSongbook(item)
			}
		}
	}

	return nil, errNotExist()
}

// GetSongbookBySlug resolves a songbook by its unique slug.
func (s *Store) GetSongbookBySlug(ctx context.Context, slug string) (*models.Songbook, error) {
	norm := models.NormalizeSlug(slug)
	if norm == "" {
		return nil, errNotExist()
	}
	result, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.tables.songbooks),
		IndexName:                 aws.String(slugIndex),
		KeyConditionExpression:    aws.String("slug = :slug"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":slug": avString(norm)},
	})
	if err != nil {
		return nil, fmt.Errorf("get songbook by slug: %w", err)
	}
	for _, raw := range result.Items {
		var item songbookItem
		if err := unmarshal(raw, &item); err != nil {
			return nil, err
		}
		return modelSongbook(item)
	}
	return nil, errNotExist()
}

// ListSongbooks scans all songbook metadata in creation order.
func (s *Store) ListSongbooks(ctx context.Context) ([]*models.Songbook, error) {
	var items []songbookItem
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(s.tables.songbooks), ExclusiveStartKey: start})
		if err != nil {
			return nil, fmt.Errorf("list songbooks: %w", err)
		}
		for _, raw := range result.Items {
			var item songbookItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return s.modelSongbooks(ctx, items)
}

// ListSongbooksByOwner returns all songbooks owned by an account using an authoritative
// strongly consistent scan on the base table.
func (s *Store) ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error) {
	var items []songbookItem
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.songbooks),
			FilterExpression:          aws.String("owner_id = :owner"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":owner": avString(ownerID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list owner songbooks: %w", err)
		}
		for _, raw := range result.Items {
			var item songbookItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return s.modelSongbooks(ctx, items)
}

func (s *Store) getSongbookItem(ctx context.Context, id string) (*songbookItem, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.songbooks), Key: stringKey("id", id), ConsistentRead: boolValue(true)})
	if err != nil {
		return nil, fmt.Errorf("get songbook state: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item songbookItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) modelSongbooks(_ context.Context, items []songbookItem) ([]*models.Songbook, error) {
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	result := make([]*models.Songbook, 0, len(items))
	for _, item := range items {
		model, err := modelSongbook(item)
		if err != nil {
			return nil, err
		}
		result = append(result, model)
	}
	return result, nil
}

func modelSongbook(item songbookItem) (*models.Songbook, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &models.Songbook{ID: item.ID, OwnerID: item.OwnerID, PublicID: item.PublicID, Slug: item.Slug, VenueID: item.VenueID, CatalogVersionID: item.CatalogVersionID,
		SongCount: item.SongCount, CatalogGeneration: item.CatalogGeneration, Deleting: item.Deleting, DeletionConfirmed: item.DeletionConfirmed, Name: item.Name, IsPublic: item.IsPublic, CreatedAt: created, UpdatedAt: updated}, nil
}

// NextCatalogGeneration atomically increments and returns the next import job generation for a songbook.
func (s *Store) NextCatalogGeneration(ctx context.Context, songbookID string) (int64, error) {
	if actorID := store.SongbookEditActorFromContext(ctx); actorID != "" {
		return s.nextCatalogGenerationFenced(ctx, songbookID, actorID)
	}
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.songbooks),
		Key:                 stringKey("id", songbookID),
		UpdateExpression:    aws.String("SET last_import_generation = if_not_exists(last_import_generation, :zero) + :one"),
		ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":zero":         avNumber(0),
			":one":          avNumber(1),
			":not_deleting": avBool(false),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		if current, getErr := s.getSongbookItem(ctx, songbookID); getErr == nil && current.Deleting {
			return 0, fmt.Errorf("next catalog generation: %w", store.ErrSongbookDeleting)
		}
		return 0, errNotExist()
	}
	if err != nil {
		return 0, fmt.Errorf("next catalog generation: %w", err)
	}
	var item songbookItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return 0, err
	}
	return item.LastImportGeneration, nil
}

// BeginSongbookDeletion marks a songbook as undergoing irreversible deletion.
// Once marked, no child items (catalog changes, import jobs, songs) or updates can commit against it.
// It returns the frozen songbook state from the barrier update.
func (s *Store) BeginSongbookDeletion(ctx context.Context, id string) (*models.Songbook, error) {
	now := time.Now().UTC()
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.songbooks),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET deleting = :true, updated_at = :updated"),
		ConditionExpression: aws.String("attribute_exists(id)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":true":    avBool(true),
			":updated": avString(timestamp(now)),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		return nil, errNotExist()
	}
	if err != nil {
		return nil, fmt.Errorf("begin songbook deletion: %w", err)
	}
	var item songbookItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelSongbook(item)
}
