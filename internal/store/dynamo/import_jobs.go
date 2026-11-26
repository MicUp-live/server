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

type importJobItem struct {
	ID                     string `dynamodbav:"id"`
	SongbookID             string `dynamodbav:"songbook_id"`
	ManifestKey            string `dynamodbav:"manifest_key"`
	Status                 string `dynamodbav:"status"`
	TotalSongs             int    `dynamodbav:"total_songs"`
	ProcessedSongs         int    `dynamodbav:"processed_songs"`
	PruneMissing           bool   `dynamodbav:"prune_missing"`
	Generation             int64  `dynamodbav:"generation,omitempty"`
	CatalogVersionID       string `dynamodbav:"catalog_version_id,omitempty"`
	DispatchReady          bool   `dynamodbav:"dispatch_ready,omitempty"`
	DispatchClaimToken     string `dynamodbav:"dispatch_claim_token,omitempty"`
	DispatchClaimExpiresAt string `dynamodbav:"dispatch_claim_expires_at,omitempty"`
	DispatchAttempts       int    `dynamodbav:"dispatch_attempts,omitempty"`
	DispatchedAt           string `dynamodbav:"dispatched_at,omitempty"`
	Error                  string `dynamodbav:"error,omitempty"`
	CreatedAt              string `dynamodbav:"created_at"`
	UpdatedAt              string `dynamodbav:"updated_at"`
	CompletedAt            string `dynamodbav:"completed_at,omitempty"`
}

func modelImportJob(item importJobItem) *models.ImportJob {
	job := &models.ImportJob{
		ID:                 item.ID,
		SongbookID:         item.SongbookID,
		ManifestKey:        item.ManifestKey,
		Status:             models.ImportJobStatus(item.Status),
		TotalSongs:         item.TotalSongs,
		ProcessedSongs:     item.ProcessedSongs,
		PruneMissing:       item.PruneMissing,
		Generation:         item.Generation,
		CatalogVersionID:   item.CatalogVersionID,
		DispatchReady:      item.DispatchReady,
		DispatchClaimToken: item.DispatchClaimToken,
		DispatchAttempts:   item.DispatchAttempts,
		Error:              item.Error,
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, item.CreatedAt)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, item.UpdatedAt)
	if item.CompletedAt != "" {
		job.CompletedAt, _ = time.Parse(time.RFC3339Nano, item.CompletedAt)
	}
	if item.DispatchedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, item.DispatchedAt)
		if err == nil {
			job.DispatchedAt = &t
		}
	} else if item.Status == string(models.ImportJobProcessing) || item.Status == string(models.ImportJobCompleted) {
		// Legacy record already in processing/completed without dispatched_at
		t := job.CreatedAt
		job.DispatchedAt = &t
	}
	if item.DispatchClaimExpiresAt != "" {
		t, err := time.Parse(time.RFC3339Nano, item.DispatchClaimExpiresAt)
		if err == nil {
			job.DispatchClaimExpiresAt = &t
		}
	}
	return job
}

// LatestImportJob returns the newest job for a songbook. The small pre-launch
// table is scanned until a songbook GSI is warranted by measured usage.
func (s *Store) LatestImportJob(ctx context.Context, songbookID string) (*models.ImportJob, error) {
	var latest *models.ImportJob
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.importJobs),
			FilterExpression:          aws.String("songbook_id = :songbook"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":songbook": avString(songbookID)},
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return nil, fmt.Errorf("latest import job: %w", err)
		}
		for _, raw := range result.Items {
			var item importJobItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			job, err := s.GetImportJob(ctx, item.ID)
			if err != nil {
				continue
			}
			if latest == nil || job.CreatedAt.After(latest.CreatedAt) {
				latest = job
			}
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	if latest == nil {
		return nil, os.ErrNotExist
	}
	return latest, nil
}

