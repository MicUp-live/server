package reference

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	MaxRemoteBodyBytes = 128 << 20 // 128 MiB limit on upstream catalog downloads
	defaultUserAgent   = "MicUp-Reference-Compiler/2.0"
)

var karaFunCSVLinkRe = regexp.MustCompile(`https://www\.karafun\.com/cl/3107312/[0-9a-f]{32}/`)

func invalidHTMLKind(body []byte) string {
	sample := body
	if len(sample) > 64<<10 {
		sample = sample[:64<<10]
	}
	text := strings.ToLower(string(sample))
	switch {
	case strings.Contains(text, "captcha"), strings.Contains(text, "challenge-platform"), strings.Contains(text, "client challenge"), strings.Contains(text, "just a moment"):
		return "challenge"
	case strings.Contains(text, "access denied"), strings.Contains(text, "forbidden"), strings.Contains(text, "blocked"):
		return "access denied"
	case strings.Contains(text, "sign in"), strings.Contains(text, "log in"), strings.Contains(text, "login"):
		return "login"
	default:
		return "unclassified"
	}
}

// ResolvedRawSource contains the validated raw upstream bytes and provenance metadata for one source.
type ResolvedRawSource struct {
	RawBytes       []byte
	RawKey         string
	RawFilename    string
	RawSHA256      string
	RawContentType string
	ShortFormat    string
	FetchedAt      string
	ETag           string
	LastModified   string
	Origin         string
	Stale          bool
	FallbackReason string
}

// FetchOptions configures how FetchOrResolveSource resolves raw source bytes.
type FetchOptions struct {
	HTTPClient *http.Client
	Store      Store
	ArchiveDir string
	Offline    bool
	Now        time.Time
	RetryDelay time.Duration
	MaxRetries int
}

// ValidateMagicBytes checks that rawBytes match the expected format (PDF, XLSX, or non-HTML CSV).
func ValidateMagicBytes(format string, rawBytes []byte) error {
	if len(rawBytes) == 0 {
		return fmt.Errorf("empty payload (0 bytes)")
	}
	_, _, shortFmt := RawExtAndContentType(format)
	switch shortFmt {
	case "pdf":
		if len(rawBytes) < 5 || !bytes.Equal(rawBytes[:5], []byte("%PDF-")) {
			return fmt.Errorf("invalid PDF magic bytes (expected %%PDF- header)")
		}
	case "xlsx":
		if len(rawBytes) < 4 || !bytes.Equal(rawBytes[:4], []byte{'P', 'K', 0x03, 0x04}) {
			return fmt.Errorf("invalid XLSX magic bytes (expected PK\\x03\\x04 zip header)")
		}
	default:
		sample := rawBytes
		if len(sample) > 512 {
			sample = sample[:512]
		}
		trimmed := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(sample), "\ufeff")))
		if strings.HasPrefix(trimmed, "<!doctype html") ||
			strings.HasPrefix(trimmed, "<html") ||
			strings.HasPrefix(trimmed, "<!--") ||
			strings.HasPrefix(trimmed, "<?xml") {
			return fmt.Errorf("invalid CSV payload: upstream returned HTML/XML page instead of CSV")
		}
	}
	return nil
}

// ResolveKaraFunCSVURL scrapes the current dynamic CSV export link from the KaraFun song list HTML page.
// Unlike v1, it returns an explicit error if the link cannot be found instead of silently using a stale hardcoded hash.
func ResolveKaraFunCSVURL(ctx context.Context, client *http.Client, pageURL string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("build karafun landing page request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch karafun landing page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("karafun landing page returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("read karafun landing page: %w", err)
	}
	match := karaFunCSVLinkRe.FindString(string(body))
	if match == "" {
		return "", fmt.Errorf("could not locate KaraFun CSV download URL matching %s on %s", karaFunCSVLinkRe.String(), pageURL)
	}
	return match, nil
}

