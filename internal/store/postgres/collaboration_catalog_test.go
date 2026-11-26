package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestSongbooksLifecycleAndSlugUniqueness(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "sbowner@example.com", true, true)

	// Multiple empty slugs allowed; duplicate non-empty slug rejected.
	sb1 := createTestSongbook(t, s, owner.ID, "", false)
	sb2 := createTestSongbook(t, s, owner.ID, "", true)
	sb3 := createTestSongbook(t, s, owner.ID, "Rock-Bar", true)
	if err := s.CreateSongbook(ctx, &models.Songbook{
		ID: uuid.NewString(), OwnerID: owner.ID, PublicID: "pub-dup", Slug: "  rock-bar ", Name: "Dup",
	}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist for duplicate slug, got %v", err)
	}

	// GetPublicSongbook rejects private songbooks, resolves public by public_id or slug.
	if _, err := s.GetPublicSongbook(ctx, sb1.PublicID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected private songbook ErrNotExist, got %v", err)
	}
	if pub, err := s.GetPublicSongbook(ctx, sb2.PublicID); err != nil || pub.ID != sb2.ID {
		t.Fatalf("GetPublicSongbook by publicID failed: %+v err=%v", pub, err)
	}
	if pub, err := s.GetPublicSongbook(ctx, "ROCK-BAR"); err != nil || pub.ID != sb3.ID {
		t.Fatalf("GetPublicSongbook by slug failed: %+v err=%v", pub, err)
	}
	if bySlug, err := s.GetSongbookBySlug(ctx, "rock-bar"); err != nil || bySlug.ID != sb3.ID {
		t.Fatalf("GetSongbookBySlug failed: %+v err=%v", bySlug, err)
	}

	// NextCatalogGeneration does not advance catalog_generation.
	gen1, err := s.NextCatalogGeneration(ctx, sb3.ID)
	if err != nil || gen1 != 1 {
		t.Fatalf("NextCatalogGeneration #1: gen=%d err=%v", gen1, err)
	}
	gen2, err := s.NextCatalogGeneration(ctx, sb3.ID)
	if err != nil || gen2 != 2 {
		t.Fatalf("NextCatalogGeneration #2: gen=%d err=%v", gen2, err)
	}
	loaded, _ := s.GetSongbook(ctx, sb3.ID)
	if loaded.CatalogGeneration != 0 {
		t.Fatalf("expected CatalogGeneration still 0, got %d", loaded.CatalogGeneration)
	}

	// UpdateSongbook.
	loaded.Name = "Updated Rock Bar"
	loaded.IsPublic = false
	if err := s.UpdateSongbook(ctx, loaded); err != nil {
		t.Fatalf("UpdateSongbook: %v", err)
	}

	// Deletion barrier: Begin -> Update blocked -> Abort -> Begin -> Confirm -> Abort cannot revert -> Delete.
	if _, err := s.BeginSongbookDeletion(ctx, sb3.ID); err != nil {
		t.Fatalf("BeginSongbookDeletion: %v", err)
	}
	if err := s.UpdateSongbook(ctx, loaded); !errors.Is(err, store.ErrSongbookDeleting) {
		t.Fatalf("expected ErrSongbookDeleting on UpdateSongbook, got %v", err)
	}
	if err := s.AbortSongbookDeletion(ctx, sb3.ID); err != nil {
		t.Fatalf("AbortSongbookDeletion: %v", err)
	}
	if err := s.UpdateSongbook(ctx, loaded); err != nil {
		t.Fatalf("UpdateSongbook after abort: %v", err)
	}
	if _, err := s.BeginSongbookDeletion(ctx, sb3.ID); err != nil {
		t.Fatalf("BeginSongbookDeletion #2: %v", err)
	}
	if err := s.ConfirmSongbookDeletion(ctx, sb3.ID); err != nil {
		t.Fatalf("ConfirmSongbookDeletion: %v", err)
	}
	if err := s.AbortSongbookDeletion(ctx, sb3.ID); err != nil {
		t.Fatalf("AbortSongbookDeletion after confirm: %v", err)
	}
	confirmedCheck, _ := s.GetSongbook(ctx, sb3.ID)
	if !confirmedCheck.Deleting || !confirmedCheck.DeletionConfirmed {
		t.Fatalf("expected deleting & deletion_confirmed to remain true, got %+v", confirmedCheck)
	}
	if err := s.DeleteSongbook(ctx, sb3.ID); err != nil {
		t.Fatalf("DeleteSongbook: %v", err)
	}
	if _, err := s.GetSongbook(ctx, sb3.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deleted songbook ErrNotExist, got %v", err)
	}
}

