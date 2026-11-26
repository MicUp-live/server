// Package dynamo persists immutable catalog version metadata.
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

// DefaultBuildLeaseDuration is the default lease granted to a building catalog version.
const DefaultBuildLeaseDuration = 10 * time.Minute

// CreateCatalogVersion records a building catalog version. Version IDs are
// content hashes, so a conditional collision means an identical build exists.
func (s *Store) CreateCatalogVersion(ctx context.Context, version *models.CatalogVersion) error {
	if strings.TrimSpace(version.ID) == "" {
		return fmt.Errorf("create catalog version: ID is required")
	}
	if version.SchemaVersion <= 0 {
		return fmt.Errorf("create catalog version: schema version is required")
	}
	if version.Status == "" {
		version.Status = models.CatalogVersionBuilding
	}
	if version.Status != models.CatalogVersionBuilding {
		return fmt.Errorf("create catalog version: status must be building")
	}
	created, _ := nowForCreate(version.CreatedAt)
	version.CreatedAt = created
	if version.LeaseExpiresAt.IsZero() {
		version.LeaseExpiresAt = created.Add(DefaultBuildLeaseDuration)
	}
	if version.LeaseToken == "" {
		version.LeaseToken = uuid.NewString()
	}
	item, err := marshal(catalogVersionItem{
		ID: version.ID, SchemaVersion: version.SchemaVersion, Status: string(version.Status),
		SongCount: version.SongCount, AssetPrefix: version.AssetPrefix, CreatedAt: timestamp(created),
		UpdatedAt: timestamp(created), LeaseToken: version.LeaseToken, LeaseExpiresAt: timestamp(version.LeaseExpiresAt),
	})
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.tables.catalogs), Item: item,
		ConditionExpression: aws.String("attribute_not_exists(id)")})
	if isConditional(err) {
		return os.ErrExist
	}
	if err != nil {
		return fmt.Errorf("create catalog version: %w", err)
	}
	return nil
}

// GetCatalogVersion loads one catalog version by its content hash.
func (s *Store) GetCatalogVersion(ctx context.Context, id string) (*models.CatalogVersion, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.catalogs), Key: stringKey("id", id), ConsistentRead: boolValue(true)})
	if err != nil {
		return nil, fmt.Errorf("get catalog version: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item catalogVersionItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelCatalogVersion(item)
}

// ListCatalogVersions returns all catalog version metadata for maintenance jobs.
func (s *Store) ListCatalogVersions(ctx context.Context) ([]*models.CatalogVersion, error) {
	var versions []*models.CatalogVersion
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tables.catalogs),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, fmt.Errorf("list catalog versions: %w", err)
		}
		for _, raw := range result.Items {
			var item catalogVersionItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			version, err := modelCatalogVersion(item)
			if err != nil {
				return nil, err
			}
			versions = append(versions, version)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return versions, nil
}

// DeleteCatalogVersion removes metadata after its immutable assets are removed.
func (s *Store) DeleteCatalogVersion(ctx context.Context, id string, leaseToken ...string) error {
	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tables.catalogs),
		Key:       stringKey("id", id),
	}
	if len(leaseToken) > 0 && strings.TrimSpace(leaseToken[0]) != "" {
		input.ConditionExpression = aws.String("#status = :deleting AND (lease_token = :token OR attribute_not_exists(lease_token))")
		input.ExpressionAttributeNames = map[string]string{
			"#status": "status",
		}
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":deleting": avString(string(models.CatalogVersionDeleting)),
			":token":    avString(leaseToken[0]),
		}
	}
	_, err := s.db.DeleteItem(ctx, input)
	if isConditional(err) {
		return store.ErrCatalogDeletionConflict
	}
	return err
}

