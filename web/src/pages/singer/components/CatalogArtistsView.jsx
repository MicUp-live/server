import { useRef } from 'preact/hooks'
import { isFavoriteSong, getFavoriteSong } from '../../../utils/favoritesStore.js'
import { showToast } from '../../../components/Toast.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function CatalogArtistsView({
    artistGroups = [],
    query = '',
    expandedArtists = new Set(),
    loadingArtistSongs = {},
    event,
    onToggleArtistExpand,
    onToggleArtistFavorite,
    onToggleSongFavorite,
    onSelectSong,
}) {
    const artistParentRef = useRef(null)
    const statusInfo = getShowStatusInfo(event)

    if (artistGroups.length === 0) {
        return (
            <p style={{ textAlign: 'center', color: 'var(--color-text-dim)', padding: 'var(--space-8)' }}>
                {query ? `No artists matching "${query}"` : 'No artists in this catalog yet'}
            </p>
        )
    }

    return (
        <div
            ref={artistParentRef}
            style={{
                flex: 1,
                overflowY: 'auto',
                WebkitOverflowScrolling: 'touch',
                minHeight: 0,
                paddingBottom: 'var(--space-6)',
            }}
        >
            {artistGroups.map(group => {
                const isExpanded = expandedArtists.has(group.norm)
                const isArtistFav = group.isFav

                return (
                    <div
                        key={group.norm}
                        id={`artist-card-${group.norm}`}
                        style={{
                            marginBottom: 'var(--space-2)',
                            background: 'var(--color-surface)',
                            border: `1px solid ${isArtistFav ? 'rgba(234, 179, 8, 0.4)' : 'var(--color-border)'}`,
                            borderRadius: 'var(--radius-lg)',
                            overflow: 'hidden',
                            transition: 'border-color 0.2s ease',
                        }}
                    >
                        {/* Artist Accordion Header */}
                        <div
                            onClick={() => onToggleArtistExpand(group.norm)}
                            style={{
                                padding: 'var(--space-3) var(--space-4)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'space-between',
                                cursor: 'pointer',
                                background: isArtistFav ? 'rgba(234, 179, 8, 0.06)' : 'transparent',
                                userSelect: 'none',
                            }}
                        >
                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)', minWidth: 0 }}>
                                <button
                                    type="button"
                                    onClick={(e) => onToggleArtistFavorite(e, group.artist)}
                                    title={isArtistFav ? "Favorited artist (tap to unfavorite)" : "Star this artist"}
                                    aria-label={isArtistFav ? `Unfavorite ${group.artist}` : `Favorite ${group.artist}`}
                                    style={{
                                        background: 'none',
                                        border: 'none',
                                        fontSize: '1.25rem',
                                        cursor: 'pointer',
                                        padding: 0,
                                        color: isArtistFav ? '#facc15' : 'rgba(255, 255, 255, 0.2)',
                                        filter: isArtistFav ? 'drop-shadow(0 0 6px rgba(250, 204, 21, 0.6))' : 'none',
                                        lineHeight: 1,
                                    }}
                                >
                                    {isArtistFav ? '★' : '☆'}
                                </button>
                                <div style={{ fontWeight: 'bold', fontSize: 'var(--text-base)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                    {group.artist}
                                </div>
                                {isArtistFav && (
                                    <span style={{
                                        fontSize: '0.65rem',
                                        fontWeight: 'bold',
                                        textTransform: 'uppercase',
                                        background: 'rgba(234, 179, 8, 0.2)',
                                        color: '#facc15',
                                        padding: '1px 6px',
                                        borderRadius: '4px',
                                        letterSpacing: '0.05em',
                                    }}>
                                        Favorite
                                    </span>
                                )}
                            </div>

                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexShrink: 0 }}>
                                <span style={{
                                    fontSize: 'var(--text-xs)',
                                    background: 'var(--color-surface-alt)',
                                    color: 'var(--color-text-muted)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-full)',
                                    border: '1px solid var(--color-border)',
                                }}>
                                    {group.count || group.songs.length} {(group.count || group.songs.length) === 1 ? 'song' : 'songs'}
                                </span>
                                <span style={{
                                    fontSize: 'var(--text-sm)',
                                    color: 'var(--color-text-dim)',
                                    transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)',
                                    transition: 'transform 0.2s ease',
                                    display: 'inline-block',
                                }}>
                                    ▼
                                </span>
                            </div>
                        </div>

                        {/* Expanded Song List */}
                        {isExpanded && (
                            <div style={{
                                borderTop: '1px solid var(--color-border)',
                                background: 'rgba(0, 0, 0, 0.2)',
                                padding: 'var(--space-2) var(--space-4)',
                            }}>
                                {loadingArtistSongs[group.norm] ? (
                                    <div style={{ padding: 'var(--space-3) 0', textAlign: 'center', color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)' }}>
                                        Loading songs...
                                    </div>
                                ) : group.songs.length === 0 ? (
                                    <div style={{ padding: 'var(--space-3) 0', textAlign: 'center', color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)' }}>
                                        No songs found
                                    </div>
                                ) : (
                                    group.songs.map(song => {
                                        const titleText = song.title || song.label || ''
                                        const isSongFav = isFavoriteSong(song.artist, titleText)
                                        const favInfo = isSongFav ? getFavoriteSong(song.artist, titleText) : null
                                        const keyMem = favInfo?.preferred_key || 0

                                        return (
                                            <div
                                                key={song.song_id || song.id || titleText}
                                                style={{
                                                    display: 'flex',
                                                    justifyContent: 'space-between',
                                                    alignItems: 'center',
                                                    padding: 'var(--space-2) 0',
                                                    borderBottom: '1px solid rgba(255, 255, 255, 0.04)',
                                                    gap: 'var(--space-2)',
                                                }}
                                            >
                                                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', minWidth: 0, flex: 1 }}>
                                                    <button
                                                        type="button"
                                                        onClick={(e) => onToggleSongFavorite(e, song)}
                                                        style={{
                                                            background: 'none',
                                                            border: 'none',
                                                            cursor: 'pointer',
                                                            fontSize: '1.15rem',
                                                            padding: 0,
                                                            color: isSongFav ? '#facc15' : 'rgba(255, 255, 255, 0.2)',
                                                            lineHeight: 1,
                                                        }}
                                                        title={isSongFav ? "Favorited" : "Add to favorites"}
                                                    >
                                                        {isSongFav ? '★' : '☆'}
                                                    </button>
                                                    <div style={{ minWidth: 0 }}>
                                                        <div style={{ fontWeight: 'var(--font-medium)', fontSize: 'var(--text-sm)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                                            {titleText}
                                                        </div>
                                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', marginTop: '2px', flexWrap: 'wrap' }}>
                                                            {song.versions?.length > 1 && (
                                                                <span style={{ fontSize: '0.7em', color: 'var(--color-text-muted)', background: 'var(--color-surface-alt)', border: '1px solid var(--color-border)', padding: '0 5px', borderRadius: '2px' }}>
                                                                    {song.versions.length} versions
                                                                </span>
                                                            )}
                                                            {song.genre && (
                                                                <span style={{ fontSize: '0.7em', color: 'var(--color-chorus)', textTransform: 'uppercase' }}>
                                                                    {song.genre}
                                                                </span>
                                                            )}
                                                            {keyMem !== 0 && (event?.allow_key_change !== false) && (
                                                                <span style={{
                                                                    fontSize: '0.7em',
                                                                    color: '#93c5fd',
                                                                    background: 'rgba(59, 130, 246, 0.2)',
                                                                    padding: '0 5px',
                                                                    borderRadius: '2px',
                                                                    fontWeight: 'bold',
                                                                }}>
                                                                    Key: {keyMem > 0 ? `+${keyMem}♯` : `${keyMem}♭`}
                                                                </span>
                                                            )}
                                                        </div>
                                                    </div>
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
                                                                padding: '3px 10px',
                                                                fontSize: 'var(--text-xs)',
                                                                fontWeight: 'var(--font-bold)',
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
                                        )
                                    })
                                )}
                            </div>
                        )}
                    </div>
                )
            })}
        </div>
    )
}
