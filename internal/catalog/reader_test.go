package catalog

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"micup.live/server/internal/models"
)

type fakeCatalogObjectAPI struct {
	body    []byte
	objects map[string][]byte
}

func (f *fakeCatalogObjectAPI) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.objects != nil {
		key := aws.ToString(in.Key)
		data, ok := f.objects[key]
		if !ok {
			return nil, os.ErrNotExist
		}
		return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
	}
	if strings.HasPrefix(aws.ToString(in.Key), "private/") {
		return nil, os.ErrNotExist
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(f.body)),
	}, nil
}

func TestS3CatalogReaderPreservesSourceCanonicalIDIsCustomAndVideoURL(t *testing.T) {
	compiled, err := Compile([]*models.Song{
		{
			Artist:      "Queen",
			Title:       "Bohemian Rhapsody",
			Genre:       "Rock",
			Disc:        "SC8101-02",
			Source:      "Sound Choice",
			VideoURL:    "https://youtu.be/abc",
			CanonicalID: "can_1234",
			IsCustom:    true,
			IsAvailable: true,
		},
	}, Options{SchemaVersion: 1})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	assets := assetsByPath(compiled.Assets)
	reader := &S3CatalogReader{
		Bucket: "test-bucket",
		Client: &fakeCatalogObjectAPI{body: assets["search.json.gz"].Body},
	}

	songs, err := reader.ListSongs(context.Background(), "catalogs/v1", "sb-1")
	if err != nil {
		t.Fatalf("ListSongs: %v", err)
	}
	if len(songs) != 1 {
		t.Fatalf("got %d songs, want 1", len(songs))
	}
	got := songs[0]
	if got.Source != "Sound Choice" || got.Disc != "SC8101-02" || got.VideoURL != "https://youtu.be/abc" || got.CanonicalID != "can_1234" || !got.IsCustom {
		t.Fatalf("unexpected decoded song: %+v", got)
	}
}

func TestPrivateSongLocationsIsolatedFromPublicCatalogAndHydratedForHost(t *testing.T) {
	withLoc, err := Compile([]*models.Song{
		{
			Artist:      "",
			Title:       "FALL TO PIECES",
			Location:    "C:\\Karaoke\\Rock\\CB40123-05 - Velvet Revolver - Fall To Pieces.zip",
			IsAvailable: true,
		},
	}, Options{SchemaVersion: 1})
	if err != nil {
		t.Fatalf("compile with location: %v", err)
	}
	withoutLoc, err := Compile([]*models.Song{
		{
			Artist:      "",
			Title:       "FALL TO PIECES",
			IsAvailable: true,
		},
	}, Options{SchemaVersion: 1})
	if err != nil {
		t.Fatalf("compile without location: %v", err)
	}
	if withLoc.CatalogID != withoutLoc.CatalogID {
		t.Fatalf("expected identical public CatalogID regardless of private location: %s vs %s", withLoc.CatalogID, withoutLoc.CatalogID)
	}
	if withLoc.LocationsAsset == nil {
		t.Fatalf("expected private LocationsAsset to be generated")
	}
	for _, asset := range withLoc.Assets {
		if strings.Contains(string(asset.Body), "Velvet Revolver") || strings.Contains(asset.Path, "locations") {
			t.Fatalf("private location leaked into public asset %s", asset.Path)
		}
	}

	assets := assetsByPath(withLoc.Assets)
	reader := &S3CatalogReader{
		Bucket: "test-bucket",
		Client: &fakeCatalogObjectAPI{
			objects: map[string][]byte{
				"catalogs/epochs/" + withLoc.CatalogID + "/ep-1/search.json.gz":      assets["search.json.gz"].Body,
				"private/songbooks/sb-1/" + withLoc.CatalogID + "/locations.json.gz": withLoc.LocationsAsset.Body,
			},
		},
	}

	hostSongs, err := reader.ListSongs(context.Background(), "catalogs/epochs/"+withLoc.CatalogID+"/ep-1", "sb-1")
	if err != nil {
		t.Fatalf("ListSongs sb-1: %v", err)
	}
	if len(hostSongs) != 1 || hostSongs[0].Location != "C:\\Karaoke\\Rock\\CB40123-05 - Velvet Revolver - Fall To Pieces.zip" {
		t.Fatalf("expected hydrated host location for sb-1, got %+v", hostSongs)
	}

	otherSongs, err := reader.ListSongs(context.Background(), "catalogs/epochs/"+withLoc.CatalogID+"/ep-1", "sb-other")
	if err != nil {
		t.Fatalf("ListSongs sb-other: %v", err)
	}
	if len(otherSongs) != 1 || otherSongs[0].Location != "" {
		t.Fatalf("expected tenant isolation (empty location for sb-other), got %+v", otherSongs)
	}

	// Transient S3 / corrupt gzip error on private locations must propagate (never silently wipe locations)
	errReader := &S3CatalogReader{
		Bucket: "test-bucket",
		Client: &fakeCatalogObjectAPI{
			objects: map[string][]byte{
				"catalogs/epochs/" + withLoc.CatalogID + "/ep-1/search.json.gz":      assets["search.json.gz"].Body,
				"private/songbooks/sb-1/" + withLoc.CatalogID + "/locations.json.gz": []byte("not-gzip"),
			},
		},
	}
	if _, err := errReader.ListSongs(context.Background(), "catalogs/epochs/"+withLoc.CatalogID+"/ep-1", "sb-1"); err == nil {
		t.Fatalf("expected error on corrupt private locations archive")
	}

	// Additive MergeSongs preserves existing Location when incoming row omits it
	merged := MergeSongs(
		[]*models.Song{{ID: "s1", Artist: "Queen", Title: "Bohemian Rhapsody", Location: "C:\\Karaoke\\Queen.zip", IsAvailable: true}},
		[]*models.Song{{ID: "s1", Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true}},
	)
	if len(merged) != 1 || merged[0].Location != "C:\\Karaoke\\Queen.zip" {
		t.Fatalf("expected MergeSongs to preserve existing location, got %+v", merged)
	}
}