// MarkCatalogVersionReady makes a fully uploaded catalog eligible for publishing.
func (s *Store) MarkCatalogVersionReady(ctx context.Context, id, assetPrefix string, songCount int, leaseToken ...string) (*models.CatalogVersion, error) {
	if strings.TrimSpace(assetPrefix) == "" || songCount < 0 {
		return nil, fmt.Errorf("mark catalog version ready: asset prefix and non-negative song count are required")
	}
	now := time.Now().UTC()
	cond := "#status = :building"
	exprValues := map[string]types.AttributeValue{
		":ready":    avString(string(models.CatalogVersionReady)),
		":prefix":   avString(assetPrefix),
		":count":    avNumber(int64(songCount)),
		":ready_at": avString(timestamp(now)),
		":now":      avString(timestamp(now)),
		":building": avString(string(models.CatalogVersionBuilding)),
	}
	if len(leaseToken) > 0 && strings.TrimSpace(leaseToken[0]) != "" {
		cond = "#status = :building AND (lease_token = :token OR attribute_not_exists(lease_token))"
		exprValues[":token"] = avString(leaseToken[0])
	}
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :ready, asset_prefix = :prefix, song_count = :count, ready_at = :ready_at, updated_at = :now REMOVE failure_reason, lease_expires_at, lease_token"),
		ConditionExpression: aws.String(cond),
		ReturnValues:        types.ReturnValueAllNew,
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: exprValues,
	})
	if isConditional(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("mark catalog version ready: %w", err)
	}
	var item catalogVersionItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelCatalogVersion(item)
}

// FailCatalogVersion records a terminal build failure without publishing it.
func (s *Store) FailCatalogVersion(ctx context.Context, id, reason string, leaseToken ...string) (*models.CatalogVersion, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("fail catalog version: reason is required")
	}
	now := time.Now().UTC()
	cond := "#status = :building"
	exprValues := map[string]types.AttributeValue{
		":failed":   avString(string(models.CatalogVersionFailed)),
		":reason":   avString(reason),
		":building": avString(string(models.CatalogVersionBuilding)),
		":now":      avString(timestamp(now)),
	}
	if len(leaseToken) > 0 && strings.TrimSpace(leaseToken[0]) != "" {
		cond = "#status = :building AND (lease_token = :token OR attribute_not_exists(lease_token))"
		exprValues[":token"] = avString(leaseToken[0])
	}
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :failed, failure_reason = :reason, updated_at = :now REMOVE lease_expires_at, lease_token"),
		ConditionExpression: aws.String(cond),
		ReturnValues:        types.ReturnValueAllNew,
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: exprValues,
	})
	if isConditional(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("fail catalog version: %w", err)
	}
	var item catalogVersionItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelCatalogVersion(item)
}

// ReclaimCatalogVersion atomically resets a failed or abandoned catalog version to building status
// with a new lease token and expiration.
func (s *Store) ReclaimCatalogVersion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("reclaim catalog version: ID is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = DefaultBuildLeaseDuration
	}
	now := time.Now().UTC()
	leaseExpires := now.Add(leaseDuration)
	token := uuid.NewString()

	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :building, lease_token = :token, lease_expires_at = :lease_expires, updated_at = :now REMOVE failure_reason, asset_prefix, song_count, ready_at"),
		ConditionExpression: aws.String("#status = :failed OR (#status = :building AND ((attribute_exists(lease_expires_at) AND lease_expires_at <= :now) OR (attribute_not_exists(lease_expires_at) AND created_at <= :cutoff))) OR (#status = :deleting AND ((attribute_exists(lease_expires_at) AND lease_expires_at <= :now) OR (attribute_not_exists(lease_expires_at) AND updated_at <= :cutoff)))"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":building":      avString(string(models.CatalogVersionBuilding)),
			":failed":        avString(string(models.CatalogVersionFailed)),
			":deleting":      avString(string(models.CatalogVersionDeleting)),
			":token":         avString(token),
			":lease_expires": avString(timestamp(leaseExpires)),
			":now":           avString(timestamp(now)),
			":cutoff":        avString(timestamp(now.Add(-leaseDuration))),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		return nil, store.ErrReclaimConflict
	}
	if err != nil {
		return nil, fmt.Errorf("reclaim catalog version: %w", err)
	}
	var item catalogVersionItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelCatalogVersion(item)
}

