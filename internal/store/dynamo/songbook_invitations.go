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

type songbookInvitationItem struct {
	ID               string `dynamodbav:"id"`
	RecordType       string `dynamodbav:"record_type,omitempty"`
	SongbookID       string `dynamodbav:"songbook_id"`
	OwnerID          string `dynamodbav:"owner_id"`
	RecipientEmail   string `dynamodbav:"recipient_email"`
	CanEditCatalog   bool   `dynamodbav:"can_edit_catalog"`
	CanHostShows     bool   `dynamodbav:"can_host_shows"`
	TokenHash        string `dynamodbav:"token_hash,omitempty"`
	Status           string `dynamodbav:"status"`
	ExpiresAt        string `dynamodbav:"expires_at"`
	ExpiresAtUnix    int64  `dynamodbav:"expires_at_unix"`
	LastSentAt       string `dynamodbav:"last_sent_at,omitempty"`
	SendError        string `dynamodbav:"send_error,omitempty"`
	AcceptedByUserID string `dynamodbav:"accepted_by_user_id,omitempty"`
	AcceptedAt       string `dynamodbav:"accepted_at,omitempty"`
	CreatedAt        string `dynamodbav:"created_at"`
	CreatedKey       string `dynamodbav:"created_key"`
	UpdatedAt        string `dynamodbav:"updated_at"`
}

func modelSongbookInvitation(item songbookInvitationItem) (*models.SongbookInvitation, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	expires, err := parseTime(item.ExpiresAt)
	if err != nil {
		return nil, err
	}
	var lastSent *time.Time
	if item.LastSentAt != "" {
		if t, err := parseTime(item.LastSentAt); err == nil {
			lastSent = &t
		}
	}
	var acceptedAt *time.Time
	if item.AcceptedAt != "" {
		if t, err := parseTime(item.AcceptedAt); err == nil {
			acceptedAt = &t
		}
	}
	return &models.SongbookInvitation{
		ID:               item.ID,
		SongbookID:       item.SongbookID,
		OwnerID:          item.OwnerID,
		RecipientEmail:   item.RecipientEmail,
		CanEditCatalog:   item.CanEditCatalog,
		CanHostShows:     item.CanHostShows,
		TokenHash:        item.TokenHash,
		Status:           models.SongbookInvitationStatus(item.Status),
		ExpiresAt:        expires,
		LastSentAt:       lastSent,
		SendError:        item.SendError,
		AcceptedByUserID: item.AcceptedByUserID,
		AcceptedAt:       acceptedAt,
		CreatedAt:        created,
		UpdatedAt:        updated,
	}, nil
}

