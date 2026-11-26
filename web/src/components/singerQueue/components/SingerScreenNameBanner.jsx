export function SingerScreenNameBanner({ screenName, onOpenEdit }) {
    return (
        <div style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            flexWrap: 'wrap',
            gap: 'var(--space-2)',
            padding: 'var(--space-3) var(--space-4)',
            background: 'rgba(255, 255, 255, 0.04)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-lg)',
            marginBottom: 'var(--space-4)',
        }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0, flex: '1 1 auto' }}>
                <span style={{ fontSize: '1.1rem' }}>🎤</span>
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    Singing as: <strong style={{ color: 'var(--color-text)', fontSize: 'var(--text-sm)' }}>{screenName || 'Singer'}</strong>
                </div>
            </div>
            <button
                type="button"
                onClick={onOpenEdit}
                class="btn btn-secondary btn-sm"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '6px 12px',
                    minHeight: '32px',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                    flexShrink: 0,
                    whiteSpace: 'nowrap',
                }}
                id="btn-change-screen-name-queue"
            >
                <span>✏️</span>
                <span>Change Screen Name</span>
            </button>
        </div>
    )
}
