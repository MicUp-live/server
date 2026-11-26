package dynamo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type testImportQueue struct {
	mu       sync.Mutex
	failNext error
	enqueued []importjob.Job
}

func (q *testImportQueue) Enqueue(_ context.Context, job importjob.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failNext != nil {
		err := q.failNext
		q.failNext = nil
		return err
	}
	q.enqueued = append(q.enqueued, job)
	return nil
}

type testManifestWriter struct {
	mu        sync.Mutex
	stored    map[string][]byte
	presigned map[string]int64
}

func (w *testManifestWriter) Put(_ context.Context, key string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stored[key] = data
	return nil
}

func (w *testManifestWriter) Presign(_ context.Context, key string, size int64) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.presigned[key] = size
	return "https://s3.local/" + key, nil
}

// 1. Publication atomically commits catalog pointer swap on songbooks and completion on importJobs
// with exact generation equality and active-state fencing.
func TestImport_PublicationAtomicallyCommitsJobCompletionAndSongbookPointer(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	jobID := "job-" + uuid.NewString()
	versionID := "sha256:ver-" + uuid.NewString()

	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Test SB"})

	job := &models.ImportJob{
		ID:            jobID,
		SongbookID:    sbID,
		Status:        models.ImportJobProcessing,
		Generation:    1,
		DispatchReady: true,
	}
	if err := s.CreateImportJob(ctx, job); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	helperCreateAndReadyVersion(t, s, versionID, 42)

	opts := store.PublicationOptions{
		JobID:          jobID,
		JobGeneration:  1,
		ProcessedSongs: 42,
	}
	if err := s.PublishCatalogVersion(ctx, sbID, versionID, opts); err != nil {
		t.Fatalf("publish catalog version: %v", err)
	}

	// Verify songbook pointer updated
	sb, err := s.GetSongbook(ctx, sbID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if sb.CatalogVersionID != versionID || sb.CatalogGeneration != 1 || sb.SongCount != 42 {
		t.Fatalf("unexpected songbook state: %+v", sb)
	}

	// Verify import job atomically marked completed
	savedJob, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if savedJob.Status != models.ImportJobCompleted {
		t.Fatalf("expected job status completed, got %s", savedJob.Status)
	}
	if savedJob.CatalogVersionID != versionID || savedJob.ProcessedSongs != 42 || savedJob.CompletedAt.IsZero() {
		t.Fatalf("unexpected saved job state: %+v", savedJob)
	}
}

// 2. Competing publishers: two concurrent workers attempt to publish.
// The first one commits atomically; the second detects completed receipt and gets ErrImportJobAlreadyCompleted.
func TestImport_CompetingPublishers_OnlyOneWinsAndSecondGetsAlreadyCompleted(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	jobID := "job-" + uuid.NewString()
	v1 := "sha256:v1-" + uuid.NewString()
	v2 := "sha256:v2-" + uuid.NewString()

	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Competing SB"})
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    sbID,
		Status:        models.ImportJobProcessing,
		Generation:    1,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	helperCreateAndReadyVersion(t, s, v1, 10)
	helperCreateAndReadyVersion(t, s, v2, 10)

	opts1 := store.PublicationOptions{JobID: jobID, JobGeneration: 1, ProcessedSongs: 10}
	if err := s.PublishCatalogVersion(ctx, sbID, v1, opts1); err != nil {
		t.Fatalf("worker 1 publish failed: %v", err)
	}

	// Worker 2 attempts to publish v2 for the same job
	opts2 := store.PublicationOptions{JobID: jobID, JobGeneration: 1, ProcessedSongs: 10}
	err := s.PublishCatalogVersion(ctx, sbID, v2, opts2)
	if !errors.Is(err, store.ErrImportJobAlreadyCompleted) {
		t.Fatalf("expected ErrImportJobAlreadyCompleted for worker 2, got: %v", err)
	}

	// Verify songbook remains pointing to v1
	sb, err := s.GetSongbook(ctx, sbID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if sb.CatalogVersionID != v1 {
		t.Fatalf("expected songbook to remain at v1, got: %s", sb.CatalogVersionID)
	}
}

// 3. Completed replay succeeds even after catalog assets and record were purged by GC.
// Per-job receipt pre-check validates importJobs first.
func TestImport_CompletedReplay_SurvivesPurgedCatalogVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	jobID := "job-" + uuid.NewString()
	vID := "sha256:purged-" + uuid.NewString()

	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "GC SB"})
	// Job is already marked completed from previous run
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:               jobID,
		SongbookID:       sbID,
		Status:           models.ImportJobCompleted,
		Generation:       1,
		CatalogVersionID: vID,
		CompletedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	// Notice: vID does NOT exist in DynamoDB catalogs table (simulating GC deletion).
	// Attempting to publish must check receipt on importJobs first and return ErrImportJobAlreadyCompleted.
	opts := store.PublicationOptions{JobID: jobID, JobGeneration: 1, ProcessedSongs: 5}
	err := s.PublishCatalogVersion(ctx, sbID, vID, opts)
	if !errors.Is(err, store.ErrImportJobAlreadyCompleted) {
		t.Fatalf("expected ErrImportJobAlreadyCompleted even with missing catalog, got: %v", err)
	}
}

