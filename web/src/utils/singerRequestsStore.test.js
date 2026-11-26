import test from 'node:test'
import assert from 'node:assert/strict'
import {
    getSingerRequestsKey,
    getSingerRequests,
    saveSingerRequests,
    addSingerRequest,
    updateSingerRequests,
    removeSingerRequest,
    onSingerRequestsChange,
} from './singerRequestsStore.js'

// Mock minimal localStorage and window for Node test environment
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
    }
}

test('getSingerRequests returns empty array when empty or invalid', () => {
    storage.clear()
    assert.deepEqual(getSingerRequests(''), [])
    assert.deepEqual(getSingerRequests(null), [])
    assert.deepEqual(getSingerRequests('evt-1'), [])

    storage.set('micup_my_requests_evt-1', 'invalid-json')
    assert.deepEqual(getSingerRequests('evt-1'), [])
})

test('addSingerRequest prepends, bounds to 20, and notifies listeners', () => {
    storage.clear()
    let notified = null
    const unsubscribe = onSingerRequestsChange('evt-1', (reqs) => {
        notified = reqs
    })

    const req1 = { id: 'req-1', song_title: 'First Song', cancel_token: 'tok-1', status: 'pending' }
    const result1 = addSingerRequest('evt-1', req1)
    assert.equal(result1.length, 1)
    assert.equal(result1[0].id, 'req-1')
    assert.equal(notified?.length, 1)

    const req2 = { id: 'req-2', song_title: 'Second Song', cancel_token: 'tok-2', status: 'pending' }
    const result2 = addSingerRequest('evt-1', req2)
    assert.equal(result2.length, 2)
    assert.equal(result2[0].id, 'req-2', 'Newest request should be prepended')
    assert.equal(result2[1].id, 'req-1')

    // Bounding to 20 items
    for (let i = 3; i <= 25; i++) {
        addSingerRequest('evt-1', { id: `req-${i}`, song_title: `Song ${i}` })
    }
    const finalStored = getSingerRequests('evt-1')
    assert.equal(finalStored.length, 20)

    unsubscribe()
})

test('state remains strictly isolated between different events', () => {
    storage.clear()
    addSingerRequest('evt-A', { id: 'req-A', cancel_token: 'token-A', status: 'pending' })
    addSingerRequest('evt-B', { id: 'req-B', cancel_token: 'token-B', status: 'pending' })

    const reqsA = getSingerRequests('evt-A')
    const reqsB = getSingerRequests('evt-B')

    assert.equal(reqsA.length, 1)
    assert.equal(reqsA[0].id, 'req-A')
    assert.equal(reqsA[0].cancel_token, 'token-A')

    assert.equal(reqsB.length, 1)
    assert.equal(reqsB[0].id, 'req-B')
    assert.equal(reqsB[0].cancel_token, 'token-B')

    // Mutating evt-A leaves evt-B unchanged
    removeSingerRequest('evt-A', 'req-A')
    assert.equal(getSingerRequests('evt-A').length, 0)
    assert.equal(getSingerRequests('evt-B').length, 1)
})

test('updateSingerRequests applies commit-time updater preserving racing submissions', () => {
    storage.clear()
    addSingerRequest('evt-1', { id: 'req-1', status: 'pending', cancel_token: 'tok-1' })

    // Simulate async operation: queue reconciliation starts with req-1
    // While in-flight, singer submits req-2
    addSingerRequest('evt-1', { id: 'req-2', status: 'pending', cancel_token: 'tok-2' })

    // When updater commits, it re-reads storage to apply updates without erasing req-2
    updateSingerRequests('evt-1', (current) => {
        return current.map(r => r.id === 'req-1' ? { ...r, status: 'accepted', round: 1 } : r)
    })

    const finalReqs = getSingerRequests('evt-1')
    assert.equal(finalReqs.length, 2)
    const r1 = finalReqs.find(r => r.id === 'req-1')
    const r2 = finalReqs.find(r => r.id === 'req-2')

    assert.equal(r1.status, 'accepted')
    assert.equal(r1.cancel_token, 'tok-1')
    assert.equal(r2.status, 'pending')
    assert.equal(r2.cancel_token, 'tok-2')
})

test('removeSingerRequest removes target and retains others with tokens', () => {
    storage.clear()
    addSingerRequest('evt-1', { id: 'req-1', cancel_token: 'tok-1' })
    addSingerRequest('evt-1', { id: 'req-2', cancel_token: 'tok-2' })

    removeSingerRequest('evt-1', 'req-1')
    const remaining = getSingerRequests('evt-1')
    assert.equal(remaining.length, 1)
    assert.equal(remaining[0].id, 'req-2')
    assert.equal(remaining[0].cancel_token, 'tok-2')
})
