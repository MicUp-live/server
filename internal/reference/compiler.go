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

	colIndex := make(map[string]int)
	for i, col := range header {
		colIndex[strings.ToLower(strings.Trim(col, `"`))] = i
	}

	for _, reqCol := range []string{"title", "artist"} {
		if _, ok := colIndex[reqCol]; !ok {
			return nil, fmt.Errorf("missing required column: %s", reqCol)
		}
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

		title := strings.Trim(record[colIndex["title"]], `" `)
		artist := strings.Trim(record[colIndex["artist"]], `" `)
		if title == "" || artist == "" {
			continue
		}

		cleanArtist := UninvertArtist(artist)
		cleanTitle := StripNoiseTags(title)
		id := GenerateCanonicalID(cleanArtist, cleanTitle)

		track := CanonicalTrack{
			ID:     id,
			Artist: cleanArtist,
			Title:  cleanTitle,
			Source: "karafun",
		}

		if idx, ok := colIndex["styles"]; ok && idx < len(record) {
			track.Genre = strings.Trim(record[idx], `" `)
		}
		if idx, ok := colIndex["year"]; ok && idx < len(record) {
			if y, err := strconv.Atoi(strings.TrimSpace(record[idx])); err == nil && y > 1900 && y < 2100 {
				track.Year = y
			}
		}
		if idx, ok := colIndex["duo"]; ok && idx < len(record) {
			track.Duo = strings.TrimSpace(record[idx]) == "1"
		}
		if idx, ok := colIndex["explicit"]; ok && idx < len(record) {
			track.Explicit = strings.TrimSpace(record[idx]) == "1"
		}

		tracks = append(tracks, track)
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
	if strings.Contains(text, "Partytyme.net") || strings.Contains(text, "CDG-MP3G") {
		tracks, dm := parsePartyTymeCatalogText(text)
		if len(dm) > 0 {
			return tracks, dm, nil
		}
	}
	if strings.Contains(text, "Songs by Artist") && strings.Contains(text, "DiscID") {
		tracks, dm := parseTwoColumnArtistHeaderText(text, defaultProvider)
		if len(dm) > 0 {
			return tracks, dm, nil
		}
	}
	if strings.Contains(text, "Made Famous By") && strings.Contains(text, "Song Title") {
		tracks := parseSunflyTwoColumnText(text, defaultProvider)
		if len(tracks) > 0 {
			return tracks, make(map[string]CanonicalTrack), nil
		}
	}
	lines := strings.Split(text, "\n")
	colSplitRegex := regexp.MustCompile(`\s{2,}`)
	discCodeRegex := regexp.MustCompile(`(?i)^[A-Z]{2,5}\d{2,6}[A-Z]?([-_]\d{1,3}){0,2}$`)

	var tracks []CanonicalTrack
	discMap := make(map[string]CanonicalTrack)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip common page headers and footers
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "page ") || strings.HasPrefix(lower, "catalogue") ||
			strings.Contains(lower, "industrial estate") || strings.Contains(lower, "all rights reserved") ||
			strings.Contains(lower, "song title") || strings.HasPrefix(lower, "artist") {
			continue
		}

		parts := colSplitRegex.Split(trimmed, -1)
		if len(parts) < 2 {
			continue
		}

		var artist, title, discCode string

		if len(parts) == 2 {
			artist = strings.TrimSpace(parts[0])
			title = strings.TrimSpace(parts[1])
		} else if len(parts) >= 3 {
			// Check if first or third column is a disc code
			if discCodeRegex.MatchString(strings.TrimSpace(parts[0])) {
				discCode = strings.TrimSpace(parts[0])
				artist = strings.TrimSpace(parts[1])
				title = strings.TrimSpace(parts[2])
			} else if discCodeRegex.MatchString(strings.TrimSpace(parts[len(parts)-1])) {
				artist = strings.TrimSpace(parts[0])
				title = strings.TrimSpace(parts[1])
				discCode = strings.TrimSpace(parts[len(parts)-1])
			} else {
				artist = strings.TrimSpace(parts[0])
				title = strings.TrimSpace(parts[1])
			}
		}

		if artist == "" || title == "" {
			continue
		}

		cleanArtist := UninvertArtist(artist)
		cleanTitle := StripNoiseTags(title)
		id := GenerateCanonicalID(cleanArtist, cleanTitle)

		provider := defaultProvider
		if discCode != "" {
			normDisc := NormalizeDiscCode(discCode)
			discCode = normDisc
			if p := ResolveProvider(normDisc, ""); p != "" {
				provider = p
			}
		} else if provider == "" {
			provider = ResolveProvider("", cleanArtist)
		}

		track := CanonicalTrack{
			ID:       id,
			Artist:   cleanArtist,
			Title:    cleanTitle,
			DiscCode: discCode,
			Source:   provider,
		}

		if discCode != "" {
			discMap[discCode] = track
		}
		tracks = append(tracks, track)
	}

	return tracks, discMap, nil
}

