import { useState, useEffect } from 'preact/hooks'
import { api } from '../../../api/client.js'
import { filterHostableSongbooks } from '../../../utils/songbookPermissions.js'
import { showToast } from '../../../components/Toast.jsx'

export function RestartShowModal({
    isOpen,
    onClose,
    currentEvent,
    songbooks = [],
    onRestartShow,
    restarting = false,
}) {
    const [restartClearQueue, setRestartClearQueue] = useState(true)
    const hostableSongbooks = filterHostableSongbooks(songbooks)
    const isDetached = !!currentEvent && (currentEvent.songbook_id === '' || !!currentEvent.detached_songbook_name)
    const [replacementSongbookId, setReplacementSongbookId] = useState('')
    const [rebinding, setRebinding] = useState(false)

    useEffect(() => {
        if (isOpen && isDetached) {
            setReplacementSongbookId(hostableSongbooks[0]?.id || '')
        }
    }, [isOpen, isDetached, songbooks])

    if (!isOpen || !currentEvent) return null

    const handleRestart = async () => {
        if (isDetached) {
            if (!replacementSongbookId) return
            setRebinding(true)
            try {
                await api.patch(`/events/${currentEvent.id}`, { songbook_id: replacementSongbookId })
            } catch (err) {
                showToast(err.message || 'Failed to assign replacement songbook', 'error')
                setRebinding(false)
                return
            }
            setRebinding(false)
        }
        const ok = await onRestartShow(restartClearQueue)
        if (ok) {
            onClose()
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="restart-show-title"
        >
            <div
                class="modal-sheet"
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div
                    class="kj-modal-header"
                    style={{ background: 'linear-gradient(180deg, rgba(34, 197, 94, 0.15) 0%, transparent 100%)' }}
                >
                    <div>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-success)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Resume Gig / Residency
                        </div>
                        <h3 id="restart-show-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-xl)', color: 'var(--color-text)' }}>
                            Restart "{currentEvent.name}"
                        </h3>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close restart show modal"
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                    >
                        ✕
                    </button>
                </div>

                <div class="kj-modal-body">
                    <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', margin: '0 0 var(--space-4)', lineHeight: 1.5 }}>
                        Restarting will activate the show immediately so singers can use the same join code (<strong>{currentEvent.join_code}</strong>) and open the catalog.
                    </p>

                    {isDetached && (
                        <div style={{
                            background: 'rgba(245, 158, 11, 0.1)',
                            border: '1px solid rgba(245, 158, 11, 0.35)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-4)',
                            marginBottom: 'var(--space-4)',
                        }}>
                            <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: '4px' }}>
                                Original songbook removed{currentEvent.detached_songbook_name ? ` ("${currentEvent.detached_songbook_name}")` : ''}
                            </div>
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-2)' }}>
                                Select an authorized replacement songbook before restarting this show:
                            </div>
                            {hostableSongbooks.length > 0 ? (
                                <select
                                    value={replacementSongbookId}
                                    onChange={e => setReplacementSongbookId(e.target.value)}
                                    style={{ width: '100%' }}
                                >
                                    {hostableSongbooks.map(sb => (
                                        <option key={sb.id} value={sb.id}>{sb.name}</option>
                                    ))}
                                </select>
                            ) : (
                                <div style={{ fontSize: 'var(--text-xs)', color: '#f87171' }}>
                                    You have no authorized songbooks available for hosting. Create or obtain hosting access to a songbook first.
                                </div>
                            )}
                        </div>
                    )}

                    <div style={{
                        background: 'var(--color-surface-alt)',
                        border: '1px solid var(--color-border)',
                        borderRadius: 'var(--radius-lg)',
                        padding: 'var(--space-4)',
                        marginBottom: 'var(--space-5)',
                    }}>
                        <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-3)', cursor: 'pointer' }}>
                            <input
                                type="checkbox"
                                checked={restartClearQueue}
                                onChange={e => setRestartClearQueue(e.target.checked)}
                                style={{ marginTop: '3px' }}
                            />
                            <div>
                                <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                    Start with a fresh queue
                                </div>
                                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '2px' }}>
                                    Archives past session's pending requests so tonight starts with a clean slate.
                                </div>
                            </div>
                        </label>
                    </div>

                </div>

                <div class="kj-modal-footer">
                    <button
                        type="button"
                        onClick={onClose}
                        class="btn btn-secondary btn-sm"
                        disabled={restarting}
                    >
                        Cancel
                    </button>
                    <button
                        type="button"
                        onClick={handleRestart}
                        class="btn btn-primary btn-sm"
                        disabled={restarting || rebinding || (isDetached && !replacementSongbookId)}
                        style={{ background: 'var(--color-success)', borderColor: 'var(--color-success)' }}
                    >
                        {restarting || rebinding ? 'Restarting...' : '🔄 Restart Show Now'}
                    </button>
                </div>
            </div>
        </div>
    )
}
