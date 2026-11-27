// Package catalog provides application services for catalog queries, overrides, imports, and publication.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// DefaultDispatchLeaseDuration is the lease window for an import job dispatch attempt.
const DefaultDispatchLeaseDuration = 5 * time.Minute

var (
	ErrSongbookNotFound     = errors.New("songbook not found")
	ErrSongNotFound         = errors.New("song not found")
	ErrImportJobClosed      = errors.New("import job is already closed")
	ErrServiceUnavailable   = errors.New("service unavailable")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrMissingFingerprint   = errors.New("every song requires a fingerprint")
	ErrInvalidUploadRequest = errors.New("total_songs and size are required")
)

// CatalogSongReader reads catalog tracks from compiled static storage.
type CatalogSongReader interface {
	ListSongs(ctx context.Context, assetPrefix, songbookID string) ([]*models.Song, error)
}

// ImportQueue enqueues durable import execution jobs.
type ImportQueue interface {
	Enqueue(context.Context, importjob.Job) error
}

// ManifestWriter stores or presigns manifest assets in object storage.
type ManifestWriter interface {
	Put(context.Context, string, []byte) error
	Presign(context.Context, string, int64) (string, error)
}

// PublicChange is a public-safe representation of a catalog override.
type PublicChange struct {
	SongID  string       `json:"song_id"`
	Deleted bool         `json:"deleted"`
	Song    *models.Song `json:"song,omitempty"`
}

// ParseFileItem describes an unparsed raw input file.
type ParseFileItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ParseFilesReq encapsulates file parsing inputs.
type ParseFilesReq struct {
	Files   []ParseFileItem `json:"files"`
	Pattern string          `json:"pattern"`
}

// ParseFilesResItem describes parsed track metadata.
type ParseFilesResItem struct {
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Disc        string `json:"disc"`
	Location    string `json:"location"`
	Fingerprint string `json:"fingerprint"`
	Confidence  string `json:"confidence"`
}

var (
	importRegex  = regexp.MustCompile(`\s+[-–]\s+`)
	cleanupRegex = regexp.MustCompile(`\[.*?\]|\(.*?\)`)
)

// Service encapsulates catalog overrides, search, and S3-first import orchestration.
type Service struct {
	Songbooks      store.SongbookStore
	Events         store.EventStore
	CatalogJobs    JobQueue
	Catalogs       store.CatalogStore
	CatalogReader  CatalogSongReader
	CatalogChanges store.CatalogChangeStore
	ImportJobs     store.ImportJobStore
	ImportQueue    ImportQueue
	ManifestWriter ManifestWriter
	Access         store.SongbookAccessStore
}

// NewService constructs a catalog service.
func NewService(
	songbooks store.SongbookStore,
	events store.EventStore,
	catalogJobs JobQueue,
	catalogs store.CatalogStore,
	catalogReader CatalogSongReader,
	catalogChanges store.CatalogChangeStore,
	importJobs store.ImportJobStore,
	importQueue ImportQueue,
	manifestWriter ManifestWriter,
) *Service {
	return &Service{
		Songbooks:      songbooks,
		Events:         events,
		CatalogJobs:    catalogJobs,
		Catalogs:       catalogs,
		CatalogReader:  catalogReader,
		CatalogChanges: catalogChanges,
		ImportJobs:     importJobs,
		ImportQueue:    importQueue,
		ManifestWriter: manifestWriter,
	}
}

// CreateSong adds a single song override to the pending catalog change overlay.
func (s *Service) CreateSong(ctx context.Context, songbookID string, req models.SongCreate) (*models.Song, error) {
	if s.CatalogChanges == nil {
		return nil, ErrServiceUnavailable
	}
	label := models.BuildLabel(req.Artist, req.Title)
	song := &models.Song{
		ID:          uuid.NewString(),
		SongbookID:  songbookID,
		Artist:      req.Artist,
		Title:       req.Title,
		Label:       label,
		SearchText:  models.BuildSearchText(req.Artist, req.Title, req.Disc, label, req.Location),
		Genre:       req.Genre,
		Disc:        req.Disc,
		Source:      req.Source,
		Location:    req.Location,
		VideoURL:    req.VideoURL,
		IsAvailable: true,
		Fingerprint: req.Fingerprint,
	}

	if err := s.CatalogChanges.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: songbookID, SongID: song.ID, Song: song}); err != nil {
		return nil, err
	}
	s.triggerCompaction(ctx, songbookID)
	return song, nil
}

