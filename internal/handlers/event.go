package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"micup.live/server/internal/event"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// EventHandler manages live karaoke events/shows.
type EventHandler struct {
	Events         store.EventStore
	Songbooks      store.SongbookStore
	Requests       store.RequestStore
	Access         store.SongbookAccessStore
	QueueRetention time.Duration
	EventService   *event.Service
}

func (h *EventHandler) getEventService() *event.Service {
	if h.EventService != nil {
		return h.EventService
	}
	return event.NewService(h.Events, h.Songbooks, h.Requests, h.QueueRetention).WithAccess(h.Access)
}

func writeEventHostingError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrPermissionDenied):
		http.Error(w, "insufficient hosting permissions for this songbook", http.StatusForbidden)
	case errors.Is(err, store.ErrSongbookDeleting):
		http.Error(w, "songbook is being deleted", http.StatusConflict)
	case errors.Is(err, event.ErrSongbookDetached):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "songbook_detached",
			"message": err.Error(),
		})
	default:
		return false
	}
	return true
}

// RequireOwnership ensures only the event's host can manage it.
func (h *EventHandler) RequireOwnership(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		ev, err := h.getEventService().GetEvent(r.Context(), r.PathValue("id"))
		if err != nil || ev.OwnerID != user.ID {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// CreateEvent starts a new event/show session.
func (h *EventHandler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	var req models.EventCreate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ev, err := h.getEventService().CreateEvent(r.Context(), user.ID, req)
	if err != nil {
		if writeEventHostingError(w, err) {
			return
		}
		if errors.Is(err, event.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to create event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ev)
}

// ListEvents lists all events owned by the authenticated host.
func (h *EventHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	events, err := h.getEventService().ListEvents(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"events": events,
	})
}

// GetEvent returns details for an event by ID (management view).
func (h *EventHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ev, err := h.getEventService().GetEvent(r.Context(), id)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ev)
}

// GetPublicEvent returns public show info for singers and kiosks.
func (h *EventHandler) GetPublicEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pub, err := h.getEventService().GetPublicEvent(r.Context(), id)
	if err != nil {
		if errors.Is(err, event.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		http.Error(w, "linked songbook not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pub)
}

// GetEventByCode resolves an event by its short join code (e.g. "OMALLEY").
func (h *EventHandler) GetEventByCode(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	result, err := h.getEventService().GetEventByCode(r.Context(), code)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Type == "songbook" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type":               "songbook",
			"songbook_id":        result.Songbook.ID,
			"public_songbook_id": result.Songbook.PublicID,
			"slug":               result.Songbook.Slug,
			"songbook_name":      result.Songbook.Name,
			"song_count":         result.Songbook.SongCount,
			"target_path":        result.Songbook.TargetPath,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(result.Event)
}

// UpdateEvent updates event settings, pauses requests, or ends the show.
func (h *EventHandler) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var update models.EventUpdate
	if err := decodeJSON(w, r, &update, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ev, err := h.getEventService().UpdateEvent(r.Context(), id, update)
	if err != nil {
		if writeQueueVersionConflict(w, err, expectedQueueVersion(nil)) {
			return
		}
		if writeEventHostingError(w, err) {
			return
		}
		if errors.Is(err, event.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, event.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, event.ErrInvalidStatus) || errors.Is(err, os.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to update event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ev)
}

// DeleteEvent deletes an event and all associated singer requests.
func (h *EventHandler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.getEventService().DeleteEvent(r.Context(), id); err != nil {
		http.Error(w, "failed to delete event", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RestartEvent re-activates an ended show and optionally archives/clears previous requests for a fresh session.
func (h *EventHandler) RestartEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ClearQueue *bool `json:"clear_queue"`
	}
	shouldClear := true
	if err := decodeJSON(w, r, &req, standardBodyLimit); err == nil && req.ClearQueue != nil {
		shouldClear = *req.ClearQueue
	}

	ev, err := h.getEventService().RestartEvent(r.Context(), id, shouldClear)
	if err != nil {
		if writeQueueVersionConflict(w, err, 0) {
			return
		}
		if writeEventHostingError(w, err) {
			return
		}
		if errors.Is(err, event.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, event.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to restart event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ev)
}

// RotateOpenKJKey generates a new integration key for the event, revoking the old one.
func (h *EventHandler) RotateOpenKJKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ev, err := h.getEventService().RotateOpenKJKey(r.Context(), id)
	if err != nil {
		if writeQueueVersionConflict(w, err, 0) {
			return
		}
		if errors.Is(err, event.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to update event integration key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"openkj_key": ev.OpenKJKey,
	})
}

// VerifyKioskPIN verifies a kiosk unlock PIN against the event's configured kiosk_pin.
func (h *EventHandler) VerifyKioskPIN(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		PIN string `json:"pin"`
	}
	if err := decodeJSON(w, r, &body, 1024); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	res, err := h.getEventService().VerifyKioskPIN(r.Context(), id, body.PIN)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	switch res {
	case event.PINResultNotConfigured:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "pin_not_configured",
			"message": "Kiosk exit PIN has not been set by the host for this event.",
		})
		return
	case event.PINResultInvalid:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_pin",
			"message": "Incorrect PIN. Ask host for assistance.",
		})
		return
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// PauseEvent freezes singer request submissions for an event.
func (h *EventHandler) PauseEvent(w http.ResponseWriter, r *http.Request) {
	h.setEventStatus(w, r, models.EventStatusPaused)
}

// ResumeEvent unfreezes singer request submissions for a paused event.
func (h *EventHandler) ResumeEvent(w http.ResponseWriter, r *http.Request) {
	h.setEventStatus(w, r, models.EventStatusActive)
}

func (h *EventHandler) setEventStatus(w http.ResponseWriter, r *http.Request, status models.EventStatus) {
	id := r.PathValue("id")
	ev, err := h.getEventService().UpdateEvent(r.Context(), id, models.EventUpdate{Status: &status})
	if err != nil {
		if writeQueueVersionConflict(w, err, 0) {
			return
		}
		if writeEventHostingError(w, err) {
			return
		}
		if errors.Is(err, event.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, event.ErrEventNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to update event status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ev)
}
