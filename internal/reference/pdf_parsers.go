package reference

import (
	"regexp"
	"strings"
)

var (
	partyTymeRowRe       = regexp.MustCompile(`^\s*(?:NEW\s+)?(?:\d{1,2}/\d{1,2}/\d{4}\s+)?(PH\d{4,6})?(?:\s+(PY\d{4,6}))?\s{2,}(.+)$`)
	partyTymePageNumRe   = regexp.MustCompile(`^\s*\d+\s*$`)
	partyTymeGenreWordRe = regexp.MustCompile(`(?i)^(contemporary|female|male|duet|english|spanish|french|italian|german)$`)
	colGapRe             = regexp.MustCompile(`\s{2,}`)
	nonSpaceChunkRe      = regexp.MustCompile(`\S+(?:\s\S+)*`)
	wideGapRe            = regexp.MustCompile(`\s{3,}`)
	scHeaderColsRe       = regexp.MustCompile(`(?i)^\s*Title\s+DiscID\s+Title\s+DiscID\s*$`)
	scDiscTokenRe        = regexp.MustCompile(`\s{2,}([A-Z]{2,8}[A-Z0-9]*(?:-{1,2}\d{1,2}){1,2})\b`)
	scDiscSuffixRe       = regexp.MustCompile(`^(.*\S)\s+([A-Z]{2,8}[A-Z0-9]*(?:-{1,2}\d{1,2}){1,2})$`)
	wvocalSuffixRe       = regexp.MustCompile(`(?i)^\(w/?vocal\)$|\s*\(w/?vocal\)\s*$`)
	trailArticleRe       = regexp.MustCompile(`(?i)^(.+?),\s*(the|a|an)(\s*(?:\(.*\))?)$`)
)

func uninvertPDFTitle(title string) string {
	s := strings.TrimSpace(title)
	if !strings.Contains(s, ",") {
		return s
	}
	if m := trailArticleRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[2] + " " + strings.TrimSpace(m[1]) + m[3])
	}
	return s
}

func cleanPartyTymeGenre(raw string) string {
	g := strings.TrimSpace(raw)
	for _, suffix := range []string{" Female", " Male", " Duet"} {
		if strings.HasSuffix(g, suffix) {
			g = strings.TrimSpace(strings.TrimSuffix(g, suffix))
		}
	}
	return g
}

type pendingPartyTymeRow struct {
	hdCode    string
	cdgCode   string
	rawTitle  string
	rawArtist string
	rawGenre  string
	artistCol int
	genreCol  int
}

// parsePartyTymeCatalogText parses the official Party Tyme multi-column PDF text
// (columns: [NEW] [Date] [PH#####] [PY#####] Title Artist Genre Lang),
// assembling wrapped title/artist/genre continuation lines by column offset.
func parsePartyTymeCatalogText(text string) ([]CanonicalTrack, map[string]CanonicalTrack) {
	lines := strings.Split(text, "\n")
	var tracks []CanonicalTrack
	discMap := make(map[string]CanonicalTrack, 60000)

	var pending *pendingPartyTymeRow
	for _, line := range lines {
		line = strings.TrimRight(line, "\r\f")
		if m := partyTymeRowRe.FindStringSubmatch(line); hasPartyTymeRowCode(m) {
			if row, ok := parsePartyTymeRowStart(line, m); ok {
				tracks = flushPartyTymePending(pending, tracks, discMap)
				pending = row
			}
			continue
		}
		if pending == nil || isPartyTymeContinuationNoise(line) {
			continue
		}
		appendPartyTymeContinuation(pending, line)
	}
	tracks = flushPartyTymePending(pending, tracks, discMap)
	return tracks, discMap
}

func hasPartyTymeRowCode(m []string) bool {
	return m != nil && (strings.TrimSpace(m[1]) != "" || strings.TrimSpace(m[2]) != "")
}

