package dynamo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	"micup.live/server/internal/store/storetest"
)

func TestStoreContract(t *testing.T) {
	s := newLocalStore(t)
	storetest.Run(t, func(*testing.T) storetest.Repositories {
		return storetest.Repositories{Auth: s, Songbooks: s}
	})
}

func helperCreateUser(t *testing.T, s *Store, id string) *models.User {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	if existing, err := s.GetUser(context.Background(), id); err == nil && existing != nil {
		return existing
	}
	u := &models.User{
		ID:    id,
		Email: fmt.Sprintf("user-%s@example.com", uuid.NewString()),
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("create test user %s: %v", id, err)
	}
	return u
}

func helperCreateSongbook(t *testing.T, s *Store, sb *models.Songbook) *models.Songbook {
	t.Helper()
	if sb.OwnerID == "" {
		owner := helperCreateUser(t, s, "")
		sb.OwnerID = owner.ID
	} else {
		helperCreateUser(t, s, sb.OwnerID)
	}
	if err := s.CreateSongbook(context.Background(), sb); err != nil {
		t.Fatalf("create test songbook %s: %v", sb.ID, err)
	}
	return sb
}

func helperCreateEvent(t *testing.T, s *Store, ev *models.Event) *models.Event {
	t.Helper()
	if ev.OwnerID == "" {
		owner := helperCreateUser(t, s, "")
		ev.OwnerID = owner.ID
	} else {
		helperCreateUser(t, s, ev.OwnerID)
	}
	if ev.JoinCode == "" {
		ev.JoinCode = "JC" + uuid.NewString()[:4]
	}
	if err := s.CreateEvent(context.Background(), ev); err != nil {
		t.Fatalf("create test event %s: %v", ev.ID, err)
	}
	return ev
}

func TestNormalizedEmailClaimIsAtomic(t *testing.T) {
	s := newLocalStore(t)
	users := []*models.User{
		{ID: uuid.NewString(), Email: " Host@Example.com ", DisplayName: "First"},
		{ID: uuid.NewString(), Email: "host@example.COM", DisplayName: "Second"},
	}
	errs := make(chan error, len(users))
	var wg sync.WaitGroup
	for _, user := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.CreateUser(context.Background(), user)
		}()
	}
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful duplicate-email creates = %d; want 1", succeeded)
	}
}

func TestCatalogVersionBuildAndPublish(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	owner := helperCreateUser(t, s, "")
	songbook := &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Versioned Catalog"}
	if err := s.CreateSongbook(ctx, songbook); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	version := &models.CatalogVersion{ID: "sha256:catalog-fixture", SchemaVersion: 1}
	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}
	if err := s.CreateCatalogVersion(ctx, version); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate catalog version error = %v; want os.ErrExist", err)
	}
	stored, err := s.GetCatalogVersion(ctx, version.ID)
	if err != nil || stored.Status != models.CatalogVersionBuilding {
		t.Fatalf("stored building catalog version = %+v, %v", stored, err)
	}

	if err := s.PublishCatalogVersion(ctx, songbook.ID, version.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publish building catalog error = %v; want os.ErrNotExist", err)
	}
	ready, err := s.MarkCatalogVersionReady(ctx, version.ID, "catalogs/sha256-catalog-fixture", 100000)
	if err != nil {
		t.Fatalf("mark catalog ready: %v", err)
	}
	if ready.Status != models.CatalogVersionReady || ready.SongCount != 100000 || ready.ReadyAt.IsZero() {
		t.Fatalf("ready catalog version = %+v", ready)
	}
	if err := s.PublishCatalogVersion(ctx, songbook.ID, version.ID); err != nil {
		t.Fatalf("publish catalog version: %v", err)
	}
	published, err := s.GetSongbook(ctx, songbook.ID)
	if err != nil || published.CatalogVersionID != version.ID {
		t.Fatalf("published songbook = %+v, %v", published, err)
	}
}

func TestCatalogVersionFailureCannotPublish(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	owner := helperCreateUser(t, s, "")
	songbook := &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Failed Catalog"}
	if err := s.CreateSongbook(ctx, songbook); err != nil {
		t.Fatalf("create songbook: %v", err)
	}
	version := &models.CatalogVersion{ID: "sha256:failed-fixture", SchemaVersion: 1}
	if err := s.CreateCatalogVersion(ctx, version); err != nil {
		t.Fatalf("create catalog version: %v", err)
	}
	failed, err := s.FailCatalogVersion(ctx, version.ID, "invalid source")
	if err != nil {
		t.Fatalf("fail catalog version: %v", err)
	}
	if failed.Status != models.CatalogVersionFailed || failed.FailureReason != "invalid source" {
		t.Fatalf("failed catalog version = %+v", failed)
	}
	if err := s.PublishCatalogVersion(ctx, songbook.ID, version.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publish failed catalog error = %v; want os.ErrNotExist", err)
	}
}

func TestExpiredSessionIsConsumed(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.com"}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	hash := []byte(uuid.NewString())
	if err := s.CreateAuthSession(ctx, &models.AuthSession{TokenHash: hash, UserID: user.ID, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if _, err := s.ConsumeAuthSession(ctx, hash); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consume expired session error = %v; want os.ErrNotExist", err)
	}
	if _, err := s.ConsumeAuthSession(ctx, hash); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reuse expired session error = %v; want os.ErrNotExist", err)
	}
}

func TestEnsureTablesIsIdempotent(t *testing.T) {
	s := newLocalStore(t)
	if err := s.EnsureTables(context.Background()); err != nil {
		t.Fatalf("ensure existing tables: %v", err)
	}
}

func newLocalStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("DYNAMO_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMO_ENDPOINT is not set")
	}
	s, err := New(context.Background(), Config{Region: "us-east-1", Endpoint: endpoint, TablePrefix: "test-" + uuid.NewString() + "-"})
	if err != nil {
		t.Fatalf("new DynamoDB store: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, tableName := range []string{
			s.tables.users, s.tables.identities, s.tables.sessions, s.tables.songbooks,
			s.tables.eventRequests,
			s.tables.events, s.tables.passkeys, s.tables.webauthn, s.tables.catalogs,
			s.tables.importJobs, s.tables.changes, s.tables.developerKeys, s.tables.rateLimits,
			s.tables.idempotency, s.tables.songbookGrants, s.tables.songbookInvitations,
		} {
			_, err := s.db.DeleteTable(ctx, &dynamodb.DeleteTableInput{
				TableName: aws.String(tableName),
			})
			if err != nil {
				var notFound *types.ResourceNotFoundException
				if !errors.As(err, &notFound) {
					t.Errorf("cleanup fixture table %s: %v", tableName, err)
				}
			}
		}
		_ = s.Close()
	})
	if err := s.EnsureTables(context.Background()); err != nil {
		t.Fatalf("ensure DynamoDB tables: %v", err)
	}
	return s
}

func TestRequestStoreOperations(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	sbID := uuid.NewString()
	owner := helperCreateUser(t, s, "")
	sb := &models.Songbook{ID: sbID, OwnerID: owner.ID, Name: "Test Book"}
	if err := s.CreateSongbook(ctx, sb); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	eventID := uuid.NewString()
	helperCreateEvent(t, s, &models.Event{
		ID:         eventID,
		OwnerID:    "owner-" + sbID[:8],
		SongbookID: sbID,
		Name:       "Live Request Night",
		JoinCode:   "REQ1",
		Status:     models.EventStatusActive,
	})

	// Eventless request creation is rejected.
	if err := s.CreateRequest(ctx, &models.SongRequest{
		SongbookID: sbID,
		SongTitle:  "Orphan",
		SingerName: "Nobody",
	}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("eventless CreateRequest = %v, want os.ErrInvalid", err)
	}

	req1 := &models.SongRequest{
		EventID:    eventID,
		SongbookID: sbID,
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		SingerName: "Freddie",
		KeyChange:  -1,
		Notes:      "High notes only",
	}
	if err := s.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("create req1: %v", err)
	}

	req2 := &models.SongRequest{
		EventID:    eventID,
		SongbookID: sbID,
		SongTitle:  "Don't Stop Believin'",
		SongArtist: "Journey",
		SingerName: "Steve",
		KeyChange:  0,
	}
	if err := s.CreateRequest(ctx, req2); err != nil {
		t.Fatalf("create req2: %v", err)
	}

	// List requests
	list, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(list))
	}
	if list[0].SingerName != "Freddie" || list[1].SingerName != "Steve" {
		t.Fatalf("unexpected order: %s, %s", list[0].SingerName, list[1].SingerName)
	}

	// Reorder Steve to #1, Freddie to #2
	if err := s.ReorderRequests(ctx, eventID, []string{req2.ID, req1.ID}); err != nil {
		t.Fatalf("reorder requests: %v", err)
	}

	listAfter, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("list after reorder: %v", err)
	}
	if listAfter[0].SingerName != "Steve" || listAfter[1].SingerName != "Freddie" {
		t.Fatalf("reorder failed, got: %s, %s", listAfter[0].SingerName, listAfter[1].SingerName)
	}

	// Update status
	req2.Status = models.StatusUpNext
	if err := s.UpdateRequest(ctx, req2); err != nil {
		t.Fatalf("update req2: %v", err)
	}
	got2, err := s.GetRequest(ctx, req2.ID)
	if err != nil || got2.Status != models.StatusUpNext {
		t.Fatalf("get updated req2: %v, status: %s", err, got2.Status)
	}

	// Delete request
	if err := s.DeleteRequest(ctx, req1.ID); err != nil {
		t.Fatalf("delete req1: %v", err)
	}
	remaining, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("expected 1 remaining after delete, got %d, err: %v", len(remaining), err)
	}
}

func TestConditionalEventMutationsAreAtomicAndVersionChecked(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	ownerID := "owner-" + eventID[:8]
	helperCreateUser(t, s, ownerID)
	event := &models.Event{ID: eventID, OwnerID: ownerID, SongbookID: "songbook-" + eventID[:8], Name: "Conditional Show", JoinCode: "COND-" + eventID[:8], Status: models.EventStatusActive, QueueVersion: 10}
	if err := s.CreateEvent(ctx, event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	first := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SongTitle: "First", SingerName: "Alice", Status: models.StatusAccepted, Rank: 1}
	second := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SongTitle: "Second", SingerName: "Bob", Status: models.StatusAccepted, Rank: 2}
	if err := s.CreateRequest(ctx, first); err != nil {
		t.Fatalf("create first request: %v", err)
	}
	if err := s.CreateRequest(ctx, second); err != nil {
		t.Fatalf("create second request: %v", err)
	}

	first.Status = models.StatusCompleted
	if err := s.UpdateRequestWithQueueVersion(ctx, first, 10); err != nil {
		t.Fatalf("conditional update: %v", err)
	}
	updatedEvent, err := s.GetEvent(ctx, eventID)
	if err != nil || updatedEvent.QueueVersion != 11 {
		t.Fatalf("conditional update queue version = %d, err=%v; want 11", updatedEvent.QueueVersion, err)
	}
	updatedFirst, err := s.GetRequest(ctx, first.ID)
	if err != nil || updatedFirst.Status != models.StatusCompleted {
		t.Fatalf("conditional update request = %s, err=%v; want completed", updatedFirst.Status, err)
	}

	second.Status = models.StatusRejected
	err = s.UpdateRequestWithQueueVersion(ctx, second, 10)
	var conflict *store.QueueVersionConflictError
	if !errors.As(err, &conflict) || conflict.Current != 11 {
		t.Fatalf("stale update error = %v; want version conflict at 11", err)
	}
	unchangedSecond, err := s.GetRequest(ctx, second.ID)
	if err != nil || unchangedSecond.Status != models.StatusAccepted {
		t.Fatalf("stale update mutated request = %s, err=%v", unchangedSecond.Status, err)
	}

	if err := s.ReorderRequestsWithQueueVersion(ctx, eventID, []string{second.ID, first.ID}, 11); err != nil {
		t.Fatalf("conditional reorder: %v", err)
	}
	updatedEvent, err = s.GetEvent(ctx, eventID)
	if err != nil || updatedEvent.QueueVersion != 12 {
		t.Fatalf("conditional reorder queue version = %d, err=%v; want 12", updatedEvent.QueueVersion, err)
	}
	ordered, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil || len(ordered) != 2 || ordered[0].ID != second.ID || ordered[1].ID != first.ID {
		t.Fatalf("conditional reorder order = %+v, err=%v", ordered, err)
	}

	err = s.ReorderRequestsWithQueueVersion(ctx, eventID, []string{first.ID, second.ID}, 11)
	if !errors.As(err, &conflict) || conflict.Current != 12 {
		t.Fatalf("stale reorder error = %v; want version conflict at 12", err)
	}
	ordered, err = s.ListRequestsByEvent(ctx, eventID)
	if err != nil || ordered[0].ID != second.ID || ordered[1].ID != first.ID {
		t.Fatalf("stale reorder changed order = %+v, err=%v", ordered, err)
	}
}

