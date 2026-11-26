import test from 'node:test'
import assert from 'node:assert/strict'
import { h, render } from 'preact'
import {
    resolveSongbookShowState,
    resolveDirectQueueHandoff,
    useStartShowHandoff,
    useDirectQueueHandoff,
} from './hooks/useStartShowHandoff.js'

// Minimal DOM mock for Preact hook execution in Node
const root = {
    childNodes: [],
    appendChild(c) { this.childNodes.push(c) },
    removeChild(c) { this.childNodes = this.childNodes.filter(x => x !== c) },
    insertBefore(c) { this.childNodes.push(c) },
    nodeType: 1,
}

global.document = {
    createElement: () => ({ setAttribute() {}, appendChild() {}, style: {} }),
    createElementNS: () => ({ setAttribute() {}, appendChild() {}, style: {} }),
    createTextNode: () => ({}),
    documentElement: { nodeType: 1 },
    addEventListener: () => {},
    removeEventListener: () => {},
}

global.window = {
    location: { origin: 'http://localhost:3000', pathname: '/kj/songbooks' },
    addEventListener: () => {},
    removeEventListener: () => {},
}

test('regression 1 (policy): no current show resolves to none status with no live queue', () => {
    const events = [
        { id: 'evt-1', songbook_id: 'sb-other', status: 'active' },
        { id: 'evt-2', songbook_id: 'sb-target', status: 'ended' },
    ]

    const state = resolveSongbookShowState('sb-target', events)
    assert.equal(state.status, 'none')
    assert.equal(state.hasLiveShow, false)
    assert.equal(state.liveEvent, null)
    assert.equal(state.queueUrl, null)
})

test('regression 2 (policy): active and paused shows resolve to live status with correct queue URL', () => {
    const events = [
        { id: 'evt-active', songbook_id: 'sb-active', status: 'active', venue_name: 'Main Stage', join_code: 'ROCK' },
        { id: 'evt-paused', songbook_id: 'sb-paused', status: 'paused', venue_name: 'Side Room', join_code: 'JAZZ' },
    ]

    const activeState = resolveSongbookShowState('sb-active', events)
    assert.equal(activeState.status, 'live')
    assert.equal(activeState.hasLiveShow, true)
    assert.equal(activeState.liveEvent.id, 'evt-active')
    assert.equal(activeState.queueUrl, '/kj/queue?id=evt-active')

    const pausedState = resolveSongbookShowState('sb-paused', events)
    assert.equal(pausedState.status, 'live')
    assert.equal(pausedState.hasLiveShow, true)
    assert.equal(pausedState.liveEvent.id, 'evt-paused')
    assert.equal(pausedState.queueUrl, '/kj/queue?id=evt-paused')
})

test('regression 5 (policy): lookup error or loading state is never treated as no show', () => {
    const events = []

    const errState = resolveSongbookShowState('sb-1', events, { eventsError: '503 Service Unavailable' })
    assert.equal(errState.status, 'error')
    assert.equal(errState.hasLiveShow, false)
    assert.equal(errState.error, '503 Service Unavailable')

    const loadingState = resolveSongbookShowState('sb-1', events, { eventsLoading: true })
    assert.equal(loadingState.status, 'loading')
    assert.equal(loadingState.hasLiveShow, false)
})

test('direct-link handoff resolution in LiveQueue', () => {
    const events = [
        { id: 'evt-live', songbook_id: 'sb-with-live', status: 'active' },
        { id: 'evt-paused', songbook_id: 'sb-with-paused', status: 'paused' },
        { id: 'evt-old', songbook_id: 'sb-ended-only', status: 'ended' },
    ]
    const songbooks = [
        { id: 'sb-with-live', name: 'Live Catalog' },
        { id: 'sb-with-paused', name: 'Paused Catalog' },
        { id: 'sb-ended-only', name: 'Ended Catalog' },
        { id: 'sb-empty', name: 'Empty Catalog' },
    ]

    // 1. Direct link to existing event ID
    const directEvent = resolveDirectQueueHandoff('evt-live', events, songbooks)
    assert.equal(directEvent.action, 'select_event')
    assert.equal(directEvent.eventId, 'evt-live')

    // 2. Direct link to songbook ID with active show
    const directSongbookActive = resolveDirectQueueHandoff('sb-with-live', events, songbooks)
    assert.equal(directSongbookActive.action, 'select_event')
    assert.equal(directSongbookActive.eventId, 'evt-live')

    // 3. Direct link to songbook ID with paused show
    const directSongbookPaused = resolveDirectQueueHandoff('sb-with-paused', events, songbooks)
    assert.equal(directSongbookPaused.action, 'select_event')
    assert.equal(directSongbookPaused.eventId, 'evt-paused')

    // 4. Direct link to songbook ID with NO active show -> open_create with preselected songbook
    const directSongbookEnded = resolveDirectQueueHandoff('sb-ended-only', events, songbooks)
    assert.equal(directSongbookEnded.action, 'open_create')
    assert.equal(directSongbookEnded.songbookId, 'sb-ended-only')

    const directSongbookEmpty = resolveDirectQueueHandoff('sb-empty', events, songbooks)
    assert.equal(directSongbookEmpty.action, 'open_create')
    assert.equal(directSongbookEmpty.songbookId, 'sb-empty')

    // 5. Unspecified ID falls back to active show
    const fallback = resolveDirectQueueHandoff('', events, songbooks)
    assert.equal(fallback.action, 'select_event')
    assert.equal(fallback.eventId, 'evt-live')
})

