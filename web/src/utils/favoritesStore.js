/**
 * Singer Universal Favorites & Preferred Key Store.
 * Persists favorites across all events, hosts, and songbooks using normalized (artist, title) keys.
 */

import { api } from '../api/client.js'
import { user } from '../auth/store.js'

export const FAVORITE_SONGS_STORAGE_KEY = 'micup_singer_favorites'
export const FAVORITE_ARTISTS_STORAGE_KEY = 'micup_singer_favorite_artists'

const SONGS_KEY = FAVORITE_SONGS_STORAGE_KEY
const ARTISTS_KEY = FAVORITE_ARTISTS_STORAGE_KEY

let syncTimer = null
let isSyncing = false

export const MIN_PREFERRED_KEY = -3
export const MAX_PREFERRED_KEY = 3

export function clampPreferredKey(val) {
    const parsed = Number(val)
    if (!Number.isFinite(parsed)) return 0
    const clamped = Math.max(MIN_PREFERRED_KEY, Math.min(MAX_PREFERRED_KEY, Math.trunc(parsed)))
    return clamped || 0
}

export function formatPreferredKey(key) {
    const k = clampPreferredKey(key)
    if (k > 0) return `+${k}♯`
    if (k < 0) return `${k}♭`
    return 'Standard Pitch'
}

export function normalizeString(str) {
    return String(str ?? '').trim().toLowerCase().replace(/\s+/g, ' ')
}

export function songKey(artist, title) {
    return `${normalizeString(artist)}:::${normalizeString(title)}`
}

function notifyChange() {
    if (typeof window !== 'undefined') {
        window.dispatchEvent(new CustomEvent('micup_favorites_changed'))
    }
}

/**
 * Get all favorited songs.
 * Returns array of { artist, title, display_artist, display_title, preferred_key, created_at }
 */
export function getFavoriteSongs() {
    if (typeof window === 'undefined') return []
    try {
        const raw = localStorage.getItem(SONGS_KEY)
        if (!raw) return []
        const parsed = JSON.parse(raw)
        if (!Array.isArray(parsed)) return []
        return parsed.filter(item => item && typeof item === 'object')
    } catch {
        return []
    }
}

/**
 * Check if a specific song is favorited (by key or canonical_id).
 */
