/**
 * Auth state store — reactive auth state using Preact signals.
 *
 * Manages user session and auth state. Refresh tokens are HttpOnly cookies.
 * that components can reactively subscribe to.
 */

import { signal, computed } from '@preact/signals'
import {
    api,
    clearTokens,
    setOnTokenInvalidated,
    setOnTokenRefresh,
    setTokens,
    restoreAccessToken,
} from '../api/client.js'
import { syncFavoritesOnLogin, clearFavoritesOnLogout } from '../utils/favoritesStore.js'

const SESSION_HINT_KEY = 'micup_has_session'

function hasSessionHint() {
    try {
        return typeof localStorage !== 'undefined' && localStorage.getItem(SESSION_HINT_KEY) === '1'
    } catch {
        return false
    }
}

function setSessionHint(active) {
    try {
        if (typeof localStorage === 'undefined') return
        if (active) {
            localStorage.setItem(SESSION_HINT_KEY, '1')
        } else {
            localStorage.removeItem(SESSION_HINT_KEY)
        }
    } catch {
        // Ignore storage errors (e.g. private browsing restrictions)
    }
}

// --- Reactive state ---
export const user = signal(null)        // { id, display_name, avatar_url, email, social_provider, is_host }
export const loading = signal(true)     // Initial session check
export const isLoggedIn = computed(() => !!user.value)
export const isHost = computed(() => !!user.value?.is_host)

setOnTokenRefresh(() => {})
setOnTokenInvalidated(() => clearAuthState())

function clearAuthState() {
    clearTokens()
    setSessionHint(false)
    user.value = null
    clearFavoritesOnLogout()
}

/** Restore a session using the HttpOnly refresh cookie when a prior session hint exists. */
export async function restoreSession() {
    if (!hasSessionHint()) {
        loading.value = false
        return
    }
    loading.value = true
    try {
        const refreshed = await restoreAccessToken()
        if (!refreshed) {
            return
        }
        const profile = await api.get('/auth/me')
        user.value = profile
        setSessionHint(true)
        syncFavoritesOnLogin().catch(err => console.warn('[Favorites Sync] failed to sync:', err))
    } catch {
        // Invalid refreshes clear auth through the callback; transient failures preserve it.
    } finally {
        loading.value = false
    }
}

// --- Auth actions ---

/** Register with email + password. */
export async function register(email, password, displayName, isHost = true) {
    const tokens = await api.post('/auth/register', {
        email,
        password,
        display_name: displayName || '',
        is_host: Boolean(isHost),
    })

    setTokens(tokens.access_token)
    setSessionHint(true)

    const profile = await api.get('/auth/me')
    user.value = profile
    syncFavoritesOnLogin().catch(err => console.warn('[Favorites Sync] failed to sync:', err))

    return profile
}

/** Login with email + password. */
export async function passwordLogin(email, password) {
    const tokens = await api.post('/auth/login', {
        email,
        password,
    })

    setTokens(tokens.access_token)
    setSessionHint(true)

    const profile = await api.get('/auth/me')
    user.value = profile
    syncFavoritesOnLogin().catch(err => console.warn('[Favorites Sync] failed to sync:', err))

    return profile
}

/** Logout — clear everything. */
export async function logout() {
    try {
        await api.post('/auth/logout')
    } catch {
        // Ignore errors — we're logging out regardless
    }
    clearAuthState()
}

// --- Passkey / WebAuthn actions ---

import {
    isPasskeySupported,
    prepareAssertionOptions,
    prepareCreationOptions,
    assertionToJSON,
    credentialToJSON,
} from '../utils/webauthn.js'

/**
 * 1-Tap or email-directed Passkey Login.
 * Prompts user for Touch ID, Face ID, Windows Hello, or hardware key.
 */
export async function loginWithPasskey(email = '') {
    if (!isPasskeySupported()) {
        throw new Error('Passkeys are not supported in this browser.')
    }

    const { options, session_id } = await api.post('/auth/passkey/login-options', {
        email: email || undefined,
    })

    const assertionOptions = prepareAssertionOptions(options)
    let assertion
    try {
        assertion = await navigator.credentials.get({
            publicKey: assertionOptions,
        })
    } catch (err) {
        if (err.name === 'NotAllowedError' || err.name === 'AbortError') {
            throw new Error('Passkey sign-in cancelled or timed out.')
        }
        throw err
    }

    if (!assertion) {
        throw new Error('Passkey authentication failed: no response from authenticator.')
    }

    const payload = assertionToJSON(assertion)
    const loginRes = await api.post(`/auth/passkey/login-verify?session_id=${encodeURIComponent(session_id)}`, payload)

    setTokens(loginRes.access_token)
    setSessionHint(true)

    let profile = loginRes.user
    if (!profile) {
        try {
            profile = await api.get('/auth/me')
        } catch (e) {
            console.warn('[MicUp Auth] Failed to fetch /auth/me after passkey login:', e)
        }
    }
    user.value = profile
    syncFavoritesOnLogin().catch(err => console.warn('[Favorites Sync] failed to sync:', err))

    return profile
}

