package reference

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestParseSongbookText_SunflyTwoColumn(t *testing.T) {
	sample := "\f                                                              Sunfly Karaoke - English - A-Z by Artist\n\n" +
		"         Made Famous By                                 Song Title                         Made Famous By                                  Song Title\n\n" +
		"10cc                                     Dreadlock Holiday                         10cc                                     I'm Not in Love\n" +
		"2nd Baptist Church Feat. Lauren Cam...   Rise Up (Dance Your Shoes Off)            The 411                                  Dumb\n" +
		"Unicode Truncated Artist\u2026                Some Song                                 O'Donnell, Daniel                        Give Me A Little Love\n" +
		"Stylistics, The                          Na Na Is The Saddest Song                 Page 12 of 450                           23/11/2025\n"
	tracks, discs, err := ParseSongbookText(sample, "Sunfly")
	if err != nil {
		t.Fatalf("ParseSongbookText failed: %v", err)
	}
	if len(discs) != 0 {
		t.Fatalf("expected 0 discs for track-only Sunfly PDF, got %d", len(discs))
	}
	// Truncated "2nd Baptist Church Feat. Lauren Cam..." must be skipped -> 5 clean tracks
	if len(tracks) != 5 {
		t.Fatalf("expected 5 clean tracks, got %d: %+v", len(tracks), tracks)
	}
	seen := make(map[string]CanonicalTrack)
	for _, tr := range tracks {
		if strings.HasSuffix(tr.Artist, "...") || strings.HasSuffix(tr.Title, "...") {
			t.Fatalf("truncated entry leaked into canonical tracks: %+v", tr)
		}
		seen[CompositeKey(tr.Artist, tr.Title)] = tr
	}
	if _, ok := seen[CompositeKey("The Stylistics", "Na Na Is The Saddest Song")]; !ok {
		t.Fatalf("expected uninverted Stylistics track, got keys: %v", seen)
	}
	if _, ok := seen[CompositeKey("Daniel O'Donnell", "Give Me A Little Love")]; !ok {
		t.Fatalf("expected uninverted Daniel O'Donnell track, got keys: %v", seen)
	}
}

