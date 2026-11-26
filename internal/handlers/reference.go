package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"micup.live/server/internal/reference"
)

const (
	maxInlineReferenceBytes = 4 * 1024 * 1024 // 4 MB threshold (safely under Lambda 6 MB payload limit)
	referenceCacheTTL       = 5 * time.Minute
	manifestObjectKey       = "reference/v1/sources/manifest.json"
	dictionaryObjectKey     = "reference/v1/dictionary.json.gz"
	discMapObjectKey        = "reference/v1/disc_map.json.gz"
)

// ReferenceHandler serves undocumented reference catalog and source archive endpoints.
type ReferenceHandler struct {
	Store       reference.Store
	SourcesPath string // fallback path to internal/reference/sources.json

	mu            sync.RWMutex
	cachedDictAt  time.Time
	cachedDiscsAt time.Time
	cachedDict    map[string]reference.CanonicalTrack
	cachedDiscs   map[string]reference.CanonicalTrack
}

// NewReferenceHandler creates a ReferenceHandler backed by the given Store.
func NewReferenceHandler(store reference.Store) *ReferenceHandler {
	if store == nil {
		store = &reference.HybridStore{
			LocalPublicDir: "./web/public",
			LocalDistDir:   "./dist",
		}
	}
	return &ReferenceHandler{
		Store:       store,
		SourcesPath: "./internal/reference/sources.json",
	}
}

type sourceResponseEntry struct {
	reference.ArchivedSource
	APIURL string `json:"api_url"`
	RawURL string `json:"raw_url"`
	CSVURL string `json:"csv_url"`
}

// ListSources handles GET /api/v1/reference/sources.
func (h *ReferenceHandler) ListSources(w http.ResponseWriter, r *http.Request) {
	manifest, err := h.loadManifest(r)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference sources manifest is unavailable")
		return
	}
	entries := make([]sourceResponseEntry, 0, len(manifest.Sources))
	for _, src := range manifest.Sources {
		entries = append(entries, enrichSourceEntry(src))
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"version":           manifest.Version,
		"build_id":          manifest.BuildID,
		"compiled_at":       manifest.CompiledAt,
		"inputs_hash":       manifest.InputsHash,
		"dictionary_tracks": manifest.DictionaryTracks,
		"mapped_discs":      manifest.MappedDiscs,
		"degraded":          manifest.Degraded,
		"stale_sources":     manifest.StaleSources,
		"sources":           entries,
	})
}

// GetSource handles GET /api/v1/reference/sources/{id}.
func (h *ReferenceHandler) GetSource(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimSpace(r.PathValue("id"))
	if rawID == "" || strings.Contains(rawID, "..") || strings.Contains(rawID, "/") {
		writeAuthError(w, http.StatusBadRequest, "invalid reference source id")
		return
	}

	manifest, err := h.loadManifest(r)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference sources manifest is unavailable")
		return
	}
	src, explicitExt, found := resolveArchivedSource(manifest, rawID)
	if !found {
		writeAuthError(w, http.StatusNotFound, "reference source not found")
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" && explicitExt != "" {
		format = strings.TrimPrefix(explicitExt, ".")
	}

	targetKey := src.RawKey
	filename := src.RawFilename
	if src.ID != "" && filename != "" {
		if ext := filepath.Ext(filename); ext != "" && !strings.EqualFold(filename, src.ID+ext) {
			filename = src.ID + ext
		}
	}
	contentType := src.RawContentType
	if format == "csv" && src.CSVKey != "" {
		targetKey = src.CSVKey
		filename = src.CSVFilename
		contentType = "text/csv; charset=utf-8"
	}

	if format == "json" {
		enriched := enrichSourceEntry(src)
		downloadURL := "/" + strings.TrimPrefix(targetKey, "/")
		if h.Store != nil {
			if presigned, err := h.Store.PresignGet(r.Context(), targetKey); err == nil && presigned != "" {
				downloadURL = presigned
			}
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"source":       enriched,
			"download_url": downloadURL,
		})
		return
	}

	h.serveReferenceObject(w, r, targetKey, filename, contentType)
}

