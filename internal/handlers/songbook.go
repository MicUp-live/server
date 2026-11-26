package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"micup.live/server/internal/catalog"
	"micup.live/server/internal/email"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/songbook"
	"micup.live/server/internal/store"
)

func newID() string {
	var u [16]byte
	rand.Read(u[:])
	u[6] = (u[6] & 0x0F) | 0x40
	u[8] = (u[8] & 0x3F) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
}

type ParseFileItem = catalog.ParseFileItem
type ParseFilesReq = catalog.ParseFilesReq
type ParseFilesResItem = catalog.ParseFilesResItem

// SongbookHandler adapts HTTP transport to songbook and catalog services.
type SongbookHandler struct {
	Events        store.EventStore
	Songbooks     store.SongbookStore
	CatalogJobs   catalog.JobQueue
	Catalogs      store.CatalogStore
	CatalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
	CatalogChanges store.CatalogChangeStore
	ImportJobs     store.ImportJobStore
	ImportQueue    interface {
		Enqueue(context.Context, importjob.Job) error
	}
	ManifestWriter interface {
		Put(context.Context, string, []byte) error
		Presign(context.Context, string, int64) (string, error)
	}
	Access         store.SongbookAccessStore
	Auth           store.AuthStore
	Requests       store.RequestStore
	Assets         songbook.AssetPurger
	EmailSender    email.Sender
	PublicBaseURL  string
	QueueRetention time.Duration

	SongbookService *songbook.Service
	CatalogService  *catalog.Service
}

func (h *SongbookHandler) getSongbookService() *songbook.Service { return h.buildSongbookService() }
func (h *SongbookHandler) getCatalogService() *catalog.Service   { return h.buildCatalogService() }

func (h *SongbookHandler) CreateSongbook(w http.ResponseWriter, r *http.Request) {
	var req models.SongbookCreate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var ownerID string
	if user := UserFromContext(r.Context()); user != nil {
		ownerID = user.ID
	}

	sb, err := h.getSongbookService().CreateSongbook(r.Context(), ownerID, req)
	if err != nil {
		if errors.Is(err, songbook.ErrSlugTaken) {
			http.Error(w, "slug is already taken", http.StatusConflict)
			return
		}
		if errors.Is(err, os.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sb)
}

func (h *SongbookHandler) ListSongbooks(w http.ResponseWriter, r *http.Request) {
	var ownerID string
	if user := UserFromContext(r.Context()); user != nil {
		ownerID = user.ID
	}
	sbs, err := h.getSongbookService().ListAccessibleSongbooks(r.Context(), ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string][]*models.Songbook{"songbooks": sbs})
}

func (h *SongbookHandler) GetSongbook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing ID", http.StatusBadRequest)
		return
	}
	if sb := songbookFromContext(r.Context()); sb != nil && sb.ID == id {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sb)
		return
	}
	sb, err := h.getSongbookService().GetSongbook(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sb)
}

