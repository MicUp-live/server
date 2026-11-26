import { useEffect, useState } from 'preact/hooks'
import { api } from '../../api/client.js'
import { AssistantImport } from '../../components/AssistantImport.jsx'
import { ErrorBanner, SuccessBanner } from '../../components/Banner.jsx'
import { Spinner } from '../../components/Spinner.jsx'
import { showToast } from '../../components/Toast.jsx'
import { fetchAllSongsForSongbook, generateSongbookCSV, downloadCSV } from '../../utils/songbookExport.js'
import { CreateShowModal } from './components/CreateShowModal.jsx'
import { SongbookQRModal } from './components/SongbookQRModal.jsx'
import { SongManagerView } from './components/SongManagerView.jsx'
import { ManageAccessModal } from './components/ManageAccessModal.jsx'
import { DeleteSongbookModal } from './components/DeleteSongbookModal.jsx'
import { ActionPopover } from '../../components/ui/ActionPopover.jsx'
import { useStartShowHandoff, resolveSongbookShowState } from './hooks/useStartShowHandoff.js'
import { getSongbookPermissions, filterHostableSongbooks } from '../../utils/songbookPermissions.js'
import './kj.css'

export function Songbooks() {
    const [songbooks, setSongbooks] = useState([])
    const [name, setName] = useState('')
    const [editing, setEditing] = useState(null)
    const [assistant, setAssistant] = useState(null)
    const [managing, setManaging] = useState(null)
    const [qrSongbook, setQrSongbook] = useState(null)
    const [accessSongbook, setAccessSongbook] = useState(null)
    const [pendingDelete, setPendingDelete] = useState(null)
    const [deletingBook, setDeletingBook] = useState(false)
    const [loading, setLoading] = useState(true)
    const [saving, setSaving] = useState(false)
    const [exportingId, setExportingId] = useState(null)
    const [error, setError] = useState('')
    const [success, setSuccess] = useState('')

    const {
        events,
        setEvents,
        eventsLoading,
        eventsError,
        createModalSongbookId,
        creatingShow,
        loadEvents,
        startShow,
        cancelCreateShow,
        createShow,
    } = useStartShowHandoff()

    const load = async () => {
        setLoading(true)
        setError('')
        try {
            const [sbRes] = await Promise.allSettled([
                api.get('/songbooks'),
                loadEvents(),
            ])

            if (sbRes.status === 'fulfilled') {
                setSongbooks(sbRes.value.songbooks || [])
            } else {
                setError(sbRes.reason?.message || 'Failed to load songbooks')
            }
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => { load() }, [])

    const create = async (event) => {
        event.preventDefault()
        if (!name.trim()) return
        setSaving(true)
        try {
            await api.post('/songbooks', { name: name.trim() })
            setName('')
            setSuccess('Songbook created')
            await load()
        } catch (err) {
            setError(err.message || 'Failed to create songbook')
        } finally {
            setSaving(false)
        }
    }

    const update = async (songbook, changes) => {
        setSaving(true)
        try {
            await api.patch(`/songbooks/${songbook.id}`, { name: songbook.name, is_public: songbook.is_public, ...changes })
            setEditing(null)
            await load()
        } catch (err) {
            setError(err.message || 'Failed to update songbook')
        } finally {
            setSaving(false)
        }
    }

    const remove = async (songbook) => {
        const linked = events.filter(event => event.songbook_id === songbook.id)
        if (linked.length > 0) {
            const eventNames = linked.map(e => e.name || e.venue_name || e.join_code || 'Event').map(n => `"${n}"`).join(', ')
            setError(`Cannot delete "${songbook.name}" because it is linked to ${linked.length} event(s): ${eventNames}. Please delete the event(s) first.`)
            window.scrollTo({ top: 0, behavior: 'smooth' })
            return
        }
        if (!confirm(`Delete "${songbook.name}" and all of its songs? This cannot be undone.`)) return
        try {
            await api.delete(`/songbooks/${songbook.id}`)
            setSongbooks(current => current.filter(item => item.id !== songbook.id))
            setEvents(current => current.filter(event => event.songbook_id !== songbook.id))
            setSuccess('Songbook deleted')
            await load()
        } catch (err) {
            if (err.status === 409 && err.data?.error === 'foreign_events_associated') {
                setPendingDelete({
                    songbook,
                    affectedShowsCount: err.data.affected_shows_count || 1,
                    activeShowsCount: err.data.active_shows_count || 0,
                    confirmationToken: err.data.confirmation_token || '',
                })
                return
            }
            setError(err.message || 'Failed to delete songbook')
            window.scrollTo({ top: 0, behavior: 'smooth' })
        }
    }

    const confirmForeignDelete = async () => {
        if (!pendingDelete) return
        setDeletingBook(true)
        setError('')
        try {
            const { songbook, confirmationToken } = pendingDelete
            await api.delete(`/songbooks/${songbook.id}?confirm_detach_token=${encodeURIComponent(confirmationToken)}`)
            setPendingDelete(null)
            setSongbooks(current => current.filter(item => item.id !== songbook.id))
            setSuccess('Songbook deleted and collaborator shows detached')
            await load()
        } catch (err) {
            if (err.status === 409 && err.data?.error === 'foreign_events_associated') {
                setPendingDelete({
                    songbook: pendingDelete.songbook,
                    affectedShowsCount: err.data.affected_shows_count || 1,
                    activeShowsCount: err.data.active_shows_count || 0,
                    confirmationToken: err.data.confirmation_token || '',
                })
                showToast('Linked shows changed; please review the updated count and confirm again.', 'error')
                return
            }
            setPendingDelete(null)
            setError(err.message || 'Failed to delete songbook')
        } finally {
            setDeletingBook(false)
        }
    }

    const copyLink = async (songbook) => {
        const slugOrId = songbook.slug || songbook.public_id
        if (!slugOrId) return
        const link = `${window.location.origin}/book/${slugOrId}`
        try {
            await navigator.clipboard.writeText(link)
        } catch {
            const input = document.createElement('textarea')
            input.value = link
            document.body.append(input)
            input.select()
            input.setSelectionRange(0, input.value.length)
            document.execCommand('copy')
            input.remove()
        }
        showToast('Share link copied')
    }

    const exportCSV = async (songbook) => {
        if (exportingId) return
        try {
            setExportingId(songbook.id)
            showToast('Preparing full CSV export...')
            const songList = await fetchAllSongsForSongbook(songbook)
            if (!songList || songList.length === 0) {
                showToast('No songs to export in this songbook', 'error')
                return
            }

            const csvContent = generateSongbookCSV(songList)
            const safeName = (songbook.name || 'songbook').replace(/[^a-zA-Z0-9_-]/g, '_')
            downloadCSV(csvContent, `${safeName}_catalog.csv`)
            showToast(`Exported ${songList.length} songs to CSV!`)
        } catch (err) {
            console.error('Failed to export CSV:', err)
            showToast('Failed to export CSV', 'error')
        } finally {
            setExportingId(null)
        }
    }

    if (assistant) return (
        <div class="container assistant-import-shell">
            <AssistantImport songbookId={assistant.id} onComplete={() => { setAssistant(null); load() }} onCancel={() => setAssistant(null)} />
        </div>
    )

    if (managing) {
        return (
            <SongManagerView
                songbook={managing}
                onClose={() => { setManaging(null); load() }}
                onSongsChanged={load}
            />
        )
    }

    if (loading) return <Spinner label="Loading songbooks..." />

    return (
        <div class="container kj-songbooks-shell">
            <h2 style={{ marginBottom: 'var(--space-4)' }}>Your Songbooks</h2>
            {error && <ErrorBanner message={error} />}
            {success && <SuccessBanner message={success} />}

            {eventsError && (
                <div
                    class="card"
                    style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        flexWrap: 'wrap',
                        gap: 'var(--space-2)',
                        marginBottom: 'var(--space-4)',
                        padding: 'var(--space-3) var(--space-4)',
                        borderColor: 'rgba(239, 68, 68, 0.4)',
                        background: 'rgba(239, 68, 68, 0.1)',
                        color: '#fca5a5',
                    }}
                    role="alert"
                >
                    <span>⚠️ {eventsError}</span>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        onClick={loadEvents}
                        disabled={eventsLoading}
                    >
                        {eventsLoading ? 'Retrying...' : 'Retry'}
                    </button>
                </div>
            )}

            <form onSubmit={create} class="card kj-create-book-form">
                <input
                    value={name}
                    onInput={event => setName(event.target.value)}
                    placeholder="New songbook name..."
                    aria-label="New songbook name"
                />
                <button class="btn btn-primary" disabled={saving}>Create</button>
            </form>

            {songbooks.length === 0 && (
                <p style={{ color: 'var(--color-text-dim)', textAlign: 'center', padding: 'var(--space-8)' }}>
                    No songbooks yet. Create one to start importing your catalog.
                </p>
            )}

            {songbooks.map(songbook => {
                const perms = getSongbookPermissions(songbook)
                const showState = resolveSongbookShowState(songbook.id, events, { eventsLoading, eventsError })
                const { hasLiveShow, liveEvent } = showState

                return (
                    <div class="card kj-songbook-card" key={songbook.id}>
                        {editing?.id === songbook.id ? (
                            <form onSubmit={event => {
                                event.preventDefault()
                                update(songbook, { name: editing.name, slug: editing.slug })
                            }} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)', padding: 'var(--space-2) 0' }}>
                                <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                                    <label style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontWeight: 'bold' }}>Songbook Name</label>
                                    <input value={editing.name} onInput={event => setEditing({ ...editing, name: event.target.value })} required autoFocus />
                                </div>
                                <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                                    <label style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontWeight: 'bold' }}>Short Code / Custom Slug (e.g. DAVEMIC)</label>
                                    <input
                                        value={editing.slug || ''}
                                        onInput={event => setEditing({ ...editing, slug: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '') })}
                                        placeholder="optional short code, e.g. davemic"
                                    />
                                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                        Patrons can join via this code directly on the home page or at /book/{editing.slug || '[code]'}
                                    </span>
                                </div>
                                <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
                                    <button class="btn btn-primary btn-sm" disabled={saving}>Save Changes</button>
                                    <button type="button" class="btn btn-secondary btn-sm" onClick={() => setEditing(null)}>Cancel</button>
                                </div>
                            </form>
                        ) : (
                            <>
                                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                                    <div>
                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                                            <strong style={{ fontSize: 'var(--text-lg)' }}>{songbook.name}</strong>
                                            {songbook.slug && (
                                                <span style={{
                                                    display: 'inline-flex',
                                                    alignItems: 'center',
                                                    gap: '4px',
                                                    background: 'rgba(255, 42, 95, 0.12)',
                                                    border: '1px solid rgba(255, 42, 95, 0.35)',
                                                    borderRadius: 'var(--radius-sm)',
                                                    padding: '1px 8px',
                                                    fontSize: 'var(--text-xs)',
                                                    fontWeight: 'bold',
                                                    color: 'var(--color-accent)',
                                                    fontFamily: 'var(--font-mono)',
                                                }}>
                                                    <span>CODE:</span> {songbook.slug.toUpperCase()}
                                                </span>
                                            )}
                                            {songbook.is_public ? (
                                                <span style={{ color: '#4ade80', fontSize: 'var(--text-xs)', fontWeight: 'bold' }}>● Public</span>
                                            ) : (
                                                <span style={{ color: 'var(--color-text-dim)', fontSize: 'var(--text-xs)' }}>○ Private</span>
                                            )}
                                            {!perms.isOwner && (
                                                <span style={{
                                                    display: 'inline-flex',
                                                    alignItems: 'center',
                                                    gap: '4px',
                                                    background: 'rgba(99, 102, 241, 0.14)',
                                                    border: '1px solid rgba(99, 102, 241, 0.35)',
                                                    borderRadius: 'var(--radius-sm)',
                                                    padding: '1px 8px',
                                                    fontSize: 'var(--text-xs)',
                                                    fontWeight: 600,
                                                    color: '#a5b4fc',
                                                }}>
                                                    Shared{songbook.owner_display_name ? ` by ${songbook.owner_display_name}` : ''}
                                                    {' · '}
                                                    {[perms.canEditCatalog && 'Edit', perms.canHostShows && 'Host'].filter(Boolean).join(' + ')}
                                                </span>
                                            )}
                                        </div>
                                        <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: '2px' }}>
                                            {songbook.song_count} {songbook.song_count === 1 ? 'song' : 'songs'}
                                        </div>

                                        {/* Active Event Indicator Badge */}
                                        {hasLiveShow && liveEvent && (
                                            <div style={{
                                                display: 'inline-flex',
                                                alignItems: 'center',
                                                gap: '6px',
                                                background: 'rgba(34, 197, 94, 0.12)',
                                                border: '1px solid rgba(34, 197, 94, 0.35)',
                                                borderRadius: 'var(--radius-md)',
                                                padding: '2px 8px',
                                                fontSize: 'var(--text-xs)',
                                                fontWeight: 'bold',
                                                color: '#4ade80',
                                                marginTop: '6px',
                                            }}>
                                                <span style={{ width: '6px', height: '6px', borderRadius: '50%', background: '#22c55e', boxShadow: '0 0 6px #22c55e' }}></span>
                                                <span>Live in: {liveEvent.venue_name ? `${liveEvent.venue_name} (${liveEvent.join_code})` : `${liveEvent.name} (${liveEvent.join_code})`}</span>
                                            </div>
                                        )}
                                    </div>
                                </div>

                                <div class="kj-songbook-actions">
                                    {(perms.canHostShows || showState.status === 'live') && (showState.status === 'error' ? (
                                        <button
                                            type="button"
                                            class="btn btn-secondary btn-sm"
                                            onClick={loadEvents}
                                            disabled={eventsLoading}
                                            title="Failed to check show status. Click to retry."
                                            style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
                                        >
                                            <span>⚠️</span>
                                            <span>Retry Show Check</span>
                                        </button>
                                    ) : showState.status === 'loading' ? (
                                        <button
                                            type="button"
                                            class="btn btn-secondary btn-sm"
                                            disabled
                                            style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
                                        >
                                            <span>⏳</span>
                                            <span>Checking Shows...</span>
                                        </button>
                                    ) : showState.status === 'live' ? (
                                        <a
                                            class="btn btn-primary btn-sm"
                                            href={showState.queueUrl}
                                            style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
                                        >
                                            <span>🎤</span>
                                            <span>Live Show Queue</span>
                                        </a>
                                    ) : (
                                        <button
                                            type="button"
                                            class="btn btn-primary btn-sm"
                                            onClick={() => startShow(songbook.id)}
                                            style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
                                        >
                                            <span>🎤</span>
                                            <span>Start Show</span>
                                        </button>
                                    ))}

                                    {perms.canEditCatalog && (
                                        <>
                                            <button type="button" class="btn btn-secondary btn-sm" onClick={() => setManaging(songbook)}>
                                                🎵 Manage Songs
                                            </button>
                                            <button type="button" class="btn btn-secondary btn-sm kj-desktop-only" onClick={() => setAssistant(songbook)}>
                                                ⚡ Assistant Sync
                                            </button>
                                        </>
                                    )}

                                    {perms.canManageAccess && (
                                        <button type="button" class="btn btn-secondary btn-sm" onClick={() => setAccessSongbook(songbook)}>
                                            👥 Manage Access
                                        </button>
                                    )}

                                    {(perms.canEditCatalog || perms.canManageBook || songbook.is_public) && (
                                    <ActionPopover
                                        label={`More actions for ${songbook.name}`}
                                        panelTitle={songbook.name}
                                        triggerText="⋯ More"
                                    >
                                        {(close) => (
                                            <>
                                                {perms.canEditCatalog && (
                                                    <>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item kj-mobile-only"
                                                            onClick={() => { close(); setAssistant(songbook) }}
                                                        >
                                                            <span>⚡</span>
                                                            <span>Assistant Sync</span>
                                                        </button>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item"
                                                            onClick={() => { close(); exportCSV(songbook) }}
                                                            disabled={exportingId === songbook.id}
                                                        >
                                                            <span>{exportingId === songbook.id ? '⏳' : '📥'}</span>
                                                            <span>{exportingId === songbook.id ? 'Exporting CSV...' : 'Export CSV'}</span>
                                                        </button>
                                                    </>
                                                )}
                                                {songbook.is_public && songbook.public_id && (
                                                    <>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item"
                                                            onClick={() => { copyLink(songbook); close() }}
                                                        >
                                                            <span>🔗</span>
                                                            <span>Copy Share Link</span>
                                                        </button>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item"
                                                            onClick={() => { close(); setQrSongbook(songbook) }}
                                                        >
                                                            <span>📱</span>
                                                            <span>Download QR Code</span>
                                                        </button>
                                                        <a
                                                            class="kj-action-item"
                                                            href={`/book/${songbook.slug || songbook.public_id}`}
                                                            target="_blank"
                                                            rel="noopener noreferrer"
                                                            onClick={close}
                                                        >
                                                            <span>👁️</span>
                                                            <span>Preview Public Book</span>
                                                        </a>
                                                    </>
                                                )}
                                                {perms.canManageBook && (
                                                    <>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item"
                                                            onClick={() => { close(); update(songbook, { is_public: !songbook.is_public }) }}
                                                        >
                                                            <span>{songbook.is_public ? '🔒' : '🌐'}</span>
                                                            <span>{songbook.is_public ? 'Make Private' : 'Publish Songbook'}</span>
                                                        </button>
                                                        <button
                                                            type="button"
                                                            class="kj-action-item"
                                                            onClick={() => { close(); setEditing({ id: songbook.id, name: songbook.name, slug: songbook.slug || '' }) }}
                                                        >
                                                            <span>✏️</span>
                                                            <span>Edit Details</span>
                                                        </button>
                                                        <hr class="kj-action-divider" />
                                                        <button
                                                            type="button"
                                                            class="kj-action-item kj-action-item-danger"
                                                            onClick={() => { close(); remove(songbook) }}
                                                            title={events.some(e => e.songbook_id === songbook.id) ? 'Cannot delete while linked to events' : 'Delete songbook'}
                                                        >
                                                            <span>🗑️</span>
                                                            <span>Delete Songbook</span>
                                                        </button>
                                                    </>
                                                )}
                                            </>
                                        )}
                                    </ActionPopover>
                                    )}
                                </div>
                            </>
                        )}
                    </div>
                )
            })}

            <CreateShowModal
                isOpen={!!createModalSongbookId}
                onClose={cancelCreateShow}
                songbooks={filterHostableSongbooks(songbooks)}
                onCreateShow={createShow}
                creatingShow={creatingShow}
                initialSongbookId={createModalSongbookId || ''}
            />

            <SongbookQRModal
                songbook={qrSongbook}
                onClose={() => setQrSongbook(null)}
            />

            <ManageAccessModal
                isOpen={!!accessSongbook}
                songbook={accessSongbook}
                onClose={() => setAccessSongbook(null)}
            />

            <DeleteSongbookModal
                pendingDelete={pendingDelete}
                deleting={deletingBook}
                onConfirm={confirmForeignDelete}
                onCancel={() => setPendingDelete(null)}
            />
        </div>
    )
}
