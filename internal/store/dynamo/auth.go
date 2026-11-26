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
	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// authEpochCondition also supports records written before epochs were introduced.
func authEpochCondition(epoch string) string {
	if epoch == "" {
		return "(attribute_not_exists(auth_epoch) OR auth_epoch = :epoch)"
	}
	return "auth_epoch = :epoch"
}

const notDeletingCondition = "(attribute_not_exists(deleting) OR deleting = :not_deleting)"

// ResetPassword consumes reset authority, changes the password, and invalidates
// all previous bearer sessions in one conditional update of the user record.
func (s *Store) ResetPassword(ctx context.Context, reset store.PasswordResetCommit) error {
	if reset.AuthEpoch == "" || reset.AuthEpoch == reset.ExpectedAuthEpoch ||
		reset.PasswordHash == "" || !reset.ExpiresAt.After(time.Now()) {
		return store.ErrInvalidPasswordReset
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tables.users), Key: stringKey("id", reset.UserID),
		ConditionExpression: aws.String("attribute_exists(id) AND email = :email AND " + notDeletingCondition + " AND " + authEpochCondition(reset.ExpectedAuthEpoch)),
		UpdateExpression:    aws.String("SET password_hash = :password, auth_epoch = :next, updated_at = :updated"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":email": avString(reset.Email), ":epoch": avString(reset.ExpectedAuthEpoch),
			":not_deleting": avBool(false),
			":password":     avString(reset.PasswordHash), ":next": avString(reset.AuthEpoch),
			":updated": avString(timestamp(time.Now().UTC())),
		},
	})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return store.ErrInvalidPasswordReset
	}
	if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	return nil
}

// CreateUser atomically reserves the normalized email and creates its user.
func (s *Store) CreateUser(ctx context.Context, user *models.User) error {
	created, updated := nowForCreate(user.CreatedAt)
	if !user.UpdatedAt.IsZero() {
		updated = user.UpdatedAt
	}
	user.CreatedAt, user.UpdatedAt = created, updated
	u := userItem{
		ID:            user.ID,
		Email:         user.Email,
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

	normEmail := normalizedEmail(user.Email)
	if normEmail != "" {
		claim := identityItem{Email: normEmail, UserID: user.ID}
		claimAV, err := marshal(claim)
		if err != nil {
			return err
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(s.tables.identities), Item: claimAV, ConditionExpression: aws.String("attribute_not_exists(email)")}},
			{Put: &types.Put{TableName: aws.String(s.tables.users), Item: userAV, ConditionExpression: aws.String("attribute_not_exists(id)")}},
		}})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
	} else {
		_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String(s.tables.users),
			Item:                userAV,
			ConditionExpression: aws.String("attribute_not_exists(id)"),
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
	}
	return nil
}

