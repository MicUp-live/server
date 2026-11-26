import test from 'node:test'
import assert from 'node:assert/strict'
import { h, render } from 'preact'
import { reconcileSingerRequests, filterSingerRequests, useSingerQueue } from './useSingerQueue.js'
import { api } from '../../../api/client.js'

// Mock minimal environment for hook lifecycle tests in Node
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

global.document = {
    createElement: () => ({ setAttribute(){}, appendChild(){}, style:{} }),
    createElementNS: () => ({ setAttribute(){}, appendChild(){}, style:{} }),
    createTextNode: () => ({}),
    documentElement: { nodeType: 1 },
    addEventListener: () => {},
    removeEventListener: () => {},
    hidden: false,
}

test('filterSingerRequests separates active, completed, and declined requests', () => {
    const requests = [
        { id: '1', status: 'pending', song_title: 'Song 1' },
        { id: '2', status: 'accepted', song_title: 'Song 2' },
        { id: '3', status: 'up_next', song_title: 'Song 3' },
        { id: '4', status: 'completed', song_title: 'Song 4' },
        { id: '5', status: 'rejected', song_title: 'Song 5' },
        { id: '6', status: 'cancelled', song_title: 'Song 6' },
    ]

    const { active, completed, declined } = filterSingerRequests(requests)

    assert.equal(active.length, 3)
    assert.deepEqual(active.map(r => r.id), ['1', '2', '3'])

    assert.equal(completed.length, 1)
    assert.equal(completed[0].id, '4')

    assert.equal(declined.length, 1)
    assert.equal(declined[0].id, '5')
})

test('reconcileSingerRequests updates status and round from public queue', () => {
    const prev = [
        { id: 'req-1', status: 'pending', round: 1 },
        { id: 'req-2', status: 'accepted', round: 1 },
    ]
    const queue = [
        { id: 'req-1', status: 'accepted', round: 2 },
        { id: 'req-2', status: 'up_next', round: 1 },
    ]

    const updated = reconcileSingerRequests(prev, queue)

    assert.equal(updated[0].status, 'accepted')
    assert.equal(updated[0].round, 2)
    assert.equal(updated[1].status, 'up_next')
})

test('reconcileSingerRequests marks missing up_next and accepted requests as completed', () => {
    const prev = [
        { id: 'req-1', status: 'up_next' },
        { id: 'req-2', status: 'accepted' },
        { id: 'req-3', status: 'pending' },
    ]
    const queue = [
        // Both req-1 and req-2 have fallen off the live queue
    ]

    const updated = reconcileSingerRequests(prev, queue)

    assert.equal(updated[0].status, 'completed')
    assert.ok(updated[0].completed_at)

    assert.equal(updated[1].status, 'completed')
    assert.ok(updated[1].completed_at)

    // Pending requests remain pending while waiting for host decision
    assert.equal(updated[2].status, 'pending')
})

test('regression: out-of-band submission while closed is retained and survives polling', async () => {
    storage.clear()
    const targetKey = 'test-event-1'
    const storageKey = `micup_my_requests_${targetKey}`

    const origGet = api.get
    api.get = async () => ({ queue: [], current_round: 1 })

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ isOpen }) {
        hookResult = useSingerQueue({ eventId: targetKey, isOpen })
        return null
    }

    try {
        // 1. Initial mount while closed (isOpen = false)
        render(h(TestComponent, { isOpen: false }), root)
        assert.equal(hookResult.myRequests.length, 0)

        // 2. Submission occurs out-of-band (SongRequestModal writes to localStorage)
        const newReq = {
            id: 'req-new-1',
            cancel_token: 'secret-cancellation-token-1',
            song_title: 'Bohemian Rhapsody',
            status: 'pending',
        }
        storage.set(storageKey, JSON.stringify([newReq]))

        // 3. User opens Live Queue drawer (isOpen = true)
        render(h(TestComponent, { isOpen: true }), root)
        await new Promise(r => setTimeout(r, 50))

        // Newly submitted request must be visible in hook state
        assert.equal(hookResult.myRequests.length, 1, 'Newly submitted request must be visible in hook state upon drawer open')
        assert.equal(hookResult.myRequests[0].id, 'req-new-1')
        assert.equal(hookResult.myRequests[0].cancel_token, 'secret-cancellation-token-1')

        // 4. Polling returns empty public queue (host has not accepted yet)
        // Verify storage was not overwritten with empty array
        const stored = JSON.parse(storage.get(storageKey) || '[]')
        assert.equal(stored.length, 1, 'localStorage must retain the pending request and cancel token after polling')
        assert.equal(stored[0].cancel_token, 'secret-cancellation-token-1')
    } finally {
        // Cleanup Preact tree and restore api
        render(null, root)
        api.get = origGet
    }
})