func TestIdempotentEventMutationsReplayExactlyOnce(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	helperCreateUser(t, s, "host-idempotent")
	helperCreateUser(t, s, "other-host")
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-idempotent",
		SongbookID:   "songbook-" + eventID[:8],
		Name:         "Idempotent Show",
		JoinCode:     "IDEMP-" + eventID[:8],
		Status:       models.EventStatusActive,
		QueueVersion: 20,
	}
	if err := s.CreateEvent(ctx, event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	request := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SongTitle: "Replay Me", SingerName: "Alice", Status: models.StatusAccepted, Rank: 1}
	if err := s.CreateRequest(ctx, request); err != nil {
		t.Fatalf("create request: %v", err)
	}
	expected := int64(20)
	key := "33333333-3333-4333-8333-333333333333"
	first, err := s.UpdateRequestWithIdempotency(ctx, request, &expected, key, event.OwnerID, "events/"+eventID+"/requests", "status-completed")
	if err != nil || first.Replay || first.QueueVersion != 21 {
		t.Fatalf("first idempotent completion = %+v, err=%v", first, err)
	}
	updated, err := s.GetEvent(ctx, eventID)
	if err != nil || updated.QueueVersion != 21 {
		t.Fatalf("first completion version = %d, err=%v; want 21", updated.QueueVersion, err)
	}

	replay, err := s.UpdateRequestWithIdempotency(ctx, request, &expected, key, event.OwnerID, "events/"+eventID+"/requests", "status-completed")
	if err != nil || !replay.Replay || replay.QueueVersion != 21 || string(replay.Body) != string(first.Body) {
		t.Fatalf("duplicate completion = %+v, err=%v", replay, err)
	}
	updated, _ = s.GetEvent(ctx, eventID)
	if updated.QueueVersion != 21 {
		t.Fatalf("duplicate completion bumped version to %d", updated.QueueVersion)
	}

	request.Status = models.StatusRejected
	if _, err := s.UpdateRequestWithIdempotency(ctx, request, &expected, key, event.OwnerID, "events/"+eventID+"/requests", "status-rejected"); !errors.As(err, new(*store.IdempotencyConflictError)) {
		t.Fatalf("payload mismatch error = %v; want idempotency conflict", err)
	}
	if _, err := s.UpdateRequestWithIdempotency(ctx, request, &expected, key, "other-host", "events/"+eventID+"/requests", "status-completed"); !errors.As(err, new(*store.IdempotencyConflictError)) {
		t.Fatalf("tenant mismatch error = %v; want idempotency conflict", err)
	}

	second := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SongTitle: "Second", SingerName: "Bob", Status: models.StatusAccepted, Rank: 2}
	if err := s.CreateRequest(ctx, second); err != nil {
		t.Fatalf("create second request: %v", err)
	}
	reorderExpected := int64(21)
	reorderKey := "44444444-4444-4444-8444-444444444444"
	orderRoute := "events/" + eventID + "/requests/reorder"
	ordered, err := s.ReorderRequestsWithIdempotency(ctx, eventID, []string{second.ID, request.ID}, &reorderExpected, reorderKey, event.OwnerID, orderRoute, "order-second-first")
	if err != nil || ordered.QueueVersion != 22 {
		t.Fatalf("first idempotent reorder = %+v, err=%v", ordered, err)
	}
	orderedReplay, err := s.ReorderRequestsWithIdempotency(ctx, eventID, []string{second.ID, request.ID}, &reorderExpected, reorderKey, event.OwnerID, orderRoute, "order-second-first")
	if err != nil || !orderedReplay.Replay || orderedReplay.QueueVersion != 22 {
		t.Fatalf("duplicate idempotent reorder = %+v, err=%v", orderedReplay, err)
	}
	updated, _ = s.GetEvent(ctx, eventID)
	if updated.QueueVersion != 22 {
		t.Fatalf("duplicate reorder bumped version to %d", updated.QueueVersion)
	}
	staleExpected := int64(21)
	staleKey := "66666666-6666-4666-8666-666666666666"
	if _, err := s.ReorderRequestsWithIdempotency(ctx, eventID, []string{request.ID, second.ID}, &staleExpected, staleKey, event.OwnerID, orderRoute, "stale-order"); !errors.As(err, new(*store.QueueVersionConflictError)) {
		t.Fatalf("stale idempotent reorder error = %v; want queue conflict", err)
	}
	orderedAfterStale, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil || len(orderedAfterStale) != 2 || orderedAfterStale[0].ID != second.ID {
		t.Fatalf("stale idempotent reorder changed order: %+v, err=%v", orderedAfterStale, err)
	}

	// Simulate an ambiguous post-commit retry: the first call has already
	// committed, and the retry must recover the exact stored result.
	if !orderedReplay.Replay {
		t.Fatal("ambiguous retry did not replay")
	}

	expiredKey := "55555555-5555-4555-8555-555555555555"
	expiredPayload, err := marshal(idempotencyItem{
		IdempotencyKey: expiredKey,
		OwnerID:        event.OwnerID,
		Route:          "events/" + eventID + "/requests",
		EntityID:       request.ID,
		RequestHash:    "expired-hash",
		StatusCode:     http.StatusOK,
		ResponseBody:   `{"status":"old"}`,
		QueueVersion:   22,
		TTL:            time.Now().Add(-time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal expired record: %v", err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.tables.idempotency), Item: expiredPayload}); err != nil {
		t.Fatalf("insert expired record: %v", err)
	}
	request.Status = models.StatusCompleted
	expiredExpected := int64(22)
	newResult, err := s.UpdateRequestWithIdempotency(ctx, request, &expiredExpected, expiredKey, event.OwnerID, "events/"+eventID+"/requests", "expired-hash")
	if err != nil || newResult.Replay || newResult.QueueVersion != 23 {
		t.Fatalf("expired idempotency record did not permit new mutation: %+v, err=%v", newResult, err)
	}
}

func TestEventStoreOperations(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ownerID := uuid.NewString()
	helperCreateUser(t, s, ownerID)
	sbID := uuid.NewString()

	ev1 := &models.Event{
		OwnerID:    ownerID,
		SongbookID: sbID,
		VenueName:  "O'Malley's Pub",
		Name:       "Friday Night Rock",
		JoinCode:   "OMALLEY",
		Status:     models.EventStatusActive,
	}
	if err := s.CreateEvent(ctx, ev1); err != nil {
		t.Fatalf("create event 1: %v", err)
	}

	ev2 := &models.Event{
		OwnerID:    ownerID,
		SongbookID: sbID,
		VenueName:  "The Dive Bar",
		Name:       "Saturday Karaoke",
		JoinCode:   "DIVE42",
		Status:     models.EventStatusActive,
	}
	if err := s.CreateEvent(ctx, ev2); err != nil {
		t.Fatalf("create event 2: %v", err)
	}

	// Lookup by ID
	got1, err := s.GetEvent(ctx, ev1.ID)
	if err != nil || got1.JoinCode != "OMALLEY" {
		t.Fatalf("get event 1: %v, got: %+v", err, got1)
	}

	// Lookup by join code (case-insensitive)
	byCode, err := s.GetEventByJoinCode(ctx, "omalley")
	if err != nil || byCode.ID != ev1.ID {
		t.Fatalf("get event by code: %v, got: %+v", err, byCode)
	}

	// List events by owner
	ownerEvents, err := s.ListEventsByOwner(ctx, ownerID)
	if err != nil || len(ownerEvents) != 2 {
		t.Fatalf("list events by owner: %v, len: %d", err, len(ownerEvents))
	}

	// List events by songbook
	sbEvents, err := s.ListEventsBySongbook(ctx, sbID)
	if err != nil || len(sbEvents) != 2 {
		t.Fatalf("list events by songbook: %v, len: %d", err, len(sbEvents))
	}

	// Update status, tip url, announcement, and kiosk pin
	ev1.Status = models.EventStatusPaused
	ev1.TipURL = "https://cash.app/$karaokehost"
	ev1.Announcement = "Drink special: $5 margaritas tonight!"
	ev1.KioskPIN = "9876"
	if err := s.UpdateEvent(ctx, ev1); err != nil {
		t.Fatalf("update event: %v", err)
	}
	updated, err := s.GetEvent(ctx, ev1.ID)
	if err != nil || updated.Status != models.EventStatusPaused || updated.TipURL != ev1.TipURL || updated.Announcement != ev1.Announcement || updated.KioskPIN != "9876" {
		t.Fatalf("updated event mismatch: %v, got: %+v", err, updated)
	}

	// Create request for ev2 to verify cascade deletion
	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    ev2.ID,
		SingerName: "Charlie",
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		Status:     models.StatusPending,
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}

	// Delete event
	if err := s.DeleteEvent(ctx, ev2.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	afterDelete, err := s.ListEventsByOwner(ctx, ownerID)
	if err != nil || len(afterDelete) != 1 {
		t.Fatalf("expected 1 event after delete, got %d, err: %v", len(afterDelete), err)
	}
	reqsAfterDelete, err := s.ListRequestsByEvent(ctx, ev2.ID)
	if err != nil || len(reqsAfterDelete) != 0 {
		t.Fatalf("expected 0 requests for deleted event, got %d, err: %v", len(reqsAfterDelete), err)
	}
}

func TestChangeUserEmail(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	user1 := &models.User{
		ID:          uuid.NewString(),
		Email:       "alice@example.com",
		DisplayName: "Alice",
	}
	if err := s.CreateUser(ctx, user1); err != nil {
		t.Fatalf("create user1: %v", err)
	}

	user2 := &models.User{
		ID:          uuid.NewString(),
		Email:       "bob@example.com",
		DisplayName: "Bob",
	}
	if err := s.CreateUser(ctx, user2); err != nil {
		t.Fatalf("create user2: %v", err)
	}

	// 1. Trying to change Alice's email to Bob's email should fail with conflict
	user1Updated := *user1
	user1Updated.Email = "bob@example.com"
	err := s.ChangeUserEmail(ctx, user1.ID, user1.Email, "bob@example.com", &user1Updated)
	if err == nil {
		t.Fatal("expected conflict error when changing email to existing bob@example.com, got nil")
	}

	// 2. Change Alice's email to a new valid email
	newEmail := "alice.new@example.com"
	user1Updated.Email = newEmail
	if err := s.ChangeUserEmail(ctx, user1.ID, user1.Email, newEmail, &user1Updated); err != nil {
		t.Fatalf("change user email: %v", err)
	}

	// Verify old email no longer resolves
	_, err = s.GetUserByEmail(ctx, "alice@example.com")
	if err == nil {
		t.Fatal("expected not found for old email, got nil")
	}

	// Verify new email resolves to user1
	byNewEmail, err := s.GetUserByEmail(ctx, newEmail)
	if err != nil {
		t.Fatalf("get user by new email: %v", err)
	}
	if byNewEmail.ID != user1.ID || byNewEmail.Email != newEmail {
		t.Fatalf("unexpected user returned: %+v", byNewEmail)
	}

	// Verify user record by ID has updated email
	byID, err := s.GetUser(ctx, user1.ID)
	if err != nil {
		t.Fatalf("get user by id: %v", err)
	}
	if byID.Email != newEmail {
		t.Fatalf("user email on record = %q; want %q", byID.Email, newEmail)
	}
}

func TestCreateUserWithPasskey(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	userID := uuid.NewString()
	credID := "cred-" + uuid.NewString()
	user := &models.User{
		ID:          userID,
		Email:       "passkey.singer@example.com",
		DisplayName: "Passkey Singer",
	}
	cred := &models.PasskeyCredential{
		ID:           credID,
		UserID:       userID,
		PublicKey:    []byte("dummy-public-key"),
		FriendlyName: "iPhone Passkey",
		CreatedAt:    time.Now(),
	}

	// 1. Atomically create user and passkey
	if err := s.CreateUserWithPasskey(ctx, user, cred); err != nil {
		t.Fatalf("CreateUserWithPasskey: %v", err)
	}

	// Verify user exists by ID and email
	gotUser, err := s.GetUser(ctx, userID)
	if err != nil || gotUser.Email != user.Email {
		t.Fatalf("get user = %+v, %v", gotUser, err)
	}
	byEmail, err := s.GetUserByEmail(ctx, user.Email)
	if err != nil || byEmail.ID != userID {
		t.Fatalf("get user by email = %+v, %v", byEmail, err)
	}

	// Verify passkey credential exists and lists
	gotCred, err := s.GetPasskeyCredential(ctx, credID)
	if err != nil || gotCred.UserID != userID {
		t.Fatalf("get passkey credential = %+v, %v", gotCred, err)
	}
	list, err := s.ListPasskeyCredentialsForUser(ctx, userID)
	if err != nil || len(list) != 1 || list[0].ID != credID {
		t.Fatalf("list passkey credentials = %+v, %v", list, err)
	}

	// 2. Conflict: Duplicate email should fail atomically
	userDup := &models.User{
		ID:          uuid.NewString(),
		Email:       "passkey.singer@example.com",
		DisplayName: "Duplicate Singer",
	}
	credDup := &models.PasskeyCredential{
		ID:           "cred-dup-" + uuid.NewString(),
		UserID:       userDup.ID,
		PublicKey:    []byte("dummy-key-2"),
		FriendlyName: "Android Passkey",
	}
	if err := s.CreateUserWithPasskey(ctx, userDup, credDup); err == nil {
		t.Fatal("expected conflict error for duplicate email, got nil")
	}

	// Verify the duplicate cred was NOT written either (atomic transaction rollback)
	if _, err := s.GetPasskeyCredential(ctx, credDup.ID); err == nil {
		t.Fatal("expected duplicate passkey credential to not exist after transaction failure")
	}
}

func TestReorderRequests_TenantScoping(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventA := uuid.NewString()
	eventB := uuid.NewString()
	sbA := uuid.NewString()
	sbB := uuid.NewString()
	helperCreateUser(t, s, "host-1")
	for _, eventID := range []string{eventA, eventB} {
		if err := s.CreateEvent(ctx, &models.Event{
			ID: eventID, OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive,
		}); err != nil {
			t.Fatalf("create event %s: %v", eventID, err)
		}
	}

	reqA1 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventA,
		SongbookID: sbA,
		SongTitle:  "Song A1",
		SingerName: "Alice",
	}
	if err := s.CreateRequest(ctx, reqA1); err != nil {
		t.Fatalf("create reqA1: %v", err)
	}

	reqA2 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventA,
		SongbookID: sbA,
		SongTitle:  "Song A2",
		SingerName: "Amy",
	}
	if err := s.CreateRequest(ctx, reqA2); err != nil {
		t.Fatalf("create reqA2: %v", err)
	}

	reqB1 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventB,
		SongbookID: sbB,
		SongTitle:  "Song B1",
		SingerName: "Bob",
	}
	if err := s.CreateRequest(ctx, reqB1); err != nil {
		t.Fatalf("create reqB1: %v", err)
	}

	// 1. Cross-event reorder must be rejected
	err := s.ReorderRequests(ctx, eventA, []string{reqA1.ID, reqB1.ID})
	if err == nil {
		t.Fatal("expected cross-event reorder to fail, got nil")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected os.ErrPermission for cross-event reorder, got %v", err)
	}

	// Verify no partial rank mutation occurred
	afterA1, err := s.GetRequest(ctx, reqA1.ID)
	if err != nil {
		t.Fatalf("get reqA1: %v", err)
	}
	if afterA1.Rank == 1 {
		t.Fatalf("reqA1 rank was updated despite transaction failure")
	}

	// 2. Non-existent request ID must be rejected
	err = s.ReorderRequests(ctx, eventA, []string{reqA1.ID, "non-existent-id"})
	if err == nil {
		t.Fatal("expected non-existent request reorder to fail, got nil")
	}

	// 3. Valid scoped reorder succeeds
	if err := s.ReorderRequests(ctx, eventA, []string{reqA2.ID, reqA1.ID}); err != nil {
		t.Fatalf("valid reorder failed: %v", err)
	}
	gotA2, _ := s.GetRequest(ctx, reqA2.ID)
	gotA1, _ := s.GetRequest(ctx, reqA1.ID)
	if gotA2.Rank != 1 || gotA1.Rank != 2 {
		t.Fatalf("unexpected ranks: A2=%d, A1=%d", gotA2.Rank, gotA1.Rank)
	}
}

