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

    dispatch(type, extra = {}) {
        const cap = type.charAt(0).toUpperCase() + type.slice(1)
        const resolvedType = this.listeners[type] ? type : cap
        const listener = this.listeners[resolvedType]
        assert.ok(listener, `Expected ${type} listener on <${this.localName}>`)
        listener.call(this, {
            type: resolvedType,
            target: this,
            currentTarget: this,
            preventDefault() {},
            stopPropagation() {},
            ...extra,
        })
    }
}

test('SingerWeightsModal adjusts, adds, clamps, clears, and saves normalized singer weights', async () => {
    const vite = await createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })

    const root = new TestNode('div')
    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const mod = await vite.ssrLoadModule('/src/pages/kj/components/SingerWeightsModal.jsx')
        const {
            SingerWeightsModal,
            clampSingerWeight,
            normalizeSingerKey,
            sanitizeSingerWeightsPayload,
        } = mod

        // Unit check on pure helpers
        assert.equal(normalizeSingerKey('  Birthday Sarah  '), 'birthday sarah')
        assert.equal(clampSingerWeight(99), 10)
        assert.equal(clampSingerWeight(-99), -10)
        assert.deepEqual(
            sanitizeSingerWeightsPayload({ ' Alice ': 15, BOB: -20, Zero: 0, '': 4 }),
            { alice: 10, bob: -10 },
        )

        const savedPayloads = []
        let closedCount = 0

        const renderModal = async (props = {}) => {
            await act(() => {
                render(h(SingerWeightsModal, {
                    isOpen: true,
                    onClose: () => { closedCount++ },
                    currentEvent: {
                        id: 'ev-1',
                        rotation_mode: 'fair_priority',
                        singer_weights: { alice: 3, bob: -4 },
                    },
                    requests: [
                        { id: 'r1', singer_name: 'Alice' },
                        { id: 'r2', singer_name: 'Bob' },
                        { id: 'r3', singer_name: 'Charlie' },
                    ],
                    initialSinger: 'Alice',
                    onSaveSingerWeights: async (payload) => {
                        savedPayloads.push(payload)
                        return true
                    },
                    savingWeights: false,
                    ...props,
                }), root)
            })
        }

        await renderModal()

        // Privacy notice is rendered
        assert.ok(root.textContent.includes('Private to host'))
        assert.ok(root.textContent.includes('VIP +3'))
        assert.ok(root.textContent.includes('Cooldown -4'))

        // Increase Alice from +3 to +4
        const incAlice = root.find(n => n.localName === 'button' && n.attributes['aria-label'] === 'Increase weight for Alice')
        assert.ok(incAlice)
        await act(() => { incAlice.dispatch('click') })
        assert.ok(root.textContent.includes('VIP +4'))

        // Add a new singer "Birthday Sarah" with default +3
        const nameInput = root.find(n => n.localName === 'input' && n.attributes.id === 'singer-weight-name')
        assert.ok(nameInput)
        await act(() => {
            nameInput.value = '  Birthday Sarah  '
            nameInput.dispatch('input')
        })
        const addBtn = root.find(n => n.localName === 'button' && n.textContent.trim() === 'Add')
        assert.ok(addBtn)
        await act(() => { addBtn.dispatch('click') })
        assert.ok(root.textContent.includes('Birthday Sarah'))

        // Submit form -> saves normalized payload
        const form = root.find(n => n.localName === 'form')
        assert.ok(form)
        await act(async () => {
            await form.dispatch('submit')
        })
        assert.equal(savedPayloads.length, 1)
        assert.deepEqual(savedPayloads[0], {
            alice: 4,
            bob: -4,
            'birthday sarah': 3,
        })
        assert.equal(closedCount, 1)

        // Re-render and test Clear All -> saves empty object {}
        await renderModal()
        const clearBtn = root.find(n => n.localName === 'button' && n.textContent.includes('Clear All'))
        assert.ok(clearBtn)
        await act(() => { clearBtn.dispatch('click') })
        const form2 = root.find(n => n.localName === 'form')
        await act(async () => {
            await form2.dispatch('submit')
        })
        assert.deepEqual(savedPayloads[1], {})
    } finally {
        render(null, root)
        globalThis.document = beforeDocument
        await vite.close()
    }
})
