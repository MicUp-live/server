// Package reference compiles authoritative reference datasets into immutable S3 dictionary assets.
package reference

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// CanonicalTrack represents one authoritative commercial track.
type CanonicalTrack struct {
	ID       string `json:"id"`
	Artist   string `json:"artist"`
	Title    string `json:"title"`
	Genre    string `json:"genre,omitempty"`
	Year     int    `json:"year,omitempty"`
	Duo      bool   `json:"duo,omitempty"`
	Explicit bool   `json:"explicit,omitempty"`
	DiscCode string `json:"disc_code,omitempty"`
	Source   string `json:"source,omitempty"`
}

// DictionaryAsset is the top-level payload for dictionary.json.gz.
type DictionaryAsset struct {
	Version     int                       `json:"version"`
	GeneratedAt time.Time                 `json:"generated_at"`
	TotalTracks int                       `json:"total_tracks"`
	Tracks      map[string]CanonicalTrack `json:"tracks"` // composite key: "artist:::title"
}

// DiscMapAsset is the top-level payload for disc_map.json.gz.
type DiscMapAsset struct {
	Version     int                       `json:"version"`
	GeneratedAt time.Time                 `json:"generated_at"`
	TotalDiscs  int                       `json:"total_discs"`
	Discs       map[string]CanonicalTrack `json:"discs"` // disc code key: e.g. "SC8101-02"
}

var (
	noiseTagRegex    = regexp.MustCompile(`(?i)\s*[\(\[](karaoke version|karaoke|instrumental|backing track|remastered|remastered \d{4}|original mix|album version|without backing vocals|with backing vocals|no backing vocals|with harmony|no harmony|with multiplex lead vocal|multiplex|original male key|original female key|original key|for solo male|for solo female|for solo singer|end cut down|\d+\s+semitones?\s+(up|down)|(one|two|three|four|five|six)\s+semitones?\s+(up|down)|\d{4}\s+remake)[\)\]]`)
	bracketDiscRegex = regexp.MustCompile(`(?i)\s*\[[a-z]{2,5}\d{3,5}(-\d{1,2})?\]`)
	punctuationRegex = regexp.MustCompile(`[^a-z0-9\s]`)
	multiSpaceRegex  = regexp.MustCompile(`\s+`)
	pnkRegex         = regexp.MustCompile(`(?i)\bp!nk\b`)
)

// artistAliases maps known aliases, acronyms, and stylizations to a canonical normalized artist string.
var artistAliases = map[string]string{
	// Acronyms to full canonical name
	"ccr":                              "creedence clearwater revival",
	"elo":                              "electric light orchestra",
	"csny":                             "crosby stills nash and young",
	"csn":                              "crosby stills and nash",
	"omd":                              "orchestral manoeuvres in the dark",
	"orchestral maneuvers in the dark": "orchestral manoeuvres in the dark",
	"boc":                              "blue oyster cult",

	// Stylizations & band name variants
	"guns n roses":              "guns and roses",
	"guns n  roses":             "guns and roses",
	"snoop doggy dogg":          "snoop dogg",
	"kool and the gang":         "kool and the gang",
	"kool the gang":             "kool and the gang",
	"daryl hall and john oates": "hall and oates",
	"daryl hall john oates":     "hall and oates",
	"hall and oates":            "hall and oates",
	"huey lewis and the news":   "huey lewis and the news",
	"huey lewis the news":       "huey lewis and the news",
	"kc and the sunshine band":  "kc and the sunshine band",
	"kc the sunshine band":      "kc and the sunshine band",
	"doctor dre":                "dr dre",
}

// RemoveDiacritics strips accents and diacritics (e.g., "é" -> "e").
func RemoveDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return result
}

// UninvertArtist transforms inverted names like "Beatles, The" -> "The Beatles"
// and "Presley, Elvis" -> "Elvis Presley".
func UninvertArtist(artist string) string {
	artist = strings.TrimSpace(artist)
	if !strings.Contains(artist, ",") {
		return artist
	}

	parts := strings.Split(artist, ",")
	if len(parts) != 2 {
		return artist
	}

	firstPart := strings.TrimSpace(parts[0])
	secondPart := strings.TrimSpace(parts[1])

	// Article check (The, A, An)
	lowerSecond := strings.ToLower(secondPart)
	if lowerSecond == "the" || lowerSecond == "a" || lowerSecond == "an" {
		return secondPart + " " + firstPart
	}

	// Standard "Last, First" inversion
	if len(strings.Fields(secondPart)) <= 2 && len(strings.Fields(firstPart)) <= 2 {
		return secondPart + " " + firstPart
	}

	return artist
}

