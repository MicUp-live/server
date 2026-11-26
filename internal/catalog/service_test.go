package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestCatalogService_CRUDAndResolve(t *testing.T) {
	changes := newFakeCatalogChangeStore()
	svc := NewService(nil, nil, nil, nil, nil, changes, nil, nil, nil)
	ctx := context.Background()

	// 1. Create Song
	created, err := svc.CreateSong(ctx, "sb-1", models.SongCreate{
		Artist: "Radiohead",
		Title:  "Karma Police",
	})
	if err != nil {
		t.Fatalf("unexpected error creating song: %v", err)
	}
	if created.ID == "" || created.Artist != "Radiohead" || created.Title != "Karma Police" {
		t.Fatalf("unexpected created song: %+v", created)
	}

	// 2. Resolve Song
	resolved, err := svc.ResolveCatalogSong(ctx, "sb-1", created.ID)
	if err != nil {
		t.Fatalf("unexpected error resolving song: %v", err)
	}
	if resolved.Title != "Karma Police" {
		t.Fatalf("expected Karma Police, got %s", resolved.Title)
	}

	// 3. Update Song
	newTitle := "Karma Police (Acoustic)"
	updated, err := svc.UpdateSong(ctx, "sb-1", created.ID, models.SongUpdate{
		Title: &newTitle,
	})
	if err != nil {
		t.Fatalf("unexpected error updating song: %v", err)
	}
	if updated.Title != newTitle {
		t.Fatalf("expected %s, got %s", newTitle, updated.Title)
	}

	// 4. Delete Song
	if err := svc.DeleteSong(ctx, "sb-1", created.ID); err != nil {
		t.Fatalf("unexpected error deleting song: %v", err)
	}
	_, err = svc.ResolveCatalogSong(ctx, "sb-1", created.ID)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestCatalogService_ParseFiles(t *testing.T) {
	req := ParseFilesReq{
		Pattern: "artist-title",
		Files: []ParseFileItem{
			{Name: "Queen - Bohemian Rhapsody (Remastered).mp3", Path: "/music/queen.mp3", Size: 1048576},
			{Name: "The Beatles - Hey Jude [1968].mp3", Size: 2048576},
		},
	}

	results := ParseFiles(req)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Artist != "Queen" || results[0].Title != "Bohemian Rhapsody" || results[0].Confidence != "high" {
		t.Fatalf("unexpected parsed result 0: %+v", results[0])
	}
	if results[1].Artist != "The Beatles" || results[1].Title != "Hey Jude" || results[1].Confidence != "high" {
		t.Fatalf("unexpected parsed result 1: %+v", results[1])
	}

	// Pre-parse extraction of known manufacturer disc codes (leading, trailing, bracketed)
	discResults := ParseFiles(ParseFilesReq{
		Pattern: "artist-title",
		Files: []ParseFileItem{
			{Name: "SC8101-02 - Queen - Bohemian Rhapsody.zip", Size: 100},
			{Name: "Queen - Bohemian Rhapsody - SF045-12.mp3", Size: 200},
			{Name: "Queen - Bohemian Rhapsody [CB5001-01].cdg", Size: 300},
		},
	})
	wantDiscs := []string{"SC8101-02", "SF045-12", "CB5001-01"}
	for i, wantDisc := range wantDiscs {
		got := discResults[i]
		if got.Disc != wantDisc || got.Artist != "Queen" || got.Title != "Bohemian Rhapsody" {
			t.Fatalf("disc parse %d = %+v; want disc=%q artist=Queen title=Bohemian Rhapsody", i, got, wantDisc)
		}
	}
}

type fakeServiceSongbookStore struct {
	store.SongbookStore
	books      map[string]*models.Songbook
	nextGenErr error
	nextGen    int64
}

func (f *fakeServiceSongbookStore) CreateSongbook(_ context.Context, sb *models.Songbook) error {
	return nil
}
func (f *fakeServiceSongbookStore) UpdateSongbook(_ context.Context, sb *models.Songbook) error {
	return nil
}
func (f *fakeServiceSongbookStore) DeleteSongbook(_ context.Context, id string) error { return nil }
func (f *fakeServiceSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	if b, ok := f.books[id]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceSongbookStore) GetPublicSongbook(_ context.Context, publicID string) (*models.Songbook, error) {
	for _, b := range f.books {
		if (b.PublicID == publicID || b.Slug == publicID) && b.IsPublic {
			return b, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceSongbookStore) GetSongbookBySlug(_ context.Context, slug string) (*models.Songbook, error) {
	for _, b := range f.books {
		if b.Slug == slug {
			return b, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeServiceSongbookStore) ListSongbooksByOwner(_ context.Context, _ string) ([]*models.Songbook, error) {
	return nil, nil
}
func (f *fakeServiceSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	if f.nextGenErr != nil {
		return 0, f.nextGenErr
	}
	if f.nextGen > 0 {
		return f.nextGen, nil
	}
	return 1, nil
}
func (f *fakeServiceSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	for _, b := range f.books {
		if b.ID == id {
			b.Deleting = true
			copy := *b
			return &copy, nil
		}
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type fakeServiceEventStore struct {
	store.EventStore
	events map[string]*models.Event
}

func (f *fakeServiceEventStore) CreateEvent(_ context.Context, ev *models.Event) error { return nil }
func (f *fakeServiceEventStore) GetEvent(_ context.Context, id string) (*models.Event, error) {
	if ev, ok := f.events[id]; ok {
		return ev, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceEventStore) GetEventByJoinCode(_ context.Context, code string) (*models.Event, error) {
	for _, ev := range f.events {
		if ev.JoinCode == code {
			return ev, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceEventStore) ListEventsByOwner(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}
func (f *fakeServiceEventStore) ListEventsBySongbook(_ context.Context, sbID string) ([]*models.Event, error) {
	var list []*models.Event
	for _, ev := range f.events {
		if ev.SongbookID == sbID {
			list = append(list, ev)
		}
	}
	return list, nil
}
func (f *fakeServiceEventStore) UpdateEvent(_ context.Context, _ *models.Event) error { return nil }
func (f *fakeServiceEventStore) UpdateEventWithVersion(_ context.Context, _ *models.Event, _ bool) error {
	return nil
}
func (f *fakeServiceEventStore) DeleteEvent(_ context.Context, _ string) error { return nil }
func (f *fakeServiceEventStore) BeginEventDeletion(_ context.Context, _ string) error {
	return nil
}

type fakeServiceCatalogStore struct {
	versions map[string]*models.CatalogVersion
}

func (f *fakeServiceCatalogStore) CreateCatalogVersion(_ context.Context, v *models.CatalogVersion) error {
	return nil
}
func (f *fakeServiceCatalogStore) GetCatalogVersion(_ context.Context, id string) (*models.CatalogVersion, error) {
	if v, ok := f.versions[id]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeServiceCatalogStore) MarkCatalogVersionReady(_ context.Context, _, _ string, _ int, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeServiceCatalogStore) FailCatalogVersion(_ context.Context, _, _ string, _ ...string) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeServiceCatalogStore) ReclaimCatalogVersion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeServiceCatalogStore) PublishCatalogVersion(_ context.Context, _, _ string, _ ...store.PublicationOptions) error {
	return nil
}
func (f *fakeServiceCatalogStore) DeleteCatalogVersion(_ context.Context, _ string, _ ...string) error {
	return nil
}
func (f *fakeServiceCatalogStore) ReserveCatalogVersionForDeletion(_ context.Context, _ string, _ time.Duration) (*models.CatalogVersion, error) {
	return nil, nil
}
func (f *fakeServiceCatalogStore) ReleaseCatalogVersionReservation(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeServiceCatalogStore) IsCatalogVersionReferenced(_ context.Context, _ string, _ ...string) (bool, error) {
	return false, nil
}
func (f *fakeServiceCatalogStore) PurgeCatalogVersion(_ context.Context, _ string, _ func(string) error, _ ...store.PurgeCatalogOptions) error {
	return nil
}
func (f *fakeServiceCatalogStore) PurgeOrphanCatalogPrefix(_ context.Context, _ string, _ func(string) error, _ ...store.PurgeCatalogOptions) error {
	return nil
}

func TestCatalogService_TemporaryPublicViaEventCode(t *testing.T) {
	ctx := context.Background()

	privateBook := &models.Songbook{
		ID:               "sb-private",
		PublicID:         "pub-private-123",
		Slug:             "privateshow",
		Name:             "Secret Songbook",
		IsPublic:         false,
		CatalogVersionID: "cat-v1",
	}
	publicBook := &models.Songbook{
		ID:               "sb-public",
		PublicID:         "pub-public-456",
		Slug:             "publicshow",
		Name:             "Open Songbook",
		IsPublic:         true,
		CatalogVersionID: "cat-v2",
	}
	sbStore := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			privateBook.ID: privateBook,
			publicBook.ID:  publicBook,
		},
	}

	activeEvent := &models.Event{
		ID:         "evt-active",
		JoinCode:   "ACTIVE1",
		SongbookID: privateBook.ID,
		Status:     models.EventStatusActive,
	}
	pausedEvent := &models.Event{
		ID:         "evt-paused",
		JoinCode:   "PAUSED1",
		SongbookID: privateBook.ID,
		Status:     models.EventStatusPaused,
	}
	endedEvent := &models.Event{
		ID:         "evt-ended",
		JoinCode:   "ENDED1",
		SongbookID: privateBook.ID,
		Status:     models.EventStatusEnded,
	}
	otherEvent := &models.Event{
		ID:         "evt-other",
		JoinCode:   "OTHER1",
		SongbookID: "other-book-id",
		Status:     models.EventStatusActive,
	}
	evStore := &fakeServiceEventStore{
		events: map[string]*models.Event{
			activeEvent.ID: activeEvent,
			pausedEvent.ID: pausedEvent,
			endedEvent.ID:  endedEvent,
			otherEvent.ID:  otherEvent,
		},
	}

	catStore := &fakeServiceCatalogStore{
		versions: map[string]*models.CatalogVersion{
			"cat-v1": {ID: "cat-v1", Status: models.CatalogVersionReady, SongCount: 42, AssetPrefix: "catalogs/v1"},
			"cat-v2": {ID: "cat-v2", Status: models.CatalogVersionReady, SongCount: 100, AssetPrefix: "catalogs/v2"},
		},
	}

	svc := NewService(sbStore, evStore, nil, catStore, nil, nil, nil, nil, nil)

	// 1. Public songbook should resolve without any event code
	pubManifest, err := svc.GetPublicManifest(ctx, publicBook.PublicID, "")
	if err != nil {
		t.Fatalf("expected public songbook to resolve, got %v", err)
	}
	if pubManifest["static_ready"] != true || pubManifest["name"] != "Open Songbook" {
		t.Fatalf("unexpected public manifest: %+v", pubManifest)
	}

	// 2. Private songbook without event code should be rejected (404 / ErrSongbookNotFound)
	_, err = svc.GetPublicManifest(ctx, privateBook.PublicID, "")
	if !errors.Is(err, ErrSongbookNotFound) {
		t.Fatalf("expected ErrSongbookNotFound for private book without event code, got %v", err)
	}

	// 3. Private songbook with valid active event join code should be ALLOWED (temporarily public)
	manifestByCode, err := svc.GetPublicManifest(ctx, privateBook.PublicID, "ACTIVE1")
	if err != nil {
		t.Fatalf("expected private songbook to resolve with active join code, got %v", err)
	}
	if manifestByCode["static_ready"] != true || manifestByCode["name"] != "Secret Songbook" {
		t.Fatalf("unexpected manifest with active join code: %+v", manifestByCode)
	}

	// 4. Private songbook with valid active event ID should also be ALLOWED
	manifestByEventID, err := svc.GetPublicManifest(ctx, privateBook.PublicID, "evt-active")
	if err != nil {
		t.Fatalf("expected private songbook to resolve with active event ID, got %v", err)
	}
	if manifestByEventID["static_ready"] != true {
		t.Fatalf("unexpected manifest with active event ID: %+v", manifestByEventID)
	}

	// 5. Private songbook with paused event (live show on break) should be ALLOWED
	manifestByPaused, err := svc.GetPublicManifest(ctx, privateBook.PublicID, "PAUSED1")
	if err != nil {
		t.Fatalf("expected private songbook to resolve with paused event, got %v", err)
	}
	if manifestByPaused["static_ready"] != true {
		t.Fatalf("unexpected manifest with paused event: %+v", manifestByPaused)
	}

	// 6. Private songbook with ENDED event should be REJECTED (access expired)
	_, err = svc.GetPublicManifest(ctx, privateBook.PublicID, "ENDED1")
	if !errors.Is(err, ErrSongbookNotFound) {
		t.Fatalf("expected ErrSongbookNotFound for ended event, got %v", err)
	}

	// 7. Private songbook with event code for a DIFFERENT songbook should be REJECTED
	_, err = svc.GetPublicManifest(ctx, privateBook.PublicID, "OTHER1")
	if !errors.Is(err, ErrSongbookNotFound) {
		t.Fatalf("expected ErrSongbookNotFound for unrelated event code, got %v", err)
	}

	// 8. Private songbook accessed using its internal ID with active event code should be ALLOWED
	manifestByID, err := svc.GetPublicManifest(ctx, privateBook.ID, "ACTIVE1")
	if err != nil {
		t.Fatalf("expected private songbook to resolve by internal ID with code, got %v", err)
	}
	if manifestByID["static_ready"] != true {
		t.Fatalf("unexpected manifest by internal ID: %+v", manifestByID)
	}
}

type fakeServiceImportJobStore struct {
	jobs                map[string]*models.ImportJob
	markReadyErrs       map[string]error
	claimDispatchErrs   map[string]error
	confirmDispatchErrs map[string]error
	getJobReloadErrs    map[string]error
	getJobCalls         map[string]int
}

func (f *fakeServiceImportJobStore) CreateImportJob(_ context.Context, job *models.ImportJob) error {
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeServiceImportJobStore) GetImportJob(_ context.Context, id string) (*models.ImportJob, error) {
	if f.getJobCalls != nil {
		f.getJobCalls[id]++
		if f.getJobCalls[id] > 1 && f.getJobReloadErrs != nil {
			if err, ok := f.getJobReloadErrs[id]; ok {
				return nil, err
			}
		}
	}
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeServiceImportJobStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeServiceImportJobStore) DeleteImportJob(_ context.Context, id string) error {
	delete(f.jobs, id)
	return nil
}
func (f *fakeServiceImportJobStore) UpdateImportJob(_ context.Context, job *models.ImportJob) error {
	if existing, ok := f.jobs[job.ID]; ok {
		if job.Generation == 0 && existing.Generation > 0 {
			job.Generation = existing.Generation
		}
	}
	f.jobs[job.ID] = job
	return nil
}
func (f *fakeServiceImportJobStore) LatestImportJob(_ context.Context, _ string) (*models.ImportJob, error) {
	return nil, nil
}
func (f *fakeServiceImportJobStore) AssignImportJobGeneration(_ context.Context, jobID string, generation int64) (int64, error) {
	if j, ok := f.jobs[jobID]; ok {
		if j.Generation > 0 {
			return j.Generation, nil
		}
		j.Generation = generation
		return generation, nil
	}
	return 0, os.ErrNotExist
}

func (f *fakeServiceImportJobStore) MarkImportJobDispatchReady(_ context.Context, jobID string) error {
	if f.markReadyErrs != nil {
		if err, ok := f.markReadyErrs[jobID]; ok {
			return err
		}
	}
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

func (f *fakeServiceImportJobStore) ClaimImportJobDispatch(_ context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error) {
	if f.claimDispatchErrs != nil {
		if err, ok := f.claimDispatchErrs[jobID]; ok {
			return nil, "", err
		}
	}
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

func (f *fakeServiceImportJobStore) ConfirmImportJobDispatch(_ context.Context, jobID string, claimToken string) error {
	if f.confirmDispatchErrs != nil {
		if err, ok := f.confirmDispatchErrs[jobID]; ok {
			return err
		}
	}
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

func (f *fakeServiceImportJobStore) RecordDispatchError(_ context.Context, jobID string, claimToken string, errMsg string) error {
	j, ok := f.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	j.Error = errMsg
	j.DispatchClaimToken = ""
	j.DispatchClaimExpiresAt = nil
	return nil
}

func (f *fakeServiceImportJobStore) ListDispatchableImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts < maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeServiceImportJobStore) ClaimImportJobProcessing(_ context.Context, jobID string) (*models.ImportJob, error) {
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

func (f *fakeServiceImportJobStore) FailImportJob(_ context.Context, jobID string, reason string) error {
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

func (f *fakeServiceImportJobStore) ListExhaustedImportJobs(_ context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	var res []*models.ImportJob
	for _, j := range f.jobs {
		if j.DispatchReady && j.DispatchedAt == nil && (j.Status == models.ImportJobPending || j.Status == models.ImportJobQueued) && j.DispatchAttempts >= maxAttempts {
			res = append(res, j)
		}
	}
	return res, nil
}

func (f *fakeServiceImportJobStore) UpdateImportJobProgress(_ context.Context, jobID string, processedSongs int) error {
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

func (f *fakeServiceImportJobStore) ResetImportJobDispatch(_ context.Context, jobID string) error {
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

type fakeServiceImportQueue struct {
	enqueued []importjob.Job
}

func (f *fakeServiceImportQueue) Enqueue(_ context.Context, job importjob.Job) error {
	f.enqueued = append(f.enqueued, job)
	return nil
}

type fakeServiceManifestWriter struct {
	stored map[string][]byte
}

func (f *fakeServiceManifestWriter) Put(_ context.Context, key string, body []byte) error {
	if f.stored == nil {
		f.stored = make(map[string][]byte)
	}
	f.stored[key] = body
	return nil
}

func (f *fakeServiceManifestWriter) Presign(_ context.Context, key string, _ int64) (string, error) {
	return "https://example.com/upload/" + key, nil
}

func TestCreateImportJob_PropagatesGenerationAllocationError(t *testing.T) {
	ctx := context.Background()
	allocErr := errors.New("dynamodb allocation failed")
	sbStore := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			"sb-1": {ID: "sb-1"},
		},
		nextGenErr: allocErr,
	}
	importJobs := &fakeServiceImportJobStore{jobs: make(map[string]*models.ImportJob)}
	importQueue := &fakeServiceImportQueue{}
	manifestWriter := &fakeServiceManifestWriter{}

	svc := NewService(sbStore, nil, nil, nil, nil, nil, importJobs, importQueue, manifestWriter)

	songs := []*models.Song{{Fingerprint: "fp-1", Artist: "A", Title: "T"}}
	_, err := svc.CreateImportJob(ctx, "sb-1", songs, false)
	if err == nil {
		t.Fatal("expected error allocating catalog generation, got nil")
	}
	if !errors.Is(err, allocErr) && !strings.Contains(err.Error(), "dynamodb allocation failed") {
		t.Fatalf("expected allocation error to be propagated, got: %v", err)
	}
	if len(importJobs.jobs) != 0 {
		t.Fatalf("expected no import job to be created on allocation failure, got %d jobs", len(importJobs.jobs))
	}
}

func TestCreateImportUpload_PropagatesGenerationAllocationError(t *testing.T) {
	ctx := context.Background()
	allocErr := errors.New("dynamodb allocation failed")
	sbStore := &fakeServiceSongbookStore{
		books: map[string]*models.Songbook{
			"sb-1": {ID: "sb-1"},
		},
		nextGenErr: allocErr,
	}
	importJobs := &fakeServiceImportJobStore{jobs: make(map[string]*models.ImportJob)}
	manifestWriter := &fakeServiceManifestWriter{}

	svc := NewService(sbStore, nil, nil, nil, nil, nil, importJobs, nil, manifestWriter)

	_, _, err := svc.CreateImportUpload(ctx, "sb-1", 10, 1024, false)
	if err == nil {
		t.Fatal("expected error allocating catalog generation, got nil")
	}
	if !errors.Is(err, allocErr) && !strings.Contains(err.Error(), "dynamodb allocation failed") {
		t.Fatalf("expected allocation error to be propagated, got: %v", err)
	}
	if len(importJobs.jobs) != 0 {
		t.Fatalf("expected no import job to be created on allocation failure, got %d jobs", len(importJobs.jobs))
	}
}

func TestReconcileUndispatchedJobs_RecoversUnconfirmedJobs_BoundedByAttempts(t *testing.T) {
	ctx := context.Background()
	queue := &fakeServiceImportQueue{}
	importJobs := &fakeServiceImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-ready": {
				ID:            "job-ready",
				SongbookID:    "sb-1",
				Status:        models.ImportJobQueued,
				DispatchReady: true,
			},
			"job-exhausted": {
				ID:               "job-exhausted",
				SongbookID:       "sb-1",
				Status:           models.ImportJobQueued,
				DispatchReady:    true,
				DispatchAttempts: 5,
			},
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, 5)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled job, got %d", reconciled)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0].ID != "job-ready" {
		t.Fatalf("expected job-ready enqueued, got: %+v", queue.enqueued)
	}
}

func TestCompleteImportUpload_IdempotentOnCompletedOrProcessing(t *testing.T) {
	ctx := context.Background()
	queue := &fakeServiceImportQueue{}
	now := time.Now().UTC()
	importJobs := &fakeServiceImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-proc": {
				ID:           "job-proc",
				SongbookID:   "sb-1",
				Status:       models.ImportJobProcessing,
				DispatchedAt: &now,
			},
			"job-comp": {
				ID:          "job-comp",
				SongbookID:  "sb-1",
				Status:      models.ImportJobCompleted,
				CompletedAt: now,
			},
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

	resProc, err := svc.CompleteImportUpload(ctx, "sb-1", "job-proc")
	if err != nil {
		t.Fatalf("complete proc: %v", err)
	}
	if resProc.Status != models.ImportJobProcessing {
		t.Fatalf("expected status processing, got %s", resProc.Status)
	}

	resComp, err := svc.CompleteImportUpload(ctx, "sb-1", "job-comp")
	if err != nil {
		t.Fatalf("complete comp: %v", err)
	}
	if resComp.Status != models.ImportJobCompleted {
		t.Fatalf("expected status completed, got %s", resComp.Status)
	}

	if len(queue.enqueued) != 0 {
		t.Fatalf("expected no queueing for already active/completed jobs, got %d", len(queue.enqueued))
	}
}

func TestReconcileUndispatchedJobs_SkipsExpectedConflictsAndAggregatesUnexpectedErrors(t *testing.T) {
	ctx := context.Background()
	queue := &fakeServiceImportQueue{}
	unexpectedErr := errors.New("dynamodb network partition")

	importJobs := &fakeServiceImportJobStore{
		jobs: map[string]*models.ImportJob{
			"job-ok": {
				ID:            "job-ok",
				SongbookID:    "sb-1",
				Status:        models.ImportJobQueued,
				DispatchReady: true,
			},
			"job-conflict": {
				ID:            "job-conflict",
				SongbookID:    "sb-1",
				Status:        models.ImportJobQueued,
				DispatchReady: true,
			},
			"job-unexp": {
				ID:            "job-unexp",
				SongbookID:    "sb-1",
				Status:        models.ImportJobQueued,
				DispatchReady: true,
			},
		},
		claimDispatchErrs: map[string]error{
			"job-conflict": store.ErrImportJobClaimConflict,
			"job-unexp":    unexpectedErr,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

	reconciled, err := svc.ReconcileUndispatchedJobs(ctx, 5)
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled job, got %d", reconciled)
	}
	if err == nil {
		t.Fatal("expected aggregated unexpected error, got nil")
	}
	if !errors.Is(err, unexpectedErr) {
		t.Fatalf("expected unexpected error in aggregated result, got: %v", err)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0].ID != "job-ok" {
		t.Fatalf("expected job-ok to be enqueued, got: %+v", queue.enqueued)
	}
}

func TestCompleteImportUpload_FailedStateReloadPropagates(t *testing.T) {
	ctx := context.Background()
	simulatedErr := errors.New("dynamodb get failure")

	t.Run("ReadyRaceReloadFailure", func(t *testing.T) {
		queue := &fakeServiceImportQueue{}
		importJobs := &fakeServiceImportJobStore{
			jobs: map[string]*models.ImportJob{
				"job-ready-fail": {
					ID:            "job-ready-fail",
					SongbookID:    "sb-1",
					Status:        models.ImportJobPending,
					DispatchReady: false,
				},
			},
			markReadyErrs: map[string]error{
				"job-ready-fail": store.ErrImportJobAlreadyDispatched,
			},
			getJobReloadErrs: map[string]error{
				"job-ready-fail": simulatedErr,
			},
			getJobCalls: make(map[string]int),
		}
		svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

		res, err := svc.CompleteImportUpload(ctx, "sb-1", "job-ready-fail")
		if res != nil {
			t.Fatalf("expected nil result on reload failure, got %+v", res)
		}
		if err == nil || !errors.Is(err, simulatedErr) {
			t.Fatalf("expected reload error %v, got %v", simulatedErr, err)
		}
		if !strings.Contains(err.Error(), "reload import job after dispatch ready race") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("ClaimRaceReloadFailure", func(t *testing.T) {
		queue := &fakeServiceImportQueue{}
		importJobs := &fakeServiceImportJobStore{
			jobs: map[string]*models.ImportJob{
				"job-claim-fail": {
					ID:            "job-claim-fail",
					SongbookID:    "sb-1",
					Status:        models.ImportJobQueued,
					DispatchReady: true,
				},
			},
			claimDispatchErrs: map[string]error{
				"job-claim-fail": store.ErrImportJobClaimConflict,
			},
			getJobReloadErrs: map[string]error{
				"job-claim-fail": simulatedErr,
			},
			getJobCalls: make(map[string]int),
		}
		svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

		res, err := svc.CompleteImportUpload(ctx, "sb-1", "job-claim-fail")
		if res != nil {
			t.Fatalf("expected nil result on reload failure, got %+v", res)
		}
		if err == nil || !errors.Is(err, simulatedErr) {
			t.Fatalf("expected reload error %v, got %v", simulatedErr, err)
		}
		if !strings.Contains(err.Error(), "reload import job after dispatch claim race") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("ConfirmRaceReloadFailure", func(t *testing.T) {
		queue := &fakeServiceImportQueue{}
		importJobs := &fakeServiceImportJobStore{
			jobs: map[string]*models.ImportJob{
				"job-confirm-fail": {
					ID:            "job-confirm-fail",
					SongbookID:    "sb-1",
					Status:        models.ImportJobQueued,
					DispatchReady: true,
				},
			},
			confirmDispatchErrs: map[string]error{
				"job-confirm-fail": store.ErrImportJobStaleClaim,
			},
			getJobReloadErrs: map[string]error{
				"job-confirm-fail": simulatedErr,
			},
			getJobCalls: make(map[string]int),
		}
		svc := NewService(nil, nil, nil, nil, nil, nil, importJobs, queue, nil)

		res, err := svc.CompleteImportUpload(ctx, "sb-1", "job-confirm-fail")
		if res != nil {
			t.Fatalf("expected nil result on reload failure, got %+v", res)
		}
		if err == nil || !errors.Is(err, simulatedErr) {
			t.Fatalf("expected reload error %v, got %v", simulatedErr, err)
		}
		if !strings.Contains(err.Error(), "reload import job after dispatch confirm race") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}
