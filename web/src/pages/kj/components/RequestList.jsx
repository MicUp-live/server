import { Spinner } from '../../../components/Spinner.jsx'
import { RequestCard } from './RequestCard.jsx'

export function RequestList({
    tab = 'active',
    setTab,
    loading = false,
    loadError = null,
    onRetry,
    currentEvent,
    currentRound = 1,
    stagingRequests = [],
    activeRequests = [],
    historyRequests = [],
    singerPerformanceCounts = {},
    copiedId = null,
    onMoveRequest,
    onUpdateRequestRound,
    onUpdateStatus,
    onDeferRequest,
    onDeleteRequest,
    onCopySongInfo,
    onEditRequest,
    onOpenWeightsModal,
    onAdvanceRound,
    onLaunchShow,
    onSelectEventId,
    events = [],
}) {
    const displayedRequests = tab === 'staging' ? stagingRequests : (tab === 'active' ? activeRequests : historyRequests)

    if (loadError) {
        return (
            <div style={{
                background: 'linear-gradient(135deg, rgba(239, 68, 68, 0.08) 0%, rgba(20, 20, 30, 0.95) 100%)',
                border: '1px solid rgba(239, 68, 68, 0.3)',
                borderRadius: 'var(--radius-xl)',
                padding: 'var(--space-8) var(--space-6)',
                marginTop: 'var(--space-4)',
                marginBottom: 'var(--space-6)',
                textAlign: 'center',
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: 'var(--space-4)',
                boxShadow: '0 8px 32px rgba(0, 0, 0, 0.4)',
            }} role="alert">
                <div style={{ fontSize: '3.5rem' }}>⚠️</div>
                <div style={{ fontSize: 'var(--text-xs)', color: '#f87171', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                    Lookup Error
                </div>
                <h3 style={{ margin: 0, fontSize: 'var(--text-2xl)', color: '#fff' }}>
                    Failed to Load Host Data
                </h3>
                <p style={{ margin: 0, maxWidth: '580px', fontSize: 'var(--text-base)', color: 'var(--color-text-muted)', lineHeight: 1.6 }}>
                    {loadError}. Please check your connection and try again.
                </p>
                {onRetry && (
                    <div style={{ display: 'flex', gap: 'var(--space-3)', flexWrap: 'wrap', justifyContent: 'center', marginTop: 'var(--space-2)' }}>
                        <button
                            type="button"
                            onClick={onRetry}
                            class="btn btn-primary"
                            disabled={loading}
                        >
                            {loading ? 'Retrying...' : '🔄 Retry'}
                        </button>
                    </div>
                )}
            </div>
        )
    }

    if (!currentEvent && !loading) {
        return (
            <div style={{
                background: 'linear-gradient(135deg, rgba(255, 42, 95, 0.08) 0%, rgba(20, 20, 30, 0.95) 100%)',
                border: '1px solid var(--color-border)',
                borderRadius: 'var(--radius-xl)',
                padding: 'var(--space-8) var(--space-6)',
                marginTop: 'var(--space-4)',
                marginBottom: 'var(--space-6)',
                textAlign: 'center',
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: 'var(--space-4)',
                boxShadow: '0 8px 32px rgba(0, 0, 0, 0.4)',
            }}>
                <div style={{ fontSize: '3.5rem' }}>🎤</div>
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-accent)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                    Event Required For Live Queues
                </div>
                <h3 style={{ margin: 0, fontSize: 'var(--text-2xl)', color: '#fff' }}>
                    A Queue Cannot Be Started Without an Event
                </h3>
                <p style={{ margin: 0, maxWidth: '580px', fontSize: 'var(--text-base)', color: 'var(--color-text-muted)', lineHeight: 1.6 }}>
                    Songbooks are your master song catalog. Singer queues, live requests, and TV stage screens belong strictly to live events. Create a new show or select an existing show to start your queue!
                </p>
                <div style={{ display: 'flex', gap: 'var(--space-3)', flexWrap: 'wrap', justifyContent: 'center', marginTop: 'var(--space-2)' }}>
                    <button
                        type="button"
                        onClick={onLaunchShow}
                        class="btn btn-primary"
                        style={{ display: 'inline-flex', alignItems: 'center', gap: '8px', padding: 'var(--space-3) var(--space-6)', fontSize: 'var(--text-base)' }}
                    >
                        <span>🚀</span>
                        <span>Launch Tonight's Show</span>
                    </button>
                    {events.length > 0 && (
                        <button
                            type="button"
                            onClick={() => onSelectEventId(events[0].id)}
                            class="btn btn-secondary"
                            style={{ padding: 'var(--space-3) var(--space-6)', fontSize: 'var(--text-base)' }}
                        >
                            Select {events[0].venue_name ? `${events[0].venue_name} (${events[0].join_code})` : `${events[0].name} (${events[0].join_code})`}
                        </button>
                    )}
                </div>
            </div>
        )
    }

    return (
        <div>
            {/* Tabs */}
            <div class="kj-queue-tabs">
                <button
                    type="button"
                    onClick={() => setTab('staging')}
                    class={`kj-queue-tab${tab === 'staging' ? ' is-staging-active' : ''}`}
                >
                    <span class="kj-tab-label-full">📥 Staging Inbox ({stagingRequests.length})</span>
                    <span class="kj-tab-label-short">📥 Staging ({stagingRequests.length})</span>
                    {stagingRequests.length > 0 && (
                        <span style={{
                            fontSize: '0.65rem',
                            background: '#a855f7',
                            color: '#fff',
                            borderRadius: 'var(--radius-full)',
                            padding: '1px 5px',
                            fontWeight: 'bold',
                        }}>
                            New
                        </span>
                    )}
                </button>
                <button
                    type="button"
                    onClick={() => setTab('active')}
                    class={`kj-queue-tab${tab === 'active' ? ' is-active' : ''}`}
                >
                    <span class="kj-tab-label-full">🎤 Active Queue ({activeRequests.length})</span>
                    <span class="kj-tab-label-short">🎤 Queue ({activeRequests.length})</span>
                </button>
                <button
                    type="button"
                    onClick={() => setTab('history')}
                    class={`kj-queue-tab${tab === 'history' ? ' is-active' : ''}`}
                >
                    <span class="kj-tab-label-full">📜 Completed / History ({historyRequests.length})</span>
                    <span class="kj-tab-label-short">📜 History ({historyRequests.length})</span>
                </button>
            </div>

            {/* Request Cards */}
            {loading ? (
                <Spinner label="Loading queue..." />
            ) : displayedRequests.length === 0 ? (
                <div style={{
                    textAlign: 'center',
                    padding: 'var(--space-12)',
                    background: 'var(--color-surface)',
                    border: '1px dashed var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                    color: 'var(--color-text-muted)'
                }}>
                    <div style={{ fontSize: '2.5rem', marginBottom: 'var(--space-2)' }}>
                        {tab === 'staging' ? '📥' : '🎤'}
                    </div>
                    <h3 style={{ margin: 0, color: 'var(--color-text)' }}>
                        {tab === 'staging'
                            ? 'Staging inbox is empty'
                            : tab === 'active'
                            ? 'No active requests in queue'
                            : 'No history yet'}
                    </h3>
                    <p style={{ margin: 'var(--space-2) 0 0', fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)' }}>
                        {tab === 'staging'
                            ? (currentEvent?.auto_accept
                                ? 'Auto-Accept is ON: incoming singer requests automatically enter the active queue.'
                                : 'Incoming singer requests will appear here for review before entering queue.')
                            : tab === 'active'
                            ? 'Singers can request songs from your digital songbook URL or QR code.'
                            : 'Songs marked as completed will appear here.'}
                    </p>
                </div>
            ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                    {displayedRequests.map((req, index) => (
                        <RequestCard
                            key={req.id}
                            req={req}
                            index={index}
                            tab={tab}
                            activeRequests={activeRequests}
                            singerPerformanceCounts={singerPerformanceCounts}
                            currentEvent={currentEvent}
                            currentRound={currentRound}
                            copiedId={copiedId}
                            onMoveRequest={onMoveRequest}
                            onUpdateRequestRound={onUpdateRequestRound}
                            onUpdateStatus={onUpdateStatus}
                            onDeferRequest={onDeferRequest}
                            onDeleteRequest={onDeleteRequest}
                            onCopySongInfo={onCopySongInfo}
                            onEditRequest={onEditRequest}
                            onOpenWeightsModal={onOpenWeightsModal}
                        />
                    ))}
                </div>
            )}
        </div>
    )
}
