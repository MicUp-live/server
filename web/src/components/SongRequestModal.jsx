import { useState, useEffect } from 'preact/hooks'
import { api } from '../api/client'
import { showToast } from './Toast'
import { Spinner } from './Spinner'
import { user } from '../auth/store'
import { getScreenName, setScreenName } from '../utils/screenNameStore'
import { 
    getFavoriteSong, 
    toggleFavoriteSong, 
    updatePreferredKey, 
    isFavoriteSong 
} from '../utils/favoritesStore'
import {
    requestNotificationPermission,
    areStageAlertsEnabled,
    setStageAlertsEnabled,
} from '../utils/notifications'
import { addSingerRequest } from '../utils/singerRequestsStore'
import { SongVersionPicker } from './SongVersionPicker'
import { WriteInSongFields, StageAlertsOptIn } from './WriteInSongFields'
import { ShowStatusBanner } from './ShowStatusBanner'
import { getShowStatusInfo } from '../utils/showStatus'


const KEY_OPTIONS = [
    { value: -3, label: '-3 ♭' },
    { value: -2, label: '-2 ♭' },
    { value: -1, label: '-1 ♭' },
    { value: 0, label: 'Standard' },
    { value: 1, label: '+1 ♯' },
    { value: 2, label: '+2 ♯' },
    { value: 3, label: '+3 ♯' },
]