test('useStartShowHandoff lifecycle: Start Show opens modal with correct songbook, Cancel closes without creation', async () => {
    let hook = null
    function TestComponent() {
        hook = useStartShowHandoff()
        return null
    }

    try {
        render(h(TestComponent), root)
        assert.equal(hook.createModalSongbookId, null)

        // 1. Start Show for sb-42
        hook.startShow('sb-42')
        await new Promise(r => setTimeout(r, 20))
        assert.equal(hook.createModalSongbookId, 'sb-42', 'Must open modal preselecting sb-42')

        // 2. Cancel creation
        hook.cancelCreateShow()
        await new Promise(r => setTimeout(r, 20))
        assert.equal(hook.createModalSongbookId, null, 'Must close modal on cancel without side effects')
    } finally {
        render(null, root)
    }
})

test('useStartShowHandoff lifecycle: successful creation posts show and navigates to new queue', async () => {
    let hook = null
    let routedUrl = null
    let toastMessage = null
    let postedPayload = null

    const mockApi = {
        get: async () => ({ events: [] }),
        post: async (path, body) => {
            if (path === '/events') {
                postedPayload = body
                return {
                    id: 'evt-created-99',
                    join_code: body.join_code,
                    name: body.name,
                    songbook_id: body.songbook_id,
                }
            }
            return {}
        }
    }

    function TestComponent() {
        hook = useStartShowHandoff({
            onNavigate: (url) => { routedUrl = url },
            showToast: (msg) => { toastMessage = msg },
            apiClient: mockApi,
        })
        return null
    }

    try {
        render(h(TestComponent), root)

        // Host opens modal for songbook sb-party
        hook.startShow('sb-party')
        await new Promise(r => setTimeout(r, 20))
        assert.equal(hook.createModalSongbookId, 'sb-party')

        // Host submits form
        const created = await hook.createShow({
            name: 'Saturday Karaoke Bash',
            venue_name: 'The Rusty Nail',
            join_code: 'RUSTY',
            songbook_id: 'sb-party',
            rotation_mode: 'fair_rotation',
            recurring: true,
            auto_accept: true,
            submission_delay: '15',
            max_active_per_singer: '2',
            allow_key_change: true,
        })

        // Verify POST payload normalization
        assert.equal(postedPayload.name, 'Saturday Karaoke Bash')
        assert.equal(postedPayload.venue_name, 'The Rusty Nail')
        assert.equal(postedPayload.join_code, 'RUSTY')
        assert.equal(postedPayload.songbook_id, 'sb-party')
        assert.equal(postedPayload.rotation_mode, 'fair_rotation')
        assert.equal(postedPayload.recurring, true)
        assert.equal(postedPayload.auto_accept, true)
        assert.equal(postedPayload.submission_delay, 15)
        assert.equal(postedPayload.max_active_per_singer, 2)
        assert.equal(postedPayload.allow_key_change, true)

        // Verify state updates and navigation
        assert.equal(created.id, 'evt-created-99')
        assert.equal(hook.createModalSongbookId, null, 'Modal must close on success')
        assert.equal(routedUrl, '/kj/queue?id=evt-created-99', 'Must navigate to the new show queue')
        assert.ok(toastMessage && toastMessage.includes('RUSTY'), 'Must toast show creation with join code')
        assert.equal(hook.events[0].id, 'evt-created-99', 'Created show must be prepended to events list')
    } finally {
        render(null, root)
    }
})