// 4. Terminal failure racing with publication: terminal failed jobs must not resume or publish.
func TestImport_TerminalFailureRacingWithPublication(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	jobID := "job-" + uuid.NewString()
	vID := "sha256:ver-" + uuid.NewString()

	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Terminal SB"})
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    sbID,
		Status:        models.ImportJobProcessing,
		Generation:    1,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	helperCreateAndReadyVersion(t, s, vID, 20)

	// Mark job terminal failed
	if err := s.FailImportJob(ctx, jobID, "fatal parse error"); err != nil {
		t.Fatalf("fail import job: %v", err)
	}

	// Attempting to publish fails with ErrImportJobTerminal
	opts := store.PublicationOptions{JobID: jobID, JobGeneration: 1, ProcessedSongs: 20}
	err := s.PublishCatalogVersion(ctx, sbID, vID, opts)
	if !errors.Is(err, store.ErrImportJobTerminal) {
		t.Fatalf("expected ErrImportJobTerminal, got: %v", err)
	}

	// Songbook must remain empty
	sb, err := s.GetSongbook(ctx, sbID)
	if err != nil {
		t.Fatalf("get songbook: %v", err)
	}
	if sb.CatalogVersionID != "" {
		t.Fatalf("expected empty catalog_version_id, got %s", sb.CatalogVersionID)
	}
}

