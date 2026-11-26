package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"micup.live/server/internal/catalog"
	"micup.live/server/internal/models"
	"micup.live/server/internal/songbook"
	"micup.live/server/internal/store"
)

const authorizedSongbookContextKey contextKey = "authorized-songbook"

func songbookFromContext(ctx context.Context) *models.Songbook {
	if ctx == nil {
		return nil
	}
	sb, _ := ctx.Value(authorizedSongbookContextKey).(*models.Songbook)
	return sb
}

func (h *SongbookHandler) buildSongbookService() *songbook.Service {
	if h.SongbookService != nil {
		return h.SongbookService
	}
	svc := songbook.NewService(h.Songbooks, h.Events)
	svc.Access = h.Access
	svc.Auth = h.Auth
	svc.CatalogChanges = h.CatalogChanges
	svc.ImportJobs = h.ImportJobs
	svc.Requests = h.Requests
	svc.Catalogs = h.Catalogs
	svc.Assets = h.Assets
	svc.EmailSender = h.EmailSender
	svc.QueueRetention = h.QueueRetention
	return svc
}

func (h *SongbookHandler) buildCatalogService() *catalog.Service {
	if h.CatalogService != nil {
		return h.CatalogService
	}
	svc := catalog.NewService(
		h.Songbooks,
		h.Events,
		h.CatalogJobs,
		h.Catalogs,
		h.CatalogReader,
		h.CatalogChanges,
		h.ImportJobs,
		h.ImportQueue,
		h.ManifestWriter,
	)
	svc.Access = h.Access
	return svc
}

