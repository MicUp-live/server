import { useState, useRef, useMemo, useEffect } from 'preact/hooks'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api } from '../../../api/client.js'
import { ErrorBanner } from '../../../components/Banner.jsx'
import { Spinner } from '../../../components/Spinner.jsx'
import { showToast } from '../../../components/Toast.jsx'
import { fetchAllSongsForSongbook, generateSongbookCSV, downloadCSV } from '../../../utils/songbookExport.js'
import { EditSongModal } from './EditSongModal.jsx'

export function SongManagerView({ songbook, onClose, onSongsChanged }) {
    const [songs, setSongs] = useState([])
    const [songsLoading, setSongsLoading] = useState(false)
    const [songForm, setSongForm] = useState({ artist: '', title: '', disc: '', source: '', genre: '', video_url: '' })
    const [songFilter, setSongFilter] = useState('')
    const [editingSong, setEditingSong] = useState(null)
    const [selectedSongIds, setSelectedSongIds] = useState(new Set())
    const [isDeletingBatch, setIsDeletingBatch] = useState(false)
    const [exportingId, setExportingId] = useState(null)
    const [error, setError] = useState('')
    const [addSongOpen, setAddSongOpen] = useState(false)
    const initialLoadDone = useRef(false)

    const songLoadSequence = useRef(0)
    const editSongSeq = useRef(0)
    const parentRef = useRef(null)

    const openEditSong = (song) => {
        const seq = ++editSongSeq.current
        const initialLocation = song.location || ''
        setEditingSong({
            song_id: song.song_id,
            artist: song.artist || '',
            title: song.title || '',
            disc: song.disc || '',
            source: song.source || '',
            genre: song.genre || '',
            video_url: song.video_url || '',
            location: initialLocation,
            locationLoading: !initialLocation && Boolean(songbook?.id && song.song_id),
            locationError: false,
        })
        if (!initialLocation && songbook?.id && song.song_id) {
            api.get(`/songbooks/${songbook.id}/songs/${song.song_id}`)
                .then((detail) => {
                    if (seq !== editSongSeq.current) return
                    setEditingSong((prev) => prev && prev.song_id === song.song_id
                        ? { ...prev, location: detail?.location || '', locationLoading: false, locationError: false }
                        : prev)
                })
                .catch(() => {
                    if (seq !== editSongSeq.current) return
                    setEditingSong((prev) => prev && prev.song_id === song.song_id
                        ? { ...prev, locationLoading: false, locationError: true }
                        : prev)
                })
        }
    }

    const loadSongs = async (targetSongbook) => {
        const sequence = ++songLoadSequence.current
        setSongs([])
        setSelectedSongIds(new Set())
        setSongsLoading(true)
        try {
            const all = await fetchAllSongsForSongbook(targetSongbook)
            if (sequence === songLoadSequence.current) {
                setSongs(all)
                if (!initialLoadDone.current) {
                    initialLoadDone.current = true
                    if (all.length === 0) setAddSongOpen(true)
                }
            }
        } catch (err) {
            if (sequence === songLoadSequence.current) setError(err.message || 'Failed to load songs')
        } finally {
            if (sequence === songLoadSequence.current) setSongsLoading(false)
        }
    }

    useEffect(() => {
        initialLoadDone.current = false
        editSongSeq.current += 1
        setEditingSong(null)
        if (songbook) loadSongs(songbook)
        return () => {
            songLoadSequence.current += 1
            editSongSeq.current += 1
        }
    }, [songbook?.id])

    const isEditingOpen = Boolean(editingSong)
    useEffect(() => {
        if (!isEditingOpen) return
        const onKeyDown = (e) => {
            if (e.key === 'Escape') setEditingSong(null)
        }
        document.addEventListener('keydown', onKeyDown)
        return () => document.removeEventListener('keydown', onKeyDown)
    }, [isEditingOpen])

    const exportCSV = async (targetSongbook, existingSongs = null) => {
        if (exportingId) return
        try {
            setExportingId(targetSongbook.id)
            let songList = existingSongs
            if (!songList) {
                showToast('Preparing full CSV export...')
                songList = await fetchAllSongsForSongbook(targetSongbook)
            }
            if (!songList || songList.length === 0) {
                showToast('No songs to export in this songbook', 'error')
                return
            }
            const csvContent = generateSongbookCSV(songList)
            const safeName = (targetSongbook.name || 'songbook').replace(/[^a-zA-Z0-9_-]/g, '_')
            downloadCSV(csvContent, `${safeName}_catalog.csv`)
            showToast(`Exported ${songList.length} songs to CSV!`)
        } catch (err) {
            console.error('Failed to export CSV:', err)
            showToast('Failed to export CSV', 'error')
        } finally {
            setExportingId(null)
        }
    }

    const addSong = async (event) => {
        event.preventDefault()
        if (!songForm.artist.trim() || !songForm.title.trim()) return
        try {
            await api.post(`/songbooks/${songbook.id}/songs`, {
                artist: songForm.artist.trim(),
                title: songForm.title.trim(),
                disc: songForm.disc.trim(),
                source: songForm.source.trim(),
                genre: songForm.genre.trim(),
                video_url: songForm.video_url.trim(),
            })
            setSongForm({ artist: '', title: '', disc: '', source: '', genre: '', video_url: '' })
            await loadSongs(songbook)
            onSongsChanged?.()
            showToast('Song added to catalog')
        } catch (err) {
            setError(err.message || 'Failed to add song')
        }
    }

    const saveEditedSong = async (event) => {
        event.preventDefault()
        if (!editingSong || !editingSong.artist.trim() || !editingSong.title.trim()) return
        try {
            await api.patch(`/songbooks/${songbook.id}/songs/${editingSong.song_id}`, {
                artist: editingSong.artist.trim(),
                title: editingSong.title.trim(),
                disc: editingSong.disc?.trim() || '',
                source: editingSong.source?.trim() || '',
                genre: editingSong.genre?.trim() || '',
                video_url: editingSong.video_url?.trim() || '',
            })
            setEditingSong(null)
            await loadSongs(songbook)
            onSongsChanged?.()
            showToast('Song updated')
        } catch (err) {
            setError(err.message || 'Failed to update song')
        }
    }

    const deleteSong = async (song) => {
        if (!confirm(`Delete "${song.label || song.title}"?`)) return
        try {
            await api.delete(`/songbooks/${songbook.id}/songs/${song.song_id}`)
            await loadSongs(songbook)
            onSongsChanged?.()
            showToast('Song deleted')
        } catch (err) {
            setError(err.message || 'Failed to delete song')
        }
    }

    const toggleSelectSong = (songId) => {
        setSelectedSongIds(prev => {
            const next = new Set(prev)
            if (next.has(songId)) next.delete(songId)
            else next.add(songId)
            return next
        })
    }

    const toggleSelectAll = (filteredList) => {
        if (selectedSongIds.size === filteredList.length) {
            setSelectedSongIds(new Set())
        } else {
            setSelectedSongIds(new Set(filteredList.map(s => s.song_id)))
        }
    }

    const handleBatchDelete = async () => {
        const count = selectedSongIds.size
        if (count === 0) return
        if (!confirm(`Are you sure you want to delete ${count} selected songs?`)) return

        setIsDeletingBatch(true)
        try {
            await api.post(`/songbooks/${songbook.id}/songs/batch-delete`, {
                song_ids: Array.from(selectedSongIds),
            })
            showToast(`Deleted ${count} songs`)
            setSelectedSongIds(new Set())
            await loadSongs(songbook)
            onSongsChanged?.()
        } catch (err) {
            showToast(err.message || 'Failed to delete selected songs', 'error')
        } finally {
            setIsDeletingBatch(false)
        }
    }

    const handleClearAllSongs = async () => {
        if (songs.length === 0) return
        const promptText = prompt(`WARNING: This will permanently delete ALL ${songs.length} songs in "${songbook.name}".\n\nType "CLEAR" to confirm:`)
        if (promptText !== 'CLEAR') {
            showToast('Clear cancelled')
            return
        }

        setIsDeletingBatch(true)
        try {
            await api.post(`/songbooks/${songbook.id}/songs/clear`)
            showToast(`Cleared all songs in "${songbook.name}"`)
            setSelectedSongIds(new Set())
            await loadSongs(songbook)
            onSongsChanged?.()
        } catch (err) {
            showToast(err.message || 'Failed to clear songs', 'error')
        } finally {
            setIsDeletingBatch(false)
        }
    }

    const filteredSongs = useMemo(() => {
        if (!songFilter.trim()) return songs
        const q = songFilter.toLowerCase()
        return songs.filter(s =>
            (s.artist || '').toLowerCase().includes(q) ||
            (s.title || '').toLowerCase().includes(q) ||
            (s.label || '').toLowerCase().includes(q) ||
            (s.disc || '').toLowerCase().includes(q) ||
            (s.source || '').toLowerCase().includes(q) ||
            (s.genre || '').toLowerCase().includes(q)
        )
    }, [songs, songFilter])

    const rowVirtualizer = useVirtualizer({
        count: filteredSongs.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => 88,
        overscan: 10,
    })

    const isAllSelected = filteredSongs.length > 0 && selectedSongIds.size === filteredSongs.length

    return (
        <div class="container kj-song-manager-shell">
            <div class="kj-sm-topbar">
                <button type="button" class="btn btn-secondary btn-sm" onClick={onClose}>← Back</button>
                <div class="kj-sm-topbar-actions">
                    <span style={{ color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)', fontWeight: 'bold' }}>
                        {songs.length} {songs.length === 1 ? 'song' : 'songs'}
                    </span>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        onClick={() => exportCSV(songbook, songs)}
                        disabled={songs.length === 0 || exportingId === songbook.id}
                        title="Download complete catalog as CSV"
                    >
                        {exportingId === songbook.id ? '⏳ Exporting...' : '📥 Export CSV'}
                    </button>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        style={{ color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.4)' }}
                        onClick={handleClearAllSongs}
                        disabled={songs.length === 0 || isDeletingBatch}
                        title="Delete all songs from this catalog"
                    >
                        🗑️ Clear All
                    </button>
                </div>
            </div>

            <div style={{ marginBottom: 'var(--space-4)' }}>
                <h2 style={{ margin: '0 0 var(--space-1) 0' }}>{songbook.name}</h2>
                <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                    Manage individual catalog tracks, edit metadata, or perform bulk deletions.
                </span>
            </div>

            {error && <ErrorBanner message={error} />}

            <details
                class="card kj-add-song-details"
                open={addSongOpen}
                onToggle={e => setAddSongOpen(e.currentTarget.open)}
            >
                <summary class="kj-add-song-summary">Add New Song</summary>
                <form onSubmit={addSong} class="kj-add-song-body">
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--space-2)', marginBottom: 'var(--space-3)' }}>
                        <input
                            value={songForm.artist}
                            onInput={event => setSongForm(prev => ({ ...prev, artist: event.target.value }))}
                            placeholder="Artist * (e.g. Queen)"
                            required
                        />
                        <input
                            value={songForm.title}
                            onInput={event => setSongForm(prev => ({ ...prev, title: event.target.value }))}
                            placeholder="Title * (e.g. Bohemian Rhapsody)"
                            required
                        />
                        <input
                            value={songForm.disc}
                            onInput={event => setSongForm(prev => ({ ...prev, disc: event.target.value }))}
                            placeholder="Disc / Code (e.g. SC8100-01)"
                        />
                        <input
                            value={songForm.source}
                            onInput={event => setSongForm(prev => ({ ...prev, source: event.target.value }))}
                            placeholder="Publisher / Source (e.g. Sound Choice)"
                        />
                        <input
                            value={songForm.genre}
                            onInput={event => setSongForm(prev => ({ ...prev, genre: event.target.value }))}
                            placeholder="Genre (e.g. Classic Rock)"
                        />
                        <input
                            value={songForm.video_url}
                            onInput={event => setSongForm(prev => ({ ...prev, video_url: event.target.value }))}
                            placeholder="Video URL (optional)"
                        />
                    </div>
                    <button class="btn btn-primary btn-sm" style={{ minWidth: '100px' }}>+ Add Song</button>
                </form>
            </details>

            {songs.length > 0 && (
                <div class="kj-sm-search-bar">
                    <div class="kj-sm-search-input-wrap">
                        <input
                            type="text"
                            value={songFilter}
                            onInput={e => setSongFilter(e.target.value)}
                            placeholder="Search by title, artist, disc code, or genre..."
                            style={{ width: '100%', fontSize: 'var(--text-sm)' }}
                        />
                    </div>
                    {filteredSongs.length > 0 && (
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                            <label style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: 'var(--text-xs)', cursor: 'pointer', userSelect: 'none' }}>
                                <input type="checkbox" checked={isAllSelected} onChange={() => toggleSelectAll(filteredSongs)} />
                                <span>Select All ({filteredSongs.length})</span>
                            </label>
                        </div>
                    )}
                </div>
            )}

            {selectedSongIds.size > 0 && (
                <div class="kj-sm-batch-bar">
                    <span style={{ fontWeight: 'bold', color: '#fca5a5', fontSize: 'var(--text-sm)' }}>
                        ✓ {selectedSongIds.size} {selectedSongIds.size === 1 ? 'song' : 'songs'} selected
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <button type="button" class="btn btn-secondary btn-sm" onClick={() => setSelectedSongIds(new Set())}>
                            Deselect All
                        </button>
                        <button type="button" class="btn btn-danger btn-sm" onClick={handleBatchDelete} disabled={isDeletingBatch} style={{ fontWeight: 'bold' }}>
                            {isDeletingBatch ? 'Deleting...' : `🗑️ Delete (${selectedSongIds.size})`}
                        </button>
                    </div>
                </div>
            )}

            {songsLoading && <Spinner label="Loading songs..." />}
            {!songsLoading && songs.length === 0 && (
                <p style={{ textAlign: 'center', color: 'var(--color-text-dim)', padding: 'var(--space-8)' }}>
                    No songs in this songbook yet. Add one above or use Assistant Sync to import your library.
                </p>
            )}
            {!songsLoading && songs.length > 0 && filteredSongs.length === 0 && (
                <p style={{ textAlign: 'center', color: 'var(--color-text-dim)', padding: 'var(--space-4)' }}>
                    No songs match "{songFilter}"
                </p>
            )}

            {!songsLoading && filteredSongs.length > 0 && (
                <div ref={parentRef} class="kj-sm-list-viewport">
                    <div style={{ height: `${rowVirtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }}>
                        {rowVirtualizer.getVirtualItems().map(virtualItem => {
                            const song = filteredSongs[virtualItem.index]
                            if (!song) return null
                            const isSelected = selectedSongIds.has(song.song_id)

                            return (
                                <div
                                    key={song.song_id}
                                    data-index={virtualItem.index}
                                    ref={rowVirtualizer.measureElement}
                                    style={{
                                        position: 'absolute',
                                        top: 0,
                                        left: 0,
                                        width: '100%',
                                        transform: `translateY(${virtualItem.start}px)`,
                                        padding: '4px 0',
                                    }}
                                >
                                    <div
                                        class="card kj-sm-song-card"
                                        style={{
                                            background: isSelected ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                            borderColor: isSelected ? 'var(--color-accent)' : 'var(--color-border)',
                                        }}
                                    >
                                        <input
                                            type="checkbox"
                                            checked={isSelected}
                                            onChange={() => toggleSelectSong(song.song_id)}
                                            aria-label={`Select ${song.title || song.label}`}
                                            style={{ cursor: 'pointer', width: '18px', height: '18px', flexShrink: 0, marginTop: '2px' }}
                                        />
                                        <div class="kj-sm-song-copy">
                                            <div class="kj-sm-song-title-row">
                                                <span class="kj-sm-song-title">
                                                    {song.title || song.label}
                                                </span>
                                                {song.source && (
                                                    <span style={{ fontSize: '0.7rem', fontWeight: 'bold', background: 'var(--color-surface-alt)', color: 'var(--color-text-muted)', border: '1px solid var(--color-border)', padding: '1px 6px', borderRadius: 'var(--radius-sm)' }}>
                                                        {song.source}
                                                    </span>
                                                )}
                                                {song.disc && (
                                                    <span style={{ fontSize: '0.7rem', fontWeight: 'bold', background: 'rgba(59, 130, 246, 0.15)', color: '#60a5fa', border: '1px solid rgba(59, 130, 246, 0.3)', padding: '1px 6px', borderRadius: 'var(--radius-sm)' }}>
                                                        {song.disc}
                                                    </span>
                                                )}
                                                {song.genre && (
                                                    <span style={{ fontSize: '0.7rem', fontWeight: 'bold', background: 'rgba(245, 158, 11, 0.15)', color: 'var(--color-chorus)', border: '1px solid rgba(245, 158, 11, 0.3)', padding: '1px 6px', borderRadius: 'var(--radius-sm)' }}>
                                                        {song.genre}
                                                    </span>
                                                )}
                                            </div>
                                            <div class="kj-sm-song-artist">
                                                {song.artist}
                                            </div>
                                        </div>
                                        <div class="kj-sm-song-actions">
                                            <button
                                                type="button"
                                                class="btn btn-secondary btn-sm"
                                                onClick={() => openEditSong(song)}
                                            >
                                                Edit
                                            </button>
                                            <button
                                                type="button"
                                                class="btn btn-danger btn-sm"
                                                onClick={() => deleteSong(song)}
                                                aria-label={`Delete ${song.title || song.label}`}
                                                title="Delete song"
                                            >
                                                🗑️
                                            </button>
                                        </div>
                                    </div>
                                </div>
                            )
                        })}
                    </div>
                </div>
            )}

            <EditSongModal
                editingSong={editingSong}
                onChange={setEditingSong}
                onClose={() => setEditingSong(null)}
                onSave={saveEditedSong}
            />
        </div>
    )
}
