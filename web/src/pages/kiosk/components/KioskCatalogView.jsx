import { LetterBar } from '../../../components/LetterBar.jsx'
import { Spinner } from '../../../components/Spinner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

function getCountSummary({
    viewMode,
    isSearching,
    displaySongsCount,
    catalogTotal,
    artistGroupsCount,
    uniqueArtistsCount,
}) {
    if (viewMode === 'songs') {
        return isSearching ? `${displaySongsCount} results` : `${catalogTotal} songs`
    }
    if (isSearching) {
        return `${artistGroupsCount} results`
    }
    return uniqueArtistsCount !== null ? `${uniqueArtistsCount} artists` : 'Loading artists...'
}

function ViewModeTab({ active, label, onClick }) {
    return (
        <button
            type="button"
            onClick={onClick}
            style={{
                padding: 'var(--space-2) var(--space-4)',
                fontSize: 'var(--text-sm)',
                fontWeight: active ? 'var(--font-bold)' : 'var(--font-semibold)',
                color: active ? 'var(--color-accent)' : 'var(--color-text-muted)',
                background: 'none',
                border: 'none',
                borderBottom: active ? '2px solid var(--color-accent)' : '2px solid transparent',
                cursor: 'pointer',
                transition: 'all 0.15s ease',
            }}
        >
            {label}
        </button>
    )
}

function RequestButton({ statusInfo, compact = false }) {
    if (statusInfo.isEnded) return null
    const compactStyle = compact ? { fontSize: 'var(--text-xs)', padding: '4px 10px' } : undefined
    return (
        <button
            type="button"
            aria-disabled={statusInfo.isPaused ? 'true' : undefined}
            class={`btn btn-sm ${statusInfo.isPaused ? 'btn-secondary' : 'btn-primary'}`}
            style={{ pointerEvents: 'none', opacity: statusInfo.isPaused ? 0.75 : 1, ...compactStyle }}
        >
            {statusInfo.requestButtonLabel}
        </button>
    )
}

function EmptyCatalogState({ isSearching, query, onClearQuery, searchEmptyText, browseEmptyText }) {
    return (
        <div style={{ textAlign: 'center', padding: 'var(--space-12)', color: 'var(--color-text-dim)' }}>
            {isSearching ? (
                <>
                    <p style={{ fontSize: 'var(--text-lg)' }}>{searchEmptyText(query)}</p>
                    <button type="button" class="btn btn-secondary" onClick={onClearQuery}>Clear Search</button>
                </>
            ) : (
                <p>{browseEmptyText}</p>
            )}
        </div>
    )
}

function KioskSongList({
    songListRef,
    loadingSongs,
    displaySongs,
    statusInfo,
    onSelectSong,
    isSearching,
    query,
    onClearQuery,
}) {
    return (
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
                            <RequestButton statusInfo={statusInfo} />
                        </div>
                    ))}
                    {displaySongs.length === 0 && (
                        <EmptyCatalogState
                            isSearching={isSearching}
                            query={query}
                            onClearQuery={onClearQuery}
                            searchEmptyText={(q) => `No songs found matching "${q}"`}
                            browseEmptyText="No songs in this letter"
                        />
                    )}
                </div>
            )}
        </div>
    )
}

function KioskArtistGroupCard({
    group,
    isExpanded,
    groupSongs,
    isLoadingSongs,
    statusInfo,
    onToggleArtistExpand,
    onSelectSong,
}) {
    return (
        <div style={{ background: 'var(--color-surface)', borderRadius: 'var(--radius-md)', border: '1px solid var(--color-border)', overflow: 'hidden' }}>
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
                    {isLoadingSongs && <Spinner size="sm" />}
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
                            <RequestButton statusInfo={statusInfo} compact />
                        </div>
                    ))}
                    {groupSongs.length === 0 && !isLoadingSongs && (
                        <div style={{ padding: 'var(--space-3) var(--space-4)', color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)' }}>
                            Loading songs...
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}

function KioskArtistList({
    artistListRef,
    artistGroups,
    expandedArtists,
    expandedArtistSongs,
    loadingArtistSongs,
    statusInfo,
    onToggleArtistExpand,
    onSelectSong,
    isSearching,
    query,
    onClearQuery,
}) {
    return (
        <div ref={artistListRef} style={{ flex: 1, overflowY: 'auto', padding: '0 var(--space-6) var(--space-4)' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                {artistGroups.map((group) => {
                    const isExpanded = expandedArtists.has(group.norm)
                    const groupSongs = isExpanded
                        ? (expandedArtistSongs[group.norm] || group.songs)
                        : group.songs
                    return (
                        <KioskArtistGroupCard
                            key={group.norm}
                            group={group}
                            isExpanded={isExpanded}
                            groupSongs={groupSongs}
                            isLoadingSongs={Boolean(loadingArtistSongs[group.norm])}
                            statusInfo={statusInfo}
                            onToggleArtistExpand={onToggleArtistExpand}
                            onSelectSong={onSelectSong}
                        />
                    )
                })}
                {artistGroups.length === 0 && (
                    <EmptyCatalogState
                        isSearching={isSearching}
                        query={query}
                        onClearQuery={onClearQuery}
                        searchEmptyText={(q) => `No artists found for "${q}"`}
                        browseEmptyText="No artists in this letter"
                    />
                )}
            </div>
        </div>
    )
}

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
    const artistTabCount = uniqueArtistsCount !== null ? ` (${uniqueArtistsCount})` : ''
    const countSummary = getCountSummary({
        viewMode,
        isSearching,
        displaySongsCount: displaySongs.length,
        catalogTotal,
        artistGroupsCount: artistGroups.length,
        uniqueArtistsCount,
    })

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
                <ViewModeTab
                    active={viewMode === 'songs'}
                    label={`🎵 Songs (${catalogTotal})`}
                    onClick={() => onSelectViewMode('songs')}
                />
                <ViewModeTab
                    active={viewMode === 'artists'}
                    label={`🎤 By Artist${artistTabCount}`}
                    onClick={() => onSelectViewMode('artists')}
                />
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
                {countSummary}
            </div>

            {/* Song list */}
            {viewMode === 'songs' && (
                <KioskSongList
                    songListRef={songListRef}
                    loadingSongs={loadingSongs}
                    displaySongs={displaySongs}
                    statusInfo={statusInfo}
                    onSelectSong={onSelectSong}
                    isSearching={isSearching}
                    query={query}
                    onClearQuery={onClearQuery}
                />
            )}

            {/* Artist list */}
            {viewMode === 'artists' && (
                <KioskArtistList
                    artistListRef={artistListRef}
                    artistGroups={artistGroups}
                    expandedArtists={expandedArtists}
                    expandedArtistSongs={expandedArtistSongs}
                    loadingArtistSongs={loadingArtistSongs}
                    statusInfo={statusInfo}
                    onToggleArtistExpand={onToggleArtistExpand}
                    onSelectSong={onSelectSong}
                    isSearching={isSearching}
                    query={query}
                    onClearQuery={onClearQuery}
                />
            )}
        </div>
    )
}