// ListImportJobs returns all durable import jobs for garbage-collection audits.
func (s *Store) ListImportJobs(ctx context.Context) ([]*models.ImportJob, error) {
	var jobs []*models.ImportJob
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tables.importJobs),
			ExclusiveStartKey: start,
			ConsistentRead:    boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list import jobs: %w", err)
		}
		for _, raw := range result.Items {
			var item importJobItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			job, err := s.GetImportJob(ctx, item.ID)
			if err != nil {
				return nil, fmt.Errorf("get import job %s: %w", item.ID, err)
			}
			jobs = append(jobs, job)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return jobs, nil
}

// DeleteImportJob removes terminal import metadata after its retention window.
func (s *Store) DeleteImportJob(ctx context.Context, id string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(s.tables.importJobs), Key: stringKey("id", id)})
	return err
}

func (s *Store) CreateImportJob(ctx context.Context, job *models.ImportJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.SongbookID) == "" {
		return fmt.Errorf("create import job: id and songbook are required")
	}
	if job.Status == "" {
		job.Status = models.ImportJobPending
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	rawItem := importJobItem{
		ID:                 job.ID,
		SongbookID:         job.SongbookID,
		ManifestKey:        job.ManifestKey,
		Status:             string(job.Status),
		TotalSongs:         job.TotalSongs,
		ProcessedSongs:     job.ProcessedSongs,
		PruneMissing:       job.PruneMissing,
		Generation:         job.Generation,
		CatalogVersionID:   job.CatalogVersionID,
		DispatchReady:      job.DispatchReady,
		DispatchClaimToken: job.DispatchClaimToken,
		DispatchAttempts:   job.DispatchAttempts,
		Error:              job.Error,
		CreatedAt:          timestamp(job.CreatedAt),
		UpdatedAt:          timestamp(job.UpdatedAt),
	}
	if job.DispatchedAt != nil {
		rawItem.DispatchedAt = timestamp(*job.DispatchedAt)
	}
	if job.DispatchClaimExpiresAt != nil {
		rawItem.DispatchClaimExpiresAt = timestamp(*job.DispatchClaimExpiresAt)
	}
	if !job.CompletedAt.IsZero() {
		rawItem.CompletedAt = timestamp(job.CompletedAt)
	}

	item, err := marshal(rawItem)
	if err != nil {
		return err
	}
	txItems := s.appendEditFence(ctx, job.SongbookID, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.tables.importJobs), Item: item, ConditionExpression: aws.String("attribute_not_exists(id)")}},
		s.songbookActiveConditionCheck(job.SongbookID),
	})
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems})
	if err != nil {
		if isConditional(err) {
			if _, getJobErr := s.GetImportJob(ctx, job.ID); getJobErr == nil {
				return os.ErrExist
			}
			if sb, getSbErr := s.getSongbookItem(ctx, job.SongbookID); errors.Is(getSbErr, os.ErrNotExist) {
				return fmt.Errorf("create import job: %w", os.ErrNotExist)
			} else if getSbErr == nil && sb.Deleting {
				return fmt.Errorf("create import job: %w", store.ErrSongbookDeleting)
			}
			if gErr := s.checkEditGrantError(ctx, job.SongbookID); gErr != nil {
				return fmt.Errorf("create import job: %w", gErr)
			}
		}
		return fmt.Errorf("create import job: %w", err)
	}
	return nil
}

