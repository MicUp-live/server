import { useState, useEffect } from 'preact/hooks'

export function AnnouncementModal({
    isOpen,
    onClose,
    currentEvent,
    onSaveAnnouncement,
    updatingFeatures = false,
}) {
    const [announcementText, setAnnouncementText] = useState('')

    useEffect(() => {
        if (isOpen) {
            setAnnouncementText(currentEvent?.announcement || '')
        }
    }, [isOpen, currentEvent?.announcement])

    if (!isOpen) return null

    const handleSubmit = async (e) => {
        e.preventDefault()
        const ok = await onSaveAnnouncement(announcementText)
        if (ok) {
            onClose()
        }
    }

    const handleClear = async () => {
        setAnnouncementText('')
        const ok = await onSaveAnnouncement('')
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
            aria-labelledby="announcement-title"
        >
            <div
                class="modal-sheet"
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div class="kj-modal-header">
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <span style={{ fontSize: '1.5rem' }}>📢</span>
                        <h3 id="announcement-title" style={{ margin: 0 }}>Broadcast Shout-Out</h3>
                    </div>
                    <button type="button" class="btn btn-secondary btn-sm" onClick={onClose} aria-label="Close announcement modal">✕</button>
                </div>
                <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, overflow: 'hidden' }}>
                    <div class="kj-modal-body">
                        <p style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: 0, marginBottom: 'var(--space-4)' }}>
                            This announcement will be displayed live on the TV Stage monitor and on all singer phones.
                        </p>
                        <textarea
                            placeholder="e.g. Last call for singer signups! Drink special: $5 draft beers at the bar."
                            value={announcementText}
                            onInput={e => setAnnouncementText(e.currentTarget.value)}
                            rows={3}
                            maxLength={140}
                            style={{
                                width: '100%',
                                padding: 'var(--space-3)',
                                fontSize: 'var(--text-base)',
                                borderRadius: 'var(--radius-md)',
                                border: '1px solid var(--color-border)',
                                background: 'var(--color-surface-alt)',
                                color: 'var(--color-text)',
                                resize: 'none',
                                marginBottom: 'var(--space-2)',
                            }}
                            autoFocus
                        />
                        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                                {announcementText.length}/140 chars
                            </span>
                            {currentEvent?.announcement && (
                                <button
                                    type="button"
                                    class="btn btn-secondary btn-sm"
                                    style={{ color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.4)' }}
                                    onClick={handleClear}
                                >
                                    Clear Announcement
                                </button>
                            )}
                        </div>
                    </div>
                    <div class="kj-modal-footer">
                        <button type="button" class="btn btn-secondary" onClick={onClose}>Cancel</button>
                        <button type="submit" class="btn btn-primary" disabled={updatingFeatures}>
                            {updatingFeatures ? 'Saving...' : 'Broadcast 📢'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
