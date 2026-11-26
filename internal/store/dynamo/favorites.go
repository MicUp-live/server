package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	parent "micup.live/server/internal/store"
)

// GetUserFavorites retrieves the bookmarked songs and artists for a user.
func (s *Store) GetUserFavorites(ctx context.Context, userID string) (*models.UserFavorites, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.users),
		Key:            stringKey("id", userID),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get user favorites: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}

	var item struct {
		FavoriteSongs   []models.FavoriteSong   `dynamodbav:"favorite_songs"`
		FavoriteArtists []models.FavoriteArtist `dynamodbav:"favorite_artists"`
	}
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, fmt.Errorf("unmarshal user favorites: %w", err)
	}

	songs := item.FavoriteSongs
	if songs == nil {
		songs = []models.FavoriteSong{}
	}
	artists := item.FavoriteArtists
	if artists == nil {
		artists = []models.FavoriteArtist{}
	}

	return &models.UserFavorites{
		Songs:   songs,
		Artists: artists,
	}, nil
}

// SaveUserFavorites persists the user's bookmarked songs and artists on the users table item.
func (s *Store) SaveUserFavorites(ctx context.Context, userID string, favs *models.UserFavorites) error {
	if favs == nil {
		favs = &models.UserFavorites{
			Songs:   []models.FavoriteSong{},
			Artists: []models.FavoriteArtist{},
		}
	}
	if favs.Songs == nil {
		favs.Songs = []models.FavoriteSong{}
	}
	if favs.Artists == nil {
		favs.Artists = []models.FavoriteArtist{}
	}

	songsAV, err := attributevalue.MarshalList(favs.Songs)
	if err != nil {
		return fmt.Errorf("marshal favorite songs: %w", err)
	}
	artistsAV, err := attributevalue.MarshalList(favs.Artists)
	if err != nil {
		return fmt.Errorf("marshal favorite artists: %w", err)
	}
	now := timestamp(time.Now().UTC())

	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.users),
		Key:                 stringKey("id", userID),
		ConditionExpression: aws.String("attribute_exists(id) AND " + notDeletingCondition),
		UpdateExpression:    aws.String("SET favorite_songs = :songs, favorite_artists = :artists, updated_at = :updated_at"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":songs":        &types.AttributeValueMemberL{Value: songsAV},
			":artists":      &types.AttributeValueMemberL{Value: artistsAV},
			":updated_at":   avString(now),
			":not_deleting": avBool(false),
		},
	})
	if err != nil {
		var conflict *types.ConditionalCheckFailedException
		if errors.As(err, &conflict) {
			if user, getErr := s.GetUser(ctx, userID); getErr == nil && user.Deleting {
				return parent.ErrAccountDeleting
			}
			return os.ErrNotExist
		}
		return fmt.Errorf("save user favorites: %w", err)
	}

	return nil
}