func (s *Store) GetImportJob(ctx context.Context, id string) (*models.ImportJob, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.importJobs), Key: stringKey("id", id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("get import job: %w", err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item importJobItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelImportJob(item), nil
}

func (s *Store) UpdateImportJob(ctx context.Context, job *models.ImportJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("update import job: id is required")
	}
	now := time.Now().UTC()
	job.UpdatedAt = now

	exprNames := make(map[string]string)
	exprVals := map[string]types.AttributeValue{
		":updated": avString(timestamp(job.UpdatedAt)),
	}
	var setClauses []string
	var removeClauses []string

	setClauses = append(setClauses, "updated_at = :updated")

	if job.Status != "" {
		setClauses = append(setClauses, "#status = :status")
		exprNames["#status"] = "status"
		exprVals[":status"] = avString(string(job.Status))
	}
	if job.SongbookID != "" {
		setClauses = append(setClauses, "songbook_id = :sb_id")
		exprVals[":sb_id"] = avString(job.SongbookID)
	}
	if job.ManifestKey != "" {
		setClauses = append(setClauses, "manifest_key = :manifest_key")
		exprVals[":manifest_key"] = avString(job.ManifestKey)
	}
	if job.TotalSongs > 0 {
		setClauses = append(setClauses, "total_songs = :total_songs")
		exprVals[":total_songs"] = avNumber(int64(job.TotalSongs))
	}
	if job.ProcessedSongs >= 0 {
		setClauses = append(setClauses, "processed_songs = :processed_songs")
		exprVals[":processed_songs"] = avNumber(int64(job.ProcessedSongs))
	}
	if job.PruneMissing {
		setClauses = append(setClauses, "prune_missing = :prune_missing")
		exprVals[":prune_missing"] = avBool(true)
	}
	if job.Error != "" {
		setClauses = append(setClauses, "#err = :err")
		exprNames["#err"] = "error"
		exprVals[":err"] = avString(job.Error)
	} else {
		removeClauses = append(removeClauses, "#err")
		exprNames["#err"] = "error"
	}
	if !job.CompletedAt.IsZero() {
		setClauses = append(setClauses, "completed_at = :completed_at")
		exprVals[":completed_at"] = avString(timestamp(job.CompletedAt))
	} else {
		removeClauses = append(removeClauses, "completed_at")
	}

	if job.Status == models.ImportJobCompleted {
		return fmt.Errorf("update import job %s: completion requires atomic publication transaction: %w", job.ID, store.ErrImportJobNotReady)
	}

	condExpr := "attribute_exists(id) AND #status <> :failed_status"
	exprNames["#status"] = "status"
	exprVals[":failed_status"] = avString(string(models.ImportJobFailed))
	if job.Status != "" {
		exprVals[":completed_status"] = avString(string(models.ImportJobCompleted))
		condExpr += " AND #status <> :completed_status"
	}
	if job.Generation > 0 {
		setClauses = append(setClauses, "generation = :gen")
		exprVals[":gen"] = avNumber(job.Generation)
		exprVals[":zero"] = avNumber(0)
		condExpr += " AND (attribute_not_exists(generation) OR generation = :zero OR generation = :gen)"
	}

	updateExpr := "SET " + strings.Join(setClauses, ", ")
	if len(removeClauses) > 0 {
		updateExpr += " REMOVE " + strings.Join(removeClauses, ", ")
	}

	var names map[string]string
	if len(exprNames) > 0 {
		names = exprNames
	}

	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tables.importJobs),
		Key:                       stringKey("id", job.ID),
		UpdateExpression:          aws.String(updateExpr),
		ConditionExpression:       aws.String(condExpr),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: exprVals,
		ReturnValues:              types.ReturnValueAllNew,
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, job.ID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return fmt.Errorf("update import job %s: %w", job.ID, store.ErrImportJobAlreadyCompleted)
		}
		if existing.Status == models.ImportJobFailed {
			return fmt.Errorf("update import job %s: %w", job.ID, store.ErrImportJobTerminal)
		}
		if job.Generation > 0 && existing.Generation > 0 && existing.Generation != job.Generation {
			return fmt.Errorf("update import job %s: %w", job.ID, store.ErrJobGenerationConflict)
		}
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("update import job: %w", err)
	}
	if result != nil && len(result.Attributes) > 0 {
		var updatedItem importJobItem
		if err := unmarshal(result.Attributes, &updatedItem); err == nil && updatedItem.Generation > 0 {
			job.Generation = updatedItem.Generation
		}
	}
	return nil
}

// MarkImportJobDispatchReady marks an import job as ready to be dispatched (e.g. after upload completion).
// Permitted source states: pending or queued. Processing, completed, or failed jobs are rejected.
func (s *Store) MarkImportJobDispatchReady(ctx context.Context, jobID string) error {
	if actorID := store.SongbookEditActorFromContext(ctx); actorID != "" {
		return s.markImportJobDispatchReadyFenced(ctx, jobID, actorID)
	}
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET dispatch_ready = :ready, #status = :queued, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ready":   avBool(true),
			":pending": avString(string(models.ImportJobPending)),
			":queued":  avString(string(models.ImportJobQueued)),
			":now":     avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		if existing.Status == models.ImportJobProcessing || existing.DispatchedAt != nil {
			return store.ErrImportJobAlreadyDispatched
		}
		return os.ErrNotExist
	}
	return err
}

