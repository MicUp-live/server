package handlers

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"micup.live/server/internal/reference"
)

func isValidSourceID(rawID string) bool {
	return rawID != "" && !strings.Contains(rawID, "..") && !strings.Contains(rawID, "/")
}

func resolveSourceDownloadTarget(src reference.ArchivedSource, format string) (targetKey, filename, contentType string) {
	targetKey = src.RawKey
	filename = normalizeSourceFilename(src.ID, src.RawFilename)
	contentType = src.RawContentType
	if format == "csv" && src.CSVKey != "" {
		targetKey = src.CSVKey
		filename = src.CSVFilename
		contentType = "text/csv; charset=utf-8"
	}
	return targetKey, filename, contentType
}

func normalizeSourceFilename(sourceID, filename string) string {
	if sourceID == "" || filename == "" {
		return filename
	}
	ext := filepath.Ext(filename)
	if ext != "" && !strings.EqualFold(filename, sourceID+ext) {
		return sourceID + ext
	}
	return filename
}

func (h *ReferenceHandler) resolveSourceDownloadURL(ctx context.Context, targetKey string) string {
	downloadURL := "/" + strings.TrimPrefix(targetKey, "/")
	if h.Store == nil {
		return downloadURL
	}
	if presigned, err := h.Store.PresignGet(ctx, targetKey); err == nil && presigned != "" {
		return presigned
	}
	return downloadURL
}

func sortedTrackMapKeys(tracksMap map[string]reference.CanonicalTrack) []string {
	keys := make([]string, 0, len(tracksMap))
	for k := range tracksMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func searchDictionaryTracks(tracksMap map[string]reference.CanonicalTrack, q, artist, title string, limit int) []reference.CanonicalTrack {
	matches := make([]reference.CanonicalTrack, 0, limit)
	// 1. Exact composite key match when both artist and title are provided.
	if q == "" && artist != "" && title != "" {
		key := reference.CompositeKey(artist, title)
		if exact, ok := tracksMap[key]; ok {
			return append(matches, exact)
		}
	}

	// 2. Substring search across normalized artist/title when needed.
	qLower := strings.ToLower(q)
	artistLower := strings.ToLower(artist)
	titleLower := strings.ToLower(title)

	for _, k := range sortedTrackMapKeys(tracksMap) {
		t := tracksMap[k]
		if !matchesDictionaryQuery(t, k, qLower, artistLower, titleLower) {
			continue
		}
		matches = append(matches, t)
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

func matchesDictionaryQuery(t reference.CanonicalTrack, key, qLower, artistLower, titleLower string) bool {
	artistVal := strings.ToLower(t.Artist)
	titleVal := strings.ToLower(t.Title)
	if qLower != "" && !strings.Contains(artistVal, qLower) && !strings.Contains(titleVal, qLower) && !strings.Contains(key, qLower) {
		return false
	}
	if artistLower != "" && !strings.Contains(artistVal, artistLower) {
		return false
	}
	if titleLower != "" && !strings.Contains(titleVal, titleLower) {
		return false
	}
	return true
}

func searchDiscRecords(discsMap map[string]reference.CanonicalTrack, q, provider string, limit int) []discRecord {
	qLower := strings.ToLower(q)
	qNormDisc := strings.ToLower(reference.NormalizeDiscCode(q))
	provLower := strings.ToLower(provider)

	matches := make([]discRecord, 0, limit)
	for _, code := range sortedTrackMapKeys(discsMap) {
		t := discsMap[code]
		if !matchesDiscQuery(t, code, provider, provLower, qLower, qNormDisc) {
			continue
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
	return matches
}

func matchesDiscQuery(t reference.CanonicalTrack, code, provider, provLower, qLower, qNormDisc string) bool {
	if provLower != "" && !strings.EqualFold(t.Source, provider) && !strings.Contains(strings.ToLower(t.Source), provLower) {
		return false
	}
	if qLower == "" {
		return true
	}
	codeLower := strings.ToLower(code)
	return strings.Contains(codeLower, qLower) ||
		(qNormDisc != "" && strings.Contains(codeLower, qNormDisc)) ||
		strings.Contains(strings.ToLower(t.Artist), qLower) ||
		strings.Contains(strings.ToLower(t.Title), qLower)
}
