package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func setupHP1PolicyFixture(t *testing.T, s *Store) (context.Context, *models.Event, *models.SongRequest) {
	t.Helper()
	ctx := context.Background()
	owner := createTestUser(t, s, "hp1-host@example.com", true, true)
	sb := createTestSongbook(t, s, owner.ID, "hp1-book", true)
	ownerCtx := store.WithSongbookHostFence(ctx, store.SongbookHostFence{
		SongbookID: sb.ID, ActorID: owner.ID, IsOwner: true,
	})

	ev := &models.Event{
		ID:                   uuid.NewString(),
		OwnerID:              owner.ID,
		SongbookID:           sb.ID,
		Name:                 "HP1 Policy Show",
		JoinCode:             "HP1A",
		Status:               models.EventStatusActive,
		RotationMode:         models.RotationModeRoundRobin,
		CurrentRound:         1,
		AutoAccept:           true,
		AllowKeyChange:       false,
		AllowWriteIns:        false,
		AllowSongReplacement: true,
		QueueVersion:         1,
	}
	if err := s.CreateEvent(ownerCtx, ev); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    ev.ID,
		SongbookID: sb.ID,
		SongID:     "song-orig",
		SongTitle:  "Original Song",
		SongArtist: "Original Artist",
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       models.RoundRankMultiplier + 5,
		Round:      1,
		SingerTurn: 1,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := s.CreateRequestAtVersion(ctx, req, 1, nil, ev.QueueVersion); err != nil {
		t.Fatalf("CreateRequestAtVersion: %v", err)
	}
	return ownerCtx, ev, req
}

func assertPolicyPersistedAndWriteInRejected(t *testing.T, s *Store, ev *models.Event, req *models.SongRequest) {
	t.Helper()
	ctx := context.Background()
	reloaded, err := s.GetEvent(ctx, ev.ID)
	if err != nil || !reloaded.AllowSongReplacement {
		t.Fatalf("expected AllowSongReplacement=true, got %+v err=%v", reloaded, err)
	}
	_, _, err = s.ReplaceRequestSongAtomic(ctx, ev.ID, req.ID, store.SongReplacementUpdate{
		IsWriteIn:  true,
		SongTitle:  "Custom Track",
		SongArtist: "Unknown",
	})
	if !errors.Is(err, store.ErrWriteInsDisabled) {
		t.Fatalf("expected ErrWriteInsDisabled, got %v", err)
	}
}

func assertPreCallReplacementPreservesPlacement(t *testing.T, s *Store, ev *models.Event, req *models.SongRequest) *models.SongRequest {
	t.Helper()
	updated, newVer, err := s.ReplaceRequestSongAtomic(context.Background(), ev.ID, req.ID, store.SongReplacementUpdate{
		SongID:     "song-new",
		SongTitle:  "Don't Stop Believin'",
		SongArtist: "Journey",
		KeyChange:  2,
		Notes:      "Swapped before my turn",
	})
	if err != nil {
		t.Fatalf("ReplaceRequestSongAtomic: %v", err)
	}
	if newVer != 3 || updated.SongTitle != "Don't Stop Believin'" || updated.SongID != "song-new" || updated.KeyChange != 0 {
		t.Fatalf("unexpected replacement result ver=%d req=%+v", newVer, updated)
	}
	if updated.Rank != req.Rank || updated.Round != req.Round || updated.SingerTurn != req.SingerTurn || updated.Status != models.StatusAccepted {
		t.Fatalf("replacement mutated queue placement or status: %+v", updated)
	}
	return updated
}

func assertDisabledPolicyAndUpNextFenced(t *testing.T, s *Store, ownerCtx context.Context, ev *models.Event, updated *models.SongRequest) {
	t.Helper()
	ctx := context.Background()
	curEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	curEv.AllowSongReplacement = false
	if err := s.UpdateEventWithVersion(ownerCtx, curEv, true); err != nil {
		t.Fatalf("UpdateEventWithVersion: %v", err)
	}
	if _, _, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, updated.ID, store.SongReplacementUpdate{
		SongID: "song-blocked", SongTitle: "Should Fail",
	}); !errors.Is(err, store.ErrSongReplacementDisabled) {
		t.Fatalf("expected ErrSongReplacementDisabled, got %v", err)
	}

	curEv.AllowSongReplacement = true
	if err := s.UpdateEventWithVersion(ownerCtx, curEv, true); err != nil {
		t.Fatalf("UpdateEventWithVersion re-enable: %v", err)
	}
	updated.Status = models.StatusUpNext
	if err := s.UpdateRequest(ctx, updated); err != nil {
		t.Fatalf("UpdateRequest up_next: %v", err)
	}
	if _, _, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, updated.ID, store.SongReplacementUpdate{
		SongID: "song-late", SongTitle: "Too Late",
	}); !errors.Is(err, store.ErrRequestAlreadyCalled) {
		t.Fatalf("expected ErrRequestAlreadyCalled after up_next, got %v", err)
	}

	afterFence, err := s.GetRequest(ctx, updated.ID)
	if err != nil || afterFence.SongTitle != "Don't Stop Believin'" {
		t.Fatalf("fenced replacement overwrote song: req=%+v err=%v", afterFence, err)
	}
}

func TestPostgresReplaceRequestSongAtomic_PreservesPositionAndFencesCallToStage(t *testing.T) {
	s := openTestStore(t)
	ownerCtx, ev, req := setupHP1PolicyFixture(t, s)

	assertPolicyPersistedAndWriteInRejected(t, s, ev, req)
	updated := assertPreCallReplacementPreservesPlacement(t, s, ev, req)
	assertDisabledPolicyAndUpNextFenced(t, s, ownerCtx, ev, updated)
}