// PublishCatalogVersion atomically points a songbook at an already-ready version.
// When opts includes JobID, the import job is atomically transitioned to completed
// in the same transaction with exact generation and active-state fencing.
func (s *Store) PublishCatalogVersion(ctx context.Context, songbookID, catalogVersionID string, opts ...store.PublicationOptions) error {
	var jobID string
	var jobGen int64
	var processedSongs int
	if len(opts) > 0 {
		jobID = opts[0].JobID
		jobGen = opts[0].JobGeneration
		processedSongs = opts[0].ProcessedSongs
	}

	// Check and validate the per-job receipt before catalog lookup.
	// Completed replay must succeed even when the original catalog no longer exists.
	// Receipt-read failures must be propagated.
	if jobID != "" {
		storedJob, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
			return fmt.Errorf("read import job receipt %s: %w", jobID, getErr)
		}
		if storedJob != nil {
			if storedJob.Status == models.ImportJobCompleted {
				return store.ErrImportJobAlreadyCompleted
			}
			if storedJob.Status == models.ImportJobFailed {
				return store.ErrImportJobTerminal
			}
		}
	}

	version, err := s.GetCatalogVersion(ctx, catalogVersionID)
	if err != nil {
		return err
	}
	if version.Status != models.CatalogVersionReady {
		return fmt.Errorf("publish catalog version: catalog %s status is %s: %w", catalogVersionID, version.Status, os.ErrNotExist)
	}

	songbookCond := "attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"
	updateExpr := "SET catalog_version_id = :catalog, song_count = :count, updated_at = :updated"
	now := time.Now().UTC()
	exprValues := map[string]types.AttributeValue{
		":catalog":      avString(catalogVersionID),
		":count":        avNumber(int64(version.SongCount)),
		":updated":      avString(timestamp(now)),
		":not_deleting": avBool(false),
	}
	exprNames := map[string]string{
		"#status": "status",
	}

	if len(opts) > 0 {
		opt := opts[0]
		if opt.ExpectedBaseVersion != nil {
			if *opt.ExpectedBaseVersion != "" {
				songbookCond += " AND catalog_version_id = :expected_base"
				exprValues[":expected_base"] = avString(*opt.ExpectedBaseVersion)
			} else {
				songbookCond += " AND (attribute_not_exists(catalog_version_id) OR catalog_version_id = :empty_base)"
				exprValues[":empty_base"] = avString("")
			}
		}
		if opt.JobGeneration > 0 {
			songbookCond += " AND (attribute_not_exists(catalog_generation) OR catalog_generation <= :job_gen)"
			updateExpr += ", catalog_generation = :job_gen"
			exprValues[":job_gen"] = avNumber(opt.JobGeneration)
		}
	}

	transactItems := []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{
			TableName:                 aws.String(s.tables.catalogs),
			Key:                       stringKey("id", catalogVersionID),
			ConditionExpression:       aws.String("#status = :ready"),
			ExpressionAttributeNames:  exprNames,
			ExpressionAttributeValues: map[string]types.AttributeValue{":ready": avString(string(models.CatalogVersionReady))},
		}},
		{Update: &types.Update{
			TableName:                 aws.String(s.tables.songbooks),
			Key:                       stringKey("id", songbookID),
			UpdateExpression:          aws.String(updateExpr),
			ConditionExpression:       aws.String(songbookCond),
			ExpressionAttributeValues: exprValues,
		}},
	}

	if jobID != "" {
		jobExprNames := map[string]string{
			"#status": "status",
			"#err":    "error",
		}
		jobExprValues := map[string]types.AttributeValue{
			":completed":    avString(string(models.ImportJobCompleted)),
			":completed_at": avString(timestamp(now)),
			":catalog":      avString(catalogVersionID),
			":processed":    avNumber(int64(processedSongs)),
			":sb_id":        avString(songbookID),
			":job_gen":      avNumber(jobGen),
			":processing":   avString(string(models.ImportJobProcessing)),
			":queued":       avString(string(models.ImportJobQueued)),
		}
		jobCond := "attribute_exists(id) AND songbook_id = :sb_id AND generation = :job_gen AND (#status = :processing OR #status = :queued)"

		transactItems = append(transactItems, types.TransactWriteItem{
			Update: &types.Update{
				TableName:                 aws.String(s.tables.importJobs),
				Key:                       stringKey("id", jobID),
				UpdateExpression:          aws.String("SET #status = :completed, completed_at = :completed_at, catalog_version_id = :catalog, processed_songs = :processed, updated_at = :completed_at REMOVE #err, dispatch_claim_token, dispatch_claim_expires_at"),
				ConditionExpression:       aws.String(jobCond),
				ExpressionAttributeNames:  jobExprNames,
				ExpressionAttributeValues: jobExprValues,
			},
		})
	}

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transactItems})
	if isConditional(err) {
		// Reload the durable job receipt before deciding between completed replay, supersession, and conflict.
		if jobID != "" {
			storedJob, getErr := s.GetImportJob(ctx, jobID)
			if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
				return fmt.Errorf("reload import job receipt %s: %w", jobID, getErr)
			}
			if storedJob != nil {
				if storedJob.Status == models.ImportJobCompleted {
					return store.ErrImportJobAlreadyCompleted
				}
				if storedJob.Status == models.ImportJobFailed {
					return store.ErrImportJobTerminal
				}
			}
		}

		classifyError := func(sb *models.Songbook) error {
			if sb.Deleting {
				return store.ErrSongbookDeleting
			}
			if len(opts) > 0 {
				opt := opts[0]
				if opt.JobGeneration > 0 && sb.CatalogGeneration > opt.JobGeneration {
					return fmt.Errorf("%w (songbook generation %d > job generation %d): %w",
						store.ErrPublicationSuperseded, sb.CatalogGeneration, opt.JobGeneration, store.ErrStalePublication)
				}
				if opt.ExpectedBaseVersion != nil {
					base := *opt.ExpectedBaseVersion
					if base != sb.CatalogVersionID {
						return fmt.Errorf("%w (songbook catalog %s != expected %s): %w",
							store.ErrPublicationBaseConflict, sb.CatalogVersionID, base, store.ErrStalePublication)
					}
				}
			}
			return store.ErrStalePublication
		}

		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) && len(canceled.CancellationReasons) > 0 {
			if aws.ToString(canceled.CancellationReasons[0].Code) == "ConditionalCheckFailed" {
				return os.ErrNotExist
			}
			if len(canceled.CancellationReasons) > 1 && aws.ToString(canceled.CancellationReasons[1].Code) == "ConditionalCheckFailed" {
				if sb, getErr := s.GetSongbook(ctx, songbookID); getErr == nil && sb != nil {
					return classifyError(sb)
				}
				return os.ErrNotExist
			}
			if len(canceled.CancellationReasons) > 2 && aws.ToString(canceled.CancellationReasons[2].Code) == "ConditionalCheckFailed" {
				if jobID != "" {
					storedJob, getErr := s.GetImportJob(ctx, jobID)
					if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
						return fmt.Errorf("reload import job receipt %s: %w", jobID, getErr)
					}
					if storedJob != nil {
						if storedJob.Status == models.ImportJobCompleted {
							return store.ErrImportJobAlreadyCompleted
						}
						if storedJob.Status == models.ImportJobFailed {
							return store.ErrImportJobTerminal
						}
					}
				}
			}
		}
		if sb, getErr := s.GetSongbook(ctx, songbookID); getErr == nil && sb != nil {
			return classifyError(sb)
		}
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("publish catalog version: %w", err)
	}
	return nil
}

