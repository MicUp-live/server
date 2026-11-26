// Package account provides application services for user account export and cascading deletion.
package account

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// AssetPurger deletes object storage files and prefixes for deleted accounts and catalogs.
type AssetPurger interface {
	DeleteKey(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

// CascadeError captures partial failure during account deletion.
type CascadeError struct {
	Step    string
	Message string
	Err     error
}

func (e *CascadeError) Error() string {
	return e.Message
}

func (e *CascadeError) Unwrap() error {
	return e.Err
}

// Service coordinates user data exports and cascading account deletions.
type Service struct {
	Auth          store.AuthStore
	Passkeys      store.PasskeyStore
	DeveloperKeys store.DeveloperKeyStore
	Songbooks     store.SongbookStore
	Events        store.EventStore
	Requests      store.RequestStore
	Catalogs      store.CatalogStore
	CatalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
	CatalogChanges       store.CatalogChangeStore
	ImportJobs           store.ImportJobStore
	Assets               AssetPurger
	Access               store.SongbookAccessStore
	QueueRetention       time.Duration
	CatalogLeaseDuration time.Duration
}

// NewService constructs an account service.
func NewService(
	auth store.AuthStore,
	passkeys store.PasskeyStore,
	songbooks store.SongbookStore,
	events store.EventStore,
	requests store.RequestStore,
	catalogs store.CatalogStore,
	catalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	},
	catalogChanges store.CatalogChangeStore,
	importJobs store.ImportJobStore,
	assets AssetPurger,
) *Service {
	return &Service{
		Auth: auth, Passkeys: passkeys, Songbooks: songbooks,
		Events: events, Requests: requests, Catalogs: catalogs,
		CatalogReader: catalogReader, CatalogChanges: catalogChanges, ImportJobs: importJobs,
		Assets: assets,
	}
}

// WithDeveloperKeys attaches a DeveloperKeyStore to the service.
func (s *Service) WithDeveloperKeys(devKeys store.DeveloperKeyStore) *Service {
	s.DeveloperKeys = devKeys
	return s
}

// WithCatalogLeaseDuration configures the deletion reservation lease duration.
func (s *Service) WithCatalogLeaseDuration(d time.Duration) *Service {
	s.CatalogLeaseDuration = d
	return s
}

// ExportData generates a complete snapshot of all user records.
func (s *Service) ExportData(ctx context.Context, user *models.User) (*UserDataExport, error) {
	if user == nil {
		return nil, errors.New("user is required")
	}
	if strings.TrimSpace(user.ID) == "" {
		return nil, errors.New("user id is required")
	}
	if user.Deleting {
		return nil, store.ErrAccountDeleting
	}

	export := &UserDataExport{
		ExportedAt:     time.Now().UTC(),
		Service:        "MicUp.live",
		ComplianceNote: "This document contains a complete export of your personal account data and records.",
		User:           user,
	}
	populateFavorites(ctx, export, user, s.Auth)

	// 1. Export Passkeys Metadata
	if s.Passkeys != nil {
		creds, err := s.Passkeys.ListPasskeyCredentialsForUser(ctx, user.ID)
		if err != nil {
			return nil, &ExportError{
				Section: "passkeys",
				Message: "failed to export passkeys",
				Err:     err,
			}
		}
		for _, cred := range creds {
			export.Passkeys = append(export.Passkeys, PasskeyExportItem{
				ID:           cred.ID,
				FriendlyName: cred.FriendlyName,
				CreatedAt:    cred.CreatedAt,
				LastUsedAt:   cred.LastUsedAt,
			})
		}
	}

	// 2. Export Songbooks & Catalog
	if s.Songbooks != nil {
		books, err := s.Songbooks.ListSongbooksByOwner(ctx, user.ID)
		if err != nil {
			return nil, &ExportError{
				Section: "songbooks",
				Message: "failed to export songbooks",
				Err:     err,
			}
		}
		for _, book := range books {
			item := SongbookExportItem{
				Songbook: book,
			}

			var changes []*models.CatalogChange
			if s.CatalogChanges != nil {
				var ccErr error
				changes, ccErr = s.CatalogChanges.ListCatalogChanges(ctx, book.ID)
				if ccErr != nil {
					return nil, &ExportError{
						Section: "catalog_changes",
						Message: fmt.Sprintf("failed to export catalog changes for songbook %s", book.ID),
						Err:     ccErr,
					}
				}
				item.PendingChanges = changes
			}

			if book.CatalogVersionID != "" && s.Catalogs != nil {
				version, versionErr := s.Catalogs.GetCatalogVersion(ctx, book.CatalogVersionID)
				if versionErr != nil {
					return nil, &ExportError{
						Section: "catalog_versions",
						Message: fmt.Sprintf("failed to export catalog version %s", book.CatalogVersionID),
						Err:     versionErr,
					}
				}
				if version.Status == models.CatalogVersionReady {
					item.CatalogVersion = version.ID
					prefix := strings.Trim(version.AssetPrefix, "/")
					item.CatalogURL = "/" + prefix + "/search.json.gz"
					item.ManifestURL = "/" + prefix + "/manifest.json"
					item.SongCount = version.SongCount
				}
			}

			if item.CatalogURL == "" {
				var songs []*models.Song
				if len(changes) > 0 {
					byID := models.ApplyCatalogChanges(nil, changes)
					songs = make([]*models.Song, 0, len(byID))
					for _, song := range byID {
						songs = append(songs, song)
					}
				}
				item.Songs = songs
				item.SongCount = len(songs)
			}

			export.Songbooks = append(export.Songbooks, item)
		}
	}

	// 3. Export Events
	if s.Events != nil {
		events, err := s.Events.ListEventsByOwner(ctx, user.ID)
		if err != nil {
			return nil, &ExportError{
				Section: "events",
				Message: "failed to export events",
				Err:     err,
			}
		}
		export.Events = events
	}

	return export, nil
}

func (s *Service) failCascade(ctx context.Context, userID, step, msg string, err error, _ []string) error {
	if s.Auth != nil && strings.TrimSpace(userID) != "" {
		_ = s.Auth.RecordAccountDeletionProgress(ctx, userID, step, err.Error(), nil)
	}
	return &CascadeError{Step: step, Message: msg, Err: err}
}

// DeleteAccount orchestrates the multi-step cascading purge of all user records, assets, and identity.
func (s *Service) DeleteAccount(ctx context.Context, user *models.User) error {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return errors.New("user is required")
	}

	log.Printf("[PRIVACY] Initiating permanent account deletion for user: id=%s email=%s", user.ID, user.Email)

	catalogVersionsToPurge := mergeCatalogVersionIDs(user.PendingCatalogVersions, nil)

	// 0. Durably mark account as deleting and rotate auth_epoch before revoking credentials.
	if s.Auth != nil {
		updated, err := s.Auth.BeginAccountDeletion(ctx, user.ID, catalogVersionsToPurge)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[PRIVACY ERROR] Failed marking user %s for deletion: %v", user.ID, err)
			return &CascadeError{Step: "begin_deletion", Message: "failed to mark account for deletion", Err: err}
		}
		if updated != nil {
			user.Deleting = updated.Deleting
			user.DeletionEpoch = updated.DeletionEpoch
			user.DeletionStartedAt = updated.DeletionStartedAt
			user.AuthEpoch = updated.AuthEpoch
			catalogVersionsToPurge = mergeCatalogVersionIDs(catalogVersionsToPurge, updated.PendingCatalogVersions)
			user.PendingCatalogVersions = catalogVersionsToPurge
		}
	}

	// 1. Revoke and delete all active auth sessions
	if s.Auth != nil {
		if err := s.Auth.DeleteAuthSessionsForUser(ctx, user.ID); err != nil {
			log.Printf("[PRIVACY ERROR] Failed deleting sessions for user %s: %v", user.ID, err)
			return s.failCascade(ctx, user.ID, "auth_sessions", "failed to delete sessions", err, catalogVersionsToPurge)
		}
	}

	// 2. Delete all passkey credentials
	if s.Passkeys != nil {
		creds, err := s.Passkeys.ListPasskeyCredentialsForUser(ctx, user.ID)
		if err != nil {
			log.Printf("[PRIVACY ERROR] Failed listing passkeys for user %s: %v", user.ID, err)
			return s.failCascade(ctx, user.ID, "list_passkeys", "failed to list passkeys", err, catalogVersionsToPurge)
		}
		for _, cred := range creds {
			if err := s.Passkeys.DeletePasskeyCredential(ctx, cred.ID, user.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("[PRIVACY ERROR] Failed deleting passkey %s for user %s: %v", cred.ID, user.ID, err)
				return s.failCascade(ctx, user.ID, "delete_passkeys", "failed to delete passkey credentials", err, catalogVersionsToPurge)
			}
		}
	}

	// 3. Revoke and delete all developer API keys
	if s.DeveloperKeys != nil {
		keys, err := s.DeveloperKeys.ListDeveloperKeys(ctx, user.ID)
		if err != nil {
			log.Printf("[PRIVACY ERROR] Failed listing developer keys for user %s: %v", user.ID, err)
			return s.failCascade(ctx, user.ID, "list_developer_keys", "failed to list developer keys", err, catalogVersionsToPurge)
		}
		for _, key := range keys {
			if err := s.DeveloperKeys.RevokeDeveloperKey(ctx, key.ID, user.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("[PRIVACY ERROR] Failed revoking developer key %s for user %s: %v", key.ID, user.ID, err)
				return s.failCascade(ctx, user.ID, "delete_developer_keys", "failed to delete developer keys", err, catalogVersionsToPurge)
			}
		}
	}

	// 3b. Remove collaborator grants held by this user (leaves shared songbooks intact)
	if err := s.cleanupCollaboratorGrants(ctx, user.ID); err != nil {
		return s.failCascade(ctx, user.ID, "delete_collaborator_grants", "failed to delete collaborator grants", err, catalogVersionsToPurge)
	}

	// 4. Delete owned events and singer request PII
	if s.Events != nil {
		events, err := s.Events.ListEventsByOwner(ctx, user.ID)
		if err != nil {
			log.Printf("[PRIVACY ERROR] Failed listing events for user %s: %v", user.ID, err)
			return s.failCascade(ctx, user.ID, "list_events", "failed to list events", err, catalogVersionsToPurge)
		}
		for _, ev := range events {
			if err := s.Events.BeginEventDeletion(ctx, ev.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on event %s: %v", ev.ID, err)
				return s.failCascade(ctx, user.ID, "begin_event_deletion", "failed to begin event deletion", err, catalogVersionsToPurge)
			}
			if s.Requests != nil {
				reqs, err := s.Requests.ListRequestsByEvent(ctx, ev.ID)
				if err != nil {
					log.Printf("[PRIVACY ERROR] Failed listing requests for event %s: %v", ev.ID, err)
					return s.failCascade(ctx, user.ID, "list_requests", "failed to list event requests", err, catalogVersionsToPurge)
				}
				for _, rq := range reqs {
					if err := s.Requests.DeleteRequest(ctx, rq.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
						log.Printf("[PRIVACY ERROR] Failed deleting request %s: %v", rq.ID, err)
						return s.failCascade(ctx, user.ID, "delete_requests", "failed to delete event requests", err, catalogVersionsToPurge)
					}
				}
			}
			if err := s.Events.DeleteEvent(ctx, ev.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("[PRIVACY ERROR] Failed deleting event %s: %v", ev.ID, err)
				return s.failCascade(ctx, user.ID, "delete_events", "failed to delete events", err, catalogVersionsToPurge)
			}
		}
	}

	// 5. Delete owned songbooks, catalog changes, import jobs, and S3 assets
	if s.Songbooks != nil {
		books, err := s.Songbooks.ListSongbooksByOwner(ctx, user.ID)
		if err != nil {
			log.Printf("[PRIVACY ERROR] Failed listing songbooks for user %s: %v", user.ID, err)
			return s.failCascade(ctx, user.ID, "list_songbooks", "failed to list songbooks", err, catalogVersionsToPurge)
		}

		for _, book := range books {
			frozenBook, err := s.Songbooks.BeginSongbookDeletion(ctx, book.ID)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on songbook %s: %v", book.ID, err)
				return s.failCascade(ctx, user.ID, "begin_songbook_deletion", "failed to begin songbook deletion", err, catalogVersionsToPurge)
			}
			if frozenBook == nil {
				err := fmt.Errorf("songbook %s deletion barrier returned nil frozen state", book.ID)
				log.Printf("[PRIVACY ERROR] Failed setting deletion barrier on songbook %s: %v", book.ID, err)
				return s.failCascade(ctx, user.ID, "begin_songbook_deletion", "failed to begin songbook deletion", err, catalogVersionsToPurge)
			}
			targetBook := frozenBook
			if err := s.cleanupOwnedSongbookCollaboration(ctx, targetBook); err != nil {
				return s.failCascade(ctx, user.ID, "cleanup_songbook_collaboration", "failed to clean up songbook collaboration", err, catalogVersionsToPurge)
			}

			// Capture authoritative catalog pointer from the frozen state after the barrier
			if targetBook.CatalogVersionID != "" {
				newVersions := mergeCatalogVersionIDs(catalogVersionsToPurge, []string{targetBook.CatalogVersionID})
				if len(newVersions) > len(catalogVersionsToPurge) {
					catalogVersionsToPurge = newVersions
					user.PendingCatalogVersions = catalogVersionsToPurge

					// Persist discovered catalog version before deleting the songbook or its children
					if s.Auth != nil {
						updated, err := s.Auth.BeginAccountDeletion(ctx, user.ID, catalogVersionsToPurge)
						if err != nil && !errors.Is(err, os.ErrNotExist) {
							log.Printf("[PRIVACY ERROR] Failed recording pending catalog versions for user %s: %v", user.ID, err)
							return s.failCascade(ctx, user.ID, "record_catalog_versions", "failed to record pending catalog versions", err, catalogVersionsToPurge)
						}
						if updated != nil {
							catalogVersionsToPurge = mergeCatalogVersionIDs(catalogVersionsToPurge, updated.PendingCatalogVersions)
							user.PendingCatalogVersions = catalogVersionsToPurge
						}
					}
				}
			}
			// A. Delete pending catalog changes
			if s.CatalogChanges != nil {
				changes, err := s.CatalogChanges.ListCatalogChanges(ctx, book.ID)
				if err != nil {
					log.Printf("[PRIVACY ERROR] Failed listing catalog changes for songbook %s: %v", book.ID, err)
					return s.failCascade(ctx, user.ID, "list_catalog_changes", "failed to list catalog changes", err, catalogVersionsToPurge)
				}
				for _, change := range changes {
					if err := s.CatalogChanges.DeleteCatalogChange(ctx, book.ID, change.SongID, time.Time{}); err != nil && !errors.Is(err, os.ErrNotExist) {
						log.Printf("[PRIVACY ERROR] Failed deleting catalog change for songbook %s song %s: %v", book.ID, change.SongID, err)
						return s.failCascade(ctx, user.ID, "delete_catalog_changes", "failed to delete catalog changes", err, catalogVersionsToPurge)
					}
				}
			}

			// B. Delete import jobs and uploaded manifests
			if s.ImportJobs != nil {
				jobs, err := s.ImportJobs.ListImportJobs(ctx)
				if err != nil {
					log.Printf("[PRIVACY ERROR] Failed listing import jobs: %v", err)
					return s.failCascade(ctx, user.ID, "list_import_jobs", "failed to list import jobs", err, catalogVersionsToPurge)
				}
				for _, job := range jobs {
					if job.SongbookID == book.ID {
						if job.ManifestKey != "" && s.Assets != nil {
							if err := s.Assets.DeleteKey(ctx, job.ManifestKey); err != nil && !errors.Is(err, os.ErrNotExist) {
								log.Printf("[PRIVACY ERROR] Failed deleting import manifest key %s: %v", job.ManifestKey, err)
								return s.failCascade(ctx, user.ID, "delete_import_assets", "failed to delete import manifest assets", err, catalogVersionsToPurge)
							}
						}
						if err := s.ImportJobs.DeleteImportJob(ctx, job.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
							log.Printf("[PRIVACY ERROR] Failed deleting import job %s: %v", job.ID, err)
							return s.failCascade(ctx, user.ID, "delete_import_jobs", "failed to delete import jobs", err, catalogVersionsToPurge)
						}
					}
				}
			}

			// C. Durably delete songbook (and all songs in DynamoDB) BEFORE purging catalog assets
			if err := s.Songbooks.DeleteSongbook(ctx, book.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("[PRIVACY ERROR] Failed deleting songbook %s: %v", book.ID, err)
				return s.failCascade(ctx, user.ID, "delete_songbooks", "failed to delete songbooks", err, catalogVersionsToPurge)
			}
		}
	}

	// E. Purge catalog versions AFTER all user songbooks are durably deleted (including versions recovered on retry)
	if s.Catalogs != nil && len(catalogVersionsToPurge) > 0 {
		for _, versionID := range catalogVersionsToPurge {
			err := s.Catalogs.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
				if prefix != "" && s.Assets != nil {
					return s.Assets.DeletePrefix(ctx, prefix)
				}
				return nil
			}, store.PurgeCatalogOptions{
				LeaseDuration:       s.CatalogLeaseDuration,
				ReleaseLeaseOnError: true,
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				if errors.Is(err, store.ErrCatalogVersionInUse) {
					log.Printf("[PRIVACY] Catalog version %s is shared with other active songbooks; preserving shared assets", versionID)
				} else {
					log.Printf("[PRIVACY ERROR] Failed purging catalog version %s: %v", versionID, err)
					return s.failCascade(ctx, user.ID, "purge_catalog_version", "failed to purge catalog version", err, nil)
				}
			}
			if s.Auth != nil {
				if progErr := s.Auth.RecordAccountDeletionProgress(ctx, user.ID, "purge_catalog_version", "", []string{versionID}); progErr != nil && !errors.Is(progErr, os.ErrNotExist) {
					log.Printf("[PRIVACY ERROR] Failed recording deletion progress for user %s: %v", user.ID, progErr)
					return s.failCascade(ctx, user.ID, "record_progress", "failed to record account deletion progress", progErr, catalogVersionsToPurge)
				}
			}
		}
		user.PendingCatalogVersions = nil
	}

	// 6. Delete user record and release email identity claim
	if s.Auth != nil {
		userDeleted := false
		var lastFinalizeErr error
		for finalizeAttempt := 0; finalizeAttempt < 4; finalizeAttempt++ {
			err := s.Auth.DeleteUser(ctx, user.ID)
			if err == nil || errors.Is(err, os.ErrNotExist) {
				userDeleted = true
				break
			}
			lastFinalizeErr = err
			current, getErr := s.Auth.GetUser(ctx, user.ID)
			if getErr != nil || current == nil || len(current.PendingCatalogVersions) == 0 {
				log.Printf("[PRIVACY ERROR] Failed deleting user record %s: %v", user.ID, err)
				return s.failCascade(ctx, user.ID, "delete_user", "failed to delete account", err, nil)
			}
			for _, versionID := range current.PendingCatalogVersions {
				if s.Catalogs != nil {
					pErr := s.Catalogs.PurgeCatalogVersion(ctx, versionID, func(prefix string) error {
						if prefix != "" && s.Assets != nil {
							return s.Assets.DeletePrefix(ctx, prefix)
						}
						return nil
					}, store.PurgeCatalogOptions{
						LeaseDuration:       s.CatalogLeaseDuration,
						ReleaseLeaseOnError: true,
					})
					if pErr != nil && !errors.Is(pErr, os.ErrNotExist) && !errors.Is(pErr, store.ErrCatalogVersionInUse) {
						return s.failCascade(ctx, user.ID, "purge_catalog_version", "failed to purge catalog version", pErr, nil)
					}
				}
				if progErr := s.Auth.RecordAccountDeletionProgress(ctx, user.ID, "purge_catalog_version", "", []string{versionID}); progErr != nil && !errors.Is(progErr, os.ErrNotExist) {
					return s.failCascade(ctx, user.ID, "record_progress", "failed to record account deletion progress", progErr, nil)
				}
			}
		}
		if !userDeleted {
			log.Printf("[PRIVACY ERROR] Failed deleting user record %s after finalization retry exhaustion: %v", user.ID, lastFinalizeErr)
			return s.failCascade(ctx, user.ID, "delete_user", "failed to delete account after finalization retry exhaustion", lastFinalizeErr, nil)
		}
	}

	log.Printf("[PRIVACY] Successfully purged user account: id=%s", user.ID)
	return nil
}

// ResumeAccountDeletion resumes an interrupted account deletion using the durable deletion state
// persisted on the user record, without requiring or restoring any user credentials.
func (s *Service) ResumeAccountDeletion(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user id is required")
	}
	if s.Auth == nil {
		return errors.New("auth store is required")
	}

	user, err := s.Auth.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// User row is already removed; run idempotent cleanup of any residual user-keyed items.
			return s.DeleteAccount(ctx, &models.User{ID: userID, Deleting: true})
		}
		return err
	}
	if !user.Deleting {
		return errors.New("account is not marked for deletion")
	}
	return s.DeleteAccount(ctx, user)
}

// RecoverDeletingAccounts scans for all accounts marked as deleting and drives their cascading
// deletion to completion. It returns the number of accounts completely purged and any first error encountered.
func (s *Service) RecoverDeletingAccounts(ctx context.Context) (int, error) {
	if s.Auth == nil {
		return 0, errors.New("auth store is required")
	}
	users, err := s.Auth.ListDeletingUsers(ctx)
	if err != nil {
		return 0, err
	}
	completed := 0
	var firstErr error
	for _, u := range users {
		if err := s.DeleteAccount(ctx, u); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		completed++
	}
	return completed, firstErr
}
