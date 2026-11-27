package reference

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxXLSXEntryBytes = 64 << 20 // 64 MiB cap against zip bombs
	maxXLSXColIndex   = 256      // cap column index to avoid OOM on malformed cell refs
)

var (
	sunflyGapRe    = regexp.MustCompile(`\s{3,}`)
	sunflyColRe    = regexp.MustCompile(`\s{2,}`)
	sunflyFooterRe = regexp.MustCompile(`(?i)^page\s+\d+\b`)
	xlsxDiscCodeRe = regexp.MustCompile(`^[A-Z]{2,6}\d{2,6}[A-Z]?(?:[-_]\d{1,3}){0,2}$`)
	year4DigitRe   = regexp.MustCompile(`\b(19\d\d|20\d\d)\b`)
)

func isTruncatedCell(s string) bool {
	return strings.HasSuffix(s, "...") || strings.HasSuffix(s, "\u2026")
}

// parseSunflyTwoColumnText parses the 2-column Sunfly "A-Z by Artist" PDF layout
// ("Made Famous By   Song Title   Made Famous By   Song Title").
// Rows with truncated ("..." or "…") artist or title cells are skipped to keep canonical names clean.
func parseSunflyTwoColumnText(text, defaultProvider string) []CanonicalTrack {
	provider := strings.TrimSpace(defaultProvider)
	if provider == "" {
		provider = "Sunfly"
	}

	var tracks []CanonicalTrack
	// Track per-page rune-based right-half split boundary derived from the header row.
	splitRuneCol := -1

	for _, line := range strings.Split(text, "\n") {
		line = strings.Trim(line, "\r\f")
		trimmed := strings.TrimSpace(line)
		if isSunflyIgnoredLine(trimmed) {
			continue
		}
		if isSunflyColumnHeader(line) {
			splitRuneCol = computeSunflySplitCol(line, splitRuneCol)
			continue
		}

		// Prefer 3+-space splitting when it yields an exact 4-column row.
		parts := sunflyGapRe.Split(trimmed, -1)
		if len(parts) == 4 {
			tracks = appendSunflyTrack(tracks, parts[0], parts[1], provider)
			tracks = appendSunflyTrack(tracks, parts[2], parts[3], provider)
			continue
		}
		if len(parts) == 2 {
			tracks = appendSunflyTrack(tracks, parts[0], parts[1], provider)
			continue
		}

		// Fallback for tightly packed 2-column rows: split at the widest whitespace gap near splitRuneCol.
		tracks = append(tracks, parseSunflyFallbackLine(line, provider, splitRuneCol)...)
	}

	return tracks
}

func readBoundedZipFile(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("xlsx entry %q exceeds %d byte limit", f.Name, limit)
	}
	return data, nil
}

// ParseMrEntertainerXLSX parses an official Mr Entertainer Excel (.xlsx) catalog
// using only the Go standard library (archive/zip + encoding/xml).
func ParseMrEntertainerXLSX(rawBytes []byte, provider string) (map[string]CanonicalTrack, []CanonicalTrack, error) {
	if strings.TrimSpace(provider) == "" {
		provider = "Mr Entertainer"
	}

	zr, err := zip.NewReader(bytes.NewReader(rawBytes), int64(len(rawBytes)))
	if err != nil {
		return nil, nil, fmt.Errorf("open xlsx zip: %w", err)
	}

	sharedStrings, sheetBytes, err := readXLSXEntries(zr)
	if err != nil {
		return nil, nil, err
	}

	rows, err := parseXLSXSheetRows(sheetBytes, sharedStrings)
	if err != nil {
		return nil, nil, fmt.Errorf("parse sheet1: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("xlsx sheet1 has no data rows")
	}

	// Scan up to the first 10 rows for the header row (handles optional leading banner rows).
	cols, err := findXLSXHeaderCols(rows)
	if err != nil {
		return nil, nil, err
	}

	discMap := make(map[string]CanonicalTrack, len(rows))
	tracks := make([]CanonicalTrack, 0, len(rows))

	for _, row := range rows[cols.rowIdx+1:] {
		track, ok := buildXLSXTrack(row, cols, provider)
		if !ok {
			continue
		}
		tracks = append(tracks, track)
		if track.DiscCode != "" {
			discMap[track.DiscCode] = track
		}
	}

	return discMap, tracks, nil
}

type xlsxTextNode struct {
	Text string `xml:",chardata"`
}

type xlsxRunNode struct {
	T xlsxTextNode `xml:"t"`
}

func parseXLSXSharedStrings(data []byte) ([]string, error) {
	type siNode struct {
		T xlsxTextNode  `xml:"t"`
		R []xlsxRunNode `xml:"r"`
	}
	type sst struct {
		SI []siNode `xml:"si"`
	}
	var doc sst
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := make([]string, len(doc.SI))
	for i, si := range doc.SI {
		if len(si.R) > 0 {
			var b strings.Builder
			for _, r := range si.R {
				b.WriteString(r.T.Text)
			}
			out[i] = b.String()
		} else {
			out[i] = si.T.Text
		}
	}
	return out, nil
}

func xlsxColIndex(ref string) int {
	col := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		if ch >= 'A' && ch <= 'Z' {
			col = col*26 + int(ch-'A'+1)
		} else if ch >= 'a' && ch <= 'z' {
			col = col*26 + int(ch-'a'+1)
		} else {
			break
		}
		if col > maxXLSXColIndex+1 {
			return -2
		}
	}
	return col - 1
}

