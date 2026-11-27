package account

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func (s *Service) beginAccountDeletion(ctx context.Context, user *models.User, catalogVersionsToPurge []string) ([]string, error) {
	if s.Auth == nil {
		return catalogVersionsToPurge, nil
	}
	updated, err := s.Auth.BeginAccountDeletion(ctx, user.ID, catalogVersionsToPurge)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed marking user %s for deletion: %v", user.ID, err)
		return nil, &CascadeError{Step: "begin_deletion", Message: "failed to mark account for deletion", Err: err}
	}
	if updated != nil {
		user.Deleting = updated.Deleting
		user.DeletionEpoch = updated.DeletionEpoch
		user.DeletionStartedAt = updated.DeletionStartedAt
		user.AuthEpoch = updated.AuthEpoch
		catalogVersionsToPurge = mergeCatalogVersionIDs(catalogVersionsToPurge, updated.PendingCatalogVersions)
		user.PendingCatalogVersions = catalogVersionsToPurge
	}
	return catalogVersionsToPurge, nil
}

func (s *Service) deleteAuthSessions(ctx context.Context, userID string, catalogVersionsToPurge []string) error {
	if s.Auth == nil {
		return nil
	}
	if err := s.Auth.DeleteAuthSessionsForUser(ctx, userID); err != nil {
		log.Printf("[PRIVACY ERROR] Failed deleting sessions for user %s: %v", userID, err)
		return s.failCascade(ctx, userID, "auth_sessions", "failed to delete sessions", err, catalogVersionsToPurge)
	}
	return nil
}

func (s *Service) deleteUserPasskeys(ctx context.Context, userID string, catalogVersionsToPurge []string) error {
	if s.Passkeys == nil {
		return nil
	}
	creds, err := s.Passkeys.ListPasskeyCredentialsForUser(ctx, userID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing passkeys for user %s: %v", userID, err)
		return s.failCascade(ctx, userID, "list_passkeys", "failed to list passkeys", err, catalogVersionsToPurge)
	}
	for _, cred := range creds {
		if err := s.Passkeys.DeletePasskeyCredential(ctx, cred.ID, userID); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed deleting passkey %s for user %s: %v", cred.ID, userID, err)
			return s.failCascade(ctx, userID, "delete_passkeys", "failed to delete passkey credentials", err, catalogVersionsToPurge)
		}
	}
	return nil
}

func (s *Service) deleteUserDeveloperKeys(ctx context.Context, userID string, catalogVersionsToPurge []string) error {
	if s.DeveloperKeys == nil {
		return nil
	}
	keys, err := s.DeveloperKeys.ListDeveloperKeys(ctx, userID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing developer keys for user %s: %v", userID, err)
		return s.failCascade(ctx, userID, "list_developer_keys", "failed to list developer keys", err, catalogVersionsToPurge)
	}
	for _, key := range keys {
		if err := s.DeveloperKeys.RevokeDeveloperKey(ctx, key.ID, userID); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed revoking developer key %s for user %s: %v", key.ID, userID, err)
			return s.failCascade(ctx, userID, "delete_developer_keys", "failed to delete developer keys", err, catalogVersionsToPurge)
		}
	}
	return nil
}

func (s *Service) deleteUserEvents(ctx context.Context, userID string, catalogVersionsToPurge []string) error {
	if s.Events == nil {
		return nil
	}
	events, err := s.Events.ListEventsByOwner(ctx, userID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing events for user %s: %v", userID, err)
		return s.failCascade(ctx, userID, "list_events", "failed to list events", err, catalogVersionsToPurge)
	}
	for _, ev := range events {
		if err := s.deleteSingleEvent(ctx, userID, ev.ID, catalogVersionsToPurge); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteSingleEvent(ctx context.Context, userID, eventID string, catalogVersionsToPurge []string) error {
	if err := s.Events.BeginEventDeletion(ctx, eventID); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on event %s: %v", eventID, err)
		return s.failCascade(ctx, userID, "begin_event_deletion", "failed to begin event deletion", err, catalogVersionsToPurge)
	}
	if err := s.deleteEventRequests(ctx, userID, eventID, catalogVersionsToPurge); err != nil {
		return err
	}
	if err := s.Events.DeleteEvent(ctx, eventID); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed deleting event %s: %v", eventID, err)
		return s.failCascade(ctx, userID, "delete_events", "failed to delete events", err, catalogVersionsToPurge)
	}
	return nil
}

