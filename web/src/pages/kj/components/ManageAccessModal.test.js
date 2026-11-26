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

    findAll(predicate, results = []) {
        if (predicate(this)) results.push(this)
        for (const node of this.childNodes) {
            node.findAll(predicate, results)
        }
        return results
    }
}

function installDom() {
    globalThis.document = {
        createElement: tag => new TestNode(tag),
        createElementNS: (_ns, tag) => new TestNode(tag),
        createTextNode: text => new TestNode('', String(text)),
        activeElement: null,
    }
    globalThis.window = {
        location: { origin: 'http://localhost:5173', pathname: '/kj/songbooks', search: '' },
        addEventListener() {},
        removeEventListener() {},
    }
}

test('ManageAccessModal offers Retry after refresh for failed or unconfirmed deliveries and Resend for confirmed sends', async () => {
    installDom()
    const webRoot = fileURLToPath(new URL('../../../..', import.meta.url))
    const vite = await createServer({
        root: webRoot,
        logLevel: 'silent',
        server: { middlewareMode: true, hmr: false },
        appType: 'custom',
    })

    try {
        const { api } = await vite.ssrLoadModule('/src/api/client.js')
        const { ManageAccessModal } = await vite.ssrLoadModule('/src/pages/kj/components/ManageAccessModal.jsx')

        const origGet = api.get
        api.get = async (path) => {
            assert.equal(path, '/songbooks/sb-1/access')
            return {
                members: [],
                invitations: [
                    {
                        id: 'inv-failed',
                        recipient_email: 'failed@example.com',
                        can_edit_catalog: false,
                        can_host_shows: true,
                        send_error: 'ses transient failure on resend',
                        expires_at: '2026-12-01T00:00:00Z',
                    },
                    {
                        id: 'inv-unconfirmed',
                        recipient_email: 'unconfirmed@example.com',
                        can_edit_catalog: true,
                        can_host_shows: false,
                        expires_at: '2026-12-01T00:00:00Z',
                    },
                    {
                        id: 'inv-sent',
                        recipient_email: 'sent@example.com',
                        can_edit_catalog: true,
                        can_host_shows: true,
                        last_sent_at: '2026-11-22T20:00:00Z',
                        expires_at: '2026-12-01T00:00:00Z',
                    },
                ],
            }
        }

        const container = new TestNode('div')
        try {
            await act(async () => {
                render(h(ManageAccessModal, {
                    isOpen: true,
                    songbook: { id: 'sb-1', name: 'Friday Night Book' },
                    onClose: () => {},
                }), container)
            })
            await act(async () => {
                await Promise.resolve()
                await Promise.resolve()
            })

            const text = container.textContent
            assert.match(text, /failed@example\.com/)
            assert.match(text, /Email delivery failed — click Retry to send a fresh link\./)
            assert.match(text, /unconfirmed@example\.com/)
            assert.match(text, /Email delivery was not confirmed — click Retry to send a fresh link\./)
            assert.match(text, /sent@example\.com/)

            const buttons = container.findAll(n => n.localName === 'button').map(n => n.textContent.trim())
            const retryCount = buttons.filter(label => label === 'Retry').length
            const resendCount = buttons.filter(label => label === 'Resend').length
            assert.equal(retryCount, 2, `expected 2 Retry buttons (failed + unconfirmed), got buttons=${JSON.stringify(buttons)}`)
            assert.equal(resendCount, 1, `expected 1 Resend button (confirmed sent), got buttons=${JSON.stringify(buttons)}`)
        } finally {
            api.get = origGet
            render(null, container)
        }
    } finally {
        await vite.close()
    }
})
