const HISTORY_STATUS_BADGES = {
    completed: {
        label: '✓ Completed',
        background: 'rgba(34, 197, 94, 0.15)',
        color: '#86efac',
        border: '1px solid rgba(34, 197, 94, 0.3)',
    },
    rejected: {
        label: '✗ Declined',
        background: 'rgba(239, 68, 68, 0.15)',
        color: '#fca5a5',
        border: '1px solid rgba(239, 68, 68, 0.3)',
    },
}

const DEFAULT_HISTORY_BADGE = {
    label: 'Cancelled',
    background: 'var(--color-surface-alt)',
    color: 'var(--color-text-dim)',
    border: '1px solid var(--color-border)',
}

const POSITION_BADGES = {
    0: {
        label: '🎤 On Stage',
        background: 'var(--color-accent)',
        color: '#fff',
    },
    1: {
        label: '👀 On Deck',
        background: 'rgba(245, 158, 11, 0.18)',
        color: 'var(--color-chorus)',
        border: '1px solid rgba(245, 158, 11, 0.35)',
    },
    2: {
        label: '⏳ In the Hole',
        background: 'rgba(168, 85, 247, 0.15)',
        color: '#c084fc',
        border: '1px solid rgba(168, 85, 247, 0.35)',
    },
}

const TURN_SUFFIXES = {
    2: 'nd',
    3: 'rd',
}

export function getCardStyle(isUpNext, tab) {
    if (isUpNext) {
        return {
            background: 'rgba(255, 42, 95, 0.08)',
            border: '1px solid var(--color-accent)',
            boxShadow: '0 0 20px rgba(255, 42, 95, 0.2)',
        }
    }
    if (tab === 'staging') {
        return {
            background: 'rgba(168, 85, 247, 0.06)',
            border: '1px solid rgba(168, 85, 247, 0.3)',
            boxShadow: 'none',
        }
    }
    return {
        background: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        boxShadow: 'none',
    }
}

export function getSingerWeight(req, currentEvent) {
    if (currentEvent?.rotation_mode !== 'fair_priority') {
        return 0
    }
    const normalizedSinger = req.singer_name?.trim()?.toLowerCase() || ''
    return currentEvent?.singer_weights?.[normalizedSinger] || 0
}

export function getMoveBounds(req, index, group, activeRequests) {
    if (group) {
        const groupIndex = group.requests.findIndex(r => r.id === req.id)
        return {
            canMoveUp: groupIndex > 0,
            canMoveDown: groupIndex < group.requests.length - 1,
        }
    }
    return {
        canMoveUp: index > 0,
        canMoveDown: index < activeRequests.length - 1,
    }
}

function HistoryStatusBadge({ status }) {
    const badge = HISTORY_STATUS_BADGES[status] || DEFAULT_HISTORY_BADGE
    return (
        <span style={{
            fontSize: '0.75rem',
            fontWeight: 'var(--font-bold)',
            background: badge.background,
            color: badge.color,
            border: badge.border,
            padding: '2px 8px',
            borderRadius: 'var(--radius-sm)',
        }}>
            {badge.label}
        </span>
    )
}

function QueuePositionBadge({ isUpNext, tab, req, activeRequests }) {
    if (isUpNext) {
        return (
            <span style={{
                fontSize: '0.7rem',
                fontWeight: 'var(--font-bold)',
                background: 'var(--color-accent)',
                color: '#fff',
                padding: '2px 8px',
                borderRadius: 'var(--radius-sm)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
            }}>
                🎤 Called to Stage
            </span>
        )
    }

    if (tab !== 'active') {
        return null
    }

    const badge = POSITION_BADGES[activeRequests.indexOf(req)]
    if (!badge) {
        return null
    }

    return (
        <span style={{
            fontSize: '0.7rem',
            fontWeight: 'var(--font-bold)',
            background: badge.background,
            color: badge.color,
            border: badge.border,
            padding: '2px 8px',
            borderRadius: 'var(--radius-sm)',
            textTransform: 'uppercase',
            letterSpacing: '0.05em',
        }}>
            {badge.label}
        </span>
    )
}

function WriteInBadges({ req }) {
    const query = encodeURIComponent(`${req.song_artist || ''} ${req.song_title || ''} karaoke`.trim())
    return (
        <>
            <span style={{
                fontSize: '0.75rem',
                fontWeight: 'var(--font-bold)',
                background: 'rgba(236, 72, 153, 0.15)',
                color: '#f472b6',
                border: '1px solid rgba(236, 72, 153, 0.35)',
                padding: '2px 8px',
                borderRadius: 'var(--radius-sm)',
            }}>
                ✍️ Write-In
            </span>
            <a
                href={`https://www.youtube.com/results?search_query=${query}`}
                target="_blank"
                rel="noopener noreferrer"
                style={{
                    fontSize: '0.75rem',
                    fontWeight: 'var(--font-semibold)',
                    background: 'rgba(239, 68, 68, 0.12)',
                    color: '#fca5a5',
                    border: '1px solid rgba(239, 68, 68, 0.3)',
                    padding: '2px 8px',
                    borderRadius: 'var(--radius-sm)',
                    textDecoration: 'none',
                }}
                title="Search YouTube for this write-in karaoke track"
            >
                ▶ YouTube
            </a>
        </>
    )
}