export function SongRequestModal({ song, songbookId, eventId, eventStatus, eventDelay = 0, eventRotationMode, allowKeyChange = true, isOpen, onClose, onRequestSubmitted, onEventJoined, onEventStatusChange }) {
    const statusInfo = getShowStatusInfo(eventStatus)
    const [singerName, setSingerName] = useState(() => getScreenName(user.value))
    const [joinCodeInput, setJoinCodeInput] = useState('')
    const [resolvingCode, setResolvingCode] = useState(false)
    const [keyChange, setKeyChange] = useState(0)
    const [selectedVersionId, setSelectedVersionId] = useState('')
    const [writeInTitle, setWriteInTitle] = useState('')
    const [writeInArtist, setWriteInArtist] = useState('')
    const [notes, setNotes] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [error, setError] = useState('')
    const [isFav, setIsFav] = useState(false)
    const [cooldownRemaining, setCooldownRemaining] = useState(0)
    const [stageAlerts, setStageAlerts] = useState(() => {
        if (typeof window === 'undefined' || !('Notification' in window)) return false
        return areStageAlertsEnabled() || Notification.permission !== 'denied'
    })


    useEffect(() => {
        if (isOpen) {
            setSingerName(prev => (prev?.trim() ? prev : getScreenName(user.value)))
        }
    }, [isOpen])

    useEffect(() => {
        if (eventStatus === 'active') setError('')
    }, [eventStatus])

    const isWriteIn = !!song?.is_write_in
    const selectedVersion = (!isWriteIn && selectedVersionId && song?.versions?.find(v => (v.song_id || v.id) === selectedVersionId)) || null
    const titleText = isWriteIn ? writeInTitle.trim() : (selectedVersion?.title || selectedVersion?.label || song?.title || song?.label || '')
    const artistText = isWriteIn ? writeInArtist.trim() : (selectedVersion?.artist || song?.artist || '')

    // Calculate initial cooldown from last submission timestamp
    useEffect(() => {
        if (!isOpen) return
        const checkCooldown = () => {
            if (!eventId || !eventDelay) {
                setCooldownRemaining(0)
                return
            }
            const lastSubTimeStr = localStorage.getItem(`micup_last_sub_${eventId}`)
            if (lastSubTimeStr) {
                const lastSubTime = parseInt(lastSubTimeStr, 10)
                const elapsedSec = Math.floor((Date.now() - lastSubTime) / 1000)
                const remaining = eventDelay - elapsedSec
                setCooldownRemaining(remaining > 0 ? remaining : 0)
            } else {
                setCooldownRemaining(0)
            }
        }

        checkCooldown()
        const timer = setInterval(checkCooldown, 1000)
        return () => clearInterval(timer)
    }, [isOpen, eventId, eventDelay])

    useEffect(() => {
        if (!song) return
        const fav = getFavoriteSong(song.artist, song.title || song.label)
        const favExists = !!fav
        setIsFav(favExists)
        if (favExists && typeof fav.preferred_key === 'number') {
            setKeyChange(fav.preferred_key)
        } else {
            setKeyChange(0)
        }
        setNotes('')
        setSelectedVersionId('')
        setWriteInTitle(song.title || song.label || '')
        setWriteInArtist(song.artist || '')
        setError('')
    }, [song])

    useEffect(() => {
        if (!isOpen || !song) return
        const handleFavChanged = () => {
            setIsFav(isFavoriteSong(song.artist, song.title || song.label))
        }
        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                e.stopPropagation()
                onClose?.()
            }
        }
        window.addEventListener('micup_favorites_changed', handleFavChanged)
        window.addEventListener('keydown', handleKeyDown)
        return () => {
            window.removeEventListener('micup_favorites_changed', handleFavChanged)
            window.removeEventListener('keydown', handleKeyDown)
        }
    }, [isOpen, song])

    const handleToggleFavorite = () => {
        const favTitle = song?.title || song?.label || titleText
        const favArtist = song?.artist || artistText
        const next = toggleFavoriteSong({ title: favTitle, artist: favArtist, genre: song.genre }, keyChange)
        setIsFav(next)
        showToast(next ? `★ Added "${favTitle}" to your Favorites!` : `Removed "${favTitle}" from Favorites`)
    }

    const handleKeySelect = (val) => {
        setKeyChange(val)
        if (isFav) {
            updatePreferredKey(song?.artist || artistText, song?.title || song?.label || titleText, val)
        }
    }

    const handleSubmit = async (e) => {
        e.preventDefault()
        if (statusInfo.isPaused || statusInfo.isEnded) {
            setError(statusInfo.requestBlockedMessage)
            return
        }

        let activeEventId = eventId
        if (!activeEventId) {
            const cleanCode = joinCodeInput.trim().toUpperCase()
            if (!cleanCode) {
                setError('A queue cannot be started without an event. Please enter tonight\'s Show Code.')
                return
            }
            setResolvingCode(true)
            try {
                const eventData = await api.get(`/join/${encodeURIComponent(cleanCode)}`)
                if (!eventData || !eventData.id) {
                    setError(`Show code "${cleanCode}" not found or has ended.`)
                    return
                }
                activeEventId = eventData.id
                onEventJoined?.(eventData)
                const joinedInfo = getShowStatusInfo(eventData)
                if (joinedInfo.isPaused || joinedInfo.isEnded) {
                    setError(joinedInfo.requestBlockedMessage)
                    return
                }
            } catch (err) {
                setError(`Could not find show "${cleanCode}". Ask your host for tonight's Show Code.`)
                return
            } finally {
                setResolvingCode(false)
            }
        }

        const trimmedName = singerName.trim()
        if (!trimmedName) {
            setError('Please enter your stage or real name')
            return
        }

        if (isWriteIn && !titleText) {
            setError('Please enter a song title')
            return
        }
        setSubmitting(true)
        setError('')
        try {
            const payload = {
                song_id: isWriteIn ? '' : (selectedVersionId || song.song_id || song.id || ''),
                is_write_in: isWriteIn,
                song_title: titleText,
                song_artist: artistText,
                singer_name: trimmedName,
                key_change: allowKeyChange ? keyChange : 0,
                notes: notes.trim(),
            }

            const endpoint = `/events/${activeEventId}/requests`
            const created = await api.post(endpoint, payload)

            // Persist screen name for next time and dispatch event
            setScreenName(trimmedName)

            // Persist preferred key for favorite song
            if (isFav) {
                updatePreferredKey(song?.artist || artistText, song?.title || song?.label || titleText, keyChange)
            }

            // Save to singer's local active requests
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

            // Record submission timestamp for delay cooldown
            if (eventDelay > 0) {
                localStorage.setItem(`micup_last_sub_${activeEventId}`, Date.now().toString())
            }

            // Request or persist stage alerts preference during this direct user gesture
            if (stageAlerts) {
                if (typeof window !== 'undefined' && 'Notification' in window && Notification.permission !== 'denied') {
                    requestNotificationPermission().catch(() => {})
                }
                setStageAlertsEnabled(true)
            } else {
                setStageAlertsEnabled(false)
            }

            showToast(`Request sent to host: "${titleText}"!`)

            if (onRequestSubmitted) {
                onRequestSubmitted(created)
            }
            onClose()
        } catch (err) {
            const detail = err.data?.detail || err.message || 'Failed to submit request'
            if (err.status === 429 || err.data?.error === 'burst_protection') {
                const retryAfter = err.data?.retry_after || 60
                setCooldownRemaining(retryAfter)
                localStorage.setItem(`micup_last_sub_${activeEventId}`, (Date.now() - (eventDelay - retryAfter) * 1000).toString())
                setError(err.data?.message || `Please wait ${retryAfter}s before submitting another request.`)
            } else if (err.status === 410) {
                onEventStatusChange?.('ended')
                setError(getShowStatusInfo('ended').requestBlockedMessage)
            } else if (err.status === 409 && /paused/i.test(detail)) {
                onEventStatusChange?.('paused')
                setError(getShowStatusInfo('paused').requestBlockedMessage)
            } else {
                setError(detail)
            }
        } finally {
            setSubmitting(false)
        }
    }

    const addQuickNote = (noteSnippet) => {
        setNotes(prev => prev ? `${prev}, ${noteSnippet}` : noteSnippet)
    }

    if (!isOpen || !song) return null

    return (
        <div 
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="song-request-title"
        >
            <div 
                class="modal-sheet"
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true"></div>
                {/* Header with Hot Mic stage glow */}
                <div style={{
                    padding: 'var(--space-4) var(--space-6)',
                    background: 'linear-gradient(180deg, rgba(255, 42, 95, 0.12) 0%, transparent 100%)',
                    borderBottom: '1px solid var(--color-border)',
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'flex-start',
                    flexShrink: 0,
                }}>
                    <div>
                        <div style={{
                            fontSize: 'var(--text-xs)',
                            fontWeight: 'var(--font-bold)',
                            color: 'var(--color-accent)',
                            textTransform: 'uppercase',
                            letterSpacing: '0.08em',
                            marginBottom: 'var(--space-1)',
                            display: 'flex',
                            alignItems: 'center',
                            gap: 'var(--space-2)'
                        }}>
                            <span>🎤 Step Up To The Mic</span>
                        </div>
                        <h3 id="song-request-title" style={{
                            margin: 0,
                            fontSize: 'var(--text-xl)',
                            fontWeight: 'var(--font-bold)',
                            color: 'var(--color-text)',
                            lineHeight: 1.2,
                        }}>
                            {isWriteIn ? (titleText || 'Custom Write-In Request') : titleText}
                        </h3>
                        {!isWriteIn && artistText && (
                            <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: '2px' }}>
                                {artistText}
                            </div>
                        )}
                        {!isWriteIn && <div style={{ marginTop: 'var(--space-2)' }}>
                            <button
                                type="button"
                                onClick={handleToggleFavorite}
                                style={{
                                    background: isFav ? 'rgba(245, 158, 11, 0.15)' : 'rgba(255, 255, 255, 0.05)',
                                    border: `1px solid ${isFav ? 'var(--color-chorus)' : 'var(--color-border)'}`,
                                    color: isFav ? 'var(--color-chorus)' : 'var(--color-text-muted)',
                                    borderRadius: 'var(--radius-full)',
                                    padding: '4px 12px',
                                    minHeight: '36px',
                                    fontSize: 'var(--text-xs)',
                                    fontWeight: 'var(--font-semibold)',
                                    cursor: 'pointer',
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    gap: '5px',
                                    transition: 'all 0.15s ease'
                                }}
                            >
                                <span>{isFav ? '★ Favorited Track' : '☆ Save to Favorites'}</span>
                                {isFav && keyChange !== 0 && (
                                    <span style={{ fontSize: '0.85em', opacity: 0.9 }}>
                                        ({keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`})
                                    </span>
                                )}
                            </button>
                        </div>}
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close"
                        style={{
                            minWidth: 'var(--touch-target-min, 48px)',
                            minHeight: 'var(--touch-target-min, 48px)',
                            background: 'none',
                            border: 'none',
                            color: 'var(--color-text-dim)',
                            fontSize: '1.4rem',
                            cursor: 'pointer',
                            padding: 0,
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            borderRadius: 'var(--radius-full)',
                            lineHeight: 1,
                        }}
                    >
                        ✕
                    </button>
                </div>

                <form
                    onSubmit={handleSubmit}
                    style={{
                        display: 'flex',
                        flexDirection: 'column',
                        flex: 1,
                        minHeight: 0,
                        overflow: 'hidden',
                    }}
                >
                    <div
                        style={{
                            padding: 'var(--space-6)',
                            flex: 1,
                            overflowY: 'auto',
                            WebkitOverflowScrolling: 'touch',
                        }}
                    >
                        <ShowStatusBanner status={eventStatus} compact />
                        {error && !statusInfo.isPaused && !statusInfo.isEnded && (
                            <div style={{
                                padding: 'var(--space-3) var(--space-4)',
                                background: 'rgba(239, 68, 68, 0.15)',
                                border: '1px solid var(--color-danger)',
                                borderRadius: 'var(--radius-md)',
                                color: '#fca5a5',
                                fontSize: 'var(--text-sm)',
                                marginBottom: 'var(--space-4)',
                            }}>
                                {error}
                            </div>
                        )}

                    {/* Event Required: If singer is requesting from a browse-only catalog, require show code */}
                    {!eventId && (
                        <div style={{
                            background: 'rgba(255, 42, 95, 0.08)',
                            border: '1px solid var(--color-accent)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-4)',
                            marginBottom: 'var(--space-5)',
                        }}>
                            <div style={{
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'var(--font-bold)',
                                color: 'var(--color-accent)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.08em',
                                marginBottom: '4px',
                            }}>
                                Tonight's Show Code Required
                            </div>
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-2)' }}>
                                A queue requires an active event. Enter tonight's Show Code from your KJ to join:
                            </div>
                            <input
                                type="text"
                                placeholder="e.g. OMALLEY"
                                value={joinCodeInput}
                                onInput={e => setJoinCodeInput(e.target.value.toUpperCase())}
                                maxLength={16}
                                required
                                style={{
                                    width: '100%',
                                    fontFamily: 'var(--font-mono)',
                                    fontWeight: 'bold',
                                    textTransform: 'uppercase',
                                    letterSpacing: '0.08em',
                                    fontSize: 'var(--text-base)',
                                    padding: 'var(--space-2) var(--space-3)',
                                }}
                            />
                        </div>
                    )}

                    {/* Write-in Song Title & Artist */}
                    {isWriteIn && (
                        <WriteInSongFields
                            writeInTitle={writeInTitle}
                            setWriteInTitle={setWriteInTitle}
                            writeInArtist={writeInArtist}
                            setWriteInArtist={setWriteInArtist}
                        />
                    )}

                    {/* Singer Screen Name */}
                    <div style={{ marginBottom: 'var(--space-5)' }}>
                        <label 
                            htmlFor="request-singer-name"
                            style={{
                                display: 'block',
                                fontSize: 'var(--text-sm)',
                                fontWeight: 'var(--font-semibold)',
                                color: 'var(--color-text)',
                                marginBottom: 'var(--space-2)',
                            }}
                        >
                            Your Screen Name / Stage Name <span style={{ color: 'var(--color-accent)' }}>*</span>
                        </label>
                        <input
                            id="request-singer-name"
                            type="text"
                            placeholder="e.g. Jordan or Freddie M."
                            value={singerName}
                            onInput={e => setSingerName(e.target.value)}
                            required
                            maxLength={60}
                            autoFocus
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-base)',
                                padding: 'var(--space-3) var(--space-4)',
                            }}
                        />
                        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', display: 'block', marginTop: '4px' }}>
                            Name shown on the host's live queue and TV stage screen
                        </span>
                    </div>

                    {/* Key Change Pitch Adjust (Configurable per Event) */}
                    {allowKeyChange && (
                        <div style={{ marginBottom: 'var(--space-5)' }}>
                            <div style={{
                                display: 'flex',
                                justifyContent: 'space-between',
                                alignItems: 'center',
                                marginBottom: 'var(--space-2)'
                            }}>
                                <label style={{
                                    fontSize: 'var(--text-sm)',
                                    fontWeight: 'var(--font-semibold)',
                                    color: 'var(--color-text)',
                                }}>
                                    Key Pitch Adjustment
                                </label>
                                <span style={{
                                    fontSize: 'var(--text-xs)',
                                    color: keyChange !== 0 ? 'var(--color-chorus)' : 'var(--color-text-dim)',
                                    fontWeight: 'var(--font-bold)'
                                }}>
                                    {keyChange === 0 ? 'Standard Pitch' : (keyChange > 0 ? `+${keyChange} Semitones Higher` : `${keyChange} Semitones Lower`)}
                                </span>
                            </div>
                            <div style={{
                                display: 'grid',
                                gridTemplateColumns: 'repeat(7, 1fr)',
                                gap: 'var(--space-1)',
                                background: 'var(--color-surface-alt)',
                                padding: 'var(--space-1)',
                                borderRadius: 'var(--radius-lg)',
                                border: '1px solid var(--color-border)',
                            }}>
                                {KEY_OPTIONS.map(opt => {
                                    const isSelected = keyChange === opt.value
                                    return (
                                        <button
                                            key={opt.value}
                                            type="button"
                                            onClick={() => handleKeySelect(opt.value)}
                                            style={{
                                                padding: 'var(--space-2) 0',
                                                minHeight: '40px',
                                                fontSize: 'var(--text-xs)',
                                                fontFamily: 'var(--font-mono)',
                                                fontWeight: isSelected ? 'var(--font-bold)' : 'var(--font-medium)',
                                                color: isSelected ? '#ffffff' : 'var(--color-text-muted)',
                                                background: isSelected ? (opt.value !== 0 ? 'var(--color-chorus)' : 'var(--color-accent)') : 'transparent',
                                                border: isSelected ? (opt.value !== 0 ? '1px solid #fbbf24' : '1px solid var(--color-accent)') : '1px solid transparent',
                                                borderRadius: 'var(--radius-md)',
                                                cursor: 'pointer',
                                                transition: 'all var(--transition-fast)',
                                                boxShadow: isSelected ? (opt.value !== 0 ? '0 0 12px var(--color-chorus-glow)' : '0 0 12px var(--color-accent-glow)') : 'none',
                                            }}
                                        >
                                            {opt.label}
                                        </button>
                                    )
                                })}
                            </div>
                        </div>
                    )}

                    {/* Note to Host */}
                    <div style={{ marginBottom: 'var(--space-6)' }}>
                        <label 
                            htmlFor="request-notes"
                            style={{
                                display: 'block',
                                fontSize: 'var(--text-sm)',
                                fontWeight: 'var(--font-semibold)',
                                color: 'var(--color-text)',
                                marginBottom: 'var(--space-2)',
                            }}
                        >
                            Note to Host <span style={{ color: 'var(--color-text-dim)', fontWeight: 'var(--font-normal)' }}>(optional)</span>
                        </label>
                        <input
                            id="request-notes"
                            type="text"
                            placeholder="e.g. Duet with Taylor, birthday shoutout..."
                            value={notes}
                            onInput={e => setNotes(e.target.value)}
                            maxLength={200}
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-sm)',
                                padding: 'var(--space-3) var(--space-4)',
                                marginBottom: 'var(--space-2)'
                            }}
                        />
                        {/* Quick tags */}
                        <div style={{ display: 'flex', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                            <button
                                type="button"
                                onClick={() => addQuickNote('Duet')}
                                style={{
                                    background: 'var(--color-surface-alt)',
                                    border: '1px solid var(--color-border)',
                                    color: 'var(--color-text-dim)',
                                    borderRadius: 'var(--radius-full)',
                                    fontSize: 'var(--text-xs)',
                                    padding: '2px 8px',
                                    cursor: 'pointer'
                                }}
                            >
                                + Duet
                            </button>
                            <button
                                type="button"
                                onClick={() => addQuickNote('Birthday girl!')}
                                style={{
                                    background: 'var(--color-surface-alt)',
                                    border: '1px solid var(--color-border)',
                                    color: 'var(--color-text-dim)',
                                    borderRadius: 'var(--radius-full)',
                                    fontSize: 'var(--text-xs)',
                                    padding: '2px 8px',
                                    cursor: 'pointer'
                                }}
                            >
                                + Birthday!
                            </button>
                            <button
                                type="button"
                                onClick={() => addQuickNote('1st time singing')}
                                style={{
                                    background: 'var(--color-surface-alt)',
                                    border: '1px solid var(--color-border)',
                                    color: 'var(--color-text-dim)',
                                    borderRadius: 'var(--radius-full)',
                                    fontSize: 'var(--text-xs)',
                                    padding: '2px 8px',
                                    cursor: 'pointer'
                                }}
                            >
                                + 1st Time
                            </button>
                        </div>
                    </div>
                    <SongVersionPicker versions={song?.versions} selectedVersionId={selectedVersionId} onSelectVersionId={setSelectedVersionId} />
                    <StageAlertsOptIn stageAlerts={stageAlerts} setStageAlerts={setStageAlerts} />
                    </div>

                    {/* Actions */}
                    <div style={{
                        padding: 'var(--space-4) var(--space-6)',
                        borderTop: '1px solid var(--color-border)',
                        background: 'var(--color-surface)',
                        display: 'flex',
                        gap: 'var(--space-3)',
                        justifyContent: 'flex-end',
                        alignItems: 'center',
                        flexShrink: 0,
                    }}>
                        <button
                            type="button"
                            class="btn btn-secondary"
                            onClick={onClose}
                            disabled={submitting}
                        >
                            Cancel
                        </button>
                        <button
                            type="submit"
                            class="btn btn-primary"
                            disabled={submitting || resolvingCode || cooldownRemaining > 0 || statusInfo.isPaused || statusInfo.isEnded}
                            style={{ minWidth: '150px' }}
                        >
                            {resolvingCode ? (
                                <Spinner size="sm" label="Connecting..." inline />
                            ) : submitting ? (
                                <Spinner size="sm" label="Sending..." inline />
                            ) : (statusInfo.isPaused || statusInfo.isEnded) ? (
                                statusInfo.submitButtonLabel
                            ) : cooldownRemaining > 0 ? (
                                <span>⏱️ Wait {cooldownRemaining}s</span>
                            ) : (
                                '🎤 Send Request'
                            )}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