// ClaimImportJobDispatch acquires a time-bounded lease to dispatch an import job to SQS.
// If the job is not dispatch-ready, has already been confirmed as dispatched, or is held
// by another active claim, it returns a descriptive error.
func (s *Store) ClaimImportJobDispatch(ctx context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}
	now := time.Now().UTC()
	expires := now.Add(leaseDuration)
	token := uuid.NewString()

	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET dispatch_claim_token = :token, dispatch_claim_expires_at = :expires, dispatch_attempts = if_not_exists(dispatch_attempts, :zero) + :one, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(id) AND dispatch_ready = :ready AND (#status = :pending OR #status = :queued) AND attribute_not_exists(dispatched_at) AND (attribute_not_exists(dispatch_claim_expires_at) OR dispatch_claim_expires_at <= :now OR dispatch_claim_token = :empty)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ready":   avBool(true),
			":pending": avString(string(models.ImportJobPending)),
			":queued":  avString(string(models.ImportJobQueued)),
			":token":   avString(token),
			":expires": avString(timestamp(expires)),
			":zero":    avNumber(0),
			":one":     avNumber(1),
			":now":     avString(timestamp(now)),
			":empty":   avString(""),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return nil, "", os.ErrNotExist
		}
		if !existing.DispatchReady {
			return nil, "", store.ErrImportJobNotReady
		}
		if existing.DispatchedAt != nil {
			return nil, "", store.ErrImportJobAlreadyDispatched
		}
		if existing.Status == models.ImportJobCompleted {
			return nil, "", store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return nil, "", store.ErrImportJobTerminal
		}
		if existing.Status == models.ImportJobProcessing {
			return nil, "", store.ErrImportJobAlreadyDispatched
		}
		if existing.DispatchClaimExpiresAt != nil && existing.DispatchClaimExpiresAt.After(now) {
			return nil, "", store.ErrImportJobClaimConflict
		}
		return nil, "", store.ErrImportJobClaimConflict
	}
	if err != nil {
		return nil, "", fmt.Errorf("claim import job dispatch: %w", err)
	}
	var item importJobItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, "", err
	}
	return modelImportJob(item), token, nil
}

// ConfirmImportJobDispatch records confirmed SQS dispatch using the caller's matching claim token.
// Stale dispatchers cannot overwrite newer claims or regress processing/completed status.
func (s *Store) ConfirmImportJobDispatch(ctx context.Context, jobID string, claimToken string) error {
	if strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("confirm import job dispatch: claim token is required")
	}
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET #status = :queued, dispatched_at = :now, updated_at = :now REMOVE dispatch_claim_token, dispatch_claim_expires_at, #err"),
		ConditionExpression: aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued) AND dispatch_claim_token = :token"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
			"#err":    "error",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": avString(string(models.ImportJobPending)),
			":queued":  avString(string(models.ImportJobQueued)),
			":now":     avString(timestamp(now)),
			":token":   avString(claimToken),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		if existing.Status == models.ImportJobProcessing {
			// Worker already claimed processing from SQS; not an error
			return nil
		}
		return store.ErrImportJobStaleClaim
	}
	if err != nil {
		return fmt.Errorf("confirm import job dispatch: %w", err)
	}
	return nil
}

// RecordDispatchError releases the active claim and logs the dispatch error, keeping the job
// in pending/queued status so it remains recoverable by subsequent attempts or background reconciliation.
func (s *Store) RecordDispatchError(ctx context.Context, jobID string, claimToken string, errMsg string) error {
	if strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("record dispatch error: claim token is required")
	}
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET #err = :err, updated_at = :now REMOVE dispatch_claim_token, dispatch_claim_expires_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued) AND dispatch_claim_token = :token"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
			"#err":    "error",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": avString(string(models.ImportJobPending)),
			":queued":  avString(string(models.ImportJobQueued)),
			":err":     avString(errMsg),
			":now":     avString(timestamp(now)),
			":token":   avString(claimToken),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		return store.ErrImportJobStaleClaim
	}
	if err != nil {
		return fmt.Errorf("record dispatch error: %w", err)
	}
	return nil
}