func parseXLSXSheetRows(data []byte, sharedStrings []string) ([][]string, error) {
	type sheetDoc struct {
		Rows []xlsxRowNode `xml:"sheetData>row"`
	}
	var doc sheetDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := make([][]string, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		if len(r.Cells) == 0 {
			continue
		}
		out = append(out, parseXLSXRow(r.Cells, sharedStrings))
	}
	return out, nil
}

// stripUTF8BOM wraps r so any leading UTF-8 BOM (0xEF 0xBB 0xBF) is discarded
// before csv.NewReader parses quoted headers like \uFEFF"artist".
func stripUTF8BOM(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	bom, err := br.Peek(3)
	if err == nil && len(bom) == 3 && bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		_, _ = br.Discard(3)
	}
	return br
}

// ParseBillboardCSV parses historical Billboard weekly chart CSVs (UT Austin Hot 100
// or chartscraper Country/R&B/Rock formats), deduplicating weekly appearances into
// unique canonical tracks with their earliest chart year.
func ParseBillboardCSV(r io.Reader, defaultGenre, sourceName string) ([]CanonicalTrack, error) {
	cr := csv.NewReader(stripUTF8BOM(r))
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read billboard csv header: %w", err)
	}

	artistCol, titleCol, dateCol, err := findBillboardHeaderCols(header)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(sourceName) == "" {
		sourceName = "Billboard"
	}

	unique := make(map[string]CanonicalTrack, 32000)
	for {
		rec, err := readBillboardCSVRecord(cr)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read billboard csv row: %w", err)
		}
		artist, title, year, ok := parseBillboardRecord(rec, artistCol, titleCol, dateCol)
		if !ok {
			continue
		}
		mergeBillboardTrack(unique, artist, title, defaultGenre, sourceName, year)
	}

	return sortedTracksFromMap(unique), nil
}

// WriteTracksCSV writes a deterministic sorted CSV of (artist, title, genre, year, source)
// for archiving track-only PDFs (like Sunfly) and deduplicated chart feeds.
func WriteTracksCSV(w io.Writer, tracks []CanonicalTrack) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"artist", "title", "genre", "year", "source"}); err != nil {
		return err
	}
	sorted := make([]CanonicalTrack, len(tracks))
	copy(sorted, tracks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Artist != sorted[j].Artist {
			return sorted[i].Artist < sorted[j].Artist
		}
		if sorted[i].Title != sorted[j].Title {
			return sorted[i].Title < sorted[j].Title
		}
		if sorted[i].Year != sorted[j].Year {
			return sorted[i].Year < sorted[j].Year
		}
		if sorted[i].Genre != sorted[j].Genre {
			return sorted[i].Genre < sorted[j].Genre
		}
		return sorted[i].Source < sorted[j].Source
	})
	for _, t := range sorted {
		yearStr := ""
		if t.Year > 0 {
			yearStr = strconv.Itoa(t.Year)
		}
		if err := cw.Write([]string{t.Artist, t.Title, t.Genre, yearStr, t.Source}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
