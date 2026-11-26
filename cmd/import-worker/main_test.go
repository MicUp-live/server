package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type fakeS3Reader struct {
	objects map[string][]byte
}

func (f *fakeS3Reader) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := *params.Key
	data, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

type fakeJobQueue struct {
	enqueued []catalog.BuildJob
}

func (f *fakeJobQueue) Enqueue(_ context.Context, job catalog.BuildJob) error {
	f.enqueued = append(f.enqueued, job)
	return nil
}

type fakeImportStore struct {
	jobs map[string]*models.ImportJob
}

func (f *fakeImportStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportStore) GetImportJob(_ context.Context, id string) (*models.ImportJob, error) {
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeImportStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeImportStore) DeleteImportJob(_ context.Context, id string) error { return nil }
func (f *fakeImportStore) UpdateImportJob(_ context.Context, job *models.ImportJob) error {
	if existing, ok := f.jobs[job.ID]; ok {
		if job.Generation > 0 && existing.Generation > 0 && existing.Generation != job.Generation {
			return store.ErrJobGenerationConflict
		}
		if job.Generation == 0 && existing.Generation > 0 {
			job.Generation = existing.Generation
		}
	}
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeImportStore) LatestImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeImportStore) AssignImportJobGeneration(_ context.Context, jobID string, generation int64) (int64, error) {
	if j, ok := f.jobs[jobID]; ok {
		if j.Generation > 0 {
			return j.Generation, nil
		}
		j.Generation = generation
		return generation, nil
	}
	return 0, os.ErrNotExist
}
func (f *fakeImportStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}

func (f *fakeImportStore) MarkImportJobDispatchReady(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status == models.ImportJobProcessing || j.DispatchedAt != nil {
		return store.ErrImportJobAlreadyDispatched
	}
	j.DispatchReady = true
	j.Status = models.ImportJobQueued
	return nil
}

func (f *fakeImportStore) ClaimImportJobDispatch(_ context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, "", store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, "", store.ErrImportJobTerminal
	}
	if j.DispatchedAt != nil {
		return nil, "", store.ErrImportJobAlreadyDispatched
	}
	if !j.DispatchReady {
		return nil, "", store.ErrImportJobNotReady
	}
	token := "claim-token-" + jobID
	exp := time.Now().Add(leaseDuration)
	j.DispatchClaimToken = token
	j.DispatchClaimExpiresAt = &exp
	j.DispatchAttempts++
	return j, token, nil
}

func (f *fakeImportStore) ConfirmImportJobDispatch(_ context.Context, jobID string, claimToken string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.DispatchedAt = &now
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportStore) RecordDispatchError(_ context.Context, jobID string, claimToken string, errMsg string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	j.Error = errMsg
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeImportStore) ListDispatchableImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts < maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportStore) ClaimImportJobProcessing(_ context.Context, jobID string) (*models.ImportJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, store.ErrImportJobTerminal
	}
	j.Status = models.ImportJobProcessing
	if j.DispatchedAt == nil {
		now := time.Now().UTC()
		j.DispatchedAt = &now
	}
	return j, nil
}

func (f *fakeImportStore) FailImportJob(_ context.Context, jobID string, reason string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	j.Status = models.ImportJobFailed
	j.Error = reason
	return nil
}

func (f *fakeImportStore) ListExhaustedImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts >= maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeImportStore) UpdateImportJobProgress(_ context.Context, jobID string, processedSongs int) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status != models.ImportJobProcessing {
		return os.ErrNotExist
	}
	j.ProcessedSongs = processedSongs
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeImportStore) ResetImportJobDispatch(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if !j.DispatchReady {
		return store.ErrImportJobNotReady
	}
	if j.DispatchedAt != nil || j.Status == models.ImportJobProcessing {
		return store.ErrImportJobAlreadyDispatched
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.DispatchClaimExpiresAt != nil && j.DispatchClaimExpiresAt.After(time.Now().UTC()) {
		return store.ErrImportJobClaimConflict
	}
	j.DispatchAttempts = 0
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	j.Error = ""
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func TestProcessRecord_PreservesPruneMissingFalse(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": false
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-1.json": manifestJSON,
		},
	}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-1": {ID: "job-1", SongbookID: "book-1", Status: models.ImportJobQueued, PruneMissing: false},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-1", ManifestKey: "imports/job-1.json", PruneMissing: false}
	recordBody, _ := json.Marshal(job)

	err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket")
	if err != nil {
		t.Fatalf("processRecord failed: %v", err)
	}

	if len(queue.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued build job, got %d", len(queue.enqueued))
	}
	enqueuedJob := queue.enqueued[0]
	if enqueuedJob.PruneMissing {
		t.Errorf("expected enqueued build job PruneMissing to be false, got true")
	}
	if enqueuedJob.SongbookID != "book-1" || enqueuedJob.ManifestKey != "imports/job-1.json" {
		t.Errorf("unexpected enqueued job: %+v", enqueuedJob)
	}
}

