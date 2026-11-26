package dynamo

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const invitationSlotRecordType = "pending_slot"

func pendingInvitationSlotID(songbookID, recipientEmail string) string {
	return "pending#" + songbookID + "#" + normalizedEmail(recipientEmail)
}

func (s *Store) lookupIdentityUserID(ctx context.Context, normEmail string) (string, bool, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.identities),
		Key:            stringKey("email", normEmail),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return "", false, fmt.Errorf("lookup invitation recipient identity: %w", err)
	}
	if len(out.Item) == 0 {
		return "", false, nil
	}
	var item struct {
		UserID string `dynamodbav:"user_id"`
	}
	if err := unmarshal(out.Item, &item); err != nil {
		return "", false, err
	}
	return item.UserID, item.UserID != "", nil
}

// invitationNoExistingGrantChecks returns DynamoDB ConditionChecks ensuring that at
// commit time the recipient email does not already hold a collaborator grant on songbookID.
func (s *Store) invitationNoExistingGrantChecks(ctx context.Context, songbookID, normEmail string) ([]types.TransactWriteItem, error) {
	uid, exists, err := s.lookupIdentityUserID(ctx, normEmail)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []types.TransactWriteItem{
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.identities),
					Key:                 stringKey("email", normEmail),
					ConditionExpression: aws.String("attribute_not_exists(email)"),
				},
			},
		}, nil
	}
	return []types.TransactWriteItem{
		{
			ConditionCheck: &types.ConditionCheck{
				TableName:                 aws.String(s.tables.identities),
				Key:                       stringKey("email", normEmail),
				ConditionExpression:       aws.String("attribute_exists(email) AND user_id = :uid"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":uid": avString(uid)},
			},
		},
		{
			ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.songbookGrants),
				Key:                 songbookGrantKey(songbookID, uid),
				ConditionExpression: aws.String("attribute_not_exists(songbook_id)"),
			},
		},
	}, nil
}

// DeleteSongbookInvitation removes an invitation record during songbook cleanup.
// If the invitation is still pending, its matching pending slot is deleted in the same
// transaction so no invitee email is retained while preserving any slot owned by a newer
// invitation. Storage failures are propagated.
func (s *Store) DeleteSongbookInvitation(ctx context.Context, invitationID string) error {
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		inv, err := s.GetSongbookInvitation(ctx, invitationID)
		if errors.Is(err, store.ErrInvitationNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if inv.Status != models.InvitationStatusPending {
			_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(s.tables.songbookInvitations),
				Key:       stringKey("id", invitationID),
			})
			if err != nil {
				return fmt.Errorf("delete songbook invitation: %w", err)
			}
			return nil
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
				{
					Delete: &types.Delete{
						TableName:                aws.String(s.tables.songbookInvitations),
						Key:                      stringKey("id", invitationID),
						ConditionExpression:      aws.String("attribute_exists(id) AND #status = :pending"),
						ExpressionAttributeNames: map[string]string{"#status": "status"},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":pending": avString(string(models.InvitationStatusPending)),
						},
					},
				},
				{
					Delete: &types.Delete{
						TableName:           aws.String(s.tables.songbookInvitations),
						Key:                 stringKey("id", pendingInvitationSlotID(inv.SongbookID, inv.RecipientEmail)),
						ConditionExpression: aws.String("attribute_not_exists(id) OR active_invitation_id = :inv_id"),
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":inv_id": avString(invitationID),
						},
					},
				},
			},
		})
		if err == nil {
			return nil
		}
		if isConditional(err) {
			continue
		}
		return fmt.Errorf("delete songbook invitation atomically: %w", err)
	}
	return fmt.Errorf("delete songbook invitation %s: exceeded conflict retries", invitationID)
}
