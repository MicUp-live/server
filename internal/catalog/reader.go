package catalog

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"micup.live/server/internal/models"
)

type catalogObjectAPI interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3CatalogReader reads public canonical tracks from an immutable catalog.
type S3CatalogReader struct {
	Bucket string
	Client catalogObjectAPI
}

type searchCatalog struct {
	Songs []struct {
		ID          string `json:"song_id"`
		Artist      string `json:"artist"`
		Title       string `json:"title"`
		Label       string `json:"label"`
		Genre       string `json:"genre"`
		Disc        string `json:"disc"`
		Source      string `json:"source"`
		VideoURL    string `json:"video_url"`
		CanonicalID string `json:"canonical_id"`
		IsCustom    bool   `json:"is_custom"`
	} `json:"songs"`
}

// ListSongs loads the full public search artifact under an asset prefix.
func (r *S3CatalogReader) ListSongs(ctx context.Context, assetPrefix, songbookID string) ([]*models.Song, error) {
	if r == nil || r.Client == nil || strings.TrimSpace(r.Bucket) == "" {
		return nil, fmt.Errorf("read catalog: storage is not configured")
	}
	prefix := strings.Trim(strings.TrimSpace(assetPrefix), "/")
	if prefix == "" {
		return nil, fmt.Errorf("read catalog: asset prefix is required")
	}
	obj, err := r.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.Bucket), Key: aws.String(prefix + "/search.json.gz")})
	if err != nil {
		return nil, fmt.Errorf("read catalog: get search asset: %w", err)
	}
	defer obj.Body.Close()
	reader, err := gzip.NewReader(obj.Body)
	if err != nil {
		return nil, fmt.Errorf("read catalog: gzip: %w", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read catalog: read: %w", err)
	}
	var catalog searchCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("read catalog: decode: %w", err)
	}
	locations, err := r.readPrivateLocations(ctx, songbookID, catalogIDFromAssetPrefix(prefix))
	if err != nil {
		return nil, err
	}
	result := make([]*models.Song, 0, len(catalog.Songs))
	for _, track := range catalog.Songs {
		result = append(result, &models.Song{
			ID:          track.ID,
			SongbookID:  songbookID,
			Artist:      track.Artist,
			Title:       track.Title,
			Label:       track.Label,
			Genre:       track.Genre,
			Disc:        track.Disc,
			Source:      track.Source,
			Location:    locations[track.ID],
			VideoURL:    track.VideoURL,
			CanonicalID: track.CanonicalID,
			IsCustom:    track.IsCustom,
			IsAvailable: true,
		})
	}
	return result, nil
}

func catalogIDFromAssetPrefix(prefix string) string {
	trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
	if rest, ok := strings.CutPrefix(trimmed, "catalogs/epochs/"); ok {
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) >= 1 && parts[0] != "" {
			return parts[0]
		}
	}
	if rest, ok := strings.CutPrefix(trimmed, "catalogs/"); ok && rest != "" {
		return strings.SplitN(rest, "/", 2)[0]
	}
	return ""
}

func isS3ObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound" || code == "404"
	}
	return false
}

func (r *S3CatalogReader) readPrivateLocations(ctx context.Context, songbookID, catalogID string) (map[string]string, error) {
	songbookID = strings.TrimSpace(songbookID)
	catalogID = strings.TrimSpace(catalogID)
	if songbookID == "" || catalogID == "" || r == nil || r.Client == nil || strings.TrimSpace(r.Bucket) == "" {
		return nil, nil
	}
	obj, err := r.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.Bucket),
		Key:    aws.String("private/songbooks/" + songbookID + "/" + catalogID + "/locations.json.gz"),
	})
	if err != nil {
		if isS3ObjectNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read catalog: get private locations: %w", err)
	}
	defer obj.Body.Close()
	gr, err := gzip.NewReader(obj.Body)
	if err != nil {
		return nil, fmt.Errorf("read catalog: private locations gzip: %w", err)
	}
	defer gr.Close()
	body, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("read catalog: read private locations: %w", err)
	}
	var locs map[string]string
	if err := json.Unmarshal(body, &locs); err != nil {
		return nil, fmt.Errorf("read catalog: decode private locations: %w", err)
	}
	return locs, nil
}
