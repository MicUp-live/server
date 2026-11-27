import { firstAvailableLetter, loadCatalogManifest, loadCatalogShard, createCatalogWorker } from '../../../api/catalogAssets.js'
import { sortSongsByTitle, loadOverlayOnlyCatalog } from '../../singer/utils/overlayCatalog.js'

/**
 * Fetch the public event record for the kiosk by join code or event ID.
 */
export async function fetchKioskEvent(api, { id, code }) {
    if (code) {
        return api.get(`/join/${encodeURIComponent(code.toUpperCase())}`)
    }
    if (id) {
        return api.get(`/events/${id}/public`)
    }
    return null
}

/**
 * Build the query string used to resolve the event's public songbook manifest.
 */
export function buildCatalogQuery(ev) {
    const queryParams = new URLSearchParams()
    if (ev.join_code) queryParams.set('code', ev.join_code)
    if (ev.id) queryParams.set('event', ev.id)
    const serialized = queryParams.toString()
    return serialized ? `?${serialized}` : ''
}

/**
 * Load the static catalog manifest and its first available letter shard.
 */
export async function loadStaticCatalogInitial(manifestUrl) {
    const manifest = await loadCatalogManifest(manifestUrl)
    const baseDir = manifestUrl.substring(0, manifestUrl.lastIndexOf('/') + 1)
    manifest._baseDir = baseDir
    const letter = firstAvailableLetter(manifest)
    const shards = (manifest.letters?.[letter]?.shards || []).map(p => baseDir + p)
    const pages = await Promise.all(shards.map(loadCatalogShard))
    const resolvedLetter = letter || 'A'
    return {
        manifest,
        baseDir,
        resolvedLetter,
        songs: sortSongsByTitle(pages.flat()),
        total: manifest.total || 0,
    }
}

/**
 * Resolve either a static catalog manifest or an overlay-only catalog for an event.
 */
export async function resolveKioskCatalog(api, ev) {
    const catalogId = ev.public_songbook_id || ev.songbook_id
    const qs = buildCatalogQuery(ev)
    const resolution = await api.get(`/songbooks/${catalogId}/public-manifest${qs}`)
    if (resolution?.static_ready) {
        const initial = await loadStaticCatalogInitial(resolution.manifest_url)
        return { mode: 'static', initial }
    }
    if (resolution?.overlay_ready) {
        const overlay = await loadOverlayOnlyCatalog(api, catalogId, qs)
        return { mode: 'overlay', overlay }
    }
    throw new Error('This catalog is still being prepared. Please try again shortly.')
}

/**
 * Initialize the background catalog search worker for a static catalog.
 */
export function initKioskSearchWorker({ workerRef, baseDir, searchPath, isCancelled, onReady, onDone }) {
    const worker = createCatalogWorker()
    if (!worker) throw new Error('This device does not support catalog search.')
    workerRef.current = worker
    worker.init(baseDir + (searchPath || 'search.json.gz'))
        .then((res) => {
            if (!isCancelled()) onReady(res.artists || [])
        })
        .catch(console.error)
        .finally(() => {
            if (!isCancelled()) onDone()
        })
}