func TestCatalogVersionsAndImportJobs_Pagination(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	// Create 5 catalog versions
	for i := 0; i < 5; i++ {
		id := uuid.NewString()
		prefix := fmt.Sprintf("catalogs/v-%d", i)
		v := &models.CatalogVersion{
			ID:            id,
			SchemaVersion: 1,
			Status:        models.CatalogVersionBuilding,
		}
		if err := s.CreateCatalogVersion(ctx, v); err != nil {
			t.Fatalf("create building catalog version %d: %v", i, err)
		}
		if _, err := s.MarkCatalogVersionReady(ctx, id, prefix, 100); err != nil {
			t.Fatalf("mark catalog ready %d: %v", i, err)
		}
	}

	versions, err := s.ListCatalogVersions(ctx)
	if err != nil {
		t.Fatalf("ListCatalogVersions: %v", err)
	}
	if len(versions) != 5 {
		t.Fatalf("expected 5 catalog versions, got %d", len(versions))
	}

	// Create 5 import jobs for a songbook
	sbID := uuid.NewString()
	owner := helperCreateUser(t, s, "")
	if err := s.CreateSongbook(ctx, &models.Songbook{ID: sbID, OwnerID: owner.ID, Name: "Import SB"}); err != nil {
		t.Fatalf("create songbook: %v", err)
	}
	for i := 0; i < 5; i++ {
		job := &models.ImportJob{
			ID:          uuid.NewString(),
			SongbookID:  sbID,
			Status:      models.ImportJobCompleted,
			ManifestKey: fmt.Sprintf("imports/manifest-%d.json", i),
			CreatedAt:   time.Now().UTC().Add(time.Duration(i) * time.Minute),
			UpdatedAt:   time.Now().UTC().Add(time.Duration(i) * time.Minute),
		}
		if err := s.CreateImportJob(ctx, job); err != nil {
			t.Fatalf("create import job %d: %v", i, err)
		}
	}

	jobs, err := s.ListImportJobs(ctx)
	if err != nil {
		t.Fatalf("ListImportJobs: %v", err)
	}
	if len(jobs) != 5 {
		t.Fatalf("expected 5 import jobs, got %d", len(jobs))
	}

	latest, err := s.LatestImportJob(ctx, sbID)
	if err != nil {
		t.Fatalf("LatestImportJob: %v", err)
	}
	if latest.ManifestKey != "imports/manifest-4.json" {
		t.Fatalf("expected newest manifest-4.json, got %s", latest.ManifestKey)
	}
}

