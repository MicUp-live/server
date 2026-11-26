import test from 'node:test'
import assert from 'node:assert/strict'
import { filterFavoriteSongs, filterFavoriteArtists } from './favoritesHelpers.js'

test('filterFavoriteSongs sorts alphabetically and filters by title or artist', () => {
    const songs = [
        { artist: 'queen', title: 'bohemian rhapsody', display_artist: 'Queen', display_title: 'Bohemian Rhapsody', preferred_key: 0 },
        { artist: 'abba', title: 'dancing queen', display_artist: 'ABBA', display_title: 'Dancing Queen', preferred_key: 1 },
        { artist: 'journey', title: 'dont stop believin', display_artist: 'Journey', display_title: "Don't Stop Believin'", preferred_key: -2 },
    ]

    const all = filterFavoriteSongs(songs, '')
    assert.deepEqual(all.map(s => s.display_title), ['Bohemian Rhapsody', 'Dancing Queen', "Don't Stop Believin'"])

    const byArtistOrTitle = filterFavoriteSongs(songs, '  QUEEN ')
    assert.deepEqual(byArtistOrTitle.map(s => s.display_title), ['Bohemian Rhapsody', 'Dancing Queen'])

    const noMatch = filterFavoriteSongs(songs, 'metallica')
    assert.equal(noMatch.length, 0)
})

test('filterFavoriteArtists sorts alphabetically and filters by display name', () => {
    const artists = [
        { normalized: 'queen', display_name: 'Queen' },
        { normalized: 'fleetwood mac', display_name: 'Fleetwood Mac' },
        { normalized: 'abba', display_name: 'ABBA' },
    ]

    const all = filterFavoriteArtists(artists, '')
    assert.deepEqual(all.map(a => a.display_name), ['ABBA', 'Fleetwood Mac', 'Queen'])

    const matched = filterFavoriteArtists(artists, 'mac')
    assert.deepEqual(matched.map(a => a.display_name), ['Fleetwood Mac'])
})

test('favoritesHelpers handles legacy entries without display fields and non-array inputs', () => {
    assert.deepEqual(filterFavoriteSongs(null, 'queen'), [])
    assert.deepEqual(filterFavoriteArtists(undefined, 'mac'), [])

    const legacySongs = [
        { artist: 'queen', title: 'killer queen' },
        { artist: 'abba', title: 'waterloo' },
    ]
    assert.deepEqual(
        filterFavoriteSongs(legacySongs, 'queen').map(s => s.title),
        ['killer queen'],
    )

    const legacyArtists = [
        { normalized: 'journey' },
        { normalized: 'abba' },
    ]
    assert.deepEqual(
        filterFavoriteArtists(legacyArtists, '').map(a => a.normalized),
        ['abba', 'journey'],
    )
})
