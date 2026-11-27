import { LetterBar } from '../../../components/LetterBar.jsx'

const SEARCH_PLACEHOLDERS = {
    artists: 'Filter artists or anthems...',
    favorites: 'Search your favorites...',
}

function getSearchPlaceholder(viewMode) {
    return SEARCH_PLACEHOLDERS[viewMode] || 'Find your anthem (title or artist)...'
}

function pluralize(count, singular, plural) {
    return count === 1 ? singular : plural
}

function getSongsCountLabel({ isSearching, displaySongsCount, activeLetter, totalSongs }) {
    if (isSearching) {
        return `${displaySongsCount} results`
    }
    if (activeLetter) {
        return `${displaySongsCount} ${pluralize(displaySongsCount, 'song', 'songs')} in ${activeLetter}`
    }
    return `${totalSongs} songs`
}

function getArtistsCountLabel({ isSearching, artistGroupsCount, uniqueArtistsCount }) {
    if (isSearching) {
        return `${artistGroupsCount} results`
    }
    if (uniqueArtistsCount !== null) {
        return `${uniqueArtistsCount} artists`
    }
    return 'Loading artists...'
}

function getFavoritesCountLabel({
    isSearching,
    displayedTonightSongsCount,
    displayedFavoriteArtistsCount,
    tonightFavoriteSongsCount,
    favoriteArtistsCount,
}) {
    if (isSearching) {
        return `${displayedTonightSongsCount + displayedFavoriteArtistsCount} results`
    }
    const songWord = pluralize(tonightFavoriteSongsCount, 'song', 'songs')
    const artistWord = pluralize(favoriteArtistsCount, 'artist', 'artists')
    return `${tonightFavoriteSongsCount} ${songWord} • ${favoriteArtistsCount} ${artistWord} ready to sing tonight`
}

function getCountSummary(props) {
    if (props.viewMode === 'songs') {
        return getSongsCountLabel(props)
    }
    if (props.viewMode === 'artists') {
        return getArtistsCountLabel(props)
    }
    if (props.viewMode === 'favorites') {
        return getFavoritesCountLabel({
            ...props,
            favoriteArtistsCount: props.catalogFavoriteArtists.length,
        })
    }
    return null
}

function FavoriteArtistChip({ artist, isSelected, onSelect }) {
    const selectedStyle = isSelected
        ? {
            background: 'rgba(234, 179, 8, 0.25)',
            border: '1px solid #eab308',
            color: '#facc15',
            fontWeight: 'bold',
        }
        : {
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-muted)',
            fontWeight: 'normal',
        }

    return (
        <button
            type="button"
            onClick={() => onSelect(isSelected ? null : artist.display_name)}
            style={{
                ...selectedStyle,
                borderRadius: 'var(--radius-full)',
                padding: '2px 10px',
                fontSize: 'var(--text-xs)',
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
            }}
        >
            <span>★ {artist.display_name}</span>
            <span style={{ opacity: 0.75 }}>({artist.songCount})</span>
        </button>
    )
}

function FavoriteArtistChips({ catalogFavoriteArtists, activeArtistFilter, onSelectArtistFilter }) {
    if (catalogFavoriteArtists.length === 0) {
        return null
    }

    const isAllSelected = !activeArtistFilter
    const allChipStyle = isAllSelected
        ? {
            background: 'var(--color-accent)',
            color: '#fff',
            fontWeight: 'bold',
        }
        : {
            background: 'var(--color-surface-alt)',
            color: 'var(--color-text-muted)',
            fontWeight: 'normal',
        }

    return (
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
                    ...allChipStyle,
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-full)',
                    padding: '2px 10px',
                    fontSize: 'var(--text-xs)',
                    cursor: 'pointer',
                    whiteSpace: 'nowrap',
                }}
            >
                All
            </button>
            {catalogFavoriteArtists.map(fa => (
                <FavoriteArtistChip
                    key={fa.normalized}
                    artist={fa}
                    isSelected={activeArtistFilter === fa.display_name}
                    onSelect={onSelectArtistFilter}
                />
            ))}
        </div>
    )
}

function CatalogLetterBar({
    viewMode,
    isSearching,
    activeLetter,
    letterCounts,
    onSelectLetter,
    activeArtistLetter,
    artistLetterCounts,
    onSelectArtistLetter,
}) {
    if (isSearching) {
        return null
    }
    if (viewMode === 'songs') {
        return (
            <LetterBar
                activeLetter={activeLetter}
                letterCounts={letterCounts}
                onSelectLetter={onSelectLetter}
                itemType="songs"
            />
        )
    }
    if (viewMode === 'artists') {
        return (
            <LetterBar
                activeLetter={activeArtistLetter}
                letterCounts={artistLetterCounts}
                onSelectLetter={onSelectArtistLetter}
                itemType="artists"
            />
        )
    }
    return null
}

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
    const countSummary = getCountSummary({
        viewMode,
        isSearching,
        displaySongsCount,
        activeLetter,
        totalSongs,
        artistGroupsCount,
        uniqueArtistsCount,
        displayedTonightSongsCount,
        displayedFavoriteArtistsCount,
        tonightFavoriteSongsCount,
        catalogFavoriteArtists,
    })

    return (
        <div style={{ flexShrink: 0, display: 'flex', flexDirection: 'column' }}>
            {/* Quick Favorite Artist Filter Chips */}
            {viewMode !== 'favorites' && (
                <FavoriteArtistChips
                    catalogFavoriteArtists={catalogFavoriteArtists}
                    activeArtistFilter={activeArtistFilter}
                    onSelectArtistFilter={onSelectArtistFilter}
                />
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
                        placeholder={getSearchPlaceholder(viewMode)}
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
            <CatalogLetterBar
                viewMode={viewMode}
                isSearching={isSearching}
                activeLetter={activeLetter}
                letterCounts={letterCounts}
                onSelectLetter={onSelectLetter}
                activeArtistLetter={activeArtistLetter}
                artistLetterCounts={artistLetterCounts}
                onSelectArtistLetter={onSelectArtistLetter}
            />

            {/* List Header Counts */}
            <div style={{ flexShrink: 0, marginBottom: 'var(--space-2)', fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div>
                    {countSummary}
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