/**
 * Register a new Passkey for the current user (or as a new user account).
 */
export async function registerPasskey({ displayName, email, isHost = false, friendlyName = '' } = {}) {
    if (!isPasskeySupported()) {
        throw new Error('Passkeys are not supported in this browser.')
    }

    const { options, session_id } = await api.post('/auth/passkey/register-options', {
        display_name: displayName || undefined,
        email: email || undefined,
        is_host: Boolean(isHost),
    })

    const creationOptions = prepareCreationOptions(options)
    let credential
    try {
        credential = await navigator.credentials.create({
            publicKey: creationOptions,
        })
    } catch (err) {
        if (err.name === 'NotAllowedError' || err.name === 'AbortError') {
            throw new Error('Passkey registration cancelled or timed out.')
        }
        throw err
    }

    if (!credential) {
        throw new Error('Passkey registration failed: no credential created.')
    }

    const payload = credentialToJSON(credential)
    const nameParam = encodeURIComponent(friendlyName || (isHost ? 'KJ Host Passkey' : 'Singer Passkey'))
    const verifyRes = await api.post(
        `/auth/passkey/register-verify?session_id=${encodeURIComponent(session_id)}&friendly_name=${nameParam}`,
        payload
    )

    setTokens(verifyRes.access_token)
    setSessionHint(true)

    let profile = verifyRes.user
    if (!profile) {
        try {
            profile = await api.get('/auth/me')
        } catch (e) {
            console.warn('[MicUp Auth] Failed to fetch /auth/me after passkey registration:', e)
        }
    }
    user.value = profile
    syncFavoritesOnLogin().catch(err => console.warn('[Favorites Sync] failed to sync:', err))

    return profile
}

/**
 * List all registered passkeys for the current user.
 */
export async function listPasskeys() {
    const res = await api.get('/auth/passkeys')
    return res.passkeys || []
}

/**
 * Delete / revoke a passkey credential.
 */
export async function deletePasskey(passkeyId) {
    return await api.delete(`/auth/passkeys/${encodeURIComponent(passkeyId)}`)
}

/**
 * Update profile settings (display name, email, avatar, is_host, password).
 */
export async function updateProfile({ displayName, email, avatarUrl, isHost, password } = {}) {
    const payload = {}
    if (displayName !== undefined) payload.display_name = displayName
    if (email !== undefined) payload.email = email
    if (avatarUrl !== undefined) payload.avatar_url = avatarUrl
    if (isHost !== undefined) payload.is_host = Boolean(isHost)
    if (password !== undefined && password !== '') payload.password = password

    const updatedUser = await api.patch('/auth/me', payload)
    user.value = updatedUser
    return updatedUser
}

/**
 * Toggle between Singer and KJ Host mode.
 */
export async function toggleHostMode() {
    const nextRole = !user.value?.is_host
    return await updateProfile({ isHost: nextRole })
}

/**
 * Request an email verification message.
 */
export async function requestEmailVerification() {
    return await api.post('/auth/verify-email/request')
}

/**
 * Confirm email verification token.
 */
export async function confirmEmailVerification(token) {
    const res = await api.post('/auth/verify-email/confirm', { token })
    if (res?.user) {
        user.value = res.user
    }
    return res
}

async function parseCatalogResponse(catRes) {
    const contentType = catRes.headers.get('content-type') || ''
    if (contentType.includes('json') || typeof DecompressionStream === 'undefined') {
        return await catRes.json()
    }
    const ds = new DecompressionStream('gzip')
    const stream = catRes.body.pipeThrough(ds)
    const text = await new Response(stream).text()
    return JSON.parse(text)
}

function extractSongsList(data) {
    if (Array.isArray(data)) return data
    return data?.songs || data?.tracks || []
}

async function hydrateSongbookCatalog(item) {
    if (!item.catalog_url || item.songs?.length) return
    try {
        const catRes = await fetch(item.catalog_url)
        if (!catRes.ok) return
        const data = await parseCatalogResponse(catRes)
        item.songs = extractSongsList(data)
    } catch (err) {
        console.warn('Could not expand full catalog in data export:', err)
    }
}

async function hydrateExportSongbooks(songbooks) {
    if (!songbooks?.length || typeof fetch === 'undefined') return
    await Promise.all(songbooks.map((item) => hydrateSongbookCatalog(item)))
}

function triggerJsonDownload(data, filename) {
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
}

/**
 * Download complete personal data archive as JSON file.
 */
export async function exportUserData() {
    const res = await api.get('/auth/me/export')
    await hydrateExportSongbooks(res?.songbooks)
    const dateStamp = new Date().toISOString().slice(0, 10)
    triggerJsonDownload(res, `micup-data-export-${dateStamp}.json`)
    return res
}

/**
 * Permanently delete user account and all associated data.
 */
export async function deleteAccount(confirmText) {
    await api.delete('/auth/me', { confirm: confirmText })
    clearAuthState()
}
