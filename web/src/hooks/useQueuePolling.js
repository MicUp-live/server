import { useEffect, useRef, useState, useCallback } from 'preact/hooks'
import { api } from '../api/client.js'

/**
 * Controller managing visibility-aware conditional queue polling.
 * Pure ES6 class for testability in Node / headless test suites.
 */
export class QueuePollingController {
    constructor(options = {}) {
        this.fetcher = options.fetcher || ((url) => api.get(url))
        this.endpoint = options.endpoint || null
        this.interval = options.interval || 4000
        this.maxInterval = options.maxInterval || 30000
        this.onData = options.onData || null
        this.onError = options.onError || null
        this.onLoading = options.onLoading || null
        this.onResponse = options.onResponse || null
        this.trackVisibility = options.trackVisibility !== false
        this.stopOn404 = options.stopOn404 !== false

        this.queueVersion = null
        this.currentDelay = this.interval
        this.timer = null
        this.isActive = false
        this.isDestroyed = false
        this.isPolling = false
        this._generation = 0
        this._pendingRefresh = null

        this._onVisibilityChange = () => {
            const isHidden = typeof document !== 'undefined' ? document.hidden : false
            this.handleVisibilityChange(isHidden)
        }

        if (this.trackVisibility && typeof document !== 'undefined' && document.addEventListener) {
            document.addEventListener('visibilitychange', this._onVisibilityChange)
        }
    }

    setEndpoint(newEndpoint) {
        if (this.endpoint !== newEndpoint) {
            this.endpoint = newEndpoint
            this._generation++
            this.queueVersion = null
            this.currentDelay = this.interval
            if (this.isActive) {
                void this.refresh(true)
            }
        }
    }

    start() {
        if (this.isDestroyed) return
        this.isActive = true
        this.currentDelay = this.interval
        if (this.isPolling) {
            this._pendingRefresh = {
                fullSync: Boolean(this._pendingRefresh?.fullSync),
            }
            return
        }
        return this.poll()
    }

    stop() {
        this.isActive = false
        this._generation++
        this._pendingRefresh = null
        if (this.timer) {
            clearTimeout(this.timer)
            this.timer = null
        }
    }

    refresh(fullSync = true) {
        if (this.isDestroyed) return
        this.isActive = true
        if (this.timer) {
            clearTimeout(this.timer)
            this.timer = null
        }
        this._generation++
        if (fullSync) {
            this.queueVersion = null
        }
        this.currentDelay = this.interval
        if (this.isPolling) {
            this._pendingRefresh = {
                fullSync: Boolean(this._pendingRefresh?.fullSync || fullSync),
            }
            return
        }
        return this.poll()
    }

    handleVisibilityChange(hidden) {
        if (!this.isActive || this.isDestroyed) return
        if (!hidden) {
            void this.refresh(true)
        }
    }

    _canPoll() {
        if (!this.isActive || this.isDestroyed || !this.endpoint || this.isPolling) {
            return false
        }
        return !(this.trackVisibility && typeof document !== 'undefined' && document.hidden)
    }

    _buildPollUrl() {
        if (this.queueVersion === null) {
            return this.endpoint
        }
        const separator = this.endpoint.includes('?') ? '&' : '?'
        return `${this.endpoint}${separator}since=${encodeURIComponent(this.queueVersion)}`
    }

    _isStaleGeneration(generation) {
        return !this.isActive || this.isDestroyed || generation !== this._generation
    }

    _handlePollSuccess(data) {
        this.currentDelay = this.interval
        this.onResponse?.(data)
        if (!data) return
        if (Number.isInteger(data.queue_version)) {
            this.queueVersion = data.queue_version
        }
        this.onData?.(data)
    }

    _handlePollError(err) {
        this.onError?.(err)
        if (this.stopOn404 && err?.status === 404) {
            this.stop()
            return
        }
        this.currentDelay = Math.min(this.currentDelay * 2, this.maxInterval)
    }

    _finishPoll() {
        this.isPolling = false
        const pending = this._pendingRefresh
        this._pendingRefresh = null

        if (this.isDestroyed) return
        if (!this.isActive) {
            this.onLoading?.(false)
            return
        }
        if (pending) {
            if (pending.fullSync) {
                this.queueVersion = null
            }
            void this.poll()
            if (!this.isPolling) {
                this.onLoading?.(false)
            }
            return
        }
        this.onLoading?.(false)
        const jitter = Math.random() * 500
        this.timer = setTimeout(() => this.poll(), this.currentDelay + jitter)
    }

    async poll() {
        if (!this._canPoll()) return

        this.isPolling = true
        this.onLoading?.(true)

        const generation = this._generation
        const url = this._buildPollUrl()

        try {
            const data = await this.fetcher(url)
            if (this._isStaleGeneration(generation)) return
            this._handlePollSuccess(data)
        } catch (err) {
            if (this._isStaleGeneration(generation)) return
            this._handlePollError(err)
        } finally {
            this._finishPoll()
        }
    }

    destroy() {
        this.stop()
        this.isDestroyed = true
        if (this.trackVisibility && typeof document !== 'undefined' && document.removeEventListener) {
            document.removeEventListener('visibilitychange', this._onVisibilityChange)
        }
    }
}

/**
 * Preact Hook for shared queue polling.
 */
export function useQueuePolling(options = {}) {
    const {
        endpoint,
        enabled = true,
        interval = 4000,
        maxInterval = 30000,
        onData,
        onError,
        trackVisibility = true,
        stopOn404 = true,
    } = options

    const [data, setData] = useState(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState(null)
    const [queueVersion, setQueueVersion] = useState(null)

    const onDataRef = useRef(onData)
    onDataRef.current = onData
    const onErrorRef = useRef(onError)
    onErrorRef.current = onError

    const controllerRef = useRef(null)

    useEffect(() => {
        setError(null)
        setLoading(false)
        const ctrl = new QueuePollingController({
            endpoint,
            interval,
            maxInterval,
            trackVisibility,
            stopOn404,
            onResponse: () => {
                setError(null)
            },
            onData: (freshData) => {
                setError(null)
                setData(freshData)
                if (Number.isInteger(freshData?.queue_version)) {
                    setQueueVersion(freshData.queue_version)
                }
                onDataRef.current?.(freshData)
            },
            onError: (err) => {
                setError(err)
                onErrorRef.current?.(err)
            },
            onLoading: (isLoading) => {
                setLoading(isLoading)
            },
        })
        controllerRef.current = ctrl

        if (enabled && endpoint) {
            void ctrl.start()
        }

        return () => {
            ctrl.destroy()
            controllerRef.current = null
        }
    }, [endpoint, enabled, interval, maxInterval, trackVisibility, stopOn404])

    const refresh = useCallback((fullSync = true) => {
        controllerRef.current?.refresh(fullSync)
    }, [])

    return {
        data,
        loading,
        error,
        refresh,
        queueVersion,
        controller: controllerRef.current,
    }
}