// 5. Claim dispatch lease, token validation, and delayed stale dispatcher writes.
func TestImport_ClaimDispatch_LeaseTokenValidationAndDelayedStaleDispatcher(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-1", Name: "SB 1"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    "sb-1",
		Status:        models.ImportJobQueued,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	// Dispatcher 1 claims job with 5m lease
	job1, token1, err := s.ClaimImportJobDispatch(ctx, jobID, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch 1: %v", err)
	}
	if token1 == "" || job1.DispatchAttempts != 1 {
		t.Fatalf("unexpected claim 1 state: token=%s attempts=%d", token1, job1.DispatchAttempts)
	}

	// Concurrent dispatcher immediately attempts to claim: gets ErrImportJobClaimConflict
	_, _, err = s.ClaimImportJobDispatch(ctx, jobID, 5*time.Minute)
	if !errors.Is(err, store.ErrImportJobClaimConflict) {
		t.Fatalf("expected ErrImportJobClaimConflict, got: %v", err)
	}

	// Explicitly expire Dispatcher 1's claim lease in the test fixture
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.tables.importJobs),
		Key:              stringKey("id", jobID),
		UpdateExpression: aws.String("SET dispatch_claim_expires_at = :expired"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":expired": avString(past),
		},
	})
	if err != nil {
		t.Fatalf("expire lease in fixture: %v", err)
	}

	// Dispatcher 2 claims expired job
	job2, token2, err := s.ClaimImportJobDispatch(ctx, jobID, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch 2: %v", err)
	}
	if token2 == "" || token2 == token1 || job2.DispatchAttempts != 2 {
		t.Fatalf("unexpected claim 2 state: token=%s attempts=%d", token2, job2.DispatchAttempts)
	}

	// Stale Dispatcher 1 attempts to confirm with token1: rejected with ErrImportJobStaleClaim
	err = s.ConfirmImportJobDispatch(ctx, jobID, token1)
	if !errors.Is(err, store.ErrImportJobStaleClaim) {
		t.Fatalf("expected ErrImportJobStaleClaim for stale token1, got: %v", err)
	}

	// Stale Dispatcher 1 attempts to record error with token1: rejected with ErrImportJobStaleClaim
	err = s.RecordDispatchError(ctx, jobID, token1, "delayed timeout error")
	if !errors.Is(err, store.ErrImportJobStaleClaim) {
		t.Fatalf("expected ErrImportJobStaleClaim for stale token1 error record, got: %v", err)
	}

	// Dispatcher 2 confirms with valid token2: succeeds
	if err := s.ConfirmImportJobDispatch(ctx, jobID, token2); err != nil {
		t.Fatalf("confirm dispatch with token2: %v", err)
	}

	// Verify job confirmed dispatched
	confirmedJob, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if confirmedJob.DispatchedAt == nil || confirmedJob.DispatchClaimToken != "" {
		t.Fatalf("unexpected confirmed job state: %+v", confirmedJob)
	}
}

// 6. Worker starts processing before dispatch confirmation is persisted.
func TestImport_WorkerStartsBeforeDispatchConfirmation(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-1", Name: "SB 1"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    "sb-1",
		Status:        models.ImportJobQueued,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	_, token, err := s.ClaimImportJobDispatch(ctx, jobID, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}

	// Worker receives SQS message and claims processing BEFORE dispatcher confirms
	procJob, err := s.ClaimImportJobProcessing(ctx, jobID)
	if err != nil {
		t.Fatalf("claim processing: %v", err)
	}
	if procJob.Status != models.ImportJobProcessing || procJob.DispatchedAt == nil {
		t.Fatalf("unexpected procJob state: %+v", procJob)
	}

	// Now delayed dispatcher runs ConfirmImportJobDispatch
	// In DynamoDB import_jobs.go: if worker already transitioned to processing, ConfirmImportJobDispatch returns nil
	if err := s.ConfirmImportJobDispatch(ctx, jobID, token); err != nil {
		t.Fatalf("confirm dispatch should succeed even if worker started processing, got: %v", err)
	}

	// Verify status remains processing
	current, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if current.Status != models.ImportJobProcessing {
		t.Fatalf("expected processing status, got: %s", current.Status)
	}
}

// 7. Legacy jobs without dispatch metadata.
func TestImport_LegacyJobsWithoutDispatchMetadata(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	jobID := "legacy-job-" + uuid.NewString()
	sbID := "sb-legacy-" + uuid.NewString()
	vID := "sha256:legacy-v-" + uuid.NewString()

	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Legacy SB"})

	// Legacy job created with only basic fields
	legacyJob := &models.ImportJob{
		ID:         jobID,
		SongbookID: sbID,
		Status:     models.ImportJobQueued,
		CreatedAt:  time.Now().UTC().Add(-2 * time.Hour),
	}
	if err := s.CreateImportJob(ctx, legacyJob); err != nil {
		t.Fatalf("create legacy import job: %v", err)
	}

	// Worker claims processing
	claimed, err := s.ClaimImportJobProcessing(ctx, jobID)
	if err != nil {
		t.Fatalf("claim legacy processing: %v", err)
	}
	if claimed.Status != models.ImportJobProcessing || claimed.DispatchedAt == nil {
		t.Fatalf("expected processing status and populated dispatched_at, got: %+v", claimed)
	}

	// Assign sequential generation durably
	assignedGen, err := s.AssignImportJobGeneration(ctx, jobID, 1)
	if err != nil || assignedGen != 1 {
		t.Fatalf("assign generation: gen=%d, err=%v", assignedGen, err)
	}

	// Publication commits exact generation
	helperCreateAndReadyVersion(t, s, vID, 15)
	opts := store.PublicationOptions{JobID: jobID, JobGeneration: 1, ProcessedSongs: 15}
	if err := s.PublishCatalogVersion(ctx, sbID, vID, opts); err != nil {
		t.Fatalf("publish legacy version: %v", err)
	}

	// Verify completion
	completed, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if completed.Status != models.ImportJobCompleted || completed.Generation != 1 {
		t.Fatalf("unexpected legacy completed job: %+v", completed)
	}
}

