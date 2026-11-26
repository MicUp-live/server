import { useState, useEffect, useCallback, useRef } from 'preact/hooks'
import { api } from '../../../api/client.js'
import { showToast } from '../../toastState.js'
import { useQueuePolling } from '../../../hooks/useQueuePolling.js'
import {
    requestNotificationPermission,
    areStageAlertsEnabled,
    setStageAlertsEnabled,
    requestScreenWakeLock,
    releaseScreenWakeLock,
} from '../../../utils/notifications.js'
import { getScreenName, onScreenNameChange } from '../../../utils/screenNameStore.js'
import { user } from '../../../auth/store.js'
import {
    getSingerRequests,
    updateSingerRequests,
    removeSingerRequest,
    onSingerRequestsChange,
} from '../../../utils/singerRequestsStore.js'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function reconcileSingerRequests(prevRequests, queueItems = []) {
    return (prevRequests || []).map(myReq => {
        const found = queueItems.find(q => q.id === myReq.id)
        if (found) {
            return { ...myReq, status: found.status, round: found.round || myReq.round || 1 }
        }
        if (myReq.status === 'up_next' || myReq.status === 'accepted') {
            return {
                ...myReq,
                status: 'completed',
                completed_at: myReq.completed_at || new Date().toISOString()
            }
        }
        return myReq
    })
}

export function filterSingerRequests(requests = []) {
    const active = requests.filter(r => r.status !== 'completed' && r.status !== 'cancelled' && r.status !== 'rejected')
    const completed = requests.filter(r => r.status === 'completed')
    const declined = requests.filter(r => r.status === 'rejected')
    return { active, completed, declined }
}

