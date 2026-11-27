import { useState, useEffect } from 'preact/hooks'

const KEY_OPTIONS = [
    { value: -3, label: '-3 ♭' },
    { value: -2, label: '-2 ♭' },
    { value: -1, label: '-1 ♭' },
    { value: 0, label: 'Standard' },
    { value: 1, label: '+1 ♯' },
    { value: 2, label: '+2 ♯' },
    { value: 3, label: '+3 ♯' },
]

export function ReplaceSongModal({
    request,
    allowKeyChange = true,
    allowWriteIns = false,
    onClose,
    onSubmit,
}) {
    const [songTitle, setSongTitle] = useState('')
    const [songArtist, setSongArtist] = useState('')
    const [keyChange, setKeyChange] = useState(0)
    const [notes, setNotes] = useState('')
    const [saving, setSaving] = useState(false)
    const [error, setError] = useState('')

    useEffect(() => {
        if (!request) return
        setSongTitle(request.song_title || '')
        setSongArtist(request.song_artist || '')
        setKeyChange(typeof request.key_change === 'number' ? request.key_change : 0)
        setNotes(request.notes || '')
        setError('')
    }, [request])

    if (!request) return null

    const canEditTitleArtist = allowWriteIns || !!request.is_write_in

    const handleSubmit = async (e) => {
        e.preventDefault()
        const trimmedTitle = songTitle.trim()
        const trimmedArtist = songArtist.trim()
        if (!trimmedTitle) {
            setError('Please enter a song title.')
            return
        }
        const songChanged = trimmedTitle !== (request.song_title || '') || trimmedArtist !== (request.song_artist || '')
        setSaving(true)
        setError('')
        const res = await onSubmit(request.id, {
            song_id: songChanged ? '' : (request.song_id || ''),
            song_title: trimmedTitle,
            song_artist: trimmedArtist,
            key_change: allowKeyChange ? keyChange : 0,
            notes: notes.trim(),
            is_write_in: songChanged ? true : !!request.is_write_in,
        })
        setSaving(false)
        if (res?.ok) {
            onClose()
        } else {
            setError(res?.error || 'Could not update song.')
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="replace-song-modal-title"
            style={{ zIndex: 1100 }}
        >
            <div
                class="modal-sheet"
                onClick={e => e.stopPropagation()}
                style={{ maxWidth: '460px', width: '100%' }}
            >
                <div style={{
                    padding: 'var(--space-4) var(--space-5)',
                    borderBottom: '1px solid var(--color-border)',
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                }}>
                    <div>
                        <h3 id="replace-song-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)', color: 'var(--color-text)' }}>
                            🔄 Change Your Song
                        </h3>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                            Your place in the rotation stays the same.
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close song replacement modal"
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                    >
                        ✕
                    </button>
                </div>

                <form onSubmit={handleSubmit} style={{ padding: 'var(--space-4) var(--space-5)' }}>
                    {error && (
                        <div
                            role="alert"
                            style={{
                                marginBottom: 'var(--space-3)',
                                padding: 'var(--space-2) var(--space-3)',
                                borderRadius: 'var(--radius-md)',
                                background: 'rgba(239, 68, 68, 0.12)',
                                border: '1px solid rgba(239, 68, 68, 0.35)',
                                color: 'var(--color-danger)',
                                fontSize: 'var(--text-xs)',
                            }}
                        >
                            {error}
                        </div>
                    )}

                    <div style={{ marginBottom: 'var(--space-3)' }}>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'var(--font-bold)', color: 'var(--color-text-muted)', marginBottom: '4px' }}>
                            Song Title
                        </label>
                        <input
                            type="text"
                            class="input"
                            value={songTitle}
                            onInput={e => setSongTitle(e.target.value)}
                            maxLength={150}
                            required
                            disabled={!canEditTitleArtist}
                            placeholder="Enter new song title"
                        />
                    </div>

                    <div style={{ marginBottom: 'var(--space-3)' }}>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'var(--font-bold)', color: 'var(--color-text-muted)', marginBottom: '4px' }}>
                            Artist
                        </label>
                        <input
                            type="text"
                            class="input"
                            value={songArtist}
                            onInput={e => setSongArtist(e.target.value)}
                            maxLength={150}
                            disabled={!canEditTitleArtist}
                            placeholder="Enter artist name"
                        />
                        {!canEditTitleArtist && (
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '4px' }}>
                                Custom write-ins are disabled for this show; you can update your pitch or notes here.
                            </div>
                        )}
                    </div>

                    {allowKeyChange && (
                        <div style={{ marginBottom: 'var(--space-3)' }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'var(--font-bold)', color: 'var(--color-text-muted)', marginBottom: '4px' }}>
                                Pitch / Key
                            </label>
                            <div style={{ display: 'flex', gap: 'var(--space-1)', flexWrap: 'wrap' }}>
                                {KEY_OPTIONS.map(opt => (
                                    <button
                                        key={opt.value}
                                        type="button"
                                        onClick={() => setKeyChange(opt.value)}
                                        class={keyChange === opt.value ? 'btn btn-primary btn-sm' : 'btn btn-secondary btn-sm'}
                                    >
                                        {opt.label}
                                    </button>
                                ))}
                            </div>
                        </div>
                    )}

                    <div style={{ marginBottom: 'var(--space-4)' }}>
                        <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'var(--font-bold)', color: 'var(--color-text-muted)', marginBottom: '4px' }}>
                            Notes for Host (Optional)
                        </label>
                        <input
                            type="text"
                            class="input"
                            value={notes}
                            onInput={e => setNotes(e.target.value)}
                            maxLength={300}
                            placeholder="Any notes for the KJ"
                        />
                    </div>

                    <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--space-2)' }}>
                        <button type="button" class="btn btn-secondary btn-sm" onClick={onClose} disabled={saving}>
                            Cancel
                        </button>
                        <button type="submit" class="btn btn-primary btn-sm" disabled={saving}>
                            {saving ? 'Saving...' : 'Save Song Change'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