// 8. Direct import enqueue failure: preserves manifest, records dispatch error,
// status reads do not fabricate terminal failure, and background reconciliation recovers.
func TestImport_ServiceEnqueueFailure_PreservesManifestAndUnattendedReconciliationRecovers(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Recovery SB"})

	manifestWriter := &testManifestWriter{stored: make(map[string][]byte), presigned: make(map[string]int64)}
	queue := &testImportQueue{failNext: errors.New("SQS timeout / network glitch")}

	svc := catalog.NewService(
		s, nil, nil, s, nil, nil, s, queue, manifestWriter,
	)

	songs := []*models.Song{
		{Artist: "Queen", Title: "Bohemian Rhapsody", Fingerprint: "fp-queen-1"},
	}

	// Direct import attempt: SQS enqueue returns error
	job, err := svc.CreateImportJob(ctx, sbID, songs, false)
	if err == nil {
		t.Fatal("expected enqueue error from CreateImportJob, got nil")
	}
	if job != nil {
		t.Fatal("expected nil job return on error")
	}

	// 1. Verify manifest is preserved in ManifestWriter!
	if len(manifestWriter.stored) != 1 {
		t.Fatalf("expected 1 stored manifest in S3, got %d", len(manifestWriter.stored))
	}
	var storedKey string
	for k := range manifestWriter.stored {
		storedKey = k
	}

	// 2. Read status via GetLatestImportJob: must NOT fabricate terminal failed state!
	latestJob, err := svc.GetLatestImportJob(ctx, sbID)
	if err != nil {
		t.Fatalf("get latest import job: %v", err)
	}
	if latestJob.Status != models.ImportJobQueued {
		t.Fatalf("expected status queued, got %s", latestJob.Status)
	}
	if latestJob.Error == "" {
		t.Fatal("expected recorded dispatch error, got empty")
	}
	if latestJob.DispatchClaimToken != "" {
		t.Fatalf("expected dispatch claim token cleared, got: %s", latestJob.DispatchClaimToken)
	}

	// 3. Unattended background reconciliation (no API reads) recovers undispatched job!
	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, 5)
	if err != nil {
		t.Fatalf("reconcile undispatched jobs: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled job, got %d", reconciled)
	}

	// Verify enqueued into SQS
	queue.mu.Lock()
	enqueuedCount := len(queue.enqueued)
	queue.mu.Unlock()
	if enqueuedCount != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", enqueuedCount)
	}

	// Verify confirmed dispatched in DynamoDB
	reconciledJob, err := s.GetImportJob(ctx, latestJob.ID)
	if err != nil {
		t.Fatalf("get reconciled job: %v", err)
	}
	if reconciledJob.DispatchedAt == nil {
		t.Fatal("expected dispatched_at populated after reconciliation")
	}

	// 4. Worker processes manifest and completes
	claimed, err := s.ClaimImportJobProcessing(ctx, reconciledJob.ID)
	if err != nil {
		t.Fatalf("worker claim processing: %v", err)
	}
	if claimed.Status != models.ImportJobProcessing {
		t.Fatalf("expected processing status, got: %s", claimed.Status)
	}

	// Verify manifest still in S3
	if _, ok := manifestWriter.stored[storedKey]; !ok {
		t.Fatalf("expected manifest %s still in S3", storedKey)
	}
}

