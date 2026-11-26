// Package catalog compiles public, immutable song catalog assets.
package catalog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"micup.live/server/internal/models"
)

const defaultShardSize = 500

// Options controls a deterministic catalog compilation.
type Options struct {
	SchemaVersion int
	ShardSize     int
}

// Asset is one upload-ready catalog object relative to a catalog version prefix.
type Asset struct {
	Path            string
	Body            []byte
	ContentType     string
	ContentEncoding string
}

// Manifest identifies the public files needed to browse one catalog version.
type Manifest struct {
	SchemaVersion int                       `json:"schema_version"`
	CatalogID     string                    `json:"catalog_id"`
	Total         int                       `json:"total"`
	Letters       map[string]LetterManifest `json:"letters"`
	SearchPath    string                    `json:"search_path"`
}

// LetterManifest describes the shard files for one title-initial bucket.
type LetterManifest struct {
	Count  int      `json:"count"`
	Shards []string `json:"shards"`
}

// Compilation is a complete immutable catalog build ready for object storage.
// LocationsAsset is optional host-only metadata uploaded under private/songbooks/{id}/
// and must never be placed in public catalog prefixes.
type Compilation struct {
	CatalogID      string
	Manifest       Manifest
	Assets         []Asset
	LocationsAsset *Asset
}

