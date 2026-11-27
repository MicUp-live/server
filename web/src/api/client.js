/**
 * API client — fetch wrapper with automatic auth headers.
 *
 * Usage:
 *   import { api } from './api/client'
 */

const API_BASE = import.meta.env?.VITE_API_URL || '/api/v1'

let accessToken = null
let deletionRecoveryToken = null
let onTokenRefresh = null
let onTokenInvalidated = null
let refreshPromise = null

/** Set tokens after login. */
export function setTokens(access) {
    accessToken = access
    deletionRecoveryToken = null
}

/** Clear tokens on logout. */
export function clearTokens() {
    accessToken = null
}

/** Clear any stored account-deletion recovery token. */
export function clearDeletionRecoveryToken() {
    deletionRecoveryToken = null
}

/** Register a callback invoked after a silent token refresh succeeds. */
export function setOnTokenRefresh(callback) {
    onTokenRefresh = callback
}

/** Register a callback invoked when the refresh token is rejected. */
export function setOnTokenInvalidated(callback) {
    onTokenInvalidated = callback
}

/**
 * Make an authenticated API request.
 * Automatically includes Authorization header if logged in.
 */
async function request(method, path, body = null, extraHeaders = null) {
    const headers = { 'Content-Type': 'application/json', ...extraHeaders }
    const isDeleteAccount = method === 'DELETE' && path === '/auth/me'

    if (accessToken) {
        headers['Authorization'] = `Bearer ${accessToken}`
    }
    if (isDeleteAccount && deletionRecoveryToken) {
        headers['X-Deletion-Token'] = deletionRecoveryToken
    }

    const opts = { method, headers }
    if (body && method !== 'GET') {
        opts.body = JSON.stringify(body)
    }

    const url = `${API_BASE}${path}`
    let res = await fetch(url, opts)

    // Try auto-refresh on 401
    const canRecoverDeletion = isDeleteAccount && (deletionRecoveryToken || res.headers?.get?.('X-Deletion-Token'))
    if (res.status === 401 && !canRecoverDeletion) {
        const refreshed = await tryRefresh()
        if (refreshed) {
            headers['Authorization'] = `Bearer ${accessToken}`
            res = await fetch(url, opts)
        }
    }

    if (!res.ok) {
        if (isDeleteAccount) {
            const headerDeletionToken = res.headers?.get?.('X-Deletion-Token')
            if (headerDeletionToken) deletionRecoveryToken = headerDeletionToken
        }
        const err = await readError(res)
        if (isDeleteAccount && err?.deletion_token) {
            deletionRecoveryToken = err.deletion_token
        }
        console.error(`[MicUp API] ${method} ${path} -> ${res.status}:`, isDeleteAccount ? err?.detail : err)
        throw new ApiError(res.status, err)
    }

    if (isDeleteAccount) {
        deletionRecoveryToken = null
    }
    if (res.status === 204) return null
    return res.json()
}

async function readError(response) {
    const text = await response.text()
    if (!text) return { detail: 'Request failed' }
    try {
        return JSON.parse(text)
    } catch {
        return { detail: text.trim() || 'Request failed' }
    }
}

/** Attempt to refresh the access token. */
function tryRefresh() {
    if (!refreshPromise) {
        refreshPromise = refreshAccessToken().finally(() => {
            refreshPromise = null
        })
    }
    return refreshPromise
}

/** Restore an access token from the HttpOnly cookie without probing an API route first. */
export function restoreAccessToken() {
    return tryRefresh()
}

async function refreshAccessToken() {
    try {
        const res = await fetch(`${API_BASE}/auth/refresh`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
        })
        if (!res.ok) {
            if (res.status === 400 || res.status === 401) {
                clearTokens()
                if (onTokenInvalidated) {
                    onTokenInvalidated()
                }
            }
            return false
        }
        const data = await res.json()
        accessToken = data.access_token

        if (onTokenRefresh) {
            onTokenRefresh(data.access_token)
        }
        return true
    } catch {
        return false
    }
}

/** Custom error class for API errors. */
export class ApiError extends Error {
    constructor(status, data) {
        super(data?.detail || `HTTP ${status}`)
        this.status = status
        this.data = data
    }
}

/** Convenience API object. */
export const api = {
    get: (path) => request('GET', path),
    post: (path, body, headers) => request('POST', path, body, headers),
    put: (path, body, headers) => request('PUT', path, body, headers),
    patch: (path, body, headers) => request('PATCH', path, body, headers),
    delete: (path, body, headers) => request('DELETE', path, body, headers),
}