// UpdateUser persists changes to an existing user profile (display name, is_host, avatar, etc.).
func (s *Store) UpdateUser(ctx context.Context, user *models.User) error {
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	user.UpdatedAt = time.Now().UTC()
	u := userItem{
		ID:            user.ID,
		Email:         user.Email,
		DisplayName:   user.DisplayName,
		AvatarURL:     user.AvatarURL,
		PasswordHash:  user.PasswordHash,
		AuthEpoch:     user.AuthEpoch,
		IsHost:        user.IsHost,
		EmailVerified: user.EmailVerified,
		CreatedAt:     timestamp(user.CreatedAt),
		UpdatedAt:     timestamp(user.UpdatedAt),
	}
	userAV, err := marshal(u)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tables.users),
		Item:                userAV,
		ConditionExpression: aws.String("attribute_exists(id) AND " + notDeletingCondition + " AND " + authEpochCondition(user.AuthEpoch)),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":epoch":        avString(user.AuthEpoch),
			":not_deleting": avBool(false),
		},
	})
	if err != nil {
		if isConditional(err) {
			if current, getErr := s.GetUser(ctx, user.ID); getErr == nil && current.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// ChangeUserEmail transactionally updates the user's email, moves the identity index claim,
// and ensures no other account has claimed the new email.
func (s *Store) ChangeUserEmail(ctx context.Context, userID, oldEmail, newEmail string, user *models.User) error {
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	normOld := normalizedEmail(oldEmail)
	normNew := normalizedEmail(newEmail)

	user.UpdatedAt = time.Now().UTC()
	user.Email = normNew
	u := userItem{
		ID:            user.ID,
		Email:         normNew,
		DisplayName:   user.DisplayName,
		AvatarURL:     user.AvatarURL,
		PasswordHash:  user.PasswordHash,
		AuthEpoch:     user.AuthEpoch,
		IsHost:        user.IsHost,
		EmailVerified: user.EmailVerified,
		CreatedAt:     timestamp(user.CreatedAt),
		UpdatedAt:     timestamp(user.UpdatedAt),
	}
	userAV, err := marshal(u)
	if err != nil {
		return err
	}

	if normOld == normNew {
		return s.UpdateUser(ctx, user)
	}

	items := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.users),
				Item:                userAV,
				ConditionExpression: aws.String("attribute_exists(id) AND " + notDeletingCondition + " AND " + authEpochCondition(user.AuthEpoch)),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":epoch":        avString(user.AuthEpoch),
					":not_deleting": avBool(false),
				},
			},
		},
	}

	if normOld != "" {
		items = append(items, types.TransactWriteItem{
			Delete: &types.Delete{
				TableName:           aws.String(s.tables.identities),
				Key:                 stringKey("email", normOld),
				ConditionExpression: aws.String("attribute_exists(email) AND user_id = :uid"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":uid": avString(userID),
				},
			},
		})
	}

	if normNew != "" {
		claim := identityItem{Email: normNew, UserID: userID}
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
		if isConditional(err) {
			if current, getErr := s.GetUser(ctx, userID); getErr == nil && current.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("change user email: %w", err)
	}
	return nil
}

func mergePendingCatalogVersions(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	var merged []string
	for _, list := range [][]string{existing, incoming} {
		for _, raw := range list {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, id)
		}
	}
	return merged
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func subtractPendingCatalogVersions(current, resolved []string) []string {
	resolvedSet := make(map[string]struct{}, len(resolved))
	for _, id := range resolved {
		clean := strings.TrimSpace(id)
		if clean != "" {
			resolvedSet[clean] = struct{}{}
		}
	}
	var remaining []string
	for _, id := range current {
		clean := strings.TrimSpace(id)
		if clean == "" {
			continue
		}
		if _, ok := resolvedSet[clean]; !ok {
			remaining = append(remaining, clean)
		}
	}
	return remaining
}

func marshalPendingList(items []string) (types.AttributeValue, error) {
	out, err := marshal(struct {
		Pending []string `dynamodbav:"pending"`
	}{Pending: items})
	if err != nil {
		return nil, err
	}
	return out["pending"], nil
}

func pendingCatalogVersionsCondition(current []string, exprValues map[string]types.AttributeValue) (string, error) {
	if len(current) == 0 {
		exprValues[":zero"] = &types.AttributeValueMemberN{Value: "0"}
		return "(attribute_not_exists(pending_catalog_versions) OR size(pending_catalog_versions) = :zero)", nil
	}
	av, err := marshalPendingList(current)
	if err != nil {
		return "", err
	}
	exprValues[":expected_pending"] = av
	return "pending_catalog_versions = :expected_pending", nil
}