// FetchOrResolveSource resolves the raw bytes for src according to its v2 lifecycle ("immutable" vs "rolling"),
// offline mode, conditional HTTP headers, and S3/local archive fallback.
func FetchOrResolveSource(ctx context.Context, src SourceConfig, prev *ArchivedSource, opts FetchOptions) (*ResolvedRawSource, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowRFC3339 := now.UTC().Format(time.RFC3339)
	ext, contentType, shortFmt := RawExtAndContentType(src.Format)

	buildResult := func(raw []byte, fetchedAt, etag, lastMod, origin string, stale bool, fallbackReason string) (*ResolvedRawSource, error) {
		if err := ValidateMagicBytes(src.Format, raw); err != nil {
			return nil, fmt.Errorf("source %q magic-byte validation failed: %w", src.ID, err)
		}
		rawKey, rawFilename, fullSHA := ContentAddressedRawKey(src.ID, ext, raw)
		if fetchedAt == "" {
			fetchedAt = nowRFC3339
		}
		return &ResolvedRawSource{
			RawBytes:       raw,
			RawKey:         rawKey,
			RawFilename:    rawFilename,
			RawSHA256:      fullSHA,
			RawContentType: contentType,
			ShortFormat:    shortFmt,
			FetchedAt:      fetchedAt,
			ETag:           etag,
			LastModified:   lastMod,
			Origin:         origin,
			Stale:          stale,
			FallbackReason: fallbackReason,
		}, nil
	}

	// 1. Checked-in local file (e.g., historical-discs) unless missing locally.
	if strings.TrimSpace(src.LocalPath) != "" {
		if raw, err := os.ReadFile(src.LocalPath); err == nil {
			fetchedAt := nowRFC3339
			if prev != nil && prev.FetchedAt != "" {
				sumKey, _, fullSHA := ContentAddressedRawKey(src.ID, ext, raw)
				if prev.RawSHA256 == fullSHA || prev.RawKey == sumKey {
					fetchedAt = prev.FetchedAt
				}
			}
			return buildResult(raw, fetchedAt, "", "", OriginLocalFile, false, "")
		} else if src.URL == "" {
			if raw, origin, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, true); cacheErr == nil {
				prevFetched := nowRFC3339
				if prev != nil && prev.FetchedAt != "" {
					prevFetched = prev.FetchedAt
				}
				return buildResult(raw, prevFetched, "", "", origin, false, "")
			}
			return nil, fmt.Errorf("read local source %q (%s): %w", src.ID, src.LocalPath, err)
		}
	}

	// 2. Offline mode: never perform upstream HTTP requests; load from S3 or local archive cache.
	// Note: allowLegacyFlat=false for billboard_csv so offline runs cannot promote v1's lossy deduplicated CSV into raw/.
	if opts.Offline {
		allowLegacyFlat := src.Format != "billboard_csv"
		raw, origin, err := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat)
		if err != nil {
			return nil, fmt.Errorf("offline mode: cached raw bytes not found for %q: %w", src.ID, err)
		}
		fetchedAt, etag, lastMod := nowRFC3339, "", ""
		var stale bool
		var reason string
		if prev != nil {
			if prev.FetchedAt != "" {
				fetchedAt = prev.FetchedAt
			}
			etag = prev.ETag
			lastMod = prev.LastModified
			stale = prev.Stale
			reason = prev.FallbackReason
		}
		if src.Lifecycle == LifecycleRolling && src.MaxStaleDays > 0 {
			if fetchedTime, tErr := time.Parse(time.RFC3339, fetchedAt); tErr != nil || now.Sub(fetchedTime).Hours()/24.0 > float64(src.MaxStaleDays) {
				stale = true
				if reason == "" {
					reason = "offline cache exceeded max_stale_days"
				}
			}
		}
		return buildResult(raw, fetchedAt, etag, lastMod, origin, stale, reason)
	}

	// 3. Immutable lifecycle: reuse S3/local cache before touching upstream URL.
	// Note: allowLegacyFlat=false for billboard_csv so v1->v2 migration fetches true upstream weekly chart CSVs once
	// instead of locking in v1's lossy deduplicated WriteTracksCSV output.
	if src.Lifecycle == LifecycleImmutable {
		allowLegacyFlat := src.Format != "billboard_csv"
		if raw, origin, err := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat); err == nil {
			fetchedAt, etag, lastMod := nowRFC3339, "", ""
			if prev != nil {
				if prev.FetchedAt != "" {
					fetchedAt = prev.FetchedAt
				}
				etag = prev.ETag
				lastMod = prev.LastModified
			}
			return buildResult(raw, fetchedAt, etag, lastMod, origin, false, "")
		}
	}

	// 4. Upstream HTTP fetch (with conditional ETag / If-Modified-Since for rolling sources).
	if strings.TrimSpace(src.URL) == "" {
		return nil, fmt.Errorf("source %q has no URL or available cached file", src.ID)
	}

	raw, status, etag, lastMod, fetchErr := fetchUpstreamWithRetries(ctx, src, prev, opts)
	if fetchErr == nil && status == http.StatusNotModified {
		cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, src.Format != "billboard_csv")
		if cacheErr == nil && prev != nil && prev.RawSHA256 != "" {
			if _, _, sha := ContentAddressedRawKey(src.ID, ext, cachedRaw); sha != prev.RawSHA256 {
				cacheErr = fmt.Errorf("cached SHA-256 %s does not match prior manifest SHA-256 %s", sha[:16], prev.RawSHA256[:16])
			}
		}
		if cacheErr == nil {
			if etag == "" && prev != nil {
				etag = prev.ETag
			}
			if lastMod == "" && prev != nil {
				lastMod = prev.LastModified
			}
			return buildResult(cachedRaw, nowRFC3339, etag, lastMod, OriginHTTP304, false, "")
		}
		// Cached body for 304 Not Modified is missing or mismatched; re-fetch unconditionally without ETag/If-Modified-Since.
		raw, status, etag, lastMod, fetchErr = fetchUpstreamWithRetries(ctx, src, nil, opts)
		if fetchErr == nil && status != http.StatusOK {
			fetchErr = fmt.Errorf("unconditional retry after 304 cache miss returned HTTP %d", status)
		}
	}

	if fetchErr == nil {
		res, valErr := buildResult(raw, nowRFC3339, etag, lastMod, OriginHTTP200, false, "")
		if valErr == nil {
			return res, nil
		}
		fetchErr = valErr
	}

	// 5. Upstream fetch or magic-byte check failed: attempt S3 / local cache fallback if available.
	allowLegacyFlat := src.Format != "billboard_csv"
	if cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat); cacheErr == nil {
		fetchedAt := nowRFC3339
		prevETag, prevLastMod := "", ""
		if prev != nil {
			if prev.FetchedAt != "" {
				fetchedAt = prev.FetchedAt
			}
			_, _, cachedSHA := ContentAddressedRawKey(src.ID, ext, cachedRaw)
			if prev.RawSHA256 != "" && prev.RawSHA256 == cachedSHA {
				prevETag = prev.ETag
				prevLastMod = prev.LastModified
			}
		}
		stale := src.Lifecycle == LifecycleRolling
		return buildResult(cachedRaw, fetchedAt, prevETag, prevLastMod, OriginS3Fallback, stale, fetchErr.Error())
	}

	return nil, fmt.Errorf("fetch source %q failed and no cached fallback is available: %w", src.ID, fetchErr)
}