// StripNoiseTags removes generic tags like "(Karaoke Version)" or "[SC8101]" from titles.
func StripNoiseTags(title string) string {
	title = noiseTagRegex.ReplaceAllString(title, "")
	title = bracketDiscRegex.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

// NormalizeKey strips diacritics, expands conjunctions (&, + -> and), strips punctuation, and normalizes whitespace.
func NormalizeKey(s string) string {
	s = RemoveDiacritics(s)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.ReplaceAll(s, "+", " and ")
	s = punctuationRegex.ReplaceAllString(s, " ")
	s = multiSpaceRegex.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// NormalizeArtist un-inverts inverted names, normalizes stylizations (P!nk, Ke$ha),
// expands conjunctions (&, + -> and), and resolves MusicBrainz aliases.
func NormalizeArtist(artist string) string {
	artist = UninvertArtist(artist)
	artist = pnkRegex.ReplaceAllString(artist, "pink")
	artist = strings.ReplaceAll(artist, "$", "s")
	norm := NormalizeKey(artist)
	if canonical, exists := artistAliases[norm]; exists {
		return canonical
	}
	return norm
}

// NormalizeTitle strips noise tags, expands conjunctions, and removes punctuation.
func NormalizeTitle(title string) string {
	clean := StripNoiseTags(title)
	return NormalizeKey(clean)
}

// CompositeKey generates a lookup key: normalize(artist) + ":::" + normalize(title).
func CompositeKey(artist, title string) string {
	return NormalizeArtist(artist) + ":::" + NormalizeTitle(title)
}

// GenerateCanonicalID creates a deterministic 16-hex canonical ID from artist and title.
func GenerateCanonicalID(artist, title string) string {
	hash := sha256.Sum256([]byte(CompositeKey(artist, title)))
	return "can_" + hex.EncodeToString(hash[:])[:16]
}

// ParseKaraFunCSV parses KaraFun's official semicolon-delimited CSV catalog.
// Header: Id;Title;Artist;Year;Duo;Explicit;Date Added;Styles;Languages
func ParseKaraFunCSV(r io.Reader) ([]CanonicalTrack, error) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	colIndex := buildQuotedCSVHeaderIndex(header)
	if err := requireCSVColumns(colIndex, "title", "artist"); err != nil {
		return nil, err
	}

	var tracks []CanonicalTrack
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed lines
		}
		if track, ok := parseKaraFunRecord(record, colIndex); ok {
			tracks = append(tracks, track)
		}
	}

	return tracks, nil
}

// ParseZoomCSV parses Zoom Karaoke's official CSV export (Artist,"Song title").
func ParseZoomCSV(r io.Reader) ([]CanonicalTrack, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	colIndex := make(map[string]int)
	for i, col := range header {
		colIndex[strings.ToLower(strings.Trim(col, `"`))] = i
	}

	artistIdx, hasArtist := colIndex["artist"]
	titleIdx, hasTitle := colIndex["song title"]
	if !hasTitle {
		titleIdx, hasTitle = colIndex["title"]
	}
	if !hasArtist || !hasTitle {
		return nil, fmt.Errorf("missing required column in Zoom CSV")
	}

	var tracks []CanonicalTrack
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		if artistIdx >= len(record) || titleIdx >= len(record) {
			continue
		}

		artist := strings.Trim(record[artistIdx], `" `)
		title := strings.Trim(record[titleIdx], `" `)
		if artist == "" || title == "" {
			continue
		}

		cleanArtist := UninvertArtist(artist)
		cleanTitle := StripNoiseTags(title)
		id := GenerateCanonicalID(cleanArtist, cleanTitle)

		tracks = append(tracks, CanonicalTrack{
			ID:     id,
			Artist: cleanArtist,
			Title:  cleanTitle,
			Source: "Zoom Karaoke",
		})
	}

	return tracks, nil
}

