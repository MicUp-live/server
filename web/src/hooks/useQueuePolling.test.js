import test from 'node:test'
import assert from 'node:assert/strict'
import { QueuePollingController } from './useQueuePolling.js'

test('QueuePollingController polls endpoint and appends since parameter', async () => {
    const calls = []
    let queueVersionToReturn = 10

    const fetcher = async (url) => {
        calls.push(url)
        return {
            queue_version: queueVersionToReturn,
            requests: [{ id: 'req-1' }],
        }
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/requests',
        interval: 100,
        trackVisibility: false,
        fetcher,
    })

    // First poll (no since parameter)
    await controller.start()
    assert.equal(calls.length, 1)
    assert.equal(calls[0], '/api/v1/events/ev-1/requests')
    assert.equal(controller.queueVersion, 10)

    // Second poll (should include since=10)
    queueVersionToReturn = 11
    await controller.poll()
    assert.equal(calls.length, 2)
    assert.equal(calls[1], '/api/v1/events/ev-1/requests?since=10')
    assert.equal(controller.queueVersion, 11)

    controller.destroy()
})

test('QueuePollingController handles 204 No Content', async () => {
    let callCount = 0
    const fetcher = async (url) => {
        callCount++
        if (callCount === 1) {
            return { queue_version: 5, requests: [] }
        }
        // 204 No Content returns null/undefined
        return null
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/queue',
        interval: 100,
        trackVisibility: false,
        fetcher,
    })

    await controller.start()
    assert.equal(controller.queueVersion, 5)

    // Poll with 204 keeps version 5
    await controller.poll()
    assert.equal(controller.queueVersion, 5)

    controller.destroy()
})

test('QueuePollingController handles visibility recovery with full sync', async () => {
    const urls = []
    const fetcher = async (url) => {
        urls.push(url)
        return { queue_version: 42, requests: [] }
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/requests',
        trackVisibility: false,
        fetcher,
    })

    await controller.start()
    assert.equal(controller.queueVersion, 42)

    // Tab returns from hidden state -> should reset queueVersion and trigger full sync
    controller.handleVisibilityChange(false)
    assert.equal(controller.queueVersion, null)

    controller.destroy()
})

test('QueuePollingController backs off exponentially on error and stops on 404', async () => {
    let errorSeen = null
    let errorStatus = 500

    const fetcher = async () => {
        const err = new Error('Server error')
        err.status = errorStatus
        throw err
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/requests',
        interval: 1000,
        maxInterval: 8000,
        trackVisibility: false,
        fetcher,
        onError: (err) => { errorSeen = err },
    })

    controller.isActive = true
    await controller.poll()
    assert.equal(errorSeen.status, 500)
    assert.equal(controller.currentDelay, 2000)

    await controller.poll()
    assert.equal(controller.currentDelay, 4000)

    await controller.poll()
    assert.equal(controller.currentDelay, 8000)

    // If 404 occurs, it stops polling
    errorStatus = 404
    await controller.poll()
    assert.equal(controller.isActive, false)

    controller.destroy()
})

test('QueuePollingController refresh triggers immediate full sync', async () => {
    const calls = []
    const fetcher = async (url) => {
        calls.push(url)
        return { queue_version: 7, requests: [] }
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/requests',
        trackVisibility: false,
        fetcher,
    })

    controller.isActive = true
    await controller.poll()
    assert.equal(controller.queueVersion, 7)

    // Call refresh(true)
    await controller.refresh(true)
    assert.equal(calls.length, 2)
    assert.equal(calls[1], '/api/v1/events/ev-1/requests') // No since= on full sync

    controller.destroy()
})

test('QueuePollingController queues refresh during in-flight poll and drops stale response', async () => {
    const calls = []
    const received = []
    let resolveFirst
    let callNum = 0

    const fetcher = (url) => {
        calls.push(url)
        callNum++
        if (callNum === 1) {
            return new Promise((resolve) => { resolveFirst = resolve })
        }
        return Promise.resolve({ queue_version: 20, requests: [{ id: 'fresh' }] })
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/requests',
        interval: 60000,
        trackVisibility: false,
        fetcher,
        onData: (data) => received.push(data),
    })

    const firstPoll = controller.start()
    assert.equal(calls.length, 1)
    assert.equal(controller.isPolling, true)

    // Trigger refresh(true) while first poll is still in flight
    controller.refresh(true)

    // Resolve stale first poll with older queue_version
    resolveFirst({ queue_version: 5, requests: [{ id: 'stale' }] })
    await firstPoll
    // Flush microtasks for queued refresh poll
    await new Promise((r) => setTimeout(r, 10))

    assert.equal(calls.length, 2)
    assert.equal(calls[1], '/api/v1/events/ev-1/requests')
    assert.equal(received.length, 1)
    assert.equal(received[0].queue_version, 20)
    assert.equal(controller.queueVersion, 20)

    controller.destroy()
})

test('QueuePollingController notifies onResponse on 204 recovery and clears loading on 404', async () => {
    let attempt = 0
    let responses = 0
    let lastLoading = null

    const fetcher = async () => {
        attempt++
        if (attempt === 1) {
            const err = new Error('temporary failure')
            err.status = 500
            throw err
        }
        if (attempt === 2) {
            return null // 204 No Content recovery
        }
        const err = new Error('not found')
        err.status = 404
        throw err
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/queue',
        interval: 60000,
        trackVisibility: false,
        fetcher,
        onResponse: () => { responses++ },
        onLoading: (val) => { lastLoading = val },
    })

    controller.isActive = true
    await controller.poll() // 500
    assert.equal(responses, 0)
    assert.equal(lastLoading, false)

    await controller.poll() // 204 recovery
    assert.equal(responses, 1)
    assert.equal(controller.currentDelay, 60000)

    await controller.poll() // 404 stops controller and clears loading
    assert.equal(controller.isActive, false)
    assert.equal(lastLoading, false)

    controller.destroy()
})

test('QueuePollingController clears loading when setEndpoint(null) happens mid-poll and resumes after stop/start', async () => {
    let resolveFirst
    let callCount = 0
    const loadingEvents = []

    const fetcher = () => {
        callCount++
        if (callCount === 1) {
            return new Promise((resolve) => { resolveFirst = resolve })
        }
        return Promise.resolve({ queue_version: 2, requests: [] })
    }

    const controller = new QueuePollingController({
        endpoint: '/api/v1/events/ev-1/queue',
        interval: 60000,
        trackVisibility: false,
        fetcher,
        onLoading: (val) => loadingEvents.push(val),
    })

    const first = controller.start()
    assert.equal(loadingEvents.at(-1), true)

    // Clearing endpoint mid-poll queues a refresh whose poll() exits early because endpoint is null
    controller.setEndpoint(null)
    resolveFirst({ queue_version: 1, requests: [] })
    await first
    assert.equal(loadingEvents.at(-1), false)

    // Re-attach endpoint, start poll, stop+start mid-poll -> should trigger immediate refresh on settle
    let resolveSecond
    controller.fetcher = () => {
        callCount++
        if (callCount === 2) {
            return new Promise((resolve) => { resolveSecond = resolve })
        }
        return Promise.resolve({ queue_version: 9, requests: [] })
    }
    controller.setEndpoint('/api/v1/events/ev-2/queue')
    const second = controller.start()
    controller.stop()
    controller.start()
    resolveSecond({ queue_version: 3, requests: [] })
    await second
    await new Promise((r) => setTimeout(r, 10))
    assert.equal(callCount, 3)
    assert.equal(controller.queueVersion, 9)

    controller.destroy()
})
