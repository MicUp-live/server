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
type resolvedSourceMeta struct {
	fetchedAt      string
	etag           string
	lastMod        string
	origin         string
	stale          bool
	fallbackReason string
}

func FetchOrResolveSource(ctx context.Context, src SourceConfig, prev *ArchivedSource, opts FetchOptions) (*ResolvedRawSource, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowRFC3339 := now.UTC().Format(time.RFC3339)
	ext, _, _ := RawExtAndContentType(src.Format)

	// 1. Checked-in local file (e.g., historical-discs) unless missing locally.
	if strings.TrimSpace(src.LocalPath) != "" {
		if res, err := resolveLocalSource(ctx, src, ext, prev, opts, nowRFC3339); res != nil || err != nil {
			return res, err
		}
	}

	// 2. Offline mode: never perform upstream HTTP requests; load from S3 or local archive cache.
	// Note: allowLegacyFlat=false for billboard_csv so offline runs cannot promote v1's lossy deduplicated CSV into raw/.
	if opts.Offline {
		return resolveOfflineSource(ctx, src, ext, prev, opts, now)
	}

	// 3. Immutable lifecycle: reuse S3/local cache before touching upstream URL.
	// Note: allowLegacyFlat=false for billboard_csv so v1->v2 migration fetches true upstream weekly chart CSVs once
	// instead of locking in v1's lossy deduplicated WriteTracksCSV output.
	if src.Lifecycle == LifecycleImmutable {
		if res, err := resolveImmutableCache(ctx, src, ext, prev, opts, nowRFC3339); res != nil || err != nil {
			return res, err
		}
	}

	// 4. Upstream HTTP fetch (with conditional ETag / If-Modified-Since for rolling sources).
	if strings.TrimSpace(src.URL) == "" {
		return nil, fmt.Errorf("source %q has no URL or available cached file", src.ID)
	}
	return fetchOrFallbackUpstream(ctx, src, ext, prev, opts, nowRFC3339)
}

func buildResolvedRawSource(src SourceConfig, raw []byte, nowRFC3339 string, meta resolvedSourceMeta) (*ResolvedRawSource, error) {
	if err := ValidateMagicBytes(src.Format, raw); err != nil {
		return nil, fmt.Errorf("source %q magic-byte validation failed: %w", src.ID, err)
	}
	ext, contentType, shortFmt := RawExtAndContentType(src.Format)
	rawKey, rawFilename, fullSHA := ContentAddressedRawKey(src.ID, ext, raw)
	fetchedAt := meta.fetchedAt
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
		ETag:           meta.etag,
		LastModified:   meta.lastMod,
		Origin:         meta.origin,
		Stale:          meta.stale,
		FallbackReason: meta.fallbackReason,
	}, nil
}

func prevFetchedAtOrNow(prev *ArchivedSource, nowRFC3339 string) string {
	if prev != nil && prev.FetchedAt != "" {
		return prev.FetchedAt
	}
	return nowRFC3339
}

func resolveLocalSource(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, nowRFC3339 string) (*ResolvedRawSource, error) {
	raw, err := os.ReadFile(src.LocalPath)
	if err == nil {
		fetchedAt := nowRFC3339
		if prev != nil && prev.FetchedAt != "" {
			sumKey, _, fullSHA := ContentAddressedRawKey(src.ID, ext, raw)
			if prev.RawSHA256 == fullSHA || prev.RawKey == sumKey {
				fetchedAt = prev.FetchedAt
			}
		}
		return buildResolvedRawSource(src, raw, nowRFC3339, resolvedSourceMeta{
			fetchedAt: fetchedAt,
			origin:    OriginLocalFile,
		})
	}
	if src.URL != "" {
		return nil, nil
	}
	if cached, origin, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, true); cacheErr == nil {
		return buildResolvedRawSource(src, cached, nowRFC3339, resolvedSourceMeta{
			fetchedAt: prevFetchedAtOrNow(prev, nowRFC3339),
			origin:    origin,
		})
	}
	return nil, fmt.Errorf("read local source %q (%s): %w", src.ID, src.LocalPath, err)
}

