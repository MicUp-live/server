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
import { SongbookCard } from './components/SongbookCard.jsx'
import { useStartShowHandoff } from './hooks/useStartShowHandoff.js'
import { filterHostableSongbooks } from '../../utils/songbookPermissions.js'
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
        } catch (err) {
            setError(err?.message || 'Failed to load songbooks')
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => { void load() }, [])

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
            <AssistantImport songbookId={assistant.id} onComplete={() => { setAssistant(null); void load() }} onCancel={() => setAssistant(null)} />
        </div>
    )

    if (managing) {
        return (
            <SongManagerView
                songbook={managing}
                onClose={() => { setManaging(null); void load() }}
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

            {songbooks.map(songbook => (
                <SongbookCard
                    key={songbook.id}
                    songbook={songbook}
                    events={events}
                    eventsLoading={eventsLoading}
                    eventsError={eventsError}
                    editing={editing}
                    saving={saving}
                    exportingId={exportingId}
                    onEditChange={setEditing}
                    onUpdate={update}
                    onCancelEdit={() => setEditing(null)}
                    onStartEdit={setEditing}
                    onRetryEvents={loadEvents}
                    onStartShow={startShow}
                    onManageSongs={setManaging}
                    onAssistant={setAssistant}
                    onManageAccess={setAccessSongbook}
                    onExportCSV={exportCSV}
                    onCopyLink={copyLink}
                    onQr={setQrSongbook}
                    onDelete={remove}
                />
            ))}

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
