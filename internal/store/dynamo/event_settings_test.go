package dynamo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/event"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// The callback runs after service reads/planning and before the actual DynamoDB
// commit. The competing operation uses the base store, so there are no sleeps or
// process-local locks standing in for the persistence condition.
type eventCommitInterleave struct {
	*Store
	beforeUpdate     func() error
	beforeTransition func() error
}

func (s *eventCommitInterleave) UpdateEventWithVersion(ctx context.Context, ev *models.Event, bump bool) error {
	if s.beforeUpdate != nil {
		if err := s.beforeUpdate(); err != nil {
			return err
		}
	}
	return s.Store.UpdateEventWithVersion(ctx, ev, bump)
}
func (s *eventCommitInterleave) TransitionQueueRotation(ctx context.Context, ev *models.Event, requests []*models.SongRequest) error {
	if s.beforeTransition != nil {
		if err := s.beforeTransition(); err != nil {
			return err
		}
	}
	return s.Store.TransitionQueueRotation(ctx, ev, requests)
}
func settingsEvent(t *testing.T, s *Store) *models.Event {
	t.Helper()
	helperCreateUser(t, s, "settings-host")
	ev := &models.Event{ID: uuid.NewString(), OwnerID: "settings-host", Name: "Original", JoinCode: "SETT", SongbookID: uuid.NewString(), Status: models.EventStatusActive, RotationMode: models.RotationModeRoundRobin, QueueVersion: 1, CurrentRound: 1}
	if err := s.CreateEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	return ev
}
func TestEventSettingsRejectStaleServiceSnapshots(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	for _, competing := range []string{"pause", "end", "rotation", "key"} {
		t.Run(competing, func(t *testing.T) {
			ev := settingsEvent(t, s)
			winner := event.NewService(s, nil, s, 0)
			var savedWinner *models.Event
			interleave := &eventCommitInterleave{Store: s, beforeUpdate: func() error {
				var err error
				switch competing {
				case "pause":
					savedWinner, err = winner.PauseEvent(ctx, ev.ID)
				case "end":
					status := models.EventStatusEnded
					savedWinner, err = winner.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &status})
				case "rotation":
					mode := models.RotationModeFCFS
					savedWinner, err = winner.UpdateEvent(ctx, ev.ID, models.EventUpdate{RotationMode: &mode})
				case "key":
					savedWinner, err = winner.RotateOpenKJKey(ctx, ev.ID)
				}
				return err
			}}
			name := "Stale edit"
			result, err := event.NewService(interleave, nil, s, 0).UpdateEvent(ctx, ev.ID, models.EventUpdate{Name: &name})
			var conflict *store.QueueVersionConflictError
			if result != nil || !errors.As(err, &conflict) {
				t.Errorf("stale settings=(%+v,%v); want conflict", result, err)
			}
			actual, readErr := s.GetEvent(ctx, ev.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if actual.Name != ev.Name || actual.Status != savedWinner.Status || actual.RotationMode != savedWinner.RotationMode || actual.OpenKJKey != savedWinner.OpenKJKey || actual.TTL != savedWinner.TTL || actual.QueueVersion != savedWinner.QueueVersion {
				t.Fatalf("stale edit overwrote newer event: %+v; winner %+v", actual, savedWinner)
			}
			// The same explicit patch is safe after reloading; absent fields retain the winner.
			fresh, err := winner.UpdateEvent(ctx, ev.ID, models.EventUpdate{Name: &name})
			if err != nil || fresh.Name != name || fresh.Status != savedWinner.Status || fresh.OpenKJKey != savedWinner.OpenKJKey || fresh.RotationMode != savedWinner.RotationMode || fresh.TTL != savedWinner.TTL {
				t.Fatalf("fresh patch=(%+v,%v)", fresh, err)
			}
		})
	}
	t.Run("key rotation after pause", func(t *testing.T) {
		ev := settingsEvent(t, s)
		interleave := &eventCommitInterleave{Store: s, beforeUpdate: func() error { _, err := event.NewService(s, nil, s, 0).PauseEvent(ctx, ev.ID); return err }}
		_, err := event.NewService(interleave, nil, s, 0).RotateOpenKJKey(ctx, ev.ID)
		var conflict *store.QueueVersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("stale rotation error=%v", err)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Status != models.EventStatusPaused || actual.OpenKJKey != ev.OpenKJKey {
			t.Fatal("key rotation reverted pause or committed stale key")
		}
	})
	t.Run("rotation transaction after key rotation", func(t *testing.T) {
		ev := settingsEvent(t, s)
		req := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, SingerName: "Singer", Status: models.StatusPending, Round: 4, Rank: 900, CreatedAt: time.Now()}
		if err := s.CreateRequest(ctx, req); err != nil {
			t.Fatal(err)
		}
		var key string
		interleave := &eventCommitInterleave{Store: s, beforeTransition: func() error {
			winner, err := event.NewService(s, nil, s, 0).RotateOpenKJKey(ctx, ev.ID)
			if err == nil {
				key = winner.OpenKJKey
			}
			return err
		}}
		mode := models.RotationModeFCFS
		_, err := event.NewService(interleave, nil, s, 0).UpdateEvent(ctx, ev.ID, models.EventUpdate{RotationMode: &mode})
		var conflict *store.QueueVersionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("stale transition error=%v", err)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		actualRequest, err := s.GetRequest(ctx, req.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.OpenKJKey != key || actual.RotationMode != ev.RotationMode || actual.QueueVersion != ev.QueueVersion || actualRequest.Round != req.Round || actualRequest.Rank != req.Rank {
			t.Fatal("stale transition partially committed event or request")
		}
	})
}

