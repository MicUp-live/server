package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func (s *Service) loadStaticSongsForList(ctx context.Context, songbookID, query string) ([]*models.Song, error) {
	if s.CatalogReader == nil || s.Catalogs == nil || s.Songbooks == nil {
		return nil, nil
	}
	book, bookErr := s.Songbooks.GetSongbook(ctx, songbookID)
	if bookErr != nil && !errors.Is(bookErr, os.ErrNotExist) {
		return nil, bookErr
	}
	if bookErr != nil || book.CatalogVersionID == "" {
		return nil, nil
	}
	version, versionErr := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
	if versionErr != nil && !errors.Is(versionErr, os.ErrNotExist) {
		return nil, versionErr
	}
	if versionErr != nil || version.Status != models.CatalogVersionReady {
		return nil, nil
	}
	songs, readErr := s.CatalogReader.ListSongs(ctx, version.AssetPrefix, songbookID)
	if readErr != nil {
		return nil, readErr
	}
	return filterSongsByQuery(songs, query), nil
}

func filterSongsByQuery(songs []*models.Song, query string) []*models.Song {
	if query == "" {
		return songs
	}
	needle := strings.ToLower(query)
	filtered := songs[:0]
	for _, song := range songs {
		if songMatchesQuery(song, needle) {
			filtered = append(filtered, song)
		}
	}
	return filtered
}

func songMatchesQuery(song *models.Song, lowerNeedle string) bool {
	return strings.Contains(strings.ToLower(models.BuildSearchText(song.Artist, song.Title, song.Disc, song.Label, song.Source)), lowerNeedle)
}

func (s *Service) applyAndFilterCatalogChanges(ctx context.Context, songbookID, query string, songs []*models.Song) ([]*models.Song, error) {
	if s.CatalogChanges == nil {
		return songs, nil
	}
	changes, changeErr := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
	if changeErr != nil {
		return nil, changeErr
	}
	byID := models.ApplyCatalogChanges(songs, changes)
	songs = songs[:0]
	needle := strings.ToLower(query)
	for _, song := range byID {
		if needle != "" && !songMatchesQuery(song, needle) {
			continue
		}
		songs = append(songs, song)
	}
	sort.SliceStable(songs, func(i, j int) bool {
		left := strings.ToLower(songs[i].Artist + "\x00" + songs[i].Title)
		right := strings.ToLower(songs[j].Artist + "\x00" + songs[j].Title)
		return left < right
	})
	return songs, nil
}

func sanitizeAndPaginateSongs(songs []*models.Song, offset, pageSize int) []*models.Song {
	for _, sng := range songs {
		sng.Data = nil
		sng.SearchText = ""
		sng.Fingerprint = ""
		sng.Location = ""
		sng.SongbookID = ""
		sng.CreatedAt = time.Time{}
		sng.UpdatedAt = time.Time{}
	}
	if offset >= len(songs) {
		return nil
	}
	end := offset + pageSize
	if end > len(songs) {
		end = len(songs)
	}
	return songs[offset:end]
}

func (s *Service) resolveSongbookViaEvent(ctx context.Context, identifier, eventRef string) *models.Songbook {
	if eventRef == "" || s.Events == nil {
		return nil
	}
	trimmed := strings.TrimSpace(eventRef)
	ev, evErr := s.Events.GetEventByJoinCode(ctx, strings.ToUpper(trimmed))
	if evErr != nil || ev == nil {
		ev, evErr = s.Events.GetEvent(ctx, trimmed)
	}
	if evErr != nil || ev == nil {
		return nil
	}
	if ev.Status != models.EventStatusActive && ev.Status != models.EventStatusPaused {
		return nil
	}
	linkedBook, bookErr := s.Songbooks.GetSongbook(ctx, ev.SongbookID)
	if bookErr != nil || linkedBook == nil {
		return nil
	}
	if matchesSongbookIdentifier(identifier, linkedBook) {
		return linkedBook
	}
	return nil
}

func matchesSongbookIdentifier(identifier string, book *models.Songbook) bool {
	if identifier == book.ID || identifier == book.PublicID {
		return true
	}
	normSlug := models.NormalizeSlug(book.Slug)
	return normSlug != "" && models.NormalizeSlug(identifier) == normSlug
}

func (s *Service) ensureImportJobDispatchReady(ctx context.Context, job *models.ImportJob) (*models.ImportJob, bool, error) {
	if job.DispatchReady {
		return job, false, nil
	}
	err := s.ImportJobs.MarkImportJobDispatchReady(ctx, job.ID)
	if err == nil {
		job.DispatchReady = true
		job.Status = models.ImportJobQueued
		return job, false, nil
	}
	if errors.Is(err, store.ErrImportJobAlreadyDispatched) || errors.Is(err, store.ErrImportJobAlreadyCompleted) {
		latest, getErr := s.ImportJobs.GetImportJob(ctx, job.ID)
		if getErr != nil {
			return nil, false, fmt.Errorf("reload import job after dispatch ready race: %w", getErr)
		}
		return latest, true, nil
	}
	if errors.Is(err, store.ErrImportJobTerminal) {
		return nil, false, ErrImportJobClosed
	}
	return nil, false, fmt.Errorf("mark import job dispatch ready: %w", err)
}