test('useStartShowHandoff lifecycle: lookup failure captures error and retry recovers state', async () => {
    let hook = null
    let failEvents = true

    const mockApi = {
        get: async (path) => {
            if (path === '/events') {
                if (failEvents) {
                    throw new Error('500 Internal Server Error')
                }
                return {
                    events: [
                        { id: 'evt-recovered', songbook_id: 'sb-1', status: 'active' }
                    ]
                }
            }
            return {}
        }
    }

    function TestComponent() {
        hook = useStartShowHandoff({ apiClient: mockApi })
        return null
    }

    try {
        render(h(TestComponent), root)

        // 1. Initial failed lookup
        await assert.rejects(async () => {
            await hook.loadEvents()
        }, /500 Internal Server Error/)

        assert.equal(hook.eventsError, '500 Internal Server Error', 'Error must be captured in state')
        assert.equal(hook.events.length, 0, 'Events list must not fabricate empty valid shows')
        assert.equal(hook.eventsLoading, false)

        // 2. Retry succeeds
        failEvents = false
        const recovered = await hook.loadEvents()

        assert.equal(hook.eventsError, '', 'Error must be cleared after successful retry')
        assert.equal(recovered.length, 1)
        assert.equal(hook.events[0].id, 'evt-recovered')
    } finally {
        render(null, root)
    }
})

test('regression: direct-link loading/handoff integration failure and retry handling', async () => {
    let failEvents = true
    let failSongbooks = false
    let mockEvents = [{ id: 'evt-1', songbook_id: 'sb-target', status: 'active' }]
    let mockSongbooks = [{ id: 'sb-target', name: 'Rock Classics' }]

    const mockApi = {
        get: async (endpoint) => {
            if (endpoint === '/events') {
                if (failEvents) throw new Error('503 Service Unavailable: Events')
                return { events: mockEvents }
            }
            if (endpoint === '/songbooks') {
                if (failSongbooks) throw new Error('500 Internal Error: Songbooks')
                return { songbooks: mockSongbooks }
            }
            return {}
        }
    }

    let hook = null
    function TestComponent() {
        hook = useDirectQueueHandoff({
            targetId: 'sb-target',
            apiClient: mockApi,
            showToast: () => {},
            autoLoad: false,
        })
        return null
    }

    try {
        render(h(TestComponent), root)

        // 1. Songbook lookup succeeds, event lookup fails -> error, no creation
        const res1 = await hook.loadHostData()
        assert.equal(res1.ok, false)
        assert.equal(res1.error, '503 Service Unavailable: Events')
        assert.equal(hook.loadError, '503 Service Unavailable: Events')
        assert.equal(hook.showCreateModal, false, 'Must not open creation modal on lookup failure')
        assert.equal(hook.selectedEventId, '', 'Must not select a fallback show on lookup failure')
        assert.equal(hook.loading, false)

        // 2. Retry returns an existing show -> correct queue
        failEvents = false
        mockEvents = [{ id: 'evt-1', songbook_id: 'sb-target', status: 'active' }]
        await hook.loadHostData()

        assert.equal(hook.loadError, '')
        assert.equal(hook.showCreateModal, false)
        assert.equal(hook.selectedEventId, 'evt-1', 'Must select active show for the songbook')

        // 3. Retry confirms no current show -> preselected creation
        // First simulate a failure again
        failEvents = true
        await hook.loadHostData()
        assert.equal(hook.loadError, '503 Service Unavailable: Events')
        assert.equal(hook.showCreateModal, false)

        // Then retry succeeds with NO live shows for the songbook
        failEvents = false
        mockEvents = []
        await hook.loadHostData()
        assert.equal(hook.loadError, '')
        assert.equal(hook.showCreateModal, true, 'Must open creation when absence of show is confirmed')
        assert.equal(hook.initialCreateSongbookId, 'sb-target', 'Must preselect the target songbook')
        assert.equal(hook.selectedEventId, '')

        // 4. Songbook lookup failure as well
        failSongbooks = true
        await hook.loadHostData()
        assert.equal(hook.loadError, '500 Internal Error: Songbooks')
        assert.equal(hook.showCreateModal, false, 'Must not open creation when songbook lookup fails')
        assert.equal(hook.selectedEventId, '')
    } finally {
        render(null, root)
    }
})
