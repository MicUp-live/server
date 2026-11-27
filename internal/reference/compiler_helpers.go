package reference

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func buildQuotedCSVHeaderIndex(header []string) map[string]int {
	colIndex := make(map[string]int, len(header))
	for i, col := range header {
		colIndex[strings.ToLower(strings.Trim(col, `"`))] = i
	}
	return colIndex
}

func buildTrimmedCSVHeaderIndex(header []string) map[string]int {
	colIndex := make(map[string]int, len(header))
	for i, col := range header {
		colIndex[strings.ToLower(strings.TrimSpace(col))] = i
	}
	return colIndex
}

func requireCSVColumns(colIndex map[string]int, cols ...string) error {
	for _, reqCol := range cols {
		if _, ok := colIndex[reqCol]; !ok {
			return fmt.Errorf("missing required column: %s", reqCol)
		}
	}
	return nil
}

func csvField(record []string, colIndex map[string]int, name string) (string, bool) {
	idx, ok := colIndex[name]
	if !ok || idx >= len(record) {
		return "", false
	}
	return record[idx], true
}

func parseKaraFunRecord(record []string, colIndex map[string]int) (CanonicalTrack, bool) {
	title := strings.Trim(record[colIndex["title"]], `" `)
	artist := strings.Trim(record[colIndex["artist"]], `" `)
	if title == "" || artist == "" {
		return CanonicalTrack{}, false
	}

	cleanArtist := UninvertArtist(artist)
	cleanTitle := StripNoiseTags(title)
	track := CanonicalTrack{
		ID:     GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist: cleanArtist,
		Title:  cleanTitle,
		Source: "karafun",
	}

	if val, ok := csvField(record, colIndex, "styles"); ok {
		track.Genre = strings.Trim(val, `" `)
	}
	if val, ok := csvField(record, colIndex, "year"); ok {
		if y, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && y > 1900 && y < 2100 {
			track.Year = y
		}
	}
	if val, ok := csvField(record, colIndex, "duo"); ok {
		track.Duo = strings.TrimSpace(val) == "1"
	}
	if val, ok := csvField(record, colIndex, "explicit"); ok {
		track.Explicit = strings.TrimSpace(val) == "1"
	}

	return track, true
}

func parseKnownSongbookFormat(text, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack, bool) {
	if strings.Contains(text, "Partytyme.net") || strings.Contains(text, "CDG-MP3G") {
		tracks, dm := parsePartyTymeCatalogText(text)
		if len(dm) > 0 {
			return tracks, dm, true
		}
	}
	if strings.Contains(text, "Songs by Artist") && strings.Contains(text, "DiscID") {
		tracks, dm := parseTwoColumnArtistHeaderText(text, defaultProvider)
		if len(dm) > 0 {
			return tracks, dm, true
		}
	}
	if strings.Contains(text, "Made Famous By") && strings.Contains(text, "Song Title") {
		tracks := parseSunflyTwoColumnText(text, defaultProvider)
		if len(tracks) > 0 {
			return tracks, make(map[string]CanonicalTrack), true
		}
	}
	return nil, nil, false
}

func isSongbookHeaderOrFooter(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "page ") ||
		strings.HasPrefix(lower, "catalogue") ||
		strings.Contains(lower, "industrial estate") ||
		strings.Contains(lower, "all rights reserved") ||
		strings.Contains(lower, "song title") ||
		strings.HasPrefix(lower, "artist")
}

func extractSongbookColumns(parts []string, discCodeRegex *regexp.Regexp) (string, string, string) {
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), ""
	}
	if len(parts) < 3 {
		return "", "", ""
	}
	first := strings.TrimSpace(parts[0])
	if discCodeRegex.MatchString(first) {
		return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), first
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if discCodeRegex.MatchString(last) {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), last
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), ""
}

