import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function SingerQueueHeader({
    displayTitle,
    eventStatus,
    alertsEnabled,
    onToggleAlerts,
    onClose,
    myUpNext,
}) {
    const statusInfo = getShowStatusInfo(eventStatus)
    const headerAccent = statusInfo.isEnded ? 'var(--color-danger)' : 'var(--color-chorus)'
    const headerBg = statusInfo.isEnded
        ? 'linear-gradient(180deg, rgba(239, 68, 68, 0.12) 0%, transparent 100%)'
        : 'linear-gradient(180deg, rgba(245, 158, 11, 0.12) 0%, transparent 100%)'

    return (
        <>
            <div class="modal-drag-handle" aria-hidden="true"></div>
            {/* Header */}
            <div style={{
                padding: 'var(--space-4) var(--space-6)',
                background: headerBg,
                borderBottom: '1px solid var(--color-border)',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                flexShrink: 0,
            }}>
                <div>
                    <div style={{
                        fontSize: 'var(--text-xs)',
                        fontWeight: 'var(--font-bold)',
                        color: headerAccent,
                        textTransform: 'uppercase',
                        letterSpacing: '0.08em',
                        display: 'flex',
                        alignItems: 'center',
                        gap: 'var(--space-2)'
                    }}>
                        <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: headerAccent, display: 'inline-block', boxShadow: statusInfo.isActive ? `0 0 8px ${headerAccent}` : 'none' }}></span>
                        <span>{statusInfo.queueHeaderLabel}</span>
                    </div>
                    <h3 id="singer-queue-title" style={{ margin: '4px 0 0', fontSize: 'var(--text-xl)', color: 'var(--color-text)' }}>
                        {displayTitle}
                    </h3>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                    {!statusInfo.isEnded && typeof window !== 'undefined' && 'Notification' in window && (
                        <button
                            type="button"
                            onClick={onToggleAlerts}
                            style={{
                                background: alertsEnabled ? 'rgba(34, 197, 94, 0.15)' : 'rgba(100, 116, 139, 0.15)',
                                border: `1px solid ${alertsEnabled ? 'rgba(34, 197, 94, 0.4)' : 'rgba(100, 116, 139, 0.4)'}`,
                                color: alertsEnabled ? '#86efac' : '#94a3b8',
                                borderRadius: 'var(--radius-full)',
                                padding: '4px 10px',
                                minHeight: '36px',
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'bold',
                                cursor: 'pointer',
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: '4px',
                            }}
                            title={alertsEnabled ? "Click to turn off stage alerts" : "Click to enable stage alerts"}
                        >
                            <span>{alertsEnabled ? '🔔 Alerts On' : '🔕 Alerts Off'}</span>
                        </button>
                    )}

                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close"
                        style={{
                            minWidth: 'var(--touch-target-min, 48px)',
                            minHeight: 'var(--touch-target-min, 48px)',
                            background: 'none',
                            border: 'none',
                            color: 'var(--color-text-dim)',
                            fontSize: '1.4rem',
                            cursor: 'pointer',
                            padding: 0,
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            borderRadius: 'var(--radius-full)',
                            lineHeight: 1,
                        }}
                    >
                        ✕
                    </button>
                </div>
            </div>

            {/* Up Next Alert Banner if Singer is next */}
            {myUpNext && !statusInfo.isEnded && (
                <div style={{
                    background: 'linear-gradient(90deg, #ff2a5f 0%, #dc1d4c 100%)',
                    color: '#fff',
                    padding: 'var(--space-3) var(--space-5)',
                    fontWeight: 'var(--font-bold)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: 'var(--space-2)',
                    boxShadow: '0 4px 12px var(--color-accent-glow)',
                    textAlign: 'center',
                }}>
                    <span style={{ fontSize: '1.2rem' }}>🎉</span>
                    <span>You're Up Next! Head toward the stage!</span>
                </div>
            )}
        </>
    )
}
