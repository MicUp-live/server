import { Spinner } from '../../Spinner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

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
            <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginBottom: 'var(--space-3)'
            }}>
                <div style={{
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'var(--font-bold)',
                    color: 'var(--color-text-muted)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                }}>
                    {statusInfo.isEnded
                        ? `Final Rotation (${queue.length} remaining)`
                        : `Full Room Rotation (${queue.length} in line)`}
                </div>
                <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                    {statusInfo.isEnded ? 'Show ended' : 'Auto-updates live'}
                </span>
            </div>

            {loading ? (
                <Spinner label="Loading rotation..." />
            ) : noEvent ? (
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
            ) : queue.length === 0 ? (
                <div style={{
                    textAlign: 'center',
                    padding: 'var(--space-8)',
                    color: 'var(--color-text-dim)',
                    background: 'var(--color-surface-alt)',
                    borderRadius: 'var(--radius-lg)',
                    border: '1px dashed var(--color-border)',
                }}>
                    <div style={{ fontSize: '2rem', marginBottom: 'var(--space-2)' }}>
                        {statusInfo.isPaused ? '⏸️' : statusInfo.isEnded ? '🏁' : '🎤'}
                    </div>
                    <div>{statusInfo.emptyQueueTitle}</div>
                    <div style={{ fontSize: 'var(--text-xs)', marginTop: 'var(--space-1)' }}>
                        {statusInfo.emptyQueueSubtitle}
                    </div>
                </div>
            ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                    {(() => {
                        const hasUpNext = !statusInfo.isEnded && queue.some(q => q.status === 'up_next')
                        const onDeckIndex = !statusInfo.isEnded
                            ? (hasUpNext ? queue.findIndex(q => q.status !== 'up_next') : 0)
                            : -1

                        return queue.map((item, index) => {
                            const isOnStage = !statusInfo.isEnded && item.status === 'up_next'
                            const isOnDeck = !statusInfo.isEnded && !isOnStage && index === onDeckIndex
                            return (
                                <div
                                    key={item.id}
                                    style={{
                                        background: isOnStage ? 'rgba(255, 42, 95, 0.08)' : (isOnDeck ? 'rgba(245, 158, 11, 0.08)' : 'var(--color-surface-alt)'),
                                        border: `1px solid ${isOnStage ? 'var(--color-accent)' : (isOnDeck ? 'var(--color-chorus)' : 'var(--color-border)')}`,
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
                                        background: isOnStage ? 'var(--color-accent)' : (isOnDeck ? 'var(--color-chorus)' : 'var(--color-surface)'),
                                        color: isOnStage ? '#ffffff' : (isOnDeck ? '#0b0b10' : 'var(--color-text-muted)'),
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
                                            color: isOnStage ? 'var(--color-accent)' : (isOnDeck ? 'var(--color-chorus)' : 'var(--color-text)'),
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
                                            <span style={{
                                                fontSize: '0.7rem',
                                                fontWeight: 'var(--font-bold)',
                                                color: '#ffffff',
                                                background: 'var(--color-accent)',
                                                border: '1px solid var(--color-accent)',
                                                padding: '2px 6px',
                                                borderRadius: 'var(--radius-sm)',
                                                textTransform: 'uppercase',
                                                letterSpacing: '0.04em',
                                            }}>
                                                On Stage
                                            </span>
                                        )}
                                        {isOnDeck && (
                                            <span style={{
                                                fontSize: '0.7rem',
                                                fontWeight: 'var(--font-bold)',
                                                color: 'var(--color-chorus)',
                                                border: '1px solid var(--color-chorus)',
                                                padding: '2px 6px',
                                                borderRadius: 'var(--radius-sm)',
                                                textTransform: 'uppercase',
                                                letterSpacing: '0.04em',
                                            }}>
                                                On Deck
                                            </span>
                                        )}
                                    </div>
                                </div>
                            )
                        })
                    })()}
                </div>
            )}
        </div>
    )
}