func TestProcessRecord_CarriesPruneMissingTrue(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": true
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-2.json": manifestJSON,
		},
	}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-2": {ID: "job-2", SongbookID: "book-1", Status: models.ImportJobQueued, PruneMissing: true},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-2", ManifestKey: "imports/job-2.json", PruneMissing: true}
	recordBody, _ := json.Marshal(job)

	err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket")
	if err != nil {
		t.Fatalf("processRecord failed: %v", err)
	}

	if len(queue.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued build job, got %d", len(queue.enqueued))
	}
	enqueuedJob := queue.enqueued[0]
	if !enqueuedJob.PruneMissing {
		t.Errorf("expected enqueued build job PruneMissing to be true, got false")
	}
}

func TestProcessRecord_CarriesGeneration(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": false
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-gen.json": manifestJSON,
		},
	}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-gen": {ID: "job-gen", SongbookID: "book-1", Status: models.ImportJobQueued, Generation: 42},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-gen", ManifestKey: "imports/job-gen.json", Generation: 42}
	recordBody, _ := json.Marshal(job)

	err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket")
	if err != nil {
		t.Fatalf("processRecord failed: %v", err)
	}

	if len(queue.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued build job, got %d", len(queue.enqueued))
	}
	enqueuedJob := queue.enqueued[0]
	if enqueuedJob.JobGeneration != 42 {
		t.Errorf("expected enqueued build job JobGeneration to be 42, got %d", enqueuedJob.JobGeneration)
	}
}

type fakeLegacyImportStore struct {
	jobs       map[string]*models.ImportJob
	nextGen    int64
	nextGenErr error
}

func (f *fakeLegacyImportStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeLegacyImportStore) GetImportJob(_ context.Context, id string) (*models.ImportJob, error) {
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeLegacyImportStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) DeleteImportJob(_ context.Context, id string) error { return nil }
func (f *fakeLegacyImportStore) UpdateImportJob(_ context.Context, job *models.ImportJob) error {
	if existing, ok := f.jobs[job.ID]; ok {
		if job.Generation > 0 && existing.Generation > 0 && existing.Generation != job.Generation {
			return store.ErrJobGenerationConflict
		}
		if job.Generation == 0 && existing.Generation > 0 {
			job.Generation = existing.Generation
		}
	}
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeLegacyImportStore) LatestImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) AssignImportJobGeneration(_ context.Context, jobID string, generation int64) (int64, error) {
	if j, ok := f.jobs[jobID]; ok {
		if j.Generation > 0 {
			return j.Generation, nil
		}
		j.Generation = generation
		return generation, nil
	}
	return 0, os.ErrNotExist
}

func (f *fakeLegacyImportStore) MarkImportJobDispatchReady(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status == models.ImportJobProcessing || j.DispatchedAt != nil {
		return store.ErrImportJobAlreadyDispatched
	}
	j.DispatchReady = true
	j.Status = models.ImportJobQueued
	return nil
}

func (f *fakeLegacyImportStore) ClaimImportJobDispatch(_ context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, "", store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, "", store.ErrImportJobTerminal
	}
	if j.DispatchedAt != nil {
		return nil, "", store.ErrImportJobAlreadyDispatched
	}
	if !j.DispatchReady {
		return nil, "", store.ErrImportJobNotReady
	}
	token := "claim-token-" + jobID
	exp := time.Now().Add(leaseDuration)
	j.DispatchClaimToken = token
	j.DispatchClaimExpiresAt = &exp
	j.DispatchAttempts++
	return j, token, nil
}