// RequireAction enforces the canonical songbook permission policy for the requested action.
// For catalog-edit actions by collaborators, it attaches the commit-time edit-grant fence.
func (h *SongbookHandler) RequireAction(action songbook.Action, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		sb, _, err := h.getSongbookService().Authorize(r.Context(), user.ID, r.PathValue("id"), action)
		if err != nil {
			if errors.Is(err, store.ErrPermissionDenied) {
				http.Error(w, "insufficient permissions for this songbook", http.StatusForbidden)
				return
			}
			if errors.Is(err, store.ErrSongbookDeleting) {
				http.Error(w, "songbook is being deleted", http.StatusConflict)
				return
			}
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		ctx := context.WithValue(r.Context(), authorizedSongbookContextKey, sb)
		if action == songbook.ActionEditCatalog {
			ctx = songbook.WithEditContext(ctx, user.ID, sb)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func writeCatalogWriteError(w http.ResponseWriter, err error, fallbackStatus int, fallbackMsg string) {
	if errors.Is(err, store.ErrPermissionDenied) {
		http.Error(w, "insufficient permissions for this songbook", http.StatusForbidden)
		return
	}
	if errors.Is(err, store.ErrSongbookDeleting) {
		http.Error(w, "songbook is being deleted", http.StatusConflict)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if fallbackMsg == "" {
		fallbackMsg = err.Error()
	}
	http.Error(w, fallbackMsg, fallbackStatus)
}

func (h *SongbookHandler) publicBaseURL(r *http.Request) string {
	if strings.TrimSpace(h.PublicBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(h.PublicBaseURL), "/")
	}
	scheme := "https"
	if r.TLS == nil {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host
}

func (h *SongbookHandler) deleteSongbookWithConfirm(w http.ResponseWriter, r *http.Request) {
	confirmToken := strings.TrimSpace(r.Header.Get("X-Confirm-Detach-Token"))
	if confirmToken == "" {
		confirmToken = strings.TrimSpace(r.URL.Query().Get("confirm_detach_token"))
	}
	if confirmToken == "" && r.Body != nil {
		var body struct {
			ConfirmDetachToken string `json:"confirm_detach_token"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err == nil {
			confirmToken = strings.TrimSpace(body.ConfirmDetachToken)
		}
	}

	var ownerID string
	if user := UserFromContext(r.Context()); user != nil {
		ownerID = user.ID
	}

	err := h.getSongbookService().DeleteSongbookWithConfirmation(r.Context(), ownerID, r.PathValue("id"), confirmToken)
	if err != nil {
		var eventsErr *songbook.EventsAssociatedError
		if errors.As(err, &eventsErr) {
			http.Error(w, eventsErr.Error(), http.StatusConflict)
			return
		}
		var foreignErr *songbook.ForeignEventsAssociatedError
		if errors.As(err, &foreignErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":                "foreign_events_associated",
				"message":              foreignErr.Error(),
				"affected_shows_count": foreignErr.AffectedShowsCount,
				"active_shows_count":   foreignErr.ActiveShowsCount,
				"confirmation_token":   foreignErr.ConfirmationToken,
			})
			return
		}
		if errors.Is(err, songbook.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, store.ErrPermissionDenied) {
			http.Error(w, "insufficient permissions for this songbook", http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSongbookAccess lists accepted collaborators and pending invitations for the songbook owner.
func (h *SongbookHandler) GetSongbookAccess(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	overview, err := h.getSongbookService().GetAccessOverview(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(overview)
}

// CreateSongbookInvitation creates a pending invitation and emails the single-use acceptance link.
func (h *SongbookHandler) CreateSongbookInvitation(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	var req struct {
		Email          string `json:"email"`
		CanEditCatalog bool   `json:"can_edit_catalog"`
		CanHostShows   bool   `json:"can_host_shows"`
	}
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	inv, err := h.getSongbookService().CreateInvitation(
		r.Context(), user, r.PathValue("id"), req.Email, req.CanEditCatalog, req.CanHostShows, h.publicBaseURL(r),
	)
	if err != nil {
		writeAccessError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"invitation":      inv,
		"delivery_failed": inv.SendError != "",
	})
}

// ResendSongbookInvitation rotates the single-use link (invalidating the prior link) and resends the email.
func (h *SongbookHandler) ResendSongbookInvitation(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	inv, err := h.getSongbookService().ResendInvitation(
		r.Context(), user, r.PathValue("id"), r.PathValue("invitationID"), h.publicBaseURL(r),
	)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"invitation":      inv,
		"delivery_failed": inv.SendError != "",
	})
}

// CancelSongbookInvitation cancels a pending invitation and invalidates its link.
func (h *SongbookHandler) CancelSongbookInvitation(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if err := h.getSongbookService().CancelInvitation(r.Context(), user.ID, r.PathValue("id"), r.PathValue("invitationID")); err != nil {
		writeAccessError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateSongbookMember updates a collaborator's permissions on the songbook.
func (h *SongbookHandler) UpdateSongbookMember(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	var req struct {
		CanEditCatalog bool `json:"can_edit_catalog"`
		CanHostShows   bool `json:"can_host_shows"`
	}
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	grant, err := h.getSongbookService().UpdateMember(r.Context(), user.ID, r.PathValue("id"), r.PathValue("userID"), req.CanEditCatalog, req.CanHostShows)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(grant)
}

// RemoveSongbookMember revokes a collaborator's access to the songbook.
func (h *SongbookHandler) RemoveSongbookMember(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if err := h.getSongbookService().RemoveMember(r.Context(), user.ID, r.PathValue("id"), r.PathValue("userID")); err != nil {
		writeAccessError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PreviewSongbookInvitation returns recipient-facing details for a pending invitation token.
func (h *SongbookHandler) PreviewSongbookInvitation(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing invitation token", http.StatusBadRequest)
		return
	}
	preview, err := h.getSongbookService().PreviewInvitation(r.Context(), token, UserFromContext(r.Context()))
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(preview)
}

// AcceptSongbookInvitation consumes a single-use invitation and grants collaborator access.
func (h *SongbookHandler) AcceptSongbookInvitation(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil || strings.TrimSpace(req.Token) == "" {
		http.Error(w, "invitation token is required", http.StatusBadRequest)
		return
	}
	grant, _, err := h.getSongbookService().AcceptInvitation(r.Context(), req.Token, user)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"grant":       grant,
		"songbook_id": grant.SongbookID,
	})
}

func writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, songbook.ErrSongbookNotFound), errors.Is(err, store.ErrGrantNotFound), errors.Is(err, store.ErrInvitationNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, songbook.ErrPermissionRequired), errors.Is(err, songbook.ErrInvalidEmail), errors.Is(err, songbook.ErrCannotInviteOwner), errors.Is(err, os.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, songbook.ErrAlreadyCollaborator), errors.Is(err, store.ErrAlreadyCollaborator), errors.Is(err, store.ErrSongbookDeleting), errors.Is(err, store.ErrInvitationConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, store.ErrInvitationExpired), errors.Is(err, store.ErrInvitationInvalid):
		http.Error(w, err.Error(), http.StatusGone)
	case errors.Is(err, store.ErrInvitationEmailMismatch), errors.Is(err, songbook.ErrHostRequired), errors.Is(err, songbook.ErrEmailNotVerified), errors.Is(err, store.ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	default:
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
