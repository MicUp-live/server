import { api } from '../api/client'
import { setScreenName } from '../utils/screenNameStore'
import { updatePreferredKey } from '../utils/favoritesStore'
import {
    requestNotificationPermission,
    areStageAlertsEnabled,
    setStageAlertsEnabled,
} from '../utils/notifications'
import { addSingerRequest } from '../utils/singerRequestsStore'
import { getShowStatusInfo } from '../utils/showStatus'

export function getInitialStageAlerts() {
    if (typeof window === 'undefined' || !('Notification' in window)) return false
    return areStageAlertsEnabled() || Notification.permission !== 'denied'
}

export function resolveSongDisplay({ song, isWriteIn, selectedVersionId, writeInTitle, writeInArtist }) {
    const selectedVersion = (!isWriteIn && selectedVersionId && song?.versions?.find(v => (v.song_id || v.id) === selectedVersionId)) || null
    const titleText = isWriteIn
        ? writeInTitle.trim()
        : (selectedVersion?.title || selectedVersion?.label || song?.title || song?.label || '')
    const artistText = isWriteIn
        ? writeInArtist.trim()
        : (selectedVersion?.artist || song?.artist || '')
    return { titleText, artistText }
}

export function computeCooldownRemaining(eventId, eventDelay) {
    if (!eventId || !eventDelay) return 0
    const lastSubTimeStr = localStorage.getItem(`micup_last_sub_${eventId}`)
    if (!lastSubTimeStr) return 0
    const lastSubTime = Number.parseInt(lastSubTimeStr, 10)
    const elapsedSec = Math.floor((Date.now() - lastSubTime) / 1000)
    const remaining = eventDelay - elapsedSec
    return remaining > 0 ? remaining : 0
}

export async function resolveActiveEventId({ eventId, joinCodeInput, onEventJoined, setResolvingCode }) {
    if (eventId) return { activeEventId: eventId, error: '' }

    const cleanCode = joinCodeInput.trim().toUpperCase()
    if (!cleanCode) {
        return {
            activeEventId: null,
            error: "A queue cannot be started without an event. Please enter tonight's Show Code.",
        }
    }

    setResolvingCode(true)
    try {
        const eventData = await api.get(`/join/${encodeURIComponent(cleanCode)}`)
        if (!eventData?.id) {
            return { activeEventId: null, error: `Show code "${cleanCode}" not found or has ended.` }
        }
        onEventJoined?.(eventData)
        const joinedInfo = getShowStatusInfo(eventData)
        if (joinedInfo.isPaused || joinedInfo.isEnded) {
            return { activeEventId: null, error: joinedInfo.requestBlockedMessage }
        }
        return { activeEventId: eventData.id, error: '' }
    } catch {
        return {
            activeEventId: null,
            error: `Could not find show "${cleanCode}". Ask your host for tonight's Show Code.`,
        }
    } finally {
        setResolvingCode(false)
    }
}

export function validateRequestInputs({ trimmedName, isWriteIn, titleText }) {
    if (!trimmedName) return 'Please enter your stage or real name'
    if (isWriteIn && !titleText) return 'Please enter a song title'
    return ''
}

export function buildRequestPayload({
    isWriteIn,
    selectedVersionId,
    song,
    titleText,
    artistText,
    trimmedName,
    allowKeyChange,
    keyChange,
    notes,
}) {
    return {
        song_id: isWriteIn ? '' : (selectedVersionId || song.song_id || song.id || ''),
        is_write_in: isWriteIn,
        song_title: titleText,
        song_artist: artistText,
        singer_name: trimmedName,
        key_change: allowKeyChange ? keyChange : 0,
        notes: notes.trim(),
    }
}

export function persistSubmissionSideEffects({
    activeEventId,
    created,
    trimmedName,
    isFav,
    song,
    titleText,
    artistText,
    keyChange,
    isWriteIn,
    eventDelay,
    stageAlerts,
}) {
    setScreenName(trimmedName)

    if (isFav) {
        updatePreferredKey(song?.artist || artistText, song?.title || song?.label || titleText, keyChange)
    }

    try {
        addSingerRequest(activeEventId, {
            id: created.id,
            cancel_token: created.cancel_token,
            edit_token: created.edit_token,
            song_title: titleText,
            song_artist: artistText,
            is_write_in: isWriteIn,
            key_change: keyChange,
            status: created.status,
            created_at: created.created_at,
        })
    } catch (storageErr) {
        console.warn('Failed to save to singer requests store', storageErr)
    }

    if (eventDelay > 0) {
        localStorage.setItem(`micup_last_sub_${activeEventId}`, Date.now().toString())
    }

    if (stageAlerts) {
        if (typeof window !== 'undefined' && 'Notification' in window && Notification.permission !== 'denied') {
            requestNotificationPermission().catch(() => {})
        }
        setStageAlertsEnabled(true)
    } else {
        setStageAlertsEnabled(false)
    }
}

export function handleSubmitError({ err, activeEventId, eventDelay, setCooldownRemaining, onEventStatusChange }) {
    const detail = err.data?.detail || err.message || 'Failed to submit request'
    if (err.status === 429 || err.data?.error === 'burst_protection') {
        const retryAfter = err.data?.retry_after || 60
        setCooldownRemaining(retryAfter)
        localStorage.setItem(`micup_last_sub_${activeEventId}`, (Date.now() - (eventDelay - retryAfter) * 1000).toString())
        return err.data?.message || `Please wait ${retryAfter}s before submitting another request.`
    }
    if (err.status === 410) {
        onEventStatusChange?.('ended')
        return getShowStatusInfo('ended').requestBlockedMessage
    }
    if (err.status === 409 && /paused/i.test(detail)) {
        onEventStatusChange?.('paused')
        return getShowStatusInfo('paused').requestBlockedMessage
    }
    return detail
}

export function formatKeyPitchLabel(keyChange) {
    if (keyChange === 0) return 'Standard Pitch'
    if (keyChange > 0) return `+${keyChange} Semitones Higher`
    return `${keyChange} Semitones Lower`
}

export function getKeyButtonStyle(isSelected, value) {
    const baseStyle = {
        padding: 'var(--space-2) 0',
        minHeight: '40px',
        fontSize: 'var(--text-xs)',
        fontFamily: 'var(--font-mono)',
        fontWeight: isSelected ? 'var(--font-bold)' : 'var(--font-medium)',
        color: isSelected ? '#ffffff' : 'var(--color-text-muted)',
        background: 'transparent',
        border: '1px solid transparent',
        borderRadius: 'var(--radius-md)',
        cursor: 'pointer',
        transition: 'all var(--transition-fast)',
        boxShadow: 'none',
    }
    if (!isSelected) return baseStyle

    if (value !== 0) {
        return {
            ...baseStyle,
            background: 'var(--color-chorus)',
            border: '1px solid #fbbf24',
            boxShadow: '0 0 12px var(--color-chorus-glow)',
        }
    }

    return {
        ...baseStyle,
        background: 'var(--color-accent)',
        border: '1px solid var(--color-accent)',
        boxShadow: '0 0 12px var(--color-accent-glow)',
    }
}