func (f *fakeLegacyImportStore) ConfirmImportJobDispatch(_ context.Context, jobID string, claimToken string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.DispatchedAt = &now
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeLegacyImportStore) RecordDispatchError(_ context.Context, jobID string, claimToken string, errMsg string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	j.Error = errMsg
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeLegacyImportStore) ListDispatchableImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts < maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeLegacyImportStore) ClaimImportJobProcessing(_ context.Context, jobID string) (*models.ImportJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return nil, store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return nil, store.ErrImportJobTerminal
	}
	j.Status = models.ImportJobProcessing
	if j.DispatchedAt == nil {
		now := time.Now().UTC()
		j.DispatchedAt = &now
	}
	return j, nil
}

func (f *fakeLegacyImportStore) FailImportJob(_ context.Context, jobID string, reason string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	j.Status = models.ImportJobFailed
	j.Error = reason
	return nil
}

func (f *fakeLegacyImportStore) ListExhaustedImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts >= maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeLegacyImportStore) UpdateImportJobProgress(_ context.Context, jobID string, processedSongs int) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.Status != models.ImportJobProcessing {
		return os.ErrNotExist
	}
	j.ProcessedSongs = processedSongs
	j.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeLegacyImportStore) ResetImportJobDispatch(_ context.Context, jobID string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	if !j.DispatchReady {
		return store.ErrImportJobNotReady
	}
	if j.DispatchedAt != nil || j.Status == models.ImportJobProcessing {
		return store.ErrImportJobAlreadyDispatched
	}
	if j.Status == models.ImportJobCompleted {
		return store.ErrImportJobAlreadyCompleted
	}
	if j.Status == models.ImportJobFailed {
		return store.ErrImportJobTerminal
	}
	if j.DispatchClaimExpiresAt != nil && j.DispatchClaimExpiresAt.After(time.Now().UTC()) {
		return store.ErrImportJobClaimConflict
	}
	j.DispatchAttempts = 0
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	j.Error = ""
	j.UpdatedAt = time.Now().UTC()
	return nil
}
func (f *fakeLegacyImportStore) CreateSongbook(_ context.Context, _ *models.Songbook) error {
	return nil
}
func (f *fakeLegacyImportStore) UpdateSongbook(_ context.Context, _ *models.Songbook) error {
	return nil
}
func (f *fakeLegacyImportStore) DeleteSongbook(_ context.Context, _ string) error { return nil }
func (f *fakeLegacyImportStore) GetSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) GetPublicSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) GetSongbookBySlug(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) ListSongbooksByOwner(_ context.Context, _ string) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeLegacyImportStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	if f.nextGenErr != nil {
		return 0, f.nextGenErr
	}
	return f.nextGen, nil
}

func TestProcessRecord_LegacyJob_AllocatesAndPersistsSequentialGeneration(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": false
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-legacy.json": manifestJSON,
		},
	}
	db := &fakeLegacyImportStore{
		jobs: map[string]*models.ImportJob{
			"job-legacy": {ID: "job-legacy", SongbookID: "book-1", Status: models.ImportJobQueued, Generation: 0},
		},
		nextGen: 7,
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-legacy", ManifestKey: "imports/job-legacy.json", Generation: 0}
	recordBody, _ := json.Marshal(job)

	err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket")
	if err != nil {
		t.Fatalf("processRecord failed: %v", err)
	}

	if len(queue.enqueued) != 1 {
		t.Fatalf("expected 1 enqueued build job, got %d", len(queue.enqueued))
	}
	enqueuedJob := queue.enqueued[0]
	if enqueuedJob.JobGeneration != 7 {
		t.Errorf("expected enqueued build job JobGeneration to be sequential 7, got %d", enqueuedJob.JobGeneration)
	}
	persistedJob := db.jobs["job-legacy"]
	if persistedJob.Generation != 7 {
		t.Errorf("expected persisted job generation to be 7, got %d", persistedJob.Generation)
	}
}

func TestProcessRecord_LegacyJob_PropagatesAllocationFailure(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": false
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-legacy.json": manifestJSON,
		},
	}
	db := &fakeLegacyImportStore{
		jobs: map[string]*models.ImportJob{
			"job-legacy": {ID: "job-legacy", SongbookID: "book-1", Status: models.ImportJobQueued, Generation: 0},
		},
		nextGenErr: errors.New("dynamo allocation failed"),
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-legacy", ManifestKey: "imports/job-legacy.json", Generation: 0}
	recordBody, _ := json.Marshal(job)

	err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket")
	if err == nil {
		t.Fatal("expected processRecord to fail on generation allocation error, got nil")
	}
	if len(queue.enqueued) != 0 {
		t.Errorf("expected no job to be enqueued on allocation error, got %d", len(queue.enqueued))
	}
}