func TestUpdateEvent_PreservesAndBumpsQueueVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	ownerID := uuid.NewString()
	helperCreateUser(t, s, ownerID)
	ev := &models.Event{
		ID:           uuid.NewString(),
		OwnerID:      ownerID,
		SongbookID:   uuid.NewString(),
		VenueName:    "Original Venue",
		Name:         "Show #1",
		JoinCode:     "JOIN01",
		Status:       models.EventStatusActive,
		QueueVersion: 10,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("create event: %v", err)
	}

	// 1. Advance queue version in the background (e.g. from singer request)
	v1, err := s.BumpQueueVersion(ctx, ev.ID)
	if err != nil || v1 != 11 {
		t.Fatalf("BumpQueueVersion = %d, %v; want 11", v1, err)
	}

	// 2. A stale snapshot must fail without overwriting settings or queue_version.
	ev.VenueName = "Updated Venue"
	var conflict *store.QueueVersionConflictError
	if err := s.UpdateEvent(ctx, ev); !errors.As(err, &conflict) || conflict.Current != 11 {
		t.Fatalf("stale UpdateEvent = %v; want conflict at 11", err)
	}
	current, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.VenueName != "Original Venue" || current.QueueVersion != 11 || current.MetadataVersion != 0 {
		t.Fatalf("rejected snapshot changed event: %+v", current)
	}
	// Reload and apply only the requested edit. The compatibility update keeps
	// queue_version unchanged while advancing its internal metadata fence.
	ev = current
	ev.VenueName = "Updated Venue"
	if err := s.UpdateEvent(ctx, ev); err != nil {
		t.Fatalf("fresh UpdateEvent: %v", err)
	}
	if ev.MetadataVersion != 1 {
		t.Fatalf("metadata version = %d; want 1", ev.MetadataVersion)
	}

	fetched, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if fetched.VenueName != "Updated Venue" {
		t.Fatalf("venue name not updated: %s", fetched.VenueName)
	}
	// Crucial check: UpdateEvent MUST NOT overwrite the live queue_version back to 10!
	if fetched.QueueVersion != 11 {
		t.Fatalf("QueueVersion was overwritten! got %d, want 11", fetched.QueueVersion)
	}

	// 3. Update with bumpVersion = true (queue-visible change, e.g. announcement or status)
	ev.Announcement = "Drink special!"
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatalf("UpdateEventWithVersion: %v", err)
	}

	fetched2, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if fetched2.Announcement != "Drink special!" {
		t.Fatalf("announcement not updated: %s", fetched2.Announcement)
	}
	if fetched2.QueueVersion != 12 {
		t.Fatalf("QueueVersion was not bumped! got %d, want 12", fetched2.QueueVersion)
	}
	if ev.QueueVersion != 12 {
		t.Fatalf("ev.QueueVersion in-memory not updated! got %d, want 12", ev.QueueVersion)
	}
}

