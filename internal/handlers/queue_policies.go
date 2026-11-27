package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/store"
)

// ReplaceEventRequestSong allows an authorized singer (holding the request's
// domain-separated edit_token) to swap their queued song before being called.
func (h *QueueHandler) ReplaceEventRequestSong(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	requestID := r.PathValue("requestID")
	if requestID == "" {
		http.Error(w, "missing request ID", http.StatusBadRequest)
		return
	}

	event, err := h.resolveEvent(r, id)
	if err != nil || event == nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	editToken := strings.TrimSpace(r.Header.Get("X-Edit-Token"))
	if editToken == "" {
		http.Error(w, "unauthorized: valid edit token required", http.StatusForbidden)
		return
	}

	var payload models.RequestSongReplace
	if err := decodeJSON(w, r, &payload, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updated, newVersion, err := h.getQueueService().ReplaceRequestSong(r.Context(), queue.ReplaceRequestSongCmd{
		EventID:    event.ID,
		RequestID:  requestID,
		EditToken:  editToken,
		SongID:     payload.SongID,
		IsWriteIn:  payload.IsWriteIn,
		SongTitle:  payload.SongTitle,
		SongArtist: payload.SongArtist,
		KeyChange:  payload.KeyChange,
		Notes:      payload.Notes,
	})
	if err != nil {
		switch {
		case errors.Is(err, queue.ErrUnauthorized):
			http.Error(w, "unauthorized: valid edit token required", http.StatusForbidden)
		case errors.Is(err, queue.ErrRequestNotFound):
			http.Error(w, "request not found", http.StatusNotFound)
		case errors.Is(err, queue.ErrEventNotFound):
			http.Error(w, "event not found", http.StatusNotFound)
		case errors.Is(err, queue.ErrShowEnded):
			http.Error(w, "this show has ended", http.StatusGone)
		case errors.Is(err, queue.ErrSongReplacementDisabled):
			http.Error(w, "song replacement is disabled for this show", http.StatusForbidden)
		case errors.Is(err, queue.ErrWriteInsDisabled):
			http.Error(w, "write-in requests are not enabled for this show", http.StatusForbidden)
		case errors.Is(err, queue.ErrRequestAlreadyCalled):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "request_already_called",
				"message": "Your song has already been called to stage and can no longer be changed here. Please ask the host.",
			})
		case errors.Is(err, store.ErrAdmissionConflict):
			http.Error(w, "queue changed; please retry", http.StatusConflict)
		case errors.Is(err, os.ErrInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "failed to replace song", http.StatusInternalServerError)
		}
		return
	}

	if newVersion > 0 {
		w.Header().Set("X-Queue-Version", strconv.FormatInt(newVersion, 10))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

// DeferEventRequest allows the host to explicitly defer an absent singer down
// the active queue without recording a completed performance.
func (h *QueueHandler) DeferEventRequest(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	requestID := r.PathValue("requestID")
	idempotencyKey, err := parseIdempotencyKey(r)
	if err != nil {
		http.Error(w, "invalid Idempotency-Key", http.StatusBadRequest)
		return
	}

	var payload struct {
		ExpectedQueueVersion *int64 `json:"expected_queue_version,omitempty"`
	}
	if r.Body != nil {
		if err := decodeJSON(w, r, &payload, standardBodyLimit); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
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

	updatedReq, result, err := h.getQueueService().DeferEventRequest(r.Context(), queue.DeferEventRequestCmd{
		EventID:        eventID,
		RequestID:      requestID,
		ExpectedVer:    payload.ExpectedQueueVersion,
		IdempotencyKey: idempotencyKey,
		HostOwnerID:    idempotencyOwner,
	})
	if err != nil {
		if writeIdempotencyConflict(w, err) || writeQueueVersionConflict(w, err, expectedQueueVersion(payload.ExpectedQueueVersion)) {
			return
		}
		switch {
		case errors.Is(err, queue.ErrRequestNotFound):
			http.Error(w, "request not found", http.StatusNotFound)
		case errors.Is(err, queue.ErrEventNotFound):
			http.Error(w, "event not found", http.StatusNotFound)
		case errors.Is(err, queue.ErrRequestNotDeferrable):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "request_not_deferrable",
				"message": "Only active or called requests can be deferred.",
			})
		case errors.Is(err, os.ErrInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "failed to defer request", http.StatusInternalServerError)
		}
		return
	}

	if result != nil {
		writeQueueMutationResult(w, result)
		return
	}

	if payload.ExpectedQueueVersion != nil {
		w.Header().Set("X-Queue-Version", strconv.FormatInt(*payload.ExpectedQueueVersion+1, 10))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updatedReq)
}
