import { LetterBar } from '../../../components/LetterBar.jsx'

export function CatalogControls({
    viewMode = 'songs',
    query = '',
    onQueryChange,
    onClearQuery,
    isSearching = false,
    catalogFavoriteArtists = [],
    activeArtistFilter = null,
    onSelectArtistFilter,
    activeLetter = 'A',
    letterCounts = {},
    onSelectLetter,
    activeArtistLetter = 'A',
    artistLetterCounts = {},
    onSelectArtistLetter,
    displaySongsCount = 0,
    totalSongs = 0,
    artistGroupsCount = 0,
    uniqueArtistsCount = 0,
    tonightFavoriteSongsCount = 0,
    displayedTonightSongsCount = 0,
    displayedFavoriteArtistsCount = 0,
    onExpandAllArtists,
    onCollapseAllArtists,
}) {
    return (
        <div style={{ flexShrink: 0, display: 'flex', flexDirection: 'column' }}>
            {/* Quick Favorite Artist Filter Chips */}
            {catalogFavoriteArtists.length > 0 && viewMode !== 'favorites' && (
                <div style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--space-2)',
                    overflowX: 'auto',
                    paddingBottom: 'var(--space-2)',
                    marginBottom: 'var(--space-2)',
                    flexShrink: 0,
                    WebkitOverflowScrolling: 'touch',
                    scrollbarWidth: 'none',
                }}>
                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', whiteSpace: 'nowrap', fontWeight: 'bold' }}>
                        Favorites:
                    </span>
                    <button
                        type="button"
                        onClick={() => onSelectArtistFilter(null)}
                        style={{
                            background: !activeArtistFilter ? 'var(--color-accent)' : 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            color: !activeArtistFilter ? '#fff' : 'var(--color-text-muted)',
                            borderRadius: 'var(--radius-full)',
                            padding: '2px 10px',
                            fontSize: 'var(--text-xs)',
                            fontWeight: !activeArtistFilter ? 'bold' : 'normal',
                            cursor: 'pointer',
                            whiteSpace: 'nowrap',
                        }}
                    >
                        All
                    </button>
                    {catalogFavoriteArtists.map(fa => {
                        const isSelected = activeArtistFilter === fa.display_name
                        return (
                            <button
                                key={fa.normalized}
                                type="button"
                                onClick={() => onSelectArtistFilter(isSelected ? null : fa.display_name)}
                                style={{
                                    background: isSelected ? 'rgba(234, 179, 8, 0.25)' : 'var(--color-surface-alt)',
                                    border: `1px solid ${isSelected ? '#eab308' : 'var(--color-border)'}`,
                                    color: isSelected ? '#facc15' : 'var(--color-text-muted)',
                                    borderRadius: 'var(--radius-full)',
                                    padding: '2px 10px',
                                    fontSize: 'var(--text-xs)',
                                    fontWeight: isSelected ? 'bold' : 'normal',
                                    cursor: 'pointer',
                                    whiteSpace: 'nowrap',
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    gap: '4px',
                                }}
                            >
                                <span>★ {fa.display_name}</span>
                                <span style={{ opacity: 0.75 }}>({fa.songCount})</span>
                            </button>
                        )
                    })}
                </div>
            )}

            {/* Search Bar */}
            <div style={{ position: 'relative', marginBottom: 'var(--space-3)', flexShrink: 0 }}>
                <label for="catalog-search" class="sr-only">Search songs</label>
                <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
                    <span style={{ position: 'absolute', left: 'var(--space-3)', color: 'var(--color-text-dim)', pointerEvents: 'none', fontSize: 'var(--text-sm)' }}>
                        🔍
                    </span>
                    <input
                        id="catalog-search"
                        type="text"
                        placeholder={viewMode === 'artists' ? "Filter artists or anthems..." : (viewMode === 'favorites' ? "Search your favorites..." : "Find your anthem (title or artist)...")}
                        value={query}
                        onInput={(e) => onQueryChange(e.target.value)}
                        style={{
                            width: '100%',
                            fontSize: 'var(--text-base)',
                            paddingLeft: '2.5rem',
                            paddingRight: query ? '2.75rem' : 'var(--space-4)',
                            minHeight: 'var(--touch-target-min, 48px)',
                            background: 'var(--color-surface-alt)',
                            borderRadius: 'var(--radius-lg)',
                            border: '1px solid var(--color-border)',
                        }}
                    />
                    {query && (
                        <button
                            type="button"
                            onClick={onClearQuery}
                            aria-label="Clear search"
                            style={{
                                position: 'absolute',
                                right: 'var(--space-2)',
                                background: 'none',
                                border: 'none',
                                color: 'var(--color-text-muted)',
                                fontSize: 'var(--text-base)',
                                cursor: 'pointer',
                                width: '40px',
                                height: '40px',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                borderRadius: 'var(--radius-md)',
                            }}
                        >
                            ✕
                        </button>
                    )}
                </div>
            </div>

            {/* Alphabet Letter Bars */}
            {viewMode === 'songs' && !isSearching && (
                <LetterBar
                    activeLetter={activeLetter}
                    letterCounts={letterCounts}
                    onSelectLetter={onSelectLetter}
                    itemType="songs"
                />
            )}

            {viewMode === 'artists' && !isSearching && (
                <LetterBar
                    activeLetter={activeArtistLetter}
                    letterCounts={artistLetterCounts}
                    onSelectLetter={onSelectArtistLetter}
                    itemType="artists"
                />
            )}

            {/* List Header Counts */}
            <div style={{ flexShrink: 0, marginBottom: 'var(--space-2)', fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div>
                    {viewMode === 'songs' && (
                        isSearching
                            ? `${displaySongsCount} results`
                            : (activeLetter ? `${displaySongsCount} ${displaySongsCount === 1 ? 'song' : 'songs'} in ${activeLetter}` : `${totalSongs} songs`)
                    )}
                    {viewMode === 'artists' && (isSearching
                        ? `${artistGroupsCount} results`
                        : (uniqueArtistsCount !== null ? `${uniqueArtistsCount} artists` : 'Loading artists...'))}
                    {viewMode === 'favorites' && (
                        isSearching 
                            ? `${displayedTonightSongsCount + displayedFavoriteArtistsCount} results` 
                            : `${tonightFavoriteSongsCount} ${tonightFavoriteSongsCount === 1 ? 'song' : 'songs'} • ${catalogFavoriteArtists.length} ${catalogFavoriteArtists.length === 1 ? 'artist' : 'artists'} ready to sing tonight`
                    )}
                    {activeArtistFilter && (
                        <span style={{ marginLeft: 'var(--space-2)', color: '#facc15' }}>
                            (filtered by ★ {activeArtistFilter})
                        </span>
                    )}
                </div>

                {viewMode === 'artists' && artistGroupsCount > 0 && (
                    <div style={{ display: 'flex', gap: 'var(--space-2)', fontSize: 'var(--text-xs)' }}>
                        <button
                            type="button"
                            onClick={onExpandAllArtists}
                            style={{ background: 'none', border: 'none', color: 'var(--color-accent)', cursor: 'pointer', padding: 0 }}
                        >
                            Expand All
                        </button>
                        <span>•</span>
                        <button
                            type="button"
                            onClick={onCollapseAllArtists}
                            style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', cursor: 'pointer', padding: 0 }}
                        >
                            Collapse All
                        </button>
                    </div>
                )}
            </div>
        </div>
    )
}
