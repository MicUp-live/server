package songbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// ForeignEventsAssociatedError reports that other hosts have shows linked to the songbook
// and requires explicit owner confirmation bound to the observed dependencies.
type ForeignEventsAssociatedError struct {
	AffectedShowsCount int    `json:"affected_shows_count"`
	ActiveShowsCount   int    `json:"active_shows_count"`
	ConfirmationToken  string `json:"confirmation_token"`
}

func (e *ForeignEventsAssociatedError) Error() string {
	return fmt.Sprintf("cannot delete songbook without confirmation: %d collaborator show(s) will be ended and detached", e.AffectedShowsCount)
}

func computeForeignEventsToken(songbookID string, foreignEvents []*models.Event) string {
	ids := make([]string, 0, len(foreignEvents))
	for _, ev := range foreignEvents {
		ids = append(ids, ev.ID)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(songbookID + ":" + strings.Join(ids, ",")))
	return hex.EncodeToString(sum[:16])
}

func partitionEventsByOwner(events []*models.Event, ownerID string) (ownerEvents, foreignEvents []*models.Event) {
	for _, ev := range events {
		if ev.OwnerID == "" || ev.OwnerID == ownerID {
			ownerEvents = append(ownerEvents, ev)
		} else {
			foreignEvents = append(foreignEvents, ev)
		}
	}
	return ownerEvents, foreignEvents
}

func buildForeignEventsError(songbookID string, foreignEvents []*models.Event) *ForeignEventsAssociatedError {
	active := 0
	for _, ev := range foreignEvents {
		if ev.Status == models.EventStatusActive || ev.Status == models.EventStatusPaused {
			active++
		}
	}
	return &ForeignEventsAssociatedError{
		AffectedShowsCount: len(foreignEvents),
		ActiveShowsCount:   active,
		ConfirmationToken:  computeForeignEventsToken(songbookID, foreignEvents),
	}
}

func formatEventNames(events []*models.Event) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		name := e.Name
		if name == "" {
			name = e.VenueName
		}
		if name == "" {
			name = e.JoinCode
		}
		if name == "" {
			name = e.ID
		}
		names = append(names, fmt.Sprintf("%q", name))
	}
	return names
}

// DeleteSongbookWithConfirmation deletes an owned songbook with an authoritative
// dependency check, write barrier, foreign-event detach confirmation, and resumable cleanup.
func (s *Service) DeleteSongbookWithConfirmation(ctx context.Context, ownerID, songbookID, confirmToken string) error {
	if s.Songbooks == nil {
		return ErrSongbookNotFound
	}
	sb, err := s.Songbooks.GetSongbook(ctx, songbookID)
	if err != nil || sb == nil {
		return ErrSongbookNotFound
	}
	if ownerID != "" && sb.OwnerID != ownerID {
		return ErrSongbookNotFound
	}
	if sb.Deleting && sb.DeletionConfirmed {
		return s.CleanupDeletingSongbook(ctx, sb)
	}

	frozenBook, err := s.confirmSongbookDeletion(ctx, sb, confirmToken)
	if err != nil {
		return err
	}
	return s.CleanupDeletingSongbook(ctx, frozenBook)
}

func (s *Service) confirmSongbookDeletion(ctx context.Context, sb *models.Songbook, confirmToken string) (*models.Songbook, error) {
	effectiveOwner := sb.OwnerID
	preForeign, err := s.checkPreBarrierEvents(ctx, sb.ID, effectiveOwner, confirmToken)
	if err != nil {
		return nil, err
	}

	// Establish write barrier so no new events, grants, or catalog writes can attach.
	frozenBook, err := s.Songbooks.BeginSongbookDeletion(ctx, sb.ID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSongbookNotFound
		}
		return nil, fmt.Errorf("begin songbook deletion: %w", err)
	}
	if frozenBook != nil {
		sb = frozenBook
	}

	// Re-check dependencies authoritatively AFTER the write barrier.
	if err := s.verifyPostBarrierEvents(ctx, sb.ID, effectiveOwner, preForeign); err != nil {
		return nil, err
	}
	if err := s.Songbooks.ConfirmSongbookDeletion(ctx, sb.ID); err != nil {
		return nil, fmt.Errorf("confirm songbook deletion: %w", err)
	}
	return sb, nil
}

