package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/store"
)

// QueueHandler handles singer song requests and host live queue management as an HTTP transport adapter.
type QueueHandler struct {
	Events    store.EventStore
	Songbooks store.SongbookStore
	Requests  store.RequestStore
	Secret    []byte
	QueueSvc  *queue.Service
}

func (h *QueueHandler) getSecret() []byte {
	if h != nil && len(h.Secret) > 0 {
		return h.Secret
	}
	return []byte("micup-production-queue-cancellation-secret-key-32b")
}

func (h *QueueHandler) getQueueService() *queue.Service {
	if h.QueueSvc != nil {
		return h.QueueSvc
	}
	return queue.NewService(h.Requests, h.Events, h.getSecret())
}

func (h *QueueHandler) generateCancelToken(requestID, eventID string) string {
	return queue.GenerateCancelToken(h.getSecret(), requestID, eventID)
}

func (h *QueueHandler) validateCancelToken(token, requestID, eventID string) bool {
	return queue.ValidateCancelToken(h.getSecret(), token, requestID, eventID)
}

// CreateEventRequest allows singers to submit a song request for a specific live event.
func (h *QueueHandler) CreateEventRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := h.resolveEvent(r, id)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	if event.Status == models.EventStatusPaused {
		http.Error(w, "requests are temporarily paused for this show", http.StatusConflict)
		return
	}
	if event.Status == models.EventStatusEnded {
		http.Error(w, "this show has ended", http.StatusGone)
		return
	}

	var req models.RequestCreate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	songReq, err := h.getQueueService().SubmitRequest(r.Context(), queue.SubmitRequestCmd{
		EventID:    event.ID,
		SongID:     req.SongID,
		IsWriteIn:  req.IsWriteIn,
		SongTitle:  req.SongTitle,
		SongArtist: req.SongArtist,
		SingerName: req.SingerName,
		KeyChange:  req.KeyChange,
		Notes:      req.Notes,
	})
	if err != nil {
		if writeAdmissionPolicyError(w, err) {
			return
		}
		if errors.Is(err, store.ErrSingerLimitExceeded) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			msg := fmt.Sprintf("You already have %d active request(s) in the queue. Please wait until your song has been performed.", event.MaxActivePerSinger)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "max_active_limit",
				"message": msg,
				"detail":  msg,
			})
			return
		}
		var cooldown *queue.ErrCooldownActive
		if errors.As(err, &cooldown) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			msg := cooldown.Error()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":             "submission_cooldown",
				"message":           msg,
				"seconds_remaining": cooldown.RemainingSeconds,
			})
			return
		}
		if errors.Is(err, os.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to submit request", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(songReq)
}

// GetEventPublicQueue returns the sanitized active queue for an event.
func (h *QueueHandler) GetEventPublicQueue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := h.resolveEvent(r, id)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	if h.queueUnchanged(r, event) {
		w.Header().Set("X-Queue-Version", strconv.FormatInt(event.QueueVersion, 10))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ev, items, err := h.getQueueService().GetPublicQueue(r.Context(), event.ID)
	if err != nil {
		http.Error(w, "failed to list queue", http.StatusInternalServerError)
		return
	}

	currentRound := ev.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Queue-Version", strconv.FormatInt(ev.QueueVersion, 10))
	payload := map[string]interface{}{
		"event_id":         ev.ID,
		"event_name":       ev.Name,
		"venue_name":       ev.VenueName,
		"join_code":        ev.JoinCode,
		"event_status":     ev.Status,
		"songbook_id":      ev.SongbookID,
		"tip_url":          ev.TipURL,
		"announcement":     ev.Announcement,
		"rotation_mode":    ev.RotationMode,
		"auto_accept":      ev.AutoAccept,
		"submission_delay": ev.SubmissionDelay,
		"current_round":    currentRound,
		"queue_version":    ev.QueueVersion,
		"queue":            items,
	}
	if ev.AllowSongReplacement {
		payload["allow_song_replacement"] = true
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// ListEventRequests returns all requests for an event to the host.
func (h *QueueHandler) ListEventRequests(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	event, err := h.Events.GetEvent(r.Context(), eventID)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}
	if h.queueUnchanged(r, event) {
		w.Header().Set("X-Queue-Version", strconv.FormatInt(event.QueueVersion, 10))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ev, requests, err := h.getQueueService().ListRequests(r.Context(), eventID)
	if err != nil {
		http.Error(w, "failed to list requests", http.StatusInternalServerError)
		return
	}

	currentRound := ev.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Queue-Version", strconv.FormatInt(ev.QueueVersion, 10))
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"requests":      requests,
		"current_round": currentRound,
		"queue_version": ev.QueueVersion,
	})
}

