package reference

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
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
	lines := strings.Split(text, "\n")

	// Track per-page rune-based right-half split boundary derived from the header row.
	splitRuneCol := -1

	addPair := func(rawArtist, rawTitle string) {
		a := strings.TrimSpace(rawArtist)
		t := strings.TrimSpace(rawTitle)
		if a == "" || t == "" || isTruncatedCell(a) || isTruncatedCell(t) {
			return
		}
		if sunflyFooterRe.MatchString(a) || sunflyFooterRe.MatchString(t) {
			return
		}
		cleanArtist := UninvertArtist(a)
		cleanTitle := StripNoiseTags(uninvertPDFTitle(t))
		if cleanArtist == "" || cleanTitle == "" {
			return
		}
		tracks = append(tracks, CanonicalTrack{
			ID:     GenerateCanonicalID(cleanArtist, cleanTitle),
			Artist: cleanArtist,
			Title:  cleanTitle,
			Source: provider,
		})
	}

	for _, line := range lines {
		line = strings.Trim(line, "\r\f")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(strings.ToLower(trimmed), "sunfly karaoke") || sunflyFooterRe.MatchString(trimmed) {
			continue
		}
		if strings.Contains(line, "Made Famous By") && strings.Contains(line, "Song Title") {
			// Compute rune-based midpoint between end of left "Song Title" and start of right "Made Famous By".
			firstTitleByte := strings.Index(line, "Song Title")
			secondArtistByte := strings.LastIndex(line, "Made Famous By")
			if firstTitleByte > 0 && secondArtistByte > firstTitleByte {
				leftTitleEndRune := len([]rune(line[:firstTitleByte+len("Song Title")]))
				rightArtistRune := len([]rune(line[:secondArtistByte]))
				if rightArtistRune > leftTitleEndRune {
					splitRuneCol = (leftTitleEndRune + rightArtistRune) / 2
				}
			}
			continue
		}

		// Prefer 3+-space splitting when it yields an exact 4-column row.
		parts := sunflyGapRe.Split(trimmed, -1)
		if len(parts) == 4 {
			addPair(parts[0], parts[1])
			addPair(parts[2], parts[3])
			continue
		}
		if len(parts) == 2 {
			addPair(parts[0], parts[1])
			continue
		}

		// Fallback for tightly packed 2-column rows: split at the widest whitespace gap near splitRuneCol.
		if splitRuneCol > 0 {
			runes := []rune(line)
			if len(runes) <= splitRuneCol {
				continue
			}
			// Find a space boundary near splitRuneCol (searching +/-12 runes) so we never slice inside a word.
			cut := -1
			for delta := 0; delta <= 12; delta++ {
				if idx := splitRuneCol - delta; idx > 0 && idx < len(runes)-1 && runes[idx] == ' ' && runes[idx-1] == ' ' {
					cut = idx
					break
				}
				if idx := splitRuneCol + delta; idx > 0 && idx < len(runes)-1 && runes[idx] == ' ' && runes[idx-1] == ' ' {
					cut = idx
					break
				}
			}
			if cut <= 0 {
				continue
			}
			leftChunk := strings.TrimSpace(string(runes[:cut]))
			rightChunk := strings.TrimSpace(string(runes[cut:]))
			for _, chunk := range []string{leftChunk, rightChunk} {
				if chunk == "" {
					continue
				}
				cols := sunflyColRe.Split(chunk, 2)
				if len(cols) == 2 {
					addPair(cols[0], cols[1])
				}
			}
		}
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

	var sharedStrings []string
	var sheetBytes []byte

	for _, f := range zr.File {
		switch f.Name {
		case "xl/sharedStrings.xml":
			data, err := readBoundedZipFile(f, maxXLSXEntryBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("read sharedStrings: %w", err)
			}
			sharedStrings, err = parseXLSXSharedStrings(data)
			if err != nil {
				return nil, nil, fmt.Errorf("parse sharedStrings: %w", err)
			}
		case "xl/worksheets/sheet1.xml":
			sheetBytes, err = readBoundedZipFile(f, maxXLSXEntryBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("read sheet1: %w", err)
			}
		}
	}

	if len(sheetBytes) == 0 {
		return nil, nil, fmt.Errorf("xlsx missing xl/worksheets/sheet1.xml")
	}

	rows, err := parseXLSXSheetRows(sheetBytes, sharedStrings)
	if err != nil {
		return nil, nil, fmt.Errorf("parse sheet1: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("xlsx sheet1 has no data rows")
	}

	// Scan up to the first 10 rows for the header row (handles optional leading banner rows).
	headerRowIdx := -1
	codeCol, titleCol, artistCol := -1, -1, -1
	scanLimit := len(rows)
	if scanLimit > 10 {
		scanLimit = 10
	}
	for rIdx := 0; rIdx < scanLimit; rIdx++ {
		cCol, tCol, aCol := -1, -1, -1
		for i, h := range rows[rIdx] {
			hl := strings.ToLower(strings.TrimSpace(h))
			switch hl {
			case "code", "disc", "disc_code", "discid", "catalog", "catalogue":
				cCol = i
			case "song title", "title", "song":
				tCol = i
			case "artist", "performer":
				aCol = i
			}
		}
		if tCol >= 0 && aCol >= 0 {
			headerRowIdx = rIdx
			codeCol, titleCol, artistCol = cCol, tCol, aCol
			break
		}
	}
	if headerRowIdx < 0 {
		return nil, nil, fmt.Errorf("xlsx header missing required artist/title columns in first %d rows", scanLimit)
	}

	discMap := make(map[string]CanonicalTrack, len(rows))
	tracks := make([]CanonicalTrack, 0, len(rows))

	for _, row := range rows[headerRowIdx+1:] {
		if artistCol >= len(row) || titleCol >= len(row) {
			continue
		}
		rawArtist := strings.TrimSpace(row[artistCol])
		rawTitle := strings.TrimSpace(row[titleCol])
		if rawArtist == "" || rawTitle == "" {
			continue
		}
		cleanArtist := UninvertArtist(rawArtist)
		cleanTitle := StripNoiseTags(uninvertPDFTitle(rawTitle))
		if cleanArtist == "" || cleanTitle == "" {
			continue
		}

		var normDisc string
		if codeCol >= 0 && codeCol < len(row) {
			if rawCode := strings.TrimSpace(row[codeCol]); rawCode != "" {
				cand := NormalizeDiscCode(rawCode)
				// Strict Provider Guard: only map valid disc codes that do not collide
				// with another authoritative manufacturer's disc prefix.
				if xlsxDiscCodeRe.MatchString(cand) {
					claimedBy := ResolveProvider(cand, "")
					if claimedBy == "" || strings.EqualFold(claimedBy, provider) {
						normDisc = cand
					}
				}
			}
		}

		track := CanonicalTrack{
			ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
			Artist:   cleanArtist,
			Title:    cleanTitle,
			DiscCode: normDisc,
			Source:   provider,
		}
		tracks = append(tracks, track)
		if normDisc != "" {
			discMap[normDisc] = track
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
	type inlineStrNode struct {
		T xlsxTextNode  `xml:"t"`
		R []xlsxRunNode `xml:"r"`
	}
	type cellNode struct {
		Ref string        `xml:"r,attr"`
		T   string        `xml:"t,attr"`
		V   string        `xml:"v"`
		IS  inlineStrNode `xml:"is"`
	}
	type rowNode struct {
		Cells []cellNode `xml:"c"`
	}
	type sheetDoc struct {
		Rows []rowNode `xml:"sheetData>row"`
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
		var vals []string
		for _, c := range r.Cells {
			idx := xlsxColIndex(c.Ref)
			if idx == -2 || idx > maxXLSXColIndex {
				continue
			}
			if idx < 0 {
				idx = len(vals)
				if idx > maxXLSXColIndex {
					continue
				}
			}
			for len(vals) <= idx {
				vals = append(vals, "")
			}
			switch c.T {
			case "s":
				if si, err := strconv.Atoi(strings.TrimSpace(c.V)); err == nil && si >= 0 && si < len(sharedStrings) {
					vals[idx] = sharedStrings[si]
				}
			case "inlineStr":
				if len(c.IS.R) > 0 {
					var b strings.Builder
					for _, run := range c.IS.R {
						b.WriteString(run.T.Text)
					}
					vals[idx] = b.String()
				} else {
					vals[idx] = c.IS.T.Text
				}
			default:
				vals[idx] = c.V
			}
		}
		out = append(out, vals)
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

	artistCol, titleCol, dateCol := -1, -1, -1
	for i, h := range header {
		hl := strings.ToLower(strings.TrimSpace(h))
		switch hl {
		case "performer", "artist":
			artistCol = i
		case "title", "song":
			titleCol = i
		case "chart_week", "chart_date", "date":
			dateCol = i
		}
	}
	if artistCol < 0 || titleCol < 0 {
		return nil, fmt.Errorf("billboard csv missing artist/title columns: %v", header)
	}

	if strings.TrimSpace(sourceName) == "" {
		sourceName = "Billboard"
	}

	unique := make(map[string]CanonicalTrack, 32000)
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				continue
			}
			return nil, fmt.Errorf("read billboard csv row: %w", err)
		}
		if artistCol >= len(rec) || titleCol >= len(rec) {
			continue
		}
		// Billboard artist & title names are already in natural (uninverted) order;
		// do NOT call UninvertArtist (which would corrupt "Tyler, The Creator").
		cleanArtist := strings.TrimSpace(rec[artistCol])
		cleanTitle := StripNoiseTags(strings.TrimSpace(rec[titleCol]))
		if cleanArtist == "" || cleanTitle == "" {
			continue
		}

		year := 0
		if dateCol >= 0 && dateCol < len(rec) {
			if m := year4DigitRe.FindString(rec[dateCol]); m != "" {
				if y, convErr := strconv.Atoi(m); convErr == nil {
					year = y
				}
			}
		}

		key := CompositeKey(cleanArtist, cleanTitle)
		if existing, ok := unique[key]; ok {
			if year > 0 && (existing.Year == 0 || year < existing.Year) {
				existing.Year = year
				unique[key] = existing
			}
			continue
		}
		unique[key] = CanonicalTrack{
			ID:     GenerateCanonicalID(cleanArtist, cleanTitle),
			Artist: cleanArtist,
			Title:  cleanTitle,
			Genre:  defaultGenre,
			Year:   year,
			Source: sourceName,
		}
	}

	keys := make([]string, 0, len(unique))
	for k := range unique {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tracks := make([]CanonicalTrack, 0, len(keys))
	for _, k := range keys {
		tracks = append(tracks, unique[k])
	}
	return tracks, nil
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
