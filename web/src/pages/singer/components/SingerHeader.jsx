import { ShowStatusBanner } from '../../../components/ShowStatusBanner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function SingerHeader({
    event,
    songbook,
    totalSongs = 0,
    uniqueArtistsCount = 0,
    totalFavoritesCount = 0,
    viewMode = 'songs',
    setViewMode,
    code,
    myActiveStatus = null,
    queueCount = 0,
    onOpenQueue,
    onOpenJoinPrompt,
}) {
    const statusInfo = getShowStatusInfo(event)
    const isCalledUp = !statusInfo.isEnded && (myActiveStatus?.includes('CALLED') || myActiveStatus?.includes('UP NEXT'))
    const queueButtonLabel = statusInfo.isEnded
        ? 'Show Recap'
        : myActiveStatus || (statusInfo.isPaused ? `Queue (${queueCount}) • Paused` : `Live Queue (${queueCount})`)

    return (
        <div class="public-book-pinned" style={{ flexShrink: 0, display: 'flex', flexDirection: 'column' }}>
            {/* Live Shout-Out Announcement */}
            {event?.announcement && !statusInfo.isEnded && (
                <div style={{
                    background: 'linear-gradient(90deg, rgba(255, 42, 95, 0.25) 0%, rgba(245, 158, 11, 0.25) 100%)',
                    border: '1px solid rgba(255, 42, 95, 0.5)',
                    borderRadius: 'var(--radius-lg)',
                    padding: 'var(--space-3) var(--space-4)',
                    marginBottom: 'var(--space-3)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--space-2)',
                    fontSize: 'var(--text-sm)',
                    boxShadow: '0 0 15px rgba(255, 42, 95, 0.15)',
                }}>
                    <span style={{ fontSize: '1.3rem' }}>📢</span>
                    <div>
                        <strong style={{ color: 'var(--color-accent)' }}>Host Shout-Out: </strong>
                        <span>{event.announcement}</span>
                    </div>
                </div>
            )}

            <div style={{ marginBottom: 'var(--space-3)', flexShrink: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                <div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                        <h2 style={{ margin: 0, fontSize: 'var(--text-2xl)' }}>{event?.name || songbook?.name}</h2>
                        {(statusInfo.isPaused || statusInfo.isEnded) && (
                            <span style={{
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'var(--font-bold)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.06em',
                                padding: '2px 8px',
                                borderRadius: 'var(--radius-full)',
                                background: statusInfo.isPaused ? 'rgba(245, 158, 11, 0.15)' : 'rgba(239, 68, 68, 0.15)',
                                color: statusInfo.isPaused ? 'var(--color-chorus)' : 'var(--color-danger)',
                                border: `1px solid ${statusInfo.isPaused ? 'rgba(245, 158, 11, 0.4)' : 'rgba(239, 68, 68, 0.4)'}`,
                            }}>
                                {statusInfo.badgeLabel}
                            </span>
                        )}
                    </div>
                    <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: 'var(--space-1)', display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                        <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusInfo.dotColor, display: 'inline-block', boxShadow: statusInfo.dotGlow ? `0 0 8px ${statusInfo.dotColor}` : 'none' }}></span>
                        <span>{totalSongs} songs {event && !statusInfo.isEnded ? 'on stage' : 'in catalog'}</span>
                        {event?.venue_name && <span>• @ {event.venue_name}</span>}
                        {event && (
                            <button
                                type="button"
                                onClick={onOpenQueue}
                                class="btn btn-secondary btn-sm"
                                style={{
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    gap: 'var(--space-1)',
                                    color: statusInfo.isEnded ? 'var(--color-text-muted)' : (isCalledUp ? 'var(--color-accent)' : 'var(--color-chorus)'),
                                    borderColor: statusInfo.isEnded ? 'var(--color-border)' : (isCalledUp ? 'var(--color-accent)' : 'var(--color-chorus)'),
                                    fontWeight: 'var(--font-bold)',
                                    padding: '2px 8px',
                                }}
                            >
                                {queueButtonLabel}
                            </button>
                        )}
                        {!event && (
                            <button
                                type="button"
                                onClick={onOpenJoinPrompt}
                                class="btn btn-secondary btn-sm"
                                style={{
                                    color: 'var(--color-chorus)',
                                    borderColor: 'var(--color-chorus)',
                                    fontWeight: 'var(--font-bold)',
                                    padding: '2px 8px',
                                    whiteSpace: 'nowrap',
                                }}
                            >
                                Enter Show Code
                            </button>
                        )}
                    </div>
                </div>
            </div>

            <ShowStatusBanner
                status={event?.status}
                onJoinAnotherShow={onOpenJoinPrompt}
            />

            {/* View Mode Switcher: Songs | By Artist | Favorites */}
            <div style={{
                display: 'flex',
                background: 'var(--color-surface-alt)',
                border: '1px solid var(--color-border)',
                borderRadius: 'var(--radius-lg)',
                padding: '3px',
                marginBottom: 'var(--space-3)',
                gap: '4px',
                flexShrink: 0,
            }}>
                <button
                    type="button"
                    onClick={() => setViewMode('songs')}
                    style={{
                        flex: 1,
                        padding: 'var(--space-2) var(--space-1)',
                        fontSize: 'var(--text-xs)',
                        fontWeight: viewMode === 'songs' ? 'var(--font-bold)' : 'var(--font-semibold)',
                        color: viewMode === 'songs' ? '#fff' : 'var(--color-text-muted)',
                        background: viewMode === 'songs' ? 'var(--color-accent)' : 'transparent',
                        border: 'none',
                        borderRadius: 'var(--radius-md)',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                        boxShadow: viewMode === 'songs' ? '0 0 10px var(--color-accent-glow)' : 'none',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        gap: '4px',
                    }}
                >
                    <span>🎵 Songs</span>
                    <span style={{ opacity: 0.85, fontSize: '0.85em' }}>({totalSongs})</span>
                </button>
                <button
                    type="button"
                    onClick={() => setViewMode('artists')}
                    style={{
                        flex: 1,
                        padding: 'var(--space-2) var(--space-1)',
                        fontSize: 'var(--text-xs)',
                        fontWeight: viewMode === 'artists' ? 'var(--font-bold)' : 'var(--font-semibold)',
                        color: viewMode === 'artists' ? '#fff' : 'var(--color-text-muted)',
                        background: viewMode === 'artists' ? 'var(--color-accent)' : 'transparent',
                        border: 'none',
                        borderRadius: 'var(--radius-md)',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                        boxShadow: viewMode === 'artists' ? '0 0 10px var(--color-accent-glow)' : 'none',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        gap: '4px',
                    }}
                >
                    <span>🎤 By Artist</span>
                    {uniqueArtistsCount !== null && (
                        <span style={{ opacity: 0.85, fontSize: '0.85em' }}>({uniqueArtistsCount})</span>
                    )}
                </button>
                <button
                    type="button"
                    onClick={() => setViewMode('favorites')}
                    style={{
                        flex: 1,
                        padding: 'var(--space-2) var(--space-1)',
                        fontSize: 'var(--text-xs)',
                        fontWeight: viewMode === 'favorites' ? 'var(--font-bold)' : 'var(--font-semibold)',
                        color: viewMode === 'favorites' ? '#fff' : 'var(--color-text-muted)',
                        background: viewMode === 'favorites' ? 'linear-gradient(135deg, #eab308, #ca8a04)' : 'transparent',
                        border: 'none',
                        borderRadius: 'var(--radius-md)',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                        boxShadow: viewMode === 'favorites' ? '0 0 10px rgba(234, 179, 8, 0.4)' : 'none',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        gap: '4px',
                    }}
                >
                    <span>★ Favorites</span>
                    {totalFavoritesCount > 0 && (
                        <span style={{ opacity: 0.9, fontSize: '0.85em' }}>({totalFavoritesCount})</span>
                    )}
                </button>
            </div>
        </div>
    )
}