export function useSingerQueue({ songbookId, eventId, eventStatus, isOpen, onClose, onEventJoined, onEventStatusChange }) {
    const [screenName, setScreenNameState] = useState(() => getScreenName(user.value))
    const [showScreenNameModal, setShowScreenNameModal] = useState(false)

    useEffect(() => {
        setScreenNameState(getScreenName(user.value))
        return onScreenNameChange((newName) => setScreenNameState(newName))
    }, [user.value])

    const [queue, setQueue] = useState([])
    const [loading, setLoading] = useState(!!eventId)
    const [currentRound, setCurrentRound] = useState(1)
    const [liveEventStatus, setLiveEventStatus] = useState(eventStatus || null)
    const [error, setError] = useState('')
    const [noEvent, setNoEvent] = useState(!eventId)
    const [joinCodeInput, setJoinCodeInput] = useState('')
    const [joining, setJoining] = useState(false)
    const [alertsEnabled, setAlertsEnabled] = useState(() => areStageAlertsEnabled())

    useEffect(() => {
        setLiveEventStatus(eventStatus || null)
    }, [eventStatus, eventId])

    const effectiveEventStatus = liveEventStatus || eventStatus || null

    useEffect(() => {
        if (!isOpen || effectiveEventStatus === 'ended') {
            releaseScreenWakeLock()
            return
        }
        requestScreenWakeLock()
        return () => releaseScreenWakeLock()
    }, [isOpen, effectiveEventStatus])

    const targetKey = eventId || songbookId
    const activeTargetKeyRef = useRef(targetKey)
    activeTargetKeyRef.current = targetKey

    const [prevTargetKey, setPrevTargetKey] = useState(targetKey)
    const [myRequests, setMyRequests] = useState(() => getSingerRequests(targetKey))

    if (prevTargetKey !== targetKey) {
        setPrevTargetKey(targetKey)
        const fresh = getSingerRequests(targetKey)
        setMyRequests(fresh)
    }

    useEffect(() => {
        setMyRequests(getSingerRequests(targetKey))
        return onSingerRequestsChange(targetKey, (latest) => {
            setMyRequests(latest)
        })
    }, [targetKey])

    useEffect(() => {
        if (isOpen) {
            setMyRequests(getSingerRequests(targetKey))
        }
    }, [isOpen, targetKey])

    const { active: activeRequests, completed: completedRequests, declined: declinedRequests } = filterSingerRequests(myRequests)

    const dismissDeclinedRequest = (reqId) => {
        const currentKey = activeTargetKeyRef.current
        if (!currentKey) return
        const next = removeSingerRequest(currentKey, reqId)
        setMyRequests(next)
    }

    const endpoint = isOpen && eventId ? `/events/${eventId}/queue` : null

    useEffect(() => {
        if (!isOpen) return
        if (!eventId) {
            setNoEvent(true)
            setQueue([])
            setError('')
            setLoading(false)
        } else {
            setNoEvent(false)
            setQueue([])
            setError('')
            setLoading(true)
        }
    }, [isOpen, eventId])

    const handleQueueData = useCallback(async (data) => {
        if (!data) return
        const currentKey = activeTargetKeyRef.current
        if (!currentKey) return
        if (eventId && data.event_id && data.event_id !== eventId) return

        if (Number.isInteger(data.current_round)) setCurrentRound(data.current_round)
        if (data.event_status) {
            setLiveEventStatus(data.event_status)
            if (eventId && data.event_status !== eventStatus) {
                onEventStatusChange?.(data.event_status)
            }
        }
        if (data.status === 'no_event') {
            setNoEvent(true)
            setQueue([])
        } else {
            setNoEvent(false)
            setQueue(data.queue || [])
        }
        setError('')
        setLoading(false)

        // Atomically reconcile against latest storage so out-of-band submissions are never lost
        const updated = updateSingerRequests(currentKey, (stored) => {
            return reconcileSingerRequests(stored, data.queue || [])
        })
        setMyRequests(updated)

        // Check pending requests directly to detect host rejection or acceptance
        const pendingReqs = updated.filter(r => r.status === 'pending' && r.cancel_token && !(data.queue || []).some(q => q.id === r.id))
        if (eventId && pendingReqs.length > 0) {
            for (const preq of pendingReqs) {
                try {
                    const reqStatus = await api.get(`/events/${eventId}/requests/${preq.id}?cancel_token=${encodeURIComponent(preq.cancel_token)}`)
                    if (reqStatus && reqStatus.status && reqStatus.status !== 'pending') {
                        const next = updateSingerRequests(currentKey, (curr) => {
                            return curr.map(r => r.id === preq.id ? { ...r, status: reqStatus.status, decline_reason: reqStatus.decline_reason || '' } : r)
                        })
                        if (activeTargetKeyRef.current === currentKey) {
                            setMyRequests(next)
                        }
                    }
                } catch {
                    // Keep current pending status if check fails
                }
            }
        }
    }, [eventId, eventStatus, songbookId, onEventJoined, onEventStatusChange])

    const { refresh: refreshQueue } = useQueuePolling({
        endpoint,
        enabled: isOpen && !!endpoint,
        interval: 5000,
        onData: handleQueueData,
        onError: () => {
            setError('Could not refresh live queue')
            setLoading(false)
        },
    })

    const handleJoin = async (e) => {
        e.preventDefault()
        const code = joinCodeInput.trim().toUpperCase()
        if (!code) return
        setJoining(true)
        try {
            const eventData = await api.get(`/join/${encodeURIComponent(code)}`)
            if (!eventData || !eventData.id) {
                showToast(`Show "${code}" not found or has ended`, 'error')
                return
            }
            if (onEventJoined) {
                onEventJoined(eventData)
            }
            if (eventData.status) {
                setLiveEventStatus(eventData.status)
            }
            showToast(getShowStatusInfo(eventData).joinToast(eventData.name))
            setNoEvent(false)
        } catch {
            showToast(`Could not find show "${code}". Ask your host for tonight's code.`, 'error')
        } finally {
            setJoining(false)
        }
    }

    useEffect(() => {
        if (!isOpen) {
            setLoading(true)
            return
        }
        const onKeyDown = (e) => {
            if (e.key === 'Escape') {
                e.stopPropagation()
                onClose?.()
            }
        }
        window.addEventListener('keydown', onKeyDown)
        return () => {
            window.removeEventListener('keydown', onKeyDown)
        }
    }, [isOpen, onClose])

    const cancelMyRequest = async (reqId) => {
        const originatingKey = targetKey
        if (!originatingKey || !eventId) return
        const originatingStored = getSingerRequests(originatingKey)
        const myReq = originatingStored.find(r => r.id === reqId)
        if (!myReq) return
        const token = myReq.cancel_token || ''
        const tokenParam = token ? `?cancel_token=${encodeURIComponent(token)}` : ''
        try {
            await api.delete(`/events/${eventId}/requests/${reqId}${tokenParam}`)
            const nextOriginating = removeSingerRequest(originatingKey, reqId)
            if (activeTargetKeyRef.current === originatingKey) {
                showToast('Request cancelled')
                setMyRequests(nextOriginating)
                refreshQueue()
            }
        } catch {
            if (activeTargetKeyRef.current === originatingKey) {
                showToast('Failed to cancel request', 'error')
            }
        }
    }

    const toggleAlerts = async () => {
        if (alertsEnabled) {
            setStageAlertsEnabled(false)
            setAlertsEnabled(false)
            showToast('🔕 Stage alerts turned off for this device.', 'info')
        } else {
            const res = await requestNotificationPermission()
            if (res === 'granted') {
                setStageAlertsEnabled(true)
                setAlertsEnabled(true)
                showToast('🔔 Stage alerts enabled! We will buzz your device when called to the stage.')
            } else {
                showToast('Notification permission was not granted by your browser', 'info')
            }
        }
    }

    const myUpNext = effectiveEventStatus !== 'ended' && myRequests.some(r => r.status === 'up_next')

    return {
        screenName,
        setScreenNameState,
        showScreenNameModal,
        setShowScreenNameModal,
        queue,
        loading,
        currentRound,
        eventStatus: effectiveEventStatus,
        error,
        noEvent,
        joinCodeInput,
        setJoinCodeInput,
        joining,
        alertsEnabled,
        toggleAlerts,
        myRequests,
        setMyRequests,
        activeRequests,
        completedRequests,
        declinedRequests,
        myUpNext,
        dismissDeclinedRequest,
        cancelMyRequest,
        handleJoin,
        refreshQueue,
    }
}
