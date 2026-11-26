package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func grantCollaborator(t *testing.T, s *Store, sb *models.Songbook, collab *models.User, canEdit, canHost bool) {
	t.Helper()
	ctx := context.Background()
	tokHash := sha256Hex("invite-" + uuid.NewString())
	inv := &models.SongbookInvitation{
		ID:             uuid.NewString(),
		SongbookID:     sb.ID,
		OwnerID:        sb.OwnerID,
		RecipientEmail: collab.Email,
		CanEditCatalog: canEdit,
		CanHostShows:   canHost,
		TokenHash:      tokHash,
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, inv, nil); err != nil {
		t.Fatalf("CreateSongbookInvitation: %v", err)
	}
	if _, _, err := s.AcceptSongbookInvitation(ctx, tokHash, collab.ID, collab.Email, time.Now()); err != nil {
		t.Fatalf("AcceptSongbookInvitation: %v", err)
	}
}

func TestEventsAndHostFencing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "host@example.com", true, true)
	collab := createTestUser(t, s, "collab@example.com", true, true)
	stranger := createTestUser(t, s, "stranger@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "main-book", true)

	// Host fence checks on CreateEvent.
	ownerCtx := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
		SongbookID: sb.ID, ActorID: owner.ID, IsOwner: true,
	})
	ev := createTestEventWithCtx(t, ownerCtx, s, owner.ID, sb.ID, "mic42")

	// Case-insensitive join code lookup.
	byCode, err := s.GetEventByJoinCode(ctx, "  MiC42 ")
	if err != nil || byCode.ID != ev.ID {
		t.Fatalf("GetEventByJoinCode failed: %+v err=%v", byCode, err)
	}
	byID, err := s.GetEvent(ctx, ev.ID)
	if err != nil || byID.QueueVersion != 1 {
		t.Fatalf("GetEvent failed: %+v err=%v", byID, err)
	}

	// Stranger / non-owner rejected.
	badOwnerCtx := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
		SongbookID: sb.ID, ActorID: stranger.ID, IsOwner: true,
	})
	if err := s.CreateEvent(badOwnerCtx, &models.Event{
		OwnerID: stranger.ID, SongbookID: sb.ID, Name: "Bad", JoinCode: "BAD1",
	}); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for wrong owner fence, got %v", err)
	}

	// Collaborator without host grant rejected; with host grant allowed.
	grantCollaborator(t, s, sb, collab, true, false)
	collabCtx := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
		SongbookID: sb.ID, ActorID: collab.ID, IsOwner: false,
	})
	if err := s.CreateEvent(collabCtx, &models.Event{
		OwnerID: collab.ID, SongbookID: sb.ID, Name: "Collab Denied", JoinCode: "CDEN",
	}); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when can_host_shows=false, got %v", err)
	}

	if err := s.UpdateSongbookGrant(ctx, &models.SongbookGrant{
		SongbookID: sb.ID, UserID: collab.ID, CanEditCatalog: true, CanHostShows: true,
	}); err != nil {
		t.Fatalf("UpdateSongbookGrant: %v", err)
	}
	ev2 := createTestEventWithCtx(t, collabCtx, s, collab.ID, sb.ID, "COL1")

	ownerEvents, err := s.ListEventsByOwner(ctx, owner.ID)
	if err != nil || len(ownerEvents) != 1 {
		t.Fatalf("ListEventsByOwner: len=%d err=%v", len(ownerEvents), err)
	}
	sbEvents, err := s.ListEventsBySongbook(ctx, sb.ID)
	if err != nil || len(sbEvents) != 2 {
		t.Fatalf("ListEventsBySongbook: len=%d err=%v", len(sbEvents), err)
	}

	// UpdateEvent / UpdateEventWithVersion + version conflicts.
	ev.Announcement = "Welcome!"
	if err := s.UpdateEvent(ctx, ev); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if ev.MetadataVersion != 1 || ev.QueueVersion != 1 {
		t.Fatalf("expected MV=1 QV=1, got MV=%d QV=%d", ev.MetadataVersion, ev.QueueVersion)
	}
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatalf("UpdateEventWithVersion: %v", err)
	}
	if ev.MetadataVersion != 2 || ev.QueueVersion != 2 {
		t.Fatalf("expected MV=2 QV=2, got MV=%d QV=%d", ev.MetadataVersion, ev.QueueVersion)
	}

	staleQV := *ev
	staleQV.QueueVersion = 1
	var qvConflict *store.QueueVersionConflictError
	if err := s.UpdateEvent(ctx, &staleQV); !errors.As(err, &qvConflict) || qvConflict.Current != 2 {
		t.Fatalf("expected QueueVersionConflictError(Current=2), got %v", err)
	}

	staleMV := *ev
	staleMV.MetadataVersion = 0
	if err := s.UpdateEvent(ctx, &staleMV); !errors.As(err, &qvConflict) {
		t.Fatalf("expected QueueVersionConflictError on stale metadata version, got %v", err)
	}

	// Deleting songbook blocks fenced event creation.
	if _, err := s.BeginSongbookDeletion(ctx, sb.ID); err != nil {
		t.Fatalf("BeginSongbookDeletion: %v", err)
	}
	if err := s.CreateEvent(collabCtx, &models.Event{
		OwnerID: collab.ID, SongbookID: sb.ID, Name: "Blocked", JoinCode: "BLK1",
	}); !errors.Is(err, store.ErrSongbookDeleting) {
		t.Fatalf("expected ErrSongbookDeleting, got %v", err)
	}
	_ = ev2
}

