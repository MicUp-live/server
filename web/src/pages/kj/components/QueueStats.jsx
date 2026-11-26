import { formatWaitEstimate } from '../../../utils/waitTime.js'

export function QueueStats({
    currentEvent,
    currentRound = 1,
    activeCount = 0,
    completedCount = 0,
    totalCount = 0,
    uniqueSingersCount = 0,
    onAdvanceRound,
    onOpenWeightsModal,
}) {
    const weightedCount = Object.keys(currentEvent?.singer_weights || {}).length
    const waitEstimate = formatWaitEstimate(activeCount)
    return (
        <div class="kj-stats-grid">
            {currentEvent?.rotation_mode === 'fair_priority' ? (
                <div class="kj-stat-card">
                    <div class="kj-stat-label">Mode</div>
                    <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'var(--font-bold)', color: 'var(--color-accent)', marginTop: '2px' }}>
                        ⚖️ Fair
                    </div>
                    {onOpenWeightsModal && (
                        <button
                            type="button"
                            onClick={() => onOpenWeightsModal('')}
                            class="btn btn-secondary btn-sm kj-stat-next-btn"
                            style={{
                                fontSize: 'var(--text-xs)',
                                padding: '2px 8px',
                                marginTop: '4px',
                                borderColor: weightedCount > 0 ? 'var(--color-accent)' : 'var(--color-border)',
                                color: weightedCount > 0 ? 'var(--color-accent)' : 'var(--color-text-muted)',
                            }}
                            title="Manage private host-only singer priority weights"
                        >
                            {weightedCount > 0 ? `Weights (${weightedCount})` : 'Weights'}
                        </button>
                    )}
                </div>
            ) : (
                <div class="kj-stat-card">
                    <div class="kj-stat-label">Singers</div>
                    <div class="kj-stat-value" style={{ color: 'var(--color-accent)' }}>
                        {uniqueSingersCount}
                    </div>
                </div>
            )}

            <div class="kj-stat-card">
                <div class="kj-stat-label">In Queue</div>
                <div class="kj-stat-value" style={{ color: 'var(--color-chorus)' }}>
                    {activeCount}
                </div>
                {waitEstimate && (
                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                        {waitEstimate}
                    </div>
                )}
            </div>

            <div class="kj-stat-card">
                <div class="kj-stat-label">Sung</div>
                <div class="kj-stat-value" style={{ color: 'var(--color-success)' }}>
                    {completedCount}
                </div>
            </div>

            <div class="kj-stat-card">
                <div class="kj-stat-label">
                    <span class="kj-tab-label-full">Total Requests</span>
                    <span class="kj-tab-label-short">Total</span>
                </div>
                <div class="kj-stat-value" style={{ color: 'var(--color-text)' }}>
                    {totalCount}
                </div>
            </div>
        </div>
    )
}