func resolveOfflineSource(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, now time.Time) (*ResolvedRawSource, error) {
	nowRFC3339 := now.UTC().Format(time.RFC3339)
	allowLegacyFlat := src.Format != "billboard_csv"
	raw, origin, err := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat)
	if err != nil {
		return nil, fmt.Errorf("offline mode: cached raw bytes not found for %q: %w", src.ID, err)
	}
	meta := resolvedSourceMeta{
		fetchedAt: nowRFC3339,
		origin:    origin,
	}
	if prev != nil {
		meta.fetchedAt = prevFetchedAtOrNow(prev, nowRFC3339)
		meta.etag = prev.ETag
		meta.lastMod = prev.LastModified
		meta.stale = prev.Stale
		meta.fallbackReason = prev.FallbackReason
	}
	if src.Lifecycle == LifecycleRolling && src.MaxStaleDays > 0 {
		if fetchedTime, tErr := time.Parse(time.RFC3339, meta.fetchedAt); tErr != nil || now.Sub(fetchedTime).Hours()/24.0 > float64(src.MaxStaleDays) {
			meta.stale = true
			if meta.fallbackReason == "" {
				meta.fallbackReason = "offline cache exceeded max_stale_days"
			}
		}
	}
	return buildResolvedRawSource(src, raw, nowRFC3339, meta)
}

func resolveImmutableCache(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, nowRFC3339 string) (*ResolvedRawSource, error) {
	allowLegacyFlat := src.Format != "billboard_csv"
	raw, origin, err := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat)
	if err != nil {
		return nil, nil
	}
	meta := resolvedSourceMeta{
		fetchedAt: prevFetchedAtOrNow(prev, nowRFC3339),
		origin:    origin,
	}
	if prev != nil {
		meta.etag = prev.ETag
		meta.lastMod = prev.LastModified
	}
	return buildResolvedRawSource(src, raw, nowRFC3339, meta)
}

func fetchOrFallbackUpstream(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, nowRFC3339 string) (*ResolvedRawSource, error) {
	raw, status, etag, lastMod, fetchErr := fetchUpstreamWithRetries(ctx, src, prev, opts)
	if fetchErr == nil && status == http.StatusNotModified {
		if res, ok := resolveNotModifiedCache(ctx, src, ext, prev, opts, etag, lastMod, nowRFC3339); ok {
			return res, nil
		}
		// Cached body for 304 Not Modified is missing or mismatched; re-fetch unconditionally without ETag/If-Modified-Since.
		raw, status, etag, lastMod, fetchErr = fetchUpstreamWithRetries(ctx, src, nil, opts)
		if fetchErr == nil && status != http.StatusOK {
			fetchErr = fmt.Errorf("unconditional retry after 304 cache miss returned HTTP %d", status)
		}
	}

	if fetchErr == nil {
		res, valErr := buildResolvedRawSource(src, raw, nowRFC3339, resolvedSourceMeta{
			fetchedAt: nowRFC3339,
			etag:      etag,
			lastMod:   lastMod,
			origin:    OriginHTTP200,
		})
		if valErr == nil {
			return res, nil
		}
		fetchErr = valErr
	}

	// 5. Upstream fetch or magic-byte check failed: attempt S3 / local cache fallback if available.
	return resolveCachedFallback(ctx, src, ext, prev, opts, nowRFC3339, fetchErr)
}

func resolveNotModifiedCache(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, etag, lastMod, nowRFC3339 string) (*ResolvedRawSource, bool) {
	cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, src.Format != "billboard_csv")
	if cacheErr != nil {
		return nil, false
	}
	if prev != nil && prev.RawSHA256 != "" {
		if _, _, sha := ContentAddressedRawKey(src.ID, ext, cachedRaw); sha != prev.RawSHA256 {
			return nil, false
		}
	}
	if prev != nil {
		if etag == "" {
			etag = prev.ETag
		}
		if lastMod == "" {
			lastMod = prev.LastModified
		}
	}
	res, err := buildResolvedRawSource(src, cachedRaw, nowRFC3339, resolvedSourceMeta{
		fetchedAt: nowRFC3339,
		etag:      etag,
		lastMod:   lastMod,
		origin:    OriginHTTP304,
	})
	return res, err == nil
}

