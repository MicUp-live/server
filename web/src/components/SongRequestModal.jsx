import { useState, useEffect, useRef } from 'preact/hooks'
import { api } from '../api/client'
import { showToast } from './Toast'
import { user } from '../auth/store'
import { getScreenName } from '../utils/screenNameStore'
import { 
    getFavoriteSong, 
    toggleFavoriteSong, 
    updatePreferredKey, 
    isFavoriteSong 
} from '../utils/favoritesStore'
import { SongVersionPicker } from './SongVersionPicker'
import { WriteInSongFields } from './WriteInSongFields'
import { StageAlertsOptIn } from './WriteInSongFields'
import { ShowStatusBanner } from './ShowStatusBanner'
import { getShowStatusInfo } from '../utils/showStatus'
import {
    getInitialStageAlerts,
    resolveSongDisplay,
    computeCooldownRemaining,
    resolveActiveEventId,
    validateRequestInputs,
    buildRequestPayload,
    persistSubmissionSideEffects,
    handleSubmitError,
} from './songRequestHelpers'
import {
    renderSubmitButtonContent,
    SongRequestHeader,
    KeyPitchSelector,
} from './SongRequestParts'

export function SongRequestModal({ song, songbookId, eventId, eventStatus, eventDelay = 0, eventRotationMode, allowKeyChange = true, isOpen, onClose, onRequestSubmitted, onEventJoined, onEventStatusChange }) {
    const statusInfo = getShowStatusInfo(eventStatus)
    const singerNameInputRef = useRef(null)
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
    const [stageAlerts, setStageAlerts] = useState(getInitialStageAlerts)


    useEffect(() => {
        if (!isOpen) return
        setSingerName(prev => (prev?.trim() ? prev : getScreenName(user.value)))
        if (song) singerNameInputRef.current?.focus()
    }, [isOpen, song])

    useEffect(() => {
        if (eventStatus === 'active') setError('')
    }, [eventStatus])

    const isWriteIn = !!song?.is_write_in
    const { titleText, artistText } = resolveSongDisplay({
        song,
        isWriteIn,
        selectedVersionId,
        writeInTitle,
        writeInArtist,
    })

    // Calculate initial cooldown from last submission timestamp
    useEffect(() => {
        if (!isOpen) return
        const checkCooldown = () => setCooldownRemaining(computeCooldownRemaining(eventId, eventDelay))
        checkCooldown()
        const timer = setInterval(checkCooldown, 1000)
        return () => clearInterval(timer)
    }, [isOpen, eventId, eventDelay])

    useEffect(() => {
        if (!song) return
        const fav = getFavoriteSong(song.artist, song.title || song.label)
        setIsFav(!!fav)
        setKeyChange(typeof fav?.preferred_key === 'number' ? fav.preferred_key : 0)
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

        const { activeEventId, error: joinError } = await resolveActiveEventId({
            eventId,
            joinCodeInput,
            onEventJoined,
            setResolvingCode,
        })
        if (joinError) {
            setError(joinError)
            return
        }

        const trimmedName = singerName.trim()
        const validationError = validateRequestInputs({ trimmedName, isWriteIn, titleText })
        if (validationError) {
            setError(validationError)
            return
        }

        setSubmitting(true)
        setError('')
        try {
            const payload = buildRequestPayload({
                isWriteIn,
                selectedVersionId,
                song,
                titleText,
                artistText,
                trimmedName,
                allowKeyChange,
                keyChange,
                notes,
            })
            const created = await api.post(`/events/${activeEventId}/requests`, payload)
            persistSubmissionSideEffects({
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
            })
            showToast(`Request sent to host: "${titleText}"!`)
            onRequestSubmitted?.(created)
            onClose()
        } catch (err) {
            setError(handleSubmitError({
                err,
                activeEventId,
                eventDelay,
                setCooldownRemaining,
                onEventStatusChange,
            }))
        } finally {
            setSubmitting(false)
        }
    }

    const addQuickNote = (noteSnippet) => {
        setNotes(prev => prev ? `${prev}, ${noteSnippet}` : noteSnippet)
    }

    if (!isOpen || !song) return null

    return (
        <dialog
            open
            class="modal-overlay"
            aria-modal="true"
            aria-labelledby="song-request-title"
            onCancel={(e) => { e.preventDefault(); onClose() }}
            style={{ border: 'none', margin: 0, maxWidth: 'none', maxHeight: 'none', width: '100%', height: '100%' }}
        >
            <div class="modal-sheet">
                <div class="modal-drag-handle" aria-hidden="true"></div>
                <SongRequestHeader
                    isWriteIn={isWriteIn}
                    titleText={titleText}
                    artistText={artistText}
                    isFav={isFav}
                    keyChange={keyChange}
                    onToggleFavorite={handleToggleFavorite}
                    onClose={onClose}
                />

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
                            ref={singerNameInputRef}
                            id="request-singer-name"
                            type="text"
                            placeholder="e.g. Jordan or Freddie M."
                            value={singerName}
                            onInput={e => setSingerName(e.target.value)}
                            required
                            maxLength={60}
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

                    <KeyPitchSelector
                        allowKeyChange={allowKeyChange}
                        keyChange={keyChange}
                        onSelectKey={handleKeySelect}
                    />

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
                            {renderSubmitButtonContent({ resolvingCode, submitting, statusInfo, cooldownRemaining })}
                        </button>
                    </div>
                </form>
            </div>
        </dialog>
    )
}