function KeyChangeBadge({ keyChange }) {
    const label = keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`
    return (
        <span style={{
            fontSize: '0.75rem',
            fontWeight: 'var(--font-bold)',
            background: 'rgba(245, 158, 11, 0.15)',
            color: 'var(--color-chorus)',
            border: '1px solid rgba(245, 158, 11, 0.3)',
            padding: '2px 8px',
            borderRadius: 'var(--radius-sm)',
        }}>
            Key: {label}
        </span>
    )
}

function SingerTurnBadge({ tab, req, singerPerformanceCounts }) {
    if (tab === 'history' && (req.status === 'rejected' || req.status === 'cancelled')) {
        return null
    }
    const pastSongsCount = singerPerformanceCounts[req.singer_name?.trim()?.toLowerCase()] || 0
    const turn = req.singer_turn || (pastSongsCount + 1)

    if (turn <= 1) {
        return (
            <span style={{
                fontSize: '0.75rem',
                fontWeight: 'var(--font-bold)',
                background: 'rgba(34, 197, 94, 0.12)',
                color: '#86efac',
                border: '1px solid rgba(34, 197, 94, 0.25)',
                padding: '2px 8px',
                borderRadius: 'var(--radius-sm)',
            }}>
                ✨ 1st song
            </span>
        )
    }

    const suffix = TURN_SUFFIXES[turn] || 'th'
    return (
        <span style={{
            fontSize: '0.75rem',
            fontWeight: 'var(--font-bold)',
            background: 'rgba(147, 51, 234, 0.15)',
            color: '#c084fc',
            border: '1px solid rgba(147, 51, 234, 0.3)',
            padding: '2px 8px',
            borderRadius: 'var(--radius-sm)',
        }}>
            🎤 {turn}{suffix} song
        </span>
    )
}

function SingerWeightBadge({ singerWeight, singerName, onOpenWeightsModal }) {
    const isPositive = singerWeight > 0
    return (
        <button
            type="button"
            onClick={() => onOpenWeightsModal?.(singerName)}
            style={{
                fontSize: '0.75rem',
                fontWeight: 'var(--font-bold)',
                background: isPositive ? 'rgba(16, 185, 129, 0.14)' : 'rgba(239, 68, 68, 0.14)',
                color: isPositive ? 'var(--color-success)' : 'var(--color-danger)',
                border: `1px solid ${isPositive ? 'rgba(16, 185, 129, 0.35)' : 'rgba(239, 68, 68, 0.35)'}`,
                padding: '2px 8px',
                borderRadius: 'var(--radius-sm)',
                cursor: 'pointer',
            }}
            title="Private host weight (never shown to singers). Click to adjust."
        >
            {isPositive ? `⭐ VIP +${singerWeight}` : `🐢 ${singerWeight}`}
        </button>
    )
}

export function RequestInfo({
    req,
    tab,
    isUpNext,
    activeRequests,
    singerPerformanceCounts,
    isFairPriority,
    singerWeight,
    onOpenWeightsModal,
}) {
    const declineReason = tab === 'history' && req.status === 'rejected'
        ? (req.decline_reason || req.rejection_reason)
        : null

    return (
        <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                <span style={{
                    fontSize: 'var(--text-lg)',
                    fontWeight: 'var(--font-bold)',
                    color: 'var(--color-text)',
                }}>
                    {req.singer_name}
                </span>

                {tab === 'history' && <HistoryStatusBadge status={req.status} />}

                <QueuePositionBadge
                    isUpNext={isUpNext}
                    tab={tab}
                    req={req}
                    activeRequests={activeRequests}
                />

                {req.is_write_in && <WriteInBadges req={req} />}

                {req.key_change !== 0 && <KeyChangeBadge keyChange={req.key_change} />}

                <SingerTurnBadge
                    tab={tab}
                    req={req}
                    singerPerformanceCounts={singerPerformanceCounts}
                />

                {isFairPriority && singerWeight !== 0 && (
                    <SingerWeightBadge
                        singerWeight={singerWeight}
                        singerName={req.singer_name}
                        onOpenWeightsModal={onOpenWeightsModal}
                    />
                )}
            </div>

            <div style={{
                fontSize: 'var(--text-base)',
                color: 'var(--color-text)',
                marginTop: '2px',
                fontWeight: 'var(--font-medium)',
            }}>
                {req.song_title} {req.song_artist ? <span style={{ color: 'var(--color-text-muted)', fontWeight: 'normal' }}>— {req.song_artist}</span> : ''}
            </div>

            {req.notes && (
                <div style={{
                    fontSize: 'var(--text-xs)',
                    color: 'var(--color-text-dim)',
                    marginTop: '4px',
                    background: 'var(--color-surface-alt)',
                    padding: '2px 8px',
                    borderRadius: 'var(--radius-sm)',
                    display: 'inline-block',
                }}>
                    💬 "{req.notes}"
                </div>
            )}

            {declineReason && (
                <div style={{
                    fontSize: 'var(--text-xs)',
                    color: '#fca5a5',
                    marginTop: '4px',
                    background: 'rgba(239, 68, 68, 0.1)',
                    border: '1px solid rgba(239, 68, 68, 0.25)',
                    padding: '2px 8px',
                    borderRadius: 'var(--radius-sm)',
                    display: 'inline-block',
                }}>
                    Declined: {declineReason}
                </div>
            )}
        </div>
    )
}