func resolveCachedFallback(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, opts FetchOptions, nowRFC3339 string, fetchErr error) (*ResolvedRawSource, error) {
	allowLegacyFlat := src.Format != "billboard_csv"
	cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prev, opts.Store, opts.ArchiveDir, allowLegacyFlat)
	if cacheErr != nil {
		return nil, fmt.Errorf("fetch source %q failed and no cached fallback is available: %w", src.ID, fetchErr)
	}
	meta := resolvedSourceMeta{
		fetchedAt:      prevFetchedAtOrNow(prev, nowRFC3339),
		origin:         OriginS3Fallback,
		stale:          src.Lifecycle == LifecycleRolling,
		fallbackReason: fetchErr.Error(),
	}
	if prev != nil && prev.RawSHA256 != "" {
		_, _, cachedSHA := ContentAddressedRawKey(src.ID, ext, cachedRaw)
		if prev.RawSHA256 == cachedSHA {
			meta.etag = prev.ETag
			meta.lastMod = prev.LastModified
		}
	}
	return buildResolvedRawSource(src, cachedRaw, nowRFC3339, meta)
}

func normalizeFetchDefaults(opts FetchOptions) (*http.Client, int, time.Duration) {
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
	return client, maxRetries, retryDelay
}

func fetchUpstreamWithRetries(ctx context.Context, src SourceConfig, prev *ArchivedSource, opts FetchOptions) (raw []byte, status int, etag string, lastMod string, err error) {
	client, maxRetries, retryDelay := normalizeFetchDefaults(opts)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var retryable bool
		raw, status, etag, lastMod, retryable, err = fetchUpstreamAttempt(ctx, client, src, prev)
		if err == nil || !retryable {
			return raw, status, etag, lastMod, err
		}
		if attempt < maxRetries {
			sleepWithContext(ctx, retryDelay*time.Duration(1<<(attempt-1)))
		}
	}
	return nil, status, "", "", err
}

func resolveAttemptURL(ctx context.Context, client *http.Client, src SourceConfig) (string, error) {
	fetchURL := src.URL
	if src.Format == "karafun_csv" && strings.HasSuffix(strings.ToLower(fetchURL), ".html") {
		return ResolveKaraFunCSVURL(ctx, client, fetchURL)
	}
	return fetchURL, nil
}

func buildUpstreamRequest(ctx context.Context, fetchURL string, src SourceConfig, prev *ArchivedSource) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
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
	return req, nil
}

func readAndValidateResponse(resp *http.Response, src SourceConfig) (raw []byte, retryable bool, err error) {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxRemoteBodyBytes+1))
	if readErr != nil {
		return nil, true, fmt.Errorf("read response body: %w", readErr)
	}
	if int64(len(body)) > MaxRemoteBodyBytes {
		return nil, false, fmt.Errorf("upstream response exceeds %d bytes", MaxRemoteBodyBytes)
	}
	if valErr := ValidateMagicBytes(src.Format, body); valErr != nil {
		detail := ""
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
			detail = fmt.Sprintf(", HTML marker %q", invalidHTMLKind(body))
		}
		return nil, true, fmt.Errorf("magic-byte validation failed: %w (HTTP %d, content-type %q, %d bytes, response host %q%s)",
			valErr, resp.StatusCode, resp.Header.Get("Content-Type"), len(body), resp.Request.URL.Hostname(), detail)
	}
	return body, false, nil
}

