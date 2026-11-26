import { ActionPopover } from '../../../components/ui/ActionPopover.jsx'

export function RequestCard({
    req,
    index,
    group = null,
    tab = 'active',
    activeRequests = [],
    singerPerformanceCounts = {},
    currentEvent,
    currentRound = 1,
    copiedId = null,
    onMoveRequest,
    onUpdateRequestRound,
    onUpdateStatus,
    onDeleteRequest,
    onCopySongInfo,
    onEditRequest,
    onOpenWeightsModal,
}) {
    const isUpNext = req.status === 'up_next'
    const isFairPriority = currentEvent?.rotation_mode === 'fair_priority'
    const normalizedSinger = req.singer_name?.trim()?.toLowerCase() || ''
    const singerWeight = isFairPriority ? (currentEvent?.singer_weights?.[normalizedSinger] || 0) : 0
    const canMoveUp = group
        ? (group.requests.findIndex(r => r.id === req.id) > 0)
        : (index > 0)
    const canMoveDown = group
        ? (group.requests.findIndex(r => r.id === req.id) < group.requests.length - 1)
        : (index < activeRequests.length - 1)

    return (
        <div
            key={req.id}
            class="kj-request-card"
            style={{
                background: isUpNext ? 'rgba(255, 42, 95, 0.08)' : (tab === 'staging' ? 'rgba(168, 85, 247, 0.06)' : 'var(--color-surface)'),
                border: `1px solid ${isUpNext ? 'var(--color-accent)' : (tab === 'staging' ? 'rgba(168, 85, 247, 0.3)' : 'var(--color-border)')}`,
                boxShadow: isUpNext ? '0 0 20px rgba(255, 42, 95, 0.2)' : 'none',
            }}
        >
            <div class="kj-request-main">
                {tab === 'active' && (
                    <div class="kj-request-reorder">
                        <button
                            type="button"
                            onClick={() => onMoveRequest(index, -1, group)}
                            disabled={!canMoveUp}
                            class="kj-request-reorder-btn"
                            style={{
                                color: !canMoveUp ? 'var(--color-border)' : 'var(--color-text-dim)',
                                cursor: !canMoveUp ? 'default' : 'pointer',
                            }}
                            title="Bump Up"
                            aria-label="Move request up"
                        >
                            ▲
                        </button>
                        <span style={{
                            fontSize: 'var(--text-sm)',
                            fontWeight: 'var(--font-bold)',
                            color: isUpNext ? 'var(--color-accent)' : 'var(--color-chorus)',
                        }}>
                            #{index + 1}
                        </span>
                        <button
                            type="button"
                            onClick={() => onMoveRequest(index, 1, group)}
                            disabled={!canMoveDown}
                            class="kj-request-reorder-btn"
                            style={{
                                color: !canMoveDown ? 'var(--color-border)' : 'var(--color-text-dim)',
                                cursor: !canMoveDown ? 'default' : 'pointer',
                            }}
                            title="Move Down"
                            aria-label="Move request down"
                        >
                            ▼
                        </button>
                    </div>
                )}

                {tab === 'staging' && (
                    <div style={{
                        width: '32px',
                        height: '32px',
                        borderRadius: '50%',
                        background: 'rgba(168, 85, 247, 0.2)',
                        color: '#c084fc',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        fontSize: '1rem',
                        flexShrink: 0,
                    }}>
                        📥
                    </div>
                )}

                <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                        <span style={{
                            fontSize: 'var(--text-lg)',
                            fontWeight: 'var(--font-bold)',
                            color: 'var(--color-text)',
                        }}>
                            {req.singer_name}
                        </span>

                        {tab === 'history' && (
                            req.status === 'completed' ? (
                                <span style={{
                                    fontSize: '0.75rem',
                                    fontWeight: 'var(--font-bold)',
                                    background: 'rgba(34, 197, 94, 0.15)',
                                    color: '#86efac',
                                    border: '1px solid rgba(34, 197, 94, 0.3)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                }}>
                                    ✓ Completed
                                </span>
                            ) : req.status === 'rejected' ? (
                                <span style={{
                                    fontSize: '0.75rem',
                                    fontWeight: 'var(--font-bold)',
                                    background: 'rgba(239, 68, 68, 0.15)',
                                    color: '#fca5a5',
                                    border: '1px solid rgba(239, 68, 68, 0.3)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                }}>
                                    ✗ Declined
                                </span>
                            ) : (
                                <span style={{
                                    fontSize: '0.75rem',
                                    fontWeight: 'var(--font-bold)',
                                    background: 'var(--color-surface-alt)',
                                    color: 'var(--color-text-dim)',
                                    border: '1px solid var(--color-border)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                }}>
                                    Cancelled
                                </span>
                            )
                        )}

                        {isUpNext ? (
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
                        ) : tab === 'active' && activeRequests.indexOf(req) === 0 ? (
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
                                🎤 On Stage
                            </span>
                        ) : tab === 'active' && activeRequests.indexOf(req) === 1 ? (
                            <span style={{
                                fontSize: '0.7rem',
                                fontWeight: 'var(--font-bold)',
                                background: 'rgba(245, 158, 11, 0.18)',
                                color: 'var(--color-chorus)',
                                border: '1px solid rgba(245, 158, 11, 0.35)',
                                padding: '2px 8px',
                                borderRadius: 'var(--radius-sm)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.05em',
                            }}>
                                👀 On Deck
                            </span>
                        ) : tab === 'active' && activeRequests.indexOf(req) === 2 ? (
                            <span style={{
                                fontSize: '0.7rem',
                                fontWeight: 'var(--font-bold)',
                                background: 'rgba(168, 85, 247, 0.15)',
                                color: '#c084fc',
                                border: '1px solid rgba(168, 85, 247, 0.35)',
                                padding: '2px 8px',
                                borderRadius: 'var(--radius-sm)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.05em',
                            }}>
                                ⏳ In the Hole
                            </span>
                        ) : null}

                        {req.is_write_in && (
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
                                    href={`https://www.youtube.com/results?search_query=${encodeURIComponent(`${req.song_artist || ''} ${req.song_title || ''} karaoke`.trim())}`}
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
                        )}

                        {req.key_change !== 0 && (
                            <span style={{
                                fontSize: '0.75rem',
                                fontWeight: 'var(--font-bold)',
                                background: 'rgba(245, 158, 11, 0.15)',
                                color: 'var(--color-chorus)',
                                border: '1px solid rgba(245, 158, 11, 0.3)',
                                padding: '2px 8px',
                                borderRadius: 'var(--radius-sm)',
                            }}>
                                Key: {req.key_change > 0 ? `+${req.key_change}♯` : `${req.key_change}♭`}
                            </span>
                        )}

                        {(() => {
                            if (tab === 'history' && (req.status === 'rejected' || req.status === 'cancelled')) {
                                return null
                            }
                            const pastSongsCount = singerPerformanceCounts[req.singer_name?.trim()?.toLowerCase()] || 0
                            const turn = req.singer_turn || (pastSongsCount + 1)
                            return turn > 1 ? (
                                <span style={{
                                    fontSize: '0.75rem',
                                    fontWeight: 'var(--font-bold)',
                                    background: 'rgba(147, 51, 234, 0.15)',
                                    color: '#c084fc',
                                    border: '1px solid rgba(147, 51, 234, 0.3)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                }}>
                                    🎤 {turn}{turn === 2 ? 'nd' : turn === 3 ? 'rd' : 'th'} song
                                </span>
                            ) : (
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
                        })()}

                        {isFairPriority && singerWeight !== 0 && (
                            <button
                                type="button"
                                onClick={() => onOpenWeightsModal?.(req.singer_name)}
                                style={{
                                    fontSize: '0.75rem',
                                    fontWeight: 'var(--font-bold)',
                                    background: singerWeight > 0 ? 'rgba(16, 185, 129, 0.14)' : 'rgba(239, 68, 68, 0.14)',
                                    color: singerWeight > 0 ? 'var(--color-success)' : 'var(--color-danger)',
                                    border: `1px solid ${singerWeight > 0 ? 'rgba(16, 185, 129, 0.35)' : 'rgba(239, 68, 68, 0.35)'}`,
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                    cursor: 'pointer',
                                }}
                                title="Private host weight (never shown to singers). Click to adjust."
                            >
                                {singerWeight > 0 ? `⭐ VIP +${singerWeight}` : `🐢 ${singerWeight}`}
                            </button>
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

                    {tab === 'history' && req.status === 'rejected' && (req.decline_reason || req.rejection_reason) && (
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
                            Declined: {req.decline_reason || req.rejection_reason}
                        </div>
                    )}
                </div>
            </div>

            {/* Actions */}
            <div class="kj-request-actions">
                <button
                    type="button"
                    onClick={() => onCopySongInfo(req)}
                    class="btn btn-secondary btn-sm kj-desktop-only"
                    title="Copy song for your karaoke player"
                    style={{ alignItems: 'center', gap: '4px' }}
                >
                    <span>{copiedId === req.id ? '✓' : '📋'}</span>
                    <span>{copiedId === req.id ? 'Copied' : 'Copy'}</span>
                </button>

                <button
                    type="button"
                    onClick={() => onEditRequest?.(req)}
                    class="btn btn-secondary btn-sm kj-desktop-only"
                    title="Edit request details"
                    style={{ alignItems: 'center', gap: '4px' }}
                >
                    <span>✏️</span>
                    <span>Edit</span>
                </button>

                {isFairPriority && onOpenWeightsModal && (
                    <button
                        type="button"
                        onClick={() => onOpenWeightsModal(req.singer_name)}
                        class="btn btn-secondary btn-sm kj-desktop-only"
                        title="Adjust private priority weight for this singer"
                        style={{ alignItems: 'center', gap: '4px' }}
                    >
                        <span>⚖️</span>
                        <span>{singerWeight !== 0 ? (singerWeight > 0 ? `+${singerWeight}` : `${singerWeight}`) : 'Weight'}</span>
                    </button>
                )}

                {/* Staging Actions: Accept to Rotation, Place Up Next, Decline */}
                {tab === 'staging' && (
                    <>
                        <button
                            type="button"
                            onClick={() => onUpdateStatus(req, 'accepted')}
                            class="btn btn-primary btn-sm"
                            style={{ background: 'var(--color-success)', borderColor: 'var(--color-success)' }}
                            title="Accept request into active rotation"
                        >
                            ✓ Accept
                        </button>
                        <button
                            type="button"
                            onClick={() => onUpdateStatus(req, 'up_next')}
                            class="btn btn-primary btn-sm kj-desktop-only"
                            style={{ background: 'var(--color-accent)', borderColor: 'var(--color-accent)' }}
                            title="Call directly to the stage"
                        >
                            ⬆ Call to Stage
                        </button>
                        <button
                            type="button"
                            onClick={() => onUpdateStatus(req, 'rejected')}
                            class="btn btn-secondary btn-sm"
                            style={{ color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.3)' }}
                            title="Decline request"
                        >
                            ✗ Decline
                        </button>
                    </>
                )}

                {tab === 'active' && (
                    <>
                        {!isUpNext ? (
                            <button
                                type="button"
                                onClick={() => onUpdateStatus(req, 'up_next')}
                                class="btn btn-primary btn-sm"
                                style={{
                                    background: 'var(--color-accent)',
                                    borderColor: 'var(--color-accent)',
                                }}
                                title="Call singer to stage"
                            >
                                🎤 Call to Stage
                            </button>
                        ) : (
                            <button
                                type="button"
                                onClick={() => onUpdateStatus(req, 'accepted')}
                                class="btn btn-secondary btn-sm"
                                title="Return singer to waiting rotation"
                            >
                                Standby / Return
                            </button>
                        )}

                        <button
                            type="button"
                            onClick={() => onUpdateStatus(req, 'completed')}
                            class="btn btn-secondary btn-sm"
                            style={{ color: 'var(--color-success)', borderColor: 'rgba(16, 185, 129, 0.3)' }}
                            title="Mark as sung"
                        >
                            ✓ Done
                        </button>
                    </>
                )}

                {tab === 'history' && (
                    <button
                        type="button"
                        onClick={() => onUpdateStatus(req, 'accepted')}
                        class="btn btn-secondary btn-sm"
                    >
                        ↩ Restore
                    </button>
                )}

                <button
                    type="button"
                    onClick={() => onDeleteRequest(req)}
                    class="btn btn-secondary btn-sm kj-desktop-only"
                    style={{ color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.3)' }}
                    title="Delete request"
                >
                    ✕
                </button>

                <div class="kj-mobile-only">
                    <ActionPopover
                        label={`More actions for ${req.singer_name}`}
                        panelTitle={req.singer_name}
                    >
                        {(close) => (
                            <>
                                {tab === 'staging' && (
                                    <button
                                        type="button"
                                        class="kj-action-item kj-action-item-accent"
                                        onClick={() => { close(); onUpdateStatus(req, 'up_next') }}
                                    >
                                        <span>⬆</span>
                                        <span>Call Directly to Stage</span>
                                    </button>
                                )}
                                <button
                                    type="button"
                                    class="kj-action-item"
                                    onClick={() => { onCopySongInfo(req); close() }}
                                >
                                    <span>{copiedId === req.id ? '✓' : '📋'}</span>
                                    <span>{copiedId === req.id ? 'Copied Song Info' : 'Copy Song Info'}</span>
                                </button>
                                <button
                                    type="button"
                                    class="kj-action-item"
                                    onClick={() => { close(); onEditRequest?.(req) }}
                                >
                                    <span>✏️</span>
                                    <span>Edit Request Details</span>
                                </button>
                                {isFairPriority && onOpenWeightsModal && (
                                    <button
                                        type="button"
                                        class="kj-action-item"
                                        onClick={() => { close(); onOpenWeightsModal(req.singer_name) }}
                                    >
                                        <span>⚖️</span>
                                        <span>Private Singer Weight ({singerWeight > 0 ? `+${singerWeight}` : singerWeight})</span>
                                    </button>
                                )}
                                <hr class="kj-action-divider" />
                                <button
                                    type="button"
                                    class="kj-action-item kj-action-item-danger"
                                    onClick={() => { close(); onDeleteRequest(req) }}
                                >
                                    <span>🗑️</span>
                                    <span>Delete Request</span>
                                </button>
                            </>
                        )}
                    </ActionPopover>
                </div>
            </div>
        </div>
    )
}
