import { formatWaitEstimate } from '../../../utils/waitTime.js'

function getRotationLabel(isNext, queueIndex) {
    if (isNext) return '🎤 CALLED TO STAGE'
    if (queueIndex === 0) return '#1 in line • Up next'
    if (queueIndex > 0) return `#${queueIndex + 1} in line • ${formatWaitEstimate(queueIndex)} wait`
    return '🎤 In Rotation'
}

function formatKeyChange(keyChange) {
    if (keyChange === 0) return null
    return keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`
}

function canReplaceRequest(req, isEnded, allowSongReplacement, onReplaceSong) {
    if (isEnded || !allowSongReplacement || !onReplaceSong || !req.edit_token) return false
    return req.status === 'pending' || req.status === 'accepted'
}

const BADGE_BASE_STYLE = {
    fontSize: 'var(--text-xs)',
    fontWeight: 'var(--font-bold)',
    padding: '2px 8px',
    borderRadius: 'var(--radius-sm)',
}

function RequestStatusBadge({ isEnded, isNext, status, queueIndex }) {
    if (isEnded) {
        return (
            <span style={{
                ...BADGE_BASE_STYLE,
                background: 'rgba(239, 68, 68, 0.12)',
                color: 'var(--color-danger)',
                border: '1px solid rgba(239, 68, 68, 0.3)',
            }}>
                Show Ended
            </span>
        )
    }

    if (status === 'pending') {
        return (
            <span style={{
                ...BADGE_BASE_STYLE,
                background: 'rgba(147, 51, 234, 0.15)',
                color: '#c084fc',
                border: '1px solid rgba(147, 51, 234, 0.3)',
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
            }}>
                <span>📥</span> In Review by Host
            </span>
        )
    }

    return (
        <span style={{
            ...BADGE_BASE_STYLE,
            background: isNext ? 'var(--color-accent)' : 'rgba(245, 158, 11, 0.15)',
            color: isNext ? '#fff' : 'var(--color-chorus)',
            border: `1px solid ${isNext ? 'var(--color-accent)' : 'rgba(245, 158, 11, 0.3)'}`
        }}>
            {getRotationLabel(isNext, queueIndex)}
        </span>
    )
}

function SingerActiveRequestRow({
    req,
    queueIndex,
    isEnded,
    allowSongReplacement,
    onReplaceSong,
    onCancelRequest,
}) {
    const isNext = !isEnded && req.status === 'up_next'
    const keyChangeLabel = formatKeyChange(req.key_change)
    const showSwap = canReplaceRequest(req, isEnded, allowSongReplacement, onReplaceSong)
    const showCancel = !isEnded && req.status === 'pending'

    return (
        <div
            style={{
                background: isNext ? 'rgba(255, 42, 95, 0.15)' : 'var(--color-surface-alt)',
                border: `1px solid ${isNext ? 'var(--color-accent)' : 'var(--color-border)'}`,
                borderRadius: 'var(--radius-lg)',
                padding: 'var(--space-3) var(--space-4)',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
            }}
        >
            <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 'var(--font-bold)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {req.song_title}
                </div>
                <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-xs)', marginTop: '2px' }}>
                    {req.song_artist}
                    {keyChangeLabel && (
                        <span style={{ marginLeft: 'var(--space-2)', color: 'var(--color-chorus)', fontWeight: 'bold' }}>
                            {keyChangeLabel}
                        </span>
                    )}
                </div>
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)', flexShrink: 0 }}>
                <RequestStatusBadge
                    isEnded={isEnded}
                    isNext={isNext}
                    status={req.status}
                    queueIndex={queueIndex}
                />

                {showSwap && (
                    <button
                        type="button"
                        onClick={() => onReplaceSong(req)}
                        class="btn btn-secondary btn-sm"
                        title="Change your song while keeping your spot in line"
                    >
                        🔄 Swap
                    </button>
                )}

                {showCancel && (
                    <button
                        type="button"
                        onClick={() => onCancelRequest(req.id)}
                        title="Cancel request"
                        style={{
                            background: 'none',
                            border: 'none',
                            color: 'var(--color-text-dim)',
                            cursor: 'pointer',
                            padding: '2px',
                            fontSize: 'var(--text-sm)'
                        }}
                    >
                        ✕
                    </button>
                )}
            </div>
        </div>
    )
}

export function SingerActiveRequests({
    activeRequests,
    queue,
    currentRound,
    eventStatus,
    allowSongReplacement = false,
    onReplaceSong,
    onCancelRequest,
}) {
    if (!activeRequests || activeRequests.length === 0) return null
    const isEnded = eventStatus === 'ended'

    return (
        <div style={{ marginBottom: 'var(--space-6)' }}>
            <div style={{
                fontSize: 'var(--text-xs)',
                fontWeight: 'var(--font-bold)',
                color: isEnded ? 'var(--color-text-muted)' : 'var(--color-accent)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                marginBottom: 'var(--space-2)'
            }}>
                {isEnded ? `Unplayed Requests (${activeRequests.length})` : `Your Active Requests (${activeRequests.length})`}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                {activeRequests.map((req) => (
                    <SingerActiveRequestRow
                        key={req.id}
                        req={req}
                        queueIndex={queue.findIndex(q => q.id === req.id)}
                        isEnded={isEnded}
                        allowSongReplacement={allowSongReplacement}
                        onReplaceSong={onReplaceSong}
                        onCancelRequest={onCancelRequest}
                    />
                ))}
            </div>
        </div>
    )
}