func fetchUpstreamAttempt(ctx context.Context, client *http.Client, src SourceConfig, prev *ArchivedSource) (raw []byte, status int, etag string, lastMod string, retryable bool, err error) {
	fetchURL, resolveErr := resolveAttemptURL(ctx, client, src)
	if resolveErr != nil {
		return nil, 0, "", "", true, resolveErr
	}
	req, reqErr := buildUpstreamRequest(ctx, fetchURL, src, prev)
	if reqErr != nil {
		return nil, 0, "", "", false, reqErr
	}
	resp, doErr := client.Do(req)
	if doErr != nil {
		return nil, 0, "", "", true, doErr
	}
	defer resp.Body.Close()

	respETag := strings.TrimSpace(resp.Header.Get("ETag"))
	respLastMod := strings.TrimSpace(resp.Header.Get("Last-Modified"))

	if resp.StatusCode == http.StatusNotModified {
		return nil, http.StatusNotModified, respETag, respLastMod, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, "", "", true, fmt.Errorf("HTTP %d from %s", resp.StatusCode, fetchURL)
	}
	body, retryable, bodyErr := readAndValidateResponse(resp, src)
	if bodyErr != nil {
		return nil, resp.StatusCode, "", "", retryable, bodyErr
	}
	return body, http.StatusOK, respETag, respLastMod, false, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func isValidCachedRaw(src SourceConfig, ext string, prev *ArchivedSource, data []byte) bool {
	if len(data) == 0 || ValidateMagicBytes(src.Format, data) != nil {
		return false
	}
	if prev != nil && prev.RawSHA256 != "" {
		_, _, sha := ContentAddressedRawKey(src.ID, ext, data)
		if sha != prev.RawSHA256 {
			return false
		}
	}
	return true
}

func loadPrevReferencedRaw(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, store Store, archiveDir string, allowLegacyFlat bool) ([]byte, string, bool) {
	if prev == nil || strings.TrimSpace(prev.RawKey) == "" {
		return nil, "", false
	}
	isRawAddr := strings.Contains(prev.RawKey, "/raw/")
	if !isRawAddr && !allowLegacyFlat {
		return nil, "", false
	}
	if store != nil {
		if obj, err := store.GetObject(ctx, prev.RawKey, 0); err == nil && isValidCachedRaw(src, ext, prev, obj.Body) {
			return obj.Body, OriginS3Cache, true
		}
	}
	if strings.TrimSpace(archiveDir) != "" && prev.RawFilename != "" && isRawAddr {
		p1 := filepath.Join(archiveDir, "raw", src.ID, filepath.Base(prev.RawFilename))
		if data, err := os.ReadFile(p1); err == nil && isValidCachedRaw(src, ext, prev, data) {
			return data, OriginLocalCache, true
		}
	}
	return nil, "", false
}

func loadLocalRawSubdir(src SourceConfig, ext string, prev *ArchivedSource, archiveDir string) ([]byte, string, bool) {
	if strings.TrimSpace(archiveDir) == "" {
		return nil, "", false
	}
	rawSubdir := filepath.Join(archiveDir, "raw", src.ID)
	entries, err := os.ReadDir(rawSubdir)
	if err != nil {
		return nil, "", false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ext) {
			continue
		}
		if data, rErr := os.ReadFile(filepath.Join(rawSubdir, e.Name())); rErr == nil && isValidCachedRaw(src, ext, prev, data) {
			return data, OriginLocalCache, true
		}
	}
	return nil, "", false
}

func loadLegacyFlatRaw(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, store Store, archiveDir string) ([]byte, string, bool) {
	if store != nil {
		flatKey := SourcesS3Prefix + src.ID + ext
		if obj, err := store.GetObject(ctx, flatKey, 0); err == nil && isValidCachedRaw(src, ext, prev, obj.Body) {
			return obj.Body, OriginS3Cache, true
		}
	}
	if strings.TrimSpace(archiveDir) != "" {
		flatPath := filepath.Join(archiveDir, src.ID+ext)
		if data, err := os.ReadFile(flatPath); err == nil && isValidCachedRaw(src, ext, prev, data) {
			return data, OriginLocalCache, true
		}
	}
	return nil, "", false
}

func loadCachedRawBytes(ctx context.Context, src SourceConfig, ext string, prev *ArchivedSource, store Store, archiveDir string, allowLegacyFlat bool) ([]byte, string, error) {
	// 1. Authoritative content-addressed object referenced by prev.RawKey in S3 / Store (or matching local raw/<id>/<RawFilename>).
	if data, origin, ok := loadPrevReferencedRaw(ctx, src, ext, prev, store, archiveDir, allowLegacyFlat); ok {
		return data, origin, nil
	}
	// 2. Content-addressed local archiveDir/raw/<id>/ scan (only when prev.RawSHA256 is unset or matches).
	if data, origin, ok := loadLocalRawSubdir(src, ext, prev, archiveDir); ok {
		return data, origin, nil
	}
	// 3. Legacy v1 flat key fallback (reference/v1/sources/<id><ext> or archiveDir/<id><ext>) when permitted.
	if allowLegacyFlat {
		if data, origin, ok := loadLegacyFlatRaw(ctx, src, ext, prev, store, archiveDir); ok {
			return data, origin, nil
		}
	}
	return nil, "", os.ErrNotExist
}