// UpdateEventRequest allows the host to accept, advance, bump, or complete an event request.
func (h *QueueHandler) UpdateEventRequest(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	requestID := r.PathValue("requestID")
	idempotencyKey, err := parseIdempotencyKey(r)
	if err != nil {
		http.Error(w, "invalid Idempotency-Key", http.StatusBadRequest)
		return
	}

	var update models.RequestUpdate
	if err := decodeJSON(w, r, &update, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := normalizeRequestUpdate(&update); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if update.ExpectedQueueVersion != nil && *update.ExpectedQueueVersion < 0 {
		http.Error(w, "expected_queue_version must not be negative", http.StatusBadRequest)
		return
	}

	h.dispatchUpdateRequest(w, r, eventID, requestID, update, idempotencyKey)
}

func (h *QueueHandler) dispatchUpdateRequest(w http.ResponseWriter, r *http.Request, eventID, requestID string, update models.RequestUpdate, idempotencyKey string) {
	var idempotencyOwner string
	if idempotencyKey != "" {
		user := UserFromContext(r.Context())
		if user == nil || strings.TrimSpace(user.ID) == "" {
			http.Error(w, "authenticated host is required for idempotent mutations", http.StatusUnauthorized)
			return
		}
		idempotencyOwner = user.ID
	}

	updatedReq, result, err := h.getQueueService().UpdateEventRequest(r.Context(), queue.UpdateEventRequestCmd{
		EventID:        eventID,
		RequestID:      requestID,
		Update:         update,
		ExpectedVer:    update.ExpectedQueueVersion,
		IdempotencyKey: idempotencyKey,
		HostOwnerID:    idempotencyOwner,
	})
	if err != nil {
		if writeIdempotencyConflict(w, err) || writeQueueVersionConflict(w, err, expectedQueueVersion(update.ExpectedQueueVersion)) {
			return
		}
		if errors.Is(err, queue.ErrRequestNotFound) {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, queue.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, os.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to update request", http.StatusInternalServerError)
		return
	}

	if result != nil {
		writeQueueMutationResult(w, result)
		return
	}

	if update.ExpectedQueueVersion != nil {
		w.Header().Set("X-Queue-Version", strconv.FormatInt(*update.ExpectedQueueVersion+1, 10))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updatedReq)
}

// ReorderEventRequests allows the host to manually reorder requests in the live queue.
func (h *QueueHandler) ReorderEventRequests(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	idempotencyKey, err := parseIdempotencyKey(r)
	if err != nil {
		http.Error(w, "invalid Idempotency-Key", http.StatusBadRequest)
		return
	}

	var payload struct {
		Order                []string `json:"order"`
		ExpectedQueueVersion *int64   `json:"expected_queue_version,omitempty"`
	}
	if err := decodeJSON(w, r, &payload, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if payload.ExpectedQueueVersion != nil && *payload.ExpectedQueueVersion < 0 {
		http.Error(w, "expected_queue_version must not be negative", http.StatusBadRequest)
		return
	}

	var idempotencyOwner string
	if idempotencyKey != "" {
		user := UserFromContext(r.Context())
		if user == nil || strings.TrimSpace(user.ID) == "" {
			http.Error(w, "authenticated host is required for idempotent mutations", http.StatusUnauthorized)
			return
		}
		idempotencyOwner = user.ID
	}

	result, err := h.getQueueService().ReorderEventRequests(r.Context(), queue.ReorderEventRequestsCmd{
		EventID:        eventID,
		Order:          payload.Order,
		ExpectedVer:    payload.ExpectedQueueVersion,
		IdempotencyKey: idempotencyKey,
		HostOwnerID:    idempotencyOwner,
	})
	if err != nil {
		if writeIdempotencyConflict(w, err) {
			return
		}
		if writeQueueVersionConflict(w, err, expectedQueueVersion(payload.ExpectedQueueVersion)) {
			return
		}
		if errors.Is(err, os.ErrInvalid) || errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to reorder requests", http.StatusInternalServerError)
		return
	}

	if result != nil {
		writeQueueMutationResult(w, result)
		return
	}

	if payload.ExpectedQueueVersion != nil {
		version := *payload.ExpectedQueueVersion
		if len(payload.Order) > 0 {
			version++
		}
		w.Header().Set("X-Queue-Version", strconv.FormatInt(version, 10))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func writeAdmissionPolicyError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, queue.ErrShowPaused):
		http.Error(w, "requests are temporarily paused for this show", http.StatusConflict)
	case errors.Is(err, queue.ErrShowEnded):
		http.Error(w, "this show has ended", http.StatusGone)
	case errors.Is(err, queue.ErrWriteInsDisabled):
		http.Error(w, "write-in requests are not enabled for this show", http.StatusForbidden)
	case errors.Is(err, store.ErrAdmissionConflict):
		http.Error(w, "queue changed; please retry", http.StatusConflict)
	default:
		return false
	}
	return true
}

// loadAuthorizedRequest resolves and authorizes an event-scoped request for GetRequest/DeleteRequest.
func (h *QueueHandler) loadAuthorizedRequest(w http.ResponseWriter, r *http.Request) (*models.SongRequest, bool) {
	requestID := r.PathValue("requestID")
	if requestID == "" {
		http.Error(w, "missing request ID", http.StatusBadRequest)
		return nil, false
	}
	eventID := r.PathValue("id")
	request, err := h.Requests.GetRequest(r.Context(), requestID)
	if errors.Is(err, os.ErrNotExist) || (err == nil && request == nil) {
		http.Error(w, "request not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		http.Error(w, "failed to load request", http.StatusInternalServerError)
		return nil, false
	}
	ev, err := h.resolveEvent(r, eventID)
	if err != nil || ev == nil || request.EventID != ev.ID {
		http.Error(w, "request not found", http.StatusNotFound)
		return nil, false
	}
	if !h.isRequestAuthorized(r, requestID, ev) {
		http.Error(w, "unauthorized: valid cancellation token or host ownership required", http.StatusForbidden)
		return nil, false
	}
	return request, true
}

// DeleteRequest deletes a request from the queue after verifying host ownership or cancellation token.
func (h *QueueHandler) DeleteRequest(w http.ResponseWriter, r *http.Request) {
	request, ok := h.loadAuthorizedRequest(w, r)
	if !ok {
		return
	}
	if err := h.getQueueService().DeleteRequest(r.Context(), queue.DeleteRequestCmd{
		EventID: request.EventID, RequestID: request.ID,
	}); err != nil {
		if errors.Is(err, queue.ErrRequestNotFound) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to delete request", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetRequest returns the status of an individual request. Requires cancel_token or host ownership.
func (h *QueueHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	request, ok := h.loadAuthorizedRequest(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":             request.ID,
		"status":         request.Status,
		"decline_reason": request.DeclineReason,
		"song_title":     request.SongTitle,
		"song_artist":    request.SongArtist,
		"singer_name":    request.SingerName,
		"key_change":     request.KeyChange,
		"created_at":     request.CreatedAt,
	})
}

func (h *QueueHandler) isRequestAuthorized(r *http.Request, requestID string, ev *models.Event) bool {
	token := r.Header.Get("X-Cancel-Token")
	if token == "" {
		token = r.URL.Query().Get("cancel_token")
	}
	if token != "" && ev != nil && h.validateCancelToken(token, requestID, ev.ID) {
		return true
	}
	user := UserFromContext(r.Context())
	return user != nil && user.IsHost && ev != nil && ev.OwnerID == user.ID
}

func (h *QueueHandler) resolveEvent(r *http.Request, id string) (*models.Event, error) {
	ev, err := h.Events.GetEvent(r.Context(), id)
	if err == nil {
		return ev, nil
	}
	return h.Events.GetEventByJoinCode(r.Context(), id)
}

func (h *QueueHandler) queueUnchanged(r *http.Request, event *models.Event) bool {
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	if since == "" {
		return false
	}
	version, err := strconv.ParseInt(since, 10, 64)
	return err == nil && version == event.QueueVersion
}

func writeQueueVersionConflict(w http.ResponseWriter, err error, expected int64) bool {
	var conflict *store.QueueVersionConflictError
	if !errors.As(err, &conflict) {
		return false
	}
	if expected == 0 {
		expected = conflict.Expected
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Queue-Version", strconv.FormatInt(conflict.Current, 10))
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":                  "queue_version_conflict",
		"expected_queue_version": expected,
		"current_queue_version":  conflict.Current,
		"queue_version":          conflict.Current,
	})
	return true
}

func parseIdempotencyKey(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if raw == "" {
		return "", nil
	}
	key, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}
	return key.String(), nil
}

func normalizeRequestUpdate(update *models.RequestUpdate) error {
	if err := normalizeUpdateStatus(&update.Status); err != nil {
		return err
	}
	if err := trimRequiredString(&update.SongTitle, "song_title cannot be empty"); err != nil {
		return err
	}
	if err := trimRequiredString(&update.SongArtist, "song_artist cannot be empty"); err != nil {
		return err
	}
	if err := trimRequiredString(&update.SingerName, "singer_name cannot be empty"); err != nil {
		return err
	}
	trimOptionalString(&update.SongID)
	clampKeyChange(&update.KeyChange)
	trimOptionalString(&update.Notes)
	trimOptionalString(&update.DeclineReason)
	return nil
}

func expectedQueueVersion(version *int64) int64 {
	if version == nil {
		return 0
	}
	return *version
}

func writeIdempotencyConflict(w http.ResponseWriter, err error) bool {
	var conflict *store.IdempotencyConflictError
	if !errors.As(err, &conflict) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "idempotency_key_conflict"})
	return true
}

func writeQueueMutationResult(w http.ResponseWriter, result *store.QueueMutationResult) {
	if result == nil {
		http.Error(w, "empty queue mutation result", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Queue-Version", strconv.FormatInt(result.QueueVersion, 10))
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}
