import { useState, useEffect, useRef, useCallback } from 'preact/hooks'
import { api } from '../../api/client.js'
import { Spinner } from '../../components/Spinner.jsx'
import { showToast } from '../../components/Toast.jsx'
import { SongRequestModal } from '../../components/SongRequestModal.jsx'
import { SingerQueueModal } from '../../components/SingerQueueModal.jsx'
import { useQueuePolling } from '../../hooks/useQueuePolling.js'
import { useCatalogSearch } from './hooks/useCatalogSearch.js'
import { SingerHeader } from './components/SingerHeader.jsx'
import { CatalogControls } from './components/CatalogControls.jsx'
import { CatalogSongsView } from './components/CatalogSongsView.jsx'
import { CatalogArtistsView } from './components/CatalogArtistsView.jsx'
import { CatalogFavoritesView } from './components/CatalogFavoritesView.jsx'
import { JoinShowModal } from './components/JoinShowModal.jsx'
import {
    getFavoriteSongs,
    toggleFavoriteSong,
    getFavoriteArtists,
    toggleFavoriteArtist,
} from '../../utils/favoritesStore.js'
import { getSingerRequests } from '../../utils/singerRequestsStore.js'
import {
    sendSingerNotification,
    requestScreenWakeLock,
    releaseScreenWakeLock,
    areStageAlertsEnabled,
} from '../../utils/notifications.js'
import { getShowStatusInfo, getStatusTransitionToast } from '../../utils/showStatus.js'

function playBuzzChime() {
    try {
        const AudioContext = window.AudioContext || window.webkitAudioContext
        if (!AudioContext) return
        const ctx = new AudioContext()
        const now = ctx.currentTime
        const osc = ctx.createOscillator()
        const gain = ctx.createGain()
        osc.type = 'triangle'
        osc.frequency.setValueAtTime(523.25, now)
        osc.frequency.setValueAtTime(659.25, now + 0.12)
        osc.frequency.setValueAtTime(783.99, now + 0.24)
        gain.gain.setValueAtTime(0.2, now)
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.6)
        osc.connect(gain)
        gain.connect(ctx.destination)
        osc.start(now)
        osc.stop(now + 0.6)
    } catch {}
}

