package reference

import (
	"archive/zip"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type xlsxInlineStrNode struct {
	T xlsxTextNode  `xml:"t"`
	R []xlsxRunNode `xml:"r"`
}

type xlsxCellNode struct {
	Ref string            `xml:"r,attr"`
	T   string            `xml:"t,attr"`
	V   string            `xml:"v"`
	IS  xlsxInlineStrNode `xml:"is"`
}

type xlsxRowNode struct {
	Cells []xlsxCellNode `xml:"c"`
}

type xlsxHeaderCols struct {
	rowIdx    int
	codeCol   int
	titleCol  int
	artistCol int
}

func isSunflyIgnoredLine(trimmed string) bool {
	return trimmed == "" ||
		strings.Contains(strings.ToLower(trimmed), "sunfly karaoke") ||
		sunflyFooterRe.MatchString(trimmed)
}

const sunflySongTitleHeader = "Song Title"

func isSunflyColumnHeader(line string) bool {
	return strings.Contains(line, "Made Famous By") && strings.Contains(line, sunflySongTitleHeader)
}

func computeSunflySplitCol(line string, current int) int {
	firstTitleByte := strings.Index(line, sunflySongTitleHeader)
	secondArtistByte := strings.LastIndex(line, "Made Famous By")
	if firstTitleByte <= 0 || secondArtistByte <= firstTitleByte {
		return current
	}
	leftTitleEndRune := len([]rune(line[:firstTitleByte+len(sunflySongTitleHeader)]))
	rightArtistRune := len([]rune(line[:secondArtistByte]))
	if rightArtistRune > leftTitleEndRune {
		return (leftTitleEndRune + rightArtistRune) / 2
	}
	return current
}

func isDoubleSpaceAt(runes []rune, idx int) bool {
	return idx > 0 && idx < len(runes)-1 && runes[idx] == ' ' && runes[idx-1] == ' '
}

func findSunflySplitCut(runes []rune, splitRuneCol int) int {
	for delta := 0; delta <= 12; delta++ {
		if idx := splitRuneCol - delta; isDoubleSpaceAt(runes, idx) {
			return idx
		}
		if idx := splitRuneCol + delta; isDoubleSpaceAt(runes, idx) {
			return idx
		}
	}
	return -1
}

func appendSunflyTrack(tracks []CanonicalTrack, rawArtist, rawTitle, provider string) []CanonicalTrack {
	a := strings.TrimSpace(rawArtist)
	t := strings.TrimSpace(rawTitle)
	if a == "" || t == "" || isTruncatedCell(a) || isTruncatedCell(t) {
		return tracks
	}
	if sunflyFooterRe.MatchString(a) || sunflyFooterRe.MatchString(t) {
		return tracks
	}
	cleanArtist := UninvertArtist(a)
	cleanTitle := StripNoiseTags(uninvertPDFTitle(t))
	if cleanArtist == "" || cleanTitle == "" {
		return tracks
	}
	return append(tracks, CanonicalTrack{
		ID:     GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist: cleanArtist,
		Title:  cleanTitle,
		Source: provider,
	})
}

func parseSunflyFallbackLine(line, provider string, splitRuneCol int) []CanonicalTrack {
	if splitRuneCol <= 0 {
		return nil
	}
	runes := []rune(line)
	if len(runes) <= splitRuneCol {
		return nil
	}
	cut := findSunflySplitCut(runes, splitRuneCol)
	if cut <= 0 {
		return nil
	}
	var out []CanonicalTrack
	chunks := []string{
		strings.TrimSpace(string(runes[:cut])),
		strings.TrimSpace(string(runes[cut:])),
	}
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		if cols := sunflyColRe.Split(chunk, 2); len(cols) == 2 {
			out = appendSunflyTrack(out, cols[0], cols[1], provider)
		}
	}
	return out
}