func flushPartyTymePending(pending *pendingPartyTymeRow, tracks []CanonicalTrack, discMap map[string]CanonicalTrack) []CanonicalTrack {
	if pending == nil {
		return tracks
	}
	cleanArtist := UninvertArtist(pending.rawArtist)
	cleanTitle := StripNoiseTags(uninvertPDFTitle(pending.rawTitle))
	if cleanArtist == "" || cleanTitle == "" {
		return tracks
	}
	primaryCode := pending.hdCode
	if primaryCode == "" {
		primaryCode = pending.cdgCode
	}
	track := CanonicalTrack{
		ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist:   cleanArtist,
		Title:    cleanTitle,
		Genre:    cleanPartyTymeGenre(pending.rawGenre),
		DiscCode: primaryCode,
		Source:   "Party Tyme",
	}
	tracks = append(tracks, track)
	if pending.hdCode != "" {
		t := track
		t.DiscCode = pending.hdCode
		discMap[pending.hdCode] = t
	}
	if pending.cdgCode != "" {
		t := track
		t.DiscCode = pending.cdgCode
		discMap[pending.cdgCode] = t
	}
	return tracks
}

func parsePartyTymeRowStart(line string, m []string) (*pendingPartyTymeRow, bool) {
	rest := strings.TrimSpace(m[3])
	cols := colGapRe.Split(rest, -1)
	if len(cols) < 2 {
		return nil, false
	}
	var rawGenre string
	if len(cols) >= 3 {
		rawGenre = strings.TrimSpace(cols[2])
	}
	artistCol, genreCol := measurePartyTymeColumns(line, rest, cols[1])
	return &pendingPartyTymeRow{
		hdCode:    strings.TrimSpace(m[1]),
		cdgCode:   strings.TrimSpace(m[2]),
		rawTitle:  cols[0],
		rawArtist: cols[1],
		rawGenre:  rawGenre,
		artistCol: artistCol,
		genreCol:  genreCol,
	}, true
}

func measurePartyTymeColumns(line, rest, artistText string) (int, int) {
	// Measure exact column start positions of Title, Artist, and Genre in the line.
	restStart := strings.Index(line, rest)
	if restStart < 0 {
		restStart = 35
	}
	chunkLocs := nonSpaceChunkRe.FindAllStringIndex(line[restStart:], -1)
	artistCol := 65
	if len(chunkLocs) >= 2 {
		artistCol = restStart + chunkLocs[1][0]
	}
	if len(chunkLocs) >= 3 {
		return artistCol, restStart + chunkLocs[2][0]
	}
	genreCol := artistCol + len(artistText) + 4
	if genreCol < 82 {
		genreCol = 82
	}
	return artistCol, genreCol
}

func isPartyTymeContinuationNoise(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || partyTymePageNumRe.MatchString(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "partytyme.net") ||
		(strings.HasPrefix(lower, "new") && strings.Contains(lower, "song title")) ||
		strings.HasPrefix(lower, "date of") {
		return true
	}
	indent := len(line) - len(strings.TrimLeft(line, " "))
	return indent < 28
}

func appendPartyTymeContinuation(pending *pendingPartyTymeRow, line string) {
	// Route each whitespace-separated chunk on the continuation line to Title, Artist, or Genre by its column start offset.
	for _, loc := range nonSpaceChunkRe.FindAllStringIndex(line, -1) {
		col := loc[0]
		chunk := strings.TrimSpace(line[loc[0]:loc[1]])
		if chunk == "" {
			continue
		}
		switch {
		case col >= pending.genreCol-5 || partyTymeGenreWordRe.MatchString(chunk):
			pending.rawGenre = strings.TrimSpace(pending.rawGenre + " " + chunk)
		case col >= pending.artistCol-5:
			pending.rawArtist = strings.TrimSpace(pending.rawArtist + " " + chunk)
		default:
			pending.rawTitle = strings.TrimSpace(pending.rawTitle + " " + chunk)
		}
	}
}

func isSoundChoiceBannerOrFooter(trimmed string) bool {
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "songs by artist") ||
		strings.Contains(lower, "karaoke shack") ||
		strings.Contains(lower, "karaokeshack.com") ||
		strings.HasPrefix(lower, "©") ||
		strings.HasPrefix(lower, "page ") ||
		strings.HasPrefix(lower, "sound choice karaoke") ||
		scHeaderColsRe.MatchString(trimmed) ||
		strings.EqualFold(trimmed, "title") ||
		strings.EqualFold(trimmed, "discid")
}

