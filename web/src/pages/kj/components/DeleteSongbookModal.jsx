export function DeleteSongbookModal({ pendingDelete, deleting, onConfirm, onCancel }) {
    if (!pendingDelete) return null
    const { songbook, affectedShowsCount, activeShowsCount } = pendingDelete

    return (
        <div class="modal-overlay" onClick={onCancel} role="dialog" aria-modal="true" aria-labelledby="delete-songbook-title">
            <div class="modal-sheet" onClick={e => e.stopPropagation()} style={{ maxWidth: '480px' }}>
                <div class="modal-drag-handle" aria-hidden="true" />
                <div class="kj-modal-header">
                    <h3 id="delete-songbook-title" style={{ margin: 0, fontSize: 'var(--text-lg)' }}>
                        Delete "{songbook.name}"?
                    </h3>
                    <button type="button" onClick={onCancel} aria-label="Close delete confirmation" style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}>
                        ✕
                    </button>
                </div>
                <div class="kj-modal-body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                    <p style={{ margin: 0, fontSize: 'var(--text-sm)', lineHeight: 1.5 }}>
                        This songbook is linked to <strong>{affectedShowsCount}</strong> collaborator {affectedShowsCount === 1 ? 'show' : 'shows'}
                        {activeShowsCount > 0 ? ` (${activeShowsCount} currently live)` : ''}.
                    </p>
                    <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5 }}>
                        Confirming deletion will end and detach those shows, keep each host's event and queue history under their existing retention policy, revoke collaborator access, and permanently delete this songbook.
                    </p>
                    <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--space-2)', marginTop: 'var(--space-2)' }}>
                        <button type="button" class="btn btn-secondary btn-sm" onClick={onCancel} disabled={deleting}>
                            Cancel
                        </button>
                        <button type="button" class="btn btn-danger btn-sm" onClick={onConfirm} disabled={deleting}>
                            {deleting ? 'Deleting...' : 'End Linked Shows & Delete Songbook'}
                        </button>
                    </div>
                </div>
            </div>
        </div>
    )
}