func (s *Service) deleteEventRequests(ctx context.Context, userID, eventID string, catalogVersionsToPurge []string) error {
	if s.Requests == nil {
		return nil
	}
	reqs, err := s.Requests.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing requests for event %s: %v", eventID, err)
		return s.failCascade(ctx, userID, "list_requests", "failed to list event requests", err, catalogVersionsToPurge)
	}
	for _, rq := range reqs {
		if err := s.Requests.DeleteRequest(ctx, rq.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed deleting request %s: %v", rq.ID, err)
			return s.failCascade(ctx, userID, "delete_requests", "failed to delete event requests", err, catalogVersionsToPurge)
		}
	}
	return nil
}

func (s *Service) deleteUserSongbooks(ctx context.Context, user *models.User, catalogVersionsToPurge []string) ([]string, error) {
	if s.Songbooks == nil {
		return catalogVersionsToPurge, nil
	}
	books, err := s.Songbooks.ListSongbooksByOwner(ctx, user.ID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing songbooks for user %s: %v", user.ID, err)
		return nil, s.failCascade(ctx, user.ID, "list_songbooks", "failed to list songbooks", err, catalogVersionsToPurge)
	}
	for _, book := range books {
		catalogVersionsToPurge, err = s.deleteSingleSongbook(ctx, user, book.ID, catalogVersionsToPurge)
		if err != nil {
			return nil, err
		}
	}
	return catalogVersionsToPurge, nil
}

func (s *Service) deleteSingleSongbook(ctx context.Context, user *models.User, bookID string, catalogVersionsToPurge []string) ([]string, error) {
	frozenBook, err := s.Songbooks.BeginSongbookDeletion(ctx, bookID)
	if errors.Is(err, os.ErrNotExist) {
		return catalogVersionsToPurge, nil
	}
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on songbook %s: %v", bookID, err)
		return nil, s.failCascade(ctx, user.ID, "begin_songbook_deletion", "failed to begin songbook deletion", err, catalogVersionsToPurge)
	}
	if frozenBook == nil {
		err := fmt.Errorf("songbook %s deletion barrier returned nil frozen state", bookID)
		log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on songbook %s: %v", bookID, err)
		return nil, s.failCascade(ctx, user.ID, "begin_songbook_deletion", "failed to begin songbook deletion", err, catalogVersionsToPurge)
	}
	if err := s.cleanupOwnedSongbookCollaboration(ctx, frozenBook); err != nil {
		return nil, s.failCascade(ctx, user.ID, "cleanup_songbook_collaboration", "failed to clean up songbook collaboration", err, catalogVersionsToPurge)
	}

	catalogVersionsToPurge, err = s.recordFrozenBookCatalogVersion(ctx, user, frozenBook.CatalogVersionID, catalogVersionsToPurge)
	if err != nil {
		return nil, err
	}
	if err := s.deleteSongbookCatalogChanges(ctx, user.ID, bookID, catalogVersionsToPurge); err != nil {
		return nil, err
	}
	if err := s.deleteSongbookImportJobs(ctx, user.ID, bookID, catalogVersionsToPurge); err != nil {
		return nil, err
	}
	if err := s.Songbooks.DeleteSongbook(ctx, bookID); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed deleting songbook %s: %v", bookID, err)
		return nil, s.failCascade(ctx, user.ID, "delete_songbooks", "failed to delete songbooks", err, catalogVersionsToPurge)
	}
	return catalogVersionsToPurge, nil
}

