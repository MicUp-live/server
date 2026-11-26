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

// CreateUserWithPasskey atomically creates a user, their WebAuthn credential, and an identity claim.
func (s *Store) CreateUserWithPasskey(ctx context.Context, user *models.User, cred *models.PasskeyCredential) error {
	normEmail := normalizedEmail(user.Email)
	created, updated := nowForCreate(user.CreatedAt)
	user.CreatedAt = created
	user.UpdatedAt = updated
	u := userItem{
		ID:            user.ID,
		Email:         normEmail,
		DisplayName:   user.DisplayName,
		AvatarURL:     user.AvatarURL,
		PasswordHash:  user.PasswordHash,
		AuthEpoch:     user.AuthEpoch,
		IsHost:        user.IsHost,
		EmailVerified: user.EmailVerified,
		CreatedAt:     timestamp(created),
		UpdatedAt:     timestamp(updated),
	}
	userAV, err := marshal(u)
	if err != nil {
		return err
	}

	credCreated := cred.CreatedAt
	if credCreated.IsZero() {
		credCreated = time.Now().UTC()
		cred.CreatedAt = credCreated
	}
	item := passkeyItem{
		ID:              cred.ID,
		UserID:          cred.UserID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		AAGUID:          cred.AAGUID,
		SignCount:       cred.SignCount,
		Transports:      cred.Transports,
		BackupEligible:  cred.BackupEligible,
		BackupState:     cred.BackupState,
		UserPresent:     cred.UserPresent,
		UserVerified:    cred.UserVerified,
		FriendlyName:    cred.FriendlyName,
		CreatedAt:       timestamp(credCreated),
	}
	if !cred.LastUsedAt.IsZero() {
		item.LastUsedAt = timestamp(cred.LastUsedAt)
	}
	credAV, err := marshal(item)
	if err != nil {
		return err
	}

	items := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.users),
				Item:                userAV,
				ConditionExpression: aws.String("attribute_not_exists(id)"),
			},
		},
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.passkeys),
				Item:                credAV,
				ConditionExpression: aws.String("attribute_not_exists(id)"),
			},
		},
	}

	if normEmail != "" {
		claim := identityItem{Email: normEmail, UserID: user.ID}
		claimAV, err := marshal(claim)
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{
			Put: &types.Put{
				TableName:           aws.String(s.tables.identities),
				Item:                claimAV,
				ConditionExpression: aws.String("attribute_not_exists(email)"),
			},
		})
	}

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	if err != nil {
		return fmt.Errorf("create user with passkey: %w", err)
	}
	return nil
}

// SavePasskeyCredential writes a new WebAuthn credential record.
func (s *Store) SavePasskeyCredential(ctx context.Context, cred *models.PasskeyCredential) error {
	created := cred.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
		cred.CreatedAt = created
	}
	item := passkeyItem{
		ID:              cred.ID,
		UserID:          cred.UserID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		AAGUID:          cred.AAGUID,
		SignCount:       cred.SignCount,
		Transports:      cred.Transports,
		BackupEligible:  cred.BackupEligible,
		BackupState:     cred.BackupState,
		UserPresent:     cred.UserPresent,
		UserVerified:    cred.UserVerified,
		FriendlyName:    cred.FriendlyName,
		CreatedAt:       timestamp(created),
	}
	if !cred.LastUsedAt.IsZero() {
		item.LastUsedAt = timestamp(cred.LastUsedAt)
	}
	av, err := marshal(item)
	if err != nil {
		return err
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName: aws.String(s.tables.passkeys),
					Item:      av,
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.users),
					Key:                 stringKey("id", cred.UserID),
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
			if u, getErr := s.GetUser(ctx, cred.UserID); getErr == nil && u.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("save passkey credential: %w", err)
	}
	return nil
}

// GetPasskeyCredential loads a passkey credential by its credential ID.
func (s *Store) GetPasskeyCredential(ctx context.Context, id string) (*models.PasskeyCredential, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.passkeys),
		Key:            stringKey("id", id),
		ConsistentRead: boolValue(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get passkey credential: %w", err)
	}
	if len(result.Item) == 0 {
		// Try alternative base64 padding variants
		altID := ""
		if strings.HasSuffix(id, "=") {
			altID = strings.TrimRight(id, "=")
		} else {
			switch len(id) % 4 {
			case 2:
				altID = id + "=="
			case 3:
				altID = id + "="
			}
		}
		if altID != "" {
			altResult, altErr := s.db.GetItem(ctx, &dynamodb.GetItemInput{
				TableName:      aws.String(s.tables.passkeys),
				Key:            stringKey("id", altID),
				ConsistentRead: boolValue(true),
			})
			if altErr == nil && len(altResult.Item) > 0 {
				result = altResult
			}
		}
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item passkeyItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelPasskey(item)
}

// ListPasskeyCredentialsForUser returns all passkeys registered to a user using an authoritative
// strongly consistent scan on the base table.
func (s *Store) ListPasskeyCredentialsForUser(ctx context.Context, userID string) ([]*models.PasskeyCredential, error) {
	var creds []*models.PasskeyCredential
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.passkeys),
			FilterExpression:          aws.String("user_id = :uid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":uid": avString(userID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list passkeys for user: %w", err)
		}
		for _, raw := range result.Items {
			var item passkeyItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			model, err := modelPasskey(item)
			if err != nil {
				return nil, err
			}
			creds = append(creds, model)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return creds, nil
}

