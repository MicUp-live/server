import { useState, useEffect } from 'preact/hooks'

const QUICK_DECLINE_REASONS = [
    'Missing / corrupted track file',
    'Song already performed tonight',
    'Closing time / Last call reached',
    'Singer not present in venue',
    'Duplicate request in queue',
]

export function DeclineRequestModal({
    targetRequest,
    onClose,
    onConfirmDecline,
    decliningInProgress = false,
}) {
    const [declineReasonText, setDeclineReasonText] = useState('Missing / corrupted track file')

    useEffect(() => {
        if (targetRequest) {
            setDeclineReasonText('Missing / corrupted track file')
        }
    }, [targetRequest])

    if (!targetRequest) return null

    const handleConfirm = async () => {
        const ok = await onConfirmDecline(targetRequest, declineReasonText)
        if (ok) {
            onClose()
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={() => !decliningInProgress && onClose()}
            role="dialog"
            aria-modal="true"
            aria-labelledby="decline-modal-title"
        >
            <div
                class="modal-sheet"
                style={{ borderColor: 'rgba(239, 68, 68, 0.4)', boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px rgba(239, 68, 68, 0.2)' }}
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div
                    class="kj-modal-header"
                    style={{ background: 'linear-gradient(180deg, rgba(239, 68, 68, 0.15) 0%, transparent 100%)' }}
                >
                    <div>
                        <div style={{ fontSize: 'var(--text-xs)', color: '#f87171', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Decline Song Request
                        </div>
                        <h3 id="decline-modal-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-lg)', color: 'var(--color-text)' }}>
                            Decline: "{targetRequest.song_title}"
                        </h3>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '2px' }}>
                            Singer: <strong style={{ color: 'var(--color-text)' }}>{targetRequest.singer_name}</strong>
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={() => !decliningInProgress && onClose()}
                        aria-label="Close decline request modal"
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                    >
                        ✕
                    </button>
                </div>

                <div class="kj-modal-body">
                    <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-2)' }}>
                        Choose a Quick Reason:
                    </label>
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--space-2)', marginBottom: 'var(--space-4)' }}>
                        {QUICK_DECLINE_REASONS.map(reason => (
                            <button
                                key={reason}
                                type="button"
                                onClick={() => setDeclineReasonText(reason)}
                                style={{
                                    background: declineReasonText === reason ? 'rgba(239, 68, 68, 0.25)' : 'var(--color-surface-alt)',
                                    border: `1px solid ${declineReasonText === reason ? '#f87171' : 'var(--color-border)'}`,
                                    color: declineReasonText === reason ? '#fca5a5' : 'var(--color-text-dim)',
                                    borderRadius: 'var(--radius-full)',
                                    padding: '4px 10px',
                                    fontSize: 'var(--text-xs)',
                                    cursor: 'pointer',
                                    fontWeight: declineReasonText === reason ? 'bold' : 'normal',
                                    transition: 'all 0.15s ease',
                                }}
                            >
                                {reason}
                            </button>
                        ))}
                    </div>

                    <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--space-1)' }}>
                        Reason Sent to Singer:
                    </label>
                    <input
                        type="text"
                        value={declineReasonText}
                        onInput={e => setDeclineReasonText(e.target.value)}
                        placeholder="Enter reason or select a chip above..."
                        style={{
                            width: '100%',
                            fontSize: 'var(--text-sm)',
                            padding: 'var(--space-2) var(--space-3)',
                            background: 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            borderRadius: 'var(--radius-md)',
                            color: 'var(--color-text)',
                            marginBottom: 'var(--space-6)',
                            boxSizing: 'border-box',
                        }}
                    />

                </div>

                <div class="kj-modal-footer">
                    <button
                        type="button"
                        class="btn btn-secondary"
                        onClick={onClose}
                        disabled={decliningInProgress}
                    >
                        Cancel
                    </button>
                    <button
                        type="button"
                        class="btn btn-primary"
                        style={{ background: 'var(--color-danger)', borderColor: 'var(--color-danger)' }}
                        onClick={handleConfirm}
                        disabled={decliningInProgress || !declineReasonText.trim()}
                    >
                        {decliningInProgress ? 'Declining...' : 'Confirm Decline'}
                    </button>
                </div>
            </div>
        </div>
    )
}