test('cancellation tokens survive polling and drawer close/reopen cycle', async () => {
    storage.clear()
    const targetKey = 'test-event-reopen'
    const storageKey = `micup_my_requests_${targetKey}`

    const origGet = api.get
    api.get = async () => ({ queue: [], current_round: 1 })

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ isOpen }) {
        hookResult = useSingerQueue({ eventId: targetKey, isOpen })
        return null
    }

    try {
        const req = {
            id: 'req-reopen-1',
            cancel_token: 'secret-token-reopen',
            song_title: '9 to 5',
            status: 'pending',
        }
        storage.set(storageKey, JSON.stringify([req]))

        // Open drawer
        render(h(TestComponent, { isOpen: true }), root)
        await new Promise(r => setTimeout(r, 40))
        assert.equal(hookResult.myRequests.length, 1)
        assert.equal(hookResult.myRequests[0].cancel_token, 'secret-token-reopen')

        // Close drawer
        render(h(TestComponent, { isOpen: false }), root)
        await new Promise(r => setTimeout(r, 20))

        // Reopen drawer
        render(h(TestComponent, { isOpen: true }), root)
        await new Promise(r => setTimeout(r, 40))

        assert.equal(hookResult.myRequests.length, 1)
        assert.equal(hookResult.myRequests[0].cancel_token, 'secret-token-reopen')
        const stored = JSON.parse(storage.get(storageKey) || '[]')
        assert.equal(stored.length, 1)
        assert.equal(stored[0].cancel_token, 'secret-token-reopen')
    } finally {
        render(null, root)
        api.get = origGet
    }
})

test('successful cancellation removes request; failed cancellation preserves it and token', async () => {
    storage.clear()
    const targetKey = 'test-event-cancel'
    const storageKey = `micup_my_requests_${targetKey}`

    const origGet = api.get
    const origDelete = api.delete
    api.get = async () => ({ queue: [], current_round: 1 })

    let deleteShouldFail = true
    let deletedUrls = []
    api.delete = async (url) => {
        deletedUrls.push(url)
        if (deleteShouldFail) {
            const err = new Error('Network error')
            err.status = 500
            throw err
        }
        return { success: true }
    }

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ isOpen }) {
        hookResult = useSingerQueue({ eventId: targetKey, isOpen })
        return null
    }

    try {
        const req = {
            id: 'req-to-cancel',
            cancel_token: 'valid-cancel-token',
            song_title: 'Sweet Caroline',
            status: 'pending',
        }
        storage.set(storageKey, JSON.stringify([req]))

        render(h(TestComponent, { isOpen: true }), root)
        await new Promise(r => setTimeout(r, 30))
        assert.equal(hookResult.myRequests.length, 1)

        // 1. Attempt cancellation that FAILS
        deleteShouldFail = true
        await hookResult.cancelMyRequest('req-to-cancel')

        // Must still be present with its token preserved
        assert.equal(hookResult.myRequests.length, 1)
        assert.equal(hookResult.myRequests[0].id, 'req-to-cancel')
        assert.equal(hookResult.myRequests[0].cancel_token, 'valid-cancel-token')
        const storedAfterFail = JSON.parse(storage.get(storageKey) || '[]')
        assert.equal(storedAfterFail.length, 1)
        assert.equal(storedAfterFail[0].cancel_token, 'valid-cancel-token')

        // 2. Attempt cancellation that SUCCEEDS
        deleteShouldFail = false
        await hookResult.cancelMyRequest('req-to-cancel')

        // Must now be removed from both hook state and storage
        assert.equal(hookResult.myRequests.length, 0)
        const storedAfterSuccess = JSON.parse(storage.get(storageKey) || '[]')
        assert.equal(storedAfterSuccess.length, 0)
        assert.ok(deletedUrls[1].includes('cancel_token=valid-cancel-token'))
    } finally {
        render(null, root)
        api.get = origGet
        api.delete = origDelete
    }
})