// UpdateSongbook updates the name, slug, and publication state of an owned songbook.
func (h *SongbookHandler) UpdateSongbook(w http.ResponseWriter, r *http.Request) {
	var req models.SongbookUpdate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sb, err := h.getSongbookService().UpdateSongbook(r.Context(), r.PathValue("id"), req)
	if err != nil {
		if errors.Is(err, songbook.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, songbook.ErrSlugTaken) {
			http.Error(w, "slug is already taken", http.StatusConflict)
			return
		}
		if errors.Is(err, os.ErrInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sb)
}

// DeleteSongbook removes an owned songbook with dependency verification and resumable cleanup.
func (h *SongbookHandler) DeleteSongbook(w http.ResponseWriter, r *http.Request) {
	h.deleteSongbookWithConfirm(w, r)
}

func (h *SongbookHandler) CreateSong(w http.ResponseWriter, r *http.Request) {
	songbookID := r.PathValue("id")
	if songbookID == "" {
		http.Error(w, "missing songbook ID", http.StatusBadRequest)
		return
	}

	var req models.SongCreate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	song, err := h.getCatalogService().CreateSong(r.Context(), songbookID, req)
	if err != nil {
		writeCatalogWriteError(w, err, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(song)
}

func (h *SongbookHandler) ListSongs(w http.ResponseWriter, r *http.Request) {
	songbookID := r.PathValue("id")
	if songbookID == "" {
		http.Error(w, "missing songbook ID", http.StatusBadRequest)
		return
	}

	query := r.URL.Query().Get("q")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	songs, err := h.getCatalogService().ListSongs(r.Context(), songbookID, query, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(songs)
}

// UpdateSong updates a song within an owned songbook.
func (h *SongbookHandler) UpdateSong(w http.ResponseWriter, r *http.Request) {
	var req models.SongUpdate
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	song, err := h.getCatalogService().UpdateSong(r.Context(), r.PathValue("id"), r.PathValue("songID"), req)
	if err != nil {
		if errors.Is(err, catalog.ErrSongNotFound) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "song not found", http.StatusNotFound)
			return
		}
		writeCatalogWriteError(w, err, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(song)
}

// DeleteSong removes a song from an owned songbook.
func (h *SongbookHandler) DeleteSong(w http.ResponseWriter, r *http.Request) {
	err := h.getCatalogService().DeleteSong(r.Context(), r.PathValue("id"), r.PathValue("songID"))
	if err != nil {
		if errors.Is(err, catalog.ErrSongNotFound) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "song not found", http.StatusNotFound)
			return
		}
		writeCatalogWriteError(w, err, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSong returns a single song from a managed songbook, including host-only location metadata.
func (h *SongbookHandler) GetSong(w http.ResponseWriter, r *http.Request) {
	song, err := h.getCatalogService().GetSong(r.Context(), r.PathValue("id"), r.PathValue("songID"))
	if err != nil {
		if errors.Is(err, catalog.ErrSongNotFound) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "song not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(song)
}

// BatchDeleteSongs removes multiple songs from an owned songbook.
func (h *SongbookHandler) BatchDeleteSongs(w http.ResponseWriter, r *http.Request) {
	songbookID := r.PathValue("id")
	if songbookID == "" {
		http.Error(w, "missing songbook ID", http.StatusBadRequest)
		return
	}

	var req struct {
		SongIDs []string `json:"song_ids"`
	}
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	deletedCount, err := h.getCatalogService().BatchDeleteSongs(r.Context(), songbookID, req.SongIDs)
	if err != nil {
		writeCatalogWriteError(w, err, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"deleted": deletedCount,
	})
}

// ClearSongs removes all songs from an owned songbook.
func (h *SongbookHandler) ClearSongs(w http.ResponseWriter, r *http.Request) {
	songbookID := r.PathValue("id")
	if songbookID == "" {
		http.Error(w, "missing songbook ID", http.StatusBadRequest)
		return
	}

	if err := h.getCatalogService().ClearSongs(r.Context(), songbookID); err != nil {
		writeCatalogWriteError(w, err, http.StatusInternalServerError, "clear catalog")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"cleared": true})
}

// GetPublicManifest resolves the immutable public catalog manifest when ready.
func (h *SongbookHandler) GetPublicManifest(w http.ResponseWriter, r *http.Request) {
	eventRef := r.URL.Query().Get("code")
	if eventRef == "" {
		eventRef = r.URL.Query().Get("event")
	}
	if eventRef == "" {
		eventRef = r.URL.Query().Get("event_id")
	}
	manifest, err := h.getCatalogService().GetPublicManifest(r.Context(), r.PathValue("id"), eventRef)
	if err != nil {
		if errors.Is(err, catalog.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(manifest)
}

// GetPublicOverrides returns safe pending additions/edits for immediate public views.
func (h *SongbookHandler) GetPublicOverrides(w http.ResponseWriter, r *http.Request) {
	eventRef := r.URL.Query().Get("code")
	if eventRef == "" {
		eventRef = r.URL.Query().Get("event")
	}
	if eventRef == "" {
		eventRef = r.URL.Query().Get("event_id")
	}
	changes, err := h.getCatalogService().GetPublicOverrides(r.Context(), r.PathValue("id"), eventRef)
	if err != nil {
		if errors.Is(err, catalog.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		http.Error(w, "list catalog overrides", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(changes)
}

// GetManagedManifest resolves a private host's active immutable catalog.
func (h *SongbookHandler) GetManagedManifest(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "songbook not found", http.StatusNotFound)
		return
	}
	manifest, err := h.getCatalogService().GetManagedManifest(r.Context(), r.PathValue("id"), user.ID)
	if err != nil {
		if errors.Is(err, catalog.ErrSongbookNotFound) {
			http.Error(w, "songbook not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(manifest)
}

// GetCatalogOverrides returns authenticated songbook-specific pending changes.
func (h *SongbookHandler) GetCatalogOverrides(w http.ResponseWriter, r *http.Request) {
	changes, err := h.getCatalogService().GetCatalogOverrides(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, catalog.ErrServiceUnavailable) {
			http.Error(w, "catalog overrides unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "list catalog overrides", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(changes)
}

// CreateImportJob stores a normalized manifest and schedules durable processing.
func (h *SongbookHandler) CreateImportJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Songs        []*models.Song `json:"songs"`
		PruneMissing bool           `json:"prune_missing"`
	}
	if err := decodeJSON(w, r, &req, importBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	job, err := h.getCatalogService().CreateImportJob(r.Context(), r.PathValue("id"), req.Songs, req.PruneMissing)
	if err != nil {
		if errors.Is(err, catalog.ErrMissingFingerprint) {
			http.Error(w, "every song requires a fingerprint", http.StatusBadRequest)
			return
		}
		writeCatalogWriteError(w, err, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if job.Status == models.ImportJobQueued {
		w.WriteHeader(http.StatusAccepted)
	}
	_ = json.NewEncoder(w).Encode(job)
}

// GetImportJob returns progress for an owned asynchronous import.
func (h *SongbookHandler) GetImportJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.getCatalogService().GetImportJob(r.Context(), r.PathValue("id"), r.PathValue("jobID"))
	if err != nil {
		if errors.Is(err, catalog.ErrServiceUnavailable) {
			http.Error(w, "background imports are unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "import job not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

// GetLatestImportJob returns the newest import for a songbook, if any.
func (h *SongbookHandler) GetLatestImportJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.getCatalogService().GetLatestImportJob(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, catalog.ErrServiceUnavailable) {
			http.Error(w, "background imports are unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "no import job", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

// CreateImportUpload initializes a presigned manifest upload.
func (h *SongbookHandler) CreateImportUpload(w http.ResponseWriter, r *http.Request) {
	if h.ImportJobs == nil || h.ManifestWriter == nil {
		http.Error(w, "presigned imports are unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		TotalSongs   int   `json:"total_songs"`
		Size         int64 `json:"size"`
		PruneMissing bool  `json:"prune_missing"`
	}
	if err := decodeJSON(w, r, &req, standardBodyLimit); err != nil || req.TotalSongs < 1 || req.Size < 1 {
		http.Error(w, "total_songs and size are required", http.StatusBadRequest)
		return
	}
	job, url, err := h.getCatalogService().CreateImportUpload(r.Context(), r.PathValue("id"), req.TotalSongs, req.Size, req.PruneMissing)
	if err != nil {
		if errors.Is(err, catalog.ErrServiceUnavailable) {
			http.Error(w, "presigned imports are unavailable", http.StatusServiceUnavailable)
			return
		}
		writeCatalogWriteError(w, err, http.StatusInternalServerError, "create import job")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"job": job, "upload_url": url})
}

// CompleteImportUpload verifies the job is still queued and submits it to SQS.
func (h *SongbookHandler) CompleteImportUpload(w http.ResponseWriter, r *http.Request) {
	job, err := h.getCatalogService().CompleteImportUpload(r.Context(), r.PathValue("id"), r.PathValue("jobID"))
	if err != nil {
		if errors.Is(err, catalog.ErrServiceUnavailable) {
			http.Error(w, "background imports are unavailable", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, catalog.ErrImportJobClosed) {
			http.Error(w, "import job is already closed", http.StatusConflict)
			return
		}
		writeCatalogWriteError(w, err, http.StatusInternalServerError, "schedule import job")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func (h *SongbookHandler) ParseFiles(w http.ResponseWriter, r *http.Request) {
	songbookID := r.PathValue("id")
	if songbookID == "" {
		http.Error(w, "missing songbook ID", http.StatusBadRequest)
		return
	}

	var req ParseFilesReq
	if err := decodeJSON(w, r, &req, importBodyLimit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := catalog.ParseFiles(req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
}
