import test from 'node:test'
import assert from 'node:assert/strict'
import { api, setTokens, clearTokens, clearDeletionRecoveryToken } from './client.js'

test('api.delete(/auth/me) captures X-Deletion-Token on partial 500 failure and forwards it on retry', async () => {
    clearTokens()
    clearDeletionRecoveryToken()
    setTokens('initial-access-token')

    const calls = []
    const originalFetch = globalThis.fetch
    globalThis.fetch = async (url, opts) => {
        calls.push({ url, opts: { ...opts, headers: { ...opts.headers } } })
        if (calls.length === 1) {
            return {
                ok: false,
                status: 500,
                headers: {
                    get: (name) => (name === 'X-Deletion-Token' ? 'del-recovery-jwt-1' : null),
                },
                text: async () => JSON.stringify({
                    detail: 'account deletion incomplete; retry to finish cleanup',
                    step: 'delete_events',
                    deleting: true,
                    deletion_token: 'del-recovery-jwt-1',
                }),
            }
        }
        return {
            ok: true,
            status: 204,
            headers: {
                get: () => null,
            },
            text: async () => '',
        }
    }

    try {
        await assert.rejects(
            () => api.delete('/auth/me', { confirm: 'DELETE' }),
            (err) => err.status === 500 && err.data?.step === 'delete_events'
        )

        // Simulate revoked session / cleared access token before retry
        clearTokens()

        const result = await api.delete('/auth/me', { confirm: 'DELETE' })
        assert.equal(result, null)
        assert.equal(calls.length, 2)
        assert.equal(calls[1].opts.headers['X-Deletion-Token'], 'del-recovery-jwt-1')
    } finally {
        globalThis.fetch = originalFetch
        clearTokens()
        clearDeletionRecoveryToken()
    }
})

for (const tokenSource of ['header', 'body']) {
    test(`account deletion saves the recovery token from the ${tokenSource} after refreshing`, async (t) => {
        setTokens('expired-access-token')
        t.after(() => {
            clearTokens()
            clearDeletionRecoveryToken()
        })

        const recoveryToken = 'deletion-recovery-token'
        const failure = { detail: 'account deletion incomplete', step: 'delete_events', deleting: true }
        if (tokenSource === 'body') failure.deletion_token = recoveryToken

        const responses = [
            Response.json({ detail: 'unauthorized' }, { status: 401 }),
            Response.json({ access_token: 'refreshed-access-token' }),
            Response.json(failure, {
                status: 500,
                headers: tokenSource === 'header' ? { 'X-Deletion-Token': recoveryToken } : {},
            }),
            new Response(null, { status: 204, headers: { 'X-Deletion-Token': recoveryToken } }),
            new Response(null, { status: 204 }),
        ]
        const calls = []
        t.mock.method(globalThis, 'fetch', async (url, opts) => {
            calls.push({ url, opts: { ...opts, headers: { ...opts.headers } } })
            return responses.shift()
        })

        await assert.rejects(
            () => api.delete('/auth/me', { confirm: 'DELETE' }),
            { status: 500, data: failure },
        )

        clearTokens()
        assert.equal(await api.delete('/auth/me', { confirm: 'DELETE' }), null)
        assert.equal(await api.delete('/auth/me', { confirm: 'DELETE' }), null)

        assert.deepEqual(calls.map(({ url, opts }) => [
            url,
            opts.method,
            opts.headers.Authorization,
            opts.headers['X-Deletion-Token'],
            opts.body,
        ]), [
            ['/api/v1/auth/me', 'DELETE', 'Bearer expired-access-token', undefined, '{"confirm":"DELETE"}'],
            ['/api/v1/auth/refresh', 'POST', undefined, undefined, undefined],
            ['/api/v1/auth/me', 'DELETE', 'Bearer refreshed-access-token', undefined, '{"confirm":"DELETE"}'],
            ['/api/v1/auth/me', 'DELETE', undefined, recoveryToken, '{"confirm":"DELETE"}'],
            ['/api/v1/auth/me', 'DELETE', undefined, undefined, '{"confirm":"DELETE"}'],
        ])
    })
}

test('api.put dispatches PUT request with serialized body', async () => {
    let capturedMethod = ''
    let capturedUrl = ''
    let capturedBody = ''
    const origFetch = globalThis.fetch

    globalThis.fetch = async (url, opts) => {
        capturedUrl = url
        capturedMethod = opts.method
        capturedBody = opts.body
        return {
            ok: true,
            status: 200,
            headers: { get: () => 'application/json' },
            json: async () => ({ ok: true }),
        }
    }

    try {
        const res = await api.put('/auth/favorites', { songs: [], artists: [] })
        assert.equal(capturedMethod, 'PUT')
        assert.equal(capturedUrl, '/api/v1/auth/favorites')
        assert.equal(capturedBody, '{"songs":[],"artists":[]}')
        assert.deepEqual(res, { ok: true })
    } finally {
        globalThis.fetch = origFetch
    }
})