// ReserveCatalogVersionForDeletion atomically marks a ready, failed, or expired deleting/building
// catalog version as deleting, allocating an exclusive time-bounded lease and unique token.
func (s *Store) ReserveCatalogVersionForDeletion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("reserve catalog version for deletion: ID is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = DefaultBuildLeaseDuration
	}
	now := time.Now().UTC()
	leaseExpires := now.Add(leaseDuration)
	token := uuid.NewString()

	cond := "#status = :ready OR #status = :failed OR " +
		"(#status = :deleting AND ((attribute_exists(lease_expires_at) AND lease_expires_at <= :now) OR (attribute_not_exists(lease_expires_at) AND updated_at <= :cutoff))) OR " +
		"(#status = :building AND ((attribute_exists(lease_expires_at) AND lease_expires_at <= :now) OR (attribute_not_exists(lease_expires_at) AND created_at <= :cutoff)))"

	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :deleting, lease_token = :token, lease_expires_at = :lease_expires, updated_at = :now"),
		ConditionExpression: aws.String(cond),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ready":         avString(string(models.CatalogVersionReady)),
			":failed":        avString(string(models.CatalogVersionFailed)),
			":deleting":      avString(string(models.CatalogVersionDeleting)),
			":building":      avString(string(models.CatalogVersionBuilding)),
			":token":         avString(token),
			":lease_expires": avString(timestamp(leaseExpires)),
			":now":           avString(timestamp(now)),
			":cutoff":        avString(timestamp(now.Add(-leaseDuration))),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		if _, getErr := s.GetCatalogVersion(ctx, id); errors.Is(getErr, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, store.ErrCatalogDeletionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("reserve catalog version for deletion: %w", err)
	}
	var item catalogVersionItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelCatalogVersion(item)
}

