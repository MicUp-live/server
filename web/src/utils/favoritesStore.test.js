import test from 'node:test'
import assert from 'node:assert/strict'
import { api } from '../api/client.js'
import { user } from '../auth/store.js'
import {
    normalizeString,
    songKey,
    clampPreferredKey,
    formatPreferredKey,
    getFavoriteSongs,
    isFavoriteSong,
    getFavoriteSong,
    toggleFavoriteSong,
    updatePreferredKey,
    addFavoriteSong,
    updateFavoriteSong,
    removeFavoriteSong,
    getFavoriteArtists,
    isFavoriteArtist,
    toggleFavoriteArtist,
    addFavoriteArtist,
    updateFavoriteArtist,
    removeFavoriteArtist,
    getFavoritesCount,
    syncFavoritesOnLogin,
    clearFavoritesOnLogout,
    flushFavoritesSync,
} from './favoritesStore.js'

// Mock minimal localStorage for Node environment test
const storage = new Map()
let failStorageWrites = false
let dispatchedEvents = []

global.localStorage = {
    getItem: (k) => (storage.has(k) ? storage.get(k) : null),
    setItem: (k, v) => {
        if (failStorageWrites) {
            throw new Error('QuotaExceededError')
        }
        storage.set(k, String(v))
    },
    removeItem: (k) => storage.delete(k),
    clear: () => storage.clear(),
}
global.window = {
    dispatchEvent: (ev) => {
        dispatchedEvents.push(ev.type)
        return true
    },
    CustomEvent: class CustomEvent { constructor(type) { this.type = type } }
}

test('favoritesStore normalization and song keys', () => {
    assert.equal(normalizeString('  Queen  '), 'queen')
    assert.equal(normalizeString('THE   BEATLES'), 'the beatles')
    assert.equal(songKey('Queen', 'Bohemian Rhapsody'), 'queen:::bohemian rhapsody')
    assert.equal(songKey('QUEEN  ', '  bohemian rhapsody'), 'queen:::bohemian rhapsody')
})

test('favoritesStore song favorites and preferred key', () => {
    storage.clear()
    const song = { artist: 'Queen', title: 'Bohemian Rhapsody' }

    assert.equal(isFavoriteSong(song.artist, song.title), false)

    // Toggle on with key -1
    const favorited = toggleFavoriteSong(song, -1)
    assert.equal(favorited, true)
    assert.equal(isFavoriteSong('QUEEN', 'bohemian RHAPSODY'), true)

    const list = getFavoriteSongs()
    assert.equal(list.length, 1)
    assert.equal(list[0].preferred_key, -1)

    // Update preferred key to +2
    updatePreferredKey('Queen', 'Bohemian Rhapsody', 2)
    const updatedList = getFavoriteSongs()
    assert.equal(updatedList[0].preferred_key, 2)

    // Toggle off
    const unfavorited = toggleFavoriteSong(song)
    assert.equal(unfavorited, false)
    assert.equal(isFavoriteSong(song.artist, song.title), false)
})

test('favoritesStore canonical_id cross-venue matching and pitch retention', () => {
    storage.clear()
    const song = { artist: 'Beatles, The', title: 'Hey Jude (Karaoke Version)', canonical_id: 'can_test_hey_jude' }

    // Favorite at Venue A with -2 key
    toggleFavoriteSong(song, -2)

    // Verify lookup by canonical_id works even if Venue B spells artist/title differently
    assert.equal(isFavoriteSong('The Beatles', 'Hey Jude', 'can_test_hey_jude'), true)
    const fav = getFavoriteSong('The Beatles', 'Hey Jude', 'can_test_hey_jude')
    assert.ok(fav)
    assert.equal(fav.preferred_key, -2)
    assert.equal(fav.canonical_id, 'can_test_hey_jude')
})

test('favoritesStore artist favorites', () => {
    storage.clear()
    assert.equal(isFavoriteArtist('Fleetwood Mac'), false)

    const favorited = toggleFavoriteArtist('Fleetwood Mac')
    assert.equal(favorited, true)
    assert.equal(isFavoriteArtist('FLEETWOOD MAC'), true)

    const artists = getFavoriteArtists()
    assert.equal(artists.length, 1)
    assert.equal(artists[0].display_name, 'Fleetwood Mac')

    const unfavorited = toggleFavoriteArtist('fleetwood mac')
    assert.equal(unfavorited, false)
    assert.equal(isFavoriteArtist('Fleetwood Mac'), false)
})