func (s *Service) recordFrozenBookCatalogVersion(ctx context.Context, user *models.User, catalogVersionID string, catalogVersionsToPurge []string) ([]string, error) {
	if catalogVersionID == "" {
		return catalogVersionsToPurge, nil
	}
	newVersions := mergeCatalogVersionIDs(catalogVersionsToPurge, []string{catalogVersionID})
	if len(newVersions) <= len(catalogVersionsToPurge) {
		return catalogVersionsToPurge, nil
	}
	catalogVersionsToPurge = newVersions
	user.PendingCatalogVersions = catalogVersionsToPurge
	if s.Auth == nil {
		return catalogVersionsToPurge, nil
	}
	updated, err := s.Auth.BeginAccountDeletion(ctx, user.ID, catalogVersionsToPurge)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed recording pending catalog versions for user %s: %v", user.ID, err)
		return nil, s.failCascade(ctx, user.ID, "record_catalog_versions", "failed to record pending catalog versions", err, catalogVersionsToPurge)
	}
	if updated != nil {
		catalogVersionsToPurge = mergeCatalogVersionIDs(catalogVersionsToPurge, updated.PendingCatalogVersions)
		user.PendingCatalogVersions = catalogVersionsToPurge
	}
	return catalogVersionsToPurge, nil
}

func (s *Service) deleteSongbookCatalogChanges(ctx context.Context, userID, bookID string, catalogVersionsToPurge []string) error {
	if s.CatalogChanges == nil {
		return nil
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, bookID)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing catalog changes for songbook %s: %v", bookID, err)
		return s.failCascade(ctx, userID, "list_catalog_changes", "failed to list catalog changes", err, catalogVersionsToPurge)
	}
	for _, change := range changes {
		if err := s.CatalogChanges.DeleteCatalogChange(ctx, bookID, change.SongID, time.Time{}); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed deleting catalog change for songbook %s song %s: %v", bookID, change.SongID, err)
			return s.failCascade(ctx, userID, "delete_catalog_changes", "failed to delete catalog changes", err, catalogVersionsToPurge)
		}
	}
	return nil
}

func (s *Service) deleteSongbookImportJobs(ctx context.Context, userID, bookID string, catalogVersionsToPurge []string) error {
	if s.ImportJobs == nil {
		return nil
	}
	jobs, err := s.ImportJobs.ListImportJobs(ctx)
	if err != nil {
		log.Printf("[PRIVACY ERROR] Failed listing import jobs: %v", err)
		return s.failCascade(ctx, userID, "list_import_jobs", "failed to list import jobs", err, catalogVersionsToPurge)
	}
	for _, job := range jobs {
		if job.SongbookID != bookID {
			continue
		}
		if err := s.deleteSingleImportJob(ctx, userID, job, catalogVersionsToPurge); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteSingleImportJob(ctx context.Context, userID string, job *models.ImportJob, catalogVersionsToPurge []string) error {
	if job.ManifestKey != "" && s.Assets != nil {
		if err := s.Assets.DeleteKey(ctx, job.ManifestKey); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed deleting import manifest key %s: %v", job.ManifestKey, err)
			return s.failCascade(ctx, userID, "delete_import_assets", "failed to delete import manifest assets", err, catalogVersionsToPurge)
		}
	}
	if err := s.ImportJobs.DeleteImportJob(ctx, job.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed deleting import job %s: %v", job.ID, err)
		return s.failCascade(ctx, userID, "delete_import_jobs", "failed to delete import jobs", err, catalogVersionsToPurge)
	}
	return nil
}

func (s *Service) purgeUserCatalogVersions(ctx context.Context, user *models.User, catalogVersionsToPurge []string) error {
	if s.Catalogs == nil || len(catalogVersionsToPurge) == 0 {
		return nil
	}
	for _, versionID := range catalogVersionsToPurge {
		if err := s.purgeAndRecordCatalogVersion(ctx, user.ID, versionID, catalogVersionsToPurge); err != nil {
			return err
		}
	}
	user.PendingCatalogVersions = nil
	return nil
}

func (s *Service) purgeAndRecordCatalogVersion(ctx context.Context, userID, versionID string, catalogVersionsToPurge []string) error {
	err := s.purgeCatalogVersionAssets(ctx, versionID)
	if errors.Is(err, store.ErrCatalogVersionInUse) {
		log.Printf("[PRIVACY] Catalog version %s is shared with other active songbooks; preserving shared assets", versionID)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[PRIVACY ERROR] Failed purging catalog version %s: %v", versionID, err)
		return s.failCascade(ctx, userID, "purge_catalog_version", "failed to purge catalog version", err, nil)
	}
	if s.Auth != nil {
		if progErr := s.Auth.RecordAccountDeletionProgress(ctx, userID, "purge_catalog_version", "", []string{versionID}); progErr != nil && !errors.Is(progErr, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed recording deletion progress for user %s: %v", userID, progErr)
			return s.failCascade(ctx, userID, "record_progress", "failed to record account deletion progress", progErr, catalogVersionsToPurge)
		}
	}
	return nil
}

func (s *Service) purgeCatalogVersionAssets(ctx context.Context, versionID string) error {
	return s.Catalogs.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
		if prefix != "" && s.Assets != nil {
			return s.Assets.DeletePrefix(ctx, prefix)
		}
		return nil
	}, store.PurgeCatalogOptions{
		LeaseDuration:       s.CatalogLeaseDuration,
		ReleaseLeaseOnError: true,
	})
}