func TestCatalogVersionsPublicationAndPurge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "catowner@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)

	// Build, fail, reclaim, and mark ready.
	cv1 := &models.CatalogVersion{ID: "cv-1", SchemaVersion: 1, LeaseToken: "tok-1"}
	if err := s.CreateCatalogVersion(ctx, cv1); err != nil {
		t.Fatalf("CreateCatalogVersion cv1: %v", err)
	}
	if _, err := s.ReclaimCatalogVersion(ctx, "cv-1", time.Minute); !errors.Is(err, store.ErrReclaimConflict) {
		t.Fatalf("expected ErrReclaimConflict while lease active, got %v", err)
	}
	if _, err := s.FailCatalogVersion(ctx, "cv-1", "bad manifest", "tok-1"); err != nil {
		t.Fatalf("FailCatalogVersion: %v", err)
	}
	reclaimed, err := s.ReclaimCatalogVersion(ctx, "cv-1", time.Minute)
	if err != nil || reclaimed.Status != models.CatalogVersionBuilding {
		t.Fatalf("ReclaimCatalogVersion failed: %+v err=%v", reclaimed, err)
	}
	if _, err := s.MarkCatalogVersionReady(ctx, "cv-1", "catalogs/cv-1", 10, "wrong-tok"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected wrong lease token rejected, got %v", err)
	}
	if _, err := s.MarkCatalogVersionReady(ctx, "cv-1", "catalogs/cv-1", 10, reclaimed.LeaseToken); err != nil {
		t.Fatalf("MarkCatalogVersionReady: %v", err)
	}

	// Publish cv-1 with ExpectedBaseVersion == "" and JobGeneration == 2 + atomic JobID completion.
	job := &models.ImportJob{ID: "job-pub-1", SongbookID: sb.ID, Status: models.ImportJobProcessing, Generation: 2}
	if err := s.CreateImportJob(ctx, job); err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	emptyBase := ""
	if err := s.PublishCatalogVersion(ctx, sb.ID, "cv-1", store.PublicationOptions{
		ExpectedBaseVersion: &emptyBase,
		JobGeneration:       2,
		JobID:               job.ID,
		ProcessedSongs:      10,
	}); err != nil {
		t.Fatalf("PublishCatalogVersion cv-1: %v", err)
	}
	doneJob, _ := s.GetImportJob(ctx, job.ID)
	if doneJob.Status != models.ImportJobCompleted || doneJob.CatalogVersionID != "cv-1" || doneJob.ProcessedSongs != 10 {
		t.Fatalf("expected job completed atomically, got %+v", doneJob)
	}

	// Publish conflicts: ExpectedBaseVersion mismatch & JobGeneration superseded.
	cv2 := &models.CatalogVersion{ID: "cv-2", SchemaVersion: 1, LeaseToken: "tok-2"}
	if err := s.CreateCatalogVersion(ctx, cv2); err != nil {
		t.Fatalf("CreateCatalogVersion cv2: %v", err)
	}
	if _, err := s.MarkCatalogVersionReady(ctx, "cv-2", "catalogs/cv-2", 20, "tok-2"); err != nil {
		t.Fatalf("MarkCatalogVersionReady cv2: %v", err)
	}
	if err := s.PublishCatalogVersion(ctx, sb.ID, "cv-2", store.PublicationOptions{
		ExpectedBaseVersion: &emptyBase,
		JobGeneration:       3,
	}); !errors.Is(err, store.ErrPublicationBaseConflict) {
		t.Fatalf("expected ErrPublicationBaseConflict, got %v", err)
	}
	baseCV1 := "cv-1"
	if err := s.PublishCatalogVersion(ctx, sb.ID, "cv-2", store.PublicationOptions{
		ExpectedBaseVersion: &baseCV1,
		JobGeneration:       1,
	}); !errors.Is(err, store.ErrPublicationSuperseded) {
		t.Fatalf("expected ErrPublicationSuperseded, got %v", err)
	}

	// Reference checks, ON DELETE RESTRICT, Reservation, Release, and Purge.
	ref, err := s.IsCatalogVersionReferenced(ctx, "cv-1")
	if err != nil || !ref {
		t.Fatalf("expected cv-1 referenced, got ref=%v err=%v", ref, err)
	}
	refExcl, err := s.IsCatalogVersionReferenced(ctx, "cv-1", sb.ID)
	if err != nil || refExcl {
		t.Fatalf("expected cv-1 unreferenced when excluding %s, got ref=%v err=%v", sb.ID, refExcl, err)
	}
	if err := s.DeleteCatalogVersion(ctx, "cv-1"); !errors.Is(err, store.ErrCatalogVersionInUse) {
		t.Fatalf("expected ErrCatalogVersionInUse (ON DELETE RESTRICT), got %v", err)
	}
	if err := s.PurgeCatalogVersion(ctx, "cv-1", nil); !errors.Is(err, store.ErrCatalogVersionInUse) {
		t.Fatalf("expected ErrCatalogVersionInUse on PurgeCatalogVersion, got %v", err)
	}
	cv1After, _ := s.GetCatalogVersion(ctx, "cv-1")
	if cv1After.Status != models.CatalogVersionReady {
		t.Fatalf("expected cv-1 released back to ready, got %s", cv1After.Status)
	}

	// Reserve & Release round-trip + Purge unreferenced cv-2.
	resv, err := s.ReserveCatalogVersionForDeletion(ctx, "cv-2", time.Minute)
	if err != nil || resv.Status != models.CatalogVersionDeleting {
		t.Fatalf("ReserveCatalogVersionForDeletion: %+v err=%v", resv, err)
	}
	if err := s.ReleaseCatalogVersionReservation(ctx, "cv-2", resv.LeaseToken); err != nil {
		t.Fatalf("ReleaseCatalogVersionReservation: %v", err)
	}
	var purgedPrefix string
	if err := s.PurgeCatalogVersion(ctx, "cv-2", func(prefix string) error {
		purgedPrefix = prefix
		return nil
	}); err != nil {
		t.Fatalf("PurgeCatalogVersion cv-2: %v", err)
	}
	if purgedPrefix != "catalogs/cv-2" {
		t.Fatalf("expected purgedPrefix catalogs/cv-2, got %q", purgedPrefix)
	}
	if _, err := s.GetCatalogVersion(ctx, "cv-2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected purged cv-2 ErrNotExist, got %v", err)
	}
}

