package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"micup.live/server/internal/account"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type AssetPurger = account.AssetPurger
type UserDataExport = account.UserDataExport
type PasskeyExportItem = account.PasskeyExportItem
type SongbookExportItem = account.SongbookExportItem
type ExportError = account.ExportError

// PrivacyHandler handles account data export and account deletion.
type PrivacyHandler struct {
	Auth          store.AuthStore
	Passkeys      store.PasskeyStore
	DeveloperKeys store.DeveloperKeyStore
	Songbooks     store.SongbookStore
	Events        store.EventStore
	Requests      store.RequestStore
	Catalogs      store.CatalogStore
	CatalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
	CatalogChanges store.CatalogChangeStore
	ImportJobs     store.ImportJobStore
	Assets         AssetPurger
	Access         store.SongbookAccessStore
	QueueRetention time.Duration
	AuthHandler    *AuthHandler

	AccountService *account.Service
}

func (h *PrivacyHandler) getAccountService() *account.Service {
	if h.AccountService != nil {
		if h.DeveloperKeys != nil && h.AccountService.DeveloperKeys == nil {
			h.AccountService.DeveloperKeys = h.DeveloperKeys
		}
		if h.Access != nil && h.AccountService.Access == nil {
			h.AccountService.Access = h.Access
		}
		return h.AccountService
	}
	svc := account.NewService(
		h.Auth,
		h.Passkeys,
		h.Songbooks,
		h.Events,
		h.Requests,
		h.Catalogs,
		h.CatalogReader,
		h.CatalogChanges,
		h.ImportJobs,
		h.Assets,
	)
	if h.DeveloperKeys != nil {
		svc.WithDeveloperKeys(h.DeveloperKeys)
	}
	if h.Access != nil {
		svc.WithAccess(h.Access)
	}
	svc.QueueRetention = h.QueueRetention
	return svc
}

// ExportData generates a complete, downloadable JSON snapshot of all user data.
func (h *PrivacyHandler) ExportData(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	export, err := h.getAccountService().ExportData(r.Context(), user)
	if err != nil {
		if errors.Is(err, store.ErrAccountDeleting) {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		log.Printf("[PRIVACY ERROR] Failed to export user data for user %s: %v", user.ID, err)
		writeAuthError(w, http.StatusInternalServerError, "failed to export user data")
		return
	}

	dateStr := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("micup-data-export-%s.json", dateStr)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	writeAuthJSON(w, http.StatusOK, export)
}

type deleteAccountRequest struct {
	Confirm string `json:"confirm"`
}

// DeleteAccount permanently purges the user account and cascades deletion across all domains.
func (h *PrivacyHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req deleteAccountRequest
	_ = decodeJSON(w, r, &req, authBodyLimit)
	if strings.ToUpper(strings.TrimSpace(req.Confirm)) != "DELETE" {
		writeAuthError(w, http.StatusBadRequest, "To permanently delete your account, you must type 'DELETE' to confirm.")
		return
	}

	if h.AuthHandler != nil {
		h.AuthHandler.clearRefreshCookie(w)
	}

	err := h.getAccountService().DeleteAccount(r.Context(), user)

	var deletionToken string
	if h.AuthHandler != nil && user.DeletionEpoch != "" {
		if tok, tokErr := h.AuthHandler.NewDeletionRecoveryToken(user.ID, user.DeletionEpoch); tokErr == nil && tok != "" {
			deletionToken = tok
			w.Header().Set("X-Deletion-Token", tok)
		}
	}

	if err != nil {
		resp := map[string]interface{}{
			"detail": err.Error(),
		}
		var cascadeErr *account.CascadeError
		if errors.As(err, &cascadeErr) {
			resp["step"] = cascadeErr.Step
		}
		if user.Deleting {
			resp["deleting"] = true
		}
		if deletionToken != "" {
			resp["deletion_token"] = deletionToken
		}
		writeAuthJSON(w, http.StatusInternalServerError, resp)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
