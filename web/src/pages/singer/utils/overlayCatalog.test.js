import test from 'node:test'
import assert from 'node:assert/strict'
import {
    getLetterBucket,
    normalizePublicOverrides,
    songIdentity,
    applyPublicOverrides,
    searchSongs,
    mergePendingSearchResults,
    sortSongsByTitle,
    countSongsByLetter,
    countArtistsByLetter,
    firstAvailableLetterFromCounts,
    groupSongsByArtist,
    loadOverlayOnlyCatalog,
} from './overlayCatalog.js'

test('getLetterBucket buckets characters into A-Z or #', () => {
    assert.equal(getLetterBucket('Adele'), 'A')
    assert.equal(getLetterBucket(' beatles'), 'B')
    assert.equal(getLetterBucket('1975'), '#')
    assert.equal(getLetterBucket('!Special'), '#')
    assert.equal(getLetterBucket(''), '#')
    assert.equal(getLetterBucket(null), '#')
})

test('normalizePublicOverrides extracts array or defaults to empty', () => {
    assert.deepEqual(normalizePublicOverrides(null), [])
    assert.deepEqual(normalizePublicOverrides(undefined), [])
    assert.deepEqual(normalizePublicOverrides([1, 2]), [1, 2])
    assert.deepEqual(normalizePublicOverrides({ overrides: [{ song_id: '123' }] }), [{ song_id: '123' }])
    assert.deepEqual(normalizePublicOverrides({ other: 'data' }), [])
})

test('songIdentity generates unique identity from id or artist+title', () => {
    assert.equal(songIdentity({ song_id: 's-1' }), 's-1')
    assert.equal(songIdentity({ id: 's-2' }), 's-2')
    assert.equal(songIdentity({ artist: 'Queen', title: 'Bohemian' }), 'Queen\u0000Bohemian')
    assert.equal(songIdentity({ artist: 'Queen', label: 'Bohemian' }), 'Queen\u0000Bohemian')
})

