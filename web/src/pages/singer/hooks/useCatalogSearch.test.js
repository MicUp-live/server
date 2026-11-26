import test from 'node:test'
import assert from 'node:assert/strict'
import {
    getLetterBucket,
    normalizePublicOverrides,
    songIdentity,
    applyPublicOverrides,
    searchSongs,
    mergePendingSearchResults,
} from './useCatalogSearch.js'

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
        { song_id: '1', song: { song_id: '1', artist: 'Queen', title: 'Bohemian Rhapsody' } }, // duplicate
        { song_id: '2', song: { song_id: '2', artist: 'Queen', title: 'Radio Ga Ga' } }, // matches 'queen'
        { song_id: '3', song: { song_id: '3', artist: 'ABBA', title: 'Waterloo' } }, // does not match 'queen'
        { song_id: '4', deleted: true, song: { song_id: '4', artist: 'Queen', title: 'Somebody to Love' } }, // deleted
    ]

    const merged = mergePendingSearchResults(results, overrides, 'queen')
    assert.equal(merged.length, 2)
    assert.equal(merged[0].song_id, '1')
    assert.equal(merged[1].song_id, '2')
})