// BeginAccountDeletion atomically transitions a user record into irreversible deletion state,
// captures initiating authority in deletion_auth_epoch, rotates auth_epoch to revoke all existing
// sessions and access tokens, and persists any catalog versions that must be purged after songbook removal.
// Concurrent calls converge on one deletion epoch assign-once and merge pending catalog versions safely.
func (s *Store) BeginAccountDeletion(ctx context.Context, userID string, pendingCatalogVersions []string) (*models.User, error) {
	const maxAttempts = 16
	for attempt := 0; attempt < maxAttempts; attempt++ {
		current, err := s.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}

		now := time.Now().UTC()
		if !current.Deleting {
			deletionEpoch := uuid.NewString()
			nextAuthEpoch := "deleting:" + deletionEpoch
			mergedPending := mergePendingCatalogVersions(current.PendingCatalogVersions, pendingCatalogVersions)

			updateExpr := "SET deleting = :deleting, deletion_epoch = :del_epoch, deletion_auth_epoch = :del_auth_epoch, deletion_started_at = :started_at, auth_epoch = :auth_epoch, updated_at = :updated"
			exprValues := map[string]types.AttributeValue{
				":not_deleting":   avBool(false),
				":empty_epoch":    avString(""),
				":epoch":          avString(current.AuthEpoch),
				":deleting":       avBool(true),
				":del_epoch":      avString(deletionEpoch),
				":del_auth_epoch": avString(current.AuthEpoch),
				":started_at":     avString(timestamp(now)),
				":auth_epoch":     avString(nextAuthEpoch),
				":updated":        avString(timestamp(now)),
			}
			if len(mergedPending) > 0 {
				pendingAV, err := marshalPendingList(mergedPending)
				if err != nil {
					return nil, err
				}
				updateExpr += ", pending_catalog_versions = :pending"
				exprValues[":pending"] = pendingAV
			}
			updateExpr += " REMOVE password_hash"

			cond := "attribute_exists(id) AND " + notDeletingCondition +
				" AND (attribute_not_exists(deletion_epoch) OR deletion_epoch = :empty_epoch) AND " +
				authEpochCondition(current.AuthEpoch)

			result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName:                 aws.String(s.tables.users),
				Key:                       stringKey("id", userID),
				ConditionExpression:       aws.String(cond),
				UpdateExpression:          aws.String(updateExpr),
				ExpressionAttributeValues: exprValues,
				ReturnValues:              types.ReturnValueAllNew,
			})
			if err != nil {
				if isConditional(err) {
					continue
				}
				return nil, fmt.Errorf("begin account deletion: %w", err)
			}
			var item userItem
			if err := unmarshal(result.Attributes, &item); err != nil {
				return nil, err
			}
			return modelUser(item)
		}

		// Already deleting: preserve deletion_epoch, deletion_auth_epoch, deletion_started_at, and auth_epoch.
		mergedPending := mergePendingCatalogVersions(current.PendingCatalogVersions, pendingCatalogVersions)
		if stringSlicesEqual(current.PendingCatalogVersions, mergedPending) {
			return current, nil
		}

		pendingAV, err := marshalPendingList(mergedPending)
		if err != nil {
			return nil, err
		}
		exprValues := map[string]types.AttributeValue{
			":deleting":  avBool(true),
			":del_epoch": avString(current.DeletionEpoch),
			":pending":   pendingAV,
			":updated":   avString(timestamp(now)),
		}
		pendingCond, err := pendingCatalogVersionsCondition(current.PendingCatalogVersions, exprValues)
		if err != nil {
			return nil, err
		}
		cond := "attribute_exists(id) AND deleting = :deleting AND deletion_epoch = :del_epoch AND " + pendingCond

		result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(s.tables.users),
			Key:                       stringKey("id", userID),
			ConditionExpression:       aws.String(cond),
			UpdateExpression:          aws.String("SET pending_catalog_versions = :pending, updated_at = :updated"),
			ExpressionAttributeValues: exprValues,
			ReturnValues:              types.ReturnValueAllNew,
		})
		if err != nil {
			if isConditional(err) {
				continue
			}
			return nil, fmt.Errorf("begin account deletion: %w", err)
		}
		var item userItem
		if err := unmarshal(result.Attributes, &item); err != nil {
			return nil, err
		}
		return modelUser(item)
	}
	return nil, fmt.Errorf("begin account deletion: exceeded max conflict retries")
}