func TestUpdateEvent_ConcurrentUpdatesAndBumps(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	ownerID := uuid.NewString()
	helperCreateUser(t, s, ownerID)
	ev := &models.Event{
		ID:           uuid.NewString(),
		OwnerID:      ownerID,
		SongbookID:   uuid.NewString(),
		VenueName:    "Main Stage",
		Name:         "Friday Karaoke",
		JoinCode:     "CONCUR",
		Status:       models.EventStatusActive,
		QueueVersion: 1,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("create event: %v", err)
	}

	const bumps = 15
	const updates = 15
	ready := make(chan struct{}, updates)
	release := make(chan struct{})

	var wg sync.WaitGroup
	var bumpWG sync.WaitGroup
	errCh := make(chan error, bumps+updates)

	// Commit queue-only mutations while all snapshot writers wait at their boundary.
	for i := 0; i < bumps; i++ {
		wg.Add(1)
		bumpWG.Add(1)
		go func() {
			defer wg.Done()
			defer bumpWG.Done()
			if _, err := s.BumpQueueVersion(ctx, ev.ID); err != nil {
				errCh <- fmt.Errorf("concurrent bump: %w", err)
			}
		}()
	}

	// Goroutines performing UpdateEventWithVersion (simulating concurrent host updates)
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			updateCopy := *ev
			updateCopy.Announcement = fmt.Sprintf("Announcement #%d", idx)
			ready <- struct{}{}
			<-release
			var conflict *store.QueueVersionConflictError
			err := s.UpdateEventWithVersion(ctx, &updateCopy, true)
			if !errors.As(err, &conflict) {
				errCh <- fmt.Errorf("stale concurrent update did not conflict: %v", err)
			}
		}(i)
	}

	for range updates {
		<-ready
	}
	bumpWG.Wait()
	close(release)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent update/bump failed: %v", err)
		}
	}

	finalEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get final event: %v", err)
	}

	expectedVersion := int64(1 + bumps)
	if finalEv.QueueVersion != expectedVersion {
		t.Fatalf("lost queue version updates under concurrency! got %d, want %d", finalEv.QueueVersion, expectedVersion)
	}
	if finalEv.Announcement != "" || finalEv.MetadataVersion != 0 {
		t.Fatalf("rejected writes changed metadata: %+v", finalEv)
	}
	finalEv.Announcement = "Fresh announcement"
	if err := s.UpdateEventWithVersion(ctx, finalEv, true); err != nil {
		t.Fatalf("fresh update failed: %v", err)
	}
	if finalEv.QueueVersion != expectedVersion+1 || finalEv.MetadataVersion != 1 {
		t.Fatalf("fresh update did not advance both versions: %+v", finalEv)
	}
}

