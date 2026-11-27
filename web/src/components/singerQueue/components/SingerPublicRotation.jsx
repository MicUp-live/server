import { Spinner } from '../../Spinner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

const ROW_THEMES = {
    stage: {
        rowBackground: 'rgba(255, 42, 95, 0.08)',
        rowBorder: '1px solid var(--color-accent)',
        numberBackground: 'var(--color-accent)',
        numberColor: '#ffffff',
        nameColor: 'var(--color-accent)',
    },
    deck: {
        rowBackground: 'rgba(245, 158, 11, 0.08)',
        rowBorder: '1px solid var(--color-chorus)',
        numberBackground: 'var(--color-chorus)',
        numberColor: '#0b0b10',
        nameColor: 'var(--color-chorus)',
    },
    default: {
        rowBackground: 'var(--color-surface-alt)',
        rowBorder: '1px solid var(--color-border)',
        numberBackground: 'var(--color-surface)',
        numberColor: 'var(--color-text-muted)',
        nameColor: 'var(--color-text)',
    },
}

function getEmptyStateEmoji(statusInfo) {
    if (statusInfo.isPaused) return '⏸️'
    if (statusInfo.isEnded) return '🏁'
    return '🎤'
}

function getOnDeckIndex(queue, isEnded) {
    if (isEnded) return -1
    const hasUpNext = queue.some(q => q.status === 'up_next')
    if (!hasUpNext) return 0
    return queue.findIndex(q => q.status !== 'up_next')
}

function getRowRole(isOnStage, isOnDeck) {
    if (isOnStage) return 'stage'
    if (isOnDeck) return 'deck'
    return 'default'
}

function RotationHeader({ isEnded, queueLength }) {
    const title = isEnded
        ? `Final Rotation (${queueLength} remaining)`
        : `Full Room Rotation (${queueLength} in line)`

    return (
        <div style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            marginBottom: 'var(--space-3)',
        }}>
            <div style={{
                fontSize: 'var(--text-xs)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-text-muted)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
            }}>
                {title}
            </div>
            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                {isEnded ? 'Show ended' : 'Auto-updates live'}
            </span>
        </div>
    )
}

function NoEventState({ joinCodeInput, onJoinCodeInput, onJoin, joining }) {
    return (
        <div style={{
            textAlign: 'center',
            padding: 'var(--space-8) var(--space-4)',
            color: 'var(--color-text-dim)',
            background: 'var(--color-surface-alt)',
            borderRadius: 'var(--radius-lg)',
            border: '1px dashed var(--color-border)',
        }}>
            <div style={{ fontSize: '2.5rem', marginBottom: 'var(--space-2)' }}>🎪</div>
            <div style={{ fontWeight: 'var(--font-bold)', color: '#fff', fontSize: 'var(--text-base)' }}>
                A Queue Requires an Event
            </div>
            <p style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', margin: 'var(--space-2) auto var(--space-4)', maxWidth: '300px' }}>
                No live show is currently active for this songbook. Singer queues only run during an active event session.
            </p>
            <form onSubmit={onJoin} style={{ display: 'flex', gap: 'var(--space-2)', maxWidth: '280px', margin: '0 auto' }}>
                <input
                    type="text"
                    placeholder="SHOW CODE"
                    value={joinCodeInput}
                    onInput={e => onJoinCodeInput(e.target.value.toUpperCase())}
                    maxLength={16}
                    required
                    style={{ textAlign: 'center', fontWeight: 'bold', fontFamily: 'var(--font-mono)', textTransform: 'uppercase' }}
                />
                <button type="submit" class="btn btn-primary btn-sm" disabled={joining}>
                    {joining ? '...' : 'Join'}
                </button>
            </form>
        </div>
    )
}

function EmptyRotationState({ statusInfo }) {
    return (
        <div style={{
            textAlign: 'center',
            padding: 'var(--space-8)',
            color: 'var(--color-text-dim)',
            background: 'var(--color-surface-alt)',
            borderRadius: 'var(--radius-lg)',
            border: '1px dashed var(--color-border)',
        }}>
            <div style={{ fontSize: '2rem', marginBottom: 'var(--space-2)' }}>
                {getEmptyStateEmoji(statusInfo)}
            </div>
            <div>{statusInfo.emptyQueueTitle}</div>
            <div style={{ fontSize: 'var(--text-xs)', marginTop: 'var(--space-1)' }}>
                {statusInfo.emptyQueueSubtitle}
            </div>
        </div>
    )
}