// RecordPasskeyUse commits assertion metadata only while the verified credential and
// its owner are still active. The stored ID retains legacy base64 padding.
func (s *Store) RecordPasskeyUse(ctx context.Context, observed *models.PasskeyCredential, usage store.PasskeyUsage) error {
	counterCondition := "sign_count = :observed"
	if observed.SignCount == 0 {
		counterCondition = "(attribute_not_exists(sign_count) OR sign_count = :observed)"
	}
	eligibilityCondition := "backup_eligible = :was_eligible"
	if !observed.BackupEligible {
		eligibilityCondition = "(attribute_not_exists(backup_eligible) OR backup_eligible = :was_eligible)"
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Update: &types.Update{
				TableName:           aws.String(s.tables.passkeys),
				Key:                 stringKey("id", observed.ID),
				ConditionExpression: aws.String("attribute_exists(id) AND user_id = :uid AND public_key = :pk AND " + counterCondition + " AND " + eligibilityCondition),
				UpdateExpression:    aws.String("SET sign_count = :sc, last_used_at = :used, backup_eligible = :be, backup_state = :bs"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":uid": avString(observed.UserID), ":pk": &types.AttributeValueMemberB{Value: observed.PublicKey},
					":observed": avNumber(int64(observed.SignCount)), ":was_eligible": avBool(observed.BackupEligible),
					":sc": avNumber(int64(usage.SignCount)), ":used": avString(timestamp(time.Now().UTC())),
					":be": avBool(usage.BackupEligible), ":bs": avBool(usage.BackupState),
				},
			}},
			{ConditionCheck: &types.ConditionCheck{
				TableName:                 aws.String(s.tables.users),
				Key:                       stringKey("id", observed.UserID),
				ConditionExpression:       aws.String("attribute_exists(id) AND " + notDeletingCondition),
				ExpressionAttributeValues: map[string]types.AttributeValue{":not_deleting": avBool(false)},
			}},
		},
	})
	var canceled *types.TransactionCanceledException
	if errors.As(err, &canceled) && len(canceled.CancellationReasons) == 2 {
		if aws.ToString(canceled.CancellationReasons[0].Code) == "ConditionalCheckFailed" {
			return store.ErrPasskeyChanged
		}
		if aws.ToString(canceled.CancellationReasons[1].Code) == "ConditionalCheckFailed" {
			return store.ErrAccountDeleting
		}
	}
	if err != nil {
		return fmt.Errorf("record passkey use: %w", err)
	}
	return nil
}

// DeletePasskeyCredential deletes a passkey ensuring it belongs to the given user.
func (s *Store) DeletePasskeyCredential(ctx context.Context, id string, userID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(s.tables.passkeys),
		Key:                 stringKey("id", id),
		ConditionExpression: aws.String("user_id = :uid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":uid": &types.AttributeValueMemberS{Value: userID},
		},
	})
	if err != nil {
		if isConditional(err) {
			res, getErr := s.db.GetItem(ctx, &dynamodb.GetItemInput{
				TableName:      aws.String(s.tables.passkeys),
				Key:            stringKey("id", id),
				ConsistentRead: boolValue(true),
			})
			if getErr == nil && len(res.Item) == 0 {
				return os.ErrNotExist
			}
		}
		return fmt.Errorf("delete passkey credential: %w", err)
	}
	return nil
}

// SaveWebAuthnSession writes a temporary ceremony challenge session with TTL.
func (s *Store) SaveWebAuthnSession(ctx context.Context, session *models.WebAuthnSession) error {
	item := webauthnSessionItem{
		SessionID:   session.SessionID,
		UserID:      session.UserID,
		Challenge:   session.Challenge,
		SessionData: session.SessionData,
		ExpiresAt:   timestamp(session.ExpiresAt),
		TTL:         session.ExpiresAt.Unix(),
	}
	av, err := marshal(item)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tables.webauthn),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("save webauthn session: %w", err)
	}
	return nil
}

// ConsumeWebAuthnSession atomically reads and deletes a ceremony challenge session.
func (s *Store) ConsumeWebAuthnSession(ctx context.Context, sessionID string) (*models.WebAuthnSession, error) {
	result, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:    aws.String(s.tables.webauthn),
		Key:          stringKey("session_id", sessionID),
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return nil, fmt.Errorf("consume webauthn session: %w", err)
	}
	if err := missing(result.Attributes); err != nil {
		return nil, err
	}
	var item webauthnSessionItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	exp, err := parseTime(item.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if time.Now().UTC().After(exp) {
		return nil, fmt.Errorf("webauthn session expired")
	}
	return &models.WebAuthnSession{
		SessionID:   item.SessionID,
		UserID:      item.UserID,
		Challenge:   item.Challenge,
		SessionData: item.SessionData,
		ExpiresAt:   exp,
	}, nil
}

func modelPasskey(item passkeyItem) (*models.PasskeyCredential, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	var lastUsed time.Time
	if item.LastUsedAt != "" {
		lastUsed, _ = parseTime(item.LastUsedAt)
	}
	return &models.PasskeyCredential{
		ID:              item.ID,
		UserID:          item.UserID,
		PublicKey:       item.PublicKey,
		AttestationType: item.AttestationType,
		AAGUID:          item.AAGUID,
		SignCount:       item.SignCount,
		Transports:      item.Transports,
		BackupEligible:  item.BackupEligible,
		BackupState:     item.BackupState,
		UserPresent:     item.UserPresent,
		UserVerified:    item.UserVerified,
		FriendlyName:    item.FriendlyName,
		CreatedAt:       created,
		LastUsedAt:      lastUsed,
	}, nil
}