export function PublicBook({ id, code, eventId, eventMode }) {
    const [event, setEvent] = useState(null)
    const [resolvedSongbookId, setResolvedSongbookId] = useState(id || '')
    const [loadingInfo, setLoadingInfo] = useState(true)
    const [infoError, setInfoError] = useState('')
    const [retryCount, setRetryCount] = useState(0)

    const [viewMode, setViewMode] = useState('songs') // 'songs' | 'artists' | 'favorites'
    const [favSongs, setFavSongs] = useState(() => getFavoriteSongs())
    const [favArtists, setFavArtists] = useState(() => getFavoriteArtists())

    const [selectedSongForRequest, setSelectedSongForRequest] = useState(null)
    const [isQueueOpen, setIsQueueOpen] = useState(false)
    const [queueCount, setQueueCount] = useState(0)
    const [myActiveStatus, setMyActiveStatus] = useState(null)

    const [showJoinPrompt, setShowJoinPrompt] = useState(false)
    const [joiningEvent, setJoiningEvent] = useState(false)
    const [joinError, setJoinError] = useState('')

    const buzzedRequestsRef = useRef(new Set())
    const prevEventStatusRef = useRef({ id: null, status: null })

    // Announce live show status transitions (pause, resume, end, restart) once
    useEffect(() => {
        const currentId = event?.id || null
        const currentStatus = event?.status || null
        const prev = prevEventStatusRef.current
        if (currentId && prev.id === currentId && prev.status && currentStatus && prev.status !== currentStatus) {
            const transition = getStatusTransitionToast(prev.status, currentStatus)
            if (transition) {
                showToast(transition.message, transition.type)
            }
        }
        prevEventStatusRef.current = { id: currentId, status: currentStatus }
    }, [event?.id, event?.status])

    // Load initial event and songbook context
    useEffect(() => {
        let cancelled = false
        setLoadingInfo(true)
        setInfoError('')
        setEvent(null)

        const loadInitialContext = async () => {
            try {
                let targetSongbookId = id
                let activeEvent = null

                if (eventId || (eventMode && id)) {
                    const targetEventId = eventId || id
                    const eventData = await api.get(`/events/${encodeURIComponent(targetEventId)}/public`)
                    if (cancelled) return
                    activeEvent = eventData
                    setEvent(eventData)
                    targetSongbookId = eventData.public_songbook_id || eventData.songbook_id
                } else if (code) {
                    const eventData = await api.get(`/join/${encodeURIComponent(code.toUpperCase())}`)
                    if (cancelled) return
                    activeEvent = eventData
                    setEvent(eventData)
                    targetSongbookId = eventData.public_songbook_id || eventData.songbook_id
                } else if (typeof window !== 'undefined') {
                    const params = new URLSearchParams(window.location.search)
                    const eventParam = params.get('event') || params.get('eventId')
                    if (eventParam) {
                        try {
                            const eventData = await api.get(`/events/${encodeURIComponent(eventParam)}/public`)
                            if (!cancelled && eventData) {
                                activeEvent = eventData
                                setEvent(eventData)
                                if (!targetSongbookId) {
                                    targetSongbookId = eventData.public_songbook_id || eventData.songbook_id
                                }
                            }
                        } catch {
                            // Event fetch failed, continue loading songbook
                        }
                    }
                }

                if (!targetSongbookId) {
                    throw new Error('No songbook specified')
                }
                if (!cancelled) {
                    setResolvedSongbookId(targetSongbookId)
                }
            } catch (err) {
                if (cancelled) return
                if (err.status === 404) {
                    setInfoError(code ? `Show "${code}" not found or has ended` : 'Songbook or show not found')
                } else {
                    setInfoError(err.message || 'Failed to load songs')
                }
            } finally {
                if (!cancelled) setLoadingInfo(false)
            }
        }

        loadInitialContext()
        return () => { cancelled = true }
    }, [id, code, eventId, eventMode, retryCount])

    // Sync universal favorites across tabs or components
    useEffect(() => {
        const syncFavorites = () => {
            setFavSongs(getFavoriteSongs())
            setFavArtists(getFavoriteArtists())
        }
        window.addEventListener('micup_favorites_changed', syncFavorites)
        return () => window.removeEventListener('micup_favorites_changed', syncFavorites)
    }, [])

    const handleJoinShow = async (cleanCode) => {
        setJoiningEvent(true)
        setJoinError('')
        try {
            const eventData = await api.get(`/join/${encodeURIComponent(cleanCode)}`)
            if (!eventData || !eventData.id) {
                setJoinError(`Show "${cleanCode}" not found or has ended`)
                return
            }
            setEvent(eventData)
            if (eventData.public_songbook_id || eventData.songbook_id) {
                setResolvedSongbookId(eventData.public_songbook_id || eventData.songbook_id)
            }
            setShowJoinPrompt(false)
            showToast(getShowStatusInfo(eventData).joinToast(eventData.name))
        } catch {
            setJoinError(`Show code "${cleanCode}" not found. Ask your KJ for tonight's code.`)
        } finally {
            setJoiningEvent(false)
        }
    }

    // Queue polling and stage alert management
    const queueEndpoint = event?.id ? `/events/${event.id}/queue` : null

    const handleQueueData = useCallback((data) => {
        if (!data) return
        const q = data.queue || []
        setQueueCount(q.length)

        if (event && (data.event_status || data.announcement !== undefined || data.tip_url !== undefined || data.rotation_mode !== undefined)) {
            setEvent(prev => prev ? {
                ...prev,
                status: data.event_status || prev.status,
                announcement: data.announcement !== undefined ? data.announcement : prev.announcement,
                tip_url: data.tip_url !== undefined ? data.tip_url : prev.tip_url,
                rotation_mode: data.rotation_mode !== undefined ? data.rotation_mode : prev.rotation_mode,
                auto_accept: data.auto_accept !== undefined ? data.auto_accept : prev.auto_accept,
                submission_delay: data.submission_delay !== undefined ? data.submission_delay : prev.submission_delay,
            } : prev)
        }

        const targetKey = event?.id
        if (!targetKey) return
        const effectiveStatus = data.event_status || event?.status
        if (effectiveStatus === 'ended') {
            releaseScreenWakeLock()
            setMyActiveStatus(null)
            return
        }
        const myReqs = getSingerRequests(targetKey)
        if (myReqs.length > 0) {
            const activeMatch = q.find(item => myReqs.some(m => m.id === item.id))
            if (activeMatch) {
                requestScreenWakeLock()
                const index = q.indexOf(activeMatch)
                if (activeMatch.status === 'up_next') {
                    setMyActiveStatus('🎤 CALLED TO STAGE!')
                    if (!buzzedRequestsRef.current.has(activeMatch.id)) {
                        buzzedRequestsRef.current.add(activeMatch.id)
                        if (areStageAlertsEnabled()) {
                            playBuzzChime()
                            if (typeof navigator !== 'undefined' && 'vibrate' in navigator) {
                                try { navigator.vibrate([300, 100, 300, 100, 500]) } catch {}
                            }
                            sendSingerNotification("🎤 YOU'RE CALLED TO THE STAGE!", {
                                body: `Head to the stage now for "${activeMatch.song_title || 'your anthem'}"!`,
                                tag: `micup-upnext-${activeMatch.id}`,
                            })
                        }
                        showToast("🎤 BUZZ! You're CALLED TO THE STAGE! Head to the stage!", 'info')
                    }
                } else {
                    if (index === 1 && !buzzedRequestsRef.current.has(`${activeMatch.id}-ondeck`)) {
                        buzzedRequestsRef.current.add(`${activeMatch.id}-ondeck`)
                        if (areStageAlertsEnabled()) {
                            playBuzzChime()
                            sendSingerNotification("⚠️ YOU'RE ON DECK!", {
                                body: `You are next after this song for "${activeMatch.song_title}". Head toward the stage!`,
                                tag: `micup-ondeck-${activeMatch.id}`,
                            })
                        }
                        showToast(`⚠️ You're ON DECK (1 song away) for "${activeMatch.song_title}"!`, 'info')
                    }
                    setMyActiveStatus(`You: #${index + 1} in line`)
                }
            } else {
                releaseScreenWakeLock()
                setMyActiveStatus(null)
            }
        } else {
            releaseScreenWakeLock()
            setMyActiveStatus(null)
        }
    }, [event?.id, event?.status])

    useQueuePolling({
        endpoint: queueEndpoint,
        enabled: !!queueEndpoint,
        interval: 6000,
        stopOn404: true,
        onData: handleQueueData,
    })

    const {
        allSongs,
        catalogName,
        totalSongs,
        loadingSongs,
        query,
        setQuery,
        indexing,
        activeLetter,
        setActiveLetter,
        activeArtistLetter,
        setActiveArtistLetter,
        activeArtistFilter,
        setActiveArtistFilter,
        expandedArtists,
        loadingArtistSongs,
        catalogError,
        toggleArtistExpand,
        expandAllArtists,
        collapseAllArtists,
        isSearching,
        displaySongs,
        artistGroups,
        letterCounts,
        artistLetterCounts,
        uniqueArtistsCount,
        tonightFavoriteSongs,
        catalogFavoriteArtists,
        displayedFavoriteArtists,
        displayedTonightSongs,
        displayedOffBookSongs,
        displayedOffBookArtists,
        totalTonightFavoritesCount,
        isRepertoireEmpty,
    } = useCatalogSearch({
        resolvedSongbookId,
        eventCode: event?.join_code || code,
        eventId: event?.id || eventId,
        favSongs,
        favArtists,
        viewMode,
        retryCount,
    })

    const handleToggleSongFavorite = (e, song) => {
        e.stopPropagation()
        const next = toggleFavoriteSong(song)
        const title = song.title || song.label || ''
        showToast(next ? `★ Added "${title}" to your Favorites!` : `Removed "${title}" from Favorites`)
    }

    const handleToggleArtistFavorite = (e, artistName) => {
        e.stopPropagation()
        const next = toggleFavoriteArtist(artistName)
        showToast(next ? `★ Added ${artistName} to favorite artists!` : `Removed ${artistName} from favorites`)
    }

    if (loadingInfo) {
        return <Spinner label="Loading catalog..." />
    }

    const displayError = infoError || catalogError
    if (displayError) {
        return (
            <div style={{ textAlign: 'center', padding: 'var(--space-12)', color: 'var(--color-text-muted)' }}>
                <p style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--space-4)' }}>{displayError}</p>
                <div style={{ display: 'flex', gap: 'var(--space-3)', justifyContent: 'center', flexWrap: 'wrap' }}>
                    <button type="button" onClick={() => { setInfoError(''); setResolvedSongbookId(id || ''); setRetryCount(c => c + 1) }} class="btn btn-primary btn-sm">
                        Try Again
                    </button>
                    <a href="/" class="btn btn-secondary btn-sm">Return Home</a>
                </div>
            </div>
        )
    }

    return (
        <div class="public-book-shell" style={{
            padding: 'var(--space-3) var(--space-4) 0',
            maxWidth: '640px',
            margin: '0 auto',
            display: 'flex',
            flexDirection: 'column',
            position: 'relative',
            overflow: 'hidden',
            boxSizing: 'border-box',
        }}>
            <SingerHeader
                event={event}
                songbook={{ name: catalogName }}
                totalSongs={totalSongs}
                uniqueArtistsCount={uniqueArtistsCount}
                totalFavoritesCount={totalTonightFavoritesCount}
                viewMode={viewMode}
                setViewMode={setViewMode}
                code={code}
                myActiveStatus={myActiveStatus}
                queueCount={queueCount}
                onOpenQueue={() => setIsQueueOpen(true)}
                onOpenJoinPrompt={() => setShowJoinPrompt(true)}
            />

            <CatalogControls
                viewMode={viewMode}
                query={query}
                onQueryChange={setQuery}
                onClearQuery={() => setQuery('')}
                isSearching={isSearching}
                catalogFavoriteArtists={catalogFavoriteArtists}
                activeArtistFilter={activeArtistFilter}
                onSelectArtistFilter={setActiveArtistFilter}
                activeLetter={activeLetter}
                letterCounts={letterCounts}
                onSelectLetter={setActiveLetter}
                activeArtistLetter={activeArtistLetter}
                artistLetterCounts={artistLetterCounts}
                onSelectArtistLetter={setActiveArtistLetter}
                displaySongsCount={displaySongs.length}
                totalSongs={totalSongs}
                artistGroupsCount={artistGroups.length}
                uniqueArtistsCount={uniqueArtistsCount}
                tonightFavoriteSongsCount={tonightFavoriteSongs.length}
                displayedTonightSongsCount={displayedTonightSongs.length}
                displayedFavoriteArtistsCount={displayedFavoriteArtists.length}
                onExpandAllArtists={() => expandAllArtists(artistGroups)}
                onCollapseAllArtists={collapseAllArtists}
            />

            {loadingSongs ? (
                <Spinner label="Loading catalog..." />
            ) : indexing && !allSongs.length ? (
                <Spinner label="Building search index..." />
            ) : viewMode === 'songs' ? (
                <CatalogSongsView
                    displaySongs={displaySongs}
                    query={query}
                    isSearching={isSearching}
                    event={event}
                    onSelectSong={(song) => setSelectedSongForRequest(song)}
                    onToggleFavorite={handleToggleSongFavorite}
                    onRequestWriteIn={(initialTitle) => setSelectedSongForRequest({ is_write_in: true, title: initialTitle || '', artist: '' })}
                />
            ) : viewMode === 'artists' ? (
                <CatalogArtistsView
                    artistGroups={artistGroups}
                    query={query}
                    expandedArtists={expandedArtists}
                    loadingArtistSongs={loadingArtistSongs}
                    event={event}
                    onToggleArtistExpand={toggleArtistExpand}
                    onToggleArtistFavorite={handleToggleArtistFavorite}
                    onToggleSongFavorite={handleToggleSongFavorite}
                    onSelectSong={(song) => setSelectedSongForRequest(song)}
                />
            ) : (
                <CatalogFavoritesView
                    isRepertoireEmpty={isRepertoireEmpty}
                    isSearching={isSearching}
                    query={query}
                    displayedFavoriteArtists={displayedFavoriteArtists}
                    displayedTonightSongs={displayedTonightSongs}
                    displayedOffBookSongs={displayedOffBookSongs}
                    displayedOffBookArtists={displayedOffBookArtists}
                    expandedArtists={expandedArtists}
                    loadingArtistSongs={loadingArtistSongs}
                    event={event}
                    onToggleArtistExpand={toggleArtistExpand}
                    onToggleArtistFavorite={handleToggleArtistFavorite}
                    onToggleSongFavorite={handleToggleSongFavorite}
                    onSelectSong={(song) => setSelectedSongForRequest(song)}
                    onSwitchToSongs={() => setViewMode('songs')}
                />
            )}

            <SongRequestModal 
                song={selectedSongForRequest}
                songbookId={resolvedSongbookId}
                eventId={event?.id}
                eventStatus={event?.status}
                eventDelay={event?.submission_delay}
                eventRotationMode={event?.rotation_mode}
                allowKeyChange={event ? (event.allow_key_change !== false) : true}
                isOpen={!!selectedSongForRequest}
                onClose={() => setSelectedSongForRequest(null)}
                onEventJoined={(joinedEvent) => setEvent(joinedEvent)}
                onEventStatusChange={(nextStatus) => setEvent(prev => prev ? { ...prev, status: nextStatus } : prev)}
                onRequestSubmitted={() => {
                    setIsQueueOpen(true)
                }}
            />

            <SingerQueueModal 
                songbookId={resolvedSongbookId}
                songbookName={catalogName || event?.name}
                eventId={event?.id}
                eventName={event?.name}
                eventStatus={event?.status}
                venueName={event?.venue_name}
                rotationMode={event?.rotation_mode}
                autoAccept={event?.auto_accept}
                isOpen={isQueueOpen}
                onClose={() => setIsQueueOpen(false)}
                onEventJoined={(joinedEvent) => setEvent(joinedEvent)}
                onEventStatusChange={(nextStatus) => setEvent(prev => prev ? { ...prev, status: nextStatus } : prev)}
                onReRequest={(song) => {
                    const statusInfo = getShowStatusInfo(event)
                    if (statusInfo.isPaused || statusInfo.isEnded) {
                        showToast(statusInfo.requestBlockedMessage, 'info')
                        return
                    }
                    setIsQueueOpen(false)
                    setSelectedSongForRequest(song)
                }}
            />

            <JoinShowModal
                isOpen={showJoinPrompt}
                onClose={() => setShowJoinPrompt(false)}
                onJoin={handleJoinShow}
                joining={joiningEvent}
                joinError={joinError}
            />
        </div>
    )
}