func TestParseMrEntertainerXLSX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	writePart := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writePart("xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>code</t></si>
  <si><t>song title</t></si>
  <si><t>artist</t></si>
  <si><t>HD02812</t></si>
  <si><t>Dreadlock Holiday</t></si>
  <si><t>10cc</t></si>
  <si><t>HD03021</t></si>
  <si><r><t>I'm Not </t></r><r><t>in Love</t></r></si>
</sst>`)

	writePart("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
      <c r="C1" t="s"><v>2</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>3</v></c>
      <c r="B2" t="s"><v>4</v></c>
      <c r="C2" t="s"><v>5</v></c>
    </row>
    <row r="3">
      <c r="A3" t="s"><v>6</v></c>
      <c r="B3" t="s"><v>7</v></c>
      <c r="C3" t="inlineStr"><is><r><t>10</t></r><r><t>cc</t></r></is></c>
      <c r="AAAAAAAA3" t="inlineStr"><is><t>ignored</t></is></c>
    </row>
    <row r="4">
      <c r="A4" t="inlineStr"><is><t>SC8101-01</t></is></c>
      <c r="B4" t="inlineStr"><is><t>Collision Song</t></is></c>
      <c r="C4" t="inlineStr"><is><t>Collision Artist</t></is></c>
    </row>
  </sheetData>
</worksheet>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	dm, tracks, err := ParseMrEntertainerXLSX(buf.Bytes(), "Mr Entertainer")
	if err != nil {
		t.Fatalf("ParseMrEntertainerXLSX failed: %v", err)
	}
	if len(tracks) != 3 || len(dm) != 2 {
		t.Fatalf("expected 3 tracks and 2 non-colliding discs, got %d tracks / %d discs", len(tracks), len(dm))
	}
	if _, collided := dm["SC8101-01"]; collided {
		t.Fatalf("expected Sound Choice prefix SC8101-01 to be excluded from Mr Entertainer discMap")
	}
	if inferContentType("reference/v1/sources/mr-entertainer.xlsx") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("expected xlsx MIME type from inferContentType")
	}
	tr, ok := dm["HD03021"]
	if !ok {
		t.Fatalf("expected HD03021 in discMap, got %+v", dm)
	}
	if tr.Artist != "10cc" || tr.Title != "I'm Not in Love" || tr.Source != "Mr Entertainer" {
		t.Fatalf("unexpected rich-text track: %+v", tr)
	}
}

func TestParseBillboardCSV_DeduplicatesAndKeepsEarliestYear(t *testing.T) {
	hot100 := "\uFEFF\"chart_week\",\"current_week\",\"title\",\"performer\",\"last_week\",\"peak_pos\",\"wks_on_chart\",\"wks_at_no1\"\n" +
		"2022-01-01,1,All I Want For Christmas Is You,Mariah Carey,1,1,50,NA\n" +
		"12/24/1994,12,All I Want For Christmas Is You,Mariah Carey,15,12,3,NA\n" +
		"2019-06-01,13,EARFQUAKE,\"Tyler, The Creator\",15,13,1,NA\n"
	tracks, err := ParseBillboardCSV(strings.NewReader(hot100), "", "Billboard Hot 100")
	if err != nil {
		t.Fatalf("ParseBillboardCSV hot100 failed: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 unique tracks, got %d", len(tracks))
	}
	var mariah, tyler CanonicalTrack
	for _, tr := range tracks {
		if tr.Artist == "Mariah Carey" {
			mariah = tr
		}
		if tr.Title == "EARFQUAKE" {
			tyler = tr
		}
	}
	if mariah.Year != 1994 {
		t.Fatalf("expected earliest chart year 1994 for Mariah Carey (from MM/DD/YYYY), got %d", mariah.Year)
	}
	if tyler.Artist != "Tyler, The Creator" {
		t.Fatalf("expected uncorrupted comma artist 'Tyler, The Creator', got %q", tyler.Artist)
	}

	country := `,ranking,artist,title,last_week_rank,peak_position,weeks_on_chart,chart_date
1958-10-20_1,1,Ray Price,City Lights,0,1,1,1958-10-20
1959-01-05_5,5,Ray Price,City Lights,4,1,12,1959-01-05
`
	cTracks, err := ParseBillboardCSV(strings.NewReader(country), "Country", "Billboard Country")
	if err != nil {
		t.Fatalf("ParseBillboardCSV country failed: %v", err)
	}
	if len(cTracks) != 1 || cTracks[0].Genre != "Country" || cTracks[0].Year != 1958 {
		t.Fatalf("unexpected country track: %+v", cTracks)
	}

	var csvOut bytes.Buffer
	if err := WriteTracksCSV(&csvOut, cTracks); err != nil {
		t.Fatalf("WriteTracksCSV failed: %v", err)
	}
	if !strings.Contains(csvOut.String(), "Ray Price,City Lights,Country,1958,Billboard Country") {
		t.Fatalf("unexpected WriteTracksCSV output:\n%s", csvOut.String())
	}
}

func TestSourcesJSONValid(t *testing.T) {
	cfg, err := LoadSourcesConfig("sources.json")
	if err != nil {
		t.Fatalf("LoadSourcesConfig(sources.json): %v", err)
	}
	if cfg.Version != 2 {
		t.Fatalf("expected version 2 in sources.json, got %d", cfg.Version)
	}
	sources := cfg.AllSources()
	if len(sources) != 10 {
		t.Fatalf("expected 10 unified sources in sources.json, got %d", len(sources))
	}
	// Observed live production catalog counts from reference/v1/sources/manifest.json:
	liveCounts := map[string][2]int{
		"karafun":           {90470, 0},
		"zoom":              {15589, 0},
		"party-tyme":        {44971, 59011},
		"sound-choice":      {37044, 37009},
		"mr-entertainer":    {8158, 8158},
		"sunfly":            {17386, 0},
		"billboard-hot-100": {32795, 0},
		"billboard-country": {12436, 0},
		"billboard-rb":      {14361, 0},
		"historical-discs":  {80, 80},
	}
	for _, s := range sources {
		if s.ID == "" || s.Name == "" || s.Provider == "" || s.Format == "" {
			t.Fatalf("source missing required fields: %+v", s)
		}
		if s.Lifecycle != LifecycleImmutable && s.Lifecycle != LifecycleRolling {
			t.Fatalf("source %q has invalid lifecycle %q", s.ID, s.Lifecycle)
		}
		if !s.Required {
			t.Fatalf("source %q expected required=true in v2 manifest", s.ID)
		}
		counts, ok := liveCounts[s.ID]
		if !ok {
			t.Fatalf("unexpected source ID %q not in liveCounts table", s.ID)
		}
		if s.MinTracks <= 0 || s.MinTracks > counts[0] {
			t.Fatalf("source %q min_tracks=%d must be >0 and <= live count %d", s.ID, s.MinTracks, counts[0])
		}
		if s.MinDiscs > counts[1] {
			t.Fatalf("source %q min_discs=%d exceeds live disc count %d", s.ID, s.MinDiscs, counts[1])
		}
	}
}