func TestCatalogChangesAndEditActorFence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "chowner@example.com", true, true)
	collab := createTestUser(t, s, "chedit@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)

	change := &models.CatalogChange{
		SongbookID: sb.ID,
		SongID:     "s-1",
		Song:       &models.Song{ID: "s-1", Artist: "Queen", Title: "Killer Queen", IsAvailable: true},
	}

	// Collaborator without grant rejected.
	collabCtx := store.WithSongbookEditActor(ctx, collab.ID)
	if err := s.PutCatalogChange(collabCtx, change); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied without grant, got %v", err)
	}

	// Grant host-only (can_edit_catalog=false) still rejected.
	grantCollaborator(t, s, sb, collab, false, true)
	if err := s.PutCatalogChange(collabCtx, change); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied with can_edit_catalog=false, got %v", err)
	}

	// Grant edit -> succeeds.
	if err := s.UpdateSongbookGrant(ctx, &models.SongbookGrant{
		SongbookID: sb.ID, UserID: collab.ID, CanEditCatalog: true, CanHostShows: true,
	}); err != nil {
		t.Fatalf("UpdateSongbookGrant: %v", err)
	}
	if err := s.PutCatalogChange(collabCtx, change); err != nil {
		t.Fatalf("PutCatalogChange with edit grant: %v", err)
	}

	changes, err := s.ListCatalogChanges(ctx, sb.ID)
	if err != nil || len(changes) != 1 || changes[0].Song.Title != "Killer Queen" {
		t.Fatalf("ListCatalogChanges: %+v err=%v", changes, err)
	}

	// Mismatched updatedAt preserves change; matching updatedAt deletes it.
	if err := s.DeleteCatalogChange(collabCtx, sb.ID, "s-1", change.UpdatedAt.Add(-time.Second)); err != nil {
		t.Fatalf("DeleteCatalogChange mismatch ts: %v", err)
	}
	changes, _ = s.ListCatalogChanges(ctx, sb.ID)
	if len(changes) != 1 {
		t.Fatalf("expected change preserved when updatedAt mismatches, got %d", len(changes))
	}
	if err := s.DeleteCatalogChange(collabCtx, sb.ID, "s-1", change.UpdatedAt); err != nil {
		t.Fatalf("DeleteCatalogChange matching ts: %v", err)
	}
	changes, _ = s.ListCatalogChanges(ctx, sb.ID)
	if len(changes) != 0 {
		t.Fatalf("expected change deleted, got %d", len(changes))
	}

	// Deleting songbook blocks PutCatalogChange.
	if _, err := s.BeginSongbookDeletion(ctx, sb.ID); err != nil {
		t.Fatalf("BeginSongbookDeletion: %v", err)
	}
	if err := s.PutCatalogChange(collabCtx, change); !errors.Is(err, store.ErrSongbookDeleting) {
		t.Fatalf("expected ErrSongbookDeleting, got %v", err)
	}
}

