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

    dispatch(type, extra = {}) {
        const cap = type.charAt(0).toUpperCase() + type.slice(1)
        const resolvedType = this.listeners[type] ? type : cap
        const listener = this.listeners[resolvedType]
        assert.ok(listener, `Expected ${type} listener on <${this.localName}>`)
        return listener.call(this, {
            type: resolvedType,
            target: this,
            currentTarget: this,
            preventDefault() {},
            stopPropagation() {},
            ...extra,
        })
    }
}

async function createVite() {
    return createServer({
        root: fileURLToPath(new URL('../../../../', import.meta.url)),
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, ws: false, watch: null },
    })
}

test('ShowSettingsModal displays current kiosk_pin and submits trimmed updates or omits blank PIN', async () => {
    const vite = await createVite()
    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { ShowSettingsModal } = await vite.ssrLoadModule('/src/pages/kj/components/ShowSettingsModal.jsx')
        const savedPayloads = []
        const root = new TestNode('div')

        act(() => {
            render(
                h(ShowSettingsModal, {
                    isOpen: true,
                    onClose: () => {},
                    currentEvent: {
                        id: 'evt-1',
                        name: 'Friday Karaoke',
                        rotation_mode: 'round_robin',
                        kiosk_pin: '4829',
                    },
                    onSaveSettings: async (payload) => {
                        savedPayloads.push(payload)
                        return true
                    },
                }),
                root
            )
        })

        const pinInput = root.find(n => n.localName === 'input' && n.attributes.id === 'settings-kiosk-pin')
        assert.ok(pinInput, 'expected kiosk PIN input in ShowSettingsModal')
        assert.equal(pinInput.value, '4829')
        assert.equal(pinInput.attributes.inputMode || pinInput.attributes.inputmode || pinInput.inputMode, 'numeric')

        // Update PIN and submit
        act(() => {
            pinInput.value = ' 9051 '
            pinInput.dispatch('input')
        })

        const form = root.find(n => n.localName === 'form')
        await act(async () => {
            await form.dispatch('submit')
        })

        assert.equal(savedPayloads.length, 1)
        assert.equal(savedPayloads[0].kiosk_pin, '9051')

        // Clear PIN and submit -> kiosk_pin should be omitted so backend keeps existing PIN
        act(() => {
            pinInput.value = '   '
            pinInput.dispatch('input')
        })
        await act(async () => {
            await form.dispatch('submit')
        })

        assert.equal(savedPayloads.length, 2)
        assert.equal('kiosk_pin' in savedPayloads[1], false)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('CreateShowModal exposes optional kiosk_pin input and passes it to onCreateShow', async () => {
    const vite = await createVite()
    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { CreateShowModal } = await vite.ssrLoadModule('/src/pages/kj/components/CreateShowModal.jsx')
        const submitted = []
        const root = new TestNode('div')

        act(() => {
            render(
                h(CreateShowModal, {
                    isOpen: true,
                    onClose: () => {},
                    songbooks: [{ id: 'sb-1', name: 'Main Book', song_count: 100, static_ready: true, role: 'owner' }],
                    onCreateShow: (form) => submitted.push(form),
                }),
                root
            )
        })

        const pinInput = root.find(n => n.localName === 'input' && n.attributes.id === 'create-form-kiosk-pin')
        assert.ok(pinInput, 'expected kiosk PIN input in CreateShowModal')

        act(() => {
            pinInput.value = '7744'
            pinInput.dispatch('input')
        })

        const form = root.find(n => n.localName === 'form')
        act(() => {
            form.dispatch('submit')
        })

        assert.equal(submitted.length, 1)
        assert.equal(submitted[0].kiosk_pin, '7744')
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})

test('KioskExitModal labels PIN input accessibly and omits misleading default 1234 placeholder', async () => {
    const vite = await createVite()
    const beforeDocument = globalThis.document
    globalThis.document = {
        createElementNS: (_, name) => new TestNode(name),
        createTextNode: data => new TestNode(null, data),
        activeElement: null,
    }

    try {
        const { KioskExitModal } = await vite.ssrLoadModule('/src/pages/kiosk/components/KioskExitModal.jsx')
        const root = new TestNode('div')

        act(() => {
            render(
                h(KioskExitModal, {
                    isOpen: true,
                    pinInput: '',
                    onPinInputChange: () => {},
                    pinError: '',
                    verifyingPin: false,
                    onVerifyPin: () => {},
                    onClose: () => {},
                }),
                root
            )
        })

        const label = root.find(n => n.localName === 'label' && (n.attributes.for === 'kiosk-exit-pin' || n.attributes.htmlFor === 'kiosk-exit-pin' || n.htmlFor === 'kiosk-exit-pin'))
        const input = root.find(n => n.localName === 'input' && n.attributes.id === 'kiosk-exit-pin')
        assert.ok(label, 'expected accessible label for kiosk-exit-pin')
        assert.ok(input, 'expected input#kiosk-exit-pin')
        assert.doesNotMatch(String(input.attributes.placeholder || input.placeholder || ''), /1234/)
    } finally {
        globalThis.document = beforeDocument
        await vite.close()
    }
})