// ListDispatchableImportJobs returns all unconfirmed import jobs that are ready for dispatch
// and have either no active claim or an expired claim, bounded by maxAttempts.
func (s *Store) ListDispatchableImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	now := time.Now().UTC()
	var candidates []*models.ImportJob
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tables.importJobs),
			FilterExpression:  aws.String("dispatch_ready = :ready AND attribute_not_exists(dispatched_at) AND (#status = :pending OR #status = :queued)"),
			ExclusiveStartKey: start,
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":ready":   avBool(true),
				":pending": avString(string(models.ImportJobPending)),
				":queued":  avString(string(models.ImportJobQueued)),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list dispatchable import jobs: %w", err)
		}
		for _, raw := range result.Items {
			var item importJobItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("unmarshal import job: %w", err)
			}
			job := modelImportJob(item)
			if job.DispatchAttempts >= maxAttempts {
				continue
			}
			if job.DispatchClaimExpiresAt != nil && job.DispatchClaimExpiresAt.After(now) {
				continue
			}
			candidates = append(candidates, job)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return candidates, nil
}

// ListExhaustedImportJobs returns all unconfirmed import jobs that are ready for dispatch
// but have reached or exceeded maxAttempts without confirmed delivery.
func (s *Store) ListExhaustedImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var exhausted []*models.ImportJob
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tables.importJobs),
			FilterExpression:  aws.String("dispatch_ready = :ready AND attribute_not_exists(dispatched_at) AND (#status = :pending OR #status = :queued)"),
			ExclusiveStartKey: start,
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":ready":   avBool(true),
				":pending": avString(string(models.ImportJobPending)),
				":queued":  avString(string(models.ImportJobQueued)),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list exhausted import jobs: %w", err)
		}
		for _, raw := range result.Items {
			var item importJobItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("unmarshal import job: %w", err)
			}
			job := modelImportJob(item)
			if job.DispatchAttempts >= maxAttempts {
				exhausted = append(exhausted, job)
			}
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	return exhausted, nil
}

// UpdateImportJobProgress updates the processed song count on an active import job.
// This operation is conditional on status=processing and cannot mutate identity,
// generation, or lifecycle status.
func (s *Store) UpdateImportJobProgress(ctx context.Context, jobID string, processedSongs int) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("update import job progress: id is required")
	}
	if processedSongs < 0 {
		processedSongs = 0
	}
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET processed_songs = :processed, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(id) AND #status = :processing"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":processed":  avNumber(int64(processedSongs)),
			":processing": avString(string(models.ImportJobProcessing)),
			":now":        avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("update import job progress: %w", err)
	}
	return nil
}

// ResetImportJobDispatch clears the dispatch claim, error, and resets dispatch attempts
// for an unconfirmed, dispatch-ready import job so reconciliation or manual retry can re-attempt dispatch.
// Unfinished uploads, confirmed jobs, terminal jobs, and jobs with active unexpired claims are rejected.
func (s *Store) ResetImportJobDispatch(ctx context.Context, jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("reset import job dispatch: id is required")
	}
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET dispatch_attempts = :zero, updated_at = :now REMOVE #err, dispatch_claim_token, dispatch_claim_expires_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND dispatch_ready = :ready AND attribute_not_exists(dispatched_at) AND (#status = :pending OR #status = :queued) AND (attribute_not_exists(dispatch_claim_expires_at) OR dispatch_claim_expires_at <= :now OR dispatch_claim_token = :empty)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
			"#err":    "error",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ready":   avBool(true),
			":pending": avString(string(models.ImportJobPending)),
			":queued":  avString(string(models.ImportJobQueued)),
			":zero":    avNumber(0),
			":now":     avString(timestamp(now)),
			":empty":   avString(""),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if !existing.DispatchReady {
			return store.ErrImportJobNotReady
		}
		if existing.DispatchedAt != nil || existing.Status == models.ImportJobProcessing {
			return store.ErrImportJobAlreadyDispatched
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		if existing.DispatchClaimExpiresAt != nil && existing.DispatchClaimExpiresAt.After(now) {
			return store.ErrImportJobClaimConflict
		}
		return store.ErrImportJobClaimConflict
	}
	if err != nil {
		return fmt.Errorf("reset import job dispatch: %w", err)
	}
	return nil
}

