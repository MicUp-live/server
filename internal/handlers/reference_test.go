package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/reference"
)

type fakeReferenceStore struct {
	objects    map[string]*reference.ObjectResult
	presigned  map[string]string
	errByKey   map[string]error
	lastMaxInl int64
}

func (f *fakeReferenceStore) GetObject(_ context.Context, key string, maxInlineBytes int64) (*reference.ObjectResult, error) {
	f.lastMaxInl = maxInlineBytes
	clean := strings.TrimPrefix(key, "/")
	if err, ok := f.errByKey[clean]; ok && err != nil {
		return nil, err
	}
	obj, ok := f.objects[clean]
	if !ok {
		return nil, os.ErrNotExist
	}
	if maxInlineBytes > 0 && obj.Size > maxInlineBytes {
		if url, ok := f.presigned[clean]; ok {
			return &reference.ObjectResult{
				ContentType: obj.ContentType,
				Size:        obj.Size,
				Presigned:   url,
			}, nil
		}
	}
	return obj, nil
}

func (f *fakeReferenceStore) PresignGet(_ context.Context, key string) (string, error) {
	clean := strings.TrimPrefix(key, "/")
	if url, ok := f.presigned[clean]; ok {
		return url, nil
	}
	return "", os.ErrNotExist
}

func newReferenceTestRouter(t *testing.T, store reference.Store) http.Handler {
	t.Helper()
	router, err := app.NewRouter(config.Config{
		Environment:    config.Development,
		Port:           "8080",
		AuthSecret:     "test-signing-secret-with-at-least-32-bytes",
		AllowedOrigins: []string{"https://micup.live"},
	}, app.Dependencies{
		ReferenceStore: store,
	})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	return router
}

