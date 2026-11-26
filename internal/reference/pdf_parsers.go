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
	flushPending := func() {
		if pending == nil {
			return
		}
		row := pending
		pending = nil
		cleanArtist := UninvertArtist(row.rawArtist)
		cleanTitle := StripNoiseTags(uninvertPDFTitle(row.rawTitle))
		if cleanArtist == "" || cleanTitle == "" {
			return
		}
		id := GenerateCanonicalID(cleanArtist, cleanTitle)
		primaryCode := row.hdCode
		if primaryCode == "" {
			primaryCode = row.cdgCode
		}
		track := CanonicalTrack{
			ID:       id,
			Artist:   cleanArtist,
			Title:    cleanTitle,
			Genre:    cleanPartyTymeGenre(row.rawGenre),
			DiscCode: primaryCode,
			Source:   "Party Tyme",
		}
		tracks = append(tracks, track)
		if row.hdCode != "" {
			t := track
			t.DiscCode = row.hdCode
			discMap[row.hdCode] = t
		}
		if row.cdgCode != "" {
			t := track
			t.DiscCode = row.cdgCode
			discMap[row.cdgCode] = t
		}
	}

	for _, line := range lines {
		line = strings.TrimRight(line, "\r\f")
		if m := partyTymeRowRe.FindStringSubmatch(line); m != nil && (strings.TrimSpace(m[1]) != "" || strings.TrimSpace(m[2]) != "") {
			hdCode := strings.TrimSpace(m[1])
			cdgCode := strings.TrimSpace(m[2])
			rest := strings.TrimSpace(m[3])
			cols := colGapRe.Split(rest, -1)
			if len(cols) < 2 {
				continue
			}
			flushPending()
			var rawGenre string
			if len(cols) >= 3 {
				rawGenre = strings.TrimSpace(cols[2])
			}
			// Measure exact column start positions of Title, Artist, and Genre in the line
			restStart := strings.Index(line, rest)
			if restStart < 0 {
				restStart = 35
			}
			chunkLocs := nonSpaceChunkRe.FindAllStringIndex(line[restStart:], -1)
			artistCol := 65
			genreCol := 85
			if len(chunkLocs) >= 2 {
				artistCol = restStart + chunkLocs[1][0]
			}
			if len(chunkLocs) >= 3 {
				genreCol = restStart + chunkLocs[2][0]
			} else {
				genreCol = artistCol + len(cols[1]) + 4
				if genreCol < 82 {
					genreCol = 82
				}
			}
			pending = &pendingPartyTymeRow{
				hdCode:    hdCode,
				cdgCode:   cdgCode,
				rawTitle:  cols[0],
				rawArtist: cols[1],
				rawGenre:  rawGenre,
				artistCol: artistCol,
				genreCol:  genreCol,
			}
			continue
		}

		if pending == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || partyTymePageNumRe.MatchString(trimmed) {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "partytyme.net") ||
			(strings.HasPrefix(lower, "new") && strings.Contains(lower, "song title")) ||
			strings.HasPrefix(lower, "date of") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent < 28 {
			continue
		}
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
	flushPending()
	return tracks, discMap
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
	var rawLeft, rawRight string
	rightStart := colSplit
	// 1. If a disc code finishes in the left column, split right after that disc code.
	foundLeftDisc := false
	for _, loc := range scDiscTokenRe.FindAllStringIndex(line, -1) {
		if loc[1] <= colSplit+8 {
			rawLeft = line[:loc[1]]
			rawRight = line[loc[1]:]
			rightStart = loc[1]
			foundLeftDisc = true
			break
		}
	}
	if !foundLeftDisc {
		if len(line) <= colSplit {
			rawLeft = line
		} else if strings.TrimSpace(line[:colSplit]) == "" {
			rawRight = line[colSplit:]
			rightStart = colSplit
		} else {
			bestSplit := -1
			for _, gap := range wideGapRe.FindAllStringIndex(line, -1) {
				if gap[1] >= colSplit-10 && gap[0] <= colSplit+12 {
					bestSplit = gap[1]
					break
				}
			}
			if bestSplit > 0 {
				rawLeft = line[:bestSplit]
				rawRight = line[bestSplit:]
				rightStart = bestSplit
			} else {
				rawLeft = line[:colSplit]
				rawRight = line[colSplit:]
				rightStart = colSplit
			}
		}
	}

	leftIndented := false
	if strings.TrimSpace(rawLeft) != "" {
		firstLeft := len(rawLeft) - len(strings.TrimLeft(rawLeft, " "))
		if firstLeft >= leftTitleCol && firstLeft >= 1 {
			leftIndented = true
		}
	}
	rightIndented := false
	if strings.TrimSpace(rawRight) != "" {
		firstNonSpace := rightStart + (len(rawRight) - len(strings.TrimLeft(rawRight, " ")))
		if firstNonSpace >= rightTitleCol {
			rightIndented = true
		}
	}
	return scColumnCell{text: rawLeft, indented: leftIndented}, scColumnCell{text: rawRight, indented: rightIndented}
}