func TestProcessRecord_ConcurrentDeliveries_ConvergeOnAssignedGeneration(t *testing.T) {
	ctx := context.Background()
	manifestJSON := []byte(`{
		"songs": [{"artist": "The Beatles", "title": "Yesterday"}],
		"prune_missing": false
	}`)
	s3c := &fakeS3Reader{
		objects: map[string][]byte{
			"imports/job-concurrent.json": manifestJSON,
		},
	}
	db := &fakeLegacyImportStore{
		jobs: map[string]*models.ImportJob{
			"job-concurrent": {ID: "job-concurrent", SongbookID: "book-1", Status: models.ImportJobQueued, Generation: 0},
		},
		nextGen: 10,
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-concurrent", ManifestKey: "imports/job-concurrent.json", Generation: 0}
	recordBody, _ := json.Marshal(job)

	// Delivery 1 processes record
	if err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket"); err != nil {
		t.Fatalf("delivery 1 failed: %v", err)
	}

	// Change nextGen to 11 to simulate subsequent increments on the songbook
	db.nextGen = 11

	// Delivery 2 processes record (duplicate delivery of the same legacy job)
	if err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket"); err != nil {
		t.Fatalf("delivery 2 failed: %v", err)
	}

	if len(queue.enqueued) != 2 {
		t.Fatalf("expected 2 enqueued jobs, got %d", len(queue.enqueued))
	}

	// Both deliveries must converge on the exact same persisted generation (10), never 11!
	if queue.enqueued[0].JobGeneration != 10 {
		t.Errorf("delivery 1 enqueued generation %d, expected 10", queue.enqueued[0].JobGeneration)
	}
	if queue.enqueued[1].JobGeneration != 10 {
		t.Errorf("delivery 2 enqueued generation %d, expected 10 (did not converge!)", queue.enqueued[1].JobGeneration)
	}
}

func TestProcessRecord_CompletedJobRedelivery_ReturnsNilWithoutEnqueuing(t *testing.T) {
	ctx := context.Background()
	s3c := &fakeS3Reader{objects: map[string][]byte{}}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-done": {ID: "job-done", SongbookID: "book-1", Status: models.ImportJobCompleted},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-done", ManifestKey: "imports/job-done.json", Generation: 1}
	recordBody, _ := json.Marshal(job)

	// Completed job redelivery must be a safe no-op returning nil without enqueuing
	if err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket"); err != nil {
		t.Fatalf("expected nil on completed job redelivery, got: %v", err)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("expected 0 enqueued jobs for completed redelivery, got %d", len(queue.enqueued))
	}
}

func TestProcessRecord_CompletedJobWithDeletedManifest_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	// S3 does not have the manifest (it was deleted after completion)
	s3c := &fakeS3Reader{objects: map[string][]byte{}}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-done-deleted": {ID: "job-done-deleted", SongbookID: "book-1", Status: models.ImportJobCompleted},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-done-deleted", ManifestKey: "imports/job-done-deleted.json", Generation: 1}
	recordBody, _ := json.Marshal(job)

	// Must return nil and not error out because manifest was already deleted
	if err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket"); err != nil {
		t.Fatalf("expected nil for completed job with deleted manifest, got: %v", err)
	}
}

func TestProcessRecord_TerminalFailedJob_ReturnsNilWithoutEnqueuing(t *testing.T) {
	ctx := context.Background()
	s3c := &fakeS3Reader{objects: map[string][]byte{}}
	db := &fakeImportStore{
		jobs: map[string]*models.ImportJob{
			"job-failed": {ID: "job-failed", SongbookID: "book-1", Status: models.ImportJobFailed, Error: "fatal error"},
		},
	}
	queue := &fakeJobQueue{}

	job := importjob.Job{ID: "job-failed", ManifestKey: "imports/job-failed.json", Generation: 1}
	recordBody, _ := json.Marshal(job)

	if err := processRecord(ctx, string(recordBody), db, s3c, queue, "test-bucket"); err != nil {
		t.Fatalf("expected nil for terminal failed job, got: %v", err)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("expected 0 enqueued jobs for terminal failed job, got %d", len(queue.enqueued))
	}
}
