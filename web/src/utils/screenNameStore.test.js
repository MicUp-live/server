import test from 'node:test'
import assert from 'node:assert/strict'
import { getScreenName, setScreenName, onScreenNameChange } from './screenNameStore.js'

// Mock minimal localStorage and window for Node environment test
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
        const list = listeners.get(type).filter(l => l !== fn)
        listeners.set(type, list)
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

test('returns empty string if no screen name set', () => {
    storage.clear()
    assert.equal(getScreenName(), '')
})

test('returns localStorage screen name when not logged in', () => {
    storage.clear()
    storage.set('micup_singer_name', 'Guest Rocker')
    assert.equal(getScreenName(), 'Guest Rocker')
})

test('prefers user display_name when logged in', () => {
    storage.clear()
    storage.set('micup_singer_name', 'Guest Rocker')
    const user = { display_name: 'Logged-in Star' }
    assert.equal(getScreenName(user), 'Logged-in Star')
})

test('updates localStorage and notifies listeners on setScreenName', async () => {
    storage.clear()
    let received = null
    const unsubscribe = onScreenNameChange((name) => {
        received = name
    })

    await setScreenName('New Screen Name')
    assert.equal(storage.get('micup_singer_name'), 'New Screen Name')
    assert.equal(received, 'New Screen Name')

    unsubscribe()
})

test('rejects empty screen name', async () => {
    assert.throws(
        () => setScreenName('   '),
        { message: 'Screen name cannot be empty' }
    )
})