func createTestEventWithCtx(t *testing.T, ctx context.Context, s *Store, ownerID, sbID, joinCode string) *models.Event {
	t.Helper()
	ev := &models.Event{
		ID:           uuid.NewString(),
		OwnerID:      ownerID,
		SongbookID:   sbID,
		VenueName:    "Venue",
		Name:         "Show " + joinCode,
		JoinCode:     joinCode,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	return ev
}

func TestRequestsAndVersionedMutations(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "reqhost@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)
	ev := createTestEvent(t, s, owner.ID, sb.ID, "REQ1")

	r1 := makeTestRequest(ev.ID, sb.ID, "Alice", "Song A", 1, computeRequestRank(1, 0))
	if err := s.CreateRequestAtVersion(ctx, r1, 1, nil, 1); err != nil {
		t.Fatalf("CreateRequestAtVersion r1: %v", err)
	}
	// Singer active limit enforced (case & whitespace normalized).
	rDup := makeTestRequest(ev.ID, sb.ID, "  alice ", "Song A2", 1, computeRequestRank(1, 1))
	if err := s.CreateRequestAtVersion(ctx, rDup, 1, nil, 2); !errors.Is(err, store.ErrSingerLimitExceeded) {
		t.Fatalf("expected ErrSingerLimitExceeded, got %v", err)
	}
	// Stale version conflict.
	rStale := makeTestRequest(ev.ID, sb.ID, "Bob", "Song B", 1, computeRequestRank(1, 1))
	var qvErr *store.QueueVersionConflictError
	if err := s.CreateRequestAtVersion(ctx, rStale, 1, nil, 1); !errors.As(err, &qvErr) || qvErr.Current != 2 {
		t.Fatalf("expected QueueVersionConflictError(Current=2), got %v", err)
	}

	// Atomic admission + reorder places r2 ahead of r1.
	r2 := makeTestRequest(ev.ID, sb.ID, "Bob", "Song B", 1, computeRequestRank(1, 0))
	if err := s.CreateRequestAtVersion(ctx, r2, 1, []string{r2.ID, r1.ID}, 2); err != nil {
		t.Fatalf("CreateRequestAtVersion r2 with reorder: %v", err)
	}
	list, err := s.ListRequestsByEvent(ctx, ev.ID)
	if err != nil || len(list) != 2 || list[0].ID != r2.ID || list[1].ID != r1.ID {
		t.Fatalf("unexpected request order after admission reorder: %+v err=%v", list, err)
	}

	// UpdateRequestWithQueueVersion (success + conflict).
	r2.Status = models.StatusUpNext
	if err := s.UpdateRequestWithQueueVersion(ctx, r2, 3); err != nil {
		t.Fatalf("UpdateRequestWithQueueVersion: %v", err)
	}
	if err := s.UpdateRequestWithQueueVersion(ctx, r2, 3); !errors.As(err, &qvErr) {
		t.Fatalf("expected QueueVersionConflictError on stale update, got %v", err)
	}

	// ReorderRequestsWithQueueVersion (success + conflict).
	if err := s.ReorderRequestsWithQueueVersion(ctx, ev.ID, []string{r1.ID, r2.ID}, 4); err != nil {
		t.Fatalf("ReorderRequestsWithQueueVersion: %v", err)
	}
	if err := s.ReorderRequestsWithQueueVersion(ctx, ev.ID, []string{r2.ID, r1.ID}, 4); !errors.As(err, &qvErr) {
		t.Fatalf("expected QueueVersionConflictError on stale reorder, got %v", err)
	}

	// UpdateRequestAndReorderWithQueueVersion.
	r1.Status = models.StatusUpNext
	if err := s.UpdateRequestAndReorderWithQueueVersion(ctx, r1, []string{r2.ID, r1.ID}, 5); err != nil {
		t.Fatalf("UpdateRequestAndReorderWithQueueVersion: %v", err)
	}
	list, _ = s.ListRequestsByEvent(ctx, ev.ID)
	if list[0].ID != r2.ID || list[1].Status != models.StatusUpNext {
		t.Fatalf("unexpected state after UpdateRequestAndReorderWithQueueVersion: %+v", list)
	}

	// DeleteEventRequestAtomic (wrong event rejected, right event deletes & bumps version).
	if err := s.DeleteEventRequestAtomic(ctx, "wrong-event", r1.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for wrong event, got %v", err)
	}
	if err := s.DeleteEventRequestAtomic(ctx, ev.ID, r1.ID); err != nil {
		t.Fatalf("DeleteEventRequestAtomic: %v", err)
	}

	// Non-active event rejects CreateRequestAtVersion with ErrEventNotActive; TTL applied when ended.
	latestEv, _ := s.GetEvent(ctx, ev.ID)
	ttl := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	// Ignored while event is active.
	if err := s.SetRequestsTTLByEvent(ctx, ev.ID, ttl); err != nil {
		t.Fatalf("SetRequestsTTLByEvent active: %v", err)
	}
	r2Check, _ := s.GetRequest(ctx, r2.ID)
	if r2Check.TTL != 0 {
		t.Fatalf("expected TTL ignored while active, got %d", r2Check.TTL)
	}

	latestEv.Status = models.EventStatusEnded
	if err := s.UpdateEvent(ctx, latestEv); err != nil {
		t.Fatalf("UpdateEvent ended: %v", err)
	}
	r3 := makeTestRequest(ev.ID, sb.ID, "Carol", "Song C", 1, 1)
	if err := s.CreateRequestAtVersion(ctx, r3, 0, nil, latestEv.QueueVersion); !errors.Is(err, store.ErrEventNotActive) {
		t.Fatalf("expected ErrEventNotActive, got %v", err)
	}
	if err := s.SetRequestsTTLByEvent(ctx, ev.ID, ttl); err != nil {
		t.Fatalf("SetRequestsTTLByEvent ended: %v", err)
	}
	r2Check, _ = s.GetRequest(ctx, r2.ID)
	if r2Check.TTL != ttl.Unix() {
		t.Fatalf("expected TTL %d, got %d", ttl.Unix(), r2Check.TTL)
	}
	if err := s.SetRequestsTTLByEvent(ctx, ev.ID, time.Time{}); err != nil {
		t.Fatalf("SetRequestsTTLByEvent clear: %v", err)
	}
	r2Check, _ = s.GetRequest(ctx, r2.ID)
	if r2Check.TTL != 0 {
		t.Fatalf("expected TTL cleared, got %d", r2Check.TTL)
	}
}

func TestOver100RequestsAtomicReorderAndRotation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "bigqueue@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)
	ev := createTestEvent(t, s, owner.ID, sb.ID, "BIG1")

	const count = 110
	ids := make([]string, count)
	reqs := make([]*models.SongRequest, count)
	for i := 0; i < count; i++ {
		r := makeTestRequest(ev.ID, sb.ID, fmt.Sprintf("Singer%03d", i), fmt.Sprintf("Song %03d", i), 1, computeRequestRank(1, i))
		if err := s.CreateRequestAtVersion(ctx, r, 0, nil, int64(i+1)); err != nil {
			t.Fatalf("CreateRequestAtVersion #%d: %v", i, err)
		}
		ids[i] = r.ID
		reqs[i] = r
	}

	reversed := make([]string, count)
	for i := 0; i < count; i++ {
		reversed[i] = ids[count-1-i]
	}

	expectedVer := int64(count + 1)
	if err := s.ReorderRequestsWithQueueVersion(ctx, ev.ID, reversed, expectedVer); err != nil {
		t.Fatalf("ReorderRequestsWithQueueVersion(110 items): %v", err)
	}

	listed, err := s.ListRequestsByEvent(ctx, ev.ID)
	if err != nil || len(listed) != count {
		t.Fatalf("ListRequestsByEvent: len=%d err=%v", len(listed), err)
	}
	for i := 0; i < count; i++ {
		if listed[i].ID != reversed[i] {
			t.Fatalf("order mismatch at index %d: got %s want %s", i, listed[i].ID, reversed[i])
		}
		listed[i].Round = 2
		listed[i].Rank = computeRequestRank(2, i)
	}

	loadedEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil || loadedEv.QueueVersion != expectedVer+1 {
		t.Fatalf("expected QueueVersion %d, got %+v err=%v", expectedVer+1, loadedEv, err)
	}
	loadedEv.RotationMode = models.RotationModeFCFS
	loadedEv.CurrentRound = 2
	if err := s.TransitionQueueRotation(ctx, loadedEv, listed); err != nil {
		t.Fatalf("TransitionQueueRotation(110 items): %v", err)
	}
	if loadedEv.QueueVersion != expectedVer+2 || loadedEv.MetadataVersion != 1 {
		t.Fatalf("unexpected versions after TransitionQueueRotation: QV=%d MV=%d", loadedEv.QueueVersion, loadedEv.MetadataVersion)
	}
	afterRot, err := s.ListRequestsByEvent(ctx, ev.ID)
	if err != nil || len(afterRot) != count || afterRot[0].Round != 2 || afterRot[count-1].Round != 2 {
		t.Fatalf("unexpected requests after TransitionQueueRotation: len=%d err=%v", len(afterRot), err)
	}
}

