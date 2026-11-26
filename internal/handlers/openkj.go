package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"micup.live/server/internal/event"
	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/songbook"
	"micup.live/server/internal/store"
)

// OpenKJHandler adapts OpenKJ's request-server protocol (pkircher29/OpenKJ okjsongbookapi.cpp)
// to MicUp's domain model and S3-first catalog change overlay.
type OpenKJHandler struct {
	Events         store.EventStore
	Songbooks      store.SongbookStore
	CatalogChanges store.CatalogChangeStore
	Requests       store.RequestStore
	Access         store.SongbookAccessStore
	Auth           store.AuthStore
	QueueSvc       *queue.Service
	EventSvc       *event.Service
}

func (h *OpenKJHandler) authorizeCatalogEdit(ctx context.Context, ev *models.Event) (context.Context, error) {
	if ev == nil || strings.TrimSpace(ev.SongbookID) == "" {
		return ctx, store.ErrPermissionDenied
	}
	if h.Songbooks == nil {
		return ctx, store.ErrPermissionDenied
	}
	if h.Auth != nil && ev.OwnerID != "" {
		u, err := h.Auth.GetUser(ctx, ev.OwnerID)
		if err != nil || u == nil || u.Deleting || !u.IsHost {
			return ctx, store.ErrPermissionDenied
		}
	}
	svc := songbook.NewService(h.Songbooks, h.Events)
	svc.Access = h.Access
	sb, _, err := svc.Authorize(ctx, ev.OwnerID, ev.SongbookID, songbook.ActionEditCatalog)
	if err != nil {
		return ctx, store.ErrPermissionDenied
	}
	return songbook.WithEditContext(ctx, ev.OwnerID, sb), nil
}

func (h *OpenKJHandler) getQueueService() *queue.Service {
	if h.QueueSvc != nil {
		return h.QueueSvc
	}
	return queue.NewService(h.Requests, h.Events, nil)
}

func (h *OpenKJHandler) getEventService() *event.Service {
	if h.EventSvc != nil {
		return h.EventSvc
	}
	return event.NewService(h.Events, h.Songbooks, h.Requests, 14*24*time.Hour).WithAccess(h.Access)
}

// HandleAction serves as the central dispatcher for OpenKJ protocol calls.
func (h *OpenKJHandler) HandleAction(w http.ResponseWriter, r *http.Request) {
	r = attachOpenKJBodyParams(r)
	params := openkjParamsFromContext(r)
	action := firstNonEmpty(
		r.URL.Query().Get("action"),
		r.URL.Query().Get("command"),
		r.PathValue("action"),
		params.Action,
		r.FormValue("action"),
		r.FormValue("command"),
	)

	switch normalizeOpenKJAction(action) {
	case "cleardatabase":
		h.ClearDatabase(w, r)
	case "addsongs":
		h.AddSongs(w, r)
	case "getserial":
		h.GetSerial(w, r)
	case "getvenues":
		h.GetVenues(w, r)
	case "getalert":
		h.GetAlert(w, r)
	case "getentitledsystemcount":
		h.GetEntitledSystemCount(w, r)
	case "setaccepting":
		h.SetAccepting(w, r)
	case "getrequests":
		h.GetRequests(w, r)
	case "deleterequest", "delrequest":
		h.DeleteRequest(w, r)
	case "clearrequests":
		h.ClearRequests(w, r)
	default:
		log.Printf("[OPENKJ WARN] unknown action=%q method=%s path=%s content_type=%q", action, r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"command":     action,
			"error":       true,
			"errorString": fmt.Sprintf("unknown action: %s", action),
			"status":      "error",
			"message":     fmt.Sprintf("unknown action: %s", action),
		})
	}
}

// resolveEvent authenticates and retrieves the target event using a private integration key.
// OpenKJ credentials must follow the format "okj_<event_id>_<secret>" or "<event_id>:<secret>".
// Public event IDs (UUIDs) and public join codes (e.g. "OMALLEY") are strictly prohibited.
func (h *OpenKJHandler) resolveEvent(r *http.Request) (*models.Event, error) {
	params := openkjParamsFromContext(r)
	key := firstNonEmpty(
		r.URL.Query().Get("key"),
		r.URL.Query().Get("api_key"),
		r.URL.Query().Get("venue"),
		r.Header.Get("X-API-Key"),
		params.Key,
		r.FormValue("key"),
		r.FormValue("api_key"),
		r.FormValue("venue"),
	)

	if key == "" {
		return nil, fmt.Errorf("valid private integration key required")
	}

	var eventID string
	if strings.HasPrefix(key, "okj_") {
		trimmed := strings.TrimPrefix(key, "okj_")
		lastUnderscore := strings.LastIndex(trimmed, "_")
		if lastUnderscore > 0 {
			eventID = trimmed[:lastUnderscore]
		}
	} else if colonIdx := strings.Index(key, ":"); colonIdx > 0 {
		eventID = key[:colonIdx]
	}

	if eventID == "" {
		return nil, fmt.Errorf("invalid integration key format")
	}

	ev, err := h.Events.GetEvent(r.Context(), eventID)
	if err != nil {
		return nil, fmt.Errorf("invalid integration key")
	}

	if ev.OpenKJKey == "" {
		return nil, fmt.Errorf("openkj integration key not configured for event")
	}

	if subtle.ConstantTimeCompare([]byte(key), []byte(ev.OpenKJKey)) != 1 {
		return nil, fmt.Errorf("invalid integration key")
	}

	if ev.Status == models.EventStatusEnded {
		return nil, fmt.Errorf("event has ended")
	}

	return ev, nil
}