test('favoritesStore direct add/edit/remove for songs without a songbook', () => {
    storage.clear()

    // Reject blank inputs
    assert.deepEqual(addFavoriteSong({ title: '  ', artist: 'Queen' }), { ok: false, reason: 'invalid' })
    assert.deepEqual(addFavoriteSong({ title: 'Killer Queen', artist: '' }), { ok: false, reason: 'invalid' })

    // Add song with out-of-range pitch key (clamped to +3)
    const added = addFavoriteSong({ title: '  Killer Queen ', artist: ' Queen ', preferred_key: 9 })
    assert.equal(added.ok, true)
    assert.equal(added.entry.display_title, 'Killer Queen')
    assert.equal(added.entry.display_artist, 'Queen')
    assert.equal(added.entry.preferred_key, 3)

    // Duplicate add (case-insensitive / whitespace-normalized) is rejected
    const dup = addFavoriteSong({ title: 'killer   queen', artist: 'QUEEN', preferred_key: -1 })
    assert.equal(dup.ok, false)
    assert.equal(dup.reason, 'duplicate')
    assert.equal(getFavoriteSongs().length, 1)

    // Seed a second song with canonical_id to verify identity preservation & clearing
    const canAdd = addFavoriteSong({
        title: 'Hey Jude',
        artist: 'The Beatles',
        preferred_key: -1,
        canonical_id: 'can_hey_jude',
    })
    assert.equal(canAdd.ok, true)
    const origCreatedAt = canAdd.entry.created_at

    // Edit only pitch key and casing -> preserves canonical_id and created_at
    const pitchEdit = updateFavoriteSong(canAdd.entry, {
        title: 'Hey Jude',
        artist: 'The Beatles',
        preferred_key: -2,
    })
    assert.equal(pitchEdit.ok, true)
    assert.equal(pitchEdit.entry.preferred_key, -2)
    assert.equal(pitchEdit.entry.canonical_id, 'can_hey_jude')
    assert.equal(pitchEdit.entry.created_at, origCreatedAt)

    // Edit colliding with another existing song -> rejected
    const collision = updateFavoriteSong(pitchEdit.entry, {
        title: 'Killer Queen',
        artist: 'Queen',
        preferred_key: 0,
    })
    assert.equal(collision.ok, false)
    assert.equal(collision.reason, 'duplicate')

    // Edit changing title/artist -> updates normalized identity and clears canonical_id
    const renamed = updateFavoriteSong(pitchEdit.entry, {
        title: 'Let It Be',
        artist: 'The Beatles',
        preferred_key: 1,
    })
    assert.equal(renamed.ok, true)
    assert.equal(renamed.entry.display_title, 'Let It Be')
    assert.equal(renamed.entry.canonical_id, null)
    assert.equal(renamed.entry.preferred_key, 1)
    assert.equal(isFavoriteSong('The Beatles', 'Hey Jude'), false)
    assert.equal(isFavoriteSong('The Beatles', 'Let It Be'), true)

    // Remove song
    assert.equal(removeFavoriteSong('The Beatles', 'Let It Be'), true)
    assert.equal(removeFavoriteSong('The Beatles', 'Let It Be'), false)
    assert.equal(getFavoriteSongs().length, 1)
})