func (s *Service) finalizeUserDeletion(ctx context.Context, userID string) error {
	if s.Auth == nil {
		return nil
	}
	var lastFinalizeErr error
	for finalizeAttempt := 0; finalizeAttempt < 4; finalizeAttempt++ {
		err := s.Auth.DeleteUser(ctx, userID)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		lastFinalizeErr = err
		if retryErr := s.purgePendingCatalogVersionsOnRetry(ctx, userID, err); retryErr != nil {
			return retryErr
		}
	}
	log.Printf("[PRIVACY ERROR] Failed deleting user record %s after finalization retry exhaustion: %v", userID, lastFinalizeErr)
	return s.failCascade(ctx, userID, "delete_user", "failed to delete account after finalization retry exhaustion", lastFinalizeErr, nil)
}

func (s *Service) purgePendingCatalogVersionsOnRetry(ctx context.Context, userID string, deleteErr error) error {
	current, getErr := s.Auth.GetUser(ctx, userID)
	if getErr != nil || current == nil || len(current.PendingCatalogVersions) == 0 {
		log.Printf("[PRIVACY ERROR] Failed deleting user record %s: %v", userID, deleteErr)
		return s.failCascade(ctx, userID, "delete_user", "failed to delete account", deleteErr, nil)
	}
	for _, versionID := range current.PendingCatalogVersions {
		if err := s.purgePendingVersionDuringFinalize(ctx, userID, versionID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) purgePendingVersionDuringFinalize(ctx context.Context, userID, versionID string) error {
	if s.Catalogs != nil {
		pErr := s.purgeCatalogVersionAssets(ctx, versionID)
		if pErr != nil && !errors.Is(pErr, os.ErrNotExist) && !errors.Is(pErr, store.ErrCatalogVersionInUse) {
			return s.failCascade(ctx, userID, "purge_catalog_version", "failed to purge catalog version", pErr, nil)
		}
	}
	if progErr := s.Auth.RecordAccountDeletionProgress(ctx, userID, "purge_catalog_version", "", []string{versionID}); progErr != nil && !errors.Is(progErr, os.ErrNotExist) {
		return s.failCascade(ctx, userID, "record_progress", "failed to record account deletion progress", progErr, nil)
	}
	return nil
}