func TestImportJobsAndDispatchProtocol(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "imp@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)

	t1 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	j1 := &models.ImportJob{ID: "job-1", SongbookID: sb.ID, ManifestKey: "m1", CreatedAt: t1}
	j2 := &models.ImportJob{ID: "job-2", SongbookID: sb.ID, ManifestKey: "m2", CreatedAt: t1.Add(time.Minute)}
	if err := s.CreateImportJob(ctx, j1); err != nil {
		t.Fatalf("CreateImportJob j1: %v", err)
	}
	if err := s.CreateImportJob(ctx, j2); err != nil {
		t.Fatalf("CreateImportJob j2: %v", err)
	}

	latest, err := s.LatestImportJob(ctx, sb.ID)
	if err != nil || latest.ID != j2.ID {
		t.Fatalf("LatestImportJob: %+v err=%v", latest, err)
	}

	// Generation assignment converges on first positive assignment; UpdateImportJob rejects mismatch.
	genA, err := s.AssignImportJobGeneration(ctx, j1.ID, 5)
	if err != nil || genA != 5 {
		t.Fatalf("AssignImportJobGeneration #1: gen=%d err=%v", genA, err)
	}
	genB, err := s.AssignImportJobGeneration(ctx, j1.ID, 9)
	if err != nil || genB != 5 {
		t.Fatalf("AssignImportJobGeneration #2 expected converge on 5, got %d err=%v", genB, err)
	}
	if err := s.UpdateImportJob(ctx, &models.ImportJob{ID: j1.ID, Generation: 9}); !errors.Is(err, store.ErrJobGenerationConflict) {
		t.Fatalf("expected ErrJobGenerationConflict, got %v", err)
	}

	// Dispatch protocol.
	if err := s.MarkImportJobDispatchReady(ctx, j2.ID); err != nil {
		t.Fatalf("MarkImportJobDispatchReady: %v", err)
	}
	dispatchable, err := s.ListDispatchableImportJobs(ctx, 2)
	if err != nil || len(dispatchable) != 1 || dispatchable[0].ID != j2.ID {
		t.Fatalf("ListDispatchableImportJobs: %+v err=%v", dispatchable, err)
	}

	_, tok1, err := s.ClaimImportJobDispatch(ctx, j2.ID, time.Minute)
	if err != nil || tok1 == "" {
		t.Fatalf("ClaimImportJobDispatch #1: tok=%q err=%v", tok1, err)
	}
	if _, _, err := s.ClaimImportJobDispatch(ctx, j2.ID, time.Minute); !errors.Is(err, store.ErrImportJobClaimConflict) {
		t.Fatalf("expected ErrImportJobClaimConflict while lease active, got %v", err)
	}
	if err := s.RecordDispatchError(ctx, j2.ID, "wrong-tok", "boom"); !errors.Is(err, store.ErrImportJobStaleClaim) {
		t.Fatalf("expected ErrImportJobStaleClaim, got %v", err)
	}
	if err := s.RecordDispatchError(ctx, j2.ID, tok1, "sqs timeout"); err != nil {
		t.Fatalf("RecordDispatchError: %v", err)
	}

	_, tok2, err := s.ClaimImportJobDispatch(ctx, j2.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimImportJobDispatch #2: %v", err)
	}
	// With 2 attempts and maxAttempts=2, releasing claim puts job in exhausted list.
	if err := s.RecordDispatchError(ctx, j2.ID, tok2, "sqs timeout 2"); err != nil {
		t.Fatalf("RecordDispatchError #2: %v", err)
	}
	exhausted, err := s.ListExhaustedImportJobs(ctx, 2)
	if err != nil || len(exhausted) != 1 || exhausted[0].ID != j2.ID {
		t.Fatalf("ListExhaustedImportJobs: %+v err=%v", exhausted, err)
	}

	_, tok3, err := s.ClaimImportJobDispatch(ctx, j2.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimImportJobDispatch #3: %v", err)
	}
	if err := s.ConfirmImportJobDispatch(ctx, j2.ID, "bad-tok"); !errors.Is(err, store.ErrImportJobStaleClaim) {
		t.Fatalf("expected ErrImportJobStaleClaim on Confirm, got %v", err)
	}
	if err := s.ConfirmImportJobDispatch(ctx, j2.ID, tok3); err != nil {
		t.Fatalf("ConfirmImportJobDispatch: %v", err)
	}

	// Worker processing & failure.
	proc, err := s.ClaimImportJobProcessing(ctx, j2.ID)
	if err != nil || proc.Status != models.ImportJobProcessing {
		t.Fatalf("ClaimImportJobProcessing: %+v err=%v", proc, err)
	}
	if err := s.UpdateImportJobProgress(ctx, j2.ID, 42); err != nil {
		t.Fatalf("UpdateImportJobProgress: %v", err)
	}
	if err := s.FailImportJob(ctx, j2.ID, "worker crashed"); err != nil {
		t.Fatalf("FailImportJob: %v", err)
	}
	failedJob, _ := s.GetImportJob(ctx, j2.ID)
	if failedJob.Status != models.ImportJobFailed || failedJob.ProcessedSongs != 42 {
		t.Fatalf("unexpected failedJob: %+v", failedJob)
	}
}

