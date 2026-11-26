package reference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	objects map[string]*ObjectResult
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects: make(map[string]*ObjectResult),
	}
}

func (f *fakeStore) put(key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(body))
	copy(cp, body)
	f.objects[strings.TrimPrefix(key, "/")] = &ObjectResult{
		Body: cp,
		Size: int64(len(cp)),
	}
}

func (f *fakeStore) GetObject(_ context.Context, key string, _ int64) (*ObjectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[strings.TrimPrefix(key, "/")]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := make([]byte, len(obj.Body))
	copy(cp, obj.Body)
	return &ObjectResult{
		Body:        cp,
		ContentType: obj.ContentType,
		Size:        int64(len(cp)),
	}, nil
}

func (f *fakeStore) PresignGet(_ context.Context, key string) (string, error) {
	return "https://s3.example.com/" + strings.TrimPrefix(key, "/"), nil
}

func TestValidateMagicBytes(t *testing.T) {
	if err := ValidateMagicBytes("pdf", []byte("%PDF-1.5 content")); err != nil {
		t.Fatalf("expected valid PDF magic bytes, got %v", err)
	}
	if err := ValidateMagicBytes(".pdf", []byte("<html>404 Not Found</html>")); err == nil {
		t.Fatalf("expected HTML masquerading as .pdf to fail magic-byte check")
	}
	if err := ValidateMagicBytes(".xlsx", []byte{'P', 'K', 0x03, 0x04, 0x00}); err != nil {
		t.Fatalf("expected valid .xlsx zip magic bytes, got %v", err)
	}
	if err := ValidateMagicBytes("xlsx", []byte("not-a-zip")); err == nil {
		t.Fatalf("expected non-zip XLSX to fail magic-byte check")
	}
	if err := ValidateMagicBytes("zoom_csv", []byte("Artist,\"Song title\"\nQueen,\"Bohemian Rhapsody\"\n")); err != nil {
		t.Fatalf("expected valid CSV to pass magic-byte check, got %v", err)
	}
	if err := ValidateMagicBytes("zoom_csv", []byte("\ufeff  <!DOCTYPE html><html><body>Cloudflare Challenge</body></html>")); err == nil {
		t.Fatalf("expected UTF-8 BOM + HTML error page masquerading as CSV to fail magic-byte check")
	}
}

func TestFetchSourceRetriesInvalidResponse(t *testing.T) {
	raw := `Artist,"Song title"` + "\n" + `"Queen","We Will Rock You"` + "\n"
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>temporary error</html>"))
			return
		}
		_, _ = w.Write([]byte(raw))
	}))
	defer server.Close()
	src := SourceConfig{ID: "zoom", Format: "zoom_csv", URL: server.URL}
	result, err := FetchOrResolveSource(context.Background(), src, nil, FetchOptions{
		HTTPClient: server.Client(), MaxRetries: 2, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || string(result.RawBytes) != raw {
		t.Fatalf("expected retry to return CSV; hits=%d body=%q", hits.Load(), result.RawBytes)
	}
}

func TestFetchSourceReportsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Client Challenge</title> private-token-123"))
	}))
	defer server.Close()
	src := SourceConfig{ID: "zoom", Format: "zoom_csv", URL: server.URL}
	_, err := FetchOrResolveSource(context.Background(), src, nil, FetchOptions{
		HTTPClient: server.Client(), MaxRetries: 1,
	})
	if err == nil || !strings.Contains(err.Error(), `HTML marker "challenge"`) ||
		!strings.Contains(err.Error(), `response host "127.0.0.1"`) ||
		!strings.Contains(err.Error(), "magic-byte validation failed") ||
		strings.Contains(err.Error(), "private-token-123") {
		t.Fatalf("expected safe invalid response diagnostics, got %v", err)
	}
}