test('applyPublicOverrides handles upserts, deletes, and clears', () => {
    const base = [
        { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' },
        { song_id: '2', artist: 'ABBA', title: 'Dancing Queen' },
    ]

    const overrides = [
        { song_id: '2', deleted: true },
        { song_id: '3', song: { song_id: '3', artist: 'Beatles', title: 'Yesterday' } },
    ]

    const result = applyPublicOverrides(base, overrides)
    assert.equal(result.length, 2)
    assert.equal(result.find(s => s.song_id === '1')?.title, 'Bohemian Rhapsody')
    assert.equal(result.find(s => s.song_id === '2'), undefined)
    assert.equal(result.find(s => s.song_id === '3')?.title, 'Yesterday')

    const clearOverrides = [
        { song_id: '__clear__', deleted: true },
        { song_id: '4', song: { song_id: '4', artist: 'Prince', title: 'Purple Rain' } },
    ]
    const clearedResult = applyPublicOverrides(base, clearOverrides)
    assert.equal(clearedResult.length, 1)
    assert.equal(clearedResult[0].title, 'Purple Rain')
})

test('sortSongsByTitle sorts primarily by title/label and secondarily by artist', () => {
    const unsorted = [
        { song_id: '1', artist: 'Zoe', title: 'Yesterday' },
        { song_id: '2', artist: 'Beatles', title: 'Yesterday' },
        { song_id: '3', artist: 'Queen', title: 'Bohemian Rhapsody' },
        { song_id: '4', artist: 'ABBA', label: 'Dancing Queen' },
    ]
    const sorted = sortSongsByTitle(unsorted)
    assert.deepEqual(sorted.map(s => s.song_id), ['3', '4', '2', '1'])
})

test('searchSongs filters case-insensitively across artist, title, genre', () => {
    const songs = [
        { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody', genre: 'Rock' },
        { song_id: '2', artist: 'ABBA', title: 'Dancing Queen', genre: 'Pop' },
        { song_id: '3', artist: 'Michael Jackson', title: 'Billie Jean', genre: 'Pop' },
    ]

    assert.equal(searchSongs(songs, '').length, 3)
    assert.equal(searchSongs(songs, 'queen').length, 2)
    assert.equal(searchSongs(songs, 'billie').length, 1)
    assert.equal(searchSongs(songs, 'rock').length, 1)
    assert.equal(searchSongs(songs, 'nonexistent').length, 0)
})

test('mergePendingSearchResults appends matching overrides without duplicates', () => {
    const results = [
        { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' },
    ]

    const overrides = [
        { song_id: '1', song: { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' } },
        { song_id: '2', song: { song_id: '2', artist: 'Queen', title: 'Radio Ga Ga' } },
        { song_id: '3', song: { song_id: '3', artist: 'ABBA', title: 'Waterloo' } },
        { song_id: '4', deleted: true, song: { song_id: '4', artist: 'Queen', title: 'Somebody to Love' } },
    ]

    const merged = mergePendingSearchResults(results, overrides, 'queen')
    assert.equal(merged.length, 2)
    assert.equal(merged[0].song_id, '1')
    assert.equal(merged[1].song_id, '2')
})

test('countSongsByLetter, countArtistsByLetter, and firstAvailableLetterFromCounts work correctly', () => {
    const songs = [
        { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' },
        { song_id: '2', artist: 'ABBA', title: 'Dancing Queen' },
        { song_id: '3', artist: 'Queen', title: 'Radio Ga Ga' },
        { song_id: '4', artist: '10cc', title: 'Dreadlock Holiday' },
        { song_id: '5', artist: '10cc', title: 'The Things We Do for Love' },
    ]
    const songCounts = countSongsByLetter(songs)
    assert.deepEqual(songCounts, { B: 1, D: 2, R: 1, T: 1 })

    const artistCounts = countArtistsByLetter(songs)
    assert.deepEqual(artistCounts, { Q: 1, A: 1, '#': 1 })

    assert.equal(firstAvailableLetterFromCounts(songCounts), 'B')
    assert.equal(firstAvailableLetterFromCounts(artistCounts), 'A')
    assert.equal(firstAvailableLetterFromCounts({ '#': 3 }), '#')
    assert.equal(firstAvailableLetterFromCounts({}), 'A')
})

test('groupSongsByArtist groups by artist and handles letter filtering & search', () => {
    const songs = [
        { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' },
        { song_id: '2', artist: 'Queen', title: 'Radio Ga Ga' },
        { song_id: '3', artist: 'ABBA', title: 'Dancing Queen' },
        { song_id: '4', artist: 'Aerosmith', title: 'Dream On' },
    ]

    const groupsLetterA = groupSongsByArtist(songs, { activeArtistLetter: 'A', isSearching: false })
    assert.equal(groupsLetterA.length, 2)
    assert.equal(groupsLetterA[0].artist, 'ABBA')
    assert.equal(groupsLetterA[0].count, 1)
    assert.equal(groupsLetterA[1].artist, 'Aerosmith')

    const groupsSearching = groupSongsByArtist(songs, { activeArtistLetter: 'A', isSearching: true })
    assert.equal(groupsSearching.length, 3) // ABBA, Aerosmith, Queen
})

test('loadOverlayOnlyCatalog fetches overrides, excludes deleted, sorts, and counts', async () => {
    let requestedPath = null
    const mockApi = {
        get: async (path) => {
            requestedPath = path
            return {
                overrides: [
                    { song_id: '1', song: { song_id: '1', artist: 'Queen', title: 'Radio Ga Ga' }, deleted: false },
                    { song_id: '2', song: { song_id: '2', artist: 'ABBA', title: 'Dancing Queen' }, deleted: false },
                    { song_id: '3', song: { song_id: '3', artist: 'Prince', title: '1999' }, deleted: true },
                    { song_id: '4', song: { song_id: '4', artist: 'Beatles', title: 'All You Need Is Love' }, deleted: false },
                ]
            }
        }
    }

    const result = await loadOverlayOnlyCatalog(mockApi, 'sb-456', '?code=JOIN123')
    assert.equal(requestedPath, '/songbooks/sb-456/public-overrides?code=JOIN123')
    assert.equal(result.totalSongs, 3)
    assert.equal(result.songs.length, 3)
    // Sorted by title: "All You Need Is Love", "Dancing Queen", "Radio Ga Ga"
    assert.deepEqual(result.songs.map(s => s.song_id), ['4', '2', '1'])
    assert.equal(result.firstLetter, 'A')
    assert.equal(result.letterCounts.A, 1)
    assert.equal(result.letterCounts.D, 1)
    assert.equal(result.letterCounts.R, 1)
    assert.equal(result.artistLetterCounts.A, 1)
    assert.equal(result.artistLetterCounts.B, 1)
    assert.equal(result.artistLetterCounts.Q, 1)
})
