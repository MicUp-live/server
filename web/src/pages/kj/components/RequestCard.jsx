import { ActionPopover } from '../../../components/ui/ActionPopover.jsx'
import {
    RequestInfo,
    getCardStyle,
    getMoveBounds,
    getSingerWeight,
} from './RequestCardParts.jsx'

function formatWeightLabel(singerWeight) {
    if (singerWeight === 0) {
        return 'Weight'
    }
    return singerWeight > 0 ? `+${singerWeight}` : `${singerWeight}`
}

function ReorderControls({ index, group, isUpNext, canMoveUp, canMoveDown, onMoveRequest }) {
    return (
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
    )
}

function StagingIcon() {
    return (
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
    )
}

function StagingActions({ req, onUpdateStatus }) {
    return (
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
    )
}

function ActiveActions({ req, isUpNext, onUpdateStatus, onDeferRequest }) {
    return (
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

            {onDeferRequest && (
                <button
                    type="button"
                    onClick={() => onDeferRequest(req)}
                    class="btn btn-secondary btn-sm kj-desktop-only"
                    title="Absent singer: bump down 3 spots without marking sung"
                >
                    ⏭ Defer
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
    )
}

function RequestActionMenu({
    req,
    tab,
    isFairPriority,
    singerWeight,
    copiedId,
    onCopySongInfo,
    onEditRequest,
    onOpenWeightsModal,
    onUpdateStatus,
    onDeferRequest,
    onDeleteRequest,
}) {
    const isCopied = copiedId === req.id
    const weightText = singerWeight > 0 ? `+${singerWeight}` : singerWeight

    return (
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
                        {tab === 'active' && onDeferRequest && (
                            <button
                                type="button"
                                class="kj-action-item"
                                onClick={() => { close(); onDeferRequest(req) }}
                            >
                                <span>⏭</span>
                                <span>Defer Absent Singer (Down 3)</span>
                            </button>
                        )}
                        <button
                            type="button"
                            class="kj-action-item"
                            onClick={() => { onCopySongInfo(req); close() }}
                        >
                            <span>{isCopied ? '✓' : '📋'}</span>
                            <span>{isCopied ? 'Copied Song Info' : 'Copy Song Info'}</span>
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
                                <span>Private Singer Weight ({weightText})</span>
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
    )
}

function RequestActions({
    req,
    tab,
    isUpNext,
    isFairPriority,
    singerWeight,
    copiedId,
    onCopySongInfo,
    onEditRequest,
    onOpenWeightsModal,
    onUpdateStatus,
    onDeferRequest,
    onDeleteRequest,
}) {
    const isCopied = copiedId === req.id

    return (
        <div class="kj-request-actions">
            <button
                type="button"
                onClick={() => onCopySongInfo(req)}
                class="btn btn-secondary btn-sm kj-desktop-only"
                title="Copy song for your karaoke player"
                style={{ alignItems: 'center', gap: '4px' }}
            >
                <span>{isCopied ? '✓' : '📋'}</span>
                <span>{isCopied ? 'Copied' : 'Copy'}</span>
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
                    <span>{formatWeightLabel(singerWeight)}</span>
                </button>
            )}

            {tab === 'staging' && <StagingActions req={req} onUpdateStatus={onUpdateStatus} />}

            {tab === 'active' && (
                <ActiveActions
                    req={req}
                    isUpNext={isUpNext}
                    onUpdateStatus={onUpdateStatus}
                    onDeferRequest={onDeferRequest}
                />
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

            <RequestActionMenu
                req={req}
                tab={tab}
                isFairPriority={isFairPriority}
                singerWeight={singerWeight}
                copiedId={copiedId}
                onCopySongInfo={onCopySongInfo}
                onEditRequest={onEditRequest}
                onOpenWeightsModal={onOpenWeightsModal}
                onUpdateStatus={onUpdateStatus}
                onDeferRequest={onDeferRequest}
                onDeleteRequest={onDeleteRequest}
            />
        </div>
    )
}

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
    onDeferRequest,
    onDeleteRequest,
    onCopySongInfo,
    onEditRequest,
    onOpenWeightsModal,
}) {
    const isUpNext = req.status === 'up_next'
    const isFairPriority = currentEvent?.rotation_mode === 'fair_priority'
    const singerWeight = getSingerWeight(req, currentEvent)
    const { canMoveUp, canMoveDown } = getMoveBounds(req, index, group, activeRequests)

    return (
        <div
            key={req.id}
            class="kj-request-card"
            style={getCardStyle(isUpNext, tab)}
        >
            <div class="kj-request-main">
                {tab === 'active' && (
                    <ReorderControls
                        index={index}
                        group={group}
                        isUpNext={isUpNext}
                        canMoveUp={canMoveUp}
                        canMoveDown={canMoveDown}
                        onMoveRequest={onMoveRequest}
                    />
                )}

                {tab === 'staging' && <StagingIcon />}

                <RequestInfo
                    req={req}
                    tab={tab}
                    isUpNext={isUpNext}
                    activeRequests={activeRequests}
                    singerPerformanceCounts={singerPerformanceCounts}
                    isFairPriority={isFairPriority}
                    singerWeight={singerWeight}
                    onOpenWeightsModal={onOpenWeightsModal}
                />
            </div>

            {/* Actions */}
            <RequestActions
                req={req}
                tab={tab}
                isUpNext={isUpNext}
                isFairPriority={isFairPriority}
                singerWeight={singerWeight}
                copiedId={copiedId}
                onCopySongInfo={onCopySongInfo}
                onEditRequest={onEditRequest}
                onOpenWeightsModal={onOpenWeightsModal}
                onUpdateStatus={onUpdateStatus}
                onDeferRequest={onDeferRequest}
                onDeleteRequest={onDeleteRequest}
            />
        </div>
    )
}
