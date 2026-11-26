import test from 'node:test'
import assert from 'node:assert/strict'
import { extractDiscFromRawStem, parseFolderFileEntry, parseFolderFilesLocal } from './folderParser.js'

test('extractDiscFromRawStem preserves underscore disc codes before delimiter splitting', () => {
    assert.deepEqual(
        extractDiscFromRawStem('SC8101_02 - Queen - Bohemian Rhapsody'),
        { disc: 'SC8101_02', cleaned: 'Queen - Bohemian Rhapsody' }
    )
    assert.deepEqual(
        extractDiscFromRawStem('Queen - Bohemian Rhapsody [SF045-12]'),
        { disc: 'SF045-12', cleaned: 'Queen - Bohemian Rhapsody' }
    )
    assert.deepEqual(
        extractDiscFromRawStem('Queen - Bohemian Rhapsody - CB5001-01'),
        { disc: 'CB5001-01', cleaned: 'Queen - Bohemian Rhapsody' }
    )
})

test('parseFolderFileEntry extracts disc/source, preserves extra title segments and meaningful parentheticals', () => {
    const s1 = parseFolderFileEntry({ name: 'SC8101_02 - Queen - Bohemian Rhapsody (Acoustic) - Live.zip', size: 123 }, 'artist-title')
    assert.equal(s1.disc, 'SC8101_02')
    assert.equal(s1.source, 'Sound Choice')
    assert.equal(s1.artist, 'Queen')
    assert.equal(s1.title, 'Bohemian Rhapsody (Acoustic) - Live')
    assert.equal(s1.fingerprint, 'SC8101_02 - Queen - Bohemian Rhapsody (Acoustic) - Live.zip-123')

    const s2 = parseFolderFileEntry({ name: 'Bohemian Rhapsody - Live - Queen.mp3', size: 456 }, 'title-artist')
    assert.equal(s2.artist, 'Queen')
    assert.equal(s2.title, 'Bohemian Rhapsody - Live')

    const s3 = parseFolderFileEntry({ name: 'CUSTOM-01 - Queen - Bohemian Rhapsody - Live.cdg', size: 789 }, 'disc-artist-title')
    assert.equal(s3.disc, 'CUSTOM-01')
    assert.equal(s3.source, '')
    assert.equal(s3.artist, 'Queen')
    assert.equal(s3.title, 'Bohemian Rhapsody - Live')
})

test('parseFolderFilesLocal parses 65,000 entries locally in order', () => {
    const files = Array.from({ length: 65_000 }, (_, i) => ({
        name: `SC8101-02 - Artist ${i} - Song ${i}.zip`,
        path: `Karaoke/SC8101-02 - Artist ${i} - Song ${i}.zip`,
        size: i,
    }))
    const parsed = parseFolderFilesLocal(files, 'artist-title')
    assert.equal(parsed.length, 65_000)
    assert.equal(parsed[0].disc, 'SC8101-02')
    assert.equal(parsed[0].source, 'Sound Choice')
    assert.equal(parsed[64_999].artist, 'Artist 64999')
    assert.equal(parsed[64_999].title, 'Song 64999')
})

test('parseFolderFileEntry handles multipart/letter-suffixed disc codes, download clutter, and 2-part disc-artist-title fallback', () => {
    const multipart = parseFolderFileEntry({ name: 'CBEP457-3-08 - Ed Sheeran - The A Team.zip', size: 10 }, 'disc-artist-title')
    assert.equal(multipart.disc, 'CBEP457-3-08')
    assert.equal(multipart.source, 'Chartbuster')
    assert.equal(multipart.artist, 'Ed Sheeran')
    assert.equal(multipart.title, 'The A Team')

    const lettered = parseFolderFileEntry({ name: 'MM6380B-01 - Queen - Radio Ga Ga.zip', size: 11 }, 'artist-title')
    assert.equal(lettered.disc, 'MM6380B-01')
    assert.equal(lettered.source, 'Music Maestro')
    assert.equal(lettered.artist, 'Queen')
    assert.equal(lettered.title, 'Radio Ga Ga')

    const cluttered = parseFolderFileEntry({ name: 'George Ezra - Blame It on Me (MP3+CDG - Karaoke) - 443579.zip', size: 12 }, 'artist-title')
    assert.equal(cluttered.artist, 'George Ezra')
    assert.equal(cluttered.title, 'Blame It on Me')

    const twoPartNoDisc = parseFolderFileEntry({ name: 'Bon Jovi - (You Want To) Make A Memory.zip', size: 13 }, 'disc-artist-title')
    assert.equal(twoPartNoDisc.disc, '')
    assert.equal(twoPartNoDisc.artist, 'Bon Jovi')
    assert.equal(twoPartNoDisc.title, '(You Want To) Make A Memory')
})

