package dynamo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestEventSnapshotVersionPersistence(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	t.Run("legacy missing versions", func(t *testing.T) {
		ev := settingsEvent(t, s)
		if _, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.tables.events), Key: stringKey("id", ev.ID), UpdateExpression: aws.String("REMOVE queue_version, metadata_version")}); err != nil {
			t.Fatal(err)
		}
		legacy, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if legacy.QueueVersion != 0 || legacy.MetadataVersion != 0 {
			t.Fatal("legacy missing version was not zero")
		}
		legacy.Name = "Legacy edit"
		if err := s.UpdateEventWithVersion(ctx, legacy, true); err != nil {
			t.Fatal(err)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Name != legacy.Name || actual.QueueVersion != 1 || actual.MetadataVersion != 1 {
			t.Fatal("legacy snapshot failed to establish versions")
		}
	})
	t.Run("concurrent private metadata writes", func(t *testing.T) {
		ev := settingsEvent(t, s)
		first, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		first.OpenKJKey = models.GenerateOpenKJKey(ev.ID)
		second.OpenKJKey = models.GenerateOpenKJKey(ev.ID)
		// Both snapshots have the same queue and metadata version before either write.
		for _, snapshot := range []*models.Event{first, second} {
			go func() { <-start; results <- s.UpdateEventWithVersion(ctx, snapshot, false) }()
		}
		close(start)
		successes, conflicts := 0, 0
		for range 2 {
			err := <-results
			var conflict *store.QueueVersionConflictError
			if err == nil {
				successes++
			} else if errors.As(err, &conflict) {
				conflicts++
				if conflict.Current != ev.QueueVersion {
					t.Fatal("private update bumped queue version")
				}
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.MetadataVersion != 1 || actual.QueueVersion != ev.QueueVersion || (actual.OpenKJKey != first.OpenKJKey && actual.OpenKJKey != second.OpenKJKey) {
			t.Fatal("private metadata/version did not commit together")
		}
	})
	t.Run("restart barrier", func(t *testing.T) {
		ev := settingsEvent(t, s)
		if err := s.BeginQueueRestart(ctx, ev); err != nil {
			t.Fatal(err)
		}
		ev.Name = "stale restart edit"
		var conflict *store.QueueVersionConflictError
		if err := s.UpdateEventWithVersion(ctx, ev, true); !errors.As(err, &conflict) {
			t.Fatalf("restart conflict=%v", err)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !actual.Restarting || actual.Status != models.EventStatusEnded || actual.Name != "Original" || actual.MetadataVersion != 0 || actual.QueueVersion != ev.QueueVersion+1 {
			t.Fatal("snapshot breached restart barrier")
		}
	})
	t.Run("rotation transaction rollback", func(t *testing.T) {
		ev := settingsEvent(t, s)
		request := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, SingerName: "Singer", Status: models.StatusPending, Round: 3, Rank: 100}
		if err := s.CreateRequest(ctx, request); err != nil {
			t.Fatal(err)
		}
		changed := *request
		changed.Round = 0
		missing := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, Round: 0}
		ev.RotationMode = models.RotationModeFCFS
		if err := s.TransitionQueueRotation(ctx, ev, []*models.SongRequest{&changed, missing}); err == nil {
			t.Fatal("invalid transaction succeeded")
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		actualRequest, err := s.GetRequest(ctx, request.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.RotationMode != models.RotationModeRoundRobin || actual.MetadataVersion != 0 || actual.QueueVersion != ev.QueueVersion || actualRequest.Round != request.Round || actualRequest.Rank != request.Rank {
			t.Fatal("failed rotation partially committed metadata, versions, or request")
		}
	})
	t.Run("deleted event remains missing", func(t *testing.T) {
		ev := settingsEvent(t, s)
		if err := s.DeleteEvent(ctx, ev.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateEvent(ctx, ev); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted snapshot error=%v", err)
		}
		if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("snapshot recreated deleted event")
		}
	})
}