func (s *Service) claimImportJobForDispatch(ctx context.Context, job *models.ImportJob) (*models.ImportJob, string, bool, error) {
	claimedJob, claimToken, claimErr := s.ImportJobs.ClaimImportJobDispatch(ctx, job.ID, DefaultDispatchLeaseDuration)
	if claimErr == nil {
		if claimedJob != nil {
			job = claimedJob
		}
		return job, claimToken, false, nil
	}
	if errors.Is(claimErr, store.ErrImportJobAlreadyDispatched) || errors.Is(claimErr, store.ErrImportJobAlreadyCompleted) || errors.Is(claimErr, store.ErrImportJobClaimConflict) {
		latest, getErr := s.ImportJobs.GetImportJob(ctx, job.ID)
		if getErr != nil {
			return nil, "", false, fmt.Errorf("reload import job after dispatch claim race: %w", getErr)
		}
		return latest, "", true, nil
	}
	if errors.Is(claimErr, store.ErrImportJobTerminal) {
		return nil, "", false, ErrImportJobClosed
	}
	return nil, "", false, fmt.Errorf("claim import job dispatch: %w", claimErr)
}

func (s *Service) enqueueAndConfirmImportJob(ctx context.Context, job *models.ImportJob, claimToken string) (*models.ImportJob, error) {
	if err := s.ImportQueue.Enqueue(ctx, importjob.Job{
		ID:           job.ID,
		ManifestKey:  job.ManifestKey,
		PruneMissing: job.PruneMissing,
		Generation:   job.Generation,
	}); err != nil {
		_ = s.ImportJobs.RecordDispatchError(ctx, job.ID, claimToken, err.Error())
		return nil, fmt.Errorf("schedule import job: %w", err)
	}

	if err := s.ImportJobs.ConfirmImportJobDispatch(ctx, job.ID, claimToken); err == nil {
		confNow := time.Now().UTC()
		job.DispatchedAt = &confNow
		job.Status = models.ImportJobQueued
		job.DispatchClaimToken = ""
		job.DispatchClaimExpiresAt = nil
		return job, nil
	}
	latest, getErr := s.ImportJobs.GetImportJob(ctx, job.ID)
	if getErr != nil {
		return nil, fmt.Errorf("reload import job after dispatch confirm race: %w", getErr)
	}
	return latest, nil
}

func parseSingleFile(f ParseFileItem, pattern string) ParseFilesResItem {
	parts, disc := splitFilenameParts(f.Name, pattern)
	artist, title, disc := assignPartsByPattern(parts, pattern, disc)

	title = cleanupRegex.ReplaceAllString(title, "")
	title = strings.Join(strings.Fields(title), " ")

	confidence := "low"
	if artist != "" && title != "" {
		confidence = "high"
	}

	loc := f.Path
	if loc == "" {
		loc = f.Name
	}

	return ParseFilesResItem{
		Artist:      artist,
		Title:       title,
		Disc:        disc,
		Location:    loc,
		Fingerprint: fmt.Sprintf("%s-%d", f.Name, f.Size),
		Confidence:  confidence,
	}
}

func splitFilenameParts(name, pattern string) ([]string, string) {
	stem := name
	if idx := strings.LastIndex(stem, "."); idx != -1 {
		stem = stem[:idx]
	}
	var disc string
	if pattern != "disc-artist-title" {
		disc, stem = extractDiscCodeFromRawStem(stem)
	}
	partsRaw := importRegex.Split(stem, -1)
	if len(partsRaw) == 1 {
		partsRaw = strings.Split(stem, "_")
	}
	parts := make([]string, len(partsRaw))
	for i, p := range partsRaw {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(p, "_", " "))
	}
	return parts, disc
}

func partAt(parts []string, idx int) string {
	if idx < len(parts) {
		return parts[idx]
	}
	return ""
}

func assignPartsByPattern(parts []string, pattern, disc string) (artist, title, outDisc string) {
	outDisc = disc
	switch pattern {
	case "artist-title":
		artist = partAt(parts, 0)
		if len(parts) > 1 {
			title = strings.Join(parts[1:], " - ")
		}
	case "title-artist":
		if len(parts) > 1 {
			title = strings.Join(parts[:len(parts)-1], " - ")
			artist = parts[len(parts)-1]
		} else if len(parts) == 1 {
			title = parts[0]
		}
	case "disc-artist-title":
		outDisc = partAt(parts, 0)
		artist = partAt(parts, 1)
		if len(parts) > 2 {
			title = strings.Join(parts[2:], " - ")
		}
	}
	return artist, title, outDisc
}