// ListSongs retrieves tracks from the active static catalog version with pending overrides applied.
func (s *Service) ListSongs(ctx context.Context, songbookID, query string, offset int) ([]*models.Song, error) {
	const pageSize = 5000
	if offset < 0 {
		offset = 0
	}
	songs, err := s.loadStaticSongsForList(ctx, songbookID, query)
	if err != nil {
		return nil, err
	}
	songs, err = s.applyAndFilterCatalogChanges(ctx, songbookID, query, songs)
	if err != nil {
		return nil, err
	}
	return sanitizeAndPaginateSongs(songs, offset, pageSize), nil
}

// UpdateSong updates a song override within a songbook.
func (s *Service) UpdateSong(ctx context.Context, songbookID, songID string, req models.SongUpdate) (*models.Song, error) {
	song, err := s.GetSong(ctx, songbookID, songID)
	if err != nil {
		return nil, err
	}
	if req.Artist != nil {
		song.Artist = *req.Artist
	}
	if req.Title != nil {
		song.Title = *req.Title
	}
	if req.Genre != nil {
		song.Genre = *req.Genre
	}
	if req.Disc != nil {
		song.Disc = *req.Disc
	}
	if req.Source != nil {
		song.Source = *req.Source
	}
	if req.Location != nil {
		song.Location = *req.Location
	}
	if req.VideoURL != nil {
		song.VideoURL = *req.VideoURL
	}
	if req.Fingerprint != nil {
		song.Fingerprint = *req.Fingerprint
	}
	if req.IsAvailable != nil {
		song.IsAvailable = *req.IsAvailable
	}
	song.Label = models.BuildLabel(song.Artist, song.Title)
	song.SearchText = models.BuildSearchText(song.Artist, song.Title, song.Disc, song.Label, song.Location)

	if s.CatalogChanges == nil {
		return nil, ErrServiceUnavailable
	}
	if err = s.CatalogChanges.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: song.SongbookID, SongID: song.ID, Song: song}); err != nil {
		return nil, err
	}
	return song, nil
}

// DeleteSong marks a song as deleted in the catalog change overlay.
func (s *Service) DeleteSong(ctx context.Context, songbookID, songID string) error {
	if s.CatalogChanges == nil {
		return ErrServiceUnavailable
	}
	song, err := s.GetSong(ctx, songbookID, songID)
	if err != nil {
		return err
	}
	return s.CatalogChanges.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: song.SongbookID, SongID: song.ID, Deleted: true})
}

// BatchDeleteSongs deletes multiple songs from a songbook, loading pending
// overrides and the active static catalog at most once per batch.
func (s *Service) BatchDeleteSongs(ctx context.Context, songbookID string, songIDs []string) (int, error) {
	if s.CatalogChanges == nil {
		return 0, ErrServiceUnavailable
	}
	songs, err := s.resolveBatchCatalogSongs(ctx, songbookID, songIDs)
	if err != nil {
		return 0, err
	}

	deletedCount := 0
	for _, song := range songs {
		delErr := s.CatalogChanges.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: songbookID, SongID: song.ID, Deleted: true})
		if errors.Is(delErr, store.ErrPermissionDenied) || errors.Is(delErr, store.ErrSongbookDeleting) {
			return deletedCount, delErr
		}
		if delErr == nil {
			deletedCount++
		}
	}
	return deletedCount, nil
}

// ClearSongs clears all songs from a songbook via a pending clear marker.
func (s *Service) ClearSongs(ctx context.Context, songbookID string) error {
	if s.CatalogChanges == nil {
		return ErrServiceUnavailable
	}
	return s.CatalogChanges.PutCatalogChange(ctx, &models.CatalogChange{SongbookID: songbookID, SongID: "__clear__", Clear: true})
}

