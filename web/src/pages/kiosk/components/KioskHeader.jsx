import { BrandIcon } from '../../../components/BrandIcon.jsx'
import { ShowStatusBanner } from '../../../components/ShowStatusBanner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function KioskHeader({ event, onOpenExitModal }) {
    const statusInfo = getShowStatusInfo(event)

    return (
        <header style={{ flexShrink: 0 }}>
            <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                padding: 'var(--space-4) var(--space-6)',
                background: 'var(--color-surface)',
                borderBottom: '1px solid var(--color-border)',
            }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                    <BrandIcon size={32} />
                    <div>
                        <h2 style={{ margin: 0, fontSize: 'var(--text-lg)', fontFamily: 'var(--font-display)' }}>
                            {event?.venue_name ? `${event.venue_name} — ${event.name}` : 'Karaoke Kiosk'}
                        </h2>
                        <span style={{ fontSize: 'var(--text-xs)', color: statusInfo.isPaused ? 'var(--color-chorus)' : statusInfo.isEnded ? 'var(--color-danger)' : 'var(--color-text-dim)' }}>
                            {(statusInfo.isPaused || statusInfo.isEnded) ? statusInfo.badgeLabel : 'Touch to search and request songs'}
                        </span>
                    </div>
                </div>

                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                    <div style={{
                        background: 'rgba(255, 42, 95, 0.1)',
                        border: '1px solid rgba(255, 42, 95, 0.3)',
                        padding: 'var(--space-1) var(--space-3)',
                        borderRadius: 'var(--radius-full)',
                        fontSize: 'var(--text-xs)',
                        fontWeight: 'var(--font-bold)',
                        color: 'var(--color-accent)',
                    }}>
                        CODE: {event?.join_code}
                    </div>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        style={{ opacity: 0.6 }}
                        onClick={onOpenExitModal}
                        title="Host Exit Kiosk"
                    >
                        🔒 Exit
                    </button>
                </div>
            </div>

            {/* Announcement Ticker if present */}
            {event?.announcement && !statusInfo.isEnded && (
                <div style={{
                    background: 'linear-gradient(90deg, #ff2a5f 0%, #f59e0b 100%)',
                    color: '#fff',
                    padding: 'var(--space-2) var(--space-6)',
                    fontWeight: 'var(--font-semibold)',
                    fontSize: 'var(--text-sm)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--space-2)',
                }}>
                    <span>📢</span>
                    <span>{event.announcement}</span>
                </div>
            )}

            <ShowStatusBanner
                status={event?.status}
                style={{ margin: 'var(--space-2) var(--space-6) 0' }}
            />
        </header>
    )
}