// GetDictionary handles GET /api/v1/reference/dictionary.
func (h *ReferenceHandler) GetDictionary(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	artist := strings.TrimSpace(r.URL.Query().Get("artist"))
	title := strings.TrimSpace(r.URL.Query().Get("title"))

	if q == "" && artist == "" && title == "" {
		h.serveReferenceObject(w, r, dictionaryObjectKey, "dictionary.json.gz", "application/json")
		return
	}

	tracksMap, err := h.loadDictionary(r)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference dictionary is unavailable")
		return
	}

	limit := parseBoundedLimit(r.URL.Query().Get("limit"), 50, 500)
	matches := make([]reference.CanonicalTrack, 0, limit)

	// 1. Exact composite key match when both artist and title are provided.
	if q == "" && artist != "" && title != "" {
		key := reference.CompositeKey(artist, title)
		if exact, ok := tracksMap[key]; ok {
			matches = append(matches, exact)
		}
	}

	// 2. Substring search across normalized artist/title when needed.
	if len(matches) == 0 {
		qLower := strings.ToLower(q)
		artistLower := strings.ToLower(artist)
		titleLower := strings.ToLower(title)

		keys := make([]string, 0, len(tracksMap))
		for k := range tracksMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			t := tracksMap[k]
			if qLower != "" {
				if !strings.Contains(strings.ToLower(t.Artist), qLower) &&
					!strings.Contains(strings.ToLower(t.Title), qLower) &&
					!strings.Contains(k, qLower) {
					continue
				}
			}
			if artistLower != "" && !strings.Contains(strings.ToLower(t.Artist), artistLower) {
				continue
			}
			if titleLower != "" && !strings.Contains(strings.ToLower(t.Title), titleLower) {
				continue
			}
			matches = append(matches, t)
			if len(matches) >= limit {
				break
			}
		}
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"tracks":       matches,
		"count":        len(matches),
		"total_tracks": len(tracksMap),
	})
}

