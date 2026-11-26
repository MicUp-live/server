import { useState, useCallback, useEffect } from 'preact/hooks'
import { route } from 'preact-router'
import { api } from '../../../api/client.js'
import { showToast as defaultShowToast } from '../../../components/toastState.js'
import { getSongbookPermissions } from '../../../utils/songbookPermissions.js'

/**
 * Pure state evaluator for a songbook's current live show status.
 * Distinguishes loading, lookup failures, active/paused shows, and absence of shows.
 */
export function resolveSongbookShowState(songbookId, events = [], { eventsLoading = false, eventsError = null } = {}) {
    if (eventsError) {
        return {
            status: 'error',
            error: eventsError,
            hasLiveShow: false,
            liveEvent: null,
            queueUrl: null,
        }
    }

    if (eventsLoading) {
        return {
            status: 'loading',
            error: null,
            hasLiveShow: false,
            liveEvent: null,
            queueUrl: null,
        }
    }

    const activeEvents = events.filter(e => e.songbook_id === songbookId && (e.status === 'active' || e.status === 'paused'))
    if (activeEvents.length > 0) {
        const liveEvent = activeEvents[0]
        return {
            status: 'live',
            error: null,
            hasLiveShow: true,
            liveEvent,
            queueUrl: `/kj/queue?id=${liveEvent.id}`,
        }
    }

    return {
        status: 'none',
        error: null,
        hasLiveShow: false,
        liveEvent: null,
        queueUrl: null,
    }
}

/**
 * Resolves routing for direct links to /kj/queue when passed an event ID or songbook ID.
 * Preserves lookup errors or loading states, preventing unconfirmed show creation or fallback selection.
 */
export function resolveDirectQueueHandoff(targetId, events = [], songbooks = [], { loading = false, error = null } = {}) {
    if (error) {
        return { action: 'error', error, eventId: null, songbookId: null }
    }

    if (loading) {
        return { action: 'loading', error: null, eventId: null, songbookId: null }
    }

    if (targetId && events.some(e => e.id === targetId)) {
        return { action: 'select_event', eventId: targetId, songbookId: null }
    }

    const targetBook = targetId ? songbooks.find(s => s.id === targetId) : null
    if (targetBook) {
        const liveEv = events.find(e => e.songbook_id === targetId && (e.status === 'active' || e.status === 'paused'))
        if (liveEv) {
            return { action: 'select_event', eventId: liveEv.id, songbookId: null }
        }
        if (getSongbookPermissions(targetBook).canHostShows) {
            return { action: 'open_create', eventId: null, songbookId: targetId }
        }
    }

    if (events.length > 0) {
        const active = events.find(e => e.status === 'active') || events[0]
        return { action: 'select_event', eventId: active.id, songbookId: null }
    }

    return { action: 'none', eventId: '', songbookId: null }
}

/**
 * Hook managing direct-link handoffs to /kj/queue, preserving lookup errors
 * and preventing unconfirmed show creation or fallback selection.
 */