// ReleaseCatalogVersionReservation atomically restores a deleting catalog version back to ready,
// clearing the deletion lease.
func (s *Store) ReleaseCatalogVersionReservation(ctx context.Context, id, leaseToken string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("release catalog version reservation: ID is required")
	}
	now := time.Now().UTC()
	cond := "#status = :deleting"
	exprValues := map[string]types.AttributeValue{
		":deleting": avString(string(models.CatalogVersionDeleting)),
		":ready":    avString(string(models.CatalogVersionReady)),
		":now":      avString(timestamp(now)),
	}
	if strings.TrimSpace(leaseToken) != "" {
		cond = "#status = :deleting AND (lease_token = :token OR attribute_not_exists(lease_token))"
		exprValues[":token"] = avString(leaseToken)
	}

	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :ready, updated_at = :now REMOVE lease_token, lease_expires_at"),
		ConditionExpression: aws.String(cond),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: exprValues,
	})
	if isConditional(err) {
		return store.ErrCatalogDeletionConflict
	}
	if err != nil {
		return fmt.Errorf("release catalog version reservation: %w", err)
	}
	return nil
}

// IsCatalogVersionReferenced performs a strongly consistent scan on the songbooks table
// to check if any active songbook (excluding specified songbook IDs) points to the catalog version.
func (s *Store) IsCatalogVersionReferenced(ctx context.Context, id string, excludeSongbookIDs ...string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("check catalog version references: ID is required")
	}
	excluded := make(map[string]bool, len(excludeSongbookIDs))
	for _, sbID := range excludeSongbookIDs {
		excluded[sbID] = true
	}

	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tables.songbooks),
			ConsistentRead:    boolValue(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return false, fmt.Errorf("scan songbooks for catalog reference: %w", err)
		}
		for _, raw := range result.Items {
			var item songbookItem
			if err := unmarshal(raw, &item); err != nil {
				return false, err
			}
			if item.CatalogVersionID == id && !excluded[item.ID] {
				return true, nil
			}
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return false, nil
}

