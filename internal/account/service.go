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
	passkeys, err := s.exportPasskeys(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	export.Passkeys = passkeys

	// 2. Export Songbooks & Catalog
	songbooks, err := s.exportSongbooks(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	export.Songbooks = songbooks

	// 3. Export Events
	events, err := s.exportEvents(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	export.Events = events

	return export, nil
}

func (s *Service) exportPasskeys(ctx context.Context, userID string) ([]PasskeyExportItem, error) {
	if s.Passkeys == nil {
		return nil, nil
	}
	creds, err := s.Passkeys.ListPasskeyCredentialsForUser(ctx, userID)
	if err != nil {
		return nil, &ExportError{
			Section: "passkeys",
			Message: "failed to export passkeys",
			Err:     err,
		}
	}
	items := make([]PasskeyExportItem, 0, len(creds))
	for _, cred := range creds {
		items = append(items, PasskeyExportItem{
			ID:           cred.ID,
			FriendlyName: cred.FriendlyName,
			CreatedAt:    cred.CreatedAt,
			LastUsedAt:   cred.LastUsedAt,
		})
	}
	return items, nil
}

func (s *Service) exportSongbooks(ctx context.Context, userID string) ([]SongbookExportItem, error) {
	if s.Songbooks == nil {
		return nil, nil
	}
	books, err := s.Songbooks.ListSongbooksByOwner(ctx, userID)
	if err != nil {
		return nil, &ExportError{
			Section: "songbooks",
			Message: "failed to export songbooks",
			Err:     err,
		}
	}
	items := make([]SongbookExportItem, 0, len(books))
	for _, book := range books {
		item, err := s.exportSongbookItem(ctx, book)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) exportSongbookItem(ctx context.Context, book *models.Songbook) (SongbookExportItem, error) {
	item := SongbookExportItem{Songbook: book}

	changes, err := s.exportBookCatalogChanges(ctx, book.ID)
	if err != nil {
		return SongbookExportItem{}, err
	}
	item.PendingChanges = changes

	if err := s.applyCatalogVersionToExport(ctx, &item, book.CatalogVersionID); err != nil {
		return SongbookExportItem{}, err
	}
	if item.CatalogURL == "" {
		populateFallbackSongs(&item, changes)
	}
	return item, nil
}

func (s *Service) exportBookCatalogChanges(ctx context.Context, bookID string) ([]*models.CatalogChange, error) {
	if s.CatalogChanges == nil {
		return nil, nil
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, bookID)
	if err != nil {
		return nil, &ExportError{
			Section: "catalog_changes",
			Message: fmt.Sprintf("failed to export catalog changes for songbook %s", bookID),
			Err:     err,
		}
	}
	return changes, nil
}

func (s *Service) applyCatalogVersionToExport(ctx context.Context, item *SongbookExportItem, versionID string) error {
	if versionID == "" || s.Catalogs == nil {
		return nil
	}
	version, err := s.Catalogs.GetCatalogVersion(ctx, versionID)
	if err != nil {
		return &ExportError{
			Section: "catalog_versions",
			Message: fmt.Sprintf("failed to export catalog version %s", versionID),
			Err:     err,
		}
	}
	if version.Status == models.CatalogVersionReady {
		item.CatalogVersion = version.ID
		prefix := strings.Trim(version.AssetPrefix, "/")
		item.CatalogURL = "/" + prefix + "/search.json.gz"
		item.ManifestURL = "/" + prefix + "/manifest.json"
		item.SongCount = version.SongCount
	}
	return nil
}

func populateFallbackSongs(item *SongbookExportItem, changes []*models.CatalogChange) {
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

func (s *Service) exportEvents(ctx context.Context, userID string) ([]*models.Event, error) {
	if s.Events == nil {
		return nil, nil
	}
	events, err := s.Events.ListEventsByOwner(ctx, userID)
	if err != nil {
		return nil, &ExportError{
			Section: "events",
			Message: "failed to export events",
			Err:     err,
		}
	}
	return events, nil
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
	var err error
	catalogVersionsToPurge, err = s.beginAccountDeletion(ctx, user, catalogVersionsToPurge)
	if err != nil {
		return err
	}

	// 1. Revoke and delete all active auth sessions
	if err := s.deleteAuthSessions(ctx, user.ID, catalogVersionsToPurge); err != nil {
		return err
	}

	// 2. Delete all passkey credentials
	if err := s.deleteUserPasskeys(ctx, user.ID, catalogVersionsToPurge); err != nil {
		return err
	}

	// 3. Revoke and delete all developer API keys
	if err := s.deleteUserDeveloperKeys(ctx, user.ID, catalogVersionsToPurge); err != nil {
		return err
	}

	// 3b. Remove collaborator grants held by this user (leaves shared songbooks intact)
	if err := s.cleanupCollaboratorGrants(ctx, user.ID); err != nil {
		return s.failCascade(ctx, user.ID, "delete_collaborator_grants", "failed to delete collaborator grants", err, catalogVersionsToPurge)
	}

	// 4. Delete owned events and singer request PII
	if err := s.deleteUserEvents(ctx, user.ID, catalogVersionsToPurge); err != nil {
		return err
	}

	// 5. Delete owned songbooks, catalog changes, import jobs, and S3 assets
	catalogVersionsToPurge, err = s.deleteUserSongbooks(ctx, user, catalogVersionsToPurge)
	if err != nil {
		return err
	}

	// E. Purge catalog versions AFTER all user songbooks are durably deleted (including versions recovered on retry)
	if err := s.purgeUserCatalogVersions(ctx, user, catalogVersionsToPurge); err != nil {
		return err
	}

	// 6. Delete user record and release email identity claim
	if err := s.finalizeUserDeletion(ctx, user.ID); err != nil {
		return err
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