// RecordAccountDeletionProgress updates the durable step/error and removes resolved catalog versions
// for a user undergoing account deletion using compare-and-swap retry to prevent lost updates.
func (s *Store) RecordAccountDeletionProgress(ctx context.Context, userID, step, errMsg string, resolvedCatalogVersions []string) error {
	const maxAttempts = 16
	for attempt := 0; attempt < maxAttempts; attempt++ {
		current, err := s.GetUser(ctx, userID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !current.Deleting || current.DeletionEpoch == "" {
			return errors.New("account is not marked for deletion")
		}

		now := time.Now().UTC()
		setParts := []string{
			"deletion_step = :step",
			"deletion_error = :err",
			"updated_at = :updated",
		}
		var removeParts []string
		exprValues := map[string]types.AttributeValue{
			":deleting":  avBool(true),
			":del_epoch": avString(current.DeletionEpoch),
			":step":      avString(step),
			":err":       avString(errMsg),
			":updated":   avString(timestamp(now)),
		}
		cond := "attribute_exists(id) AND deleting = :deleting AND deletion_epoch = :del_epoch"

		if len(resolvedCatalogVersions) > 0 {
			pendingCond, err := pendingCatalogVersionsCondition(current.PendingCatalogVersions, exprValues)
			if err != nil {
				return err
			}
			cond += " AND " + pendingCond
			remaining := subtractPendingCatalogVersions(current.PendingCatalogVersions, resolvedCatalogVersions)
			if len(remaining) > 0 {
				pendingAV, err := marshalPendingList(remaining)
				if err != nil {
					return err
				}
				setParts = append(setParts, "pending_catalog_versions = :pending")
				exprValues[":pending"] = pendingAV
			} else {
				removeParts = append(removeParts, "pending_catalog_versions")
			}
		}

		updateExpr := "SET " + strings.Join(setParts, ", ")
		if len(removeParts) > 0 {
			updateExpr += " REMOVE " + strings.Join(removeParts, ", ")
		}

		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(s.tables.users),
			Key:                       stringKey("id", userID),
			ConditionExpression:       aws.String(cond),
			UpdateExpression:          aws.String(updateExpr),
			ExpressionAttributeValues: exprValues,
		})
		if err != nil {
			if isConditional(err) {
				continue
			}
			return fmt.Errorf("record account deletion progress: %w", err)
		}
		return nil
	}
	return fmt.Errorf("record account deletion progress: exceeded max conflict retries")
}

// ListDeletingUsers returns all user records currently marked as undergoing deletion.
func (s *Store) ListDeletingUsers(ctx context.Context) ([]*models.User, error) {
	var users []*models.User
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.users),
			FilterExpression:          aws.String("deleting = :deleting"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":deleting": avBool(true)},
			ExclusiveStartKey:         startKey,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list deleting users: %w", err)
		}
		for _, raw := range out.Items {
			var item userItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			u, err := modelUser(item)
			if err != nil {
				return nil, err
			}
			users = append(users, u)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return users, nil
}