type discRecord struct {
	Disc        string `json:"disc"`
	CanonicalID string `json:"canonical_id"`
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Provider    string `json:"provider,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        int    `json:"year,omitempty"`
}

// GetDiscs handles GET /api/v1/reference/discs.
func (h *ReferenceHandler) GetDiscs(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))

	if q == "" && provider == "" {
		h.serveReferenceObject(w, r, discMapObjectKey, "disc_map.json.gz", "application/json")
		return
	}

	discsMap, err := h.loadDiscMap(r)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference disc map is unavailable")
		return
	}

	limit := parseBoundedLimit(r.URL.Query().Get("limit"), 50, 500)
	qLower := strings.ToLower(q)
	qNormDisc := strings.ToLower(reference.NormalizeDiscCode(q))
	provLower := strings.ToLower(provider)

	keys := make([]string, 0, len(discsMap))
	for k := range discsMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	matches := make([]discRecord, 0, limit)
	for _, code := range keys {
		t := discsMap[code]
		if provLower != "" && !strings.EqualFold(t.Source, provider) && !strings.Contains(strings.ToLower(t.Source), provLower) {
			continue
		}
		if qLower != "" {
			codeLower := strings.ToLower(code)
			if !strings.Contains(codeLower, qLower) &&
				(qNormDisc == "" || !strings.Contains(codeLower, qNormDisc)) &&
				!strings.Contains(strings.ToLower(t.Artist), qLower) &&
				!strings.Contains(strings.ToLower(t.Title), qLower) {
				continue
			}
		}
		matches = append(matches, discRecord{
			Disc:        code,
			CanonicalID: t.ID,
			Artist:      t.Artist,
			Title:       t.Title,
			Provider:    t.Source,
			Genre:       t.Genre,
			Year:        t.Year,
		})
		if len(matches) >= limit {
			break
		}
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"discs":       matches,
		"count":       len(matches),
		"total_discs": len(discsMap),
	})
}

// GetDiscByCode handles GET /api/v1/reference/discs/{code}.
func (h *ReferenceHandler) GetDiscByCode(w http.ResponseWriter, r *http.Request) {
	rawCode := strings.TrimSpace(r.PathValue("code"))
	normCode := reference.NormalizeDiscCode(rawCode)
	if normCode == "" {
		writeAuthError(w, http.StatusBadRequest, "disc code is required")
		return
	}

	discsMap, err := h.loadDiscMap(r)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference disc map is unavailable")
		return
	}

	track, ok := discsMap[normCode]
	if !ok {
		writeAuthError(w, http.StatusNotFound, "disc code not found")
		return
	}

	writeAuthJSON(w, http.StatusOK, discRecord{
		Disc:        normCode,
		CanonicalID: track.ID,
		Artist:      track.Artist,
		Title:       track.Title,
		Provider:    track.Source,
		Genre:       track.Genre,
		Year:        track.Year,
	})
}

func (h *ReferenceHandler) serveReferenceObject(w http.ResponseWriter, r *http.Request, key, filename, fallbackContentType string) {
	if h.Store == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "reference storage is not configured")
		return
	}
	if r.URL.Query().Get("redirect") == "true" {
		if url, err := h.Store.PresignGet(r.Context(), key); err == nil && url != "" {
			http.Redirect(w, r, url, http.StatusTemporaryRedirect)
			return
		}
		http.Redirect(w, r, "/"+strings.TrimPrefix(key, "/"), http.StatusTemporaryRedirect)
		return
	}

	obj, err := h.Store.GetObject(r.Context(), key, maxInlineReferenceBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeAuthError(w, http.StatusNotFound, "reference artifact not found")
			return
		}
		writeAuthError(w, http.StatusServiceUnavailable, "reference storage is temporarily unavailable")
		return
	}
	if obj.Presigned != "" {
		http.Redirect(w, r, obj.Presigned, http.StatusTemporaryRedirect)
		return
	}
	ct := obj.ContentType
	if ct == "" {
		ct = fallbackContentType
	}
	w.Header().Set("Content-Type", ct)
	if filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	}
	if strings.HasSuffix(key, ".gz") && len(obj.Body) >= 2 && obj.Body[0] == 0x1f && obj.Body[1] == 0x8b {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Body)
}

func (h *ReferenceHandler) loadManifest(r *http.Request) (*reference.SourceManifest, error) {
	if h.Store == nil {
		return nil, fmt.Errorf("reference store not configured")
	}
	obj, err := h.Store.GetObject(r.Context(), manifestObjectKey, 0)
	if err == nil {
		if len(obj.Body) == 0 {
			return nil, fmt.Errorf("reference sources manifest is empty")
		}
		var m reference.SourceManifest
		if decErr := reference.DecodeGzipJSON(obj.Body, &m); decErr != nil {
			return nil, fmt.Errorf("decode reference sources manifest: %w", decErr)
		}
		return &m, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// Only fall back to local internal/reference/sources.json when manifest.json does not exist yet in local dev.
	if h.SourcesPath == "" {
		return nil, err
	}
	raw, readErr := os.ReadFile(h.SourcesPath)
	if readErr != nil {
		return nil, err
	}
	var cfg reference.SourcesConfig
	if decErr := reference.DecodeGzipJSON(raw, &cfg); decErr != nil {
		return nil, fmt.Errorf("decode local sources config: %w", decErr)
	}
	srcSlice := cfg.AllSources()
	sources := make([]reference.ArchivedSource, 0, len(srcSlice)+1)
	hasHistorical := false
	for _, src := range srcSlice {
		id := src.ID
		if id == "" {
			id = reference.FormatSourceSlug(src.Name)
		}
		if id == "historical-discs" {
			hasHistorical = true
		}
		ext, ct, fmtName := reference.RawExtAndContentType(src.Format)
		srcURL := src.URL
		if srcURL == "" {
			srcURL = src.LocalPath
		}
		sources = append(sources, reference.ArchivedSource{
			ID:             id,
			Name:           src.Name,
			Provider:       src.Provider,
			Format:         fmtName,
			Lifecycle:      src.Lifecycle,
			SourceURL:      srcURL,
			RawFilename:    id + ext,
			RawKey:         "reference/v1/sources/" + id + ext,
			RawContentType: ct,
			CSVFilename:    id + ".csv",
			CSVKey:         "reference/v1/sources/" + id + ".csv",
		})
	}
	if !hasHistorical {
		sources = append(sources, reference.ArchivedSource{
			ID:             "historical-discs",
			Name:           "historical_discs.csv",
			Provider:       "Multi-Publisher",
			Format:         "csv",
			Lifecycle:      reference.LifecycleImmutable,
			RawFilename:    "historical-discs.csv",
			RawKey:         "reference/v1/sources/historical-discs.csv",
			RawContentType: "text/csv; charset=utf-8",
			CSVFilename:    "historical-discs.csv",
			CSVKey:         "reference/v1/sources/historical-discs.csv",
		})
	}
	version := cfg.Version
	if version == 0 {
		version = 1
	}
	return &reference.SourceManifest{
		Version:      version,
		StaleSources: []string{},
		Sources:      sources,
	}, nil
}

func (h *ReferenceHandler) loadDictionary(r *http.Request) (map[string]reference.CanonicalTrack, error) {
	h.mu.RLock()
	if h.cachedDict != nil && time.Since(h.cachedDictAt) < referenceCacheTTL {
		dict := h.cachedDict
		h.mu.RUnlock()
		return dict, nil
	}
	h.mu.RUnlock()

	if h.Store == nil {
		return nil, fmt.Errorf("reference store not configured")
	}
	obj, err := h.Store.GetObject(r.Context(), dictionaryObjectKey, 0)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tracks map[string]reference.CanonicalTrack `json:"tracks"`
	}
	if err := reference.DecodeGzipJSON(obj.Body, &payload); err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.cachedDict = payload.Tracks
	h.cachedDictAt = time.Now()
	h.mu.Unlock()
	return payload.Tracks, nil
}

func (h *ReferenceHandler) loadDiscMap(r *http.Request) (map[string]reference.CanonicalTrack, error) {
	h.mu.RLock()
	if h.cachedDiscs != nil && time.Since(h.cachedDiscsAt) < referenceCacheTTL {
		discs := h.cachedDiscs
		h.mu.RUnlock()
		return discs, nil
	}
	h.mu.RUnlock()

	if h.Store == nil {
		return nil, fmt.Errorf("reference store not configured")
	}
	obj, err := h.Store.GetObject(r.Context(), discMapObjectKey, 0)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Discs map[string]reference.CanonicalTrack `json:"discs"`
	}
	if err := reference.DecodeGzipJSON(obj.Body, &payload); err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.cachedDiscs = payload.Discs
	h.cachedDiscsAt = time.Now()
	h.mu.Unlock()
	return payload.Discs, nil
}

func enrichSourceEntry(src reference.ArchivedSource) sourceResponseEntry {
	return sourceResponseEntry{
		ArchivedSource: src,
		APIURL:         "/api/v1/reference/sources/" + src.ID,
		RawURL:         "/api/v1/reference/sources/" + src.ID + "?format=raw",
		CSVURL:         "/api/v1/reference/sources/" + src.ID + "?format=csv",
	}
}

func resolveArchivedSource(manifest *reference.SourceManifest, rawID string) (reference.ArchivedSource, string, bool) {
	ext := strings.ToLower(filepath.Ext(rawID))
	baseID := strings.TrimSuffix(rawID, ext)
	slug := reference.FormatSourceSlug(baseID)

	if manifest != nil {
		for _, src := range manifest.Sources {
			if strings.EqualFold(src.ID, rawID) ||
				strings.EqualFold(src.ID, baseID) ||
				strings.EqualFold(src.ID, slug) ||
				strings.EqualFold(src.RawFilename, rawID) ||
				strings.EqualFold(src.CSVFilename, rawID) {
				return src, ext, true
			}
		}
	}
	return reference.ArchivedSource{}, "", false
}

func parseBoundedLimit(raw string, defaultLimit, maxLimit int) int {
	if raw == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}