func TestCatalogChanges_ConditionalDeletePreservesNewerChange(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	songbookID := "sb-" + uuid.NewString()
	owner := helperCreateUser(t, s, "")
	if err := s.CreateSongbook(ctx, &models.Songbook{ID: songbookID, OwnerID: owner.ID, Name: "Changes SB"}); err != nil {
		t.Fatalf("create songbook: %v", err)
	}
	songID := "song-" + uuid.NewString()

	t1 := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	change1 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     songID,
		Song:       &models.Song{ID: songID, Title: "Version 1", Artist: "Artist"},
		UpdatedAt:  t1,
	}

	if err := s.PutCatalogChange(ctx, change1); err != nil {
		t.Fatalf("put change 1: %v", err)
	}

	changes, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil || len(changes) != 1 {
		t.Fatalf("list changes after t1: %v, len: %d", err, len(changes))
	}
	if changes[0].Song.Title != "Version 1" {
		t.Fatalf("expected Version 1, got %s", changes[0].Song.Title)
	}

	// Concurrent update at t2 while compaction is running
	t2 := time.Date(2026, 9, 13, 10, 0, 5, 0, time.UTC)
	change2 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     songID,
		Song:       &models.Song{ID: songID, Title: "Version 2", Artist: "Artist"},
		UpdatedAt:  t2,
	}
	if err := s.PutCatalogChange(ctx, change2); err != nil {
		t.Fatalf("put change 2: %v", err)
	}

	// Compaction finishes for change1 (snapshot t1) and tries to conditionally delete
	if err := s.DeleteCatalogChange(ctx, songbookID, songID, t1); err != nil {
		t.Fatalf("delete change at t1 should succeed (no error): %v", err)
	}

	// Version 2 MUST still be preserved in the table!
	changesAfterT1Delete, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil || len(changesAfterT1Delete) != 1 {
		t.Fatalf("expected newer change to survive conditional delete, len: %d, err: %v", len(changesAfterT1Delete), err)
	}
	if changesAfterT1Delete[0].Song.Title != "Version 2" {
		t.Fatalf("expected preserved change to be Version 2, got %s", changesAfterT1Delete[0].Song.Title)
	}

	// Compaction finishes for change2 (snapshot t2) and conditionally deletes with t2
	if err := s.DeleteCatalogChange(ctx, songbookID, songID, t2); err != nil {
		t.Fatalf("delete change at t2: %v", err)
	}

	// Now it should be completely deleted
	changesAfterT2Delete, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil || len(changesAfterT2Delete) != 0 {
		t.Fatalf("expected change to be deleted, len: %d, err: %v", len(changesAfterT2Delete), err)
	}

	// Test unconditional delete (zero time)
	change3 := &models.CatalogChange{
		SongbookID: songbookID,
		SongID:     songID,
		Song:       &models.Song{ID: songID, Title: "Version 3"},
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.PutCatalogChange(ctx, change3); err != nil {
		t.Fatalf("put change 3: %v", err)
	}
	if err := s.DeleteCatalogChange(ctx, songbookID, songID, time.Time{}); err != nil {
		t.Fatalf("unconditional delete: %v", err)
	}
	remaining, err := s.ListCatalogChanges(ctx, songbookID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("expected 0 remaining after unconditional delete, got %d", len(remaining))
	}
}

