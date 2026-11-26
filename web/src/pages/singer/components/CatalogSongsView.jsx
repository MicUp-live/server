import { useRef } from 'preact/hooks'
import { useVirtualizer } from '@tanstack/react-virtual'
import { highlightMatch } from '../../../search/catalogIndex.js'
import { isFavoriteSong, getFavoriteSong } from '../../../utils/favoritesStore.js'
import { showToast } from '../../../components/Toast.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function CatalogSongsView({
    displaySongs = [],
    query = '',
    isSearching = false,
    event,
    onSelectSong,
    onToggleFavorite,
    onRequestWriteIn,
}) {
    const parentRef = useRef(null)
    const statusInfo = getShowStatusInfo(event)

    const rowVirtualizer = useVirtualizer({
        count: displaySongs.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => 76,
        overscan: 8,
    })

    if (displaySongs.length === 0) {
        return (
            <div style={{ textAlign: 'center', color: 'var(--color-text-dim)', padding: 'var(--space-8)' }}>
                <p style={{ margin: '0 0 var(--space-4)' }}>
                    {isSearching ? `No results for "${query}"` : 'No songs in this songbook yet'}
                </p>
                {isSearching && event?.allow_write_ins && !statusInfo.isEnded && onRequestWriteIn && (
                    <button
                        type="button"
                        aria-disabled={statusInfo.isPaused ? 'true' : undefined}
                        title={statusInfo.isPaused ? statusInfo.requestBlockedMessage : undefined}
                        class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
                        style={statusInfo.isPaused ? { opacity: 0.75 } : undefined}
                        onClick={() => {
                            if (statusInfo.isPaused) {
                                showToast(statusInfo.requestBlockedMessage, 'info')
                                return
                            }
                            onRequestWriteIn(query.trim())
                        }}
                    >
                        {statusInfo.isPaused ? statusInfo.submitButtonLabel : `✍️ Request "${query.trim()}" as a Write-In`}
                    </button>
                )}
            </div>
        )
    }

    return (
        <div
            ref={parentRef}
            style={{
                flex: 1,
                overflowY: 'auto',
                WebkitOverflowScrolling: 'touch',
                minHeight: 0,
                paddingBottom: 'var(--space-6)',
            }}
        >
            <div
                style={{
                    height: `${rowVirtualizer.getTotalSize()}px`,
                    width: '100%',
                    position: 'relative',
                }}
            >
                {rowVirtualizer.getVirtualItems().map((virtualItem) => {
                    const song = displaySongs[virtualItem.index]
                    if (!song) return null

                    const titleText = song.title || song.label || ''
                    const artistText = song.artist || ''
                    const isFav = isFavoriteSong(song.artist, titleText)
                    const favItem = isFav ? getFavoriteSong(song.artist, titleText) : null
                    const prefKey = favItem?.preferred_key || 0

                    const titleHighlights = isSearching ? highlightMatch(titleText, query) : [{ text: titleText, highlight: false }]
                    const artistHighlights = isSearching && artistText ? highlightMatch(artistText, query) : [{ text: artistText, highlight: false }]

                    return (
                        <div
                            key={virtualItem.key}
                            ref={rowVirtualizer.measureElement}
                            data-index={virtualItem.index}
                            style={{
                                position: 'absolute',
                                top: 0,
                                left: 0,
                                width: '100%',
                                transform: `translateY(${virtualItem.start}px)`,
                                paddingBottom: 'var(--space-2)',
                            }}
                        >
                            <div
                                style={{
                                    background: 'var(--color-surface)',
                                    border: `1px solid ${isFav ? 'rgba(234, 179, 8, 0.35)' : 'var(--color-border)'}`,
                                    borderRadius: 'var(--radius-lg)',
                                    display: 'flex',
                                    justifyContent: 'space-between',
                                    alignItems: 'center',
                                    padding: 'var(--space-3) var(--space-4)',
                                    boxSizing: 'border-box',
                                    gap: 'var(--space-2)',
                                }}
                            >
                                <button
                                    type="button"
                                    onClick={(e) => onToggleFavorite(e, song)}
                                    title={isFav ? "Favorited track (tap to remove)" : "Save track to favorites"}
                                    aria-label={isFav ? `Favorited: ${titleText}` : `Favorite ${titleText}`}
                                    style={{
                                        background: 'none',
                                        border: 'none',
                                        fontSize: '1.25rem',
                                        cursor: 'pointer',
                                        padding: '0 2px 0 0',
                                        color: isFav ? '#facc15' : 'rgba(255, 255, 255, 0.2)',
                                        filter: isFav ? 'drop-shadow(0 0 6px rgba(250, 204, 21, 0.6))' : 'none',
                                        lineHeight: 1,
                                        flexShrink: 0,
                                    }}
                                >
                                    {isFav ? '★' : '☆'}
                                </button>

                                <div class="song-catalog-copy" style={{ flex: 1, minWidth: 0 }}>
                                    <div class="song-catalog-title" style={{
                                        fontWeight: 'var(--font-semibold)', fontSize: 'var(--text-base)',
                                        color: 'var(--color-text)',
                                    }}>
                                        {titleHighlights.map((segment, i) =>
                                            segment.highlight ? <mark key={i} style={{ backgroundColor: 'var(--color-accent-glow)', color: 'var(--color-text)', borderRadius: '2px', padding: '0 2px' }}>{segment.text}</mark> : segment.text
                                        )}
                                    </div>
                                    <div class="song-catalog-artist" style={{
                                        color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                                        marginTop: '2px',
                                    }}>
                                        {artistText && (
                                            <span>
                                                {artistHighlights.map((segment, i) =>
                                                    segment.highlight ? <mark key={i} style={{ backgroundColor: 'var(--color-accent-glow)', color: 'var(--color-text)', borderRadius: '2px', padding: '0 2px' }}>{segment.text}</mark> : segment.text
                                                )}
                                            </span>
                                        )}
                                    </div>
                                    {(song.genre || song.versions?.length > 1 || (prefKey !== 0 && event?.allow_key_change !== false)) && (
                                        <div class="song-catalog-meta" style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', marginTop: 'var(--space-1)', flexWrap: 'wrap' }}>
                                            {song.versions?.length > 1 && (
                                                <span style={{
                                                    background: 'var(--color-surface-alt)',
                                                    color: 'var(--color-text-muted)',
                                                    border: '1px solid var(--color-border)',
                                                    padding: '0 var(--space-2)',
                                                    borderRadius: 'var(--radius-sm)',
                                                    fontSize: '0.75em',
                                                    fontWeight: 'var(--font-semibold)',
                                                }}>
                                                    {song.versions.length} versions
                                                </span>
                                            )}
                                            {song.genre && (
                                                <span style={{
                                                    background: 'rgba(245, 158, 11, 0.15)',
                                                    color: 'var(--color-chorus)',
                                                    border: '1px solid rgba(245, 158, 11, 0.25)',
                                                    padding: '0 var(--space-2)',
                                                    borderRadius: 'var(--radius-sm)',
                                                    fontSize: '0.75em',
                                                    fontWeight: 'var(--font-semibold)',
                                                    letterSpacing: '0.04em',
                                                    textTransform: 'uppercase',
                                                }}>
                                                    {song.genre}
                                                </span>
                                            )}
                                            {prefKey !== 0 && (event?.allow_key_change !== false) && (
                                                <span style={{
                                                    background: 'rgba(59, 130, 246, 0.2)',
                                                    color: '#93c5fd',
                                                    border: '1px solid rgba(59, 130, 246, 0.4)',
                                                    padding: '0 6px',
                                                    borderRadius: 'var(--radius-sm)',
                                                    fontSize: '0.75em',
                                                    fontWeight: 'var(--font-semibold)',
                                                }}>
                                                    Key: {prefKey > 0 ? `+${prefKey}♯` : `${prefKey}♭`}
                                                </span>
                                            )}
                                        </div>
                                    )}
                                </div>
                                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexShrink: 0 }}>
                                    {event && !statusInfo.isEnded && (
                                        <button
                                            type="button"
                                            aria-disabled={statusInfo.isPaused ? 'true' : undefined}
                                            title={statusInfo.isPaused ? statusInfo.requestBlockedMessage : undefined}
                                            onClick={() => {
                                                if (statusInfo.isPaused) {
                                                    showToast(statusInfo.requestBlockedMessage, 'info')
                                                    return
                                                }
                                                onSelectSong(song)
                                            }}
                                            class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
                                            style={{
                                                padding: 'var(--space-1) var(--space-3)',
                                                fontSize: 'var(--text-xs)',
                                                fontWeight: 'var(--font-bold)',
                                                display: 'inline-flex',
                                                alignItems: 'center',
                                                gap: 'var(--space-1)',
                                                borderRadius: 'var(--radius-md)',
                                                whiteSpace: 'nowrap',
                                                opacity: statusInfo.isPaused ? 0.75 : 1,
                                            }}
                                        >
                                            {statusInfo.requestButtonLabel}
                                        </button>
                                    )}
                                </div>
                            </div>
                        </div>
                    )
                })}
            </div>
        </div>
    )
}
