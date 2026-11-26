import test from 'node:test'
import assert from 'node:assert/strict'
import zlib from 'node:zlib'
import {
    user,
    loading,
    restoreSession,
    passwordLogin,
    logout,
    requestEmailVerification,
    confirmEmailVerification,
    exportUserData,
    deleteAccount,
    updateProfile,
    toggleHostMode,
} from './store.js'

// Setup global mocks for Node environment
const storage = new Map()
global.localStorage = {
    getItem: (k) => storage.get(k) || null,
    setItem: (k, v) => storage.set(k, String(v)),
    removeItem: (k) => storage.delete(k),
    clear: () => storage.clear(),
}

global.document = {
    createElement: (tag) => {
        return {
            tagName: tag,
            href: '',
            download: '',
            click: () => {},
        }
    },
    body: {
        appendChild: () => {},
        removeChild: () => {},
    },
}

global.URL = {
    createObjectURL: () => 'blob:mock-url',
    revokeObjectURL: () => {},
}

global.Blob = class Blob {
    constructor(chunks, options) {
        this.chunks = chunks
        this.options = options
    }
}

test('exportUserData calls /auth/me/export and returns structured export', async () => {
    global.fetch = async (url, opts) => {
        assert.ok(url.endsWith('/auth/me/export'))
        assert.equal(opts.method, 'GET')
        return {
            ok: true,
            status: 200,
            json: async () => ({
                service: 'MicUp.live',
                user: { id: 'u-123', email: 'user@example.com' },
                passkeys: [{ id: 'pk-1', friendly_name: 'Key 1' }],
                songbooks: [],
                events: [],
            }),
        }
    }

    const data = await exportUserData()
    assert.equal(data.service, 'MicUp.live')
    assert.equal(data.user.id, 'u-123')
    assert.equal(data.passkeys[0].friendly_name, 'Key 1')
})

test('deleteAccount calls DELETE /auth/me with confirm parameter and clears auth state', async () => {
    let capturedMethod = ''
    let capturedBody = null
    global.fetch = async (url, opts) => {
        assert.ok(url.endsWith('/auth/me'))
        capturedMethod = opts.method
        capturedBody = JSON.parse(opts.body)
        return {
            ok: true,
            status: 204,
            text: async () => '',
        }
    }

    user.value = { id: 'u-123', display_name: 'Singer' }
    await deleteAccount('DELETE')
    assert.equal(capturedMethod, 'DELETE')
    assert.deepEqual(capturedBody, { confirm: 'DELETE' })
    assert.equal(user.value, null)
})

test('requestEmailVerification calls POST /auth/verify-email/request', async () => {
    global.fetch = async (url, opts) => {
        assert.ok(url.endsWith('/auth/verify-email/request'))
        assert.equal(opts.method, 'POST')
        return {
            ok: true,
            status: 200,
            json: async () => ({ message: 'verification email sent' }),
        }
    }

    const res = await requestEmailVerification()
    assert.equal(res.message, 'verification email sent')
})

test('confirmEmailVerification updates user reactive store with verified status', async () => {
    global.fetch = async (url, opts) => {
        assert.ok(url.endsWith('/auth/verify-email/confirm'))
        assert.equal(opts.method, 'POST')
        const body = JSON.parse(opts.body)
        assert.equal(body.token, 'valid-test-token')
        return {
            ok: true,
            status: 200,
            json: async () => ({
                message: 'Email verified successfully!',
                user: { id: 'u-123', email: 'verified@example.com', email_verified: true },
            }),
        }
    }

    user.value = { id: 'u-123', email: 'verified@example.com', email_verified: false }
    const res = await confirmEmailVerification('valid-test-token')
    assert.equal(res.message, 'Email verified successfully!')
    assert.equal(user.value.email_verified, true)
})

test('exportUserData fetches and inflates static catalog when catalog_url is present', async () => {
    const mockSongs = [
        { title: 'Save Me', artist: 'Jelly Roll' },
        { title: 'Need a Favor', artist: 'Jelly Roll' }
    ]
    const gzipped = zlib.gzipSync(JSON.stringify({ songs: mockSongs }))

    global.fetch = async (url, opts) => {
        if (url.endsWith('/auth/me/export')) {
            return {
                ok: true,
                status: 200,
                json: async () => ({
                    service: 'MicUp.live',
                    user: { id: 'u-123', email: 'user@example.com' },
                    songbooks: [
                        {
                            id: 'sb-1',
                            title: 'Main Songbook',
                            catalog_version_id: 'cat-v1',
                            catalog_url: '/catalogs/cat-v1/search.json.gz',
                            songs: []
                        }
                    ],
                    events: [],
                }),
            }
        }
        if (url === '/catalogs/cat-v1/search.json.gz') {
            return new Response(gzipped, {
                status: 200,
                headers: { 'Content-Type': 'application/gzip' }
            })
        }
        throw new Error('Unexpected fetch: ' + url)
    }

    const data = await exportUserData()
    assert.equal(data.songbooks.length, 1)
    assert.equal(data.songbooks[0].songs.length, 2)
    assert.equal(data.songbooks[0].songs[0].title, 'Save Me')
    assert.equal(data.songbooks[0].songs[1].artist, 'Jelly Roll')
})