// CreateSongbookInvitation atomically persists a pending invitation and replaces any
// prior pending invitations for the same recipient, invalidating their earlier links.
func (s *Store) CreateSongbookInvitation(ctx context.Context, inv *models.SongbookInvitation, replaceIDs []string) error {
	created, updated := nowForCreate(inv.CreatedAt)
	inv.CreatedAt = created
	inv.UpdatedAt = updated
	inv.Status = models.InvitationStatusPending
	inv.RecipientEmail = normalizedEmail(inv.RecipientEmail)

	item := songbookInvitationItem{
		ID:             inv.ID,
		SongbookID:     inv.SongbookID,
		OwnerID:        inv.OwnerID,
		RecipientEmail: inv.RecipientEmail,
		CanEditCatalog: inv.CanEditCatalog,
		CanHostShows:   inv.CanHostShows,
		TokenHash:      inv.TokenHash,
		Status:         string(inv.Status),
		ExpiresAt:      timestamp(inv.ExpiresAt),
		ExpiresAtUnix:  inv.ExpiresAt.UTC().UnixNano(),
		CreatedAt:      timestamp(created),
		CreatedKey:     timestamp(created) + "#" + inv.ID,
		UpdatedAt:      timestamp(updated),
	}
	payload, err := marshal(item)
	if err != nil {
		return err
	}

	txItems := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.songbookInvitations),
				Item:                payload,
				ConditionExpression: aws.String("attribute_not_exists(id)"),
			},
		},
		{
			ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.songbooks),
				Key:                 stringKey("id", inv.SongbookID),
				ConditionExpression: aws.String("attribute_exists(id) AND owner_id = :owner AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":owner":        avString(inv.OwnerID),
					":not_deleting": avBool(false),
				},
			},
		},
	}

	var cleanReplace []string
	for _, oldID := range replaceIDs {
		if strings.TrimSpace(oldID) == "" || oldID == inv.ID {
			continue
		}
		cleanReplace = append(cleanReplace, oldID)
		txItems = append(txItems, types.TransactWriteItem{
			Update: &types.Update{
				TableName:                aws.String(s.tables.songbookInvitations),
				Key:                      stringKey("id", oldID),
				UpdateExpression:         aws.String("SET #status = :replaced, updated_at = :updated REMOVE token_hash"),
				ConditionExpression:      aws.String("attribute_exists(id) AND songbook_id = :sb AND #status = :pending"),
				ExpressionAttributeNames: map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":replaced": avString(string(models.InvitationStatusReplaced)),
					":pending":  avString(string(models.InvitationStatusPending)),
					":sb":       avString(inv.SongbookID),
					":updated":  avString(timestamp(updated)),
				},
			},
		})
	}

	slotID := pendingInvitationSlotID(inv.SongbookID, inv.RecipientEmail)
	slotPut := &types.Put{
		TableName: aws.String(s.tables.songbookInvitations),
		Item: map[string]types.AttributeValue{
			"id":                   avString(slotID),
			"record_type":          avString(invitationSlotRecordType),
			"songbook_id":          avString(inv.SongbookID),
			"recipient_email":      avString(inv.RecipientEmail),
			"active_invitation_id": avString(inv.ID),
			"updated_at":           avString(timestamp(updated)),
		},
	}
	if len(cleanReplace) == 0 {
		slotPut.ConditionExpression = aws.String("attribute_not_exists(id)")
	} else {
		slotPut.ConditionExpression = aws.String("attribute_not_exists(id) OR active_invitation_id = :prev_id")
		slotPut.ExpressionAttributeValues = map[string]types.AttributeValue{":prev_id": avString(cleanReplace[0])}
	}
	txItems = append(txItems, types.TransactWriteItem{Put: slotPut})

	grantChecks, err := s.invitationNoExistingGrantChecks(ctx, inv.SongbookID, inv.RecipientEmail)
	if err != nil {
		return err
	}
	txItems = append(txItems, grantChecks...)

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems})
	if err != nil {
		if isConditional(err) {
			if sb, sbErr := s.getSongbookItem(ctx, inv.SongbookID); sbErr == nil && sb.Deleting {
				return store.ErrSongbookDeleting
			}
			if uid, ok, idErr := s.lookupIdentityUserID(ctx, inv.RecipientEmail); idErr == nil && ok {
				if _, gErr := s.GetSongbookGrant(ctx, inv.SongbookID, uid); gErr == nil {
					return store.ErrAlreadyCollaborator
				}
			}
			return store.ErrInvitationConflict
		}
		return fmt.Errorf("create songbook invitation: %w", err)
	}
	return nil
}

// GetSongbookInvitation loads an invitation by ID with strong consistency.
func (s *Store) GetSongbookInvitation(ctx context.Context, id string) (*models.SongbookInvitation, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.songbookInvitations),
		Key:            stringKey("id", id),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get songbook invitation: %w", err)
	}
	if err := missing(out.Item); err != nil {
		return nil, store.ErrInvitationNotFound
	}
	var item songbookInvitationItem
	if err := unmarshal(out.Item, &item); err != nil {
		return nil, err
	}
	return modelSongbookInvitation(item)
}

// GetSongbookInvitationByTokenHash resolves an invitation by its SHA-256 token digest,
// followed by a strongly consistent base-table read.
func (s *Store) GetSongbookInvitationByTokenHash(ctx context.Context, tokenHash string) (*models.SongbookInvitation, error) {
	if strings.TrimSpace(tokenHash) == "" {
		return nil, store.ErrInvitationNotFound
	}
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.tables.songbookInvitations),
		IndexName:                 aws.String(tokenHashIndex),
		KeyConditionExpression:    aws.String("token_hash = :th"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":th": avString(tokenHash)},
		Limit:                     aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("get songbook invitation by token: %w", err)
	}
	if len(out.Items) == 0 {
		return nil, store.ErrInvitationNotFound
	}
	var item songbookInvitationItem
	if err := unmarshal(out.Items[0], &item); err != nil {
		return nil, err
	}
	fresh, err := s.GetSongbookInvitation(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if fresh.TokenHash != tokenHash {
		return nil, store.ErrInvitationNotFound
	}
	return fresh, nil
}

