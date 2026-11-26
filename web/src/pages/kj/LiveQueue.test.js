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

    dispatch(type) {
        this.listeners[type].call(this, { type, target: this })
    }
}

test('switching shows hides old requests and dialogs while polling fails and recovers', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const root = new TestNode('div')
    const documentListeners = new Map()
    const document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        addEventListener(type, listener) { documentListeners.set(type, listener) },
        removeEventListener(type) { documentListeners.delete(type) },
        activeElement: null,
    }
    const beforeDocument = globalThis.document
    globalThis.document = document
    const originalFetch = globalThis.fetch
    const beforeWindow = globalThis.window
    globalThis.window = { location: { search: '' } }
    const originalConfirm = globalThis.confirm
    globalThis.confirm = () => true

    const alpha = { id: 'alpha', name: 'Alpha Show', join_code: 'ALPHA', status: 'ended', rotation_mode: 'fcfs' }
    const bravo = { id: 'bravo', name: 'Bravo Show', join_code: 'BRAVO', status: 'active', rotation_mode: 'fcfs' }
    const alphaRequest = { id: 'alpha-song', status: 'accepted', singer_name: 'Alpha Singer', song_title: 'Alpha Song' }
    const bravoRequest = { id: 'bravo-song', status: 'accepted', singer_name: 'Bravo Singer', song_title: 'Bravo Song' }
    const pendingAlpha = Promise.withResolvers()
    const pendingBravo = Promise.withResolvers()
    const restart = Promise.withResolvers()
    const deleteShow = Promise.withResolvers()
    const paths = []
    let alphaReads = 0
    let bravoReads = 0

    globalThis.fetch = (url, { method }) => {
        paths.push([method, url])
        switch (url) {
        case '/api/v1/events': return Response.json({ events: [alpha, bravo] })
        case '/api/v1/songbooks': return Response.json({ songbooks: [] })
        case '/api/v1/events/alpha/requests':
            return ++alphaReads === 2
                ? pendingAlpha.promise
                : Response.json({ requests: [alphaRequest], queue_version: 1 })
        case '/api/v1/events/bravo/requests':
            return ++bravoReads === 1
                ? pendingBravo.promise
                : Response.json({ requests: [bravoRequest], queue_version: 2 })
        case '/api/v1/events/alpha/restart': return restart.promise
        case '/api/v1/events/alpha': return deleteShow.promise
        default: assert.fail(`Unexpected fetch: ${method} ${url}`)
        }
    }

    const findButton = label => root.find(node => node.localName === 'button' && node.textContent.includes(label))
    const showName = () => root.find(node => node.localName === 'h2').textContent
    const switcher = () => root.find(node => node.localName === 'select')
    const selectShow = async id => {
        await act(() => {
            const select = switcher()
            select.focus()
            select.value = id
            select.dispatch('Change')
        })
        assert.equal(document.activeElement, switcher())
    }
    const flush = () => act(async () => new Promise(resolve => setImmediate(resolve)))

    try {
        const { LiveQueue } = await vite.ssrLoadModule('/src/pages/kj/LiveQueue.jsx')
        await act(() => render(h(LiveQueue, { id: 'alpha' }), root))
        await flush()
        await flush()
        assert.match(root.textContent, /Alpha Song/)
        await act(() => root.find(node => node.attributes.title === 'Edit request details').dispatch('Click'))
        assert.match(root.textContent, /Modify Singer or Song/)
        await act(() => documentListeners.get('visibilitychange')())
        assert.equal(alphaReads, 2)

        await selectShow('bravo')
        assert.equal(showName(), 'Bravo Show')
        assert.doesNotMatch(root.textContent, /Alpha Song|Modify Singer or Song|Total Requests/)
        assert.match(root.textContent, /Loading queue/)
        assert.ok(paths.some(([method, path]) => method === 'GET' && path === '/api/v1/events/bravo/requests'))

        pendingBravo.reject(new Error('Queue lookup failed'))
        await flush()
        assert.match(root.textContent, /Could not load the show queue/)
        assert.doesNotMatch(root.textContent, /Alpha Song|No active requests/)
        pendingAlpha.resolve(Response.json({
            requests: [{ ...alphaRequest, song_title: 'Late Alpha Song' }],
            queue_version: 2,
            current_round: 9,
        }))
        await flush()
        assert.match(root.textContent, /Could not load the show queue/)
        assert.doesNotMatch(root.textContent, /Late Alpha Song|Round 9/)
        await act(() => findButton('Retry queue').dispatch('Click'))
        await flush()
        assert.match(root.textContent, /Bravo Song/)

        await selectShow('alpha')
        await flush()
        assert.match(root.textContent, /Alpha Song/)
        await act(() => findButton('Restart').dispatch('Click'))
        await act(() => findButton('Restart Show Now').dispatch('Click'))
        assert.ok(paths.some(([method, path]) => method === 'POST' && path === '/api/v1/events/alpha/restart'))
        await selectShow('bravo')
        await flush()
        assert.match(root.textContent, /Bravo Song/)
        const readsBeforeRestart = bravoReads
        restart.resolve(Response.json({ ...alpha, status: 'active' }))
        await flush()
        assert.match(root.textContent, /Bravo Song/)
        assert.equal(bravoReads, readsBeforeRestart)

        await selectShow('alpha')
        await flush()
        await act(() => findButton('Settings').dispatch('Click'))
        await act(() => findButton('Delete Event').dispatch('Click'))
        assert.ok(paths.some(([method, path]) => method === 'DELETE' && path === '/api/v1/events/alpha'))
        await selectShow('bravo')
        await flush()
        deleteShow.resolve(new Response(null, { status: 204 }))
        await flush()
        assert.equal(showName(), 'Bravo Show')
        assert.match(root.textContent, /Bravo Song/)
        assert.doesNotMatch(root.textContent, /Alpha Show/)
    } finally {
        await act(() => render(null, root))
        globalThis.document = beforeDocument
        globalThis.window = beforeWindow
        globalThis.fetch = originalFetch
        globalThis.confirm = originalConfirm
        await vite.close()
    }
})