func (s *Service) checkPreBarrierEvents(ctx context.Context, songbookID, effectiveOwner, confirmToken string) ([]*models.Event, error) {
	if s.Events == nil {
		return nil, nil
	}
	events, err := s.Events.ListEventsBySongbook(ctx, songbookID)
	if err != nil {
		return nil, fmt.Errorf("check associated events: %w", err)
	}
	ownerEvents, foreignEvents := partitionEventsByOwner(events, effectiveOwner)
	if len(ownerEvents) > 0 {
		return nil, &EventsAssociatedError{EventNames: formatEventNames(ownerEvents)}
	}
	if len(foreignEvents) > 0 && strings.TrimSpace(confirmToken) != computeForeignEventsToken(songbookID, foreignEvents) {
		return nil, buildForeignEventsError(songbookID, foreignEvents)
	}
	return foreignEvents, nil
}

func (s *Service) verifyPostBarrierEvents(ctx context.Context, songbookID, effectiveOwner string, preForeign []*models.Event) error {
	if s.Events == nil {
		return nil
	}
	postEvents, err := s.Events.ListEventsBySongbook(ctx, songbookID)
	if err != nil {
		_ = s.Songbooks.AbortSongbookDeletion(ctx, songbookID)
		return fmt.Errorf("verify associated events after barrier: %w", err)
	}
	postOwner, postForeign := partitionEventsByOwner(postEvents, effectiveOwner)
	if len(postOwner) > 0 {
		_ = s.Songbooks.AbortSongbookDeletion(ctx, songbookID)
		return &EventsAssociatedError{EventNames: formatEventNames(postOwner)}
	}
	// Ensure no newly linked foreign show raced in before the barrier.
	if hasNewForeignEvents(preForeign, postForeign) {
		_ = s.Songbooks.AbortSongbookDeletion(ctx, songbookID)
		return buildForeignEventsError(songbookID, postForeign)
	}
	return nil
}

func hasNewForeignEvents(preForeign, postForeign []*models.Event) bool {
	if len(postForeign) == 0 {
		return false
	}
	preSet := make(map[string]struct{}, len(preForeign))
	for _, ev := range preForeign {
		preSet[ev.ID] = struct{}{}
	}
	for _, ev := range postForeign {
		if _, ok := preSet[ev.ID]; !ok {
			return true
		}
	}
	return false
}

// CleanupDeletingSongbook performs resumable cleanup of a songbook that already has
// its deletion barrier established: ends/detaches foreign shows, removes grants and
// invitations, deletes pending overrides and import jobs, deletes the songbook, and
// purges unreferenced catalog assets.
func (s *Service) CleanupDeletingSongbook(ctx context.Context, sb *models.Songbook) error {
	if sb == nil {
		return ErrSongbookNotFound
	}
	retention := s.QueueRetention
	if retention <= 0 {
		retention = 14 * 24 * time.Hour
	}

	if err := s.detachCleanupEvents(ctx, sb, retention); err != nil {
		return err
	}
	if err := s.deleteCleanupGrants(ctx, sb.ID); err != nil {
		return err
	}
	if err := s.deleteCleanupInvitations(ctx, sb.ID); err != nil {
		return err
	}
	if err := s.deleteCleanupCatalogChanges(ctx, sb.ID); err != nil {
		return err
	}
	if err := s.deleteCleanupImportJobs(ctx, sb.ID); err != nil {
		return err
	}
	// 6. Durably delete the songbook (and its songs).
	if err := s.Songbooks.DeleteSongbook(ctx, sb.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete songbook: %w", err)
	}
	return s.purgeCleanupCatalogVersion(ctx, sb.CatalogVersionID)
}