// ListSongbookInvitationsBySongbook lists all invitations for a songbook with strong consistency.
func (s *Store) ListSongbookInvitationsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookInvitation, error) {
	var invitations []*models.SongbookInvitation
	var start map[string]types.AttributeValue
	for {
		out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.songbookInvitations),
			FilterExpression:          aws.String("songbook_id = :sb"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":sb": avString(songbookID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list songbook invitations: %w", err)
		}
		for _, raw := range out.Items {
			var item songbookInvitationItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			if item.RecordType == invitationSlotRecordType {
				continue
			}
			inv, err := modelSongbookInvitation(item)
			if err != nil {
				return nil, err
			}
			invitations = append(invitations, inv)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	sort.Slice(invitations, func(i, j int) bool { return invitations[i].CreatedAt.After(invitations[j].CreatedAt) })
	return invitations, nil
}

// RotateSongbookInvitationToken issues a fresh token digest and expiry on a pending invitation,
// immediately invalidating any previous acceptance link.
func (s *Store) RotateSongbookInvitationToken(ctx context.Context, songbookID, invitationID, newTokenHash string, newExpiresAt time.Time) (*models.SongbookInvitation, error) {
	now := time.Now().UTC()
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:                aws.String(s.tables.songbookInvitations),
					Key:                      stringKey("id", invitationID),
					UpdateExpression:         aws.String("SET token_hash = :th, expires_at = :exp, expires_at_unix = :exp_unix, updated_at = :updated REMOVE send_error, last_sent_at"),
					ConditionExpression:      aws.String("attribute_exists(id) AND songbook_id = :sb AND #status = :pending"),
					ExpressionAttributeNames: map[string]string{"#status": "status"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":th":       avString(newTokenHash),
						":exp":      avString(timestamp(newExpiresAt)),
						":exp_unix": avNumber(newExpiresAt.UTC().UnixNano()),
						":updated":  avString(timestamp(now)),
						":sb":       avString(songbookID),
						":pending":  avString(string(models.InvitationStatusPending)),
					},
				},
			},
			s.songbookActiveConditionCheck(songbookID),
		},
	})
	if err != nil {
		if isConditional(err) {
			if sb, sbErr := s.getSongbookItem(ctx, songbookID); sbErr == nil && sb.Deleting {
				return nil, store.ErrSongbookDeleting
			}
			inv, getErr := s.GetSongbookInvitation(ctx, invitationID)
			if errors.Is(getErr, store.ErrInvitationNotFound) || (getErr == nil && inv.SongbookID != songbookID) {
				return nil, store.ErrInvitationNotFound
			}
			return nil, store.ErrInvitationInvalid
		}
		return nil, fmt.Errorf("rotate songbook invitation token: %w", err)
	}
	return s.GetSongbookInvitation(ctx, invitationID)
}