func TestEventSingerWeightsAndRequestCompletedAtRoundTrip(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	svc := event.NewService(s, nil, s, 0)

	ev := settingsEvent(t, s)

	// 1. Persist normalized/clamped singer weights via UpdateEvent
	weights := map[string]int{"  Alice ": 12, "Bob": -15, "Zero": 0}
	updated, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{SingerWeights: &weights})
	if err != nil {
		t.Fatalf("UpdateEvent singer weights: %v", err)
	}
	if len(updated.SingerWeights) != 2 || updated.SingerWeights["alice"] != 10 || updated.SingerWeights["bob"] != -10 {
		t.Fatalf("updated.SingerWeights = %+v, want {alice:10, bob:-10}", updated.SingerWeights)
	}

	reloaded, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent after setting weights: %v", err)
	}
	if len(reloaded.SingerWeights) != 2 || reloaded.SingerWeights["alice"] != 10 || reloaded.SingerWeights["bob"] != -10 {
		t.Fatalf("reloaded.SingerWeights = %+v, want {alice:10, bob:-10}", reloaded.SingerWeights)
	}

	// 2. Clear singer weights with empty map
	empty := map[string]int{}
	if _, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{SingerWeights: &empty}); err != nil {
		t.Fatalf("UpdateEvent clear singer weights: %v", err)
	}
	cleared, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent after clearing weights: %v", err)
	}
	if len(cleared.SingerWeights) != 0 {
		t.Fatalf("expected empty SingerWeights after clear, got %+v", cleared.SingerWeights)
	}

	// 3. Round-trip CompletedAt on SongRequest
	completedAt := time.Date(2026, 11, 24, 21, 15, 0, 0, time.UTC)
	req := &models.SongRequest{
		ID:          uuid.NewString(),
		EventID:     ev.ID,
		SingerName:  "Alice",
		SongTitle:   "Respect",
		SongArtist:  "Aretha Franklin",
		Status:      models.StatusCompleted,
		Rank:        1,
		CreatedAt:   completedAt.Add(-5 * time.Minute),
		CompletedAt: &completedAt,
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest with CompletedAt: %v", err)
	}
	loadedReq, err := s.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if loadedReq.CompletedAt == nil || !loadedReq.CompletedAt.Equal(completedAt) {
		t.Fatalf("loadedReq.CompletedAt = %v, want %v", loadedReq.CompletedAt, completedAt)
	}
}