// DeleteUser atomically removes a user record and releases their normalized email claim.
// Finalization is conditioned on the authoritative deletion workflow state:
// deleting = true, matching deletion_epoch, and all pending catalog versions resolved.
// Deleting an already-absent user is idempotent and returns nil.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !u.Deleting || u.DeletionEpoch == "" {
		return errors.New("delete user: account is not in deleting state")
	}
	if len(u.PendingCatalogVersions) > 0 {
		return fmt.Errorf("delete user: %d pending catalog versions remain unpurged", len(u.PendingCatalogVersions))
	}

	userDeleteCond := "attribute_exists(id) AND deleting = :deleting AND deletion_epoch = :del_epoch AND (attribute_not_exists(pending_catalog_versions) OR size(pending_catalog_versions) = :zero)"
	userDeleteValues := map[string]types.AttributeValue{
		":deleting":  avBool(true),
		":del_epoch": avString(u.DeletionEpoch),
		":zero":      &types.AttributeValueMemberN{Value: "0"},
	}

	normEmail := normalizedEmail(u.Email)
	if normEmail != "" {
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
				{
					Delete: &types.Delete{
						TableName:           aws.String(s.tables.identities),
						Key:                 stringKey("email", normEmail),
						ConditionExpression: aws.String("attribute_not_exists(email) OR user_id = :uid"),
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":uid": avString(userID),
						},
					},
				},
				{
					Delete: &types.Delete{
						TableName:                 aws.String(s.tables.users),
						Key:                       stringKey("id", userID),
						ConditionExpression:       aws.String(userDeleteCond),
						ExpressionAttributeValues: userDeleteValues,
					},
				},
			},
		})
		if err != nil {
			if isConditional(err) {
				if _, getErr := s.GetUser(ctx, userID); errors.Is(getErr, os.ErrNotExist) {
					return nil
				}
			}
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	}

	_, err = s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 aws.String(s.tables.users),
		Key:                       stringKey("id", userID),
		ConditionExpression:       aws.String(userDeleteCond),
		ExpressionAttributeValues: userDeleteValues,
	})
	if err != nil {
		if isConditional(err) {
			if _, getErr := s.GetUser(ctx, userID); errors.Is(getErr, os.ErrNotExist) {
				return nil
			}
		}
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// GetUserByEmail resolves a normalized identity claim before loading the user.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.identities), Key: stringKey("email", normalizedEmail(email)), ConsistentRead: boolValue(true)})
	if err != nil {
		return nil, fmt.Errorf("get identity claim: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var claim identityItem
	if err := unmarshal(result.Item, &claim); err != nil {
		return nil, err
	}
	return s.GetUser(ctx, claim.UserID)
}

// GetUser loads a user by ID.
func (s *Store) GetUser(ctx context.Context, id string) (*models.User, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.users), Key: stringKey("id", id), ConsistentRead: boolValue(true)})
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item userItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelUser(item)
}

// CreateAuthSession stores a single-use refresh session transactionally conditioned
// on the user existing and not undergoing account deletion.
func (s *Store) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	item, err := marshal(sessionItem{TokenHash: tokenKey(session.TokenHash), UserID: session.UserID,
		AuthEpoch: session.AuthEpoch, ExpiresAt: timestamp(session.ExpiresAt), TTL: session.ExpiresAt.Unix()})
	if err != nil {
		return err
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(s.tables.sessions),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(token_hash)"),
				},
			},
			{
				ConditionCheck: &types.ConditionCheck{
					TableName:           aws.String(s.tables.users),
					Key:                 stringKey("id", session.UserID),
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
			if u, getErr := s.GetUser(ctx, session.UserID); getErr == nil && u.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("create auth session: %w", err)
	}
	return nil
}

// ConsumeAuthSession atomically deletes a refresh session before validating it.
func (s *Store) ConsumeAuthSession(ctx context.Context, tokenHash []byte) (*models.User, error) {
	result, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(s.tables.sessions),
		Key: stringKey("token_hash", tokenKey(tokenHash)), ReturnValues: types.ReturnValueAllOld})
	if err != nil {
		return nil, fmt.Errorf("consume auth session: %w", err)
	}
	if err := missing(result.Attributes); err != nil {
		return nil, err
	}
	var session sessionItem
	if err := unmarshal(result.Attributes, &session); err != nil {
		return nil, err
	}
	expiresAt, err := parseTime(session.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if !expiresAt.After(time.Now()) {
		return nil, os.ErrNotExist
	}
	user, err := s.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	if user.Deleting || user.AuthEpoch != session.AuthEpoch {
		return nil, os.ErrNotExist
	}
	return user, nil
}

// DeleteAuthSessionsForUser removes all sessions owned by a user using a strongly
// consistent base-table scan to ensure complete, authoritative enumeration.
func (s *Store) DeleteAuthSessionsForUser(ctx context.Context, userID string) error {
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.sessions),
			FilterExpression:          aws.String("user_id = :user"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":user": avString(userID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
			ProjectionExpression:      aws.String("token_hash"),
		})
		if err != nil {
			return fmt.Errorf("list user auth sessions: %w", err)
		}
		for _, item := range result.Items {
			_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(s.tables.sessions),
				Key:       map[string]types.AttributeValue{"token_hash": item["token_hash"]},
			})
			if err != nil {
				return fmt.Errorf("delete user auth session: %w", err)
			}
		}
		if len(result.LastEvaluatedKey) == 0 {
			return nil
		}
		start = result.LastEvaluatedKey
	}
}
