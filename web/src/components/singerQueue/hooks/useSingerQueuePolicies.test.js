import test from 'node:test'
import assert from 'node:assert/strict'
import { h, render } from 'preact'
import { reconcileSingerRequests, useSingerQueue } from './useSingerQueue.js'
import { api } from '../../../api/client.js'

const storage = new Map()
global.localStorage = {
    getItem: (k) => storage.get(k) || null,
    setItem: (k, v) => storage.set(k, String(v)),
    removeItem: (k) => storage.delete(k),
    clear: () => storage.clear(),
}

const listeners = new Map()
global.window = {
    addEventListener: (type, fn) => {
        if (!listeners.has(type)) listeners.set(type, [])
        listeners.get(type).push(fn)
    },
    removeEventListener: (type, fn) => {
        if (!listeners.has(type)) return
        listeners.set(type, listeners.get(type).filter(l => l !== fn))
    },
    dispatchEvent: (event) => {
        const list = listeners.get(event.type) || []
        for (const fn of list) fn(event)
        return true
    },
    CustomEvent: class CustomEvent {
        constructor(type, init = {}) {
            this.type = type
            this.detail = init.detail
        }
    },
}

global.document = {
    createElement: () => ({ setAttribute() {}, appendChild() {}, style: {} }),
    createElementNS: () => ({ setAttribute() {}, appendChild() {}, style: {} }),
    createTextNode: () => ({}),
    documentElement: { nodeType: 1 },
    addEventListener: () => {},
    removeEventListener: () => {},
    hidden: false,
}

test('reconcileSingerRequests syncs replaced song title, artist, and key_change', () => {
    const prev = [
        { id: 'req-1', status: 'accepted', round: 1, song_title: 'Old Song', song_artist: 'Old Artist', key_change: 0 },
    ]
    const queue = [
        { id: 'req-1', status: 'accepted', round: 1, song_title: 'New Song', song_artist: 'New Artist', key_change: -2 },
    ]
    const updated = reconcileSingerRequests(prev, queue)
    assert.equal(updated[0].song_title, 'New Song')
    assert.equal(updated[0].song_artist, 'New Artist')
    assert.equal(updated[0].key_change, -2)
})

test('replaceMyRequestSong sends X-Edit-Token and updates stored request in place', async () => {
    storage.clear()
    listeners.clear()

    const eventId = 'evt-hp1'
    storage.set(`micup_my_requests_${eventId}`, JSON.stringify([
        {
            id: 'req-1',
            cancel_token: 'cancel-tok-1',
            edit_token: 'edit-tok-1',
            song_title: 'Original Song',
            song_artist: 'Original Artist',
            key_change: 0,
            status: 'accepted',
        },
    ]))

    const origGet = api.get
    const origPatch = api.patch
    let capturedPatch = null
    api.get = async () => ({
        event_id: eventId,
        event_status: 'active',
        allow_song_replacement: true,
        current_round: 1,
        queue: [{ id: 'req-1', status: 'accepted', round: 1, song_title: 'Original Song', song_artist: 'Original Artist', key_change: 0 }],
    })
    api.patch = async (path, body, headers) => {
        capturedPatch = { path, body, headers }
        return {
            id: 'req-1',
            song_title: body.song_title,
            song_artist: body.song_artist,
            key_change: body.key_change,
            notes: body.notes,
            status: 'accepted',
        }
    }

    let hook = null
    function Harness() {
        hook = useSingerQueue({
            eventId,
            eventStatus: 'active',
            allowSongReplacement: true,
            isOpen: true,
        })
        return null
    }
    const container = { nodeType: 1, childNodes: [], appendChild() {}, removeChild() {}, insertBefore() {} }
    render(h(Harness, {}), container)

    try {
        const res = await hook.replaceMyRequestSong('req-1', {
            song_title: 'Bohemian Rhapsody',
            song_artist: 'Queen',
            key_change: 1,
            notes: 'Swapped!',
        })
        assert.equal(res.ok, true)
        assert.equal(capturedPatch.path, `/events/${eventId}/requests/req-1/song`)
        assert.equal(capturedPatch.headers['X-Edit-Token'], 'edit-tok-1')

        const stored = JSON.parse(storage.get(`micup_my_requests_${eventId}`))
        assert.equal(stored[0].song_title, 'Bohemian Rhapsody')
        assert.equal(stored[0].song_artist, 'Queen')
        assert.equal(stored[0].key_change, 1)
    } finally {
        render(null, container)
        api.get = origGet
        api.patch = origPatch
    }
})