func fetchUpstreamWithRetries(ctx context.Context, src SourceConfig, prev *ArchivedSource, opts FetchOptions) (raw []byte, status int, etag string, lastMod string, err error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	retryDelay := opts.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 500 * time.Millisecond
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fetchURL := src.URL
		if src.Format == "karafun_csv" && strings.HasSuffix(strings.ToLower(fetchURL), ".html") {
			resolvedURL, resolveErr := ResolveKaraFunCSVURL(ctx, client, fetchURL)
			if resolveErr != nil {
				err = resolveErr
				if attempt < maxRetries {
					sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
					continue
				}
				return nil, 0, "", "", err
			}
			fetchURL = resolvedURL
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if reqErr != nil {
			return nil, 0, "", "", reqErr
		}
		req.Header.Set("User-Agent", defaultUserAgent)

		if src.Lifecycle == LifecycleRolling && prev != nil {
			if prev.ETag != "" {
				req.Header.Set("If-None-Match", prev.ETag)
			}
			if prev.LastModified != "" {
				req.Header.Set("If-Modified-Since", prev.LastModified)
			}
		}

		resp, doErr := client.Do(req)
		if doErr != nil {
			err = doErr
			if attempt < maxRetries {
				sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
				continue
			}
			return nil, 0, "", "", err
		}

		respETag := strings.TrimSpace(resp.Header.Get("ETag"))
		respLastMod := strings.TrimSpace(resp.Header.Get("Last-Modified"))

		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			return nil, http.StatusNotModified, respETag, respLastMod, nil
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d from %s", resp.StatusCode, fetchURL)
			if attempt < maxRetries {
				sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
				continue
			}
			return nil, resp.StatusCode, "", "", err
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxRemoteBodyBytes+1))
		resp.Body.Close()
		if readErr != nil {
			err = fmt.Errorf("read response body: %w", readErr)
			if attempt < maxRetries {
				sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
				continue
			}
			return nil, resp.StatusCode, "", "", err
		}
		if int64(len(body)) > MaxRemoteBodyBytes {
			return nil, resp.StatusCode, "", "", fmt.Errorf("upstream response exceeds %d bytes", MaxRemoteBodyBytes)
		}
		if valErr := ValidateMagicBytes(src.Format, body); valErr != nil {
			detail := ""
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				detail = fmt.Sprintf(", HTML marker %q", invalidHTMLKind(body))
			}
			err = fmt.Errorf("magic-byte validation failed: %w (HTTP %d, content-type %q, %d bytes, response host %q%s)",
				valErr, resp.StatusCode, resp.Header.Get("Content-Type"), len(body), resp.Request.URL.Hostname(), detail)
			if attempt < maxRetries {
				sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
				continue
			}
			return nil, resp.StatusCode, "", "", err
		}
		return body, http.StatusOK, respETag, respLastMod, nil
	}
	return nil, 0, "", "", err
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func loadCachedRawBytes(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, store Store, archiveDir string, allowLegacyFlat bool) ([]byte, string, error) {
	id := src.ID
	accept := func(data []byte, requirePrevSHA bool) bool {
		if len(data) == 0 || ValidateMagicBytes(src.Format, data) != nil {
			return false
		}
		if requirePrevSHA && prev != nil && prev.RawSHA256 != "" {
			_, _, sha := ContentAddressedRawKey(id, ext, data)
			if sha != prev.RawSHA256 {
				return false
			}
		}
		return true
	}

	// 1. Authoritative content-addressed object referenced by prev.RawKey in S3 / Store (or matching local raw/<id>/<RawFilename>).
	if prev != nil && strings.TrimSpace(prev.RawKey) != "" {
		isRawAddr := strings.Contains(prev.RawKey, "/raw/")
		if isRawAddr || allowLegacyFlat {
			if store != nil {
				if obj, err := store.GetObject(ctx, prev.RawKey, 0); err == nil && accept(obj.Body, true) {
					return obj.Body, OriginS3Cache, nil
				}
			}
			if strings.TrimSpace(archiveDir) != "" && prev.RawFilename != "" && isRawAddr {
				p1 := filepath.Join(archiveDir, "raw", id, filepath.Base(prev.RawFilename))
				if data, err := os.ReadFile(p1); err == nil && accept(data, true) {
					return data, OriginLocalCache, nil
				}
			}
		}
	}

	// 2. Content-addressed local archiveDir/raw/<id>/ scan (only when prev.RawSHA256 is unset or matches).
	if strings.TrimSpace(archiveDir) != "" {
		rawSubdir := filepath.Join(archiveDir, "raw", id)
		if entries, err := os.ReadDir(rawSubdir); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				e := entries[i]
				if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ext) {
					if data, rErr := os.ReadFile(filepath.Join(rawSubdir, e.Name())); rErr == nil && accept(data, true) {
						return data, OriginLocalCache, nil
					}
				}
			}
		}
	}

	// 3. Legacy v1 flat key fallback (reference/v1/sources/<id><ext> or archiveDir/<id><ext>) when permitted.
	if allowLegacyFlat {
		if store != nil {
			flatKey := SourcesS3Prefix + id + ext
			if obj, err := store.GetObject(ctx, flatKey, 0); err == nil && accept(obj.Body, true) {
				return obj.Body, OriginS3Cache, nil
			}
		}
		if strings.TrimSpace(archiveDir) != "" {
			flatPath := filepath.Join(archiveDir, id+ext)
			if data, err := os.ReadFile(flatPath); err == nil && accept(data, true) {
				return data, OriginLocalCache, nil
			}
		}
	}

	return nil, "", os.ErrNotExist
}
