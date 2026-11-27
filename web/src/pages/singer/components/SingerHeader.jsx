import { ShowStatusBanner } from '../../../components/ShowStatusBanner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

function getQueueButtonLabel(statusInfo, myActiveStatus, queueCount) {
    if (statusInfo.isEnded) return 'Show Recap'
    if (myActiveStatus) return myActiveStatus
    return statusInfo.isPaused ? `Queue (${queueCount}) • Paused` : `Live Queue (${queueCount})`
}

function getQueueButtonColors(isEnded, isCalledUp) {
    if (isEnded) {
        return { color: 'var(--color-text-muted)', borderColor: 'var(--color-border)' }
    }
    if (isCalledUp) {
        return { color: 'var(--color-accent)', borderColor: 'var(--color-accent)' }
    }
    return { color: 'var(--color-chorus)', borderColor: 'var(--color-chorus)' }
}

function ShoutOutBanner({ announcement, isEnded }) {
    if (!announcement || isEnded) return null

    return (
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
                <span>{announcement}</span>
            </div>
        </div>
    )
}

function ShowStatusBadge({ statusInfo }) {
    if (!statusInfo.isPaused && !statusInfo.isEnded) return null

    const badgeStyle = statusInfo.isPaused
        ? {
            background: 'rgba(245, 158, 11, 0.15)',
            color: 'var(--color-chorus)',
            border: '1px solid rgba(245, 158, 11, 0.4)',
        }
        : {
            background: 'rgba(239, 68, 68, 0.15)',
            color: 'var(--color-danger)',
            border: '1px solid rgba(239, 68, 68, 0.4)',
        }

    return (
        <span style={{
            fontSize: 'var(--text-xs)',
            fontWeight: 'var(--font-bold)',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            padding: '2px 8px',
            borderRadius: 'var(--radius-full)',
            ...badgeStyle,
        }}>
            {statusInfo.badgeLabel}
        </span>
    )
}

function ShowActionButton({ event, statusInfo, isCalledUp, queueButtonLabel, onOpenQueue, onOpenJoinPrompt }) {
    if (!event) {
        return (
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
        )
    }

    const buttonColors = getQueueButtonColors(statusInfo.isEnded, isCalledUp)
    return (
        <button
            type="button"
            onClick={onOpenQueue}
            class="btn btn-secondary btn-sm"
            style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 'var(--space-1)',
                ...buttonColors,
                fontWeight: 'var(--font-bold)',
                padding: '2px 8px',
            }}
        >
            {queueButtonLabel}
        </button>
    )
}

function ViewModeTab({
    label,
    count,
    countOpacity = 0.85,
    isActive,
    activeBackground = 'var(--color-accent)',
    activeShadow = '0 0 10px var(--color-accent-glow)',
    onClick,
}) {
    const activeStyles = isActive
        ? {
            fontWeight: 'var(--font-bold)',
            color: '#fff',
            background: activeBackground,
            boxShadow: activeShadow,
        }
        : {
            fontWeight: 'var(--font-semibold)',
            color: 'var(--color-text-muted)',
            background: 'transparent',
            boxShadow: 'none',
        }

    return (
        <button
            type="button"
            onClick={onClick}
            style={{
                flex: 1,
                padding: 'var(--space-2) var(--space-1)',
                fontSize: 'var(--text-xs)',
                border: 'none',
                borderRadius: 'var(--radius-md)',
                cursor: 'pointer',
                transition: 'all 0.15s ease',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '4px',
                ...activeStyles,
            }}
        >
            <span>{label}</span>
            {count !== null && count !== undefined && (
                <span style={{ opacity: countOpacity, fontSize: '0.85em' }}>({count})</span>
            )}
        </button>
    )
}

function ViewModeSwitcher({ viewMode, setViewMode, totalSongs, uniqueArtistsCount, totalFavoritesCount }) {
    const artistCount = uniqueArtistsCount !== null ? uniqueArtistsCount : null
    const favoritesCount = totalFavoritesCount > 0 ? totalFavoritesCount : null

    return (
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
            <ViewModeTab
                label="🎵 Songs"
                count={totalSongs}
                isActive={viewMode === 'songs'}
                onClick={() => setViewMode('songs')}
            />
            <ViewModeTab
                label="🎤 By Artist"
                count={artistCount}
                isActive={viewMode === 'artists'}
                onClick={() => setViewMode('artists')}
            />
            <ViewModeTab
                label="★ Favorites"
                count={favoritesCount}
                countOpacity={0.9}
                isActive={viewMode === 'favorites'}
                activeBackground="linear-gradient(135deg, #eab308, #ca8a04)"
                activeShadow="0 0 10px rgba(234, 179, 8, 0.4)"
                onClick={() => setViewMode('favorites')}
            />
        </div>
    )
}

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
    const queueButtonLabel = getQueueButtonLabel(statusInfo, myActiveStatus, queueCount)
    const title = event?.name || songbook?.name
    const songsLocationLabel = event && !statusInfo.isEnded ? 'on stage' : 'in catalog'
    const dotShadow = statusInfo.dotGlow ? `0 0 8px ${statusInfo.dotColor}` : 'none'

    return (
        <div class="public-book-pinned" style={{ flexShrink: 0, display: 'flex', flexDirection: 'column' }}>
            {/* Live Shout-Out Announcement */}
            <ShoutOutBanner announcement={event?.announcement} isEnded={statusInfo.isEnded} />

            <div style={{ marginBottom: 'var(--space-3)', flexShrink: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                <div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                        <h2 style={{ margin: 0, fontSize: 'var(--text-2xl)' }}>{title}</h2>
                        <ShowStatusBadge statusInfo={statusInfo} />
                    </div>
                    <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: 'var(--space-1)', display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                        <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusInfo.dotColor, display: 'inline-block', boxShadow: dotShadow }}></span>
                        <span>{totalSongs} songs {songsLocationLabel}</span>
                        {event?.venue_name && <span>• @ {event.venue_name}</span>}
                        <ShowActionButton
                            event={event}
                            statusInfo={statusInfo}
                            isCalledUp={isCalledUp}
                            queueButtonLabel={queueButtonLabel}
                            onOpenQueue={onOpenQueue}
                            onOpenJoinPrompt={onOpenJoinPrompt}
                        />
                    </div>
                </div>
            </div>

            <ShowStatusBanner
                status={event?.status}
                onJoinAnotherShow={onOpenJoinPrompt}
            />

            {/* View Mode Switcher: Songs | By Artist | Favorites */}
            <ViewModeSwitcher
                viewMode={viewMode}
                setViewMode={setViewMode}
                totalSongs={totalSongs}
                uniqueArtistsCount={uniqueArtistsCount}
                totalFavoritesCount={totalFavoritesCount}
            />
        </div>
    )
}