func TestCreateEventRequestWithLimit_AtomicEnforcement(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	singer := "Singer Atomic"

	// Create event first
	ownerID := uuid.NewString()
	helperCreateUser(t, s, ownerID)
	event := &models.Event{
		ID:                 eventID,
		OwnerID:            ownerID,
		JoinCode:           "ATOMIC",
		Name:               "Atomic Limit Show",
		Status:             models.EventStatusActive,
		MaxActivePerSinger: 1,
	}
	if err := s.CreateEvent(ctx, event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	// 1. Launch 10 concurrent requests for the same singer with maxActive=1.
	// Exactly 1 must succeed and 9 must fail with ErrSingerLimitExceeded.
	const concurrency = 10
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes []*models.SongRequest
	var limitExceededCount int
	var otherErrors []error

	submitWithVersionRetry := func(r *models.SongRequest) error {
		for attempt := 0; attempt < concurrency+2; attempt++ {
			ev, err := s.GetEvent(ctx, eventID)
			if err != nil {
				return err
			}
			err = s.CreateRequestAtVersion(ctx, r, 1, nil, ev.QueueVersion)
			var conflict *store.QueueVersionConflictError
			if errors.As(err, &conflict) || errors.Is(err, store.ErrAdmissionConflict) {
				continue
			}
			return err
		}
		return store.ErrAdmissionConflict
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate
			r := &models.SongRequest{
				ID:         uuid.NewString(),
				EventID:    eventID,
				SingerName: singer,
				SongTitle:  fmt.Sprintf("Concurrent Song %d", idx),
				Status:     models.StatusPending,
			}
			err := submitWithVersionRetry(r)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes = append(successes, r)
			} else if errors.Is(err, store.ErrSingerLimitExceeded) {
				limitExceededCount++
			} else {
				otherErrors = append(otherErrors, err)
			}
		}(i)
	}

	close(startGate)
	wg.Wait()

	if len(otherErrors) > 0 {
		t.Fatalf("unexpected errors during concurrent submission: %v", otherErrors)
	}
	if len(successes) != 1 {
		t.Fatalf("expected exactly 1 success under maxActive=1, got %d", len(successes))
	}
	if limitExceededCount != concurrency-1 {
		t.Fatalf("expected exactly %d limit exceeded errors, got %d", concurrency-1, limitExceededCount)
	}

	// 2. Mark the winning request completed -> now a second concurrent batch can claim the slot.
	firstWinner := successes[0]
	firstWinner.Status = models.StatusCompleted
	if err := s.UpdateRequest(ctx, firstWinner); err != nil {
		t.Fatalf("update winner to completed: %v", err)
	}

	startGate2 := make(chan struct{})
	var successes2 []*models.SongRequest
	var limitExceededCount2 int

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate2
			r := &models.SongRequest{
				ID:         uuid.NewString(),
				EventID:    eventID,
				SingerName: singer,
				SongTitle:  fmt.Sprintf("Second Batch Song %d", idx),
				Status:     models.StatusPending,
			}
			err := submitWithVersionRetry(r)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes2 = append(successes2, r)
			} else if errors.Is(err, store.ErrSingerLimitExceeded) {
				limitExceededCount2++
			}
		}(i)
	}

	close(startGate2)
	wg.Wait()

	if len(successes2) != 1 {
		t.Fatalf("expected exactly 1 success in second batch, got %d", len(successes2))
	}
	if limitExceededCount2 != concurrency-1 {
		t.Fatalf("expected exactly %d limit exceeded in second batch, got %d", concurrency-1, limitExceededCount2)
	}

	// 3. Verify ListRequestsByEvent returns only the 2 song requests and NO slot records.
	listReqs, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("list requests by event: %v", err)
	}
	if len(listReqs) != 2 {
		t.Fatalf("expected 2 song requests in queue, got %d", len(listReqs))
	}
	for _, lr := range listReqs {
		if strings.HasPrefix(lr.ID, "slot#") {
			t.Fatalf("slot record leaked into queue list: %s", lr.ID)
		}
	}

	// 4. Verify that singer slot record exists before event deletion.
	slotID := fmt.Sprintf("slot#%s#%s#1", eventID, strings.ToLower(singer))
	slotKey := eventRequestKey(singerSlotPartition(eventID), slotID)
	slotRes, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            slotKey,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || len(slotRes.Item) == 0 {
		t.Fatalf("expected slot record %s to exist before DeleteEvent", slotID)
	}

	// 5. DeleteEvent cleans up child requests and singer slot items before deleting event (Finding 8).
	if err := s.DeleteEvent(ctx, eventID); err != nil {
		t.Fatalf("DeleteEvent failed: %v", err)
	}

	slotResAfter, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            slotKey,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("get slot after DeleteEvent: %v", err)
	}
	if len(slotResAfter.Item) != 0 {
		t.Fatalf("expected slot item %s to be deleted by DeleteEvent, but it still exists", slotID)
	}

	listAfter, err := s.ListRequestsByEvent(ctx, eventID)
	if err != nil || len(listAfter) != 0 {
		t.Fatalf("expected 0 requests remaining after DeleteEvent, got %d", len(listAfter))
	}

	_, err = s.GetEvent(ctx, eventID)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected event %s to be deleted, got err=%v", eventID, err)
	}
}

func TestStore_DistributedRateLimit(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	scope := "test_auth"
	ip := "198.51.100.42"
	limit := 3
	window := time.Minute

	// Request 1: hits=1, allowed=true, remaining=2
	res1, err := s.Allow(ctx, scope, ip, limit, window)
	if err != nil {
		t.Fatalf("unexpected error on request 1: %v", err)
	}
	if !res1.Allowed || res1.Remaining != 2 || res1.Limit != 3 {
		t.Fatalf("expected allowed with remaining=2, got %+v", res1)
	}

	// Request 2: hits=2, allowed=true, remaining=1
	res2, err := s.Allow(ctx, scope, ip, limit, window)
	if err != nil {
		t.Fatalf("unexpected error on request 2: %v", err)
	}
	if !res2.Allowed || res2.Remaining != 1 {
		t.Fatalf("expected allowed with remaining=1, got %+v", res2)
	}

	// Request 3: hits=3, allowed=true, remaining=0
	res3, err := s.Allow(ctx, scope, ip, limit, window)
	if err != nil {
		t.Fatalf("unexpected error on request 3: %v", err)
	}
	if !res3.Allowed || res3.Remaining != 0 {
		t.Fatalf("expected allowed with remaining=0, got %+v", res3)
	}

	// Request 4: hits=4, allowed=false, remaining=0 (exceeded limit)
	res4, err := s.Allow(ctx, scope, ip, limit, window)
	if err != nil {
		t.Fatalf("unexpected error on request 4: %v", err)
	}
	if res4.Allowed || res4.Remaining != 0 {
		t.Fatalf("expected rejected with remaining=0, got %+v", res4)
	}

	// Request with different scope or different IP should still be allowed
	resOtherIP, err := s.Allow(ctx, scope, "203.0.113.99", limit, window)
	if err != nil || !resOtherIP.Allowed || resOtherIP.Remaining != 2 {
		t.Fatalf("expected other IP to be unaffected, got %+v (err=%v)", resOtherIP, err)
	}

	resOtherScope, err := s.Allow(ctx, "other_scope", ip, limit, window)
	if err != nil || !resOtherScope.Allowed || resOtherScope.Remaining != 2 {
		t.Fatalf("expected other scope to be unaffected, got %+v (err=%v)", resOtherScope, err)
	}
}