func TestIdempotencyMutationsAndConflicts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "idem@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)
	ev := createTestEvent(t, s, owner.ID, sb.ID, "IDM1")

	r1 := makeTestRequest(ev.ID, sb.ID, "Alice", "Song 1", 1, computeRequestRank(1, 0))
	r2 := makeTestRequest(ev.ID, sb.ID, "Bob", "Song 2", 1, computeRequestRank(1, 1))
	if err := s.CreateRequestAtVersion(ctx, r1, 0, nil, 1); err != nil {
		t.Fatalf("create r1: %v", err)
	}
	if err := s.CreateRequestAtVersion(ctx, r2, 0, nil, 2); err != nil {
		t.Fatalf("create r2: %v", err)
	}

	// 1. UpdateRequestWithIdempotency
	v3 := int64(3)
	r1.Notes = "key +1"
	res1, err := s.UpdateRequestWithIdempotency(ctx, r1, &v3, "idem-upd-1", owner.ID, "PATCH /requests", "hash-a")
	if err != nil || res1.Replay || res1.StatusCode != 200 || res1.QueueVersion != 4 {
		t.Fatalf("UpdateRequestWithIdempotency first: %+v err=%v", res1, err)
	}
	replay1, err := s.UpdateRequestWithIdempotency(ctx, r1, &v3, "idem-upd-1", owner.ID, "PATCH /requests", "hash-a")
	if err != nil || !replay1.Replay || replay1.QueueVersion != 4 || string(replay1.Body) != string(res1.Body) {
		t.Fatalf("UpdateRequestWithIdempotency replay: %+v err=%v", replay1, err)
	}
	var conflictErr *store.IdempotencyConflictError
	if _, err := s.UpdateRequestWithIdempotency(ctx, r1, &v3, "idem-upd-1", owner.ID, "PATCH /requests", "hash-different"); !errors.As(err, &conflictErr) {
		t.Fatalf("expected IdempotencyConflictError for payload mismatch, got %v", err)
	}
	if _, err := s.UpdateRequestWithIdempotency(ctx, r1, &v3, "idem-upd-1", "other-owner", "PATCH /requests", "hash-a"); !errors.As(err, &conflictErr) {
		t.Fatalf("expected IdempotencyConflictError for owner mismatch, got %v", err)
	}

	// 2. ReorderRequestsWithIdempotency
	v4 := int64(4)
	reord1, err := s.ReorderRequestsWithIdempotency(ctx, ev.ID, []string{r2.ID, r1.ID}, &v4, "idem-reord-1", owner.ID, "PUT /reorder", "hash-r")
	if err != nil || reord1.Replay || reord1.QueueVersion != 5 {
		t.Fatalf("ReorderRequestsWithIdempotency first: %+v err=%v", reord1, err)
	}
	reordReplay, err := s.ReorderRequestsWithIdempotency(ctx, ev.ID, []string{r2.ID, r1.ID}, &v4, "idem-reord-1", owner.ID, "PUT /reorder", "hash-r")
	if err != nil || !reordReplay.Replay || reordReplay.QueueVersion != 5 {
		t.Fatalf("ReorderRequestsWithIdempotency replay: %+v err=%v", reordReplay, err)
	}
	if _, err := s.ReorderRequestsWithIdempotency(ctx, ev.ID, []string{r1.ID, r2.ID}, &v4, "idem-reord-1", owner.ID, "OTHER /route", "hash-r"); !errors.As(err, &conflictErr) {
		t.Fatalf("expected IdempotencyConflictError for route mismatch, got %v", err)
	}

	// 3. UpdateRequestAndReorderWithIdempotency
	v5 := int64(5)
	r1.Status = models.StatusUpNext
	ur1, err := s.UpdateRequestAndReorderWithIdempotency(ctx, r1, []string{r1.ID, r2.ID}, &v5, "idem-ur-1", owner.ID, "PATCH /req-reorder", "hash-ur")
	if err != nil || ur1.Replay || ur1.QueueVersion != 6 {
		t.Fatalf("UpdateRequestAndReorderWithIdempotency first: %+v err=%v", ur1, err)
	}
	urReplay, err := s.UpdateRequestAndReorderWithIdempotency(ctx, r1, []string{r1.ID, r2.ID}, &v5, "idem-ur-1", owner.ID, "PATCH /req-reorder", "hash-ur")
	if err != nil || !urReplay.Replay || urReplay.QueueVersion != 6 {
		t.Fatalf("UpdateRequestAndReorderWithIdempotency replay: %+v err=%v", urReplay, err)
	}
	if _, err := s.UpdateRequestAndReorderWithIdempotency(ctx, r2, []string{r1.ID, r2.ID}, &v5, "idem-ur-1", owner.ID, "PATCH /req-reorder", "hash-ur"); !errors.As(err, &conflictErr) {
		t.Fatalf("expected IdempotencyConflictError for entity mismatch, got %v", err)
	}
}