function deferred() {
    let resolve, reject
    const promise = new Promise((res, rej) => {
        resolve = res
        reject = rej
    })
    return { promise, resolve, reject }
}

test('controlled interleaving: successful cancellation of A after switching to B updates A storage without touching B UI or storage', async () => {
    storage.clear()
    const origGet = api.get
    const origDelete = api.delete
    api.get = async () => ({ queue: [], current_round: 1 })

    const deleteDef = deferred()
    api.delete = async () => deleteDef.promise

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ eventId }) {
        hookResult = useSingerQueue({ eventId, isOpen: true })
        return null
    }

    try {
        storage.set('micup_my_requests_evt-A', JSON.stringify([
            { id: 'req-A', cancel_token: 'tok-A', status: 'pending' }
        ]))
        storage.set('micup_my_requests_evt-B', JSON.stringify([
            { id: 'req-B', cancel_token: 'tok-B', status: 'pending' }
        ]))

        // 1. Mount on Event A
        render(h(TestComponent, { eventId: 'evt-A' }), root)
        assert.equal(hookResult.myRequests.length, 1)
        assert.equal(hookResult.myRequests[0].id, 'req-A')

        // 2. Start cancellation on Event A (promise remains pending)
        const cancelPromise = hookResult.cancelMyRequest('req-A')

        // 3. Switch to Event B while deletion of req-A is in-flight
        render(h(TestComponent, { eventId: 'evt-B' }), root)
        assert.equal(hookResult.myRequests.length, 1)
        assert.equal(hookResult.myRequests[0].id, 'req-B')

        // 4. Resolve A's deletion
        deleteDef.resolve({ success: true })
        await cancelPromise

        // 5. Assert Event B remains completely untouched
        assert.equal(hookResult.myRequests.length, 1, 'Current Event B UI must remain untouched')
        assert.equal(hookResult.myRequests[0].id, 'req-B')
        const storedB = JSON.parse(storage.get('micup_my_requests_evt-B') || '[]')
        assert.equal(storedB.length, 1)
        assert.equal(storedB[0].id, 'req-B')

        // 6. Assert Event A storage reflects successful cancellation
        const storedA = JSON.parse(storage.get('micup_my_requests_evt-A') || '[]')
        assert.equal(storedA.length, 0, 'Originating Event A storage must have req-A removed')
    } finally {
        render(null, root)
        api.get = origGet
        api.delete = origDelete
    }
})