func readAndParseSharedStrings(f *zip.File) ([]string, error) {
	data, err := readBoundedZipFile(f, maxXLSXEntryBytes)
	if err != nil {
		return nil, fmt.Errorf("read sharedStrings: %w", err)
	}
	sharedStrings, err := parseXLSXSharedStrings(data)
	if err != nil {
		return nil, fmt.Errorf("parse sharedStrings: %w", err)
	}
	return sharedStrings, nil
}

func readXLSXEntries(zr *zip.Reader) ([]string, []byte, error) {
	var sharedStrings []string
	var sheetBytes []byte

	for _, f := range zr.File {
		switch f.Name {
		case "xl/sharedStrings.xml":
			var err error
			sharedStrings, err = readAndParseSharedStrings(f)
			if err != nil {
				return nil, nil, err
			}
		case "xl/worksheets/sheet1.xml":
			var err error
			sheetBytes, err = readBoundedZipFile(f, maxXLSXEntryBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("read sheet1: %w", err)
			}
		}
	}

	if len(sheetBytes) == 0 {
		return nil, nil, fmt.Errorf("xlsx missing xl/worksheets/sheet1.xml")
	}
	return sharedStrings, sheetBytes, nil
}

func matchXLSXHeaderRow(row []string) (int, int, int, bool) {
	codeCol, titleCol, artistCol := -1, -1, -1
	for i, h := range row {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "code", "disc", "disc_code", "discid", "catalog", "catalogue":
			codeCol = i
		case "song title", "title", "song":
			titleCol = i
		case "artist", "performer":
			artistCol = i
		}
	}
	return codeCol, titleCol, artistCol, titleCol >= 0 && artistCol >= 0
}

func findXLSXHeaderCols(rows [][]string) (xlsxHeaderCols, error) {
	scanLimit := len(rows)
	if scanLimit > 10 {
		scanLimit = 10
	}
	for rIdx := 0; rIdx < scanLimit; rIdx++ {
		cCol, tCol, aCol, ok := matchXLSXHeaderRow(rows[rIdx])
		if ok {
			return xlsxHeaderCols{
				rowIdx:    rIdx,
				codeCol:   cCol,
				titleCol:  tCol,
				artistCol: aCol,
			}, nil
		}
	}
	return xlsxHeaderCols{}, fmt.Errorf("xlsx header missing required artist/title columns in first %d rows", scanLimit)
}

func resolveXLSXDiscCode(rawCode, provider string) string {
	trimmed := strings.TrimSpace(rawCode)
	if trimmed == "" {
		return ""
	}
	cand := NormalizeDiscCode(trimmed)
	// Strict Provider Guard: only map valid disc codes that do not collide
	// with another authoritative manufacturer's disc prefix.
	if !xlsxDiscCodeRe.MatchString(cand) {
		return ""
	}
	claimedBy := ResolveProvider(cand, "")
	if claimedBy == "" || strings.EqualFold(claimedBy, provider) {
		return cand
	}
	return ""
}

func buildXLSXTrack(row []string, cols xlsxHeaderCols, provider string) (CanonicalTrack, bool) {
	if cols.artistCol >= len(row) || cols.titleCol >= len(row) {
		return CanonicalTrack{}, false
	}
	rawArtist := strings.TrimSpace(row[cols.artistCol])
	rawTitle := strings.TrimSpace(row[cols.titleCol])
	if rawArtist == "" || rawTitle == "" {
		return CanonicalTrack{}, false
	}
	cleanArtist := UninvertArtist(rawArtist)
	cleanTitle := StripNoiseTags(uninvertPDFTitle(rawTitle))
	if cleanArtist == "" || cleanTitle == "" {
		return CanonicalTrack{}, false
	}

	var normDisc string
	if cols.codeCol >= 0 && cols.codeCol < len(row) {
		normDisc = resolveXLSXDiscCode(row[cols.codeCol], provider)
	}

	return CanonicalTrack{
		ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist:   cleanArtist,
		Title:    cleanTitle,
		DiscCode: normDisc,
		Source:   provider,
	}, true
}

func joinXLSXRuns(runs []xlsxRunNode) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.T.Text)
	}
	return b.String()
}

