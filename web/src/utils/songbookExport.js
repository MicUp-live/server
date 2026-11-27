import { api } from '../api/client.js'

/**
 * Loads a search asset (e.g. search.json.gz) with automatic handling
 * for gzipped content across browsers and proxies.
 */
async function loadSearchAsset(url) {
    const res = await fetch(url)
    if (!res.ok) return null

    const contentType = res.headers.get('content-type') || ''
    if (contentType.includes('json')) {
        try {
            const clone = res.clone()
            return await clone.json()
        } catch {
            // Direct json parsing failed, may be raw gzipped bytes
            if (typeof DecompressionStream !== 'undefined') {
                try {
                    const ds = new DecompressionStream('gzip')
                    const stream = res.body.pipeThrough(ds)
                    const text = await new Response(stream).text()
                    return JSON.parse(text)
                } catch {
                    return null
                }
            }
        }
    } else if (typeof DecompressionStream !== 'undefined') {
        try {
            const ds = new DecompressionStream('gzip')
            const stream = res.body.pipeThrough(ds)
            const text = await new Response(stream).text()
            return JSON.parse(text)
        } catch {
            return null
        }
    }

    try {
        return await res.json()
    } catch {
        return null
    }
}

function normalizeStaticSong(s) {
    return {
        song_id: s.song_id || s.id,
        artist: s.artist || '',
        title: s.title || '',
        label: s.label || '',
        genre: s.genre || '',
        disc: s.disc || '',
        source: s.source || '',
        video_url: s.video_url || '',
        is_available: true,
    }
}

function normalizeOverrideSong(changedSong) {
    return {
        ...changedSong,
        song_id: changedSong.song_id || changedSong.ID,
        artist: changedSong.artist || changedSong.Artist || '',
        title: changedSong.title || changedSong.Title || '',
        disc: changedSong.disc || changedSong.Disc || '',
        source: changedSong.source || changedSong.Source || '',
        genre: changedSong.genre || changedSong.Genre || '',
        video_url: changedSong.video_url || changedSong.VideoURL || '',
    }
}

function upsertOverrideSong(songs, changedSong) {
    const normalized = normalizeOverrideSong(changedSong)
    const index = songs.findIndex(s => s.song_id === normalized.song_id)
    if (index >= 0) {
        songs[index] = { ...songs[index], ...normalized }
    } else {
        songs.push(normalized)
    }
}

function applyCatalogOverrides(songs, overrides) {
    if (!overrides?.length) return songs
    let result = songs
    for (const change of overrides) {
        if (change.deleted || change.Deleted) {
            const changeID = change.song_id || change.SongID
            result = result.filter(s => s.song_id !== changeID)
            continue
        }
        const changedSong = change.song || change.Song
        if (changedSong) {
            upsertOverrideSong(result, changedSong)
        }
    }
    return result
}

function extractSearchSongs(searchData) {
    if (Array.isArray(searchData)) return searchData
    return searchData.songs || searchData.tracks || []
}

async function loadStaticCatalog(songbook, customApi) {
    try {
        const managed = await customApi.get(`/songbooks/${songbook.id}/managed-manifest`).catch(() => null)
        if (!managed?.static_ready || !managed.manifest_url) return null

        const manifest = await fetch(managed.manifest_url)
            .then(r => (r.ok ? r.json() : null))
            .catch(() => null)
        const searchPath = manifest?.search_path
        if (!searchPath) return null

        const searchUrl = managed.manifest_url.replace(/manifest\.json$/, searchPath)
        const searchData = await loadSearchAsset(searchUrl)
        if (!searchData) return null

        const songs = extractSearchSongs(searchData).map(normalizeStaticSong)
        const overrides = await customApi.get(`/songbooks/${songbook.id}/catalog-overrides`).catch(() => [])
        return applyCatalogOverrides(songs, overrides)
    } catch (err) {
        console.warn('Could not load static catalog for songbook:', err)
        return null
    }
}

async function fetchPaginatedSongs(songbookId, customApi) {
    const all = []
    const pageSize = 5000
    for (let offset = 0; ; offset += pageSize) {
        const page = await customApi.get(`/songbooks/${songbookId}/songs?offset=${offset}`)
        if (page?.length) all.push(...page)
        if (!page || page.length < pageSize) break
    }
    return all
}

function compareSongsByArtistAndTitle(a, b) {
    const aArtist = (a.artist || '').toLowerCase()
    const bArtist = (b.artist || '').toLowerCase()
    if (aArtist !== bArtist) return aArtist.localeCompare(bArtist)
    const aTitle = (a.title || a.label || '').toLowerCase()
    const bTitle = (b.title || b.label || '').toLowerCase()
    return aTitle.localeCompare(bTitle)
}

/**
 * Fetches the complete list of songs for a songbook.
 * Handles S3 static catalogs (manifest + search.json.gz) with catalog overrides,
 * and falls back to paginated API queries across all pages.
 */
export async function fetchAllSongsForSongbook(songbook, customApi = api) {
    let all = await loadStaticCatalog(songbook, customApi)
    if (all === null) {
        all = await fetchPaginatedSongs(songbook.id, customApi)
    }
    all.sort(compareSongsByArtistAndTitle)
    return all
}

/**
 * Formats a list of song objects into RFC-4180 compliant CSV string with UTF-8 BOM.
 */
export function generateSongbookCSV(songList) {
    const csvHeader = 'Artist,Title,Disc,Source,Genre,Video URL\r\n'
    const csvRows = (songList || []).map(s => {
        const escape = val => `"${(val || '').replace(/"/g, '""')}"`
        return `${escape(s.artist)},${escape(s.title || s.label)},${escape(s.disc)},${escape(s.source)},${escape(s.genre)},${escape(s.video_url)}`
    }).join('\r\n')

    return '\uFEFF' + csvHeader + csvRows
}

/**
 * Triggers a client-side file download of CSV content.
 */
export function downloadCSV(csvContent, filename) {
    if (typeof document === 'undefined' || typeof Blob === 'undefined' || typeof URL === 'undefined') {
        return
    }
    const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
}
