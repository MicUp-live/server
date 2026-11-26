import { showToast } from '../../../components/Toast.jsx'

export function EditSongModal({ editingSong, onChange, onClose, onSave }) {
    if (!editingSong) return null

    const copyLocation = async () => {
        if (!editingSong.location) return
        try {
            if (navigator.clipboard?.writeText) {
                await navigator.clipboard.writeText(editingSong.location)
                showToast('File path copied')
                return
            }
            const ta = document.createElement('textarea')
            ta.value = editingSong.location
            ta.style.position = 'fixed'
            ta.style.opacity = '0'
            document.body.appendChild(ta)
            ta.select()
            const ok = document.execCommand('copy')
            document.body.removeChild(ta)
            if (ok) showToast('File path copied')
            else showToast('Failed to copy file path', 'error')
        } catch {
            showToast('Failed to copy file path', 'error')
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="edit-song-modal-title"
        >
            <div class="modal-sheet" onClick={e => e.stopPropagation()}>
                <div class="modal-drag-handle" aria-hidden="true" />
                <div class="kj-modal-header">
                    <h3 id="edit-song-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)' }}>
                        Edit Song
                    </h3>
                    <button
                        type="button"
                        onClick={onClose}
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                        aria-label="Close edit song dialog"
                    >
                        ✕
                    </button>
                </div>
                <form onSubmit={onSave} style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
                    <div class="kj-modal-body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                        <div style={{ background: 'rgba(255, 255, 255, 0.03)', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-sm)', padding: '8px 10px' }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '2px' }}>
                                <span style={{ fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-dim)' }}>
                                    Original File / Path
                                </span>
                                {editingSong.location && (
                                    <button
                                        type="button"
                                        class="btn btn-secondary btn-sm"
                                        style={{ padding: '1px 6px', fontSize: '0.7rem' }}
                                        onClick={copyLocation}
                                    >
                                        Copy
                                    </button>
                                )}
                            </div>
                            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 'var(--text-xs)', color: editingSong.location ? 'var(--color-text)' : 'var(--color-text-dim)', wordBreak: 'break-all' }}>
                                {editingSong.locationLoading
                                    ? 'Loading file path...'
                                    : editingSong.locationError
                                        ? 'Unable to load file path'
                                        : (editingSong.location || 'Not recorded (re-import to capture file path)')}
                            </div>
                        </div>
                        <div>
                            <label htmlFor="edit-song-artist" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                Artist *
                            </label>
                            <input
                                id="edit-song-artist"
                                value={editingSong.artist}
                                onInput={e => onChange({ ...editingSong, artist: e.target.value })}
                                required
                                autoFocus
                                style={{ width: '100%' }}
                            />
                        </div>
                        <div>
                            <label htmlFor="edit-song-title" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                Title *
                            </label>
                            <input
                                id="edit-song-title"
                                value={editingSong.title}
                                onInput={e => onChange({ ...editingSong, title: e.target.value })}
                                required
                                style={{ width: '100%' }}
                            />
                        </div>
                        <div class="kj-modal-two-col">
                            <div>
                                <label htmlFor="edit-song-disc" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                    Disc / Code
                                </label>
                                <input
                                    id="edit-song-disc"
                                    value={editingSong.disc}
                                    onInput={e => onChange({ ...editingSong, disc: e.target.value })}
                                    placeholder="e.g. SC8100-01"
                                    style={{ width: '100%' }}
                                />
                            </div>
                            <div>
                                <label htmlFor="edit-song-genre" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                    Genre
                                </label>
                                <input
                                    id="edit-song-genre"
                                    value={editingSong.genre}
                                    onInput={e => onChange({ ...editingSong, genre: e.target.value })}
                                    placeholder="e.g. Classic Rock"
                                    style={{ width: '100%' }}
                                />
                            </div>
                        </div>
                        <div class="kj-modal-two-col">
                            <div>
                                <label htmlFor="edit-song-source" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                    Publisher / Source
                                </label>
                                <input
                                    id="edit-song-source"
                                    value={editingSong.source}
                                    onInput={e => onChange({ ...editingSong, source: e.target.value })}
                                    placeholder="e.g. Sound Choice"
                                    style={{ width: '100%' }}
                                />
                            </div>
                            <div>
                                <label htmlFor="edit-song-video-url" style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '4px', color: 'var(--color-text-dim)' }}>
                                    Video URL
                                </label>
                                <input
                                    id="edit-song-video-url"
                                    value={editingSong.video_url}
                                    onInput={e => onChange({ ...editingSong, video_url: e.target.value })}
                                    placeholder="https://..."
                                    style={{ width: '100%' }}
                                />
                            </div>
                        </div>
                    </div>
                    <div class="kj-modal-footer">
                        <button type="button" class="btn btn-secondary btn-sm" onClick={onClose}>
                            Cancel
                        </button>
                        <button type="submit" class="btn btn-primary btn-sm">
                            Save Changes
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