// PurgeCatalogVersion coordinates the shared deletion protocol:
// 1. Reserves the catalog version in deleting status (blocking concurrent publication).
// 2. Checks strongly consistent songbook references (excluding any specified songbooks).
// 3. If referenced, safely releases the reservation back to ready and returns ErrCatalogVersionInUse.
// 4. If unreferenced, invokes asset purging, then removes the catalog version metadata.
// In the event of partial asset purging failure, the version remains in deleting status
// with its lease, preventing broken songbook references and enabling idempotent recovery.
func (s *Store) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	opt := store.PurgeCatalogOptions{
		LeaseDuration: DefaultBuildLeaseDuration,
	}
	if len(opts) > 0 {
		if opts[0].LeaseDuration > 0 {
			opt.LeaseDuration = opts[0].LeaseDuration
		}
		opt.ExcludeSongbookIDs = opts[0].ExcludeSongbookIDs
		opt.ReleaseLeaseOnError = opts[0].ReleaseLeaseOnError
	}

	version, err := s.ReserveCatalogVersionForDeletion(ctx, id, opt.LeaseDuration)
	if err != nil {
		return err
	}

	referenced, err := s.IsCatalogVersionReferenced(ctx, id, opt.ExcludeSongbookIDs...)
	if err != nil {
		_ = s.ReleaseCatalogVersionReservation(ctx, id, version.LeaseToken)
		return fmt.Errorf("verify catalog version references: %w", err)
	}
	if referenced {
		if relErr := s.ReleaseCatalogVersionReservation(ctx, id, version.LeaseToken); relErr != nil {
			return fmt.Errorf("release catalog version reservation: %w", relErr)
		}
		return store.ErrCatalogVersionInUse
	}

	if purgeAssets != nil && version.AssetPrefix != "" {
		if err := purgeAssets(version.AssetPrefix); err != nil {
			if opt.ReleaseLeaseOnError {
				_ = s.expireCatalogVersionLease(ctx, id, version.LeaseToken)
			}
			return fmt.Errorf("purge catalog assets: %w", err)
		}
	}

	if err := s.DeleteCatalogVersion(ctx, id, version.LeaseToken); err != nil {
		if opt.ReleaseLeaseOnError {
			_ = s.expireCatalogVersionLease(ctx, id, version.LeaseToken)
		}
		return fmt.Errorf("delete catalog version metadata: %w", err)
	}
	return nil
}

func (s *Store) expireCatalogVersionLease(ctx context.Context, id, leaseToken string) error {
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.catalogs),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET lease_expires_at = :now, updated_at = :now"),
		ConditionExpression: aws.String("#status = :deleting AND (lease_token = :token OR attribute_not_exists(lease_token))"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":deleting": avString(string(models.CatalogVersionDeleting)),
			":token":    avString(leaseToken),
			":now":      avString(timestamp(now)),
		},
	})
	return err
}

func extractCatalogIDFromPrefix(prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	trimmed = strings.TrimPrefix(trimmed, "catalogs/")
	parts := strings.Split(trimmed, "/")
	if len(parts) > 1 && parts[0] == "epochs" {
		return parts[1]
	}
	return parts[0]
}