export function useDirectQueueHandoff({
    targetId: propTargetId,
    apiClient = api,
    showToast = defaultShowToast,
    autoLoad = true,
} = {}) {
    const [events, setEvents] = useState([])
    const [songbooks, setSongbooks] = useState([])
    const [selectedEventId, setSelectedEventId] = useState('')
    const [initialCreateSongbookId, setInitialCreateSongbookId] = useState('')
    const [showCreateModal, setShowCreateModal] = useState(false)
    const [loading, setLoading] = useState(true)
    const [loadError, setLoadError] = useState('')

    const getTargetId = useCallback(() => {
        if (propTargetId) return propTargetId
        if (typeof window !== 'undefined') {
            const queryParams = new URLSearchParams(window.location.search)
            return queryParams.get('id') || queryParams.get('event_id') || queryParams.get('songbook_id') || ''
        }
        return ''
    }, [propTargetId])

    const loadHostData = useCallback(async () => {
        setLoading(true)
        setLoadError('')
        try {
            const [eventsData, songbooksData] = await Promise.all([
                apiClient.get('/events'),
                apiClient.get('/songbooks'),
            ])

            const evList = eventsData?.events || []
            const sbList = songbooksData?.songbooks || []

            setEvents(evList)
            setSongbooks(sbList)

            const targetId = getTargetId()
            const handoff = resolveDirectQueueHandoff(targetId, evList, sbList)

            if (handoff.action === 'select_event') {
                setSelectedEventId(handoff.eventId)
            } else if (handoff.action === 'open_create') {
                setSelectedEventId('')
                setInitialCreateSongbookId(handoff.songbookId)
                setShowCreateModal(true)
            } else if (evList.length > 0) {
                setSelectedEventId(prev => {
                    if (prev && evList.some(e => e.id === prev)) return prev
                    const active = evList.find(e => e.status === 'active') || evList[0]
                    return active.id
                })
            }
            return { ok: true, events: evList, songbooks: sbList, handoff }
        } catch (err) {
            const msg = err.message || 'Failed to load host data'
            setLoadError(msg)
            setShowCreateModal(false)
            showToast(msg, 'error')
            return { ok: false, error: msg }
        } finally {
            setLoading(false)
        }
    }, [getTargetId, apiClient, showToast])

    useEffect(() => {
        if (autoLoad) {
            loadHostData()
        }
    }, [])

    return {
        events,
        setEvents,
        songbooks,
        setSongbooks,
        selectedEventId,
        setSelectedEventId,
        initialCreateSongbookId,
        setInitialCreateSongbookId,
        showCreateModal,
        setShowCreateModal,
        loading,
        setLoading,
        loadError,
        setLoadError,
        loadHostData,
        getTargetId,
    }
}

/**
 * Hook managing show handoff states, event fetching, and show creation lifecycle.
 */
export function useStartShowHandoff({
    onNavigate = (url) => route(url),
    showToast = defaultShowToast,
    apiClient = api,
} = {}) {
    const [events, setEvents] = useState([])
    const [eventsLoading, setEventsLoading] = useState(false)
    const [eventsError, setEventsError] = useState('')
    const [createModalSongbookId, setCreateModalSongbookId] = useState(null)
    const [creatingShow, setCreatingShow] = useState(false)

    const loadEvents = useCallback(async () => {
        setEventsLoading(true)
        setEventsError('')
        try {
            const data = await apiClient.get('/events')
            const list = data?.events || []
            setEvents(list)
            return list
        } catch (err) {
            const msg = err.message || 'Failed to load active shows'
            setEventsError(msg)
            throw err
        } finally {
            setEventsLoading(false)
        }
    }, [apiClient])

    const startShow = useCallback((songbookId) => {
        setCreateModalSongbookId(songbookId)
    }, [])

    const cancelCreateShow = useCallback(() => {
        setCreateModalSongbookId(null)
    }, [])

    const createShow = useCallback(async (formData) => {
        setCreatingShow(true)
        try {
            const payload = {
                name: formData.name.trim(),
                venue_name: formData.venue_name.trim(),
                join_code: formData.join_code.trim().toUpperCase(),
                songbook_id: formData.songbook_id,
                recurring: !!formData.recurring,
                rotation_mode: formData.rotation_mode || 'round_robin',
                auto_accept: formData.auto_accept !== undefined ? formData.auto_accept : false,
                submission_delay: parseInt(formData.submission_delay, 10) || 0,
                max_active_per_singer: Math.max(0, parseInt(formData.max_active_per_singer, 10) || 0),
                allow_key_change: formData.allow_key_change !== undefined ? formData.allow_key_change : true,
                allow_write_ins: !!formData.allow_write_ins,
            }
            const created = await apiClient.post('/events', payload)
            showToast(`Show created! Singers can join with code: ${created.join_code}`)
            setCreateModalSongbookId(null)
            setEvents(prev => [created, ...prev])
            onNavigate(`/kj/queue?id=${created.id}`)
            return created
        } catch (err) {
            showToast(err.message || 'Failed to create show', 'error')
            throw err
        } finally {
            setCreatingShow(false)
        }
    }, [apiClient, showToast, onNavigate])

    return {
        events,
        setEvents,
        eventsLoading,
        eventsError,
        setEventsError,
        createModalSongbookId,
        setCreateModalSongbookId,
        creatingShow,
        loadEvents,
        startShow,
        cancelCreateShow,
        createShow,
    }
}