test('controlled interleaving: failed cancellation of A after switching to B preserves A storage and token without touching B', async () => {
    storage.clear()
    const origGet = api.get
    const origDelete = api.delete
    api.get = async () => ({ queue: [], current_round: 1 })

    const deleteDef = deferred()
    api.delete = async () => deleteDef.promise

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ eventId }) {
        hookResult = useSingerQueue({ eventId, isOpen: true })
        return null
    }

    try {
        storage.set('micup_my_requests_evt-A', JSON.stringify([
            { id: 'req-A', cancel_token: 'tok-A', status: 'pending' }
        ]))
        storage.set('micup_my_requests_evt-B', JSON.stringify([
            { id: 'req-B', cancel_token: 'tok-B', status: 'pending' }
        ]))

        // 1. Mount on Event A
        render(h(TestComponent, { eventId: 'evt-A' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-A')

        // 2. Start cancellation on Event A
        const cancelPromise = hookResult.cancelMyRequest('req-A')

        // 3. Switch to Event B
        render(h(TestComponent, { eventId: 'evt-B' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-B')

        // 4. Reject A's deletion (server error)
        deleteDef.reject(new Error('Server error 500'))
        await cancelPromise

        // 5. Assert Event B remains untouched
        assert.equal(hookResult.myRequests[0].id, 'req-B')
        const storedB = JSON.parse(storage.get('micup_my_requests_evt-B') || '[]')
        assert.equal(storedB[0].id, 'req-B')

        // 6. Assert Event A preserves the request and token
        const storedA = JSON.parse(storage.get('micup_my_requests_evt-A') || '[]')
        assert.equal(storedA.length, 1)
        assert.equal(storedA[0].id, 'req-A')
        assert.equal(storedA[0].cancel_token, 'tok-A')
    } finally {
        render(null, root)
        api.get = origGet
        api.delete = origDelete
    }
})

test('controlled interleaving: pending-status resolution for A after switching to B updates A storage without touching B', async () => {
    storage.clear()
    const origGet = api.get
    const pendingRequestedDef = deferred()
    const pendingDef = deferred()

    api.get = async (url) => {
        if (url.includes('/requests/req-A')) {
            pendingRequestedDef.resolve()
            return pendingDef.promise
        }
        return { queue: [], current_round: 1 }
    }

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ eventId }) {
        hookResult = useSingerQueue({ eventId, isOpen: true })
        return null
    }

    try {
        storage.set('micup_my_requests_evt-A', JSON.stringify([
            { id: 'req-A', cancel_token: 'tok-A', status: 'pending' }
        ]))
        storage.set('micup_my_requests_evt-B', JSON.stringify([
            { id: 'req-B', cancel_token: 'tok-B', status: 'pending' }
        ]))

        // 1. Mount on Event A -> queue polling runs and triggers pending check for req-A
        render(h(TestComponent, { eventId: 'evt-A' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-A')

        // Wait until pending check for req-A is deterministically in-flight
        await pendingRequestedDef.promise

        // 2. Switch to Event B while pending check for req-A is in-flight
        render(h(TestComponent, { eventId: 'evt-B' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-B')

        // 3. Resolve pending check: Host declined req-A with a reason
        pendingDef.resolve({ id: 'req-A', status: 'rejected', decline_reason: 'Duplicate song' })
        await pendingDef.promise
        await new Promise(r => queueMicrotask(r))

        // 4. Assert Event B UI and storage are untouched
        assert.equal(hookResult.myRequests[0].id, 'req-B')
        const storedB = JSON.parse(storage.get('micup_my_requests_evt-B') || '[]')
        assert.equal(storedB[0].id, 'req-B')

        // 5. Assert Event A storage captured the rejection and decline reason
        const storedA = JSON.parse(storage.get('micup_my_requests_evt-A') || '[]')
        assert.equal(storedA.length, 1)
        assert.equal(storedA[0].id, 'req-A')
        assert.equal(storedA[0].status, 'rejected')
        assert.equal(storedA[0].decline_reason, 'Duplicate song')
        assert.equal(storedA[0].cancel_token, 'tok-A')
    } finally {
        render(null, root)
        api.get = origGet
    }
})

test('controlled interleaving: switching A -> B -> A before older response resolves updates A UI and storage while keeping B untouched', async () => {
    storage.clear()
    const origGet = api.get
    const origDelete = api.delete
    api.get = async () => ({ queue: [], current_round: 1 })

    const deleteDef = deferred()
    api.delete = async () => deleteDef.promise

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ eventId }) {
        hookResult = useSingerQueue({ eventId, isOpen: true })
        return null
    }

    try {
        storage.set('micup_my_requests_evt-A', JSON.stringify([
            { id: 'req-A', cancel_token: 'tok-A', status: 'pending' }
        ]))
        storage.set('micup_my_requests_evt-B', JSON.stringify([
            { id: 'req-B', cancel_token: 'tok-B', status: 'pending' }
        ]))

        // 1. Mount on Event A
        render(h(TestComponent, { eventId: 'evt-A' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-A')

        // 2. Start cancellation on Event A
        const cancelPromise = hookResult.cancelMyRequest('req-A')

        // 3. Switch to Event B
        render(h(TestComponent, { eventId: 'evt-B' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-B')

        // 4. Switch back to Event A before deleteDef resolves
        render(h(TestComponent, { eventId: 'evt-A' }), root)
        assert.equal(hookResult.myRequests[0].id, 'req-A')

        // 5. Resolve deletion of req-A
        deleteDef.resolve({ success: true })
        await cancelPromise

        // 6. Assert Event B storage was never touched
        const storedB = JSON.parse(storage.get('micup_my_requests_evt-B') || '[]')
        assert.equal(storedB.length, 1)
        assert.equal(storedB[0].id, 'req-B')

        // 7. Assert Event A UI and storage reflect the cancellation
        assert.equal(hookResult.myRequests.length, 0, 'Event A UI must reflect the cancellation')
        const storedA = JSON.parse(storage.get('micup_my_requests_evt-A') || '[]')
        assert.equal(storedA.length, 0, 'Event A storage must reflect the cancellation')
    } finally {
        render(null, root)
        api.get = origGet
        api.delete = origDelete
    }
})

test('useSingerQueue syncs event_status updates from polling and suppresses myUpNext when show ends', async () => {
    storage.clear()
    const origGet = api.get
    let pollResponse = {
        event_id: 'evt-status',
        event_status: 'paused',
        queue_version: 2,
        current_round: 1,
        queue: [{ id: 'req-up', status: 'up_next', round: 1 }],
    }
    api.get = async () => pollResponse

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    const reportedStatuses = []
    let hookResult = null
    function TestComponent({ eventStatus }) {
        hookResult = useSingerQueue({
            eventId: 'evt-status',
            eventStatus,
            isOpen: true,
            onEventStatusChange: (next) => reportedStatuses.push(next),
        })
        return null
    }

    try {
        storage.set('micup_my_requests_evt-status', JSON.stringify([
            { id: 'req-up', status: 'up_next', song_title: 'Don\'t Stop Believin\'' },
        ]))

        render(h(TestComponent, { eventStatus: 'active' }), root)
        await new Promise(r => setTimeout(r, 60))

        assert.equal(hookResult.eventStatus, 'paused')
        assert.deepEqual(reportedStatuses, ['paused'])
        assert.equal(hookResult.myUpNext, true, 'myUpNext remains active while show is paused and queue still runs')

        pollResponse = {
            event_id: 'evt-status',
            event_status: 'ended',
            queue_version: 3,
            current_round: 1,
            queue: [{ id: 'req-up', status: 'up_next', round: 1 }],
        }
        await hookResult.refreshQueue(true)
        await new Promise(r => setTimeout(r, 60))

        assert.equal(hookResult.eventStatus, 'ended')
        assert.equal(reportedStatuses.at(-1), 'ended')
        assert.equal(hookResult.myUpNext, false, 'myUpNext must be suppressed once the show has ended')
    } finally {
        render(null, root)
        api.get = origGet
    }
})

test('no-event state skips dead songbook queue poll and clears noEvent/sets loading when transitioning to an active event', async () => {
    storage.clear()
    const origGet = api.get
    const requestedUrls = []
    let nextPoll = deferred()
    api.get = async (url) => {
        requestedUrls.push(url)
        return nextPoll.promise
    }

    const root = {
        childNodes: [],
        appendChild(c) { this.childNodes.push(c) },
        removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
        insertBefore(c) { this.childNodes.push(c) },
        nodeType: 1,
    }

    let hookResult = null
    function TestComponent({ isOpen, eventId }) {
        hookResult = useSingerQueue({
            songbookId: 'sb-standalone',
            eventId,
            isOpen,
        })
        return null
    }

    try {
        // 1. Open without an eventId -> noEvent=true, loading=false, zero network polls
        render(h(TestComponent, { isOpen: true, eventId: '' }), root)
        await new Promise(r => setTimeout(r, 120))

        assert.equal(hookResult.noEvent, true)
        assert.equal(hookResult.loading, false)
        assert.deepEqual(requestedUrls, [])

        // 2. Transition to an active eventId -> noEvent clears, loading resets while poll is in flight
        render(h(TestComponent, { isOpen: true, eventId: 'evt-joined' }), root)
        await new Promise(r => setTimeout(r, 120))

        assert.equal(hookResult.noEvent, false)
        assert.equal(hookResult.loading, true)
        assert.equal(requestedUrls.length, 1)
        assert.ok(requestedUrls[0].startsWith('/events/evt-joined/queue'))

        // 3. Even if the initial poll fails, noEvent stays false (does not revert to join-code screen)
        nextPoll.reject(new Error('temporary outage'))
        await new Promise(r => setTimeout(r, 120))

        assert.equal(hookResult.noEvent, false)
        assert.equal(hookResult.loading, false)
        assert.equal(hookResult.error, 'Could not refresh live queue')
    } finally {
        render(null, root)
        api.get = origGet
    }
})