type scColumnCell struct {
	text     string
	indented bool // true when cell starts at the indented track-title margin rather than the flush-left artist margin
}

func splitTwoColumnLine(line string, colSplit, leftTitleCol, rightTitleCol int) (scColumnCell, scColumnCell) {
	rawLeft, rawRight, rightStart := splitTwoColumnRaw(line, colSplit)
	return scColumnCell{
			text:     rawLeft,
			indented: isCellIndented(rawLeft, 0, leftTitleCol, 1),
		}, scColumnCell{
			text:     rawRight,
			indented: isCellIndented(rawRight, rightStart, rightTitleCol, 0),
		}
}

func splitTwoColumnRaw(line string, colSplit int) (string, string, int) {
	// 1. If a disc code finishes in the left column, split right after that disc code.
	for _, loc := range scDiscTokenRe.FindAllStringIndex(line, -1) {
		if loc[1] <= colSplit+8 {
			return line[:loc[1]], line[loc[1]:], loc[1]
		}
	}
	if len(line) <= colSplit {
		return line, "", colSplit
	}
	if strings.TrimSpace(line[:colSplit]) == "" {
		return "", line[colSplit:], colSplit
	}
	for _, gap := range wideGapRe.FindAllStringIndex(line, -1) {
		if gap[1] >= colSplit-10 && gap[0] <= colSplit+12 {
			return line[:gap[1]], line[gap[1]:], gap[1]
		}
	}
	return line[:colSplit], line[colSplit:], colSplit
}

func isCellIndented(raw string, offset, titleCol, minIndent int) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	indent := len(raw) - len(strings.TrimLeft(raw, " "))
	return offset+indent >= titleCol && indent >= minIndent
}

// parseTwoColumnArtistHeaderText parses 2-column-per-page "Songs by Artist" PDFs
// (such as Sound Choice catalogs where section lines set the current artist
// and track lines contain "Title   SC####-##").
func parseTwoColumnArtistHeaderText(text, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack) {
	state := twoColumnParserState{
		defaultProvider: defaultProvider,
		discMap:         make(map[string]CanonicalTrack, 36000),
		lastTrackIdx:    -1,
	}
	for _, page := range strings.Split(text, "\f") {
		state.processPage(page)
	}
	return state.tracks, state.discMap
}

type twoColumnParserState struct {
	defaultProvider string
	tracks          []CanonicalTrack
	discMap         map[string]CanonicalTrack
	currentArtist   string
	lastTrackIdx    int
	lastDiscCode    string
	lastRawTitle    string
	lastWasArtist   bool
}

func (s *twoColumnParserState) resetColumnState() {
	s.lastWasArtist = false
	s.lastTrackIdx = -1
	s.lastDiscCode = ""
	s.lastRawTitle = ""
}

func (s *twoColumnParserState) flushPage(leftCells, rightCells []scColumnCell) {
	s.resetColumnState()
	for _, cell := range leftCells {
		s.processCell(cell)
	}
	s.resetColumnState()
	for _, cell := range rightCells {
		s.processCell(cell)
	}
}

func (s *twoColumnParserState) processCell(cell scColumnCell) {
	trimmed := strings.TrimSpace(cell.text)
	if isSoundChoiceBannerOrFooter(trimmed) {
		return
	}
	if m := scDiscSuffixRe.FindStringSubmatch(trimmed); m != nil {
		s.addTrackFromMatch(m)
		return
	}
	if len(trimmed) > 1 {
		s.processNonTrackCell(trimmed, cell.indented)
	}
}

