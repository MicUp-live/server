import { LetterBar } from '../../../components/LetterBar.jsx'
import { Spinner } from '../../../components/Spinner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function KioskCatalogView({
    eventStatus,
    viewMode = 'songs',
    onSelectViewMode,
    catalogTotal = 0,
    uniqueArtistsCount = 0,
    indexing = false,
    loadingSongs = false,
    activeLetter = 'A',
    letterCounts = {},
    onSelectLetter,
    activeArtistLetter = 'A',
    artistLetterCounts = {},
    onSelectArtistLetter,
    isSearching = false,
    displaySongs = [],
    artistGroups = [],
    expandedArtists = new Set(),
    expandedArtistSongs = {},
    loadingArtistSongs = {},
    query = '',
    onClearQuery,
    onSelectSong,
    onToggleArtistExpand,
    songListRef,
    artistListRef,
}) {
    const statusInfo = getShowStatusInfo(eventStatus)

    return (
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, overflow: 'hidden' }}>
            {/* View Mode Tabs: Songs | By Artist */}
            <div style={{
                display: 'flex',
                background: 'var(--color-surface-alt)',
                borderBottom: '1px solid var(--color-border)',
                padding: '8px var(--space-6) 0',
                gap: '4px',
                flexShrink: 0,
            }}>
                <button
                    type="button"
                    onClick={() => onSelectViewMode('songs')}
                    style={{
                        padding: 'var(--space-2) var(--space-4)',
                        fontSize: 'var(--text-sm)',
                        fontWeight: viewMode === 'songs' ? 'var(--font-bold)' : 'var(--font-semibold)',
                        color: viewMode === 'songs' ? 'var(--color-accent)' : 'var(--color-text-muted)',
                        background: 'none',
                        border: 'none',
                        borderBottom: viewMode === 'songs' ? '2px solid var(--color-accent)' : '2px solid transparent',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                    }}
                >
                    🎵 Songs ({catalogTotal})
                </button>
                <button
                    type="button"
                    onClick={() => onSelectViewMode('artists')}
                    style={{
                        padding: 'var(--space-2) var(--space-4)',
                        fontSize: 'var(--text-sm)',
                        fontWeight: viewMode === 'artists' ? 'var(--font-bold)' : 'var(--font-semibold)',
                        color: viewMode === 'artists' ? 'var(--color-accent)' : 'var(--color-text-muted)',
                        background: 'none',
                        border: 'none',
                        borderBottom: viewMode === 'artists' ? '2px solid var(--color-accent)' : '2px solid transparent',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                    }}
                >
                    🎤 By Artist{uniqueArtistsCount !== null ? ` (${uniqueArtistsCount})` : ''}
                </button>
                <div style={{ flex: 1 }} />
                {indexing && <Spinner size="sm" />}
            </div>

            {/* Letter Bar */}
            {!isSearching && viewMode === 'songs' && (
                <LetterBar
                    activeLetter={activeLetter}
                    letterCounts={letterCounts}
                    onSelectLetter={onSelectLetter}
                    itemType="songs"
                />
            )}
            {!isSearching && viewMode === 'artists' && (
                <LetterBar
                    activeLetter={activeArtistLetter}
                    letterCounts={artistLetterCounts}
                    onSelectLetter={onSelectArtistLetter}
                    itemType="artists"
                />
            )}

            {/* Count row */}
            <div style={{ padding: '4px var(--space-6)', fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', flexShrink: 0 }}>
                {viewMode === 'songs' && (isSearching ? `${displaySongs.length} results` : `${catalogTotal} songs`)}
                {viewMode === 'artists' && (isSearching
                    ? `${artistGroups.length} results`
                    : (uniqueArtistsCount !== null ? `${uniqueArtistsCount} artists` : 'Loading artists...'))}
            </div>

            {/* Song list */}
            {viewMode === 'songs' && (
                <div ref={songListRef} style={{ flex: 1, overflowY: 'auto', padding: '0 var(--space-6) var(--space-4)' }}>
                    {loadingSongs ? (
                        <div style={{ padding: 'var(--space-8)', textAlign: 'center' }}>
                            <Spinner label="Loading..." />
                        </div>
                    ) : (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                            {displaySongs.map((song, i) => (
                                <div
                                    key={song.song_id || song.id || `${song.artist}-${song.title}-${i}`}
                                    onClick={statusInfo.isEnded ? undefined : () => onSelectSong(song)}
                                    style={{
                                        display: 'flex',
                                        justifyContent: 'space-between',
                                        alignItems: 'center',
                                        padding: 'var(--space-3) var(--space-4)',
                                        background: 'var(--color-surface)',
                                        borderRadius: 'var(--radius-md)',
                                        border: '1px solid var(--color-border)',
                                        cursor: statusInfo.isEnded ? 'default' : 'pointer',
                                        transition: 'all var(--transition-fast)',
                                    }}
                                >
                                    <div>
                                        <div style={{ fontWeight: 'var(--font-semibold)', fontSize: 'var(--text-base)', color: 'var(--color-text)' }}>
                                            {song.title || song.label}
                                        </div>
                                        <div style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)' }}>
                                            {song.artist}
                                        </div>
                                    </div>
                                    {!statusInfo.isEnded && (
                                        <button
                                            type="button"
                                            aria-disabled={statusInfo.isPaused ? 'true' : undefined}
                                            class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
                                            style={{ pointerEvents: 'none', opacity: statusInfo.isPaused ? 0.75 : 1 }}
                                        >
                                            {statusInfo.requestButtonLabel}
                                        </button>
                                    )}
                                </div>
                            ))}
                            {displaySongs.length === 0 && (
                                <div style={{ textAlign: 'center', padding: 'var(--space-12)', color: 'var(--color-text-dim)' }}>
                                    {isSearching
                                        ? (
                                            <>
                                                <p style={{ fontSize: 'var(--text-lg)' }}>No songs found matching "{query}"</p>
                                                <button type="button" class="btn btn-secondary" onClick={onClearQuery}>Clear Search</button>
                                            </>
                                        )
                                        : <p>No songs in this letter</p>
                                    }
                                </div>
                            )}
                        </div>
                    )}
                </div>
            )}

            {/* Artist list */}
            {viewMode === 'artists' && (
                <div ref={artistListRef} style={{ flex: 1, overflowY: 'auto', padding: '0 var(--space-6) var(--space-4)' }}>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                        {artistGroups.map((group) => {
                            const isExpanded = expandedArtists.has(group.norm)
                            const groupSongs = isExpanded
                                ? (expandedArtistSongs[group.norm] || group.songs)
                                : group.songs
                            return (
                                <div key={group.norm} style={{ background: 'var(--color-surface)', borderRadius: 'var(--radius-md)', border: '1px solid var(--color-border)', overflow: 'hidden' }}>
                                    <div
                                        onClick={() => onToggleArtistExpand(group.norm)}
                                        style={{
                                            display: 'flex',
                                            justifyContent: 'space-between',
                                            alignItems: 'center',
                                            padding: 'var(--space-3) var(--space-4)',
                                            cursor: 'pointer',
                                        }}
                                    >
                                        <div>
                                            <div style={{ fontWeight: 'var(--font-semibold)', fontSize: 'var(--text-base)', color: 'var(--color-text)' }}>
                                                {group.artist}
                                            </div>
                                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                                                {group.count} {group.count === 1 ? 'song' : 'songs'}
                                            </div>
                                        </div>
                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                                            {loadingArtistSongs[group.norm] && <Spinner size="sm" />}
                                            <span style={{ color: 'var(--color-text-dim)', fontSize: '1.2rem' }}>
                                                {isExpanded ? '▲' : '▼'}
                                            </span>
                                        </div>
                                    </div>
                                    {isExpanded && (
                                        <div style={{ borderTop: '1px solid var(--color-border)', display: 'flex', flexDirection: 'column' }}>
                                            {groupSongs.map((song, si) => (
                                                <div
                                                    key={song.song_id || song.id || `${song.artist}-${song.title}-${si}`}
                                                    onClick={statusInfo.isEnded ? undefined : () => onSelectSong(song)}
                                                    style={{
                                                        display: 'flex',
                                                        justifyContent: 'space-between',
                                                        alignItems: 'center',
                                                        padding: 'var(--space-2) var(--space-4)',
                                                        borderBottom: si < groupSongs.length - 1 ? '1px solid var(--color-border)' : 'none',
                                                        cursor: statusInfo.isEnded ? 'default' : 'pointer',
                                                        background: 'rgba(0,0,0,0.15)',
                                                    }}
                                                >
                                                    <span style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text)' }}>
                                                        {song.title || song.label}
                                                    </span>
                                                    {!statusInfo.isEnded && (
                                                        <button
                                                            type="button"
                                                            aria-disabled={statusInfo.isPaused ? 'true' : undefined}
                                                            class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
                                                            style={{ pointerEvents: 'none', fontSize: 'var(--text-xs)', padding: '4px 10px', opacity: statusInfo.isPaused ? 0.75 : 1 }}
                                                        >
                                                            {statusInfo.requestButtonLabel}
                                                        </button>
                                                    )}
                                                </div>
                                            ))}
                                            {isExpanded && groupSongs.length === 0 && !loadingArtistSongs[group.norm] && (
                                                <div style={{ padding: 'var(--space-3) var(--space-4)', color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)' }}>
                                                    Loading songs...
                                                </div>
                                            )}
                                        </div>
                                    )}
                                </div>
                            )
                        })}
                        {artistGroups.length === 0 && (
                            <div style={{ textAlign: 'center', padding: 'var(--space-12)', color: 'var(--color-text-dim)' }}>
                                {isSearching
                                    ? (
                                        <>
                                            <p style={{ fontSize: 'var(--text-lg)' }}>No artists found for "{query}"</p>
                                            <button type="button" class="btn btn-secondary" onClick={onClearQuery}>Clear Search</button>
                                        </>
                                    )
                                    : <p>No artists in this letter</p>
                                }
                            </div>
                        )}
                    </div>
                </div>
            )}
        </div>
    )
}