export function isFavoriteSong(artist, title, canonicalId = null) {
    const key = songKey(artist, title)
    const list = getFavoriteSongs()
    return list.some(item => {
        if (canonicalId && item.canonical_id && canonicalId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    })
}

/**
 * Get favorite entry for a song (including preferred_key and canonical_id).
 */
export function getFavoriteSong(artist, title, canonicalId = null) {
    const key = songKey(artist, title)
    const list = getFavoriteSongs()
    return list.find(item => {
        if (canonicalId && item.canonical_id && canonicalId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    }) || null
}

/**
 * Toggle favorite status for a song.
 * Returns true if now favorited, false if removed.
 */
export function toggleFavoriteSong(song, preferredKey = 0) {
    if (typeof window === 'undefined') return false
    const key = songKey(song.artist, song.title || song.label)
    const canId = song.canonical_id || null
    const list = getFavoriteSongs()
    const index = list.findIndex(item => {
        if (canId && item.canonical_id && canId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    })

    let nowFavorited = false
    if (index >= 0) {
        // Remove
        list.splice(index, 1)
        nowFavorited = false
    } else {
        // Add
        list.push({
            artist: normalizeString(song.artist),
            title: normalizeString(song.title || song.label),
            canonical_id: canId,
            display_artist: String(song.artist ?? '').trim(),
            display_title: String(song.title || song.label || '').trim(),
            preferred_key: clampPreferredKey(preferredKey),
            created_at: new Date().toISOString(),
        })
        nowFavorited = true
    }

    if (!saveFavoriteSongs(list)) return false
    return nowFavorited
}

/**
 * Update preferred key for a song. If song is not favorited yet, this also favorites it.
 */
export function updatePreferredKey(artist, title, preferredKey, canonicalId = null) {
    if (typeof window === 'undefined') return
    const key = songKey(artist, title)
    const clamped = clampPreferredKey(preferredKey)
    const list = getFavoriteSongs()
    const found = list.find(item => {
        if (canonicalId && item.canonical_id && canonicalId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    })

    if (found) {
        found.preferred_key = clamped
        if (canonicalId && !found.canonical_id) {
            found.canonical_id = canonicalId
        }
    } else {
        list.push({
            artist: normalizeString(artist),
            title: normalizeString(title),
            canonical_id: canonicalId || null,
            display_artist: String(artist ?? '').trim(),
            display_title: String(title ?? '').trim(),
            preferred_key: clamped,
            created_at: new Date().toISOString(),
        })
    }

    saveFavoriteSongs(list)
}

/**
 * Get all favorited artists.
 * Returns array of { normalized, display_name }
 */
export function getFavoriteArtists() {
    if (typeof window === 'undefined') return []
    try {
        const raw = localStorage.getItem(ARTISTS_KEY)
        if (!raw) return []
        const parsed = JSON.parse(raw)
        if (!Array.isArray(parsed)) return []
        return parsed.filter(item => item && typeof item === 'object')
    } catch {
        return []
    }
}

/**
 * Check if an artist is favorited.
 */
export function isFavoriteArtist(artist) {
    const norm = normalizeString(artist)
    if (!norm) return false
    const list = getFavoriteArtists()
    return list.some(item => item.normalized === norm)
}

/**
 * Toggle favorite status for an artist.
 * Returns true if now favorited, false if removed.
 */
export function toggleFavoriteArtist(artist) {
    if (typeof window === 'undefined') return false
    const norm = normalizeString(artist)
    if (!norm) return false

    const list = getFavoriteArtists()
    const index = list.findIndex(item => item.normalized === norm)

    let nowFavorited = false
    if (index >= 0) {
        list.splice(index, 1)
        nowFavorited = false
    } else {
        list.push({
            normalized: norm,
            display_name: (artist || '').trim(),
            created_at: new Date().toISOString(),
        })
        nowFavorited = true
    }

    if (!saveFavoriteArtists(list)) return false
    return nowFavorited
}

function saveFavoriteSongs(list) {
    try {
        localStorage.setItem(SONGS_KEY, JSON.stringify(list))
    } catch {
        return false
    }
    notifyChange()
    scheduleDebouncedAccountSync()
    return true
}

function saveFavoriteArtists(list) {
    try {
        localStorage.setItem(ARTISTS_KEY, JSON.stringify(list))
    } catch {
        return false
    }
    notifyChange()
    scheduleDebouncedAccountSync()
    return true
}

/**
 * Add a favorite song directly (without needing a songbook).
 * Returns { ok: true, entry } or { ok: false, reason: 'invalid' | 'duplicate' | 'storage_error', entry? }.
 */
export function addFavoriteSong({ title, artist, preferred_key = 0, canonical_id = null } = {}) {
    if (typeof window === 'undefined') return { ok: false, reason: 'invalid' }
    const displayTitle = String(title ?? '').trim()
    const displayArtist = String(artist ?? '').trim()
    if (!displayTitle || !displayArtist) {
        return { ok: false, reason: 'invalid' }
    }

    const normArtist = normalizeString(displayArtist)
    const normTitle = normalizeString(displayTitle)
    const key = songKey(normArtist, normTitle)
    const canId = canonical_id || null
    const list = getFavoriteSongs()

    const existing = list.find(item => {
        if (canId && item.canonical_id && canId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    })
    if (existing) {
        return { ok: false, reason: 'duplicate', entry: existing }
    }

    const entry = {
        artist: normArtist,
        title: normTitle,
        canonical_id: canId,
        display_artist: displayArtist,
        display_title: displayTitle,
        preferred_key: clampPreferredKey(preferred_key),
        created_at: new Date().toISOString(),
    }
    list.push(entry)
    if (!saveFavoriteSongs(list)) {
        return { ok: false, reason: 'storage_error' }
    }
    return { ok: true, entry }
}

/**
 * Edit an existing favorite song's title, artist, and/or preferred key.
 * Preserves canonical_id when normalized artist & title are unchanged; clears it otherwise.
 * Returns { ok: true, entry } or { ok: false, reason: 'invalid' | 'not_found' | 'duplicate' | 'storage_error' }.
 */
export function updateFavoriteSong(originalTarget, { title, artist, preferred_key } = {}) {
    if (typeof window === 'undefined') return { ok: false, reason: 'invalid' }
    const displayTitle = String(title ?? '').trim()
    const displayArtist = String(artist ?? '').trim()
    if (!displayTitle || !displayArtist) {
        return { ok: false, reason: 'invalid' }
    }

    const origCanId = originalTarget?.canonical_id || null
    const origArtistNorm = normalizeString(originalTarget?.artist)
    const origTitleNorm = normalizeString(originalTarget?.title)
    if (!origCanId && (!origArtistNorm || !origTitleNorm)) {
        return { ok: false, reason: 'not_found' }
    }

    const origKey = songKey(origArtistNorm, origTitleNorm)
    const list = getFavoriteSongs()
    const index = list.findIndex(item => {
        if (origCanId && item.canonical_id && origCanId === item.canonical_id) return true
        return songKey(item.artist, item.title) === origKey
    })
    if (index < 0) {
        return { ok: false, reason: 'not_found' }
    }

    const nextArtist = normalizeString(displayArtist)
    const nextTitle = normalizeString(displayTitle)
    const nextKey = songKey(nextArtist, nextTitle)

    const duplicate = list.some((item, idx) => idx !== index && songKey(item.artist, item.title) === nextKey)
    if (duplicate) {
        return { ok: false, reason: 'duplicate' }
    }

    const existing = list[index]
    const identityUnchanged = songKey(existing.artist, existing.title) === nextKey
    const updated = {
        ...existing,
        artist: nextArtist,
        title: nextTitle,
        canonical_id: identityUnchanged ? (existing.canonical_id || null) : null,
        display_artist: displayArtist,
        display_title: displayTitle,
        preferred_key: preferred_key === undefined
            ? clampPreferredKey(existing.preferred_key)
            : clampPreferredKey(preferred_key),
    }

    list[index] = updated
    if (!saveFavoriteSongs(list)) {
        return { ok: false, reason: 'storage_error' }
    }
    return { ok: true, entry: updated }
}

/**
 * Remove a favorite song by artist/title (or canonical_id).
 * Returns true if removed and persisted, false if not found or storage failed.
 */
export function removeFavoriteSong(artist, title, canonicalId = null) {
    if (typeof window === 'undefined') return false
    const normArtist = normalizeString(artist)
    const normTitle = normalizeString(title)
    if (!canonicalId && (!normArtist || !normTitle)) return false

    const key = songKey(normArtist, normTitle)
    const list = getFavoriteSongs()
    const index = list.findIndex(item => {
        if (canonicalId && item.canonical_id && canonicalId === item.canonical_id) return true
        return songKey(item.artist, item.title) === key
    })
    if (index < 0) return false
    list.splice(index, 1)
    return saveFavoriteSongs(list)
}

/**
 * Add a favorite artist directly (without needing a songbook).
 * Returns { ok: true, entry } or { ok: false, reason: 'invalid' | 'duplicate' | 'storage_error', entry? }.
 */
export function addFavoriteArtist(artistName) {
    if (typeof window === 'undefined') return { ok: false, reason: 'invalid' }
    const displayName = String(artistName ?? '').trim()
    const norm = normalizeString(displayName)
    if (!norm) return { ok: false, reason: 'invalid' }

    const list = getFavoriteArtists()
    const existing = list.find(item => item.normalized === norm)
    if (existing) {
        return { ok: false, reason: 'duplicate', entry: existing }
    }

    const entry = {
        normalized: norm,
        display_name: displayName,
        created_at: new Date().toISOString(),
    }
    list.push(entry)
    if (!saveFavoriteArtists(list)) {
        return { ok: false, reason: 'storage_error' }
    }
    return { ok: true, entry }
}

/**
 * Edit an existing favorite artist's display name.
 * Returns { ok: true, entry } or { ok: false, reason: 'invalid' | 'not_found' | 'duplicate' | 'storage_error' }.
 */
export function updateFavoriteArtist(originalArtist, nextArtistName) {
    if (typeof window === 'undefined') return { ok: false, reason: 'invalid' }
    const displayName = String(nextArtistName ?? '').trim()
    const nextNorm = normalizeString(displayName)
    if (!nextNorm) return { ok: false, reason: 'invalid' }

    const origNorm = normalizeString(originalArtist)
    if (!origNorm) return { ok: false, reason: 'not_found' }
    const list = getFavoriteArtists()
    const index = list.findIndex(item => item.normalized === origNorm)
    if (index < 0) return { ok: false, reason: 'not_found' }

    const duplicate = list.some((item, idx) => idx !== index && item.normalized === nextNorm)
    if (duplicate) return { ok: false, reason: 'duplicate' }

    const updated = {
        ...list[index],
        normalized: nextNorm,
        display_name: displayName,
    }
    list[index] = updated
    if (!saveFavoriteArtists(list)) {
        return { ok: false, reason: 'storage_error' }
    }
    return { ok: true, entry: updated }
}

/**
 * Remove a favorite artist by name.
 * Returns true if removed and persisted, false if not found or storage failed.
 */
export function removeFavoriteArtist(artist) {
    if (typeof window === 'undefined') return false
    const norm = normalizeString(artist)
    if (!norm) return false
    const list = getFavoriteArtists()
    const index = list.findIndex(item => item.normalized === norm)
    if (index < 0) return false
    list.splice(index, 1)
    return saveFavoriteArtists(list)
}

/**
 * Return total counts of saved favorite songs and artists.
 */
export function getFavoritesCount() {
    const songs = getFavoriteSongs().length
    const artists = getFavoriteArtists().length
    return { songs, artists, total: songs + artists }
}

export const setSongPreferredKey = updatePreferredKey

export function clearFavoritesOnLogout() {
    if (syncTimer) {
        clearTimeout(syncTimer)
        syncTimer = null
    }
    if (typeof localStorage !== 'undefined') {
        localStorage.removeItem(SONGS_KEY)
        localStorage.removeItem(ARTISTS_KEY)
    }
    notifyChange()
}

export function toEpochMs(val) {
    if (typeof val === 'number' && Number.isFinite(val)) return Math.trunc(val)
    if (!val) return Date.now()
    const parsed = new Date(val).getTime()
    return Number.isFinite(parsed) ? parsed : Date.now()
}

export async function pushFavoritesToAccount() {
    if (!user?.value) return
    const songs = getFavoriteSongs()
    const artists = getFavoriteArtists()
    const payload = {
        songs: songs.map(s => ({
            artist: s.display_artist || s.artist,
            title: s.display_title || s.title,
            preferred_key: clampPreferredKey(s.preferred_key),
            ...(s.canonical_id ? { canonical_id: s.canonical_id } : {}),
            added_at: toEpochMs(s.added_at || s.created_at),
        })),
        artists: artists.map(a => ({
            name: a.display_name || a.name || a.normalized,
            added_at: toEpochMs(a.added_at || a.created_at),
        })),
    }
    return api.put('/auth/favorites', payload)
}

export function scheduleDebouncedAccountSync(delayMs = 300) {
    if (isSyncing || !user?.value) return
    if (syncTimer) clearTimeout(syncTimer)
    syncTimer = setTimeout(() => {
        syncTimer = null
        pushFavoritesToAccount().catch(err => {
            console.warn('[Favorites Sync] debounced put failed:', err)
        })
    }, delayMs)
}

export async function flushFavoritesSync() {
    if (syncTimer) {
        clearTimeout(syncTimer)
        syncTimer = null
        return pushFavoritesToAccount()
    }
}

function toSongEntry(item, fallbackTime = 0) {
    const time = toEpochMs(item.added_at || item.created_at || fallbackTime)
    return {
        artist: normalizeString(item.artist),
        title: normalizeString(item.title),
        display_artist: item.display_artist || item.artist,
        display_title: item.display_title || item.title,
        canonical_id: item.canonical_id || null,
        preferred_key: clampPreferredKey(item.preferred_key),
        added_at: time,
        created_at: time,
    }
}

function toArtistEntry(item, fallbackTime = 0) {
    const time = toEpochMs(item.added_at || item.created_at || fallbackTime)
    return {
        normalized: item.normalized || normalizeString(item.name || item.display_name),
        display_name: item.display_name || item.name || item.normalized,
        added_at: time,
        created_at: time,
    }
}

export async function syncFavoritesOnLogin() {
    if (typeof window === 'undefined') return
    isSyncing = true
    try {
        const res = await api.get('/auth/favorites')
        const remoteSongs = Array.isArray(res?.songs) ? res.songs : []
        const remoteArtists = Array.isArray(res?.artists) ? res.artists : []
        const localSongs = getFavoriteSongs()
        const localArtists = getFavoriteArtists()

        let hasNewLocal = false
        const matchedLocalIndices = new Set()

        const mergedSongs = remoteSongs.map(rem => {
            const remKey = songKey(rem.artist, rem.title)
            const localIdx = localSongs.findIndex(loc => (rem.canonical_id && loc.canonical_id && rem.canonical_id === loc.canonical_id) || songKey(loc.artist, loc.title) === remKey)
            if (localIdx >= 0) {
                matchedLocalIndices.add(localIdx)
                const loc = localSongs[localIdx]
                const localNewer = toEpochMs(loc.added_at || loc.created_at) > toEpochMs(rem.added_at)
                return toSongEntry({
                    ...rem,
                    display_artist: loc.display_artist || rem.artist,
                    display_title: loc.display_title || rem.title,
                    canonical_id: rem.canonical_id || loc.canonical_id,
                    preferred_key: localNewer ? loc.preferred_key : rem.preferred_key,
                }, localNewer ? (loc.added_at || loc.created_at) : rem.added_at)
            }
            return toSongEntry(rem)
        })

        localSongs.forEach((loc, idx) => {
            if (!matchedLocalIndices.has(idx)) {
                hasNewLocal = true
                mergedSongs.push(toSongEntry(loc))
            }
        })

        const matchedLocalArtistIndices = new Set()
        const mergedArtists = remoteArtists.map(rem => {
            const norm = normalizeString(rem.name)
            const localIdx = localArtists.findIndex(loc => loc.normalized === norm || normalizeString(loc.display_name) === norm)
            if (localIdx >= 0) {
                matchedLocalArtistIndices.add(localIdx)
                const loc = localArtists[localIdx]
                return toArtistEntry({ ...rem, display_name: loc.display_name || rem.name }, loc.added_at || loc.created_at)
            }
            return toArtistEntry(rem)
        })

        localArtists.forEach((loc, idx) => {
            if (!matchedLocalArtistIndices.has(idx)) {
                hasNewLocal = true
                mergedArtists.push(toArtistEntry(loc))
            }
        })

        localStorage.setItem(SONGS_KEY, JSON.stringify(mergedSongs))
        localStorage.setItem(ARTISTS_KEY, JSON.stringify(mergedArtists))
        notifyChange()

        if (hasNewLocal) await pushFavoritesToAccount()
    } finally {
        isSyncing = false
    }
}