// 9. Presigned uploads remain pending and dispatch_ready=false until CompleteImportUpload is accepted.
// Unfinished uploads are never auto-dispatched by background reconciliation.
func TestImport_UnfinishedPresignedUpload_IgnoredByReconciliationUntilComplete(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Upload SB"})

	manifestWriter := &testManifestWriter{stored: make(map[string][]byte), presigned: make(map[string]int64)}
	queue := &testImportQueue{}

	svc := catalog.NewService(
		s, nil, nil, s, nil, nil, s, queue, manifestWriter,
	)

	// Step 1: Create upload
	job, url, err := svc.CreateImportUpload(ctx, sbID, 100, 1024, false)
	if err != nil {
		t.Fatalf("create import upload: %v", err)
	}
	if url == "" || job.Status != models.ImportJobPending || job.DispatchReady {
		t.Fatalf("unexpected upload job: status=%s dispatch_ready=%v", job.Status, job.DispatchReady)
	}

	// Step 2: Run reconciliation while upload is in progress (unfinished)
	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, 5)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("expected 0 reconciled jobs for unfinished upload, got %d", reconciled)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("expected queue to remain empty, got %d", len(queue.enqueued))
	}

	// Step 3: Complete upload
	completedJob, err := svc.CompleteImportUpload(ctx, sbID, job.ID)
	if err != nil {
		t.Fatalf("complete import upload: %v", err)
	}
	if !completedJob.DispatchReady || completedJob.DispatchedAt == nil {
		t.Fatalf("unexpected completedJob state: %+v", completedJob)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued job after complete, got %d", len(queue.enqueued))
	}
}

// 10. Two CompleteImportUpload calls race: both succeed cleanly, enqueued once.
func TestImport_TwoCompleteImportUploadCallsRace(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Racing SB"})

	manifestWriter := &testManifestWriter{stored: make(map[string][]byte), presigned: make(map[string]int64)}
	queue := &testImportQueue{}

	svc := catalog.NewService(
		s, nil, nil, s, nil, nil, s, queue, manifestWriter,
	)

	job, _, err := svc.CreateImportUpload(ctx, sbID, 50, 512, false)
	if err != nil {
		t.Fatalf("create import upload: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = svc.CompleteImportUpload(ctx, sbID, job.ID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d error: %v", i, err)
		}
	}

	queue.mu.Lock()
	count := len(queue.enqueued)
	queue.mu.Unlock()
	if count != 1 {
		t.Fatalf("expected exactly 1 enqueue across concurrent calls, got %d", count)
	}
}

// 11. Duplicate delivery of completed job: SQS redelivery is an idempotent no-op.
func TestImport_DuplicateDelivery_CompletedJob_NoOp(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-1", Name: "SB 1"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:          jobID,
		SongbookID:  "sb-1",
		Status:      models.ImportJobCompleted,
		CompletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create completed job: %v", err)
	}

	// Claim processing on completed job must return ErrImportJobAlreadyCompleted
	_, err := s.ClaimImportJobProcessing(ctx, jobID)
	if !errors.Is(err, store.ErrImportJobAlreadyCompleted) {
		t.Fatalf("expected ErrImportJobAlreadyCompleted, got: %v", err)
	}

	// UpdateImportJob must not regress status
	err = s.UpdateImportJob(ctx, &models.ImportJob{
		ID:         jobID,
		SongbookID: "sb-1",
		Status:     models.ImportJobProcessing,
	})
	if err == nil {
		t.Fatal("expected error updating completed job to processing, got nil")
	}

	job, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if job.Status != models.ImportJobCompleted {
		t.Fatalf("status regressed from completed to %s", job.Status)
	}
}