// 1. End and detach all foreign events while retaining their event and queue history.
func (s *Service) detachCleanupEvents(ctx context.Context, sb *models.Songbook, retention time.Duration) error {
	if s.Events == nil {
		return nil
	}
	events, err := s.Events.ListEventsBySongbook(ctx, sb.ID)
	if err != nil {
		return fmt.Errorf("list linked events during cleanup: %w", err)
	}
	for _, ev := range events {
		if ev.OwnerID == sb.OwnerID {
			return &EventsAssociatedError{EventNames: formatEventNames([]*models.Event{ev})}
		}
		if _, err := s.Events.DetachEventFromSongbook(ctx, ev.ID, sb.ID, sb.Name, retention); err != nil {
			return fmt.Errorf("detach collaborator event %s: %w", ev.ID, err)
		}
	}
	return nil
}

// 2. Delete all collaborator grants for this songbook.
func (s *Service) deleteCleanupGrants(ctx context.Context, songbookID string) error {
	if s.Access == nil {
		return nil
	}
	grants, err := s.Access.ListSongbookGrantsBySongbook(ctx, songbookID)
	if err != nil {
		return fmt.Errorf("list songbook grants during cleanup: %w", err)
	}
	for _, g := range grants {
		if err := s.Access.DeleteSongbookGrant(ctx, songbookID, g.UserID); err != nil && !errors.Is(err, store.ErrGrantNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete songbook grant %s: %w", g.UserID, err)
		}
	}
	return nil
}

// 3. Delete all invitations for this songbook.
func (s *Service) deleteCleanupInvitations(ctx context.Context, songbookID string) error {
	if s.Access == nil {
		return nil
	}
	invitations, err := s.Access.ListSongbookInvitationsBySongbook(ctx, songbookID)
	if err != nil {
		return fmt.Errorf("list songbook invitations during cleanup: %w", err)
	}
	for _, inv := range invitations {
		if err := s.Access.DeleteSongbookInvitation(ctx, inv.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete songbook invitation %s: %w", inv.ID, err)
		}
	}
	return nil
}

// 4. Delete pending catalog changes.
func (s *Service) deleteCleanupCatalogChanges(ctx context.Context, songbookID string) error {
	if s.CatalogChanges == nil {
		return nil
	}
	changes, err := s.CatalogChanges.ListCatalogChanges(ctx, songbookID)
	if err != nil {
		return fmt.Errorf("list catalog changes during cleanup: %w", err)
	}
	for _, change := range changes {
		if err := s.CatalogChanges.DeleteCatalogChange(ctx, songbookID, change.SongID, time.Time{}); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete catalog change %s: %w", change.SongID, err)
		}
	}
	return nil
}

// 5. Delete import jobs and uploaded manifests for this songbook.
func (s *Service) deleteCleanupImportJobs(ctx context.Context, songbookID string) error {
	if s.ImportJobs == nil {
		return nil
	}
	jobs, err := s.ImportJobs.ListImportJobs(ctx)
	if err != nil {
		return fmt.Errorf("list import jobs during cleanup: %w", err)
	}
	for _, job := range jobs {
		if job.SongbookID != songbookID {
			continue
		}
		if err := s.deleteCleanupImportJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteCleanupImportJob(ctx context.Context, job *models.ImportJob) error {
	if job.ManifestKey != "" && s.Assets != nil {
		if err := s.Assets.DeleteKey(ctx, job.ManifestKey); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete import manifest %s: %w", job.ManifestKey, err)
		}
	}
	if err := s.ImportJobs.DeleteImportJob(ctx, job.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete import job %s: %w", job.ID, err)
	}
	return nil
}

// 7. Purge unreferenced catalog version assets (preserving shared catalog versions).
func (s *Service) purgeCleanupCatalogVersion(ctx context.Context, catalogVersionID string) error {
	if catalogVersionID == "" || s.Catalogs == nil {
		return nil
	}
	err := s.Catalogs.PurgeCatalogVersion(ctx, catalogVersionID, func(prefix string) error {
		if prefix != "" && s.Assets != nil {
			return s.Assets.DeletePrefix(ctx, prefix)
		}
		return nil
	}, store.PurgeCatalogOptions{ReleaseLeaseOnError: true})
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, store.ErrCatalogVersionInUse) {
		return fmt.Errorf("purge catalog version %s: %w", catalogVersionID, err)
	}
	return nil
}