// RecordSongbookInvitationDelivery records delivery timestamp or provider error for the
// specific send attempt identified by expectedTokenHash without altering status.
// If a newer resend rotated token_hash or the invitation is no longer pending, it returns
// store.ErrInvitationDeliverySuperseded so an older attempt cannot overwrite newer status.
func (s *Store) RecordSongbookInvitationDelivery(ctx context.Context, invitationID, expectedTokenHash string, sentAt *time.Time, sendErr string) error {
	now := time.Now().UTC()
	var setParts []string
	var removeParts []string
	vals := map[string]types.AttributeValue{
		":updated":     avString(timestamp(now)),
		":pending":     avString(string(models.InvitationStatusPending)),
		":expected_th": avString(expectedTokenHash),
	}
	setParts = append(setParts, "updated_at = :updated")
	if sentAt != nil && !sentAt.IsZero() {
		setParts = append(setParts, "last_sent_at = :sent")
		vals[":sent"] = avString(timestamp(*sentAt))
	}
	if strings.TrimSpace(sendErr) != "" {
		setParts = append(setParts, "send_error = :err")
		vals[":err"] = avString(sendErr)
	} else {
		removeParts = append(removeParts, "send_error")
	}
	expr := "SET " + strings.Join(setParts, ", ")
	if len(removeParts) > 0 {
		expr += " REMOVE " + strings.Join(removeParts, ", ")
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tables.songbookInvitations),
		Key:                       stringKey("id", invitationID),
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       aws.String("attribute_exists(id) AND #status = :pending AND token_hash = :expected_th"),
		ExpressionAttributeNames:  map[string]string{"#status": "status"},
		ExpressionAttributeValues: vals,
	})
	if isConditional(err) {
		return store.ErrInvitationDeliverySuperseded
	}
	return err
}

// CancelSongbookInvitation cancels a pending invitation, removes its token digest, and releases its pending slot.
func (s *Store) CancelSongbookInvitation(ctx context.Context, songbookID, invitationID string) error {
	inv, err := s.GetSongbookInvitation(ctx, invitationID)
	if err != nil || inv.SongbookID != songbookID {
		if err == nil || errors.Is(err, store.ErrInvitationNotFound) {
			return store.ErrInvitationNotFound
		}
		return err
	}
	if inv.Status != models.InvitationStatusPending {
		return store.ErrInvitationInvalid
	}
	now := time.Now().UTC()
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:                aws.String(s.tables.songbookInvitations),
					Key:                      stringKey("id", invitationID),
					UpdateExpression:         aws.String("SET #status = :canceled, updated_at = :updated REMOVE token_hash"),
					ConditionExpression:      aws.String("attribute_exists(id) AND songbook_id = :sb AND #status = :pending"),
					ExpressionAttributeNames: map[string]string{"#status": "status"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":canceled": avString(string(models.InvitationStatusCanceled)),
						":pending":  avString(string(models.InvitationStatusPending)),
						":sb":       avString(songbookID),
						":updated":  avString(timestamp(now)),
					},
				},
			},
			{
				Delete: &types.Delete{
					TableName:           aws.String(s.tables.songbookInvitations),
					Key:                 stringKey("id", pendingInvitationSlotID(songbookID, inv.RecipientEmail)),
					ConditionExpression: aws.String("attribute_not_exists(id) OR active_invitation_id = :inv_id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":inv_id": avString(invitationID),
					},
				},
			},
		},
	})
	if err != nil {
		if isConditional(err) {
			return store.ErrInvitationInvalid
		}
		return fmt.Errorf("cancel songbook invitation: %w", err)
	}
	return nil
}