test('favoritesStore direct add/edit/remove for artists and counts', () => {
    storage.clear()

    assert.deepEqual(addFavoriteArtist('   '), { ok: false, reason: 'invalid' })

    const a1 = addFavoriteArtist('  Fleetwood Mac  ')
    assert.equal(a1.ok, true)
    assert.equal(a1.entry.display_name, 'Fleetwood Mac')

    const dup = addFavoriteArtist('fleetwood   mac')
    assert.equal(dup.ok, false)
    assert.equal(dup.reason, 'duplicate')

    const a2 = addFavoriteArtist('Queen')
    assert.equal(a2.ok, true)

    // Collision on edit rejected
    const collision = updateFavoriteArtist('Queen', 'fleetwood mac')
    assert.equal(collision.ok, false)
    assert.equal(collision.reason, 'duplicate')

    // Valid rename succeeds
    const renamed = updateFavoriteArtist('Queen', 'Queen & Adam Lambert')
    assert.equal(renamed.ok, true)
    assert.equal(renamed.entry.display_name, 'Queen & Adam Lambert')
    assert.equal(isFavoriteArtist('Queen'), false)
    assert.equal(isFavoriteArtist('Queen & Adam Lambert'), true)

    // Counts reflect both songs and artists
    addFavoriteSong({ title: 'Dreams', artist: 'Fleetwood Mac', preferred_key: 0 })
    assert.deepEqual(getFavoritesCount(), { songs: 1, artists: 2, total: 3 })

    assert.equal(removeFavoriteArtist('Fleetwood Mac'), true)
    assert.equal(removeFavoriteArtist('Fleetwood Mac'), false)
    assert.deepEqual(getFavoritesCount(), { songs: 1, artists: 1, total: 2 })
})

test('favoritesStore pitch helpers clamp and format', () => {
    assert.equal(clampPreferredKey(-10), -3)
    assert.equal(clampPreferredKey(10), 3)
    assert.equal(clampPreferredKey('not-a-number'), 0)
    assert.equal(Object.is(clampPreferredKey(-0.5), 0), true)
    assert.equal(formatPreferredKey(2), '+2♯')
    assert.equal(formatPreferredKey(-1), '-1♭')
    assert.equal(formatPreferredKey(0), 'Standard Pitch')
})

test('favoritesStore emits micup_favorites_changed and handles storage failures and edge cases', () => {
    storage.clear()
    dispatchedEvents = []
    failStorageWrites = false

    // Add song -> emits event
    const added = addFavoriteSong({ title: 'Dreams', artist: 'Fleetwood Mac', preferred_key: -2, canonical_id: 'can_dreams' })
    assert.equal(added.ok, true)
    assert.equal(dispatchedEvents.filter(t => t === 'micup_favorites_changed').length, 1)

    // Update preserving preferred_key when undefined and matching by canonical_id
    const preservedKey = updateFavoriteSong(
        { artist: 'Different Spelling', title: 'Different Title', canonical_id: 'can_dreams' },
        { title: 'Dreams', artist: 'Fleetwood Mac' },
    )
    assert.equal(preservedKey.ok, true)
    assert.equal(preservedKey.entry.preferred_key, -2)
    assert.equal(preservedKey.entry.canonical_id, 'can_dreams')

    // Missing / invalid targets return not_found / invalid without emitting
    const countBefore = dispatchedEvents.length
    assert.deepEqual(updateFavoriteSong({ artist: '', title: '' }, { title: 'A', artist: 'B' }), { ok: false, reason: 'not_found' })
    assert.deepEqual(updateFavoriteSong(added.entry, { title: '', artist: 'Queen' }), { ok: false, reason: 'invalid' })
    assert.equal(removeFavoriteSong('', ''), false)
    assert.deepEqual(updateFavoriteArtist('Missing Artist', 'New Name'), { ok: false, reason: 'not_found' })
    assert.deepEqual(updateFavoriteArtist('Fleetwood Mac', '   '), { ok: false, reason: 'invalid' })
    assert.equal(dispatchedEvents.length, countBefore)

    // Storage write failures return storage_error / false and do not emit change events
    failStorageWrites = true
    assert.deepEqual(addFavoriteSong({ title: 'Africa', artist: 'Toto', preferred_key: 0 }), { ok: false, reason: 'storage_error' })
    assert.deepEqual(updateFavoriteSong(added.entry, { title: 'Dreams', artist: 'Fleetwood Mac', preferred_key: 1 }), { ok: false, reason: 'storage_error' })
    assert.equal(removeFavoriteSong('Fleetwood Mac', 'Dreams'), false)
    assert.deepEqual(addFavoriteArtist('Toto'), { ok: false, reason: 'storage_error' })
    assert.equal(dispatchedEvents.length, countBefore)
    failStorageWrites = false
})

