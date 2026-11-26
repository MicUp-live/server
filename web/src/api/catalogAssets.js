// Static catalog asset loader for versioned CloudFront catalog objects.

export async function loadCatalogManifest(url) {
    const response = await fetch(url)
    if (!response.ok) throw new Error('Failed to load catalog manifest')
    return response.json()
}

export async function loadCatalogShard(url) {
    const response = await fetch(url)
    if (!response.ok) throw new Error('Failed to load catalog page')
    const data = await response.json()
    return data.songs || []
}

export function firstAvailableLetter(manifest) {
    const letters = Object.keys(manifest?.letters || {})
    return letters.sort((a, b) => a === '#' ? -1 : b === '#' ? 1 : a.localeCompare(b))[0] || null
}

export function createCatalogWorker() {
    if (typeof window === 'undefined' || typeof Worker === 'undefined') {
        return null
    }

    const worker = new Worker(new URL('../search/catalogWorker.js', import.meta.url), { type: 'module' })
    let nextId = 1
    const pendingRequests = new Map()

    worker.onmessage = (e) => {
        const { type, id, ...payload } = e.data
        if (pendingRequests.has(id)) {
            const { resolve, reject } = pendingRequests.get(id)
            pendingRequests.delete(id)
            if (type === 'ERROR') {
                reject(new Error(payload.error || 'Catalog worker error'))
            } else {
                resolve(payload)
            }
        }
    }

    worker.onerror = (err) => {
        for (const { reject } of pendingRequests.values()) {
            reject(err)
        }
        pendingRequests.clear()
    }

    return {
        init(searchUrl) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pendingRequests.set(id, { resolve, reject })
                worker.postMessage({ type: 'INIT', id, searchUrl })
            })
        },
        search(query, limit = 200) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pendingRequests.set(id, { resolve, reject })
                worker.postMessage({ type: 'SEARCH', id, query, limit })
            })
        },
        searchArtists(query, limit = 100) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pendingRequests.set(id, { resolve, reject })
                worker.postMessage({ type: 'SEARCH_ARTISTS', id, query, limit })
            })
        },
        getArtistSongs(artistNorm) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pendingRequests.set(id, { resolve, reject })
                worker.postMessage({ type: 'GET_ARTIST_SONGS', id, artistNorm })
            })
        },
        resolveFavorites(favorites) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pendingRequests.set(id, { resolve, reject })
                worker.postMessage({ type: 'RESOLVE_FAVORITES', id, favorites })
            })
        },
        terminate() {
            worker.terminate()
            pendingRequests.clear()
        }
    }
}
