import { useState, useEffect, useRef, useMemo, useCallback } from 'preact/hooks'
import { api } from '../api/client.js'
import { Spinner } from '../components/Spinner.jsx'
import { showToast } from '../components/toastState.js'
import { loadCatalogShard } from '../api/catalogAssets.js'
import { useQueuePolling } from '../hooks/useQueuePolling.js'
import { getShowStatusInfo } from '../utils/showStatus.js'
import { useKioskSession } from './kiosk/hooks/useKioskSession.js'
import { fetchKioskEvent, resolveKioskCatalog, initKioskSearchWorker } from './kiosk/utils/kioskCatalogLoader.js'
import { KioskHeader } from './kiosk/components/KioskHeader.jsx'
import { KioskCatalogView } from './kiosk/components/KioskCatalogView.jsx'
import { KioskRequestModal } from './kiosk/components/KioskRequestModal.jsx'
import { KioskExitModal } from './kiosk/components/KioskExitModal.jsx'
import { KioskSuccessSplash } from './kiosk/components/KioskSuccessSplash.jsx'

import {
    getLetterBucket,
    sortSongsByTitle,
    countSongsByLetter,
    groupSongsByArtist,
} from './singer/utils/overlayCatalog.js'

export function Kiosk({ id, code }) {
    const [event, setEvent] = useState(null)
    const [catalogTotal, setCatalogTotal] = useState(0)
    const [loading, setLoading] = useState(true)
    const [loadingSongs, setLoadingSongs] = useState(false)
    const [error, setError] = useState('')

    // Catalog state
    const [staticManifest, setStaticManifest] = useState(null)
    const [allSongs, setAllSongs] = useState([])
    const [workerArtists, setWorkerArtists] = useState([])
    const [workerReady, setWorkerReady] = useState(false)
    const [expandedArtistSongs, setExpandedArtistSongs] = useState({})
    const [expandedArtists, setExpandedArtists] = useState(new Set())
    const [loadingArtistSongs, setLoadingArtistSongs] = useState({})

    // View / browse state
    const [viewMode, setViewMode] = useState('songs') // 'songs' | 'artists'
    const [activeLetter, setActiveLetter] = useState('A')
    const [activeArtistLetter, setActiveArtistLetter] = useState('A')

    // Search state
    const [query, setQuery] = useState('')
    const [searchResults, setSearchResults] = useState([])
    const [artistSearchResults, setArtistSearchResults] = useState([])
    const [indexing, setIndexing] = useState(false)
    const workerRef = useRef(null)
    const initialShardRef = useRef(null)
    const songListRef = useRef(null)
    const artistListRef = useRef(null)

    const {
        selectedSong,
        setSelectedSong,
        singerName,
        setSingerName,
        keyChange,
        setKeyChange,
        notes,
        setNotes,
        submitting,
        successSinger,
        countdown,
        showExitModal,
        setShowExitModal,
        pinInput,
        setPinInput,
        pinError,
        verifyingPin,
        handleResetKiosk,
        handleSubmitRequest,
        handleExitKiosk,
    } = useKioskSession({
        event,
        query,
        setQuery,
        setSearchResults,
        setArtistSearchResults,
        onEventStatusChange: (nextStatus) => setEvent(prev => prev ? { ...prev, status: nextStatus } : prev),
    })

    useEffect(() => () => { workerRef.current?.terminate(); workerRef.current = null }, [])

    useQueuePolling({
        endpoint: event?.id ? `/events/${event.id}/queue` : null,
        enabled: !!event?.id,
        interval: 6000,
        stopOn404: true,
        onData: (data) => {
            if (!data) return
            setEvent(prev => prev ? {
                ...prev,
                status: data.event_status || prev.status,
                announcement: data.announcement !== undefined ? data.announcement : prev.announcement,
            } : prev)
        },
    })

    const handleSelectSong = useCallback((song) => {
        const statusInfo = getShowStatusInfo(event)
        if (statusInfo.isPaused || statusInfo.isEnded) {
            showToast(statusInfo.requestBlockedMessage, 'info')
            return
        }
        setSelectedSong(song)
    }, [event, setSelectedSong])

    // Load Event and full catalog manifest + first letter shard
    useEffect(() => {
        let cancelled = false
        setLoading(true)
        setStaticManifest(null)
        setAllSongs([])
        setWorkerArtists([])
        setWorkerReady(false)
        setExpandedArtistSongs({})
        setExpandedArtists(new Set())
        workerRef.current?.terminate()
        workerRef.current = null

        const load = async () => {
            try {
                const ev = await fetchKioskEvent(api, { id, code })
                if (cancelled) return
                setEvent(ev)

                const result = await resolveKioskCatalog(api, ev)
                if (cancelled) return

                if (result.mode === 'overlay') {
                    const { overlay } = result
                    setAllSongs(overlay.songs)
                    setCatalogTotal(overlay.totalSongs)
                    setActiveLetter(overlay.firstLetter || 'A')
                    if (overlay.firstArtistLetter) setActiveArtistLetter(overlay.firstArtistLetter)
                    return
                }

                const { initial } = result
                initialShardRef.current = { manifest: initial.manifest, letter: initial.resolvedLetter }
                setStaticManifest(initial.manifest)
                setAllSongs(initial.songs)
                setCatalogTotal(initial.total)
                setActiveLetter(initial.resolvedLetter)
                setIndexing(true)
                initKioskSearchWorker({
                    workerRef,
                    baseDir: initial.baseDir,
                    searchPath: initial.manifest.search_path,
                    isCancelled: () => cancelled,
                    onReady: (artists) => {
                        setWorkerArtists(artists)
                        setWorkerReady(true)
                    },
                    onDone: () => setIndexing(false),
                })
            } catch (err) {
                if (!cancelled) setError(err.message || 'Failed to load kiosk catalog')
            } finally {
                if (!cancelled) setLoading(false)
            }
        }

        void load()
        return () => { cancelled = true }
    }, [id, code])

    // Load shard when letter changes in songs view
    useEffect(() => {
        if (!staticManifest || !activeLetter) return
        const initial = initialShardRef.current
        initialShardRef.current = null
        if (initial?.manifest === staticManifest && initial.letter === activeLetter) return
        const baseDir = staticManifest._baseDir || ''
        const shards = (staticManifest.letters?.[activeLetter]?.shards || []).map(p => baseDir + p)
        if (!shards.length) { setAllSongs([]); return }
        let cancelled = false
        setLoadingSongs(true)
        Promise.all(shards.map(loadCatalogShard))
            .then(pages => { if (!cancelled) setAllSongs(sortSongsByTitle(pages.flat())) })
            .catch(() => { if (!cancelled) showToast('Failed to load catalog page', 'error') })
            .finally(() => { if (!cancelled) setLoadingSongs(false) })
        return () => { cancelled = true }
    }, [staticManifest, activeLetter])

    // Search
    useEffect(() => {
        if (!query.trim()) {
            setSearchResults([])
            setArtistSearchResults([])
            return
        }
        let cancelled = false
        if (viewMode === 'artists' && workerRef.current) {
            workerRef.current.searchArtists(query.trim())
                .then(res => { if (!cancelled) setArtistSearchResults(res.results || []) })
                .catch(console.error)
        } else {
            const fallback = (allSongs || []).filter(s =>
                [s.title, s.label, s.artist].some(v => (v || '').toLowerCase().includes(query.toLowerCase().trim()))
            )
            const search = workerRef.current?.search(query.trim()) || Promise.resolve({ results: fallback })
            search
                .then(res => { if (!cancelled) setSearchResults(res.results || []) })
                .catch(() => { if (!cancelled) setSearchResults(fallback) })
        }
        return () => { cancelled = true }
    }, [query, workerReady, viewMode, allSongs])

    // Computed letter counts
    const letterCounts = useMemo(() => staticManifest
        ? Object.fromEntries(Object.entries(staticManifest.letters || {}).map(([l, v]) => [l, v.count]))
        : countSongsByLetter(allSongs)
    , [staticManifest, allSongs])

    const artistLetterCounts = useMemo(() => {
        const counts = {}
        const source = workerArtists.length > 0
            ? workerArtists
            : Array.from(new Set(allSongs.map(s => (s.artist || '').trim()))).map(a => ({ artist: a }))
        for (const a of source) {
            const bucket = getLetterBucket(a.artist)
            counts[bucket] = (counts[bucket] || 0) + 1
        }
        return counts
    }, [workerArtists, allSongs])

    const uniqueArtistsCount = useMemo(() => {
        if (staticManifest && workerReady) return workerArtists.length
        if (staticManifest && indexing) return null
        return new Set(allSongs.map(s => (s.artist || '').trim().toLowerCase()).filter(Boolean)).size
    }, [staticManifest, workerReady, indexing, workerArtists, allSongs])

    const isSearching = query.trim().length > 0

    const artistGroups = useMemo(() => {
        if (workerArtists.length > 0) {
            const list = isSearching ? artistSearchResults
                : workerArtists.filter(a => getLetterBucket(a.artist) === activeArtistLetter)
            return list.map(item => ({
                artist: item.artist,
                norm: item.norm,
                count: item.count,
                songs: expandedArtistSongs[item.norm] || [],
            })).sort((a, b) => a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' }))
        }
        return groupSongsByArtist(isSearching ? searchResults : allSongs, { activeArtistLetter, isSearching })
    }, [workerArtists, artistSearchResults, allSongs, searchResults, isSearching, activeArtistLetter, expandedArtistSongs])

    const displaySongs = isSearching ? searchResults : (staticManifest ? allSongs : allSongs.filter(s => getLetterBucket(s.title || s.label) === activeLetter))

    const handleJumpToLetter = useCallback((letter) => {
        if (isSearching) return
        setActiveLetter(letter)
        songListRef.current?.scrollTo({ top: 0 })
    }, [isSearching])

    const handleSelectArtistLetter = useCallback((letter) => {
        if (isSearching) return
        setActiveArtistLetter(letter)
        artistListRef.current?.scrollTo({ top: 0 })
    }, [isSearching])

    const toggleArtistExpand = (norm) => {
        setExpandedArtists(prev => {
            const next = new Set(prev)
            const willExpand = !next.has(norm)
            if (willExpand) {
                next.add(norm)
                if (workerRef.current && !expandedArtistSongs[norm]) {
                    setLoadingArtistSongs(s => ({ ...s, [norm]: true }))
                    workerRef.current.getArtistSongs(norm)
                        .then(res => setExpandedArtistSongs(s => ({ ...s, [norm]: res.songs || [] })))
                        .catch(console.error)
                        .finally(() => setLoadingArtistSongs(s => ({ ...s, [norm]: false })))
                }
            } else {
                next.delete(norm)
            }
            return next
        })
    }

    if (loading) {
        return (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--color-bg)' }}>
                <Spinner size="lg" label="Initializing Kiosk..." />
            </div>
        )
    }

    if (error) {
        return (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--color-bg)', padding: 'var(--space-6)', textAlign: 'center' }}>
                <h2>Kiosk Offline</h2>
                <p style={{ color: 'var(--color-text-muted)' }}>{error}</p>
                <button class="btn btn-secondary" onClick={() => window.location.reload()}>Retry</button>
            </div>
        )
    }

    if (successSinger) {
        return (
            <KioskSuccessSplash
                successSinger={successSinger}
                countdown={countdown}
                onReset={handleResetKiosk}
            />
        )
    }

    return (
        <div style={{
            height: '100vh',
            display: 'flex',
            flexDirection: 'column',
            background: 'var(--color-bg)',
            boxSizing: 'border-box',
            overflow: 'hidden',
        }}>
            <KioskHeader
                event={event}
                onOpenExitModal={() => {
                    setShowExitModal(true)
                    setPinInput('')
                }}
            />

            {/* Search Bar */}
            <div style={{ padding: 'var(--space-4) var(--space-6)', background: 'var(--color-surface-alt)', borderBottom: '1px solid var(--color-border)', flexShrink: 0 }}>
                <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
                    <span style={{ position: 'absolute', left: 'var(--space-4)', fontSize: '1.2rem', color: 'var(--color-text-dim)' }}>🔍</span>
                    <input
                        type="text"
                        placeholder="Search by Song Title or Artist..."
                        value={query}
                        onInput={(e) => setQuery(e.currentTarget.value)}
                        style={{
                            width: '100%',
                            padding: 'var(--space-4) var(--space-4) var(--space-4) 3rem',
                            fontSize: 'var(--text-lg)',
                            borderRadius: 'var(--radius-lg)',
                            background: 'var(--color-surface)',
                            border: '2px solid var(--color-border)',
                            color: 'var(--color-text)',
                            outline: 'none',
                        }}
                    />
                    {query && (
                        <button
                            type="button"
                            onClick={() => setQuery('')}
                            style={{
                                position: 'absolute',
                                right: 'var(--space-4)',
                                background: 'rgba(255, 255, 255, 0.1)',
                                border: 'none',
                                color: 'var(--color-text)',
                                width: '32px',
                                height: '32px',
                                borderRadius: '50%',
                                cursor: 'pointer',
                            }}
                        >
                            ✕
                        </button>
                    )}
                </div>
            </div>

            <KioskCatalogView
                eventStatus={event?.status}
                viewMode={viewMode}
                onSelectViewMode={setViewMode}
                catalogTotal={catalogTotal}
                uniqueArtistsCount={uniqueArtistsCount}
                indexing={indexing}
                loadingSongs={loadingSongs}
                activeLetter={activeLetter}
                letterCounts={letterCounts}
                onSelectLetter={handleJumpToLetter}
                activeArtistLetter={activeArtistLetter}
                artistLetterCounts={artistLetterCounts}
                onSelectArtistLetter={handleSelectArtistLetter}
                isSearching={isSearching}
                displaySongs={displaySongs}
                artistGroups={artistGroups}
                expandedArtists={expandedArtists}
                expandedArtistSongs={expandedArtistSongs}
                loadingArtistSongs={loadingArtistSongs}
                query={query}
                onClearQuery={() => setQuery('')}
                onSelectSong={handleSelectSong}
                onToggleArtistExpand={toggleArtistExpand}
                songListRef={songListRef}
                artistListRef={artistListRef}
            />

            <KioskRequestModal
                song={selectedSong}
                event={event}
                singerName={singerName}
                onSingerNameChange={setSingerName}
                keyChange={keyChange}
                onKeyChange={setKeyChange}
                notes={notes}
                onNotesChange={setNotes}
                submitting={submitting}
                onSubmit={handleSubmitRequest}
                onClose={() => setSelectedSong(null)}
            />

            <KioskExitModal
                isOpen={showExitModal}
                pinInput={pinInput}
                onPinInputChange={setPinInput}
                pinError={pinError}
                verifyingPin={verifyingPin}
                onVerifyPin={handleExitKiosk}
                onClose={() => setShowExitModal(false)}
            />
        </div>
    )
}