// ParseSongbookText parses tabular multi-column songbook text extracted from PDFs.
// Supports 2-column (Artist, Title) and 3-column (Disc, Artist, Title or Artist, Title, Disc) layouts.
func ParseSongbookText(text, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack, error) {
	if tracks, dm, ok := parseKnownSongbookFormat(text, defaultProvider); ok {
		return tracks, dm, nil
	}

	colSplitRegex := regexp.MustCompile(`\s{2,}`)
	discCodeRegex := regexp.MustCompile(`(?i)^[A-Z]{2,5}\d{2,6}[A-Z]?([-_]\d{1,3}){0,2}$`)

	var tracks []CanonicalTrack
	discMap := make(map[string]CanonicalTrack)

	for _, line := range strings.Split(text, "\n") {
		track, ok := parseSongbookLine(line, defaultProvider, colSplitRegex, discCodeRegex)
		if !ok {
			continue
		}
		if track.DiscCode != "" {
			discMap[track.DiscCode] = track
		}
		tracks = append(tracks, track)
	}

	return tracks, discMap, nil
}

var pdftotextCandidates = []string{
	"/usr/bin/pdftotext",
	"/usr/local/bin/pdftotext",
	"/opt/homebrew/bin/pdftotext",
}

func resolvePdftotextPath() (string, error) {
	for _, candidate := range pdftotextCandidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("pdftotext binary not found in standard system paths (ensure poppler-utils is installed)")
}

// ParsePDFSongbook executes pdftotext -layout on a PDF file and parses the columnar text.
func ParsePDFSongbook(pdfPath, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack, error) {
	binPath, err := resolvePdftotextPath()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(binPath, "-layout", pdfPath, "-")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("pdftotext failed (ensure poppler-utils is installed): %w", err)
	}

	return ParseSongbookText(string(output), defaultProvider)
}

// ParseDiscMapCSV parses a disc mapping CSV (e.g. "disc_code,artist,title" or "disc,track,artist,title").
func ParseDiscMapCSV(r io.Reader) (map[string]CanonicalTrack, []CanonicalTrack, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read disc header: %w", err)
	}

	colIndex := buildTrimmedCSVHeaderIndex(header)
	discMap := make(map[string]CanonicalTrack)
	var tracks []CanonicalTrack

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		track, ok := parseDiscMapRecord(record, colIndex)
		if !ok {
			continue
		}
		discMap[track.DiscCode] = track
		tracks = append(tracks, track)
	}

	return discMap, tracks, nil
}

// NormalizeDiscCode formats disc codes to uppercase without extra symbols (e.g. "sc8101-2" -> "SC8101-02", "nsc2000_01_01" -> "NSC2000-01-01").
func NormalizeDiscCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	re := regexp.MustCompile(`^([A-Z0-9]+)[-_]+(\d+)$`)
	matches := re.FindStringSubmatch(code)
	if len(matches) == 3 {
		discPart := matches[1]
		trackNum, err := strconv.Atoi(matches[2])
		if err == nil {
			return fmt.Sprintf("%s-%02d", discPart, trackNum)
		}
	}
	return regexp.MustCompile(`[-_]+`).ReplaceAllString(code, "-")
}

// CompileDictionary packages unique canonical tracks into gzipped JSON.
func CompileDictionary(tracks []CanonicalTrack) ([]byte, error) {
	unique := make(map[string]CanonicalTrack, len(tracks))
	for _, track := range tracks {
		key := CompositeKey(track.Artist, track.Title)
		if existing, exists := unique[key]; exists {
			// Enrich with genre or year if missing
			if existing.Genre == "" && track.Genre != "" {
				existing.Genre = track.Genre
			}
			if existing.Year == 0 && track.Year != 0 {
				existing.Year = track.Year
			}
			unique[key] = existing
		} else {
			unique[key] = track
		}
	}

	asset := DictionaryAsset{
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		TotalTracks: len(unique),
		Tracks:      unique,
	}

	return gzipJSON(asset)
}

// EnrichDiscMap enriches disc mappings with metadata (Genre, Year) from canonical tracks.
func EnrichDiscMap(discMap map[string]CanonicalTrack, dictTracks []CanonicalTrack) {
	dictIndex := buildDictTrackIndex(dictTracks)
	for k, disc := range discMap {
		key := CompositeKey(disc.Artist, disc.Title)
		if match, ok := dictIndex[key]; ok {
			discMap[k] = mergeTrackMetadata(disc, match)
		}
	}
}

// CompileDiscMap packages disc mappings into gzipped JSON.
func CompileDiscMap(discMap map[string]CanonicalTrack) ([]byte, error) {
	asset := DiscMapAsset{
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		TotalDiscs:  len(discMap),
		Discs:       discMap,
	}

	return gzipJSON(asset)
}

func gzipJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = time.Unix(0, 0) // deterministic timestamp
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
