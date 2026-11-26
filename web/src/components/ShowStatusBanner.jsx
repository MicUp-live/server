import { getShowStatusInfo } from '../utils/showStatus.js'

/**
 * Accessible status banner displayed on singer/kiosk surfaces when a show is
 * paused or ended. Renders nothing when the show is active or unset.
 */
export function ShowStatusBanner({
    status,
    compact = false,
    onJoinAnotherShow,
    style = {},
}) {
    const info = getShowStatusInfo(status)
    if (!info.isPaused && !info.isEnded) return null

    const accentColor = info.isPaused ? 'var(--color-chorus)' : 'var(--color-danger)'
    const bgColor = info.isPaused ? 'rgba(245, 158, 11, 0.14)' : 'rgba(239, 68, 68, 0.14)'
    const borderColor = info.isPaused ? 'rgba(245, 158, 11, 0.45)' : 'rgba(239, 68, 68, 0.45)'

    return (
        <div
            role="status"
            aria-live="polite"
            style={{
                background: bgColor,
                border: `1px solid ${borderColor}`,
                borderRadius: 'var(--radius-lg)',
                padding: compact ? 'var(--space-2) var(--space-3)' : 'var(--space-3) var(--space-4)',
                marginBottom: compact ? 'var(--space-4)' : 'var(--space-3)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 'var(--space-3)',
                flexWrap: 'wrap',
                fontSize: compact ? 'var(--text-xs)' : 'var(--text-sm)',
                ...style,
            }}
        >
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-2)', flex: 1, minWidth: 0 }}>
                <span aria-hidden="true" style={{ fontSize: compact ? '1rem' : '1.15rem', lineHeight: 1.2 }}>
                    {info.bannerIcon}
                </span>
                <div style={{ minWidth: 0 }}>
                    <strong style={{ color: accentColor }}>{info.bannerTitle}: </strong>
                    <span style={{ color: 'var(--color-text)' }}>
                        {compact ? info.compactBannerMessage : info.bannerMessage}
                    </span>
                </div>
            </div>
            {info.isEnded && onJoinAnotherShow && (
                <button
                    type="button"
                    onClick={onJoinAnotherShow}
                    class="btn btn-secondary btn-sm"
                    style={{
                        borderColor,
                        color: 'var(--color-text)',
                        fontWeight: 'var(--font-semibold)',
                        whiteSpace: 'nowrap',
                        flexShrink: 0,
                    }}
                >
                    Join Another Show
                </button>
            )}
        </div>
    )
}