func TestReferenceEndpoints_SourcesDictionaryAndDiscs(t *testing.T) {
	tracks := []reference.CanonicalTrack{
		{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "Party Tyme", Genre: "Rock", Year: 1975},
		{Artist: "Journey", Title: "Don't Stop Believin'", Source: "Sound Choice", Genre: "Rock", Year: 1981},
	}
	dictGz, err := reference.CompileDictionary(tracks)
	if err != nil {
		t.Fatalf("compile dictionary: %v", err)
	}
	discMap := map[string]reference.CanonicalTrack{
		"PH00101": {
			ID:     reference.GenerateCanonicalID("Queen", "Bohemian Rhapsody"),
			Artist: "Queen",
			Title:  "Bohemian Rhapsody",
			Source: "Party Tyme",
			Genre:  "Rock",
			Year:   1975,
		},
		"SC8101-02": {
			ID:     reference.GenerateCanonicalID("Journey", "Don't Stop Believin'"),
			Artist: "Journey",
			Title:  "Don't Stop Believin'",
			Source: "Sound Choice",
			Genre:  "Rock",
			Year:   1981,
		},
		"NSC2000-01-01": {
			ID:     reference.GenerateCanonicalID("Backstreet Boys", "Everybody (Backstreet's Back)"),
			Artist: "Backstreet Boys",
			Title:  "Everybody (Backstreet's Back)",
			Source: "Sound Choice",
		},
	}
	discGz, err := reference.CompileDiscMap(discMap)
	if err != nil {
		t.Fatalf("compile disc map: %v", err)
	}

	manifest := reference.SourceManifest{
		Version:          2,
		BuildID:          "20261123T000000Z-12345678",
		CompiledAt:       "2026-11-23T00:00:00Z",
		InputsHash:       "sha256:1234567890abcdef",
		DictionaryTracks: 2,
		MappedDiscs:      2,
		Degraded:         false,
		StaleSources:     []string{},
		Sources: []reference.ArchivedSource{
			{
				ID:             "party-tyme",
				Name:           "Party Tyme Official Songbook PDF",
				Provider:       "Party Tyme",
				Format:         "pdf",
				Lifecycle:      reference.LifecycleRolling,
				SourceURL:      "https://www.partytyme.net/songshop/cat/party_tyme_catalog.pdf",
				RawFilename:    "party-tyme-a1b2c3d4e5f60718.pdf",
				RawKey:         "reference/v1/sources/raw/party-tyme/a1b2c3d4e5f60718.pdf",
				RawContentType: "application/pdf",
				RawSizeBytes:   12,
				RawSHA256:      "a1b2c3d4e5f60718a1b2c3d4e5f60718a1b2c3d4e5f60718a1b2c3d4e5f60718",
				CSVFilename:    "party-tyme.csv",
				CSVKey:         "reference/v1/sources/party-tyme.csv",
				CSVSizeBytes:   45,
				TracksCount:    1,
				DiscsCount:     1,
				FetchedAt:      "2026-11-23T00:00:00Z",
				Origin:         reference.OriginHTTP200,
			},
			{
				ID:             "karafun",
				Name:           "KaraFun Complete Catalog",
				Provider:       "KaraFun",
				Format:         "csv",
				Lifecycle:      reference.LifecycleRolling,
				RawFilename:    "karafun.csv",
				RawKey:         "reference/v1/sources/karafun.csv",
				RawContentType: "text/csv; charset=utf-8",
				RawSizeBytes:   10 * 1024 * 1024, // 10 MB (> 4 MB threshold)
				CSVFilename:    "karafun.csv",
				CSVKey:         "reference/v1/sources/karafun.csv",
				CSVSizeBytes:   10 * 1024 * 1024,
				TracksCount:    90000,
				Origin:         reference.OriginHTTP304,
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)

	store := &fakeReferenceStore{
		objects: map[string]*reference.ObjectResult{
			"reference/v1/sources/manifest.json": {
				Body:        manifestBytes,
				ContentType: "application/json",
				Size:        int64(len(manifestBytes)),
			},
			"reference/v1/sources/raw/party-tyme/a1b2c3d4e5f60718.pdf": {
				Body:        []byte("%PDF-1.4-mock"),
				ContentType: "application/pdf",
				Size:        13,
			},
			"reference/v1/sources/party-tyme.csv": {
				Body:        []byte("disc,artist,title,provider\nPH00101,Queen,Bohemian Rhapsody,Party Tyme\n"),
				ContentType: "text/csv; charset=utf-8",
				Size:        70,
			},
			"reference/v1/sources/karafun.csv": {
				Body:        []byte("large"),
				ContentType: "text/csv; charset=utf-8",
				Size:        10 * 1024 * 1024,
			},
			"reference/v1/dictionary.json.gz": {
				Body:        dictGz,
				ContentType: "application/json",
				Size:        int64(len(dictGz)),
			},
			"reference/v1/disc_map.json.gz": {
				Body:        discGz,
				ContentType: "application/json",
				Size:        int64(len(discGz)),
			},
		},
		presigned: map[string]string{
			"reference/v1/sources/party-tyme.pdf": "https://s3.example.com/reference/v1/sources/party-tyme.pdf?sig=1",
			"reference/v1/sources/karafun.csv":    "https://s3.example.com/reference/v1/sources/karafun.csv?sig=2",
		},
	}

	router := newReferenceTestRouter(t, store)

	// 1. GET /api/v1/reference/sources
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/sources = %d; want 200", rec.Code)
		}
		var body struct {
			Version          int      `json:"version"`
			BuildID          string   `json:"build_id"`
			InputsHash       string   `json:"inputs_hash"`
			Degraded         bool     `json:"degraded"`
			StaleSources     []string `json:"stale_sources"`
			DictionaryTracks int      `json:"dictionary_tracks"`
			MappedDiscs      int      `json:"mapped_discs"`
			Sources          []struct {
				ID        string `json:"id"`
				Lifecycle string `json:"lifecycle"`
				Origin    string `json:"origin"`
				APIURL    string `json:"api_url"`
				CSVURL    string `json:"csv_url"`
			} `json:"sources"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode sources: %v", err)
		}
		if body.Version != 2 || body.BuildID != "20261123T000000Z-12345678" || body.InputsHash != "sha256:1234567890abcdef" {
			t.Fatalf("unexpected v2 manifest fields: %+v", body)
		}
		if len(body.Sources) != 2 || body.Sources[0].ID != "party-tyme" || body.Sources[0].Lifecycle != "rolling" || body.Sources[0].Origin != "http_200" {
			t.Fatalf("unexpected sources response: %+v", body)
		}
	}

	// 2. GET /api/v1/reference/sources/party-tyme (raw PDF inline from content-addressed key with normalized Content-Disposition filename)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources/party-tyme", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/sources/party-tyme = %d; want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
			t.Fatalf("Content-Type = %q; want application/pdf", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); cd != `inline; filename="party-tyme.pdf"` {
			t.Fatalf("Content-Disposition = %q; want inline; filename=\"party-tyme.pdf\"", cd)
		}
		if rec.Body.String() != "%PDF-1.4-mock" {
			t.Fatalf("unexpected PDF body: %q", rec.Body.String())
		}
	}

	// 3. GET /api/v1/reference/sources/party-tyme?format=csv (extracted CSV)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources/party-tyme?format=csv", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/sources/party-tyme?format=csv = %d; want 200", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
			t.Fatalf("Content-Type = %q; want text/csv", rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), "PH00101,Queen,Bohemian Rhapsody,Party Tyme") {
			t.Fatalf("unexpected CSV body: %q", rec.Body.String())
		}
	}

	// 4. GET /api/v1/reference/sources/karafun (>4 MB object redirects to presigned S3 URL)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources/karafun", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("GET /api/v1/reference/sources/karafun = %d; want 307", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "https://s3.example.com/reference/v1/sources/karafun.csv?sig=2" {
			t.Fatalf("Location = %q", loc)
		}
	}

	// 5. GET /api/v1/reference/dictionary?artist=Queen&title=Bohemian+Rhapsody
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/dictionary?artist=Queen&title=Bohemian+Rhapsody", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/dictionary = %d; want 200", rec.Code)
		}
		var body struct {
			Count  int                        `json:"count"`
			Tracks []reference.CanonicalTrack `json:"tracks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode dictionary query: %v", err)
		}
		if body.Count != 1 || body.Tracks[0].Artist != "Queen" {
			t.Fatalf("unexpected dictionary result: %+v", body)
		}
	}

	// 6. GET /api/v1/reference/discs/ph00101 (case-insensitive disc lookup)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/discs/ph00101", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/discs/ph00101 = %d; want 200", rec.Code)
		}
		var body struct {
			Disc     string `json:"disc"`
			Artist   string `json:"artist"`
			Title    string `json:"title"`
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode disc lookup: %v", err)
		}
		if body.Disc != "PH00101" || body.Provider != "Party Tyme" || body.Artist != "Queen" {
			t.Fatalf("unexpected disc lookup result: %+v", body)
		}
	}

	// 6b. GET /api/v1/reference/discs/nsc2000_01_01 (multipart underscore normalization)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/discs/nsc2000_01_01", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/discs/nsc2000_01_01 = %d; want 200", rec.Code)
		}
		var body struct {
			Disc   string `json:"disc"`
			Artist string `json:"artist"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode multipart disc lookup: %v", err)
		}
		if body.Disc != "NSC2000-01-01" || body.Artist != "Backstreet Boys" {
			t.Fatalf("unexpected multipart disc lookup result: %+v", body)
		}
	}

	// 7. GET /api/v1/reference/discs?provider=Sound+Choice
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/discs?provider=Sound+Choice", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/reference/discs?provider=Sound+Choice = %d; want 200", rec.Code)
		}
		var body struct {
			Count int `json:"count"`
			Discs []struct {
				Disc     string `json:"disc"`
				Provider string `json:"provider"`
			} `json:"discs"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode discs search: %v", err)
		}
		if body.Count != 2 {
			t.Fatalf("unexpected discs search result: %+v", body)
		}
	}
}

func TestReferenceEndpoints_ManifestErrorsReturn503(t *testing.T) {
	// 1. Operational storage failure returns 503 instead of synthetic fallback 200.
	{
		store := &fakeReferenceStore{
			errByKey: map[string]error{
				"reference/v1/sources/manifest.json": errors.New("s3 access denied"),
			},
		}
		router := newReferenceTestRouter(t, store)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("storage error GET /api/v1/reference/sources = %d; want 503", rec.Code)
		}
	}

	// 2. Malformed manifest JSON returns 503 instead of synthetic fallback 200.
	{
		store := &fakeReferenceStore{
			objects: map[string]*reference.ObjectResult{
				"reference/v1/sources/manifest.json": {
					Body:        []byte("{not-json"),
					ContentType: "application/json",
					Size:        9,
				},
			},
		}
		router := newReferenceTestRouter(t, store)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/sources/party-tyme", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("malformed manifest GET /api/v1/reference/sources/party-tyme = %d; want 503", rec.Code)
		}
	}
}