func resolveSongbookProvider(discCode, defaultProvider, cleanArtist string) (string, string) {
	if discCode != "" {
		normDisc := NormalizeDiscCode(discCode)
		if p := ResolveProvider(normDisc, ""); p != "" {
			return normDisc, p
		}
		return normDisc, defaultProvider
	}
	if defaultProvider == "" {
		return "", ResolveProvider("", cleanArtist)
	}
	return "", defaultProvider
}

func parseSongbookLine(line, defaultProvider string, colSplitRegex, discCodeRegex *regexp.Regexp) (CanonicalTrack, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || isSongbookHeaderOrFooter(trimmed) {
		return CanonicalTrack{}, false
	}

	parts := colSplitRegex.Split(trimmed, -1)
	if len(parts) < 2 {
		return CanonicalTrack{}, false
	}

	artist, title, discCode := extractSongbookColumns(parts, discCodeRegex)
	if artist == "" || title == "" {
		return CanonicalTrack{}, false
	}

	cleanArtist := UninvertArtist(artist)
	cleanTitle := StripNoiseTags(title)
	normDisc, provider := resolveSongbookProvider(discCode, defaultProvider, cleanArtist)

	return CanonicalTrack{
		ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist:   cleanArtist,
		Title:    cleanTitle,
		DiscCode: normDisc,
		Source:   provider,
	}, true
}

func extractDiscCodeFromRecord(record []string, colIndex map[string]int) string {
	if val, ok := csvField(record, colIndex, "disc_code"); ok {
		return strings.TrimSpace(val)
	}
	discVal, ok := csvField(record, colIndex, "disc")
	if !ok {
		return ""
	}
	discCode := strings.TrimSpace(discVal)
	trackVal, ok := csvField(record, colIndex, "track")
	if !ok {
		return discCode
	}
	tNum := strings.TrimSpace(trackVal)
	if tNum != "" && !strings.Contains(discCode, "-") {
		return fmt.Sprintf("%s-%02s", discCode, tNum)
	}
	return discCode
}

func resolveDiscMapSource(record []string, colIndex map[string]int, normDisc string) string {
	source := ResolveProvider(normDisc, "")
	for _, col := range []string{"source", "provider"} {
		if val, ok := csvField(record, colIndex, col); ok {
			if s := strings.TrimSpace(val); s != "" {
				return s
			}
			break
		}
	}
	if source == "" {
		return "disc_map"
	}
	return source
}

func parseDiscMapRecord(record []string, colIndex map[string]int) (CanonicalTrack, bool) {
	discCode := extractDiscCodeFromRecord(record, colIndex)
	artistVal, _ := csvField(record, colIndex, "artist")
	titleVal, _ := csvField(record, colIndex, "title")
	artist := strings.TrimSpace(artistVal)
	title := strings.TrimSpace(titleVal)

	if discCode == "" || artist == "" || title == "" {
		return CanonicalTrack{}, false
	}

	cleanArtist := UninvertArtist(artist)
	cleanTitle := StripNoiseTags(title)
	normDisc := NormalizeDiscCode(discCode)

	return CanonicalTrack{
		ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist:   cleanArtist,
		Title:    cleanTitle,
		DiscCode: normDisc,
		Source:   resolveDiscMapSource(record, colIndex, normDisc),
	}, true
}

func mergeTrackMetadata(dst, src CanonicalTrack) CanonicalTrack {
	if dst.Genre == "" && src.Genre != "" {
		dst.Genre = src.Genre
	}
	if dst.Year == 0 && src.Year != 0 {
		dst.Year = src.Year
	}
	return dst
}

func buildDictTrackIndex(dictTracks []CanonicalTrack) map[string]CanonicalTrack {
	dictIndex := make(map[string]CanonicalTrack, len(dictTracks))
	for _, t := range dictTracks {
		key := CompositeKey(t.Artist, t.Title)
		if existing, ok := dictIndex[key]; ok {
			dictIndex[key] = mergeTrackMetadata(existing, t)
		} else {
			dictIndex[key] = t
		}
	}
	return dictIndex
}