test('updateProfile supports is_host to switch between Singer and Host roles', async () => {
    let sentBody = null
    global.fetch = async (url, opts) => {
        assert.ok(url.endsWith('/auth/me'))
        assert.equal(opts.method, 'PATCH')
        sentBody = JSON.parse(opts.body)
        return {
            ok: true,
            status: 200,
            json: async () => ({
                id: 'u-123',
                display_name: 'Karaoke Fan',
                is_host: sentBody.is_host,
            }),
        }
    }

    user.value = { id: 'u-123', display_name: 'Karaoke Fan', is_host: false }
    const res1 = await updateProfile({ isHost: true })
    assert.equal(sentBody.is_host, true)
    assert.equal(res1.is_host, true)
    assert.equal(user.value.is_host, true)

    const res2 = await updateProfile({ isHost: false })
    assert.equal(sentBody.is_host, false)
    assert.equal(res2.is_host, false)
    assert.equal(user.value.is_host, false)
})

test('toggleHostMode flips role back and forth', async () => {
    let sentBody = null
    global.fetch = async (url, opts) => {
        sentBody = JSON.parse(opts.body)
        return {
            ok: true,
            status: 200,
            json: async () => ({
                id: 'u-123',
                is_host: sentBody.is_host,
            }),
        }
    }

    // Singer toggles to Host
    user.value = { id: 'u-123', is_host: false }
    await toggleHostMode()
    assert.equal(sentBody.is_host, true)
    assert.equal(user.value.is_host, true)

    // Host toggles to Singer
    await toggleHostMode()
    assert.equal(sentBody.is_host, false)
    assert.equal(user.value.is_host, false)
})

test('restoreSession skips network calls when no session hint exists', async () => {
    storage.clear()
    user.value = null
    let fetchCalls = 0
    global.fetch = async () => {
        fetchCalls++
        throw new Error('should not fetch without session hint')
    }

    await restoreSession()
    assert.equal(fetchCalls, 0)
    assert.equal(loading.value, false)
    assert.equal(user.value, null)
})

test('restoreSession does not call /auth/me when refresh fails and clears hint', async () => {
    storage.clear()
    storage.set('micup_has_session', '1')
    user.value = null
    const calls = []
    global.fetch = async (url) => {
        calls.push(url)
        return {
            ok: false,
            status: 400,
            text: async () => JSON.stringify({ error: 'refresh token is required' }),
        }
    }

    await restoreSession()
    assert.deepEqual(calls, ['/api/v1/auth/refresh'])
    assert.equal(storage.get('micup_has_session'), undefined)
    assert.equal(loading.value, false)
    assert.equal(user.value, null)
})

test('restoreSession hydrates user when session hint and refresh succeed, and logout clears hint', async () => {
    storage.clear()
    user.value = null
    global.fetch = async (url) => {
        if (url.endsWith('/auth/login')) {
            return {
                ok: true,
                status: 200,
                json: async () => ({ access_token: 'tok-login' }),
            }
        }
        if (url.endsWith('/auth/refresh')) {
            return {
                ok: true,
                status: 200,
                json: async () => ({ access_token: 'tok-refreshed' }),
            }
        }
        if (url.endsWith('/auth/me')) {
            return {
                ok: true,
                status: 200,
                json: async () => ({ id: 'u-1', display_name: 'Host', is_host: true }),
            }
        }
        if (url.endsWith('/auth/logout')) {
            return { ok: true, status: 204, text: async () => '' }
        }
        throw new Error(`unexpected fetch: ${url}`)
    }

    await passwordLogin('host@example.com', 'password123')
    assert.equal(storage.get('micup_has_session'), '1')
    assert.equal(user.value?.id, 'u-1')

    user.value = null
    await restoreSession()
    assert.equal(loading.value, false)
    assert.equal(user.value?.id, 'u-1')

    await logout()
    assert.equal(storage.get('micup_has_session'), undefined)
    assert.equal(user.value, null)
})

