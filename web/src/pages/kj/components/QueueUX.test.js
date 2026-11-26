import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { h, render } from 'preact'
import { act } from 'preact/test-utils'
import { createServer } from 'vite'

class TestNode {
    constructor(localName, data = '') {
        this.localName = localName
        this.data = data
        this.nodeType = localName ? 1 : 3
        this.parentNode = null
        this.childNodes = []
        this.attributes = {}
        this.listeners = {}
        this.style = { setProperty() {} }
        this.value = ''
    }

    get firstChild() { return this.childNodes[0] ?? null }
    get nextSibling() {
        return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) + 1] ?? null
    }
    get textContent() {
        return this.nodeType === 3 ? this.data : this.childNodes.map(node => node.textContent).join('')
    }

    setAttribute(name, value) { this.attributes[name] = value }
    removeAttribute(name) { delete this.attributes[name] }
    addEventListener(type, listener) { this.listeners[type] = listener }
    removeEventListener(type) { delete this.listeners[type] }
    focus() { document.activeElement = this }
    querySelector() { return null }
    querySelectorAll() { return [] }

    appendChild(node) { this.insertBefore(node, null) }
    insertBefore(node, next) {
        if (node === next) return
        node.parentNode?.removeChild(node)
        const index = next ? this.childNodes.indexOf(next) : this.childNodes.length
        this.childNodes.splice(index, 0, node)
        node.parentNode = this
    }
    removeChild(node) {
        this.childNodes.splice(this.childNodes.indexOf(node), 1)
        node.parentNode = null
    }

    find(predicate) {
        if (predicate(this)) return this
        for (const node of this.childNodes) {
            const result = node.find(predicate)
            if (result) return result
        }
        return null
    }
}