// ParsePDFSongbook executes pdftotext -layout on a PDF file and parses the columnar text.
func ParsePDFSongbook(pdfPath, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack, error) {
	cmd := exec.Command("pdftotext", "-layout", pdfPath, "-")
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

	colIndex := make(map[string]int)
	for i, col := range header {
		colIndex[strings.ToLower(strings.TrimSpace(col))] = i
	}

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

		var discCode, artist, title string
		if idx, ok := colIndex["disc_code"]; ok && idx < len(record) {
			discCode = strings.TrimSpace(record[idx])
		} else if idx, ok := colIndex["disc"]; ok && idx < len(record) {
			discCode = strings.TrimSpace(record[idx])
			if trackIdx, ok := colIndex["track"]; ok && trackIdx < len(record) {
				tNum := strings.TrimSpace(record[trackIdx])
				if tNum != "" && !strings.Contains(discCode, "-") {
					discCode = fmt.Sprintf("%s-%02s", discCode, tNum)
				}
			}
		}

		if idx, ok := colIndex["artist"]; ok && idx < len(record) {
			artist = strings.TrimSpace(record[idx])
		}
		if idx, ok := colIndex["title"]; ok && idx < len(record) {
			title = strings.TrimSpace(record[idx])
		}

		if discCode == "" || artist == "" || title == "" {
			continue
		}

		cleanArtist := UninvertArtist(artist)
		cleanTitle := StripNoiseTags(title)
		id := GenerateCanonicalID(cleanArtist, cleanTitle)

		normDisc := NormalizeDiscCode(discCode)
		source := ResolveProvider(normDisc, "")
		if idx, ok := colIndex["source"]; ok && idx < len(record) {
			if s := strings.TrimSpace(record[idx]); s != "" {
				source = s
			}
		} else if idx, ok := colIndex["provider"]; ok && idx < len(record) {
			if s := strings.TrimSpace(record[idx]); s != "" {
				source = s
			}
		}
		if source == "" {
			source = "disc_map"
		}
		track := CanonicalTrack{
			ID:       id,
			Artist:   cleanArtist,
			Title:    cleanTitle,
			DiscCode: normDisc,
			Source:   source,
		}

		discMap[normDisc] = track
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
	dictIndex := make(map[string]CanonicalTrack, len(dictTracks))
	for _, t := range dictTracks {
		key := CompositeKey(t.Artist, t.Title)
		if existing, ok := dictIndex[key]; ok {
			if existing.Genre == "" && t.Genre != "" {
				existing.Genre = t.Genre
			}
			if existing.Year == 0 && t.Year != 0 {
				existing.Year = t.Year
			}
			dictIndex[key] = existing
		} else {
			dictIndex[key] = t
		}
	}

	for k, disc := range discMap {
		key := CompositeKey(disc.Artist, disc.Title)
		if match, ok := dictIndex[key]; ok {
			if disc.Genre == "" && match.Genre != "" {
				disc.Genre = match.Genre
			}
			if disc.Year == 0 && match.Year != 0 {
				disc.Year = match.Year
			}
			discMap[k] = disc
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