func resolveXLSXCellIndex(ref string, currentLen int) (int, bool) {
	idx := xlsxColIndex(ref)
	if idx == -2 || idx > maxXLSXColIndex {
		return 0, false
	}
	if idx < 0 {
		if currentLen > maxXLSXColIndex {
			return 0, false
		}
		return currentLen, true
	}
	return idx, true
}

func extractXLSXCellValue(c xlsxCellNode, sharedStrings []string) string {
	switch c.T {
	case "s":
		if si, err := strconv.Atoi(strings.TrimSpace(c.V)); err == nil && si >= 0 && si < len(sharedStrings) {
			return sharedStrings[si]
		}
		return ""
	case "inlineStr":
		if len(c.IS.R) > 0 {
			return joinXLSXRuns(c.IS.R)
		}
		return c.IS.T.Text
	default:
		return c.V
	}
}

func parseXLSXRow(cells []xlsxCellNode, sharedStrings []string) []string {
	var vals []string
	for _, c := range cells {
		idx, ok := resolveXLSXCellIndex(c.Ref, len(vals))
		if !ok {
			continue
		}
		for len(vals) <= idx {
			vals = append(vals, "")
		}
		vals[idx] = extractXLSXCellValue(c, sharedStrings)
	}
	return vals
}

func findBillboardHeaderCols(header []string) (int, int, int, error) {
	artistCol, titleCol, dateCol := -1, -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "performer", "artist":
			artistCol = i
		case "title", "song":
			titleCol = i
		case "chart_week", "chart_date", "date":
			dateCol = i
		}
	}
	if artistCol < 0 || titleCol < 0 {
		return 0, 0, 0, fmt.Errorf("billboard csv missing artist/title columns: %v", header)
	}
	return artistCol, titleCol, dateCol, nil
}

func readBillboardCSVRecord(cr *csv.Reader) ([]string, error) {
	for {
		rec, err := cr.Read()
		if err == nil {
			return rec, nil
		}
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			continue
		}
		return nil, err
	}
}

func extractBillboardYear(rec []string, dateCol int) int {
	if dateCol < 0 || dateCol >= len(rec) {
		return 0
	}
	m := year4DigitRe.FindString(rec[dateCol])
	if m == "" {
		return 0
	}
	y, err := strconv.Atoi(m)
	if err != nil {
		return 0
	}
	return y
}

func parseBillboardRecord(rec []string, artistCol, titleCol, dateCol int) (string, string, int, bool) {
	if artistCol >= len(rec) || titleCol >= len(rec) {
		return "", "", 0, false
	}
	// Billboard artist & title names are already in natural (uninverted) order;
	// do NOT call UninvertArtist (which would corrupt "Tyler, The Creator").
	cleanArtist := strings.TrimSpace(rec[artistCol])
	cleanTitle := StripNoiseTags(strings.TrimSpace(rec[titleCol]))
	if cleanArtist == "" || cleanTitle == "" {
		return "", "", 0, false
	}
	return cleanArtist, cleanTitle, extractBillboardYear(rec, dateCol), true
}

func mergeBillboardTrack(unique map[string]CanonicalTrack, artist, title, genre, source string, year int) {
	key := CompositeKey(artist, title)
	if existing, ok := unique[key]; ok {
		if year > 0 && (existing.Year == 0 || year < existing.Year) {
			existing.Year = year
			unique[key] = existing
		}
		return
	}
	unique[key] = CanonicalTrack{
		ID:     GenerateCanonicalID(artist, title),
		Artist: artist,
		Title:  title,
		Genre:  genre,
		Year:   year,
		Source: source,
	}
}

func sortedTracksFromMap(unique map[string]CanonicalTrack) []CanonicalTrack {
	keys := make([]string, 0, len(unique))
	for k := range unique {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tracks := make([]CanonicalTrack, 0, len(keys))
	for _, k := range keys {
		tracks = append(tracks, unique[k])
	}
	return tracks
}