// 12. Controlled regression: Terminal failure commits between processing claim and progress update.
// UpdateImportJobProgress must fail with ErrImportJobTerminal and not overwrite the failure or update songs.
func TestImport_TerminalFailure_CommitsBetweenProcessingClaimAndProgressUpdate(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-progress-race", Name: "SB Progress Race"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    "sb-progress-race",
		Status:        models.ImportJobQueued,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	// Worker claims processing
	claimed, err := s.ClaimImportJobProcessing(ctx, jobID)
	if err != nil {
		t.Fatalf("claim processing: %v", err)
	}
	if claimed.Status != models.ImportJobProcessing {
		t.Fatalf("expected processing status, got: %s", claimed.Status)
	}

	// Controlled interleaving: terminal failure commits before progress update
	if err := s.FailImportJob(ctx, jobID, "fatal parse error during manifest download"); err != nil {
		t.Fatalf("fail import job: %v", err)
	}

	// Worker attempts to update progress
	err = s.UpdateImportJobProgress(ctx, jobID, 150)
	if err == nil {
		t.Fatal("expected error updating progress on terminal failed job, got nil")
	}
	if !errors.Is(err, store.ErrImportJobTerminal) {
		t.Fatalf("expected ErrImportJobTerminal, got: %v", err)
	}

	// Verify job remains terminal failed with original reason and processed songs untouched
	job, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if job.Status != models.ImportJobFailed {
		t.Fatalf("expected status failed, got: %s", job.Status)
	}
	if job.Error != "fatal parse error during manifest download" {
		t.Fatalf("unexpected error message: %s", job.Error)
	}
	if job.ProcessedSongs != 0 {
		t.Fatalf("expected processed songs 0, got %d", job.ProcessedSongs)
	}
}

// 13. UpdateImportJob rejects setting status=completed directly and reviving failed jobs.
func TestImport_UpdateImportJob_RejectsCompletionAndRevival(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	// A: Reject status=completed directly (requires atomic publication transaction)
	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-direct-complete", Name: "SB Direct Complete"})
	jobA := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:         jobA,
		SongbookID: "sb-direct-complete",
		Status:     models.ImportJobProcessing,
	}); err != nil {
		t.Fatalf("create job A: %v", err)
	}

	err := s.UpdateImportJob(ctx, &models.ImportJob{
		ID:          jobA,
		SongbookID:  "sb-direct-complete",
		Status:      models.ImportJobCompleted,
		CompletedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected UpdateImportJob to reject status=completed, got nil")
	}
	if !errors.Is(err, store.ErrImportJobNotReady) {
		t.Fatalf("expected ErrImportJobNotReady for non-atomic completion, got: %v", err)
	}

	// B: Reject reviving terminal failed job
	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-revival", Name: "SB Revival"})
	jobB := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:         jobB,
		SongbookID: "sb-revival",
		Status:     models.ImportJobFailed,
		Error:      "fatal error",
	}); err != nil {
		t.Fatalf("create job B: %v", err)
	}

	err = s.UpdateImportJob(ctx, &models.ImportJob{
		ID:         jobB,
		SongbookID: "sb-revival",
		Status:     models.ImportJobProcessing,
	})
	if err == nil {
		t.Fatal("expected UpdateImportJob to reject reviving failed job, got nil")
	}
	if !errors.Is(err, store.ErrImportJobTerminal) {
		t.Fatalf("expected ErrImportJobTerminal reviving failed job, got: %v", err)
	}
}

