import { useState, useEffect } from 'preact/hooks'

const QUICK_KEY_OFFSETS = [-3, -2, -1, 0, 1, 2, 3]

export function EditRequestModal({
    targetRequest,
    onClose,
    onConfirmEdit,
    editingInProgress = false,
}) {
    const [singerName, setSingerName] = useState('')
    const [songTitle, setSongTitle] = useState('')
    const [songArtist, setSongArtist] = useState('')
    const [keyChange, setKeyChange] = useState(0)
    const [notes, setNotes] = useState('')

    useEffect(() => {
        if (targetRequest) {
            setSingerName(targetRequest.singer_name || '')
            setSongTitle(targetRequest.song_title || '')
            setSongArtist(targetRequest.song_artist || '')
            setKeyChange(Number.isInteger(targetRequest.key_change) ? targetRequest.key_change : 0)
            setNotes(targetRequest.notes || '')
        }
    }, [targetRequest])

    if (!targetRequest) return null

    const handleSave = async (e) => {
        e?.preventDefault?.()
        if (!singerName.trim() || !songTitle.trim()) return

        const success = await onConfirmEdit(targetRequest, {
            singer_name: singerName.trim(),
            song_title: songTitle.trim(),
            song_artist: songArtist.trim(),
            key_change: keyChange,
            notes: notes.trim(),
        })
        if (success) {
            onClose()
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={() => !editingInProgress && onClose()}
            role="dialog"
            aria-modal="true"
            aria-labelledby="edit-request-modal-title"
        >
            <div
                class="modal-sheet modal-sheet-wide"
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div
                    class="kj-modal-header"
                    style={{ background: 'linear-gradient(180deg, rgba(255, 42, 95, 0.12) 0%, transparent 100%)' }}
                >
                    <div>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-accent)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Edit Request Details
                        </div>
                        <h3 id="edit-request-modal-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-lg)', color: 'var(--color-text)' }}>
                            Modify Singer or Song
                        </h3>
                    </div>
                    <button
                        type="button"
                        onClick={() => !editingInProgress && onClose()}
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                        aria-label="Close modal"
                    >
                        ✕
                    </button>
                </div>

                <form onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, overflow: 'hidden' }}>
                    <div class="kj-modal-body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-4)' }}>
                    <div>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                            Singer Name *
                        </label>
                        <input
                            type="text"
                            value={singerName}
                            onInput={e => setSingerName(e.target.value)}
                            placeholder="Enter singer name..."
                            required
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-sm)',
                                padding: 'var(--space-2) var(--space-3)',
                                background: 'var(--color-surface-alt)',
                                border: '1px solid var(--color-border)',
                                borderRadius: 'var(--radius-md)',
                                color: 'var(--color-text)',
                                boxSizing: 'border-box',
                            }}
                        />
                    </div>

                    <div>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                            Song Title *
                        </label>
                        <input
                            type="text"
                            value={songTitle}
                            onInput={e => setSongTitle(e.target.value)}
                            placeholder="e.g. Bohemian Rhapsody"
                            required
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-sm)',
                                padding: 'var(--space-2) var(--space-3)',
                                background: 'var(--color-surface-alt)',
                                border: '1px solid var(--color-border)',
                                borderRadius: 'var(--radius-md)',
                                color: 'var(--color-text)',
                                boxSizing: 'border-box',
                            }}
                        />
                    </div>

                    <div>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                            Artist Name
                        </label>
                        <input
                            type="text"
                            value={songArtist}
                            onInput={e => setSongArtist(e.target.value)}
                            placeholder="e.g. Queen"
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-sm)',
                                padding: 'var(--space-2) var(--space-3)',
                                background: 'var(--color-surface-alt)',
                                border: '1px solid var(--color-border)',
                                borderRadius: 'var(--radius-md)',
                                color: 'var(--color-text)',
                                boxSizing: 'border-box',
                            }}
                        />
                    </div>

                    <div>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                            Key / Pitch Change ({keyChange === 0 ? 'Standard' : (keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`)})
                        </label>
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--space-2)', alignItems: 'center' }}>
                            {QUICK_KEY_OFFSETS.map(offset => (
                                <button
                                    key={offset}
                                    type="button"
                                    onClick={() => setKeyChange(offset)}
                                    style={{
                                        background: keyChange === offset ? 'var(--color-accent)' : 'var(--color-surface-alt)',
                                        border: `1px solid ${keyChange === offset ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                        color: keyChange === offset ? '#fff' : 'var(--color-text-dim)',
                                        borderRadius: 'var(--radius-full)',
                                        padding: '4px 12px',
                                        fontSize: 'var(--text-xs)',
                                        cursor: 'pointer',
                                        fontWeight: keyChange === offset ? 'bold' : 'normal',
                                        transition: 'all 0.15s ease',
                                    }}
                                >
                                    {offset === 0 ? '0 (Standard)' : (offset > 0 ? `+${offset}♯` : `${offset}♭`)}
                                </button>
                            ))}
                        </div>
                    </div>

                    <div>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                            Singer Notes / Instructions
                        </label>
                        <input
                            type="text"
                            value={notes}
                            onInput={e => setNotes(e.target.value)}
                            placeholder="e.g. Duet with friend, Birthday shoutout..."
                            style={{
                                width: '100%',
                                fontSize: 'var(--text-sm)',
                                padding: 'var(--space-2) var(--space-3)',
                                background: 'var(--color-surface-alt)',
                                border: '1px solid var(--color-border)',
                                borderRadius: 'var(--radius-md)',
                                color: 'var(--color-text)',
                                boxSizing: 'border-box',
                            }}
                        />
                    </div>

                    </div>

                    <div class="kj-modal-footer">
                        <button
                            type="button"
                            class="btn btn-secondary"
                            onClick={onClose}
                            disabled={editingInProgress}
                        >
                            Cancel
                        </button>
                        <button
                            type="submit"
                            class="btn btn-primary"
                            disabled={editingInProgress || !singerName.trim() || !songTitle.trim()}
                        >
                            {editingInProgress ? 'Saving...' : 'Save Changes'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
