import test from 'node:test'
import assert from 'node:assert/strict'
import {
    detectDelimiter,
    parseDelimitedText,
    detectSoftwareProfile,
    guessColumnMapping,
    rowsToSongs,
    SOFTWARE_PROFILES,
} from './csvParser.js'

test('detectDelimiter detects tabs for TSV', () => {
    const tsv = "Artist\tTitle\tDiskID\nQueen\tBohemian Rhapsody\tSC8100\nJourney\tDon't Stop Believin'\tCB100"
    assert.equal(detectDelimiter(tsv), '\t')
})

test('detectDelimiter detects commas for CSV', () => {
    const csv = 'Artist,Title,Disc ID,Tags\n"Queen","Bohemian Rhapsody","SC8100","Rock"\n"Journey","Don\'t Stop Believin\'","CB100","Classic"'
    assert.equal(detectDelimiter(csv), ',')
})

test('parseDelimitedText correctly handles quotes and commas inside quotes', () => {
    const csv = 'Artist,Title,Notes\n"Queen, The Band","Bohemian, Part 1","Special, rare track"\n"Simple","Song",""'
    const rows = parseDelimitedText(csv, ',')
    assert.equal(rows.length, 3)
    assert.deepEqual(rows[1], ['Queen, The Band', 'Bohemian, Part 1', 'Special, rare track'])
})

test('detectSoftwareProfile identifies PCDJ Karaoki TSV export', () => {
    const headers = ['Artist', 'Title', 'DiskID', 'Genre', 'Filename']
    const profile = detectSoftwareProfile(headers, '\t')
    assert.equal(profile.id, 'pcdj_karaoki')

    const mapping = profile.columnMapping(headers)
    assert.equal(mapping.artistIndex, 0)
    assert.equal(mapping.titleIndex, 1)
    assert.equal(mapping.discIndex, 2)
    assert.equal(mapping.genreIndex, 3)
    assert.equal(mapping.locationIndex, 4)
})

test('detectSoftwareProfile identifies SongbookDB export', () => {
    const headers = ['Artist', 'Song', 'Disc ID', 'Tags', 'Date Added']
    const profile = detectSoftwareProfile(headers, ',')
    assert.equal(profile.id, 'songbookdb')

    const mapping = profile.columnMapping(headers)
    assert.equal(mapping.artistIndex, 0)
    assert.equal(mapping.titleIndex, 1)
    assert.equal(mapping.discIndex, 2)
    assert.equal(mapping.genreIndex, 3)
})

test('detectSoftwareProfile identifies MTU Hoster export and maps Brand to sourceIndex', () => {
    const headers = ['BookID', 'Artist', 'Title', 'Brand']
    const profile = detectSoftwareProfile(headers, ',')
    assert.equal(profile.id, 'mtu_hoster')

    const mapping = profile.columnMapping(headers)
    assert.equal(mapping.discIndex, 0)
    assert.equal(mapping.artistIndex, 1)
    assert.equal(mapping.titleIndex, 2)
    assert.equal(mapping.sourceIndex, 3)
})

test('rowsToSongs maps explicit Source/Publisher and Video URL columns without colliding with Location', () => {
    const csv = [
        'Artist,Title,Disc,Source,Genre,Video URL,Location',
        'Queen,Bohemian Rhapsody,SC8101-02,Sound Choice,Rock,https://youtu.be/abc,C:\\karaoke\\queen.zip',
        'Queen,Bohemian Rhapsody,,KaraFun,Rock,,',
        'Queen,Bohemian Rhapsody,,Sunfly,Rock,,',
    ].join('\n')
    const rows = parseDelimitedText(csv, ',')
    const mapping = guessColumnMapping(rows[0])
    assert.equal(mapping.discIndex, 2)
    assert.equal(mapping.sourceIndex, 3)
    assert.equal(mapping.videoUrlIndex, 5)
    assert.equal(mapping.locationIndex, 6)

    const songs = rowsToSongs(rows, mapping, true)
    assert.equal(songs.length, 3)
    assert.equal(songs[0].disc, 'SC8101-02')
    assert.equal(songs[0].source, 'Sound Choice')
    assert.equal(songs[0].video_url, 'https://youtu.be/abc')
    assert.equal(songs[0].location, 'C:\\karaoke\\queen.zip')
    assert.equal(songs[1].source, 'KaraFun')
    assert.equal(songs[2].source, 'Sunfly')
    assert.notEqual(songs[1].fingerprint, songs[2].fingerprint)
})

test('guessColumnMapping keeps private file path columns out of public source and video_url', () => {
    const csv = [
        'Artist,Title,Source Path,Video Path,Video File',
        'Queen,Bohemian Rhapsody,C:\\Karaoke\\SC8101.zip,C:\\Video\\queen.mp4,C:\\Video\\queen2.mp4',
    ].join('\n')
    const rows = parseDelimitedText(csv, ',')
    const mapping = guessColumnMapping(rows[0])
    assert.equal(mapping.locationIndex, 2)
    assert.equal(mapping.sourceIndex, -1)
    assert.equal(mapping.videoUrlIndex, -1)

    const songs = rowsToSongs(rows, mapping, true)
    assert.equal(songs[0].source, '')
    assert.equal(songs[0].video_url, '')
    assert.equal(songs[0].location, 'C:\\Karaoke\\SC8101.zip')
})

test('detectSoftwareProfile maps Brand to sourceIndex even when BookID is absent', () => {
    const headers = ['Artist', 'Title', 'Brand']
    const profile = detectSoftwareProfile(headers, ',')
    const mapping = profile.columnMapping(headers)
    assert.equal(mapping.discIndex, -1)
    assert.equal(mapping.sourceIndex, 2)
})

test('rowsToSongs converts parsed TSV rows into standardized MicUp song models', () => {
    const tsv = "Artist\tTitle\tDiskID\tGenre\tFilename\nQueen\tBohemian Rhapsody\tSC8100\tRock\tC:\\karaoke\\queen.zip\nJourney\tDon't Stop Believin'\tCB100\tClassic\tC:\\karaoke\\journey.zip"
    const rows = parseDelimitedText(tsv, '\t')
    const mapping = guessColumnMapping(rows[0])
    const songs = rowsToSongs(rows, mapping, true)

    assert.equal(songs.length, 2)
    assert.equal(songs[0].artist, 'Queen')
    assert.equal(songs[0].title, 'Bohemian Rhapsody')
    assert.equal(songs[0].disc, 'SC8100')
    assert.equal(songs[0].genre, 'Rock')
    assert.equal(songs[0].location, 'C:\\karaoke\\queen.zip')
    assert.equal(songs[0].is_available, true)
    assert.ok(songs[0].fingerprint.startsWith('fp-'))

    assert.equal(songs[1].artist, 'Journey')
    assert.equal(songs[1].title, "Don't Stop Believin'")
})

test('rowsToSongs handles duplicate rows by appending version counter to fingerprint', () => {
    const csv = "Artist,Title,Disc\nQueen,Bohemian Rhapsody,SC8100\nQueen,Bohemian Rhapsody,SC8100"
    const rows = parseDelimitedText(csv, ',')
    const mapping = guessColumnMapping(rows[0])
    const songs = rowsToSongs(rows, mapping, true)

    assert.equal(songs.length, 2)
    assert.notEqual(songs[0].fingerprint, songs[1].fingerprint)
    assert.ok(songs[1].fingerprint.endsWith('-v2'))
})