// PurgeOrphanCatalogPrefix coordinates orphan S3 prefix cleanup with the publication exclusion protocol.
func (s *Store) PurgeOrphanCatalogPrefix(ctx context.Context, prefix string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return fmt.Errorf("purge orphan catalog prefix: prefix is required")
	}
	catalogID := extractCatalogIDFromPrefix(prefix)
	opt := store.PurgeCatalogOptions{LeaseDuration: DefaultBuildLeaseDuration}
	if len(opts) > 0 && opts[0].LeaseDuration > 0 {
		opt.LeaseDuration = opts[0].LeaseDuration
	}

	existing, err := s.GetCatalogVersion(ctx, catalogID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check catalog version for orphan prefix: %w", err)
	}

	if err == nil {
		now := time.Now().UTC()
		// If an active build is in progress, do not touch its assets
		if existing.Status == models.CatalogVersionBuilding && existing.LeaseExpiresAt.After(now) {
			return store.ErrCatalogDeletionConflict
		}
		existingPrefix := strings.Trim(existing.AssetPrefix, "/")
		targetPrefix := strings.Trim(prefix, "/")

		// Check relationship between targetPrefix and existingPrefix
		isExact := existingPrefix != "" && existingPrefix == targetPrefix
		isDescendant := existingPrefix != "" && strings.HasPrefix(targetPrefix, existingPrefix+"/")
		isAncestor := targetPrefix != "" && strings.HasPrefix(existingPrefix, targetPrefix+"/")

		// If existing version is actively pointing to this prefix (exact match), coordinate via PurgeCatalogVersion
		if isExact {
			return s.PurgeCatalogVersion(ctx, catalogID, purgeAssets, opt)
		}

		// Verify references
		referenced, refErr := s.IsCatalogVersionReferenced(ctx, catalogID)
		if refErr != nil {
			return fmt.Errorf("verify references for orphan prefix: %w", refErr)
		}

		// Protect active assets and their descendants (e.g. letters/) or ancestors against deletion if referenced
		if referenced && (isExact || isDescendant || isAncestor) {
			return store.ErrCatalogVersionInUse
		}

		// If existing version is ready and targetPrefix is an active descendant or ancestor:
		if existing.Status == models.CatalogVersionReady && (isDescendant || isAncestor) {
			if referenced {
				return store.ErrCatalogVersionInUse
			}
			return s.PurgeCatalogVersion(ctx, catalogID, purgeAssets, opt)
		}

		if purgeAssets != nil {
			if err := purgeAssets(prefix); err != nil {
				return fmt.Errorf("purge orphan assets: %w", err)
			}
		}
		return nil
	}

	// No record exists in DynamoDB. Atomically claim reservation placeholder to fence concurrent builders.
	token := uuid.NewString()
	now := time.Now().UTC()
	leaseExpires := now.Add(opt.LeaseDuration)

	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tables.catalogs),
		Item: map[string]types.AttributeValue{
			"id":               avString(catalogID),
			"status":           avString(string(models.CatalogVersionDeleting)),
			"asset_prefix":     avString(prefix),
			"lease_token":      avString(token),
			"lease_expires_at": avString(timestamp(leaseExpires)),
			"created_at":       avString(timestamp(now)),
			"updated_at":       avString(timestamp(now)),
		},
		ConditionExpression: aws.String("attribute_not_exists(id)"),
	})
	if isConditional(err) {
		return store.ErrCatalogDeletionConflict
	}
	if err != nil {
		return fmt.Errorf("reserve orphan prefix placeholder: %w", err)
	}

	// Check if any songbook references this catalog ID
	referenced, refErr := s.IsCatalogVersionReferenced(ctx, catalogID)
	if refErr != nil || referenced {
		_ = s.DeleteCatalogVersion(ctx, catalogID, token)
		if referenced {
			return store.ErrCatalogVersionInUse
		}
		return fmt.Errorf("verify references for orphan prefix: %w", refErr)
	}

	if purgeAssets != nil {
		if err := purgeAssets(prefix); err != nil {
			return fmt.Errorf("purge orphan assets: %w", err)
		}
	}

	if err := s.DeleteCatalogVersion(ctx, catalogID, token); err != nil {
		return fmt.Errorf("delete orphan reservation: %w", err)
	}
	return nil
}

func modelCatalogVersion(item catalogVersionItem) (*models.CatalogVersion, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	ready, err := parseTime(item.ReadyAt)
	if err != nil {
		return nil, err
	}
	leaseExpires, err := parseTime(item.LeaseExpiresAt)
	if err != nil {
		return nil, err
	}
	return &models.CatalogVersion{
		ID:             item.ID,
		SchemaVersion:  item.SchemaVersion,
		Status:         models.CatalogVersionStatus(item.Status),
		SongCount:      item.SongCount,
		AssetPrefix:    item.AssetPrefix,
		FailureReason:  item.FailureReason,
		CreatedAt:      created,
		ReadyAt:        ready,
		LeaseToken:     item.LeaseToken,
		LeaseExpiresAt: leaseExpires,
	}, nil
}
