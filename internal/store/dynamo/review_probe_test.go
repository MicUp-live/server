package dynamo

import (
	"context"
	"testing"
	"time"

	"micup.live/server/internal/event"
	"micup.live/server/internal/models"
)

func TestReviewRestartAfterNormalStatusUpdate(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: "review-restart", OwnerID: "host", JoinCode: "RVRE", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: "review-request", EventID: ev.ID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	req.Status = models.StatusAccepted
	if err := s.UpdateRequestWithQueueVersion(ctx, req, current.QueueVersion); err != nil {
		t.Fatal(err)
	}
	svc := event.NewService(s, nil, s, 0)
	if _, err := svc.RestartEvent(ctx, ev.ID, true); err != nil {
		t.Fatalf("restart after normal acceptance failed: %v", err)
	}
}

func TestReviewStaleRetentionAfterResume(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: "review-retention", OwnerID: "host", JoinCode: "RVTT", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: "review-retention-request", EventID: ev.ID, Status: models.StatusAccepted}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	// An end operation has committed its event update but has not applied TTLs.
	expires := time.Now().Add(24 * time.Hour)
	ev.Status = models.EventStatusEnded
	ev.TTL = expires.Unix()
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatal(err)
	}
	// A newer resume completes before that old operation continues.
	if _, err := event.NewService(s, nil, s, 0).ResumeEvent(ctx, ev.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestsTTLByEvent(ctx, ev.ID, expires); err != nil {
		return
	}
	got, err := s.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TTL != 0 {
		t.Fatalf("stale end restored TTL %d on resumed request", got.TTL)
	}
}
