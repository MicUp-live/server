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

func TestPostgresReplaceRequestSongAtomic_PreservesPositionAndFencesCallToStage(t *testing.T) {
	s := openTestStore(t)
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

	reloaded, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if !reloaded.AllowSongReplacement {
		t.Fatal("expected AllowSongReplacement=true to persist")
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

	// 1. Write-in replacement rejected when allow_write_ins=false.
	if _, _, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, req.ID, store.SongReplacementUpdate{
		IsWriteIn:  true,
		SongTitle:  "Custom Track",
		SongArtist: "Unknown",
	}); !errors.Is(err, store.ErrWriteInsDisabled) {
		t.Fatalf("expected ErrWriteInsDisabled, got %v", err)
	}

	// 2. Valid pre-call replacement preserves rank/round/turn/status, zeros key_change when allow_key_change=false, bumps queue_version.
	updated, newVer, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, req.ID, store.SongReplacementUpdate{
		SongID:     "song-new",
		SongTitle:  "Don't Stop Believin'",
		SongArtist: "Journey",
		KeyChange:  2,
		Notes:      "Swapped before my turn",
	})
	if err != nil {
		t.Fatalf("ReplaceRequestSongAtomic: %v", err)
	}
	if newVer != 3 {
		t.Fatalf("expected queue_version 3, got %d", newVer)
	}
	if updated.SongTitle != "Don't Stop Believin'" || updated.SongID != "song-new" {
		t.Fatalf("unexpected replaced song fields: %+v", updated)
	}
	if updated.KeyChange != 0 {
		t.Fatalf("expected key_change zeroed when allow_key_change=false, got %d", updated.KeyChange)
	}
	if updated.Rank != req.Rank || updated.Round != req.Round || updated.SingerTurn != req.SingerTurn || updated.Status != models.StatusAccepted {
		t.Fatalf("replacement mutated queue position or status: got %+v, want rank=%d round=%d turn=%d", updated, req.Rank, req.Round, req.SingerTurn)
	}

	// 3. Disabling allow_song_replacement denies further singer replacements.
	curEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	curEv.AllowSongReplacement = false
	if err := s.UpdateEventWithVersion(ownerCtx, curEv, true); err != nil {
		t.Fatalf("UpdateEventWithVersion: %v", err)
	}
	if _, _, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, req.ID, store.SongReplacementUpdate{
		SongTitle: "Should Fail",
	}); !errors.Is(err, store.ErrSongReplacementDisabled) {
		t.Fatalf("expected ErrSongReplacementDisabled, got %v", err)
	}

	// Re-enable policy, then call singer to stage (up_next): must fence replacement with ErrRequestAlreadyCalled.
	curEv.AllowSongReplacement = true
	if err := s.UpdateEventWithVersion(ownerCtx, curEv, true); err != nil {
		t.Fatalf("UpdateEventWithVersion re-enable: %v", err)
	}
	updated.Status = models.StatusUpNext
	if err := s.UpdateRequest(ctx, updated); err != nil {
		t.Fatalf("UpdateRequest up_next: %v", err)
	}
	if _, _, err := s.ReplaceRequestSongAtomic(ctx, ev.ID, req.ID, store.SongReplacementUpdate{
		SongTitle: "Too Late",
	}); !errors.Is(err, store.ErrRequestAlreadyCalled) {
		t.Fatalf("expected ErrRequestAlreadyCalled after up_next, got %v", err)
	}

	// Verify song title remained unchanged.
	afterFence, err := s.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if afterFence.SongTitle != "Don't Stop Believin'" {
		t.Fatalf("fenced replacement overwrote song: got %q", afterFence.SongTitle)
	}
}