// 14. Observable recovery for exhausted dispatch attempts and ResetImportJobDispatch.
func TestImport_ExhaustedDispatchAttempts_ObservableAndRecoverable(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-exhausted", Name: "SB Exhausted"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:               jobID,
		SongbookID:       "sb-exhausted",
		Status:           models.ImportJobQueued,
		DispatchReady:    true,
		DispatchAttempts: 5,
		Error:            "sqs connection timed out repeatedly",
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// ListDispatchableImportJobs with maxAttempts=5 should omit the exhausted job
	dispatchable, err := s.ListDispatchableImportJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list dispatchable: %v", err)
	}
	for _, j := range dispatchable {
		if j.ID == jobID {
			t.Fatalf("expected exhausted job %s to be omitted from dispatchable list", jobID)
		}
	}

	// ListExhaustedImportJobs with maxAttempts=5 must return it
	exhausted, err := s.ListExhaustedImportJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list exhausted: %v", err)
	}
	found := false
	for _, j := range exhausted {
		if j.ID == jobID {
			found = true
			if j.DispatchAttempts != 5 {
				t.Fatalf("expected 5 dispatch attempts, got %d", j.DispatchAttempts)
			}
			if j.Error != "sqs connection timed out repeatedly" {
				t.Fatalf("unexpected error message: %s", j.Error)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected job %s in exhausted list", jobID)
	}

	// ResetImportJobDispatch clears attempts and claim state
	if err := s.ResetImportJobDispatch(ctx, jobID); err != nil {
		t.Fatalf("reset import job dispatch: %v", err)
	}

	// Verify job is now dispatchable again
	dispatchableAfter, err := s.ListDispatchableImportJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list dispatchable after reset: %v", err)
	}
	foundAfter := false
	for _, j := range dispatchableAfter {
		if j.ID == jobID {
			foundAfter = true
			if j.DispatchAttempts != 0 {
				t.Fatalf("expected 0 attempts after reset, got %d", j.DispatchAttempts)
			}
			if j.Error != "" {
				t.Fatalf("expected error cleared, got: %s", j.Error)
			}
			break
		}
	}
	if !foundAfter {
		t.Fatalf("expected job %s to be dispatchable after reset", jobID)
	}
}

// 15. Unattended recovery through ReconcileUndispatchedJobs:
// Recovers undispatched jobs without API reads, bounding attempts and preserving intact state.
func TestImport_Reconciler_UnattendedRecovery(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-unattended", Name: "SB Unattended"})
	jobID := "job-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            jobID,
		SongbookID:    "sb-unattended",
		Status:        models.ImportJobQueued,
		DispatchReady: true,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	queue := &testImportQueue{}
	svc := &catalog.Service{
		ImportJobs:  s,
		ImportQueue: queue,
	}

	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, 5)
	if err != nil {
		t.Fatalf("reconcile undispatched: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled job, got %d", reconciled)
	}

	if len(queue.enqueued) != 1 || queue.enqueued[0].ID != jobID {
		t.Fatalf("expected job %s enqueued to SQS, got: %+v", jobID, queue.enqueued)
	}

	// Verify dispatched_at is confirmed
	job, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.DispatchedAt == nil {
		t.Fatal("expected dispatched_at to be populated")
	}
}

// 16. Delayed CompleteImportUpload racing with worker processing:
// MarkImportJobDispatchReady rejects processing jobs, and CompleteImportUpload reloads and returns current state
// without regressing processing to queued.
func TestImport_DelayedCompleteImportUpload_DoesNotRegressProcessing(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	sbID := "sb-delayed-" + uuid.NewString()
	helperCreateSongbook(t, s, &models.Songbook{ID: sbID, Name: "Delayed SB"})

	queue := &testImportQueue{}
	manifestWriter := &testManifestWriter{stored: make(map[string][]byte), presigned: make(map[string]int64)}
	svc := catalog.NewService(nil, s, nil, nil, nil, nil, s, queue, manifestWriter)

	job, _, err := svc.CreateImportUpload(ctx, sbID, 10, 1024, false)
	if err != nil {
		t.Fatalf("create import upload: %v", err)
	}
	if job.DispatchReady {
		t.Fatal("expected new upload job to have dispatch_ready=false")
	}

	// Worker receives message and claims processing before CompleteImportUpload arrives
	claimed, err := s.ClaimImportJobProcessing(ctx, job.ID)
	if err != nil {
		t.Fatalf("claim import job processing: %v", err)
	}
	if claimed.Status != models.ImportJobProcessing {
		t.Fatalf("expected status processing, got %s", claimed.Status)
	}

	// Now delayed CompleteImportUpload is invoked
	res, err := svc.CompleteImportUpload(ctx, sbID, job.ID)
	if err != nil {
		t.Fatalf("expected successful race reload on CompleteImportUpload, got: %v", err)
	}
	if res.Status != models.ImportJobProcessing {
		t.Fatalf("expected returned job status to be processing, got: %s", res.Status)
	}

	// Verify DynamoDB record was not regressed to queued
	stored, err := s.GetImportJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get import job: %v", err)
	}
	if stored.Status != models.ImportJobProcessing {
		t.Fatalf("expected stored job status to remain processing, got: %s", stored.Status)
	}
}