// ClaimImportJobProcessing transitions a job to processing status upon worker delivery.
// Permitted source states: pending, queued, processing. Completed or failed jobs are rejected.
func (s *Store) ClaimImportJobProcessing(ctx context.Context, jobID string) (*models.ImportJob, error) {
	now := time.Now().UTC()
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET #status = :processing, dispatched_at = if_not_exists(dispatched_at, :now), updated_at = :now REMOVE #err, dispatch_claim_token, dispatch_claim_expires_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued OR #status = :processing)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
			"#err":    "error",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending":    avString(string(models.ImportJobPending)),
			":queued":     avString(string(models.ImportJobQueued)),
			":processing": avString(string(models.ImportJobProcessing)),
			":now":        avString(timestamp(now)),
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return nil, os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return nil, store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return nil, store.ErrImportJobTerminal
		}
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("claim import job processing: %w", err)
	}
	var item importJobItem
	if err := unmarshal(result.Attributes, &item); err != nil {
		return nil, err
	}
	return modelImportJob(item), nil
}

// FailImportJob transitions an active import job to terminal failed status.
// Permitted source states: pending, queued, processing. Completed jobs cannot be marked failed.
func (s *Store) FailImportJob(ctx context.Context, jobID string, reason string) error {
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.importJobs),
		Key:                 stringKey("id", jobID),
		UpdateExpression:    aws.String("SET #status = :failed, #err = :err, updated_at = :now REMOVE dispatch_claim_token, dispatch_claim_expires_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued OR #status = :processing)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
			"#err":    "error",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending":    avString(string(models.ImportJobPending)),
			":queued":     avString(string(models.ImportJobQueued)),
			":processing": avString(string(models.ImportJobProcessing)),
			":failed":     avString(string(models.ImportJobFailed)),
			":err":        avString(reason),
			":now":        avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if existing.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if existing.Status == models.ImportJobFailed {
			return nil // already failed
		}
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("fail import job: %w", err)
	}
	return nil
}

// AssignImportJobGeneration assigns a positive catalog generation to an import job once.
// Concurrent callers converge on whichever generation was durably committed first.
func (s *Store) AssignImportJobGeneration(ctx context.Context, jobID string, generation int64) (int64, error) {
	if strings.TrimSpace(jobID) == "" {
		return 0, fmt.Errorf("assign import job generation: id is required")
	}
	if generation <= 0 {
		return 0, fmt.Errorf("assign import job generation: generation must be positive")
	}
	now := time.Now().UTC()
	updateExpr := "SET generation = :gen, updated_at = :updated"
	condExpr := "attribute_exists(id) AND (attribute_not_exists(generation) OR generation = :zero OR generation = :gen)"
	exprVals := map[string]types.AttributeValue{
		":gen":     avNumber(generation),
		":zero":    avNumber(0),
		":updated": avString(timestamp(now)),
	}

	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tables.importJobs),
		Key:                       stringKey("id", jobID),
		UpdateExpression:          aws.String(updateExpr),
		ConditionExpression:       aws.String(condExpr),
		ExpressionAttributeValues: exprVals,
	})
	if err == nil {
		return generation, nil
	}
	if isConditional(err) {
		existing, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return 0, fmt.Errorf("assign import job generation %s: %w", jobID, getErr)
		}
		if existing.Generation > 0 {
			return existing.Generation, nil
		}
		return 0, fmt.Errorf("assign import job generation %s: conditional check failed", jobID)
	}
	return 0, fmt.Errorf("assign import job generation %s: %w", jobID, err)
}