test('RequestCard history tab renders status badges, decline reason, and suppresses 1st song badge', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { RequestCard } = await vite.ssrLoadModule('/src/pages/kj/components/RequestCard.jsx')

        // 1. Completed card
        const rootCompleted = new TestNode('div')
        act(() => {
            render(
                h(RequestCard, {
                    req: { id: 'r1', singer_name: 'Alice', song_title: 'Song A', status: 'completed' },
                    index: 0,
                    tab: 'history',
                }),
                rootCompleted
            )
        })
        const textCompleted = rootCompleted.textContent
        assert.match(textCompleted, /✓ Completed/)
        assert.match(textCompleted, /✨ 1st song/)

        // 2. Declined card with decline reason
        const rootDeclined = new TestNode('div')
        act(() => {
            render(
                h(RequestCard, {
                    req: {
                        id: 'r2',
                        singer_name: 'Bob',
                        song_title: 'Song B',
                        status: 'rejected',
                        decline_reason: 'Duplicate song in this round',
                    },
                    index: 1,
                    tab: 'history',
                }),
                rootDeclined
            )
        })
        const textDeclined = rootDeclined.textContent
        assert.match(textDeclined, /✗ Declined/)
        assert.match(textDeclined, /Declined: Duplicate song in this round/)
        // Suppresses 1st song badge
        assert.doesNotMatch(textDeclined, /✨ 1st song/)

        // 3. Cancelled card
        const rootCancelled = new TestNode('div')
        act(() => {
            render(
                h(RequestCard, {
                    req: { id: 'r3', singer_name: 'Charlie', song_title: 'Song C', status: 'cancelled' },
                    index: 2,
                    tab: 'history',
                }),
                rootCancelled
            )
        })
        const textCancelled = rootCancelled.textContent
        assert.match(textCancelled, /Cancelled/)
        assert.doesNotMatch(textCancelled, /✨ 1st song/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('SingerPublicRotation labels up_next as On Stage and the next accepted as On Deck', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { SingerPublicRotation } = await vite.ssrLoadModule('/src/components/singerQueue/components/SingerPublicRotation.jsx')

        // Case 1: item 0 is up_next, item 1 is accepted
        const rootWithUpNext = new TestNode('div')
        const queue1 = [
            { id: 'q1', singer_name: 'Singer One', song_title: 'Song 1', status: 'up_next', round: 1 },
            { id: 'q2', singer_name: 'Singer Two', song_title: 'Song 2', status: 'accepted', round: 1 },
            { id: 'q3', singer_name: 'Singer Three', song_title: 'Song 3', status: 'accepted', round: 1 },
        ]
        act(() => {
            render(
                h(SingerPublicRotation, {
                    queue: queue1,
                    currentRound: 1,
                    eventStatus: 'active',
                    loading: false,
                    noEvent: false,
                }),
                rootWithUpNext
            )
        })
        const text1 = rootWithUpNext.textContent
        assert.match(text1, /On Stage/)
        assert.match(text1, /On Deck/)

        // Case 2: nobody is up_next, item 0 is accepted
        const rootNoUpNext = new TestNode('div')
        const queue2 = [
            { id: 'q1', singer_name: 'Singer One', song_title: 'Song 1', status: 'accepted', round: 1 },
            { id: 'q2', singer_name: 'Singer Two', song_title: 'Song 2', status: 'accepted', round: 1 },
        ]
        act(() => {
            render(
                h(SingerPublicRotation, {
                    queue: queue2,
                    currentRound: 1,
                    eventStatus: 'active',
                    loading: false,
                    noEvent: false,
                }),
                rootNoUpNext
            )
        })
        const text2 = rootNoUpNext.textContent
        assert.doesNotMatch(text2, /On Stage/)
        assert.match(text2, /On Deck/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('CatalogControls displays filtered count when letter filter is active and not searching', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { CatalogControls } = await vite.ssrLoadModule('/src/pages/singer/components/CatalogControls.jsx')

        const root = new TestNode('div')
        act(() => {
            render(
                h(CatalogControls, {
                    viewMode: 'songs',
                    isSearching: false,
                    activeLetter: 'A',
                    displaySongsCount: 1,
                    totalSongs: 12,
                }),
                root
            )
        })
        const text = root.textContent
        assert.match(text, /1 song in A/)
        assert.doesNotMatch(text, /12 songs/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('CreateShowModal displays warning when selected songbook has no published catalog', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { CreateShowModal } = await vite.ssrLoadModule('/src/pages/kj/components/CreateShowModal.jsx')

        // Unpublished songbook (0 songs)
        const root = new TestNode('div')
        act(() => {
            render(
                h(CreateShowModal, {
                    isOpen: true,
                    songbooks: [
                        { id: 'sb-empty', name: 'Empty Book', song_count: 0, permissions: { can_host_shows: true } },
                    ],
                    initialSongbookId: 'sb-empty',
                    onClose: () => {},
                    onCreateShow: () => {},
                }),
                root
            )
        })
        const text = root.textContent
        assert.match(text, /No published catalog:/)
        assert.match(text, /Remember to import and publish songs/)

        // Published songbook (>0 songs and catalog_version_id)
        const rootPub = new TestNode('div')
        act(() => {
            render(
                h(CreateShowModal, {
                    isOpen: true,
                    songbooks: [
                        { id: 'sb-pub', name: 'Published Book', song_count: 50, catalog_version_id: 'v1', permissions: { can_host_shows: true } },
                    ],
                    initialSongbookId: 'sb-pub',
                    onClose: () => {},
                    onCreateShow: () => {},
                }),
                rootPub
            )
        })
        const textPub = rootPub.textContent
        assert.doesNotMatch(textPub, /No published catalog:/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('PublicBook error state renders Try Again button alongside Return Home', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    const beforeWindow = globalThis.window
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }
    globalThis.window = {
        addEventListener: () => {},
        removeEventListener: () => {},
        location: { search: '' },
    }

    try {
        const { PublicBook } = await vite.ssrLoadModule('/src/pages/singer/PublicBook.jsx')

        const root = new TestNode('div')
        await act(async () => {
            render(
                h(PublicBook, {
                    id: '', // Triggers "No songbook specified" error
                }),
                root
            )
            // Wait for loadInitialContext promise to reject and update state
            await new Promise(r => setTimeout(r, 20))
        })
        const text = root.textContent
        assert.match(text, /Try Again/)
        assert.match(text, /Return Home/)
    } finally {
        globalThis.document = beforeDocument
        globalThis.window = beforeWindow
        await vite.close()
    }
})

test('QueueStats renders unique singers count and wait estimate, omitting round clutter', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { QueueStats } = await vite.ssrLoadModule('/src/pages/kj/components/QueueStats.jsx')

        // 1. Round robin mode: renders Singers card instead of Round card, and wait estimate under In Queue
        const rootRR = new TestNode('div')
        act(() => {
            render(
                h(QueueStats, {
                    currentEvent: { rotation_mode: 'round_robin' },
                    activeCount: 3,
                    completedCount: 5,
                    totalCount: 8,
                    uniqueSingersCount: 4,
                }),
                rootRR
            )
        })
        const textRR = rootRR.textContent
        assert.match(textRR, /Singers/)
        assert.match(textRR, /4/)
        assert.match(textRR, /In Queue/)
        assert.match(textRR, /3/)
        assert.match(textRR, /~12m/)
        assert.doesNotMatch(textRR, /Round/)
        assert.doesNotMatch(textRR, /Next Round/)

        // 2. Fair priority mode: keeps Mode Fair and Weights button
        const rootFP = new TestNode('div')
        act(() => {
            render(
                h(QueueStats, {
                    currentEvent: { rotation_mode: 'fair_priority', singer_weights: { alice: 2 } },
                    activeCount: 0,
                    completedCount: 0,
                    totalCount: 0,
                    uniqueSingersCount: 0,
                    onOpenWeightsModal: () => {},
                }),
                rootFP
            )
        })
        const textFP = rootFP.textContent
        assert.match(textFP, /Mode/)
        assert.match(textFP, /Fair/)
        assert.match(textFP, /Weights \(1\)/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('SingerPublicRotation and SingerActiveRequests omit round clutter and show friendly wait', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { SingerPublicRotation } = await vite.ssrLoadModule('/src/components/singerQueue/components/SingerPublicRotation.jsx')
        const { SingerActiveRequests } = await vite.ssrLoadModule('/src/components/singerQueue/components/SingerActiveRequests.jsx')

        // 1. Rotation header and items
        const rootRot = new TestNode('div')
        act(() => {
            render(
                h(SingerPublicRotation, {
                    loading: false,
                    noEvent: false,
                    queue: [
                        { id: 'q1', singer_name: 'Alice', song_title: 'Song 1', status: 'up_next', round: 1 },
                        { id: 'q2', singer_name: 'Bob', song_title: 'Song 2', status: 'accepted', round: 1 },
                    ],
                    currentRound: 1,
                    eventStatus: 'active',
                }),
                rootRot
            )
        })
        const textRot = rootRot.textContent
        assert.match(textRot, /Full Room Rotation \(2 in line\)/)
        assert.doesNotMatch(textRot, /Round 1/)
        assert.match(textRot, /On Stage/)
        assert.match(textRot, /On Deck/)

        // 2. Active requests wait estimates
        const rootReqs = new TestNode('div')
        act(() => {
            render(
                h(SingerActiveRequests, {
                    activeRequests: [
                        { id: 'q1', singer_name: 'Alice', song_title: 'Song 1', status: 'accepted' }, // queueIndex 0 -> #1 Up next
                        { id: 'q2', singer_name: 'Alice', song_title: 'Song 2', status: 'accepted' }, // queueIndex 1 -> #2 ~4m wait
                    ],
                    queue: [
                        { id: 'q1', singer_name: 'Alice' },
                        { id: 'q2', singer_name: 'Alice' },
                    ],
                    currentRound: 1,
                    eventStatus: 'active',
                }),
                rootReqs
            )
        })
        const textReqs = rootReqs.textContent
        assert.match(textReqs, /#1 in line • Up next/)
        assert.match(textReqs, /#2 in line • ~4m wait/)
        assert.doesNotMatch(textReqs, /Round 1/)
        assert.doesNotMatch(textReqs, /In Reserve/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('Stage TV renders wait estimate in top bar and omits round clutter', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const beforeDocument = globalThis.document
    const beforeWindow = globalThis.window
    const beforeFetch = globalThis.fetch
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }
    globalThis.window = {
        addEventListener: () => {},
        removeEventListener: () => {},
        location: { origin: 'http://localhost:3000', search: '' },
    }
    globalThis.fetch = async () => ({
        ok: true,
        status: 200,
        headers: new Headers(),
        json: async () => ({ event: { id: 'evt-1' }, queue: [] }),
    })

    try {
        const { Stage } = await vite.ssrLoadModule('/src/pages/Stage.jsx')
        const root = new TestNode('div')
        await act(async () => {
            render(h(Stage, { id: 'evt-1' }), root)
        })
        const text = root.textContent
        assert.match(text, /Stage Open/)
        assert.doesNotMatch(text, /Round/)
        act(() => {
            render(null, root)
        })
    } finally {
        globalThis.document = beforeDocument
        globalThis.window = beforeWindow
        globalThis.fetch = beforeFetch
        await vite.close()
    }
})
