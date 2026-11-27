import { api } from '../../api/client.js'
import { showToast } from '../../components/Toast.jsx'
import { getSingerRequests } from '../../utils/singerRequestsStore.js'
import {
    sendSingerNotification,
    requestScreenWakeLock,
    areStageAlertsEnabled,
} from '../../utils/notifications.js'

function getEventParam() {
    if (typeof window === 'undefined') return ''
    const params = new URLSearchParams(window.location.search)
    return params.get('event') || params.get('eventId') || ''
}

async function fetchEventByPath(path) {
    const eventData = await api.get(path)
    return {
        eventData,
        songbookId: eventData.public_songbook_id || eventData.songbook_id,
    }
}

async function fetchOptionalEventContext(eventParam, id) {
    try {
        const eventData = await api.get(`/events/${encodeURIComponent(eventParam)}/public`)
        return {
            eventData,
            songbookId: id || eventData?.public_songbook_id || eventData?.songbook_id || '',
        }
    } catch {
        return { eventData: null, songbookId: id || '' }
    }
}

export function fetchInitialBookContext({ id, code, eventId, eventMode }) {
    const directEventId = eventId || (eventMode ? id : '')
    if (directEventId) {
        return fetchEventByPath(`/events/${encodeURIComponent(directEventId)}/public`)
    }
    if (code) {
        return fetchEventByPath(`/join/${encodeURIComponent(code.toUpperCase())}`)
    }
    const eventParam = getEventParam()
    if (eventParam) {
        return fetchOptionalEventContext(eventParam, id)
    }
    return { eventData: null, songbookId: id || '' }
}

export function formatLoadError(err, code) {
    if (err?.status === 404) {
        return code ? `Show "${code}" not found or has ended` : 'Songbook or show not found'
    }
    return err?.message || 'Failed to load songs'
}

export function hasEventMetadataUpdate(data) {
    return Boolean(
        data.event_status ||
        data.announcement !== undefined ||
        data.tip_url !== undefined ||
        data.rotation_mode !== undefined
    )
}

export function mergeEventQueueUpdate(prev, data) {
    if (!prev) return prev
    return {
        ...prev,
        status: data.event_status || prev.status,
        announcement: data.announcement !== undefined ? data.announcement : prev.announcement,
        tip_url: data.tip_url !== undefined ? data.tip_url : prev.tip_url,
        rotation_mode: data.rotation_mode !== undefined ? data.rotation_mode : prev.rotation_mode,
        auto_accept: data.auto_accept !== undefined ? data.auto_accept : prev.auto_accept,
        submission_delay: data.submission_delay !== undefined ? data.submission_delay : prev.submission_delay,
    }
}

function triggerVibration() {
    if (typeof navigator === 'undefined' || !('vibrate' in navigator)) return
    try {
        navigator.vibrate([300, 100, 300, 100, 500])
    } catch {}
}

function alertUpNext(activeMatch, buzzedSet, playChime) {
    if (buzzedSet.has(activeMatch.id)) return
    buzzedSet.add(activeMatch.id)
    if (areStageAlertsEnabled()) {
        playChime()
        triggerVibration()
        void sendSingerNotification("🎤 YOU'RE CALLED TO THE STAGE!", {
            body: `Head to the stage now for "${activeMatch.song_title || 'your anthem'}"!`,
            tag: `micup-upnext-${activeMatch.id}`,
        })
    }
    showToast("🎤 BUZZ! You're CALLED TO THE STAGE! Head to the stage!", 'info')
}

function alertOnDeck(activeMatch, buzzedSet, playChime) {
    const key = `${activeMatch.id}-ondeck`
    if (buzzedSet.has(key)) return
    buzzedSet.add(key)
    if (areStageAlertsEnabled()) {
        playChime()
        void sendSingerNotification("⚠️ YOU'RE ON DECK!", {
            body: `You are next after this song for "${activeMatch.song_title}". Head toward the stage!`,
            tag: `micup-ondeck-${activeMatch.id}`,
        })
    }
    showToast(`⚠️ You're ON DECK (1 song away) for "${activeMatch.song_title}"!`, 'info')
}

export function resolveActiveQueueStatus(queue, targetKey, buzzedSet, playChime) {
    const myReqs = getSingerRequests(targetKey)
    if (myReqs.length === 0) return null

    const activeMatch = queue.find(item => myReqs.some(m => m.id === item.id))
    if (!activeMatch) return null

    void requestScreenWakeLock()
    if (activeMatch.status === 'up_next') {
        alertUpNext(activeMatch, buzzedSet, playChime)
        return '🎤 CALLED TO STAGE!'
    }

    const index = queue.indexOf(activeMatch)
    if (index === 1) {
        alertOnDeck(activeMatch, buzzedSet, playChime)
    }
    return `You: #${index + 1} in line`
}
