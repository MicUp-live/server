import {
    firstAvailableLetter,
    loadCatalogManifest,
    loadCatalogShard,
    createCatalogWorker,
} from '../../../api/catalogAssets.js'
import { showToast } from '../../../components/toastState.js'
import { normalizeString, isFavoriteArtist } from '../../../utils/favoritesStore.js'
import { groupSongVersions } from '../utils/groupSongVersions.js'
import {
    getLetterBucket,
    normalizePublicOverrides,
    applyPublicOverrides,
    loadOverlayOnlyCatalog,
} from '../utils/overlayCatalog.js'

/**
 * Builds the optional query string for public catalog requests.
 */
export function buildCatalogQueryString(eventCode, eventId) {
    const queryParams = new URLSearchParams()
    if (eventCode) queryParams.set('code', eventCode)
    if (eventId) queryParams.set('event', eventId)
    const serialized = queryParams.toString()
    return serialized ? `?${serialized}` : ''
}

/**
 * Loads an overlay-only catalog when static assets are not yet ready.
 */
export async function resolveOverlayCatalog({ api, resolvedSongbookId, qs, resolution, isCancelled, setters }) {
    if (!resolution?.overlay_ready) {
        throw new Error('This catalog is still being prepared. Please try again shortly.')
    }
    const overlay = await loadOverlayOnlyCatalog(api, resolvedSongbookId, qs)
    if (isCancelled()) return
    setters.setPublicOverrides(overlay.loadedOverrides)
    setters.setCatalogName(resolution.name || 'Songbook')
    setters.setAllSongs(overlay.songs)
    setters.setTotalSongs(overlay.totalSongs)
    setters.setActiveLetter(overlay.firstLetter || 'A')
    if (overlay.firstArtistLetter) setters.setActiveArtistLetter(overlay.firstArtistLetter)
}

/**
 * Loads the static manifest, first letter shards, and optional public overrides.
 */
export async function loadStaticInitialShard({ api, resolvedSongbookId, qs, resolution, isCancelled }) {
    const manifest = await loadCatalogManifest(resolution.manifest_url)
    const baseDir = resolution.manifest_url.substring(0, resolution.manifest_url.lastIndexOf('/') + 1)
    manifest._baseDir = baseDir
    const letter = firstAvailableLetter(manifest)
    const shards = (manifest.letters?.[letter]?.shards || []).map(s => baseDir + s)
    const pages = await Promise.all(shards.map(loadCatalogShard))
    if (isCancelled()) return null

    let songs = pages.flat()
    let loadedOverrides = null
    let overridesLoaded = false
    try {
        const rawOverrides = await api.get(`/songbooks/${resolvedSongbookId}/public-overrides${qs}`)
        if (isCancelled()) return null
        loadedOverrides = normalizePublicOverrides(rawOverrides)
        overridesLoaded = true
        songs = applyPublicOverrides(songs, loadedOverrides)
    } catch {
        if (isCancelled()) return null
    }

    songs = songs.sort((a, b) => (a.title || '').localeCompare(b.title || '', undefined, { sensitivity: 'base' }))
    return {
        manifest,
        baseDir,
        letter: letter || 'A',
        songs,
        loadedOverrides,
        overridesLoaded,
    }
}

/**
 * Initializes the background search worker for a static catalog.
 */
export function initCatalogSearchWorker({
    baseDir,
    manifest,
    workerRef,
    isCancelled,
    setWorkerArtists,
    setWorkerReady,
    setIndexing,
}) {
    const worker = createCatalogWorker()
    if (!worker) return
    workerRef.current = worker
    setIndexing(true)
    worker.init(baseDir + (manifest.search_path || 'search.json.gz'))
        .then((res) => {
            if (isCancelled()) return
            setWorkerArtists(res.artists || [])
            setWorkerReady(true)
        })
        .catch((err) => {
            console.error('Failed to init catalog worker:', err)
            if (!isCancelled()) showToast('Full search is temporarily unavailable', 'error')
        })
        .finally(() => {
            if (!isCancelled()) setIndexing(false)
        })
}

function sortArtistGroups(groups) {
    return groups.sort((a, b) => {
        if (a.isFav && !b.isFav) return -1
        if (!a.isFav && b.isFav) return 1
        return a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' })
    })
}

/**
 * Builds artist groups from the search worker's artist index.
 */
export function buildWorkerArtistGroups({
    workerArtists,
    artistSearchResults,
    isSearching,
    activeArtistFilter,
    activeArtistLetter,
    expandedArtistSongs,
}) {
    let list = isSearching ? artistSearchResults : workerArtists
    if (activeArtistFilter) {
        const normFilter = normalizeString(activeArtistFilter)
        list = list.filter(a => a.norm === normFilter)
    } else if (!isSearching) {
        list = list.filter(a => getLetterBucket(a.artist) === activeArtistLetter)
    }
    const groups = list.map(item => ({
        artist: item.artist,
        norm: item.norm,
        count: item.count,
        songs: expandedArtistSongs[item.norm] || [],
        isFav: isFavoriteArtist(item.artist),
        matchedBySong: item.matchedBySong,
    }))
    return isSearching ? groups : sortArtistGroups(groups)
}

/**
 * Builds artist groups directly from loaded song entries.
 */
export function buildSongArtistGroups({
    allSongs,
    searchResults,
    isSearching,
    activeArtistFilter,
    activeArtistLetter,
}) {
    let songsToGroup = isSearching ? searchResults : allSongs
    if (activeArtistFilter) {
        const normFilter = normalizeString(activeArtistFilter)
        songsToGroup = songsToGroup.filter(s => normalizeString(s.artist) === normFilter)
    } else if (!isSearching) {
        songsToGroup = songsToGroup.filter(s => getLetterBucket(s.artist) === activeArtistLetter)
    }

    const map = new Map()
    for (const song of songsToGroup) {
        const artist = (song.artist || 'Unknown Artist').trim()
        const norm = normalizeString(artist)
        if (!map.has(norm)) {
            map.set(norm, {
                artist,
                norm,
                count: 0,
                songs: [],
                isFav: isFavoriteArtist(artist),
            })
        }
        map.get(norm).count++
        map.get(norm).songs.push(song)
    }

    const groups = Array.from(map.values()).map(g => {
        const grouped = groupSongVersions(g.songs)
        return { ...g, songs: grouped, count: grouped.length }
    })
    return isSearching ? groups : sortArtistGroups(groups)
}
