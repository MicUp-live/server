/**
 * Singer requests storage coordinator.
 *
 * Provides synchronous read-modify-write operations against localStorage to avoid
 * the original same-page stale snapshot problem where asynchronous polling loops
 * could overwrite newly submitted requests. Emits CustomEvent ('micup_singer_requests_changed')
 * for same-page reactivity and listens to 'storage' events for multi-tab synchronization.
 *
 * Concurrency boundary: Synchronous read-modify-write prevents same-page asynchronous
 * overwrites, but does not constitute an atomic cross-tab transaction engine.
 */

const SINGER_REQUESTS_EVENT = 'micup_singer_requests_changed'

export function getSingerRequestsKey(targetKey) {
    return `micup_my_requests_${targetKey || ''}`
}

export function getSingerRequests(targetKey) {
    if (!targetKey || typeof window === 'undefined') return []
    try {
        const raw = localStorage.getItem(getSingerRequestsKey(targetKey))
        return raw ? JSON.parse(raw) : []
    } catch {
        return []
    }
}

export function saveSingerRequests(targetKey, requests) {
    if (!targetKey) return []
    const bounded = (requests || []).slice(0, 20)
    if (typeof window !== 'undefined') {
        try {
            localStorage.setItem(getSingerRequestsKey(targetKey), JSON.stringify(bounded))
        } catch (err) {
            console.warn('Failed to save singer requests to localStorage', err)
        }
        window.dispatchEvent(new CustomEvent(SINGER_REQUESTS_EVENT, {
            detail: { targetKey, requests: bounded }
        }))
    }
    return bounded
}

export function addSingerRequest(targetKey, request) {
    if (!targetKey || !request) return []
    const current = getSingerRequests(targetKey)
    const existingIndex = current.findIndex(r => r.id === request.id)
    let next
    if (existingIndex >= 0) {
        next = [...current]
        next[existingIndex] = { ...next[existingIndex], ...request }
    } else {
        next = [request, ...current]
    }
    return saveSingerRequests(targetKey, next)
}

export function updateSingerRequests(targetKey, updaterFn) {
    if (!targetKey) return []
    const current = getSingerRequests(targetKey)
    const next = updaterFn(current)
    return saveSingerRequests(targetKey, next)
}

export function removeSingerRequest(targetKey, reqId) {
    if (!targetKey || !reqId) return getSingerRequests(targetKey)
    return updateSingerRequests(targetKey, (current) => current.filter(r => r.id !== reqId))
}

export function onSingerRequestsChange(targetKey, callback) {
    if (typeof window === 'undefined') return () => {}
    const key = getSingerRequestsKey(targetKey)
    const customHandler = (e) => {
        if (e.detail?.targetKey === targetKey) {
            callback(e.detail.requests)
        }
    }
    const storageHandler = (e) => {
        if (e.key === key) {
            callback(getSingerRequests(targetKey))
        }
    }
    window.addEventListener(SINGER_REQUESTS_EVENT, customHandler)
    window.addEventListener('storage', storageHandler)
    return () => {
        window.removeEventListener(SINGER_REQUESTS_EVENT, customHandler)
        window.removeEventListener('storage', storageHandler)
    }
}