func (s *Service) resolvePublicSongbook(ctx context.Context, identifier, eventRef string) (*models.Songbook, error) {
	if s.Songbooks == nil {
		return nil, ErrSongbookNotFound
	}

	// 1. If identifier resolves to an explicitly published public songbook, allow immediately.
	sb, err := s.Songbooks.GetPublicSongbook(ctx, identifier)
	if err == nil && sb != nil && sb.IsPublic {
		return sb, nil
	}

	// 2. If a valid eventRef was provided (join code or event ID), check if it grants temporary public access.
	if linkedBook := s.resolveSongbookViaEvent(ctx, identifier, eventRef); linkedBook != nil {
		return linkedBook, nil
	}

	return nil, ErrSongbookNotFound
}

// GetPublicManifest returns metadata about the static immutable public catalog.
// If eventRef is provided (event join code or ID), a private songbook linked to that active event is permitted.
func (s *Service) GetPublicManifest(ctx context.Context, publicID, eventRef string) (map[string]any, error) {
	songbook, err := s.resolvePublicSongbook(ctx, publicID, eventRef)
	if err != nil {
		return nil, ErrSongbookNotFound
	}
	return s.resolvePublicManifest(ctx, songbook)
}

// GetPublicOverrides returns sanitized pending catalog modifications for public views.
// If eventRef is provided (event join code or ID), a private songbook linked to that active event is permitted.
func (s *Service) GetPublicOverrides(ctx context.Context, publicID, eventRef string) ([]PublicChange, error) {
	if s.CatalogChanges == nil {
		return nil, ErrSongbookNotFound
	}
	book, err := s.resolvePublicSongbook(ctx, publicID, eventRef)
	if err != nil {
		return nil, ErrSongbookNotFound
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, book.ID)
	if err != nil {
		return nil, err
	}
	result := make([]PublicChange, 0, len(changes))
	for _, change := range changes {
		if change.Clear {
			result = append(result, PublicChange{SongID: "__clear__", Deleted: true})
			continue
		}
		if change.Song != nil {
			cp := *change.Song
			cp.SongbookID = ""
			cp.Location = ""
			cp.Fingerprint = ""
			cp.SearchText = ""
			cp.Data = nil
			result = append(result, PublicChange{SongID: change.SongID, Deleted: change.Deleted, Song: &cp})
		} else {
			result = append(result, PublicChange{SongID: change.SongID, Deleted: change.Deleted})
		}
	}
	return result, nil
}

// GetCatalogOverrides returns all changes staged for an owned songbook.
func (s *Service) GetCatalogOverrides(ctx context.Context, songbookID string) ([]*models.CatalogChange, error) {
	if s.CatalogChanges == nil {
		return nil, ErrServiceUnavailable
	}
	return s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
}

// CreateImportJob initiates an asynchronous S3-backed import job.
func (s *Service) CreateImportJob(ctx context.Context, songbookID string, songs []*models.Song, pruneMissing bool) (*models.ImportJob, error) {
	for _, song := range songs {
		if song == nil || strings.TrimSpace(song.Fingerprint) == "" {
			return nil, ErrMissingFingerprint
		}
	}

	if s.ImportJobs == nil || s.ImportQueue == nil || s.ManifestWriter == nil {
		return nil, ErrServiceUnavailable
	}

	jobID := uuid.NewString()
	for _, sng := range songs {
		sng.SongbookID = songbookID
		sng.IsAvailable = true
	}
	body, err := json.Marshal(struct {
		Songs        []*models.Song `json:"songs"`
		PruneMissing bool           `json:"prune_missing"`
	}{songs, pruneMissing})
	if err != nil {
		return nil, fmt.Errorf("encode import manifest: %w", err)
	}

	key := "imports/" + jobID + ".json"
	if err := s.ManifestWriter.Put(ctx, key, body); err != nil {
		return nil, fmt.Errorf("store import manifest: %w", err)
	}

	now := time.Now().UTC()
	var generation int64
	if s.Songbooks != nil {
		gen, err := s.Songbooks.NextCatalogGeneration(ctx, songbookID)
		if err != nil {
			return nil, fmt.Errorf("allocate catalog generation: %w", err)
		}
		generation = gen
	}

	job := &models.ImportJob{
		ID:            jobID,
		SongbookID:    songbookID,
		ManifestKey:   key,
		Status:        models.ImportJobQueued,
		DispatchReady: true,
		TotalSongs:    len(songs),
		PruneMissing:  pruneMissing,
		Generation:    generation,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.ImportJobs.CreateImportJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create import job: %w", err)
	}

	_, claimToken, claimErr := s.ImportJobs.ClaimImportJobDispatch(ctx, jobID, DefaultDispatchLeaseDuration)
	if claimErr != nil {
		// Job remains dispatch_ready=true with no active claim or conflict, recoverable by background reconciliation.
		return job, nil
	}

	if err := s.ImportQueue.Enqueue(ctx, importjob.Job{ID: jobID, ManifestKey: key, PruneMissing: pruneMissing, Generation: generation}); err != nil {
		// Preserve manifest in S3 and release dispatch claim so reconciliation or retry can recover.
		_ = s.ImportJobs.RecordDispatchError(ctx, jobID, claimToken, err.Error())
		return nil, fmt.Errorf("schedule import job: %w", err)
	}
	if err := s.ImportJobs.ConfirmImportJobDispatch(ctx, jobID, claimToken); err == nil {
		confNow := time.Now().UTC()
		job.DispatchedAt = &confNow
		job.Status = models.ImportJobQueued
		job.DispatchClaimToken = ""
		job.DispatchClaimExpiresAt = nil
	}
	return job, nil
}

