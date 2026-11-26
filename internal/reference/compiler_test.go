package reference

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoveDiacritics(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Beyoncé", "Beyonce"},
		{"Motörhead", "Motorhead"},
		{"Björk", "Bjork"},
		{"Céline Dion", "Celine Dion"},
		{"Mötley Crüe", "Motley Crue"},
		{"Sigur Rós", "Sigur Ros"},
	}

	for _, tc := range cases {
		got := RemoveDiacritics(tc.input)
		if got != tc.expected {
			t.Errorf("RemoveDiacritics(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestUninvertArtist(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Beatles, The", "The Beatles"},
		{"Rolling Stones, The", "The Rolling Stones"},
		{"Who, The", "The Who"},
		{"Band, A", "A Band"},
		{"Presley, Elvis", "Elvis Presley"},
		{"Bowie, David", "David Bowie"},
		{"Queen", "Queen"},
		{"Earth, Wind & Fire", "Earth, Wind & Fire"}, // multi-comma, preserve
		{"Boyz II Men", "Boyz II Men"},
	}

	for _, tc := range cases {
		got := UninvertArtist(tc.input)
		if got != tc.expected {
			t.Errorf("UninvertArtist(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestStripNoiseTags(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Bohemian Rhapsody (Karaoke Version)", "Bohemian Rhapsody"},
		{"Sweet Caroline [SC8101]", "Sweet Caroline"},
		{"Hotel California (Instrumental)", "Hotel California"},
		{"Don't Stop Believin' (Remastered)", "Don't Stop Believin'"},
		{"Dreams (Karaoke)", "Dreams"},
		{"Dancing Queen [SF045-12]", "Dancing Queen"},
		{"Dreadlock Holiday (Without Backing Vocals)", "Dreadlock Holiday"},
		{"Dreadlock Holiday (Two Semitones Down)", "Dreadlock Holiday"},
		{"Livin' on a Prayer (With Backing Vocals)", "Livin' on a Prayer"},
		{"Hotel California (Acoustic)", "Hotel California (Acoustic)"}, // version qualifier preserved
		{"Livin' on a Prayer", "Livin' on a Prayer"},
	}

	for _, tc := range cases {
		got := StripNoiseTags(tc.input)
		if got != tc.expected {
			t.Errorf("StripNoiseTags(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeKeyConjunctions(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Simon & Garfunkel", "simon and garfunkel"},
		{"Florence + The Machine", "florence and the machine"},
		{"Rock & Roll All Nite", "rock and roll all nite"},
		{"P!nk", "p nk"},
	}

	for _, tc := range cases {
		got := NormalizeKey(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeKey(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeArtist(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Beatles, The", "the beatles"},
		{"Simon & Garfunkel", "simon and garfunkel"},
		{"Simon and Garfunkel", "simon and garfunkel"},
		{"Florence + The Machine", "florence and the machine"},
		{"P!nk", "pink"},
		{"Pink", "pink"},
		{"Ke$ha", "kesha"},
		{"Kesha", "kesha"},
		{"CCR", "creedence clearwater revival"},
		{"Creedence Clearwater Revival", "creedence clearwater revival"},
		{"ELO", "electric light orchestra"},
		{"Electric Light Orchestra", "electric light orchestra"},
		{"Guns N' Roses", "guns and roses"},
		{"Guns and Roses", "guns and roses"},
		{"Guns & Roses", "guns and roses"},
		{"Snoop Doggy Dogg", "snoop dogg"},
		{"Hall & Oates", "hall and oates"},
		{"Daryl Hall & John Oates", "hall and oates"},
	}

	for _, tc := range cases {
		got := NormalizeArtist(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeArtist(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestResolveProvider(t *testing.T) {
	cases := []struct {
		disc     string
		brand    string
		expected string
	}{
		{"SC8101-02", "", "Sound Choice"},
		{"sc8101-02", "", "Sound Choice"},
		{"SF045-12", "", "Sunfly"},
		{"CB5001-14", "", "Chartbuster"},
		{"SYB1001-05", "", "Sunfly Sybersound"},
		{"PHM0101-01", "", "Pop Hits Monthly"},
		{"MM1234-01", "", "Music Maestro"},
		{"KFN123", "", "KaraFun"},
		{"PT101", "", "Party Tyme"},
		{"ZM012-01", "", "Zoom Karaoke"},
		{"", "Sound Choice", "Sound Choice"},
		{"101", "Sunfly", "Sunfly"},
		{"CUSTOM-01", "", ""},
	}

	for _, tc := range cases {
		got := ResolveProvider(tc.disc, tc.brand)
		if got != tc.expected {
			t.Errorf("ResolveProvider(%q, %q) = %q, expected %q", tc.disc, tc.brand, got, tc.expected)
		}
	}
}

func TestCompositeKeyAndID(t *testing.T) {
	k1 := CompositeKey("Beatles, The", "Hey Jude (Karaoke Version)")
	k2 := CompositeKey("The Beatles", "Hey Jude")
	if k1 != k2 {
		t.Errorf("CompositeKey mismatch: %q != %q", k1, k2)
	}

	id1 := GenerateCanonicalID("Beatles, The", "Hey Jude (Karaoke Version)")
	id2 := GenerateCanonicalID("The Beatles", "Hey Jude")
	if id1 != id2 {
		t.Errorf("GenerateCanonicalID mismatch: %q != %q", id1, id2)
	}
	if !strings.HasPrefix(id1, "can_") {
		t.Errorf("Canonical ID must have 'can_' prefix: %q", id1)
	}

	// MusicBrainz artist alias equivalence
	ccrID1 := GenerateCanonicalID("CCR", "Bad Moon Rising")
	ccrID2 := GenerateCanonicalID("Creedence Clearwater Revival", "Bad Moon Rising")
	if ccrID1 != ccrID2 {
		t.Errorf("CCR alias mismatch: %q != %q", ccrID1, ccrID2)
	}

	pinkID1 := GenerateCanonicalID("P!nk", "Raise Your Glass")
	pinkID2 := GenerateCanonicalID("Pink", "Raise Your Glass")
	if pinkID1 != pinkID2 {
		t.Errorf("P!nk alias mismatch: %q != %q", pinkID1, pinkID2)
	}

	gnrID1 := GenerateCanonicalID("Guns N' Roses", "Sweet Child O' Mine")
	gnrID2 := GenerateCanonicalID("Guns and Roses", "Sweet Child O' Mine")
	if gnrID1 != gnrID2 {
		t.Errorf("Guns N Roses alias mismatch: %q != %q", gnrID1, gnrID2)
	}
}

func TestNormalizeDiscCode(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"sc8101-2", "SC8101-02"},
		{"SF045-1", "SF045-01"},
		{"cb5001-14", "CB5001-14"},
		{"SYB1001_5", "SYB1001-05"},
		{"OTHER", "OTHER"},
	}

	for _, tc := range cases {
		got := NormalizeDiscCode(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeDiscCode(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseKaraFunCSV(t *testing.T) {
	csvData := `Id;Title;Artist;Year;Duo;Explicit;Date Added;Styles;Languages
12320;Jolene;"Dolly Parton";1973;0;0;2011-05-31;Country;English
12543;Creep;Radiohead;1992;0;1;2008-03-07;Rock,Alternative;English
56442;Shallow;"A Star is Born (2018 film)";2018;1;0;2018-10-15;Soundtrack,Pop,Duet;English
12617;"Bohemian Rhapsody (Karaoke Version)";"Queen";1975;0;0;2008-07-21;Rock;English
`

	tracks, err := ParseKaraFunCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("ParseKaraFunCSV failed: %v", err)
	}

	if len(tracks) != 4 {
		t.Fatalf("expected 4 tracks, got %d", len(tracks))
	}

	// Check Queen track
	queen := tracks[3]
	if queen.Title != "Bohemian Rhapsody" {
		t.Errorf("expected noise stripped, got %q", queen.Title)
	}
	if queen.Artist != "Queen" {
		t.Errorf("expected Queen, got %q", queen.Artist)
	}
	if queen.Year != 1975 {
		t.Errorf("expected year 1975, got %d", queen.Year)
	}
	if queen.Genre != "Rock" {
		t.Errorf("expected genre Rock, got %q", queen.Genre)
	}

	// Check Shallow duet
	shallow := tracks[2]
	if !shallow.Duo {
		t.Errorf("expected Duo=true for Shallow")
	}

	// Check Creep explicit
	creep := tracks[1]
	if !creep.Explicit {
		t.Errorf("expected Explicit=true for Creep")
	}
}

func TestParseDiscMapCSV(t *testing.T) {
	discCSV := `disc,track,artist,title
SC8101,2,"Tucker, Tanya",Some Kind of Trouble
SC8101,4,"Jackson, Alan",Chattahoochee
SF045,12,ABBA,Dancing Queen
`

	discMap, tracks, err := ParseDiscMapCSV(strings.NewReader(discCSV))
	if err != nil {
		t.Fatalf("ParseDiscMapCSV failed: %v", err)
	}

	if len(discMap) != 3 {
		t.Fatalf("expected 3 mapped discs, got %d", len(discMap))
	}
	if len(tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(tracks))
	}

	scTrack, ok := discMap["SC8101-02"]
	if !ok {
		t.Fatalf("expected SC8101-02 in discMap")
	}
	if scTrack.Artist != "Tanya Tucker" {
		t.Errorf("expected un-inverted 'Tanya Tucker', got %q", scTrack.Artist)
	}
	if scTrack.Title != "Some Kind of Trouble" {
		t.Errorf("expected 'Some Kind of Trouble', got %q", scTrack.Title)
	}
}

func TestCompileDictionaryAndDiscMap(t *testing.T) {
	tracks := []CanonicalTrack{
		{ID: "can_1", Artist: "Queen", Title: "Bohemian Rhapsody", Genre: "Rock", Year: 1975},
		{ID: "can_2", Artist: "Dolly Parton", Title: "Jolene", Genre: "Country", Year: 1973},
		// Duplicate with more info:
		{ID: "can_1", Artist: "Queen", Title: "Bohemian Rhapsody (Karaoke Version)", Duo: false},
	}

	dictGz, err := CompileDictionary(tracks)
	if err != nil {
		t.Fatalf("CompileDictionary failed: %v", err)
	}
	if len(dictGz) == 0 {
		t.Fatal("empty dictionary gzip")
	}

	// Decompress and verify
	gzr, err := gzip.NewReader(bytes.NewReader(dictGz))
	if err != nil {
		t.Fatalf("gzip reader error: %v", err)
	}
	var dict DictionaryAsset
	if err := json.NewDecoder(gzr).Decode(&dict); err != nil {
		t.Fatalf("json decode error: %v", err)
	}
	if dict.TotalTracks != 2 {
		t.Errorf("expected 2 unique tracks after deduplication, got %d", dict.TotalTracks)
	}

	// Disc map compilation
	discMap := map[string]CanonicalTrack{
		"SC8101-02": tracks[0],
		"SF045-12":  tracks[1],
	}
	discGz, err := CompileDiscMap(discMap)
	if err != nil {
		t.Fatalf("CompileDiscMap failed: %v", err)
	}
	gzrDisc, err := gzip.NewReader(bytes.NewReader(discGz))
	if err != nil {
		t.Fatalf("gzip reader error: %v", err)
	}
	var discAsset DiscMapAsset
	if err := json.NewDecoder(gzrDisc).Decode(&discAsset); err != nil {
		t.Fatalf("json decode error: %v", err)
	}
	if discAsset.TotalDiscs != 2 {
		t.Errorf("expected 2 discs, got %d", discAsset.TotalDiscs)
	}
}

func TestEnrichDiscMap(t *testing.T) {
	discMap := map[string]CanonicalTrack{
		"SC8101-02": {
			Artist: "Tanya Tucker",
			Title:  "Some Kind of Trouble",
		},
		"KEEP-01": {
			Artist: "Existing Artist",
			Title:  "Existing Song",
			Genre:  "Existing Genre",
			Year:   2001,
		},
	}
	dictTracks := []CanonicalTrack{
		{
			Artist: "Tanya Tucker",
			Title:  "Some Kind of Trouble",
			Genre:  "Country,Rock",
		},
		{
			Artist: "Tanya Tucker",
			Title:  "Some Kind of Trouble",
			Year:   1992,
		},
		{
			Artist: "Existing Artist",
			Title:  "Existing Song",
			Genre:  "Replacement Genre",
			Year:   1999,
		},
	}
	EnrichDiscMap(discMap, dictTracks)
	enriched := discMap["SC8101-02"]
	if enriched.Genre != "Country,Rock" {
		t.Errorf("expected Country,Rock, got %q", enriched.Genre)
	}
	if enriched.Year != 1992 {
		t.Errorf("expected 1992, got %d", enriched.Year)
	}
	preserved := discMap["KEEP-01"]
	if preserved.Genre != "Existing Genre" || preserved.Year != 2001 {
		t.Errorf("expected existing disc metadata to be preserved, got genre %q and year %d", preserved.Genre, preserved.Year)
	}
}

func TestParseZoomCSV(t *testing.T) {
	csvData := `Artist,"Song title"
"101 Dalmatians","Cruella De Vil"
10cc,"Dreadlock Holiday (Without Backing Vocals)"
10cc,"Dreadlock Holiday (Two Semitones Down)"
"Presley, Elvis","Suspicious Minds (With Backing Vocals)"
`

	tracks, err := ParseZoomCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("ParseZoomCSV failed: %v", err)
	}

	if len(tracks) != 4 {
		t.Fatalf("expected 4 tracks, got %d", len(tracks))
	}

	// Verify un-inverting and noise stripping
	if tracks[1].Title != "Dreadlock Holiday" {
		t.Errorf("expected noise stripped, got %q", tracks[1].Title)
	}
	if tracks[2].Title != "Dreadlock Holiday" {
		t.Errorf("expected semitones stripped, got %q", tracks[2].Title)
	}
	if tracks[3].Artist != "Elvis Presley" {
		t.Errorf("expected 'Elvis Presley', got %q", tracks[3].Artist)
	}
	if tracks[3].Title != "Suspicious Minds" {
		t.Errorf("expected 'Suspicious Minds', got %q", tracks[3].Title)
	}
	if tracks[3].Source != "Zoom Karaoke" {
		t.Errorf("expected source Zoom Karaoke, got %q", tracks[3].Source)
	}
}

func TestParseSongbookText(t *testing.T) {
	textData := `
Catalogue of Backing Tracks
Page 1 of 50

10cc                  Dreadlock Holiday
ABBA                  Dancing Queen
SC8101-02             Tanya Tucker          Some Kind of Trouble
Elvis Presley         Suspicious Minds      SF045-05
`

	tracks, discMap, err := ParseSongbookText(textData, "Custom Provider")
	if err != nil {
		t.Fatalf("ParseSongbookText failed: %v", err)
	}

	if len(tracks) != 4 {
		t.Fatalf("expected 4 tracks, got %d", len(tracks))
	}

	// 2-column track inherits default provider
	if tracks[0].Artist != "10cc" || tracks[0].Title != "Dreadlock Holiday" {
		t.Errorf("unexpected track 0: %+v", tracks[0])
	}
	if tracks[0].Source != "Custom Provider" {
		t.Errorf("expected default provider, got %q", tracks[0].Source)
	}

	// 3-column track with disc first resolves Sound Choice
	scTrack, ok := discMap["SC8101-02"]
	if !ok {
		t.Fatalf("expected SC8101-02 in discMap")
	}
	if scTrack.Source != "Sound Choice" {
		t.Errorf("expected Sound Choice provider, got %q", scTrack.Source)
	}

	// 3-column track with disc last resolves Sunfly
	sfTrack, ok := discMap["SF045-05"]
	if !ok {
		t.Fatalf("expected SF045-05 in discMap")
	}
	if sfTrack.Source != "Sunfly" {
		t.Errorf("expected Sunfly provider, got %q", sfTrack.Source)
	}
}

func TestParsePDFSongbook_MissingFile(t *testing.T) {
	_, _, err := ParsePDFSongbook("/nonexistent/file.pdf", "")
	if err == nil {
		t.Errorf("expected error for nonexistent file")
	}
}

func TestParseSongbookText_ExpandedDiscCodesAndProviders(t *testing.T) {
	sample := strings.Join([]string{
		"CBEP457-3-08   Ed Sheeran   The A Team",
		"MM6380B-01     Queen        Radio Ga Ga",
		"LG042-05       Journey      Don't Stop Believin'",
		"KV12345        Adele        Rolling in the Deep",
	}, "\n")

	tracks, discMap, err := ParseSongbookText(sample, "")
	if err != nil {
		t.Fatalf("ParseSongbookText: %v", err)
	}
	if len(tracks) != 4 || len(discMap) != 4 {
		t.Fatalf("got %d tracks / %d discs; want 4 / 4", len(tracks), len(discMap))
	}
	want := map[string]string{
		"CBEP457-3-08": "Chartbuster",
		"MM6380B-01":   "Music Maestro",
		"LG042-05":     "Legends",
		"KV12345":      "Karaoke Version",
	}
	for code, wantSource := range want {
		tr, ok := discMap[code]
		if !ok {
			t.Fatalf("missing disc code %q in discMap: %+v", code, discMap)
		}
		if tr.Source != wantSource {
			t.Errorf("disc %q source = %q; want %q", code, tr.Source, wantSource)
		}
	}
}

func TestParseSongbookText_PartyTymeAndSoundChoiceLayouts(t *testing.T) {
	ptSample := strings.Join([]string{
		"                                   Partytyme.net song catalog 11/23/2026",
		"New    Date of       HD       CDG-MP3G   SONG TITLE                   ARTIST                            GENRE         LANG",
		"NEW   11/19/2026   PH05630     PY05630   Mess                         Jordan Davis                    Country Male    English",
		"NEW   11/12/2026   PH05625     PY05625   Us Again                     Gavin Adcock ft. Treaty Oak     Country Male    English",
		"                                                                      Revival",
		"                PH11085   PY11085   Spanish Harlem                  Neil Diamond       Adult      English",
		"                                                                                       Contemporary",
		"                PH88086                  Multiply                     Becky Hill                      Pop Female      English",
	}, "\n")
	ptTracks, ptDiscs, err := ParseSongbookText(ptSample, "Party Tyme")
	if err != nil {
		t.Fatalf("ParseSongbookText Party Tyme: %v", err)
	}
	if len(ptTracks) != 4 || len(ptDiscs) != 7 {
		t.Fatalf("Party Tyme got %d tracks / %d discs; want 4 / 7", len(ptTracks), len(ptDiscs))
	}
	if ptDiscs["PH05630"].Artist != "Jordan Davis" || ptDiscs["PY05630"].Title != "Mess" || ptDiscs["PH05630"].Genre != "Country" {
		t.Fatalf("unexpected Party Tyme disc entry: %+v", ptDiscs["PH05630"])
	}
	if ptDiscs["PH05625"].Artist != "Gavin Adcock ft. Treaty Oak Revival" || ptDiscs["PH05625"].Title != "Us Again" {
		t.Fatalf("expected wrapped Party Tyme artist continuation, got: %+v", ptDiscs["PH05625"])
	}
	if ptDiscs["PH11085"].Artist != "Neil Diamond" || ptDiscs["PH11085"].Genre != "Adult Contemporary" {
		t.Fatalf("expected wrapped Party Tyme genre continuation without corrupting artist, got: %+v", ptDiscs["PH11085"])
	}

	scSample := strings.Join([]string{
		"Sound Choice Karaoke                             Songs by Artist                    Karaoke Shack Song Books",
		"  Title                                               DiscID     Title                                       DiscID",
		"Aaron Tippin                                                    Kiss Me, Kiss Your Baby                  SC8707-07",
		"  My Blue Angel                                    SC2338-05    ABBA",
		"  Door, A                                          SC3018-13     Dancing Queen                            SC1703-06",
		"\f",
		"Sound Choice Karaoke                             Songs by Artist                    Karaoke Shack Song Books",
		" Title                                                DiscID     Title                                       DiscID",
		" Name Of The Game, The                             SC1722-07    Bill Engvall & Tracy Byrd",
		" Take A Chance On Me                               SC1703-02    (Wvocal)",
		"Notorious Cherry Bombs, The                                      Now That's Awesome!                      SC3223-14",
		" It's Hard To Kiss The Lips At Night That Chew     SC8884-08",
		" Your Ass Out All Day Long",
		" Christmas Song, The (Chestnuts Roasting On An     SCDEMO-08",
		" Open Fire)",
	}, "\n")
	scTracks, scDiscs, err := ParseSongbookText(scSample, "Sound Choice")
	if err != nil {
		t.Fatalf("ParseSongbookText Sound Choice: %v", err)
	}
	if len(scTracks) != 9 || len(scDiscs) != 9 {
		t.Fatalf("Sound Choice got %d tracks / %d discs; want 9 / 9", len(scTracks), len(scDiscs))
	}
	if scDiscs["SC3018-13"].Artist != "Aaron Tippin" || scDiscs["SC3018-13"].Title != "A Door" {
		t.Fatalf("unexpected Sound Choice left entry: %+v", scDiscs["SC3018-13"])
	}
	if scDiscs["SC8707-07"].Artist != "Aaron Tippin" || scDiscs["SC8707-07"].Title != "Kiss Me, Kiss Your Baby" {
		t.Fatalf("expected top-right continuation track to inherit Aaron Tippin from bottom-left: %+v", scDiscs["SC8707-07"])
	}
	if scDiscs["SC1722-07"].Artist != "ABBA" || scDiscs["SC1722-07"].Title != "The Name Of The Game" {
		t.Fatalf("expected next-page top-left track to inherit ABBA from previous page bottom-right: %+v", scDiscs["SC1722-07"])
	}
	if scDiscs["SC1703-02"].Artist != "ABBA" || scDiscs["SC1703-02"].Title != "Take A Chance On Me" {
		t.Fatalf("expected next-page right track to keep ABBA: %+v", scDiscs["SC1703-02"])
	}
	if scDiscs["SC8884-08"].Artist != "The Notorious Cherry Bombs" || scDiscs["SC8884-08"].Title != "It's Hard To Kiss The Lips At Night That Chew Your Ass Out All Day Long" {
		t.Fatalf("expected wrapped Sound Choice title continuation on SC8884-08, got: %+v", scDiscs["SC8884-08"])
	}
	if scDiscs["SCDEMO-08"].Artist != "The Notorious Cherry Bombs" || scDiscs["SCDEMO-08"].Title != "The Christmas Song (Chestnuts Roasting On An Open Fire)" {
		t.Fatalf("expected 1-space wrapped title continuation + un-inversion on SCDEMO-08, got: %+v", scDiscs["SCDEMO-08"])
	}
	if scDiscs["SC3223-14"].Artist != "Bill Engvall & Tracy Byrd" || scDiscs["SC3223-14"].Title != "Now That's Awesome!" {
		t.Fatalf("expected standalone (Wvocal) line to preserve artist on SC3223-14, got: %+v", scDiscs["SC3223-14"])
	}
}