// 17. ResetImportJobDispatch restriction:
// Rejects unfinished uploads, confirmed jobs, and active unexpired claims, preserving readiness.
func TestImport_ResetImportJobDispatch_RejectsUnfinishedConfirmedAndActiveClaim(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	// 17a. Unfinished upload (dispatch_ready = false): must be rejected with ErrImportJobNotReady
	helperCreateSongbook(t, s, &models.Songbook{ID: "sb-reset-test", Name: "SB Reset Test"})
	unfinishedID := "job-unfin-" + uuid.NewString()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            unfinishedID,
		SongbookID:    "sb-reset-test",
		Status:        models.ImportJobPending,
		DispatchReady: false,
	}); err != nil {
		t.Fatalf("create unfinished job: %v", err)
	}

	err := s.ResetImportJobDispatch(ctx, unfinishedID)
	if !errors.Is(err, store.ErrImportJobNotReady) {
		t.Fatalf("expected ErrImportJobNotReady for unfinished upload, got: %v", err)
	}
	unfinStored, err := s.GetImportJob(ctx, unfinishedID)
	if err != nil {
		t.Fatalf("get unfinished job: %v", err)
	}
	if unfinStored.DispatchReady {
		t.Fatal("reset must not authorize unfinished uploads: dispatch_ready must remain false")
	}

	// 17b. Confirmed job (dispatched_at != nil): must be rejected with ErrImportJobAlreadyDispatched
	confID := "job-conf-" + uuid.NewString()
	confTime := time.Now().UTC()
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:            confID,
		SongbookID:    "sb-reset-test",
		Status:        models.ImportJobQueued,
		DispatchReady: true,
		DispatchedAt:  &confTime,
	}); err != nil {
		t.Fatalf("create confirmed job: %v", err)
	}

	err = s.ResetImportJobDispatch(ctx, confID)
	if !errors.Is(err, store.ErrImportJobAlreadyDispatched) {
		t.Fatalf("expected ErrImportJobAlreadyDispatched for confirmed job, got: %v", err)
	}

	// 17c. Active unexpired claim: must be rejected with ErrImportJobClaimConflict
	activeClaimID := "job-active-" + uuid.NewString()
	claimExpires := time.Now().UTC().Add(10 * time.Minute)
	if err := s.CreateImportJob(ctx, &models.ImportJob{
		ID:                     activeClaimID,
		SongbookID:             "sb-reset-test",
		Status:                 models.ImportJobQueued,
		DispatchReady:          true,
		DispatchClaimToken:     "active-token-123",
		DispatchClaimExpiresAt: &claimExpires,
		DispatchAttempts:       2,
	}); err != nil {
		t.Fatalf("create active claim job: %v", err)
	}

	err = s.ResetImportJobDispatch(ctx, activeClaimID)
	if !errors.Is(err, store.ErrImportJobClaimConflict) {
		t.Fatalf("expected ErrImportJobClaimConflict for active claim, got: %v", err)
	}
	activeStored, err := s.GetImportJob(ctx, activeClaimID)
	if err != nil {
		t.Fatalf("get active claim job: %v", err)
	}
	if activeStored.DispatchClaimToken != "active-token-123" {
		t.Fatalf("active claim token should remain intact, got: %s", activeStored.DispatchClaimToken)
	}
}
