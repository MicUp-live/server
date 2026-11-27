package reference

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	ManifestObjectKey   = "reference/v1/sources/manifest.json"
	DictionaryObjectKey = "reference/v1/dictionary.json.gz"
	DiscMapObjectKey    = "reference/v1/disc_map.json.gz"
	SourcesS3Prefix     = "reference/v1/sources/"
)

// ArchivedSource describes an archived raw PDF/XLSX/CSV and its extracted CSV in S3.
type ArchivedSource struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	Format         string `json:"format"`    // "csv", "pdf", "xlsx"
	Lifecycle      string `json:"lifecycle"` // "immutable" or "rolling"
	SourceURL      string `json:"source_url,omitempty"`
	RawFilename    string `json:"raw_filename"`
	RawKey         string `json:"raw_key"`
	RawContentType string `json:"raw_content_type"`
	RawSizeBytes   int64  `json:"raw_size_bytes"`
	RawSHA256      string `json:"raw_sha256,omitempty"`
	CSVFilename    string `json:"csv_filename"`
	CSVKey         string `json:"csv_key"`
	CSVSizeBytes   int64  `json:"csv_size_bytes"`
	TracksCount    int    `json:"tracks_count"`
	DiscsCount     int    `json:"discs_count"`
	FetchedAt      string `json:"fetched_at"`
	ETag           string `json:"etag,omitempty"`
	LastModified   string `json:"last_modified,omitempty"`
	Origin         string `json:"origin,omitempty"` // "http_200", "http_304", "s3_cache", "s3_fallback", "local_cache", "local_file"
	Stale          bool   `json:"stale"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// SourceManifest indexes all archived reference catalogs and compiled dictionary counts.
type SourceManifest struct {
	Version          int              `json:"version"`
	BuildID          string           `json:"build_id,omitempty"`
	CompiledAt       string           `json:"compiled_at"`
	InputsHash       string           `json:"inputs_hash,omitempty"`
	DictionaryTracks int              `json:"dictionary_tracks"`
	MappedDiscs      int              `json:"mapped_discs"`
	Degraded         bool             `json:"degraded"`
	StaleSources     []string         `json:"stale_sources"`
	Sources          []ArchivedSource `json:"sources"`
}

// FindSource looks up an ArchivedSource by ID in a manifest.
func (m *SourceManifest) FindSource(id string) (ArchivedSource, bool) {
	if m == nil {
		return ArchivedSource{}, false
	}
	for _, s := range m.Sources {
		if strings.EqualFold(s.ID, id) {
			return s, true
		}
	}
	return ArchivedSource{}, false
}

// WriteDiscMapCSV writes a deterministic sorted CSV of (disc, artist, title, provider) mappings.
func WriteDiscMapCSV(w io.Writer, discs map[string]CanonicalTrack) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"disc", "artist", "title", "provider"}); err != nil {
		return err
	}
	keys := make([]string, 0, len(discs))
	for k := range discs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := discs[k]
		if err := cw.Write([]string{k, t.Artist, t.Title, t.Source}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ObjectResult represents a loaded reference artifact or source file.
type ObjectResult struct {
	Body        []byte
	ContentType string
	Size        int64
	Presigned   string
}

// Store provides read and presign access to compiled reference artifacts and archived source files.
type Store interface {
	GetObject(ctx context.Context, key string, maxInlineBytes int64) (*ObjectResult, error)
	PresignGet(ctx context.Context, key string) (string, error)
}

type s3GetAPI interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type s3PresignGetAPI interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// HybridStore reads reference objects from S3 with optional local filesystem fallback.
type HybridStore struct {
	Bucket         string
	Client         s3GetAPI
	Presigner      s3PresignGetAPI
	LocalPublicDir string // e.g. "./web/public"
	LocalDistDir   string // e.g. "./dist"
}

// PresignGet generates a temporary S3 download URL for a reference object key.
func (s *HybridStore) PresignGet(ctx context.Context, key string) (string, error) {
	if s == nil || s.Presigner == nil || strings.TrimSpace(s.Bucket) == "" {
		return "", fmt.Errorf("presign reference object: S3 presigner not configured")
	}
	cleanKey := strings.TrimPrefix(strings.TrimSpace(key), "/")
	req, err := s.Presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(cleanKey),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = 15 * time.Minute
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// GetObject fetches a reference object by key (e.g. "reference/v1/sources/raw/party-tyme/abc.pdf").
// If the object in S3 exceeds maxInlineBytes (>0) and a Presigner is configured, it returns a Presigned URL instead of buffering the full body.
func (s *HybridStore) GetObject(ctx context.Context, key string, maxInlineBytes int64) (*ObjectResult, error) {
	cleanKey := strings.TrimPrefix(strings.TrimSpace(key), "/")
	if cleanKey == "" || strings.Contains(cleanKey, "..") {
		return nil, fmt.Errorf("invalid reference key %q", key)
	}

	// 1. Try S3 when configured.
	res, found, err := s.getObjectFromS3(ctx, cleanKey, maxInlineBytes)
	if err != nil {
		return nil, err
	}
	if found {
		return res, nil
	}

	// 2. Fallback to local directories (LocalDistDir then LocalPublicDir).
	if res := s.getObjectFromLocal(cleanKey); res != nil {
		return res, nil
	}

	return nil, os.ErrNotExist
}

func (s *HybridStore) getObjectFromS3(ctx context.Context, cleanKey string, maxInlineBytes int64) (*ObjectResult, bool, error) {
	if s == nil || s.Client == nil || strings.TrimSpace(s.Bucket) == "" {
		return nil, false, nil
	}
	if presigned := s.tryPresignedObject(ctx, cleanKey, maxInlineBytes); presigned != nil {
		return presigned, true, nil
	}

	out, err := s.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(cleanKey),
	})
	if err != nil {
		if !isS3NotFound(err) {
			return nil, false, fmt.Errorf("get reference object %s from S3: %w", cleanKey, err)
		}
		return nil, false, nil
	}
	defer out.Body.Close()

	body, readErr := io.ReadAll(out.Body)
	if readErr != nil {
		return nil, false, readErr
	}
	return &ObjectResult{
		Body:        body,
		ContentType: resolveContentType(cleanKey, out.ContentType),
		Size:        int64(len(body)),
	}, true, nil
}

func (s *HybridStore) tryPresignedObject(ctx context.Context, cleanKey string, maxInlineBytes int64) *ObjectResult {
	if maxInlineBytes <= 0 || s.Presigner == nil {
		return nil
	}
	head, err := s.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(cleanKey),
	})
	if err != nil || head.ContentLength == nil || *head.ContentLength <= maxInlineBytes {
		return nil
	}
	url, pErr := s.PresignGet(ctx, cleanKey)
	if pErr != nil {
		return nil
	}
	return &ObjectResult{
		ContentType: resolveContentType(cleanKey, head.ContentType),
		Size:        *head.ContentLength,
		Presigned:   url,
	}
}

func (s *HybridStore) getObjectFromLocal(cleanKey string) *ObjectResult {
	if s == nil {
		return nil
	}
	for _, root := range []string{s.LocalDistDir, s.LocalPublicDir} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		fullPath := filepath.Join(root, filepath.FromSlash(cleanKey))
		if data, err := os.ReadFile(fullPath); err == nil {
			return &ObjectResult{
				Body:        data,
				ContentType: inferContentType(cleanKey),
				Size:        int64(len(data)),
			}
		}
	}
	return nil
}

func resolveContentType(key string, explicit *string) string {
	if explicit != nil && *explicit != "" {
		return *explicit
	}
	return inferContentType(key)
}

func inferContentType(key string) string {
	lower := strings.ToLower(key)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".xlsx"):
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case strings.HasSuffix(lower, ".csv"):
		return "text/csv; charset=utf-8"
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".json.gz"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound" || code == "404"
	}
	return false
}

// DecodeGzipJSON decompresses a gzip-encoded JSON payload (or reads plain JSON if uncompressed).
func DecodeGzipJSON(raw []byte, target any) error {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		defer gr.Close()
		return json.NewDecoder(gr).Decode(target)
	}
	return json.Unmarshal(raw, target)
}