function StatusBadge({ label, color, background, borderColor }) {
    return (
        <span style={{
            fontSize: '0.7rem',
            fontWeight: 'var(--font-bold)',
            color,
            background,
            border: `1px solid ${borderColor}`,
            padding: '2px 6px',
            borderRadius: 'var(--radius-sm)',
            textTransform: 'uppercase',
            letterSpacing: '0.04em',
        }}>
            {label}
        </span>
    )
}

function RotationItem({ item, index, isOnStage, isOnDeck }) {
    const theme = ROW_THEMES[getRowRole(isOnStage, isOnDeck)]

    return (
        <div
            style={{
                background: theme.rowBackground,
                border: theme.rowBorder,
                borderRadius: 'var(--radius-lg)',
                padding: 'var(--space-3) var(--space-4)',
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--space-3)',
            }}
        >
            <div style={{
                width: '28px',
                height: '28px',
                borderRadius: '50%',
                background: theme.numberBackground,
                color: theme.numberColor,
                fontWeight: 'var(--font-bold)',
                fontSize: 'var(--text-xs)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
            }}>
                {index + 1}
            </div>

            <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{
                    fontWeight: 'var(--font-bold)',
                    color: theme.nameColor,
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                }}>
                    {item.singer_name}
                </div>
                <div style={{
                    color: 'var(--color-text-muted)',
                    fontSize: 'var(--text-xs)',
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                }}>
                    {item.song_title} {item.song_artist ? `— ${item.song_artist}` : ''}
                </div>
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexShrink: 0 }}>
                {isOnStage && (
                    <StatusBadge
                        label="On Stage"
                        color="#ffffff"
                        background="var(--color-accent)"
                        borderColor="var(--color-accent)"
                    />
                )}
                {isOnDeck && (
                    <StatusBadge
                        label="On Deck"
                        color="var(--color-chorus)"
                        borderColor="var(--color-chorus)"
                    />
                )}
            </div>
        </div>
    )
}

function RotationList({ queue, isEnded }) {
    const onDeckIndex = getOnDeckIndex(queue, isEnded)

    return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
            {queue.map((item, index) => {
                const isOnStage = !isEnded && item.status === 'up_next'
                const isOnDeck = !isEnded && !isOnStage && index === onDeckIndex
                return (
                    <RotationItem
                        key={item.id}
                        item={item}
                        index={index}
                        isOnStage={isOnStage}
                        isOnDeck={isOnDeck}
                    />
                )
            })}
        </div>
    )
}

function RotationContent({
    loading,
    noEvent,
    queue,
    statusInfo,
    joinCodeInput,
    onJoinCodeInput,
    onJoin,
    joining,
}) {
    if (loading) {
        return <Spinner label="Loading rotation..." />
    }
    if (noEvent) {
        return (
            <NoEventState
                joinCodeInput={joinCodeInput}
                onJoinCodeInput={onJoinCodeInput}
                onJoin={onJoin}
                joining={joining}
            />
        )
    }
    if (queue.length === 0) {
        return <EmptyRotationState statusInfo={statusInfo} />
    }
    return <RotationList queue={queue} isEnded={statusInfo.isEnded} />
}

export function SingerPublicRotation({
    loading,
    noEvent,
    queue,
    currentRound,
    eventStatus,
    joinCodeInput,
    onJoinCodeInput,
    onJoin,
    joining,
}) {
    const statusInfo = getShowStatusInfo(eventStatus)

    return (
        <div>
            <RotationHeader isEnded={statusInfo.isEnded} queueLength={queue.length} />
            <RotationContent
                loading={loading}
                noEvent={noEvent}
                queue={queue}
                statusInfo={statusInfo}
                joinCodeInput={joinCodeInput}
                onJoinCodeInput={onJoinCodeInput}
                onJoin={onJoin}
                joining={joining}
            />
        </div>
    )
}