func TestSongbookGrantsAndInvitations(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "invowner@example.com", true, true)
	collab := createTestUser(t, s, "Collab@Example.com", true, true)
	other := createTestUser(t, s, "other@example.com", false, false)
	sb := createTestSongbook(t, s, owner.ID, "", false)

	// Partial unique index prevents duplicate pending invite without replaceIDs.
	tok1 := sha256Hex("inv-tok-1")
	inv1 := &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID,
		RecipientEmail: "collab@example.com", CanEditCatalog: true, CanHostShows: false,
		TokenHash: tok1, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, inv1, nil); err != nil {
		t.Fatalf("CreateSongbookInvitation #1: %v", err)
	}
	tokDup := sha256Hex("inv-tok-dup")
	if err := s.CreateSongbookInvitation(ctx, &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID,
		RecipientEmail: "COLLAB@example.com", TokenHash: tokDup, ExpiresAt: time.Now().Add(time.Hour),
	}, nil); !errors.Is(err, store.ErrInvitationConflict) {
		t.Fatalf("expected ErrInvitationConflict from partial unique index, got %v", err)
	}

	// Replace atomically supersedes inv1.
	tok2 := sha256Hex("inv-tok-2")
	inv2 := &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID,
		RecipientEmail: "collab@example.com", CanEditCatalog: true, CanHostShows: true,
		TokenHash: tok2, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, inv2, []string{inv1.ID}); err != nil {
		t.Fatalf("CreateSongbookInvitation replace: %v", err)
	}
	oldInv, _ := s.GetSongbookInvitation(ctx, inv1.ID)
	if oldInv.Status != models.InvitationStatusReplaced {
		t.Fatalf("expected inv1 replaced, got %s", oldInv.Status)
	}

	// Rotate token & delivery tracking.
	tok3 := sha256Hex("inv-tok-3")
	rotated, err := s.RotateSongbookInvitationToken(ctx, sb.ID, inv2.ID, tok3, time.Now().Add(2*time.Hour))
	if err != nil || rotated.TokenHash != tok3 {
		t.Fatalf("RotateSongbookInvitationToken: %+v err=%v", rotated, err)
	}
	if err := s.RecordSongbookInvitationDelivery(ctx, inv2.ID, tok2, nil, "old attempt"); !errors.Is(err, store.ErrInvitationDeliverySuperseded) {
		t.Fatalf("expected ErrInvitationDeliverySuperseded, got %v", err)
	}
	sentAt := time.Now().UTC()
	if err := s.RecordSongbookInvitationDelivery(ctx, inv2.ID, tok3, &sentAt, ""); err != nil {
		t.Fatalf("RecordSongbookInvitationDelivery: %v", err)
	}

	// Acceptance validation errors: email mismatch, expired, valid accept.
	if _, _, err := s.AcceptSongbookInvitation(ctx, tok3, other.ID, other.Email, time.Now()); !errors.Is(err, store.ErrInvitationEmailMismatch) {
		t.Fatalf("expected ErrInvitationEmailMismatch, got %v", err)
	}
	if _, _, err := s.AcceptSongbookInvitation(ctx, tok3, collab.ID, collab.Email, time.Now().Add(3*time.Hour)); !errors.Is(err, store.ErrInvitationExpired) {
		t.Fatalf("expected ErrInvitationExpired, got %v", err)
	}
	grant, accepted, err := s.AcceptSongbookInvitation(ctx, tok3, collab.ID, collab.Email, time.Now())
	if err != nil || !grant.CanEditCatalog || !grant.CanHostShows || accepted.Status != models.InvitationStatusAccepted {
		t.Fatalf("AcceptSongbookInvitation: grant=%+v inv=%+v err=%v", grant, accepted, err)
	}

	// Re-inviting existing collaborator rejected.
	if err := s.CreateSongbookInvitation(ctx, &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID,
		RecipientEmail: collab.Email, TokenHash: sha256Hex("inv-tok-4"), ExpiresAt: time.Now().Add(time.Hour),
	}, nil); !errors.Is(err, store.ErrAlreadyCollaborator) {
		t.Fatalf("expected ErrAlreadyCollaborator, got %v", err)
	}

	// Cancel pending invitation for other user.
	tokOther := sha256Hex("inv-tok-other")
	invOther := &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID,
		RecipientEmail: other.Email, TokenHash: tokOther, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, invOther, nil); err != nil {
		t.Fatalf("CreateSongbookInvitation other: %v", err)
	}
	if err := s.CancelSongbookInvitation(ctx, sb.ID, invOther.ID); err != nil {
		t.Fatalf("CancelSongbookInvitation: %v", err)
	}
	if _, err := s.GetSongbookInvitationByTokenHash(ctx, tokOther); !errors.Is(err, store.ErrInvitationNotFound) {
		t.Fatalf("expected canceled invitation token lookup ErrInvitationNotFound, got %v", err)
	}

	// Grant CRUD.
	loadedGrant, err := s.GetSongbookGrant(ctx, sb.ID, collab.ID)
	if err != nil || !loadedGrant.CanHostShows {
		t.Fatalf("GetSongbookGrant: %+v err=%v", loadedGrant, err)
	}
	loadedGrant.CanHostShows = false
	if err := s.UpdateSongbookGrant(ctx, loadedGrant); err != nil {
		t.Fatalf("UpdateSongbookGrant: %v", err)
	}
	if err := s.DeleteSongbookGrant(ctx, sb.ID, collab.ID); err != nil {
		t.Fatalf("DeleteSongbookGrant: %v", err)
	}
	if _, err := s.GetSongbookGrant(ctx, sb.ID, collab.ID); !errors.Is(err, store.ErrGrantNotFound) {
		t.Fatalf("expected ErrGrantNotFound after delete, got %v", err)
	}
}