// ClearDatabase clears catalog songs for the linked songbook.
func (h *OpenKJHandler) ClearDatabase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	fencedCtx, authErr := h.authorizeCatalogEdit(r.Context(), ev)
	if authErr != nil {
		http.Error(w, "insufficient catalog edit permissions for this songbook", http.StatusForbidden)
		return
	}

	if h.CatalogChanges == nil {
		http.Error(w, "catalog changes store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.CatalogChanges.PutCatalogChange(fencedCtx, &models.CatalogChange{
		SongbookID: ev.SongbookID,
		SongID:     "__clear__",
		Clear:      true,
	}); err != nil {
		writeCatalogWriteError(w, err, http.StatusInternalServerError, "failed to clear database: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "clearDatabase",
		"error":   false,
		"status":  "ok",
	})
}

// AddSongs bulk-imports songs from OpenKJ into the S3-first catalog change overlay.
func (h *OpenKJHandler) AddSongs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	fencedCtx, authErr := h.authorizeCatalogEdit(r.Context(), ev)
	if authErr != nil {
		http.Error(w, "insufficient catalog edit permissions for this songbook", http.StatusForbidden)
		return
	}
	if h.CatalogChanges == nil {
		http.Error(w, "catalog changes store unavailable", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, importBodyLimit))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var rawSongs []OpenKJSong
	// Check if JSON is an array or an object with a "songs" array
	if err := json.Unmarshal(body, &rawSongs); err != nil {
		var wrapper struct {
			Songs []OpenKJSong `json:"songs"`
		}
		if err2 := json.Unmarshal(body, &wrapper); err2 != nil {
			http.Error(w, "malformed song payload", http.StatusBadRequest)
			return
		}
		rawSongs = wrapper.Songs
	}

	const maxOpenKJSongsPerBatch = 5000
	if len(rawSongs) > maxOpenKJSongsPerBatch {
		http.Error(w, fmt.Sprintf("batch exceeds maximum of %d songs", maxOpenKJSongsPerBatch), http.StatusBadRequest)
		return
	}

	added, err := putOpenKJSongs(fencedCtx, h.CatalogChanges, ev.SongbookID, rawSongs)
	if err != nil {
		writeCatalogWriteError(w, err, http.StatusInternalServerError, "failed to insert songs: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "addSongs",
		"error":   false,
		"status":  "ok",
		"added":   added,
	})
}

// GetSerial returns a monotonic change serial number so OpenKJ knows when to poll.
func (h *OpenKJHandler) GetSerial(w http.ResponseWriter, r *http.Request) {
	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	serial := ev.QueueVersion
	if serial <= 0 {
		serial = 1
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "getSerial",
		"error":   false,
		"serial":  serial,
	})
}

// GetVenues returns the venue associated with the provided key in OpenKJ's expected envelope.
func (h *OpenKJHandler) GetVenues(w http.ResponseWriter, r *http.Request) {
	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	venueName := ev.VenueName
	if venueName == "" {
		venueName = ev.Name
	}
	urlName := ev.JoinCode
	if urlName == "" {
		urlName = ev.ID
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "getVenues",
		"error":   false,
		"venues": []map[string]any{
			{
				"venue_id":  1,
				"id":        ev.ID,
				"name":      venueName,
				"url_name":  urlName,
				"accepting": ev.Status == models.EventStatusActive,
			},
		},
	})
}

// GetAlert satisfies OpenKJ's startup alert check.
func (h *OpenKJHandler) GetAlert(w http.ResponseWriter, r *http.Request) {
	if _, err := h.resolveEvent(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "getAlert",
		"error":   false,
		"alert":   false,
	})
}

// GetEntitledSystemCount reports single-system entitlement for OpenKJ's status check.
func (h *OpenKJHandler) GetEntitledSystemCount(w http.ResponseWriter, r *http.Request) {
	if _, err := h.resolveEvent(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "getEntitledSystemCount",
		"error":   false,
		"count":   1,
	})
}

