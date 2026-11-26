import { songKey, isFavoriteSong, getFavoriteSong } from '../../../utils/favoritesStore.js'
import { showToast } from '../../../components/Toast.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function CatalogFavoritesView({
    isRepertoireEmpty = false,
    isSearching = false,
    query = '',
    displayedFavoriteArtists = [],
    displayedTonightSongs = [],
    displayedOffBookSongs = [],
    displayedOffBookArtists = [],
    expandedArtists = new Set(),
    loadingArtistSongs = {},
    event,
    onToggleArtistExpand,
    onToggleArtistFavorite,
    onToggleSongFavorite,
    onSelectSong,
    onSwitchToSongs,
}) {
    const statusInfo = getShowStatusInfo(event)

    const handleRequestClick = (song) => {
        if (!event || statusInfo.isEnded) return
        if (statusInfo.isPaused) {
            showToast(statusInfo.requestBlockedMessage, 'info')
            return
        }
        onSelectSong?.(song)
    }

    return (
        <div 
            style={{ 
                flex: 1, 
                overflowY: 'auto', 
                WebkitOverflowScrolling: 'touch',
                minHeight: 0,
                paddingBottom: 'var(--space-6)',
            }}
        >
            {isRepertoireEmpty ? (
                <div style={{
                    textAlign: 'center',
                    padding: 'var(--space-10) var(--space-4)',
                    background: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                }}>
                    <div style={{ fontSize: '3rem', marginBottom: 'var(--space-2)' }}>⭐</div>
                    <h3 style={{ margin: '0 0 var(--space-2)', fontSize: 'var(--text-lg)', color: 'var(--color-text)' }}>Your Repertoire is Empty</h3>
                    <p style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', maxWidth: '400px', margin: '0 auto var(--space-4)', lineHeight: 1.5 }}>
                        Tap the ☆ star icon on any song or artist in the songbook to save your go-to tracks.
                        Your favorites and pitch key adjustments will follow you automatically across every MicUp show!
                    </p>
                    <button
                        type="button"
                        onClick={onSwitchToSongs}
                        class="btn btn-secondary btn-sm"
                    >
                        Browse Songbook →
                    </button>
                </div>
            ) : isSearching && displayedFavoriteArtists.length === 0 && displayedTonightSongs.length === 0 && displayedOffBookSongs.length === 0 && displayedOffBookArtists.length === 0 ? (
                <p style={{ textAlign: 'center', color: 'var(--color-text-dim)', padding: 'var(--space-8)' }}>
                    No favorites matching "{query}"
                </p>
            ) : (
                <div>
                    {/* Section 1: Favorite Artists Available Tonight */}
                    {displayedFavoriteArtists.length > 0 && (
                        <div style={{ marginBottom: 'var(--space-5)' }}>
                            <div style={{
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'bold',
                                color: '#facc15',
                                textTransform: 'uppercase',
                                letterSpacing: '0.08em',
                                marginBottom: 'var(--space-2)',
                                display: 'flex',
                                alignItems: 'center',
                                gap: '6px'
                            }}>
                                <span>★ Favorite Artists Tonight ({displayedFavoriteArtists.length})</span>
                            </div>
                            {displayedFavoriteArtists.map(group => {
                                const isExpanded = expandedArtists.has(group.norm)
                                return (
                                    <div
                                        key={group.norm}
                                        id={`fav-artist-card-${group.norm}`}
                                        style={{
                                            marginBottom: 'var(--space-2)',
                                            background: 'var(--color-surface)',
                                            border: '1px solid rgba(234, 179, 8, 0.4)',
                                            borderRadius: 'var(--radius-lg)',
                                            overflow: 'hidden',
                                            transition: 'border-color 0.2s ease',
                                        }}
                                    >
                                        {/* Artist Header */}
                                        <div
                                            onClick={() => onToggleArtistExpand?.(group.norm)}
                                            style={{
                                                padding: 'var(--space-3) var(--space-4)',
                                                display: 'flex',
                                                alignItems: 'center',
                                                justifyContent: 'space-between',
                                                cursor: 'pointer',
                                                background: 'rgba(234, 179, 8, 0.06)',
                                                userSelect: 'none',
                                            }}
                                        >
                                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)', minWidth: 0 }}>
                                                <button
                                                    type="button"
                                                    onClick={(e) => onToggleArtistFavorite?.(e, group.artist)}
                                                    title="Favorited artist (tap to unfavorite)"
                                                    aria-label={`Unfavorite ${group.artist}`}
                                                    style={{
                                                        background: 'none',
                                                        border: 'none',
                                                        fontSize: '1.25rem',
                                                        cursor: 'pointer',
                                                        padding: 0,
                                                        color: '#facc15',
                                                        filter: 'drop-shadow(0 0 6px rgba(250, 204, 21, 0.6))',
                                                        lineHeight: 1,
                                                    }}
                                                >
                                                    ★
                                                </button>
                                                <div style={{ fontWeight: 'bold', fontSize: 'var(--text-base)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                                    {group.artist}
                                                </div>
                                                <span style={{
                                                    fontSize: '0.65rem',
                                                    fontWeight: 'bold',
                                                    textTransform: 'uppercase',
                                                    background: 'rgba(234, 179, 8, 0.2)',
                                                    color: '#facc15',
                                                    padding: '1px 6px',
                                                    borderRadius: '4px',
                                                    letterSpacing: '0.05em'
                                                }}>
                                                    Favorite Artist
                                                </span>
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
                                                    {group.count} {group.count === 1 ? 'song' : 'songs'}
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

                                        {/* Expanded Songs */}
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
                                                                    gap: 'var(--space-2)'
                                                                }}
                                                            >
                                                                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', minWidth: 0, flex: 1 }}>
                                                                    <button
                                                                        type="button"
                                                                        onClick={(e) => onToggleSongFavorite?.(e, song)}
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
                                                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', marginTop: '2px' }}>
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
                                                                                    fontWeight: 'bold'
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
                                                                            onClick={() => handleRequestClick(song)}
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
                    )}

                    {/* Section 2: Favorite Songs Available Tonight */}
                    <div style={{ marginBottom: 'var(--space-5)' }}>
                        <div style={{
                            fontSize: 'var(--text-xs)',
                            fontWeight: 'bold',
                            color: '#facc15',
                            textTransform: 'uppercase',
                            letterSpacing: '0.08em',
                            marginBottom: 'var(--space-2)',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '6px'
                        }}>
                            <span>★ Favorite Songs Tonight ({displayedTonightSongs.length})</span>
                        </div>
                        {displayedTonightSongs.length === 0 ? (
                            <div style={{
                                padding: 'var(--space-4)',
                                background: 'rgba(255, 255, 255, 0.02)',
                                border: '1px dashed var(--color-border)',
                                borderRadius: 'var(--radius-lg)',
                                color: 'var(--color-text-dim)',
                                fontSize: 'var(--text-sm)',
                                textAlign: 'center'
                            }}>
                                {displayedFavoriteArtists.length > 0 ? "You have favorite artists above, but no individual favorite tracks saved in tonight's book." : "None of your saved favorite tracks are in tonight's catalog. Check out the other songs below!"}
                            </div>
                        ) : (
                            displayedTonightSongs.map(song => {
                                const titleText = song.title || song.label || ''
                                const favInfo = getFavoriteSong(song.artist, titleText)
                                const prefKey = favInfo?.preferred_key || 0

                                return (
                                    <div
                                        key={song.song_id || song.id || titleText}
                                        style={{
                                            background: 'var(--color-surface)',
                                            border: '1px solid rgba(234, 179, 8, 0.35)',
                                            borderRadius: 'var(--radius-lg)',
                                            display: 'flex',
                                            justifyContent: 'space-between',
                                            alignItems: 'center',
                                            padding: 'var(--space-3) var(--space-4)',
                                            marginBottom: 'var(--space-2)',
                                            gap: 'var(--space-2)',
                                            boxShadow: '0 2px 10px rgba(0,0,0,0.2)'
                                        }}
                                    >
                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)', minWidth: 0, flex: 1 }}>
                                            <button
                                                type="button"
                                                onClick={(e) => onToggleSongFavorite?.(e, song)}
                                                title="Remove from favorites"
                                                style={{
                                                    background: 'none',
                                                    border: 'none',
                                                    color: '#facc15',
                                                    fontSize: '1.25rem',
                                                    cursor: 'pointer',
                                                    padding: 0,
                                                    lineHeight: 1,
                                                    filter: 'drop-shadow(0 0 6px rgba(250, 204, 21, 0.6))',
                                                }}
                                            >
                                                ★
                                            </button>
                                            <div style={{ minWidth: 0 }}>
                                                <div style={{ fontWeight: 'bold', fontSize: 'var(--text-base)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                                    {titleText}
                                                </div>
                                                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', marginTop: '2px' }}>
                                                    <span style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                                        {song.artist}
                                                    </span>
                                                    {prefKey !== 0 && (event?.allow_key_change !== false) ? (
                                                        <span style={{
                                                            background: 'rgba(59, 130, 246, 0.2)',
                                                            color: '#93c5fd',
                                                            border: '1px solid rgba(59, 130, 246, 0.4)',
                                                            padding: '1px 6px',
                                                            borderRadius: 'var(--radius-sm)',
                                                            fontSize: '0.75em',
                                                            fontWeight: 'bold',
                                                        }}>
                                                            Key: {prefKey > 0 ? `+${prefKey}♯` : `${prefKey}♭`}
                                                        </span>
                                                    ) : (
                                                        <span style={{ fontSize: '0.75em', color: 'var(--color-text-dim)' }}>
                                                            Standard Pitch
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
                                                    onClick={() => handleRequestClick(song)}
                                                    class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
                                                    style={{
                                                        padding: 'var(--space-1) var(--space-3)',
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

                    {/* Section 3: Other Saved Favorites (Not in tonight's book) */}
                    {(displayedOffBookSongs.length > 0 || displayedOffBookArtists.length > 0) && (
                        <div style={{ marginTop: 'var(--space-6)' }}>
                            <div style={{
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'bold',
                                color: 'var(--color-text-dim)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.08em',
                                marginBottom: 'var(--space-2)',
                                display: 'flex',
                                alignItems: 'center',
                                gap: '6px'
                            }}>
                                <span>Other Saved Favorites ({displayedOffBookSongs.length + displayedOffBookArtists.length} not in tonight's catalog)</span>
                            </div>
                            <div style={{
                                background: 'rgba(255, 255, 255, 0.02)',
                                border: '1px dashed var(--color-border)',
                                borderRadius: 'var(--radius-lg)',
                                padding: 'var(--space-3) var(--space-4)',
                            }}>
                                {/* Off-book artists */}
                                {displayedOffBookArtists.map(favArtist => (
                                    <div
                                        key={`offbook-artist-${favArtist.normalized}`}
                                        style={{
                                            display: 'flex',
                                            justifyContent: 'space-between',
                                            alignItems: 'center',
                                            padding: 'var(--space-2) 0',
                                            borderBottom: '1px solid rgba(255, 255, 255, 0.04)',
                                            opacity: 0.7,
                                        }}
                                    >
                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                                            <button
                                                type="button"
                                                onClick={(e) => onToggleArtistFavorite?.(e, favArtist.display_name)}
                                                style={{ background: 'none', border: 'none', color: '#facc15', fontSize: '1rem', cursor: 'pointer', padding: 0 }}
                                                title="Remove from favorites"
                                            >
                                                ★
                                            </button>
                                            <div>
                                                <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                                    {favArtist.display_name}
                                                </div>
                                                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                                    Favorite Artist
                                                </div>
                                            </div>
                                        </div>
                                        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontStyle: 'italic' }}>
                                            No tracks in this book
                                        </span>
                                    </div>
                                ))}
                                {/* Off-book songs */}
                                {displayedOffBookSongs.map(fav => (
                                    <div 
                                        key={songKey(fav.artist, fav.title)}
                                        style={{
                                            display: 'flex',
                                            justifyContent: 'space-between',
                                            alignItems: 'center',
                                            padding: 'var(--space-2) 0',
                                            borderBottom: '1px solid rgba(255, 255, 255, 0.04)',
                                            opacity: 0.7,
                                        }}
                                    >
                                        <div>
                                            <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                                {fav.display_title || fav.title}
                                            </div>
                                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                                {fav.display_artist || fav.artist}
                                                {fav.preferred_key !== 0 && (
                                                    <span style={{ marginLeft: '6px', color: '#60a5fa' }}>
                                                        ({fav.preferred_key > 0 ? `+${fav.preferred_key}♯` : `${fav.preferred_key}♭`})
                                                    </span>
                                                )}
                                            </div>
                                        </div>
                                        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontStyle: 'italic' }}>
                                            Unavailable tonight
                                        </span>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}