func (s *twoColumnParserState) addTrackFromMatch(m []string) {
	if s.currentArtist == "" {
		return
	}
	rawTitlePart := strings.TrimSpace(m[1])
	rawTitle := uninvertPDFTitle(rawTitlePart)
	discCode := NormalizeDiscCode(strings.ToUpper(strings.TrimSpace(m[2])))
	cleanArtist := UninvertArtist(s.currentArtist)
	cleanTitle := StripNoiseTags(rawTitle)
	if cleanArtist == "" || cleanTitle == "" {
		return
	}
	provider := ResolveProvider(discCode, s.defaultProvider)
	if provider == "" {
		provider = s.defaultProvider
	}
	tr := CanonicalTrack{
		ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
		Artist:   cleanArtist,
		Title:    cleanTitle,
		DiscCode: discCode,
		Source:   provider,
	}
	s.discMap[discCode] = tr
	s.tracks = append(s.tracks, tr)
	s.lastTrackIdx = len(s.tracks) - 1
	s.lastDiscCode = discCode
	s.lastRawTitle = rawTitlePart
	s.lastWasArtist = false
}

func (s *twoColumnParserState) processNonTrackCell(trimmed string, indented bool) {
	artistHeader := strings.TrimSpace(wvocalSuffixRe.ReplaceAllString(trimmed, ""))
	if artistHeader == "" {
		// Standalone "(Wvocal)" continuation line: preserve currentArtist.
		return
	}
	if indented && s.lastTrackIdx >= 0 && s.lastDiscCode != "" && len(s.lastRawTitle) >= 32 {
		// Indented line immediately following a long wrapped track line is a song title continuation.
		s.lastRawTitle = strings.TrimSpace(s.lastRawTitle + " " + trimmed)
		prev := s.tracks[s.lastTrackIdx]
		combinedTitle := StripNoiseTags(uninvertPDFTitle(s.lastRawTitle))
		prev.Title = combinedTitle
		prev.ID = GenerateCanonicalID(prev.Artist, combinedTitle)
		s.tracks[s.lastTrackIdx] = prev
		s.discMap[s.lastDiscCode] = prev
		return
	}
	if s.lastWasArtist && s.currentArtist != "" && !strings.EqualFold(s.currentArtist, artistHeader) {
		s.currentArtist = strings.TrimSpace(s.currentArtist + " " + artistHeader)
	} else {
		s.currentArtist = artistHeader
	}
	s.lastWasArtist = true
	s.lastTrackIdx = -1
	s.lastDiscCode = ""
	s.lastRawTitle = ""
}

func (s *twoColumnParserState) processPage(page string) {
	var leftCells, rightCells []scColumnCell
	colSplit, leftTitleCol, rightTitleCol := 52, 1, 55
	for _, line := range strings.Split(page, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmedLine), "songs by artist") && (len(leftCells) > 0 || len(rightCells) > 0) {
			s.flushPage(leftCells, rightCells)
			leftCells, rightCells = leftCells[:0], rightCells[:0]
			colSplit, leftTitleCol, rightTitleCol = 52, 1, 55
			continue
		}
		if scHeaderColsRe.MatchString(trimmedLine) {
			colSplit, leftTitleCol, rightTitleCol = parseHeaderColumns(line, colSplit, leftTitleCol, rightTitleCol)
			continue
		}
		if isSoundChoiceBannerOrFooter(trimmedLine) {
			continue
		}
		leftCell, rightCell := splitTwoColumnLine(line, colSplit, leftTitleCol, rightTitleCol)
		leftCells = appendNonEmptyCell(leftCells, leftCell)
		rightCells = appendNonEmptyCell(rightCells, rightCell)
	}
	s.flushPage(leftCells, rightCells)
}

func parseHeaderColumns(line string, colSplit, leftTitleCol, rightTitleCol int) (int, int, int) {
	if firstTitle := strings.Index(line, "Title"); firstTitle >= 1 && firstTitle <= 6 {
		leftTitleCol = firstTitle
	}
	if idx := strings.Index(line, "DiscID"); idx > 15 {
		colSplit = idx + len("DiscID")
		if secondTitle := strings.Index(line[colSplit:], "Title"); secondTitle >= 0 {
			rightTitleCol = colSplit + secondTitle
		} else {
			rightTitleCol = colSplit + 6
		}
	}
	return colSplit, leftTitleCol, rightTitleCol
}

func appendNonEmptyCell(cells []scColumnCell, cell scColumnCell) []scColumnCell {
	if strings.TrimSpace(cell.text) == "" {
		return cells
	}
	return append(cells, cell)
}
