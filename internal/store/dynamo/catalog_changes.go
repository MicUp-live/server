package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type catalogChangeItem struct {
	SongbookID string       `dynamodbav:"songbook_id"`
	SongID     string       `dynamodbav:"song_id"`
	Song       *models.Song `dynamodbav:"song,omitempty"`
	Deleted    bool         `dynamodbav:"deleted"`
	Clear      bool         `dynamodbav:"clear"`
	UpdatedAt  string       `dynamodbav:"updated_at"`
}

// PutCatalogChange stores or replaces one pending songbook delta.
func (s *Store) PutCatalogChange(ctx context.Context, change *models.CatalogChange) error {
	if change == nil || strings.TrimSpace(change.SongbookID) == "" || strings.TrimSpace(change.SongID) == "" {
		return fmt.Errorf("put catalog change: songbook and song are required")
	}
	if change.UpdatedAt.IsZero() {
		change.UpdatedAt = time.Now().UTC()
	}
	item, err := marshal(catalogChangeItem{SongbookID: change.SongbookID, SongID: change.SongID, Song: change.Song, Deleted: change.Deleted, Clear: change.Clear, UpdatedAt: timestamp(change.UpdatedAt)})
	if err != nil {
		return err
	}
	txItems := s.appendEditFence(ctx, change.SongbookID, []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName: aws.String(s.tables.changes),
				Item:      item,
			},
		},
		s.songbookActiveConditionCheck(change.SongbookID),
	})
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: txItems,
	})
	if err != nil {
		if isConditional(err) {
			if sb, getErr := s.getSongbookItem(ctx, change.SongbookID); getErr == nil && sb.Deleting {
				return fmt.Errorf("put catalog change: %w", store.ErrSongbookDeleting)
			}
			if gErr := s.checkEditGrantError(ctx, change.SongbookID); gErr != nil {
				return fmt.Errorf("put catalog change: %w", gErr)
			}
			return fmt.Errorf("put catalog change: %w", os.ErrNotExist)
		}
		return fmt.Errorf("put catalog change: %w", err)
	}
	return nil
}

// ListCatalogChanges lists pending deltas for one songbook.
func (s *Store) ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	paginator := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.changes),
		KeyConditionExpression: aws.String("songbook_id = :book"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":book": avString(songbookID),
		},
		ConsistentRead: aws.Bool(true),
	})
	var changes []*models.CatalogChange
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, raw := range page.Items {
			var item catalogChangeItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			updated, _ := time.Parse(time.RFC3339Nano, item.UpdatedAt)
			changes = append(changes, &models.CatalogChange{SongbookID: item.SongbookID, SongID: item.SongID, Song: item.Song, Deleted: item.Deleted, Clear: item.Clear, UpdatedAt: updated})
		}
	}
	return changes, nil
}

// DeleteCatalogChange removes a pending delta after compaction. If updatedAt is non-zero,
// it uses a conditional delete to ensure newer changes written during compaction are preserved.
func (s *Store) DeleteCatalogChange(ctx context.Context, songbookID, songID string, updatedAt time.Time) error {
	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tables.changes),
		Key: map[string]types.AttributeValue{
			"songbook_id": avString(songbookID),
			"song_id":     avString(songID),
		},
	}
	if !updatedAt.IsZero() {
		input.ConditionExpression = aws.String("attribute_not_exists(updated_at) OR updated_at = :updated_at")
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":updated_at": avString(timestamp(updatedAt)),
		}
	}
	_, err := s.db.DeleteItem(ctx, input)
	if err != nil {
		var ccfe *types.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			// A newer change arrived during compaction build; preserve it for the next run.
			return nil
		}
		return fmt.Errorf("delete catalog change: %w", err)
	}
	return nil
}