func TestResolveKaraFunCSVURL_ExplicitErrorOnMissingLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.html":
			_, _ = w.Write([]byte(`<a href="https://www.karafun.com/cl/3107312/0123456789abcdef0123456789abcdef/">Download CSV</a>`))
		default:
			_, _ = w.Write([]byte(`<html><body>Layout changed, no CSV link here</body></html>`))
		}
	}))
	defer srv.Close()

	got, err := ResolveKaraFunCSVURL(context.Background(), srv.Client(), srv.URL+"/ok.html")
	if err != nil {
		t.Fatalf("ResolveKaraFunCSVURL ok.html: %v", err)
	}
	if got != "https://www.karafun.com/cl/3107312/0123456789abcdef0123456789abcdef/" {
		t.Fatalf("unexpected resolved URL: %q", got)
	}

	if _, err := ResolveKaraFunCSVURL(context.Background(), srv.Client(), srv.URL+"/broken.html"); err == nil {
		t.Fatalf("expected explicit error when KaraFun HTML page lacks CSV download regex match")
	}
}

func TestPipeline_ImmutableCacheReuse_Rolling304_OutageFallback_AndGuardrails(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	var immutableHits atomic.Int32
	var rollingHits atomic.Int32
	var rollingMode atomic.Int32 // 0 = 200 OK, 1 = 304 Not Modified, 2 = 503 Outage, 3 = 200 HTML corrupt page

	immutableBody := "chart_week,current_week,title,performer\n1975-11-01,1,Bohemian Rhapsody,Queen\n1975-11-08,1,Bohemian Rhapsody,Queen\n1981-10-01,2,Don't Stop Believin',Journey\n"
	rollingBodyV1 := "Artist,\"Song title\"\n\"Queen\",\"We Will Rock You\"\n\"ABBA\",\"Dancing Queen\"\n\"Oasis\",\"Wonderwall\"\n"

	var hdrMu sync.Mutex
	var lastIfNoneMatch string
	var lastIfModSince string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/immutable.csv":
			immutableHits.Add(1)
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte(immutableBody))
		case "/rolling.csv":
			rollingHits.Add(1)
			hdrMu.Lock()
			lastIfNoneMatch = r.Header.Get("If-None-Match")
			lastIfModSince = r.Header.Get("If-Modified-Since")
			hdrMu.Unlock()
			switch rollingMode.Load() {
			case 0:
				w.Header().Set("ETag", `"v1-etag"`)
				w.Header().Set("Last-Modified", "Wed, 18 Nov 2026 06:00:00 GMT")
				_, _ = w.Write([]byte(rollingBodyV1))
			case 1:
				if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
					w.WriteHeader(http.StatusNotModified)
				} else {
					w.Header().Set("ETag", `"v1-etag"`)
					w.Header().Set("Last-Modified", "Wed, 18 Nov 2026 06:00:00 GMT")
					_, _ = w.Write([]byte(rollingBodyV1))
				}
			case 2:
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("upstream outage"))
			case 3:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("<!DOCTYPE html><html><body>502 Bad Gateway</body></html>"))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	localCSVPath := filepath.Join(tmpDir, "local_discs.csv")
	localCSVBody := "disc_code,artist,title,source\nSC8101-01,Queen,Bohemian Rhapsody,Sound Choice\nSC8101-02,Journey,Don't Stop Believin',Sound Choice\n"
	if err := os.WriteFile(localCSVPath, []byte(localCSVBody), 0644); err != nil {
		t.Fatalf("write local_discs.csv: %v", err)
	}

	sourcesJSONPath := filepath.Join(tmpDir, "sources.json")
	cfg := SourcesConfig{
		Version:          2,
		MaxGlobalDropPct: 5.0,
		Sources: []SourceConfig{
			{
				ID:         "billboard-country",
				Name:       "Billboard Country Frozen",
				Provider:   "Billboard Country",
				Genre:      "Country",
				Format:     "billboard_csv",
				Lifecycle:  LifecycleImmutable,
				Required:   true,
				URL:        srv.URL + "/immutable.csv",
				MinTracks:  2,
				MaxDropPct: 10.0,
			},
			{
				ID:           "zoom",
				Name:         "Zoom Rolling Catalog",
				Provider:     "Zoom",
				Format:       "zoom_csv",
				Lifecycle:    LifecycleRolling,
				Required:     true,
				URL:          srv.URL + "/rolling.csv",
				MinTracks:    3,
				MaxDropPct:   10.0,
				MaxStaleDays: 21,
			},
			{
				ID:         "historical-discs",
				Name:       "historical_discs.csv",
				Provider:   "Multi-Publisher",
				Format:     "disc_csv",
				Lifecycle:  LifecycleImmutable,
				Required:   true,
				LocalPath:  localCSVPath,
				MinTracks:  2,
				MinDiscs:   2,
				MaxDropPct: 5.0,
			},
		},
	}
	rawCfg, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(sourcesJSONPath, rawCfg, 0644); err != nil {
		t.Fatalf("write sources.json: %v", err)
	}

	store := newFakeStore()
	t0 := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

	// Pre-seed a v1 lossy flat billboard-country.csv in S3 to verify v1->v2 upgrade does NOT lock in the flat file
	// for immutable billboard_csv, and instead fetches true raw upstream bytes into raw/billboard-country/<sha>.csv!
	store.put("reference/v1/sources/billboard-country.csv",
		[]byte("artist,title,genre,year,source\nQueen,Bohemian Rhapsody,Country,1975,Billboard Country\n"))

	// --- Run 1: Initial cold build ---
	outDir1 := filepath.Join(tmpDir, "out1")
	archDir1 := filepath.Join(tmpDir, "arch1")
	res1, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		OutDir:      outDir1,
		ArchiveDir:  archDir1,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t0,
		RetryDelay:  time.Millisecond,
		MaxRetries:  2,
	})
	if err != nil {
		t.Fatalf("Run 1 failed: %v", err)
	}
	if res1.Manifest.Degraded {
		t.Fatalf("expected non-degraded manifest on Run 1, got %+v", res1)
	}
	if immutableHits.Load() != 1 || rollingHits.Load() != 1 {
		t.Fatalf("expected 1 immutable hit and 1 rolling hit on Run 1, got %d / %d", immutableHits.Load(), rollingHits.Load())
	}

	// Simulate aws s3 sync populating S3 from archDir1:
	for _, s := range res1.Manifest.Sources {
		rawBytes, rErr := os.ReadFile(filepath.Join(archDir1, "raw", s.ID, s.RawFilename))
		if rErr != nil {
			t.Fatalf("read local raw %s: %v", s.RawFilename, rErr)
		}
		store.put(s.RawKey, rawBytes)
		csvBytes, cErr := os.ReadFile(filepath.Join(archDir1, s.CSVFilename))
		if cErr != nil {
			t.Fatalf("read local csv %s: %v", s.CSVFilename, cErr)
		}
		store.put(s.CSVKey, csvBytes)
	}
	manifestBytes, _ := os.ReadFile(filepath.Join(archDir1, "manifest.json"))
	store.put(ManifestObjectKey, manifestBytes)

	// Verify billboard_csv raw_key stores exact upstream bytes (3 weekly rows), while csv_key stores deduplicated 2 tracks!
	bbSrc, ok := res1.Manifest.FindSource("billboard-country")
	if !ok {
		t.Fatalf("missing billboard-country in manifest")
	}
	if !strings.HasPrefix(bbSrc.RawKey, "reference/v1/sources/raw/billboard-country/") {
		t.Fatalf("expected content-addressed raw key for billboard-country, got %q", bbSrc.RawKey)
	}
	rawObj, err := store.GetObject(ctx, bbSrc.RawKey, 0)
	if err != nil || string(rawObj.Body) != immutableBody {
		t.Fatalf("expected exact upstream bytes in %s, got %q (err=%v)", bbSrc.RawKey, string(rawObj.Body), err)
	}
	csvObj, err := store.GetObject(ctx, bbSrc.CSVKey, 0)
	if err != nil || string(csvObj.Body) == immutableBody {
		t.Fatalf("expected deduplicated CSV at %s, got %q", bbSrc.CSVKey, string(csvObj.Body))
	}

	// --- Run 2: Immutable cache reuse + Rolling HTTP 304 Not Modified ---
	rollingMode.Store(1) // 304 Not Modified
	t1 := t0.Add(7 * 24 * time.Hour)
	res2, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		OutDir:      filepath.Join(tmpDir, "out2"),
		ArchiveDir:  filepath.Join(tmpDir, "arch2"),
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t1,
		RetryDelay:  time.Millisecond,
		MaxRetries:  2,
	})
	if err != nil {
		t.Fatalf("Run 2 failed: %v", err)
	}
	// Immutable source must NOT have made an HTTP request!
	if immutableHits.Load() != 1 {
		t.Fatalf("expected immutable source to reuse S3 cache without HTTP request (hits=%d, want 1)", immutableHits.Load())
	}
	hdrMu.Lock()
	gotINM, gotIMS := lastIfNoneMatch, lastIfModSince
	hdrMu.Unlock()
	if gotINM != `"v1-etag"` || gotIMS != "Wed, 18 Nov 2026 06:00:00 GMT" {
		t.Fatalf("expected conditional headers on rolling request, got If-None-Match=%q If-Modified-Since=%q", gotINM, gotIMS)
	}
	zoomSrc2, _ := res2.Manifest.FindSource("zoom")
	if zoomSrc2.Origin != OriginHTTP304 || zoomSrc2.Stale {
		t.Fatalf("expected zoom origin=%s stale=false on 304, got %+v", OriginHTTP304, zoomSrc2)
	}

	// --- Run 2b: Offline mode re-parses from S3 with zero HTTP hits ---
	beforeRolling := rollingHits.Load()
	resOffline, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Offline:     true,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t1.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Offline run failed: %v", err)
	}
	if immutableHits.Load() != 1 || rollingHits.Load() != beforeRolling || resOffline.Manifest.DictionaryTracks != res1.Manifest.DictionaryTracks {
		t.Fatalf("expected offline run to make 0 HTTP requests")
	}

	// --- Run 2c: If cached raw object for a 304 response is missing, pipeline re-fetches unconditionally ---
	store.mu.Lock()
	delete(store.objects, zoomSrc2.RawKey)
	delete(store.objects, zoomSrc2.CSVKey)
	store.mu.Unlock()
	resRefetch, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t1.Add(2 * time.Hour),
		RetryDelay:  time.Millisecond,
		MaxRetries:  1,
	})
	if err != nil {
		t.Fatalf("304 cache-miss unconditional refetch failed: %v", err)
	}
	zoomRefetched, _ := resRefetch.Manifest.FindSource("zoom")
	if zoomRefetched.Origin != OriginHTTP200 {
		t.Fatalf("expected unconditional HTTP 200 refetch when 304 cache is missing, got %+v", zoomRefetched)
	}
	store.put(zoomRefetched.RawKey, []byte(rollingBodyV1))

	// --- Run 2d: Offline mode enforces max_stale_days on expired rolling cache ---
	if _, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Offline:     true,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t0.Add(30 * 24 * time.Hour),
	}); err == nil || !strings.Contains(err.Error(), "max_stale_days") {
		t.Fatalf("expected offline run to fail max_stale_days guardrail when rolling cache is 30 days old, got: %v", err)
	}

	// --- Run 3: Rolling upstream 503 outage falls back to S3 within max_stale_days ---
	rollingMode.Store(2)              // 503 Service Unavailable
	t2 := t0.Add(10 * 24 * time.Hour) // 10 days old <= 21 max_stale_days
	res3, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t2,
		RetryDelay:  time.Millisecond,
		MaxRetries:  2,
	})
	if err != nil {
		t.Fatalf("Run 3 (fallback within staleness window) failed: %v", err)
	}
	if !res3.Manifest.Degraded || len(res3.Manifest.StaleSources) != 1 || res3.Manifest.StaleSources[0] != "zoom" {
		t.Fatalf("expected degraded manifest with stale_sources=[zoom], got %+v", res3.Manifest)
	}
	zoomSrc3, _ := res3.Manifest.FindSource("zoom")
	if zoomSrc3.Origin != OriginS3Fallback || !zoomSrc3.Stale || zoomSrc3.FallbackReason == "" {
		t.Fatalf("expected s3_fallback provenance on zoom, got %+v", zoomSrc3)
	}

	// --- Run 3b: Rolling upstream returns 200 HTML error page (magic-byte rejection -> S3 fallback) ---
	rollingMode.Store(3) // 200 HTML page
	res3b, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t2,
		RetryDelay:  time.Millisecond,
		MaxRetries:  1,
	})
	if err != nil {
		t.Fatalf("Run 3b (HTML magic-byte fallback) failed: %v", err)
	}
	zoomSrc3b, _ := res3b.Manifest.FindSource("zoom")
	if zoomSrc3b.Origin != OriginS3Fallback || !strings.Contains(zoomSrc3b.FallbackReason, "magic-byte") {
		t.Fatalf("expected magic-byte fallback reason, got %+v", zoomSrc3b)
	}

	// --- Run 4: Rolling source stays down past max_stale_days (25 days > 21 days) -> aborts! ---
	rollingMode.Store(2)
	t3 := t0.Add(25 * 24 * time.Hour)
	_, err = RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		Store:       store,
		HTTPClient:  srv.Client(),
		Now:         t3,
		RetryDelay:  time.Millisecond,
		MaxRetries:  1,
	})
	if err == nil || !strings.Contains(err.Error(), "max_stale_days") {
		t.Fatalf("expected staleness guardrail error after 25 days, got: %v", err)
	}
}