test('parseFolderFilesLocal auto mode resolves mixed layouts per file independently using reference lookup', () => {
    const knownTracks = new Set([
        'queen:::bohemian rhapsody',
        'a perfect circle:::imagine',
        'journey:::don t stop believin',
    ])
    const knownArtists = new Set(['queen', 'a perfect circle', 'journey', 'bon jovi'])
    const lookup = {
        hasTrack: (key) => knownTracks.has(key),
        hasArtist: (artist) => knownArtists.has(artist),
    }

    const mixedFiles = [
        { name: 'SC8101-02 - Queen - Bohemian Rhapsody.zip', size: 1 },
        { name: 'Imagine - A Perfect Circle.mp3', size: 2 },
        { name: 'D01 - Journey - Don\'t Stop Believin.cdg', size: 3 },
        { name: 'Deep Cut Song - Bon Jovi.zip', size: 4 }, // known artist, unknown song -> flips to Bon Jovi
        { name: 'Local Band - Original Track.mp3', size: 5 }, // unknown both -> defaults artist-title
    ]

    const parsed = parseFolderFilesLocal(mixedFiles, 'auto', lookup)
    assert.equal(parsed[0].disc, 'SC8101-02')
    assert.equal(parsed[0].artist, 'Queen')
    assert.equal(parsed[0].title, 'Bohemian Rhapsody')

    assert.equal(parsed[1].artist, 'A Perfect Circle')
    assert.equal(parsed[1].title, 'Imagine')

    assert.equal(parsed[2].disc, 'D01')
    assert.equal(parsed[2].artist, 'Journey')
    assert.equal(parsed[2].title, "Don't Stop Believin")

    assert.equal(parsed[3].artist, 'Bon Jovi')
    assert.equal(parsed[3].title, 'Deep Cut Song')

    assert.equal(parsed[4].artist, 'Local Band')
    assert.equal(parsed[4].title, 'Original Track')
})

test('parseFolderFileEntry preserves numeric song titles, unknown digit-containing artists, and numeric artists after SC disc codes', () => {
    const numericTitle = parseFolderFileEntry({ name: 'Tommy Tutone - 8675309.mp3', size: 1 }, 'auto')
    assert.equal(numericTitle.artist, 'Tommy Tutone')
    assert.equal(numericTitle.title, '8675309')

    const unknownDigitArtist = parseFolderFileEntry({ name: 'Local 5 - Original Song - Demo.mp3', size: 2 }, 'auto')
    assert.equal(unknownDigitArtist.disc, '')
    assert.equal(unknownDigitArtist.artist, 'Local 5')
    assert.equal(unknownDigitArtist.title, 'Original Song - Demo')

    const numericArtistAfterDisc = parseFolderFileEntry({ name: 'SC8101-02-311-Amber.zip', size: 3 }, 'auto')
    assert.equal(numericArtistAfterDisc.disc, 'SC8101-02')
    assert.equal(numericArtistAfterDisc.artist, '311')
    assert.equal(numericArtistAfterDisc.title, 'Amber')

    const numericTitleWithDisc = parseFolderFileEntry({ name: 'SC8101-02 - Tommy Tutone - 8675309.mp3', size: 4 }, 'auto')
    assert.equal(numericTitleWithDisc.disc, 'SC8101-02')
    assert.equal(numericTitleWithDisc.artist, 'Tommy Tutone')
    assert.equal(numericTitleWithDisc.title, '8675309')

    const numericTitleWithClutter = parseFolderFileEntry({ name: 'Tommy Tutone - 8675309 (MP3+CDG - Karaoke).zip', size: 5 }, 'auto')
    assert.equal(numericTitleWithClutter.artist, 'Tommy Tutone')
    assert.equal(numericTitleWithClutter.title, '8675309')

    const customDiscNumericTitleClutter = parseFolderFileEntry({ name: 'D01 - Tommy Tutone - 8675309 (MP3+CDG - Karaoke).zip', size: 6 }, 'disc-artist-title')
    assert.equal(customDiscNumericTitleClutter.disc, 'D01')
    assert.equal(customDiscNumericTitleClutter.artist, 'Tommy Tutone')
    assert.equal(customDiscNumericTitleClutter.title, '8675309')

    const digitBandDownloadId = parseFolderFileEntry({ name: 'Maroon 5 - Sugar - 443579.zip', size: 7 }, 'artist-title')
    assert.equal(digitBandDownloadId.artist, 'Maroon 5')
    assert.equal(digitBandDownloadId.title, 'Sugar')

    const multipartNsc = parseFolderFileEntry({ name: 'NSC2000-06-08 - Shania Twain - Man! I Feel Like a Woman!.mp3', size: 8 }, 'auto')
    assert.equal(multipartNsc.disc, 'NSC2000-06-08')
    assert.equal(multipartNsc.artist, 'Shania Twain')
    assert.equal(multipartNsc.title, 'Man! I Feel Like a Woman!')

    const lookup = {
        hasTrack: (key) => key === 'u2:::one' || key === 'tommy tutone:::8675309',
        hasArtist: (artist) => artist === 'u2' || artist === 'tommy tutone',
    }
    const u2DownloadId = parseFolderFileEntry({ name: 'U2 - One - 443579.zip', size: 9 }, 'auto', lookup)
    assert.equal(u2DownloadId.disc, '')
    assert.equal(u2DownloadId.artist, 'U2')
    assert.equal(u2DownloadId.title, 'One')

    const customDiscAutoNumeric = parseFolderFileEntry({ name: 'D01 - Tommy Tutone - 8675309.zip', size: 10 }, 'auto', lookup)
    assert.equal(customDiscAutoNumeric.disc, 'D01')
    assert.equal(customDiscAutoNumeric.artist, 'Tommy Tutone')
    assert.equal(customDiscAutoNumeric.title, '8675309')

    const singleStripMedley = parseFolderFileEntry({ name: 'Artist - Medley - 8675309 - 443579.mp3', size: 11 }, 'auto')
    assert.equal(singleStripMedley.artist, 'Artist')
    assert.equal(singleStripMedley.title, 'Medley - 8675309')
})