// SetAccepting updates whether the event is accepting requests.
func (h *OpenKJHandler) SetAccepting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	params := openkjParamsFromContext(r)
	var accepting bool
	if params.Accepting != nil {
		accepting = *params.Accepting
	} else {
		acceptingVal := firstNonEmpty(r.URL.Query().Get("accepting"), r.FormValue("accepting"))
		accepting = acceptingVal == "1" || strings.ToLower(acceptingVal) == "true"
	}
	if accepting {
		_, err = h.getEventService().ResumeEvent(r.Context(), ev.ID)
	} else {
		_, err = h.getEventService().PauseEvent(r.Context(), ev.ID)
	}
	if err != nil {
		if writeEventHostingError(w, err) {
			return
		}
		http.Error(w, "failed to update status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command":   "setAccepting",
		"error":     false,
		"status":    "ok",
		"accepting": accepting,
	})
}

// GetRequests returns pending singer requests formatted for OpenKJ.
func (h *OpenKJHandler) GetRequests(w http.ResponseWriter, r *http.Request) {
	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	requests, err := h.Requests.ListRequestsByEvent(r.Context(), ev.ID)
	if err != nil {
		http.Error(w, "failed to list requests", http.StatusInternalServerError)
		return
	}

	out := make([]OpenKJRequestDTO, 0, len(requests))
	for _, req := range requests {
		if req.Status == models.StatusPending || req.Status == models.StatusAccepted || req.Status == models.StatusUpNext {
			ts := req.CreatedAt.Unix()
			out = append(out, OpenKJRequestDTO{
				RequestID:   openKJRequestID(req.ID),
				ID:          req.ID,
				Singer:      req.SingerName,
				Artist:      req.SongArtist,
				Title:       req.SongTitle,
				KeyChange:   req.KeyChange,
				Key:         req.KeyChange,
				Notes:       req.Notes,
				RequestTime: ts,
				Timestamp:   ts,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command":  "getRequests",
		"error":    false,
		"requests": out,
	})
}

// DeleteRequest deletes or completes a single request after verifying event scope.
func (h *OpenKJHandler) DeleteRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	params := openkjParamsFromContext(r)
	rawReqID := firstNonEmpty(r.URL.Query().Get("id"), r.URL.Query().Get("request_id"), params.RequestID, r.FormValue("id"), r.FormValue("request_id"))
	if rawReqID == "" {
		http.Error(w, "missing request id", http.StatusBadRequest)
		return
	}

	req, err := h.Requests.GetRequest(r.Context(), rawReqID)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "failed to load request", http.StatusInternalServerError)
			return
		}
		if numericID, parseErr := strconv.Atoi(rawReqID); parseErr == nil && numericID > 0 {
			eventReqs, listErr := h.Requests.ListRequestsByEvent(r.Context(), ev.ID)
			if listErr != nil {
				http.Error(w, "failed to load event requests", http.StatusInternalServerError)
				return
			}
			for _, candidate := range eventReqs {
				if openKJRequestID(candidate.ID) == numericID {
					req = candidate
					err = nil
					break
				}
			}
		}
	}
	if err != nil || req == nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	if req.EventID != ev.ID {
		http.Error(w, "request does not belong to this event", http.StatusForbidden)
		return
	}

	completed := models.StatusCompleted
	_, _, err = h.getQueueService().UpdateEventRequest(r.Context(), queue.UpdateEventRequestCmd{
		EventID:   ev.ID,
		RequestID: req.ID,
		Update: models.RequestUpdate{
			Status: &completed,
		},
	})
	if err != nil {
		http.Error(w, "failed to update request", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "deleteRequest",
		"error":   false,
		"status":  "ok",
	})
}

// ClearRequests clears all requests for an event.
func (h *OpenKJHandler) ClearRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ev, err := h.resolveEvent(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	requests, err := h.Requests.ListRequestsByEvent(r.Context(), ev.ID)
	if err != nil {
		http.Error(w, "failed to list requests", http.StatusInternalServerError)
		return
	}

	completed := models.StatusCompleted
	for _, req := range requests {
		if req.Status == models.StatusPending || req.Status == models.StatusAccepted || req.Status == models.StatusUpNext {
			if _, _, err := h.getQueueService().UpdateEventRequest(r.Context(), queue.UpdateEventRequestCmd{
				EventID:   ev.ID,
				RequestID: req.ID,
				Update: models.RequestUpdate{
					Status: &completed,
				},
			}); err != nil {
				http.Error(w, "failed to clear requests", http.StatusInternalServerError)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"command": "clearRequests",
		"error":   false,
		"status":  "ok",
	})
}
