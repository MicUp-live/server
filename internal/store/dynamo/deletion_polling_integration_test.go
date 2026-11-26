package dynamo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
)

func TestDeleteEventRequestMakesConditionalPollingObserveRemoval(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive, QueueVersion: 1})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", SongTitle: "Song", Status: models.StatusAccepted, Rank: 1}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	h := &handlers.QueueHandler{Events: s, Requests: s, Secret: []byte("test-cancel-secret")}
	poll := func(since int64) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+event.ID+"/queue?since="+strconv.FormatInt(since, 10), nil)
		r.SetPathValue("id", event.ID)
		w := httptest.NewRecorder()
		h.GetEventPublicQueue(w, r)
		return w
	}
	if w := poll(1); w.Code != http.StatusNoContent || w.Header().Get("X-Queue-Version") != "1" {
		t.Fatalf("initial poll = %d, version %q; want 204/1", w.Code, w.Header().Get("X-Queue-Version"))
	}
	deleteRequest := func() *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/events/"+event.ID+"/requests/"+req.ID, nil)
		r.SetPathValue("id", event.ID)
		r.SetPathValue("requestID", req.ID)
		r.Header.Set("X-Cancel-Token", queue.GenerateCancelToken(h.Secret, req.ID, event.ID))
		w := httptest.NewRecorder()
		h.DeleteRequest(w, r)
		return w
	}
	if w := deleteRequest(); w.Code != http.StatusNoContent {
		t.Fatalf("singer cancellation = %d: %s", w.Code, w.Body.String())
	}
	if _, err := s.GetRequest(ctx, req.ID); !os.IsNotExist(err) {
		t.Fatalf("request after cancellation: %v", err)
	}
	w := poll(1)
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "2" {
		t.Fatalf("poll after cancellation = %d, version %q; want 200/2", w.Code, w.Header().Get("X-Queue-Version"))
	}
	var body struct {
		Queue []models.PublicQueueItem `json:"queue"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Queue) != 0 {
		t.Fatalf("queue after cancellation = %v, error = %v", body.Queue, err)
	}
	if w := deleteRequest(); w.Code != http.StatusNotFound {
		t.Fatalf("repeated cancellation = %d, want 404", w.Code)
	}
	if w := poll(2); w.Code != http.StatusNoContent || w.Header().Get("X-Queue-Version") != "2" {
		t.Fatalf("poll after repeated cancellation = %d, version %q; want 204/2", w.Code, w.Header().Get("X-Queue-Version"))
	}
}

// transactionHookClient changes the event only after DeleteEventRequestAtomic
// has completed its reads, immediately before DynamoDB receives its transaction.
type transactionHookClient struct {
	base   dynamodb.HTTPClient
	once   sync.Once
	before func() error
	err    error
	called bool
}

func (c *transactionHookClient) Do(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".TransactWriteItems") {
		c.once.Do(func() { c.called, c.err = true, c.before() })
		if c.err != nil {
			return nil, c.err
		}
	}
	return c.base.Do(r)
}

func TestDeleteEventRequestRollsBackWhenVersionWriteFailsAtCommit(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive, QueueVersion: 1})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", Status: models.StatusAccepted}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	realDB := s.db
	opts := realDB.Options()
	baseHTTP := opts.HTTPClient
	if baseHTTP == nil {
		baseHTTP = http.DefaultClient
	}
	hook := &transactionHookClient{base: baseHTTP, before: func() error {
		_, err := realDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(s.tables.events), Key: stringKey("id", event.ID),
			UpdateExpression:          aws.String("SET queue_version = :bad"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":bad": avString("invalid-version-type")},
		})
		return err
	}}
	opts.HTTPClient = hook
	s.db = dynamodb.New(opts)
	err := s.DeleteEventRequestAtomic(ctx, event.ID, req.ID)
	s.db = realDB
	if !hook.called || err == nil {
		t.Fatalf("transaction hook called=%t, delete error=%v; want failed version write", hook.called, err)
	}
	_, restoreErr := realDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tables.events), Key: stringKey("id", event.ID),
		UpdateExpression:          aws.String("SET queue_version = :version"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":version": avNumber(1)},
	})
	if restoreErr != nil {
		t.Fatalf("restore injected version type: %v", restoreErr)
	}
	if _, err := s.GetRequest(ctx, req.ID); err != nil {
		t.Fatalf("request after failed transaction: %v", err)
	}
	listed, err := s.listEventRequestsConsistent(ctx, event.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("canonical requests after failure = %v, error = %v; want one", listed, err)
	}
	updated, err := s.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != 1 {
		t.Fatalf("event after failure = %v, error = %v; want version 1", updated, err)
	}
	if err := s.DeleteEventRequestAtomic(ctx, event.ID, req.ID); err != nil {
		t.Fatalf("retry atomic delete: %v", err)
	}
	if err := s.DeleteEventRequestAtomic(ctx, event.ID, req.ID); !os.IsNotExist(err) {
		t.Fatalf("repeated atomic delete = %v, want not found", err)
	}
	updated, err = s.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != 2 {
		t.Fatalf("event after retry = %v, error = %v; want version 2", updated, err)
	}
}