func TestQueueRestartAndSongbookDetach(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	owner := createTestUser(t, s, "restart@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "", false)
	ev := createTestEvent(t, s, owner.ID, sb.ID, "RST1")

	r1 := makeTestRequest(ev.ID, sb.ID, "Alice", "Song 1", 1, 1)
	r1.Status = models.StatusPending
	if err := s.CreateRequestAtVersion(ctx, r1, 0, nil, 1); err != nil {
		t.Fatalf("CreateRequestAtVersion: %v", err)
	}

	ev, _ = s.GetEvent(ctx, ev.ID)
	if err := s.BeginQueueRestart(ctx, ev); err != nil {
		t.Fatalf("BeginQueueRestart: %v", err)
	}
	// Requests rejected while restarting.
	if err := s.CreateRequest(ctx, makeTestRequest(ev.ID, sb.ID, "Bob", "Song 2", 1, 2)); !errors.Is(err, store.ErrEventNotActive) {
		t.Fatalf("expected ErrEventNotActive during restart, got %v", err)
	}
	slots, err := s.ListRestartSlots(ctx, ev.ID)
	if err != nil || slots != nil {
		t.Fatalf("ListRestartSlots: %v err=%v", slots, err)
	}
	if err := s.DeleteRestartSlots(ctx, ev.ID, ev.QueueVersion+1, []string{"slot#1"}); err != nil {
		t.Fatalf("DeleteRestartSlots: %v", err)
	}
	if err := s.ArchiveRestartBatch(ctx, ev.ID, ev.QueueVersion+2, []*models.SongRequest{r1}); err != nil {
		t.Fatalf("ArchiveRestartBatch: %v", err)
	}
	archived, _ := s.GetRequest(ctx, r1.ID)
	if archived.Status != models.StatusCompleted || archived.CompletedAt == nil {
		t.Fatalf("expected archived request completed with timestamp, got %+v", archived)
	}
	if err := s.FinishQueueRestart(ctx, ev.ID, ev.QueueVersion+3); err != nil {
		t.Fatalf("FinishQueueRestart: %v", err)
	}
	reopened, _ := s.GetEvent(ctx, ev.ID)
	if reopened.Restarting || reopened.Status != models.EventStatusActive || reopened.EndedAt != nil {
		t.Fatalf("expected active reopened event, got %+v", reopened)
	}

	// Detach event from songbook.
	detached, err := s.DetachEventFromSongbook(ctx, ev.ID, sb.ID, "  Old Book  ", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetachEventFromSongbook: %v", err)
	}
	if detached.SongbookID != "" || detached.DetachedSongbookName != "Old Book" || detached.Status != models.EventStatusEnded || detached.TTL == 0 {
		t.Fatalf("unexpected detached event: %+v", detached)
	}
	reqAfterDetach, _ := s.GetRequest(ctx, r1.ID)
	if reqAfterDetach.TTL == 0 {
		t.Fatal("expected request retention TTL set upon detach")
	}
}