type publicTrack struct {
	ID          string `json:"song_id"`
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Label       string `json:"label,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Disc        string `json:"disc,omitempty"`
	Source      string `json:"source,omitempty"`
	VideoURL    string `json:"video_url,omitempty"`
	CanonicalID string `json:"canonical_id,omitempty"`
	IsCustom    bool   `json:"is_custom,omitempty"`
	SortKey     string `json:"-"`
}

type searchDocument struct {
	Tracks []publicTrack `json:"songs"`
}

// Compile turns available songs into deterministic public catalog assets. The
// resulting CatalogID is a content hash, enabling identical imports to reuse
// the same S3 objects and catalog version record.
func Compile(songs []*models.Song, options Options) (*Compilation, error) {
	if options.SchemaVersion <= 0 {
		return nil, fmt.Errorf("compile catalog: schema version is required")
	}
	shardSize := options.ShardSize
	if shardSize == 0 {
		shardSize = defaultShardSize
	}
	if shardSize < 1 {
		return nil, fmt.Errorf("compile catalog: shard size must be positive")
	}

	tracks, locations := canonicalTracks(songs)
	catalogID := catalogID(options.SchemaVersion, tracks)
	buckets := make(map[string][]publicTrack)
	for _, track := range tracks {
		letter := titleLetter(track.Title)
		buckets[letter] = append(buckets[letter], track)
	}

	manifest := Manifest{
		SchemaVersion: options.SchemaVersion,
		CatalogID:     catalogID,
		Total:         len(tracks),
		Letters:       make(map[string]LetterManifest, len(buckets)),
		SearchPath:    "search.json.gz",
	}
	assets := make([]Asset, 0, len(buckets)+2)
	for _, letter := range sortedLetters(buckets) {
		tracksForLetter := buckets[letter]
		shards := make([]string, 0, (len(tracksForLetter)+shardSize-1)/shardSize)
		for offset, shardNumber := 0, 0; offset < len(tracksForLetter); offset, shardNumber = offset+shardSize, shardNumber+1 {
			end := min(offset+shardSize, len(tracksForLetter))
			path := fmt.Sprintf("letters/%s/%03d.json", letterPathPart(letter), shardNumber)
			body, err := json.Marshal(struct {
				Songs []publicTrack `json:"songs"`
			}{Songs: tracksForLetter[offset:end]})
			if err != nil {
				return nil, fmt.Errorf("compile catalog letter %s: %w", letter, err)
			}
			assets = append(assets, Asset{Path: path, Body: body, ContentType: "application/json"})
			shards = append(shards, path)
		}
		manifest.Letters[letter] = LetterManifest{Count: len(tracksForLetter), Shards: shards}
	}

	searchBody, err := gzipJSON(searchDocument{Tracks: tracks})
	if err != nil {
		return nil, fmt.Errorf("compile catalog search: %w", err)
	}
	assets = append(assets, Asset{Path: manifest.SearchPath, Body: searchBody, ContentType: "application/json", ContentEncoding: "gzip"})
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("compile catalog manifest: %w", err)
	}
	assets = append(assets, Asset{Path: "manifest.json", Body: manifestBody, ContentType: "application/json"})

	var locationsAsset *Asset
	if len(locations) > 0 {
		locBody, err := gzipJSON(locations)
		if err != nil {
			return nil, fmt.Errorf("compile catalog locations: %w", err)
		}
		locationsAsset = &Asset{Path: "locations.json.gz", Body: locBody, ContentType: "application/json", ContentEncoding: "gzip"}
	}
	return &Compilation{CatalogID: catalogID, Manifest: manifest, Assets: assets, LocationsAsset: locationsAsset}, nil
}

func canonicalTracks(songs []*models.Song) ([]publicTrack, map[string]string) {
	unique := make(map[string]publicTrack, len(songs))
	locations := make(map[string]string)
	for _, song := range songs {
		if song == nil || !song.IsAvailable {
			continue
		}
		track := publicTrack{
			Artist: normalize(song.Artist), Title: normalize(song.Title), Genre: normalize(song.Genre),
			Disc: normalize(song.Disc), Source: normalize(song.Source), VideoURL: strings.TrimSpace(song.VideoURL),
			CanonicalID: strings.TrimSpace(song.CanonicalID), IsCustom: song.IsCustom,
		}
		if track.Title == "" && track.Artist == "" {
			continue
		}
		track.Label = models.BuildLabel(track.Artist, track.Title)
		track.ID = trackID(track)
		track.SortKey = strings.ToLower(track.Title) + "\x00" + strings.ToLower(track.Artist) + "\x00" + track.ID
		unique[track.ID] = track
		if loc := strings.TrimSpace(song.Location); loc != "" {
			locations[track.ID] = loc
		}
	}
	tracks := make([]publicTrack, 0, len(unique))
	for _, track := range unique {
		tracks = append(tracks, track)
	}
	sort.Slice(tracks, func(i, j int) bool {
		return tracks[i].SortKey < tracks[j].SortKey
	})
	return tracks, locations
}

func catalogID(schemaVersion int, tracks []publicTrack) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "schema:%d\n", schemaVersion)
	for _, track := range tracks {
		hash.Write([]byte(track.ID))
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func trackID(track publicTrack) string {
	key := track.Artist + "\x00" + track.Title + "\x00" + track.Genre + "\x00" + track.Disc + "\x00" + track.VideoURL
	if track.Source != "" {
		key += "\x00" + track.Source
	}
	sum := sha256.Sum256([]byte(key))
	return "trk_" + hex.EncodeToString(sum[:])[:24]
}

func titleLetter(title string) string {
	for _, r := range strings.TrimSpace(title) {
		r = unicode.ToUpper(r)
		if r >= 'A' && r <= 'Z' {
			return string(r)
		}
		return "#"
	}
	return "#"
}

func sortedLetters(buckets map[string][]publicTrack) []string {
	letters := make([]string, 0, len(buckets))
	for letter := range buckets {
		letters = append(letters, letter)
	}
	sort.Slice(letters, func(i, j int) bool {
		if letters[i] == "#" {
			return true
		}
		if letters[j] == "#" {
			return false
		}
		return letters[i] < letters[j]
	})
	return letters
}

func letterPathPart(letter string) string {
	if letter == "#" {
		return "other"
	}
	return letter
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	if !strings.ContainsAny(value, "\t\n\r") && !strings.Contains(value, "  ") {
		return value
	}
	return strings.Join(strings.Fields(value), " ")
}

func gzipJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = time.Unix(0, 0)
	writer.Header.OS = 255
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
