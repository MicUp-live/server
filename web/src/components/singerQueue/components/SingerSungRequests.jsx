import { isFavoriteSong, toggleFavoriteSong } from '../../../utils/favoritesStore.js'
import { showToast } from '../../toastState.js'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function SingerSungRequests({
    completedRequests,
    eventStatus,
    onRequestsChange,
    onReRequest,
}) {
    if (!completedRequests || completedRequests.length === 0) return null
    const statusInfo = getShowStatusInfo(eventStatus)

    return (
        <div style={{ marginBottom: 'var(--space-6)' }}>
            <div style={{
                fontSize: 'var(--text-xs)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-success)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                marginBottom: 'var(--space-2)',
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
            }}>
                <span>🎉 Songs Sung Tonight ({completedRequests.length})</span>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                {completedRequests.map((req) => {
                    const isFav = isFavoriteSong(req.song_artist, req.song_title)
                    return (
                        <div
                            key={req.id}
                            style={{
                                background: 'rgba(34, 197, 94, 0.05)',
                                border: '1px solid rgba(34, 197, 94, 0.25)',
                                borderRadius: 'var(--radius-lg)',
                                padding: 'var(--space-3) var(--space-4)',
                                display: 'flex',
                                justifyContent: 'space-between',
                                alignItems: 'center',
                            }}
                        >
                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', minWidth: 0, flex: 1 }}>
                                <button
                                    type="button"
                                    onClick={() => {
                                        const next = toggleFavoriteSong({ artist: req.song_artist, title: req.song_title }, req.key_change)
                                        showToast(next ? `★ Added "${req.song_title}" to Favorites!` : `Removed from Favorites`)
                                        onRequestsChange?.()
                                    }}
                                    style={{
                                        background: 'none',
                                        border: 'none',
                                        cursor: 'pointer',
                                        padding: 0,
                                        fontSize: '1.15rem',
                                        color: isFav ? '#facc15' : 'rgba(255, 255, 255, 0.2)',
                                        lineHeight: 1,
                                    }}
                                    title={isFav ? "Saved to Favorites" : "Save to Favorites"}
                                >
                                    {isFav ? '★' : '☆'}
                                </button>
                                <div style={{ minWidth: 0 }}>
                                    <div style={{ fontWeight: 'var(--font-bold)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                        {req.song_title}
                                    </div>
                                    <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-xs)', marginTop: '2px' }}>
                                        {req.song_artist}
                                        {req.key_change !== 0 && (
                                            <span style={{ marginLeft: 'var(--space-2)', color: '#60a5fa', fontWeight: 'bold' }}>
                                                Key: {req.key_change > 0 ? `+${req.key_change}♯` : `${req.key_change}♭`}
                                            </span>
                                        )}
                                    </div>
                                </div>
                            </div>

                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexShrink: 0 }}>
                                <span style={{
                                    fontSize: '0.7em',
                                    fontWeight: 'bold',
                                    color: '#86efac',
                                    background: 'rgba(34, 197, 94, 0.15)',
                                    padding: '2px 6px',
                                    borderRadius: '4px',
                                }}>
                                    ✅ Sung
                                </span>
                                {onReRequest && !statusInfo.isEnded && (
                                    <button
                                        type="button"
                                        aria-disabled={statusInfo.isPaused ? 'true' : undefined}
                                        title={statusInfo.isPaused ? statusInfo.requestBlockedMessage : undefined}
                                        onClick={() => {
                                            if (statusInfo.isPaused) {
                                                showToast(statusInfo.requestBlockedMessage, 'info')
                                                return
                                            }
                                            onReRequest({
                                                id: req.song_id,
                                                song_id: req.song_id,
                                                title: req.song_title,
                                                label: req.song_title,
                                                artist: req.song_artist
                                            })
                                        }}
                                        class="btn btn-secondary btn-sm"
                                        style={{
                                            fontSize: 'var(--text-xs)',
                                            padding: '2px 8px',
                                            borderRadius: 'var(--radius-md)',
                                            whiteSpace: 'nowrap',
                                            opacity: statusInfo.isPaused ? 0.75 : 1,
                                        }}
                                    >
                                        {statusInfo.isPaused ? statusInfo.requestButtonLabel : '↻ Sing Again'}
                                    </button>
                                )}
                            </div>
                        </div>
                    )
                })}
            </div>
        </div>
    )
}