test('favoritesStore safely handles corrupt non-array or null-containing localStorage payloads', () => {
    storage.clear()
    storage.set('micup_singer_favorites', '{"not":"an-array"}')
    storage.set('micup_singer_favorite_artists', 'null')
    assert.deepEqual(getFavoriteSongs(), [])
    assert.deepEqual(getFavoriteArtists(), [])
    assert.deepEqual(getFavoritesCount(), { songs: 0, artists: 0, total: 0 })

    storage.set('micup_singer_favorites', JSON.stringify([null, 42, { artist: 'queen', title: 'killer queen', display_artist: 'Queen', display_title: 'Killer Queen', preferred_key: 0 }]))
    storage.set('micup_singer_favorite_artists', JSON.stringify([null, 'bad', { normalized: 'queen', display_name: 'Queen' }]))
    assert.equal(getFavoriteSongs().length, 1)
    assert.equal(getFavoriteArtists().length, 1)
    assert.deepEqual(getFavoritesCount(), { songs: 1, artists: 1, total: 2 })
})

test('account-synced favorites: guest local isolation and no sync when unauthenticated', async () => {
    storage.clear()
    user.value = null
    dispatchedEvents = []

    let putCalled = false
    const origPut = api.put
    api.put = async () => { putCalled = true }

    try {
        addFavoriteSong({ artist: 'Queen', title: 'Somebody to Love', preferred_key: 1 })
        addFavoriteArtist('David Bowie')
        await flushFavoritesSync()

        assert.equal(putCalled, false, 'Unauthenticated guests must not trigger PUT /api/v1/auth/favorites')
        assert.equal(getFavoriteSongs().length, 1)
        assert.equal(getFavoriteArtists().length, 1)
    } finally {
        api.put = origPut
    }
})

test('account-synced favorites: syncFavoritesOnLogin merges, dedupes, resolves key conflicts, and persists local additions', async () => {
    storage.clear()
    user.value = { id: 'u123', email: 'singer@example.com' }
    dispatchedEvents = []

    // Local guest favorites before login:
    // 1. "Queen - Radio Ga Ga": local has newer timestamp and preferred_key = +2
    // 2. "The Beatles - Let It Be": local has older timestamp and preferred_key = -1
    // 3. "Toto - Africa": local only (not in remote account)
    const tOlder = '2026-01-01T00:00:00.000Z'
    const tNewer = '2026-06-01T00:00:00.000Z'
    const tRemote = '2026-03-01T00:00:00.000Z'

    storage.set('micup_singer_favorites', JSON.stringify([
        { artist: 'queen', title: 'radio ga ga', display_artist: 'Queen', display_title: 'Radio Ga Ga', preferred_key: 2, added_at: tNewer },
        { artist: 'the beatles', title: 'let it be', display_artist: 'The Beatles', display_title: 'Let It Be', preferred_key: -1, added_at: tOlder },
        { artist: 'toto', title: 'africa', display_artist: 'Toto', display_title: 'Africa', preferred_key: 0, added_at: tOlder },
    ]))
    storage.set('micup_singer_favorite_artists', JSON.stringify([
        { normalized: 'queen', display_name: 'Queen', added_at: tOlder },
        { normalized: 'toto', display_name: 'Toto', added_at: tOlder },
    ]))

    const remoteData = {
        songs: [
            // Remote Radio Ga Ga has key = 0, but local is newer (tNewer > tRemote) -> local key (+2) wins
            { artist: 'Queen', title: 'Radio Ga Ga', preferred_key: 0, added_at: tRemote },
            // Remote Let It Be has key = 2, and remote is newer (tRemote > tOlder) -> account key (+2) wins
            { artist: 'The Beatles', title: 'Let It Be', preferred_key: 2, added_at: tRemote },
            // Remote only song
            { artist: 'Elton John', title: 'Rocket Man', preferred_key: 1, canonical_id: 'can_rocket', added_at: tRemote },
        ],
        artists: [
            { name: 'Queen', added_at: tRemote },
            { name: 'Elton John', added_at: tRemote },
        ],
    }

    let putPayload = null
    const origGet = api.get
    const origPut = api.put
    api.get = async (path) => {
        if (path === '/auth/favorites') return remoteData
        throw new Error(`Unexpected GET: ${path}`)
    }
    api.put = async (path, body) => {
        if (path === '/auth/favorites') {
            putPayload = body
            return body
        }
        throw new Error(`Unexpected PUT: ${path}`)
    }

    try {
        await syncFavoritesOnLogin()

        // Verify merged songs in local storage
        const songs = getFavoriteSongs()
        assert.equal(songs.length, 4, 'Merged songs count should be 4')

        const radioGaGa = songs.find(s => s.artist === 'queen' && s.title === 'radio ga ga')
        assert.ok(radioGaGa)
        assert.equal(radioGaGa.preferred_key, 2, 'Local key (+2) wins because local added_at is newer')

        const letItBe = songs.find(s => s.artist === 'the beatles' && s.title === 'let it be')
        assert.ok(letItBe)
        assert.equal(letItBe.preferred_key, 2, 'Account key (+2) wins because local was older')

        const rocketMan = songs.find(s => s.artist === 'elton john' && s.title === 'rocket man')
        assert.ok(rocketMan)
        assert.equal(rocketMan.canonical_id, 'can_rocket')

        const africa = songs.find(s => s.artist === 'toto' && s.title === 'africa')
        assert.ok(africa, 'Local-only Toto Africa is preserved in merged set')

        // Verify merged artists in local storage
        const artists = getFavoriteArtists()
        assert.equal(artists.length, 3, 'Merged artists should include Queen, Elton John, and Toto')
        assert.ok(artists.some(a => a.normalized === 'queen'))
        assert.ok(artists.some(a => a.normalized === 'elton john'))
        assert.ok(artists.some(a => a.normalized === 'toto'))

        // Because local had items not in remote (Toto Africa, Toto artist), PUT must have been called
        assert.ok(putPayload, 'PUT /api/v1/auth/favorites should have been called to sync local items to account')
        assert.equal(putPayload.songs.length, 4)
        assert.equal(putPayload.artists.length, 3)
        assert.ok(dispatchedEvents.includes('micup_favorites_changed'))
    } finally {
        api.get = origGet
        api.put = origPut
        user.value = null
    }
})

