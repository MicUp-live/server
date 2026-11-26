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
	effectiveOwner := sb.OwnerID

	alreadyConfirmed := sb.Deleting && sb.DeletionConfirmed

	if !alreadyConfirmed {
		var preForeign []*models.Event
		if s.Events != nil {
			events, err := s.Events.ListEventsBySongbook(ctx, songbookID)
			if err != nil {
				return fmt.Errorf("check associated events: %w", err)
			}
			ownerEvents, foreignEvents := partitionEventsByOwner(events, effectiveOwner)
			if len(ownerEvents) > 0 {
				return &EventsAssociatedError{EventNames: formatEventNames(ownerEvents)}
			}
			preForeign = foreignEvents
			if len(foreignEvents) > 0 {
				expected := computeForeignEventsToken(songbookID, foreignEvents)
				if strings.TrimSpace(confirmToken) != expected {
					return buildForeignEventsError(songbookID, foreignEvents)
				}
			}
		}

		// Establish write barrier so no new events, grants, or catalog writes can attach.
		frozenBook, err := s.Songbooks.BeginSongbookDeletion(ctx, songbookID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrSongbookNotFound
			}
			return fmt.Errorf("begin songbook deletion: %w", err)
		}
		if frozenBook != nil {
			sb = frozenBook
		}

		// Re-check dependencies authoritatively AFTER the write barrier.
		if s.Events != nil {
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
			if len(postForeign) > 0 {
				preSet := make(map[string]struct{}, len(preForeign))
				for _, ev := range preForeign {
					preSet[ev.ID] = struct{}{}
				}
				raced := false
				for _, ev := range postForeign {
					if _, ok := preSet[ev.ID]; !ok {
						raced = true
						break
					}
				}
				if raced {
					_ = s.Songbooks.AbortSongbookDeletion(ctx, songbookID)
					return buildForeignEventsError(songbookID, postForeign)
				}
			}
		}

		if err := s.Songbooks.ConfirmSongbookDeletion(ctx, songbookID); err != nil {
			return fmt.Errorf("confirm songbook deletion: %w", err)
		}
	}

	return s.CleanupDeletingSongbook(ctx, sb)
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

	// 1. End and detach all foreign events while retaining their event and queue history.
	if s.Events != nil {
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
	}

	// 2. Delete all collaborator grants for this songbook.
	if s.Access != nil {
		grants, err := s.Access.ListSongbookGrantsBySongbook(ctx, sb.ID)
		if err != nil {
			return fmt.Errorf("list songbook grants during cleanup: %w", err)
		}
		for _, g := range grants {
			if err := s.Access.DeleteSongbookGrant(ctx, sb.ID, g.UserID); err != nil && !errors.Is(err, store.ErrGrantNotFound) && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete songbook grant %s: %w", g.UserID, err)
			}
		}

		// 3. Delete all invitations for this songbook.
		invitations, err := s.Access.ListSongbookInvitationsBySongbook(ctx, sb.ID)
		if err != nil {
			return fmt.Errorf("list songbook invitations during cleanup: %w", err)
		}
		for _, inv := range invitations {
			if err := s.Access.DeleteSongbookInvitation(ctx, inv.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete songbook invitation %s: %w", inv.ID, err)
			}
		}
	}

	// 4. Delete pending catalog changes.
	if s.CatalogChanges != nil {
		changes, err := s.CatalogChanges.ListCatalogChanges(ctx, sb.ID)
		if err != nil {
			return fmt.Errorf("list catalog changes during cleanup: %w", err)
		}
		for _, change := range changes {
			if err := s.CatalogChanges.DeleteCatalogChange(ctx, sb.ID, change.SongID, time.Time{}); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete catalog change %s: %w", change.SongID, err)
			}
		}
	}

	// 5. Delete import jobs and uploaded manifests for this songbook.
	if s.ImportJobs != nil {
		jobs, err := s.ImportJobs.ListImportJobs(ctx)
		if err != nil {
			return fmt.Errorf("list import jobs during cleanup: %w", err)
		}
		for _, job := range jobs {
			if job.SongbookID != sb.ID {
				continue
			}
			if job.ManifestKey != "" && s.Assets != nil {
				if err := s.Assets.DeleteKey(ctx, job.ManifestKey); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("delete import manifest %s: %w", job.ManifestKey, err)
				}
			}
			if err := s.ImportJobs.DeleteImportJob(ctx, job.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete import job %s: %w", job.ID, err)
			}
		}
	}

	// 6. Durably delete the songbook (and its songs).
	if err := s.Songbooks.DeleteSongbook(ctx, sb.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete songbook: %w", err)
	}

	// 7. Purge unreferenced catalog version assets (preserving shared catalog versions).
	if sb.CatalogVersionID != "" && s.Catalogs != nil {
		err := s.Catalogs.PurgeCatalogVersion(ctx, sb.CatalogVersionID, func(prefix string) error {
			if prefix != "" && s.Assets != nil {
				return s.Assets.DeletePrefix(ctx, prefix)
			}
			return nil
		}, store.PurgeCatalogOptions{ReleaseLeaseOnError: true})
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, store.ErrCatalogVersionInUse) {
			return fmt.Errorf("purge catalog version %s: %w", sb.CatalogVersionID, err)
		}
	}
	return nil
}
