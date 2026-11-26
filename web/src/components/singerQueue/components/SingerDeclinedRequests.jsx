export function SingerDeclinedRequests({ declinedRequests, onDismiss }) {
    if (!declinedRequests || declinedRequests.length === 0) return null

    return (
        <div style={{ marginBottom: 'var(--space-6)' }}>
            <div style={{
                fontSize: 'var(--text-xs)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-danger)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                marginBottom: 'var(--space-2)',
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
            }}>
                <span>⚠️ Declined Requests ({declinedRequests.length})</span>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                {declinedRequests.map((req) => (
                    <div
                        key={req.id}
                        style={{
                            background: 'rgba(239, 68, 68, 0.08)',
                            border: '1px solid rgba(239, 68, 68, 0.3)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-3) var(--space-4)',
                        }}
                    >
                        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--space-2)' }}>
                            <div style={{ minWidth: 0, flex: 1 }}>
                                <div style={{ fontWeight: 'var(--font-bold)', color: 'var(--color-text)' }}>
                                    {req.song_title}
                                </div>
                                <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-xs)', marginTop: '2px' }}>
                                    {req.song_artist}
                                </div>
                            </div>
                            <button
                                type="button"
                                onClick={() => onDismiss(req.id)}
                                title="Dismiss notice"
                                style={{
                                    background: 'none',
                                    border: 'none',
                                    color: 'var(--color-text-dim)',
                                    cursor: 'pointer',
                                    padding: '2px 4px',
                                    fontSize: 'var(--text-sm)',
                                    lineHeight: 1,
                                }}
                            >
                                ✕
                            </button>
                        </div>
                        <div style={{
                            marginTop: 'var(--space-2)',
                            padding: '6px 10px',
                            background: 'rgba(239, 68, 68, 0.12)',
                            borderRadius: 'var(--radius-md)',
                            fontSize: 'var(--text-xs)',
                            color: '#fca5a5',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '6px',
                        }}>
                            <span style={{ fontWeight: 'bold' }}>Reason from host:</span>
                            <span>{req.decline_reason || 'Host was unable to accept this song request.'}</span>
                        </div>
                    </div>
                ))}
            </div>
        </div>
    )
}
