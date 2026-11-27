package queue

import (
	"errors"
	"testing"
	"time"

	"micup.live/server/internal/models"
)

func TestEditAndCancelTokensAreDomainSeparated(t *testing.T) {
	secret := []byte("test-secret-key-for-hp1-policies-32b")
	reqID := "req-123"
	evID := "evt-456"

	cancelTok := GenerateCancelToken(secret, reqID, evID)
	editTok := GenerateEditToken(secret, reqID, evID)

	if !ValidateCancelToken(secret, cancelTok, reqID, evID) {
		t.Fatal("expected valid cancel token to pass ValidateCancelToken")
	}
	if !ValidateEditToken(secret, editTok, reqID, evID) {
		t.Fatal("expected valid edit token to pass ValidateEditToken")
	}

	// Strict capability separation: cancel token cannot edit, edit token cannot cancel.
	if ValidateEditToken(secret, cancelTok, reqID, evID) {
		t.Fatal("cancel token must NOT satisfy ValidateEditToken")
	}
	if ValidateCancelToken(secret, editTok, reqID, evID) {
		t.Fatal("edit token must NOT satisfy ValidateCancelToken")
	}

	// Wrong request or wrong event must be rejected.
	if ValidateEditToken(secret, editTok, "req-other", evID) {
		t.Fatal("edit token must be rejected for a different request")
	}
	if ValidateEditToken(secret, editTok, reqID, "evt-other") {
		t.Fatal("edit token must be rejected for a different event")
	}
}

func TestPlanDeferRequest_BumpsDownThreeSpotsAndClearsUpNext(t *testing.T) {
	base := time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)
	ev := &models.Event{ID: "ev-1", RotationMode: models.RotationModeFairPriority}
	reqs := []*models.SongRequest{
		{ID: "r1", SingerName: "Alice", Status: models.StatusUpNext, Rank: 100, CreatedAt: base},
		{ID: "r2", SingerName: "Bob", Status: models.StatusAccepted, Rank: 200, CreatedAt: base.Add(time.Second)},
		{ID: "r3", SingerName: "Cara", Status: models.StatusAccepted, Rank: 300, CreatedAt: base.Add(2 * time.Second)},
		{ID: "r4", SingerName: "Dan", Status: models.StatusAccepted, Rank: 400, CreatedAt: base.Add(3 * time.Second)},
		{ID: "r5", SingerName: "Eve", Status: models.StatusAccepted, Rank: 500, CreatedAt: base.Add(4 * time.Second)},
		{ID: "r6", SingerName: "Pending", Status: models.StatusPending, Rank: 50, CreatedAt: base.Add(5 * time.Second)},
	}

	plan, err := PlanDeferRequest(ev, "r1", reqs, DefaultDeferPositions)
	if err != nil {
		t.Fatalf("PlanDeferRequest: %v", err)
	}
	if !plan.Changed {
		t.Fatal("expected defer plan to report Changed=true")
	}
	if plan.Target.Status != models.StatusAccepted {
		t.Fatalf("expected up_next to return to accepted, got %s", plan.Target.Status)
	}
	if plan.Target.CompletedAt != nil {
		t.Fatal("defer must never set CompletedAt")
	}
	wantOrder := []string{"r2", "r3", "r4", "r1", "r5"}
	if len(plan.ReorderIDs) != len(wantOrder) {
		t.Fatalf("got reorder %v, want %v", plan.ReorderIDs, wantOrder)
	}
	for i := range wantOrder {
		if plan.ReorderIDs[i] != wantOrder[i] {
			t.Fatalf("position %d: got %s, want %s (full: %v)", i, plan.ReorderIDs[i], wantOrder[i], plan.ReorderIDs)
		}
	}
}

func TestPlanDeferRequest_ClampsToEndAndAdoptsRoundInRoundRobin(t *testing.T) {
	base := time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)
	ev := &models.Event{ID: "ev-rr", RotationMode: models.RotationModeRoundRobin}
	reqs := []*models.SongRequest{
		{ID: "r1", SingerName: "Alice", Status: models.StatusAccepted, Round: 1, Rank: models.RoundRankMultiplier + 1, CreatedAt: base},
		{ID: "r2", SingerName: "Bob", Status: models.StatusAccepted, Round: 1, Rank: models.RoundRankMultiplier + 2, CreatedAt: base.Add(time.Second)},
		{ID: "r3", SingerName: "Cara", Status: models.StatusAccepted, Round: 2, Rank: 2*models.RoundRankMultiplier + 1, CreatedAt: base.Add(2 * time.Second)},
	}

	plan, err := PlanDeferRequest(ev, "r1", reqs, DefaultDeferPositions)
	if err != nil {
		t.Fatalf("PlanDeferRequest: %v", err)
	}
	wantOrder := []string{"r2", "r3", "r1"}
	if len(plan.ReorderIDs) != len(wantOrder) {
		t.Fatalf("got reorder %v, want %v", plan.ReorderIDs, wantOrder)
	}
	for i := range wantOrder {
		if plan.ReorderIDs[i] != wantOrder[i] {
			t.Fatalf("position %d: got %s, want %s", i, plan.ReorderIDs[i], wantOrder[i])
		}
	}
	if plan.Target.Round != 2 {
		t.Fatalf("expected deferred singer to adopt round 2, got %d", plan.Target.Round)
	}
}

func TestPlanDeferRequest_RejectsNonActiveRequests(t *testing.T) {
	ev := &models.Event{ID: "ev-1", RotationMode: models.RotationModeFCFS}
	for _, status := range []models.RequestStatus{
		models.StatusPending,
		models.StatusCompleted,
		models.StatusRejected,
		models.StatusCancelled,
	} {
		reqs := []*models.SongRequest{{ID: "r1", Status: status, Rank: 1}}
		_, err := PlanDeferRequest(ev, "r1", reqs, DefaultDeferPositions)
		if !errors.Is(err, ErrRequestNotDeferrable) {
			t.Fatalf("status %s: expected ErrRequestNotDeferrable, got %v", status, err)
		}
	}
}