func TestPipeline_ShrinkAndFloorGuardrails(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	var upstreamCSV atomic.Value
	upstreamCSV.Store("Artist,\"Song title\"\n\"Queen\",\"Song 1\"\n\"Queen\",\"Song 2\"\n\"Queen\",\"Song 3\"\n\"Queen\",\"Song 4\"\n\"Queen\",\"Song 5\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(upstreamCSV.Load().(string)))
	}))
	defer srv.Close()

	sourcesJSONPath := filepath.Join(tmpDir, "sources.json")
	cfg := SourcesConfig{
		Version:          2,
		MaxGlobalDropPct: 5.0,
		Sources: []SourceConfig{
			{
				ID:         "zoom",
				Name:       "Zoom Rolling Catalog",
				Provider:   "Zoom",
				Format:     "zoom_csv",
				Lifecycle:  LifecycleRolling,
				Required:   true,
				URL:        srv.URL + "/cat.csv",
				MinTracks:  3,
				MaxDropPct: 10.0,
			},
		},
	}
	rawCfg, _ := json.Marshal(cfg)
	_ = os.WriteFile(sourcesJSONPath, rawCfg, 0644)

	archDir := filepath.Join(tmpDir, "arch")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	// 1. Initial compile (5 tracks).
	if _, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		ArchiveDir:  archDir,
		HTTPClient:  srv.Client(),
		Now:         now,
	}); err != nil {
		t.Fatalf("initial run failed: %v", err)
	}

	// 2. Upstream shrinks from 5 -> 3 tracks (40% drop > 10% max_drop_pct) -> aborts!
	upstreamCSV.Store("Artist,\"Song title\"\n\"Queen\",\"Song 1\"\n\"Queen\",\"Song 2\"\n\"Queen\",\"Song 3\"\n")
	_, err := RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		ArchiveDir:  archDir,
		HTTPClient:  srv.Client(),
		Now:         now.Add(time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "exceeding max_drop_pct") {
		t.Fatalf("expected per-source shrink guardrail failure on 40%% drop, got: %v", err)
	}

	// 3. Upstream drops below min_tracks floor (2 tracks < 3 min_tracks) -> aborts!
	upstreamCSV.Store("Artist,\"Song title\"\n\"Queen\",\"Song 1\"\n\"Queen\",\"Song 2\"\n")
	_, err = RunPipeline(ctx, PipelineOptions{
		SourcesPath: sourcesJSONPath,
		ArchiveDir:  archDir,
		HTTPClient:  srv.Client(),
		Now:         now.Add(2 * time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "below min_tracks floor") {
		t.Fatalf("expected min_tracks floor failure, got: %v", err)
	}
}
