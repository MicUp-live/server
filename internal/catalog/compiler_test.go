// Package catalog tests deterministic public catalog compilation.
package catalog

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"micup.live/server/internal/models"
)

func TestCompileBuildsDeterministicLetterAndSearchAssets(t *testing.T) {
	songs := []*models.Song{
		{ID: "local-1", Artist: " Toto ", Title: "  Africa ", IsAvailable: true},
		{ID: "local-2", Artist: "Queen", Title: "Another One Bites the Dust", Genre: " Rock ", IsAvailable: true},
		{ID: "local-3", Artist: "Prince", Title: "1999", IsAvailable: true},
		{ID: "local-4", Artist: "Hidden", Title: "Ignored", IsAvailable: false},
		{ID: "local-5", Artist: "Toto", Title: "Africa", IsAvailable: true},
	}

	compiled, err := Compile(songs, Options{SchemaVersion: 1, ShardSize: 1})
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	reversed, err := Compile([]*models.Song{songs[4], songs[3], songs[2], songs[1], songs[0]}, Options{SchemaVersion: 1, ShardSize: 1})
	if err != nil {
		t.Fatalf("compile reversed catalog: %v", err)
	}
	if compiled.CatalogID != reversed.CatalogID {
		t.Fatalf("catalog IDs differ: %s vs %s", compiled.CatalogID, reversed.CatalogID)
	}
	if compiled.Manifest.Total != 3 {
		t.Fatalf("catalog total = %d; want 3", compiled.Manifest.Total)
	}
	if got := compiled.Manifest.Letters["A"]; got.Count != 2 || len(got.Shards) != 2 {
		t.Fatalf("A manifest = %+v; want 2 one-song shards", got)
	}
	if got := compiled.Manifest.Letters["#"]; got.Count != 1 || got.Shards[0] != "letters/other/000.json" {
		t.Fatalf("# manifest = %+v", got)
	}

	assets := assetsByPath(compiled.Assets)
	var firstShard struct {
		Songs []publicTrack `json:"songs"`
	}
	if err := json.Unmarshal(assets["letters/A/000.json"].Body, &firstShard); err != nil {
		t.Fatalf("read title shard: %v", err)
	}
	if len(firstShard.Songs) != 1 || firstShard.Songs[0].Title != "Africa" || firstShard.Songs[0].Artist != "Toto" {
		t.Fatalf("first title shard = %+v", firstShard.Songs)
	}
	search, err := readSearchAsset(assets["search.json.gz"].Body)
	if err != nil {
		t.Fatalf("read search asset: %v", err)
	}
	if len(search.Tracks) != 3 {
		t.Fatalf("search tracks = %d; want 3", len(search.Tracks))
	}
	if !bytes.Equal(assets["search.json.gz"].Body, assetsByPath(reversed.Assets)["search.json.gz"].Body) {
		t.Fatal("search asset is not deterministic")
	}
}

func TestCompilePreservesSourceAndDistinguishesPublishers(t *testing.T) {
	songs := []*models.Song{
		{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "Sound Choice", Disc: "SC8101-02", CanonicalID: "can_1", IsAvailable: true},
		{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "KaraFun", CanonicalID: "can_1", IsAvailable: true},
		{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "Sunfly", CanonicalID: "can_1", IsAvailable: true},
	}

	compiled, err := Compile(songs, Options{SchemaVersion: 1})
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	if compiled.Manifest.Total != 3 {
		t.Fatalf("catalog total = %d; want 3 distinct publisher versions", compiled.Manifest.Total)
	}
	assets := assetsByPath(compiled.Assets)
	search, err := readSearchAsset(assets["search.json.gz"].Body)
	if err != nil {
		t.Fatalf("read search asset: %v", err)
	}
	sources := make(map[string]bool, len(search.Tracks))
	ids := make(map[string]bool, len(search.Tracks))
	for _, tr := range search.Tracks {
		sources[tr.Source] = true
		ids[tr.ID] = true
		if tr.CanonicalID != "can_1" {
			t.Fatalf("track %+v missing canonical_id", tr)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 unique track IDs, got %d", len(ids))
	}
	for _, want := range []string{"Sound Choice", "KaraFun", "Sunfly"} {
		if !sources[want] {
			t.Fatalf("missing compiled source %q in %+v", want, search.Tracks)
		}
	}
}

func TestCompileRejectsInvalidOptions(t *testing.T) {
	if _, err := Compile(nil, Options{}); err == nil {
		t.Fatal("compile without a schema version succeeded")
	}
	if _, err := Compile(nil, Options{SchemaVersion: 1, ShardSize: -1}); err == nil {
		t.Fatal("compile with negative shard size succeeded")
	}
}

func BenchmarkCompile100K(b *testing.B) {
	songs := make([]*models.Song, 100000)
	for index := range songs {
		songs[index] = &models.Song{
			Artist:      fmt.Sprintf("Artist %05d", index%5000),
			Title:       fmt.Sprintf("Song %06d", index),
			Disc:        fmt.Sprintf("Disc %03d", index%200),
			IsAvailable: true,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := Compile(songs, Options{SchemaVersion: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

func assetsByPath(assets []Asset) map[string]Asset {
	result := make(map[string]Asset, len(assets))
	for _, asset := range assets {
		result[asset.Path] = asset
	}
	return result
}

func readSearchAsset(body []byte) (searchDocument, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return searchDocument{}, err
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return searchDocument{}, err
	}
	var search searchDocument
	return search, json.Unmarshal(decoded, &search)
}