// AcceptSongbookInvitation atomically verifies invitation state, expiry, recipient email,
// verified host account state, and non-deleting songbook state while consuming the
// single-use invitation and creating/updating the collaborator grant in one transaction.
func (s *Store) AcceptSongbookInvitation(ctx context.Context, tokenHash, userID, email string, now time.Time) (*models.SongbookGrant, *models.SongbookInvitation, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	normEmail := normalizedEmail(email)
	inv, err := s.GetSongbookInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, nil, err
	}
	if inv.Status != models.InvitationStatusPending {
		return nil, nil, store.ErrInvitationInvalid
	}
	if !inv.ExpiresAt.After(now) {
		return nil, nil, store.ErrInvitationExpired
	}
	if normalizedEmail(inv.RecipientEmail) != normEmail {
		return nil, nil, store.ErrInvitationEmailMismatch
	}

	grant := &models.SongbookGrant{
		SongbookID:     inv.SongbookID,
		UserID:         userID,
		CanEditCatalog: inv.CanEditCatalog,
		CanHostShows:   inv.CanHostShows,
		GrantedBy:      inv.OwnerID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	grantPayload, err := marshal(songbookGrantItem{
		SongbookID:     grant.SongbookID,
		UserID:         grant.UserID,
		CanEditCatalog: grant.CanEditCatalog,
		CanHostShows:   grant.CanHostShows,
		GrantedBy:      grant.GrantedBy,
		CreatedAt:      timestamp(now),
		UpdatedAt:      timestamp(now),
	})
	if err != nil {
		return nil, nil, err
	}

	nowStr := timestamp(now)
	nowUnix := now.UTC().UnixNano()
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:                aws.String(s.tables.songbookInvitations),
					Key:                      stringKey("id", inv.ID),
					UpdateExpression:         aws.String("SET #status = :accepted, accepted_by_user_id = :uid, accepted_at = :now, updated_at = :now REMOVE token_hash"),
					ConditionExpression:      aws.String("attribute_exists(id) AND #status = :pending AND token_hash = :th AND recipient_email = :email AND expires_at_unix > :now_unix"),
					ExpressionAttributeNames: map[string]string{"#status": "status"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":accepted": avString(string(models.InvitationStatusAccepted)),
						":pending":  avString(string(models.InvitationStatusPending)),
						":uid":      avString(userID),
						":now":      avString(nowStr),
						":now_unix": avNumber(nowUnix),
						":th":       avString(tokenHash),
						":email":    avString(normEmail),
					},
				},
			},
			{
				Put: &types.Put{
					TableName:           aws.String(s.tables.songbookGrants),
					Item:                grantPayload,
					ConditionExpression: aws.String("attribute_not_exists(songbook_id) AND attribute_not_exists(user_id)"),
				},
			},
			{
				Delete: &types.Delete{
					TableName:           aws.String(s.tables.songbookInvitations),
					Key:                 stringKey("id", pendingInvitationSlotID(inv.SongbookID, inv.RecipientEmail)),
					ConditionExpression: aws.String("attribute_not_exists(id) OR active_invitation_id = :inv_id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":inv_id": avString(inv.ID),
					},
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.songbooks),
					Key:                 stringKey("id", inv.SongbookID),
					ConditionExpression: aws.String("attribute_exists(id) AND owner_id = :owner AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":owner":        avString(inv.OwnerID),
						":not_deleting": avBool(false),
					},
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.identities),
					Key:                 stringKey("email", normEmail),
					ConditionExpression: aws.String("attribute_exists(email) AND user_id = :uid"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":uid": avString(userID),
					},
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.users),
					Key:                 stringKey("id", userID),
					ConditionExpression: aws.String("attribute_exists(id) AND email_verified = :true AND is_host = :true AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":true":         avBool(true),
						":not_deleting": avBool(false),
					},
				},
			},
		},
	})
	if err != nil {
		if isConditional(err) {
			if sb, sbErr := s.getSongbookItem(ctx, inv.SongbookID); errors.Is(sbErr, os.ErrNotExist) || (sbErr == nil && sb.Deleting) {
				return nil, nil, store.ErrSongbookDeleting
			}
			if u, uErr := s.GetUser(ctx, userID); uErr == nil {
				if u.Deleting {
					return nil, nil, store.ErrAccountDeleting
				}
				if normalizedEmail(u.Email) != normEmail {
					return nil, nil, store.ErrInvitationEmailMismatch
				}
				if !u.EmailVerified || !u.IsHost {
					return nil, nil, store.ErrPermissionDenied
				}
			}
			if _, gErr := s.GetSongbookGrant(ctx, inv.SongbookID, userID); gErr == nil {
				return nil, nil, store.ErrAlreadyCollaborator
			}
			if fresh, getErr := s.GetSongbookInvitation(ctx, inv.ID); getErr == nil {
				if fresh.Status != models.InvitationStatusPending || fresh.TokenHash != tokenHash {
					return nil, nil, store.ErrInvitationInvalid
				}
				if !fresh.ExpiresAt.After(now) {
					return nil, nil, store.ErrInvitationExpired
				}
			}
			return nil, nil, store.ErrInvitationInvalid
		}
		return nil, nil, fmt.Errorf("accept songbook invitation: %w", err)
	}

	inv.Status = models.InvitationStatusAccepted
	inv.TokenHash = ""
	inv.AcceptedByUserID = userID
	inv.AcceptedAt = &now
	inv.UpdatedAt = now
	return grant, inv, nil
}