test('account-synced favorites: mutation debounces PUT when logged in, and clearFavoritesOnLogout cancels and isolates', async () => {
    storage.clear()
    user.value = { id: 'u123', email: 'singer@example.com' }
    dispatchedEvents = []

    let putCount = 0
    let lastPutBody = null
    const origPut = api.put
    api.put = async (path, body) => {
        if (path === '/auth/favorites') {
            putCount++
            lastPutBody = body
            return body
        }
        throw new Error(`Unexpected PUT: ${path}`)
    }

    try {
        // 1. Add a favorite song while logged in
        addFavoriteSong({ artist: 'Queen', title: 'Bohemian Rhapsody', preferred_key: 0 })
        assert.equal(putCount, 0, 'Should not sync synchronously (debounced)')

        // 2. Flush pending debounced sync
        await flushFavoritesSync()
        assert.equal(putCount, 1)
        assert.equal(lastPutBody.songs.length, 1)
        assert.equal(lastPutBody.songs[0].artist, 'Queen')

        // 3. Mutate preferred key and schedule next sync
        updatePreferredKey('Queen', 'Bohemian Rhapsody', 2)
        assert.equal(putCount, 1, 'Sync is debounced')

        // 4. Logout before timer fires: must cancel timer and purge localStorage
        clearFavoritesOnLogout()
        assert.equal(localStorage.getItem('micup_singer_favorites'), null)
        assert.equal(localStorage.getItem('micup_singer_favorite_artists'), null)
        assert.deepEqual(getFavoriteSongs(), [])
        assert.deepEqual(getFavoriteArtists(), [])
        assert.ok(dispatchedEvents.includes('micup_favorites_changed'))

        // Flush must not do any PUT because timer was cancelled and user is logged out
        user.value = null
        await flushFavoritesSync()
        assert.equal(putCount, 1, 'Cancelled sync must not fire after logout')
    } finally {
        api.put = origPut
        user.value = null
    }
})