// GetImportJob returns import job progress.
func (s *Service) GetImportJob(ctx context.Context, songbookID, jobID string) (*models.ImportJob, error) {
	if s.ImportJobs == nil {
		return nil, ErrServiceUnavailable
	}
	job, err := s.ImportJobs.GetImportJob(ctx, jobID)
	if err != nil || job.SongbookID != songbookID {
		return nil, os.ErrNotExist
	}
	return job, nil
}

// GetLatestImportJob returns the newest import job for a songbook.
func (s *Service) GetLatestImportJob(ctx context.Context, songbookID string) (*models.ImportJob, error) {
	if s.ImportJobs == nil {
		return nil, ErrServiceUnavailable
	}
	return s.ImportJobs.LatestImportJob(ctx, songbookID)
}

// CreateImportUpload creates a presigned upload manifest URL.
// Presigned uploads remain pending and not dispatch-ready until CompleteImportUpload is accepted.
func (s *Service) CreateImportUpload(ctx context.Context, songbookID string, totalSongs int, size int64, pruneMissing bool) (*models.ImportJob, string, error) {
	if s.ImportJobs == nil || s.ManifestWriter == nil {
		return nil, "", ErrServiceUnavailable
	}
	if totalSongs < 1 || size < 1 {
		return nil, "", ErrInvalidUploadRequest
	}
	jobID := uuid.NewString()
	now := time.Now().UTC()
	var generation int64
	if s.Songbooks != nil {
		gen, err := s.Songbooks.NextCatalogGeneration(ctx, songbookID)
		if err != nil {
			return nil, "", fmt.Errorf("allocate catalog generation: %w", err)
		}
		generation = gen
	}

	key := "imports/" + jobID + ".json"
	job := &models.ImportJob{
		ID:            jobID,
		SongbookID:    songbookID,
		ManifestKey:   key,
		Status:        models.ImportJobPending,
		DispatchReady: false,
		TotalSongs:    totalSongs,
		PruneMissing:  pruneMissing,
		Generation:    generation,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.ImportJobs.CreateImportJob(ctx, job); err != nil {
		return nil, "", fmt.Errorf("create import job: %w", err)
	}
	url, err := s.ManifestWriter.Presign(ctx, key, size)
	if err != nil {
		return nil, "", fmt.Errorf("create upload URL: %w", err)
	}
	return job, url, nil
}

// CompleteImportUpload verifies that an uploaded manifest is ready and queues it for background worker processing.
func (s *Service) CompleteImportUpload(ctx context.Context, songbookID, jobID string) (*models.ImportJob, error) {
	if s.ImportJobs == nil || s.ImportQueue == nil {
		return nil, ErrServiceUnavailable
	}
	job, err := s.ImportJobs.GetImportJob(ctx, jobID)
	if err != nil || job.SongbookID != songbookID {
		return nil, os.ErrNotExist
	}
	if job.Status == models.ImportJobCompleted || job.Status == models.ImportJobProcessing || job.DispatchedAt != nil {
		return job, nil
	}
	if job.Status == models.ImportJobFailed {
		return nil, ErrImportJobClosed
	}

	job, done, err := s.ensureImportJobDispatchReady(ctx, job)
	if err != nil || done {
		return job, err
	}

	job, claimToken, done, err := s.claimImportJobForDispatch(ctx, job)
	if err != nil || done {
		return job, err
	}

	return s.enqueueAndConfirmImportJob(ctx, job, claimToken)
}

// ReconcileUndispatchedJobs discovers dispatch-ready unconfirmed import jobs with no claim or an expired claim,
// and enqueues them for worker execution with bounded attempts.
// Expected claim conflicts and already-dispatched jobs are skipped; unexpected storage failures are aggregated and returned.
func (s *Service) ReconcileUndispatchedJobs(ctx context.Context, maxAttempts int) (int, error) {
	if s.ImportJobs == nil || s.ImportQueue == nil {
		return 0, ErrServiceUnavailable
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	candidates, err := s.ImportJobs.ListDispatchableImportJobs(ctx, maxAttempts)
	if err != nil {
		return 0, fmt.Errorf("list dispatchable import jobs: %w", err)
	}
	var errs []error
	reconciled := 0
	for _, cand := range candidates {
		claimedJob, claimToken, err := s.ImportJobs.ClaimImportJobDispatch(ctx, cand.ID, DefaultDispatchLeaseDuration)
		if err != nil {
			if errors.Is(err, store.ErrImportJobClaimConflict) ||
				errors.Is(err, store.ErrImportJobAlreadyDispatched) ||
				errors.Is(err, store.ErrImportJobAlreadyCompleted) ||
				errors.Is(err, store.ErrImportJobTerminal) ||
				errors.Is(err, store.ErrImportJobNotReady) {
				continue
			}
			errs = append(errs, fmt.Errorf("claim job %s: %w", cand.ID, err))
			continue
		}
		target := cand
		if claimedJob != nil {
			target = claimedJob
		}
		if err := s.ImportQueue.Enqueue(ctx, importjob.Job{
			ID:           target.ID,
			ManifestKey:  target.ManifestKey,
			PruneMissing: target.PruneMissing,
			Generation:   target.Generation,
		}); err != nil {
			if recErr := s.ImportJobs.RecordDispatchError(ctx, target.ID, claimToken, err.Error()); recErr != nil {
				if !errors.Is(recErr, store.ErrImportJobStaleClaim) &&
					!errors.Is(recErr, store.ErrImportJobAlreadyCompleted) &&
					!errors.Is(recErr, store.ErrImportJobTerminal) {
					errs = append(errs, fmt.Errorf("record dispatch error for job %s: %w", target.ID, recErr))
				}
				log.Printf("reconcile: failed to record dispatch error for job %s: %v", target.ID, recErr)
			}
			continue
		}
		if confErr := s.ImportJobs.ConfirmImportJobDispatch(ctx, target.ID, claimToken); confErr != nil {
			if !errors.Is(confErr, store.ErrImportJobStaleClaim) &&
				!errors.Is(confErr, store.ErrImportJobAlreadyCompleted) &&
				!errors.Is(confErr, store.ErrImportJobTerminal) {
				errs = append(errs, fmt.Errorf("confirm dispatch for job %s: %w", target.ID, confErr))
			}
			log.Printf("reconcile: failed to confirm dispatch for job %s: %v", target.ID, confErr)
		}
		reconciled++
	}

	return reconciled, errors.Join(errs...)
}

// ListExhaustedImportJobs returns unconfirmed import jobs that have reached or exceeded maxAttempts.
func (s *Service) ListExhaustedImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error) {
	if s.ImportJobs == nil {
		return nil, ErrServiceUnavailable
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return s.ImportJobs.ListExhaustedImportJobs(ctx, maxAttempts)
}

// ResetImportJobDispatch resets dispatch attempts and clears claim state so an exhausted job can be retried.
func (s *Service) ResetImportJobDispatch(ctx context.Context, jobID string) error {
	if s.ImportJobs == nil {
		return ErrServiceUnavailable
	}
	return s.ImportJobs.ResetImportJobDispatch(ctx, jobID)
}

// ParseFiles parses filename patterns into structured metadata items.
func ParseFiles(req ParseFilesReq) []ParseFilesResItem {
	results := make([]ParseFilesResItem, 0, len(req.Files))
	for _, f := range req.Files {
		results = append(results, parseSingleFile(f, req.Pattern))
	}
	return results
}