// parseTwoColumnArtistHeaderText parses 2-column-per-page "Songs by Artist" PDFs
// (such as Sound Choice catalogs where section lines set the current artist
// and track lines contain "Title   SC####-##").
func parseTwoColumnArtistHeaderText(text, defaultProvider string) ([]CanonicalTrack, map[string]CanonicalTrack) {
	pages := strings.Split(text, "\f")
	var tracks []CanonicalTrack
	discMap := make(map[string]CanonicalTrack, 36000)

	var currentArtist string
	lastTrackIdx := -1
	lastDiscCode := ""
	lastRawTitle := ""
	lastWasArtist := false

	processCell := func(cell scColumnCell) {
		trimmed := strings.TrimSpace(cell.text)
		if isSoundChoiceBannerOrFooter(trimmed) {
			return
		}
		if m := scDiscSuffixRe.FindStringSubmatch(trimmed); m != nil {
			if currentArtist == "" {
				return
			}
			rawTitlePart := strings.TrimSpace(m[1])
			rawTitle := uninvertPDFTitle(rawTitlePart)
			discCode := NormalizeDiscCode(strings.ToUpper(strings.TrimSpace(m[2])))
			cleanArtist := UninvertArtist(currentArtist)
			cleanTitle := StripNoiseTags(rawTitle)
			if cleanArtist == "" || cleanTitle == "" {
				return
			}
			provider := ResolveProvider(discCode, defaultProvider)
			if provider == "" {
				provider = defaultProvider
			}
			tr := CanonicalTrack{
				ID:       GenerateCanonicalID(cleanArtist, cleanTitle),
				Artist:   cleanArtist,
				Title:    cleanTitle,
				DiscCode: discCode,
				Source:   provider,
			}
			discMap[discCode] = tr
			tracks = append(tracks, tr)
			lastTrackIdx = len(tracks) - 1
			lastDiscCode = discCode
			lastRawTitle = rawTitlePart
			lastWasArtist = false
			return
		}
		if len(trimmed) > 1 {
			artistHeader := strings.TrimSpace(wvocalSuffixRe.ReplaceAllString(trimmed, ""))
			if artistHeader == "" {
				// Standalone "(Wvocal)" continuation line: preserve currentArtist.
				return
			}
			if cell.indented && lastTrackIdx >= 0 && lastDiscCode != "" && len(lastRawTitle) >= 32 {
				// Indented line immediately following a long wrapped track line is a song title continuation.
				lastRawTitle = strings.TrimSpace(lastRawTitle + " " + trimmed)
				prev := tracks[lastTrackIdx]
				combinedTitle := StripNoiseTags(uninvertPDFTitle(lastRawTitle))
				prev.Title = combinedTitle
				prev.ID = GenerateCanonicalID(prev.Artist, combinedTitle)
				tracks[lastTrackIdx] = prev
				discMap[lastDiscCode] = prev
				return
			}
			if lastWasArtist && currentArtist != "" && !strings.EqualFold(currentArtist, artistHeader) {
				currentArtist = strings.TrimSpace(currentArtist + " " + artistHeader)
			} else {
				currentArtist = artistHeader
			}
			lastWasArtist = true
			lastTrackIdx = -1
			lastDiscCode = ""
			lastRawTitle = ""
		}
	}

	flushPage := func(leftCells, rightCells []scColumnCell) {
		lastWasArtist = false
		lastTrackIdx = -1
		lastDiscCode = ""
		lastRawTitle = ""
		for _, cell := range leftCells {
			processCell(cell)
		}
		lastWasArtist = false
		lastTrackIdx = -1
		lastDiscCode = ""
		lastRawTitle = ""
		for _, cell := range rightCells {
			processCell(cell)
		}
	}

	for _, page := range pages {
		var leftCells, rightCells []scColumnCell
		colSplit := 52
		leftTitleCol := 1
		rightTitleCol := 55
		for _, line := range strings.Split(page, "\n") {
			line = strings.TrimRight(line, "\r")
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine == "" {
				continue
			}
			lowerLine := strings.ToLower(trimmedLine)
			if strings.Contains(lowerLine, "songs by artist") && (len(leftCells) > 0 || len(rightCells) > 0) {
				flushPage(leftCells, rightCells)
				leftCells = leftCells[:0]
				rightCells = rightCells[:0]
				colSplit = 52
				leftTitleCol = 1
				rightTitleCol = 55
				continue
			}
			if scHeaderColsRe.MatchString(trimmedLine) {
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
				continue
			}
			if isSoundChoiceBannerOrFooter(trimmedLine) {
				continue
			}
			leftCell, rightCell := splitTwoColumnLine(line, colSplit, leftTitleCol, rightTitleCol)
			if strings.TrimSpace(leftCell.text) != "" {
				leftCells = append(leftCells, leftCell)
			}
			if strings.TrimSpace(rightCell.text) != "" {
				rightCells = append(rightCells, rightCell)
			}
		}
		flushPage(leftCells, rightCells)
	}
	return tracks, discMap
}
