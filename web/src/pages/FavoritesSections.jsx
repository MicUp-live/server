import {
    songKey,
    clampPreferredKey,
    formatPreferredKey,
} from '../utils/favoritesStore'
import { FavoriteSongForm, FavoriteArtistForm } from './FavoritesForms'

export function FavoritesAddSection({ addMode, onAddSong, onAddArtist, onCancel }) {
    if (!addMode) return null

    if (addMode === 'song') {
        return (
            <section class="favorites-form-card" aria-label="Add favorite song">
                <FavoriteSongForm
                    heading="Add a Favorite Song"
                    submitLabel="Save Favorite Song"
                    idPrefix="add-fav-song"
                    onSubmit={onAddSong}
                    onCancel={() => onCancel('add-song')}
                />
            </section>
        )
    }

    return (
        <section class="favorites-form-card" aria-label="Add favorite artist">
            <FavoriteArtistForm
                heading="Add a Favorite Artist"
                submitLabel="Save Favorite Artist"
                idPrefix="add-fav-artist"
                onSubmit={onAddArtist}
                onCancel={() => onCancel('add-artist')}
            />
        </section>
    )
}

export function FavoritesToolbar({
    totalCount,
    songsCount,
    artistsCount,
    query,
    isSearching,
    activeFilter,
    onQueryChange,
    onClearQuery,
    onFilterChange,
}) {
    return (
        <div class="favorites-toolbar">
            <search>
                <div class="favorites-search-wrap">
                    <label htmlFor="favorites-search" class="sr-only">
                        Filter favorite songs and artists
                    </label>
                    <input
                        id="favorites-search"
                        name="favorites_search"
                        type="search"
                        class="favorites-search-input"
                        placeholder="Search your favorite songs or artists..."
                        value={query}
                        autocorrect="off"
                        spellcheck={false}
                        onInput={(e) => onQueryChange(e.target.value)}
                    />
                    {isSearching && (
                        <button
                            type="button"
                            class="favorites-search-clear"
                            aria-label="Clear search"
                            onClick={onClearQuery}
                        >
                            ✕
                        </button>
                    )}
                </div>
            </search>

            <fieldset
                class="favorites-tabs"
                aria-label="Filter favorites by type"
                style={{ border: 'none', padding: 0, margin: 0, minInlineSize: 0 }}
            >
                <button
                    type="button"
                    class="favorites-tab"
                    aria-pressed={activeFilter === 'all'}
                    onClick={() => onFilterChange('all')}
                >
                    All ({totalCount})
                </button>
                <button
                    type="button"
                    class="favorites-tab"
                    aria-pressed={activeFilter === 'songs'}
                    onClick={() => onFilterChange('songs')}
                >
                    Songs ({songsCount})
                </button>
                <button
                    type="button"
                    class="favorites-tab"
                    aria-pressed={activeFilter === 'artists'}
                    onClick={() => onFilterChange('artists')}
                >
                    Artists ({artistsCount})
                </button>
            </fieldset>
        </div>
    )
}

function getNoMatchesDescription(hiddenCategoryMatches, isSearching, activeFilter) {
    if (hiddenCategoryMatches > 0) {
        const noun = hiddenCategoryMatches === 1 ? 'item is' : 'items are'
        return `${hiddenCategoryMatches} matching ${noun} in another category.`
    }
    if (isSearching) {
        return 'Try a different search term or add it directly to your favorites.'
    }
    const itemLabel = activeFilter === 'songs' ? 'song' : 'artist'
    return `Add a favorite ${itemLabel} using the buttons above.`
}

function FavoritesEmptyRepertoire({ onOpenAddMode }) {
    return (
        <section class="favorites-empty">
            <div class="favorites-empty-icon" aria-hidden="true">⭐</div>
            <h2 class="favorites-empty-title">Your repertoire is empty</h2>
            <p class="favorites-empty-desc">
                Add your favorite karaoke tracks, artists, and pitch adjustments right here—or
                star songs anytime while browsing a show's songbook.
            </p>
            <div class="favorites-empty-actions">
                <button
                    type="button"
                    class="btn btn-primary favorites-action-btn"
                    onClick={() => onOpenAddMode('song')}
                >
                    + Add Your First Song
                </button>
                <button
                    type="button"
                    class="btn btn-secondary favorites-action-btn"
                    onClick={() => onOpenAddMode('artist')}
                >
                    + Add Favorite Artist
                </button>
            </div>
        </section>
    )
}

function FavoritesNoMatches({
    activeFilter,
    query,
    isSearching,
    hiddenCategoryMatches,
    onShowAll,
    onClearSearch,
}) {
    const title = isSearching
        ? `No favorites matching "${query.trim()}"`
        : `No favorite ${activeFilter} yet`
    const description = getNoMatchesDescription(hiddenCategoryMatches, isSearching, activeFilter)

    return (
        <section class="favorites-empty">
            <h2 class="favorites-empty-title">{title}</h2>
            <p class="favorites-empty-desc">{description}</p>
            <div class="favorites-empty-actions">
                {hiddenCategoryMatches > 0 && (
                    <button
                        type="button"
                        class="btn btn-primary favorites-action-btn"
                        onClick={onShowAll}
                    >
                        Show All Favorites
                    </button>
                )}
                {isSearching && (
                    <button
                        type="button"
                        class="btn btn-secondary favorites-action-btn"
                        onClick={onClearSearch}
                    >
                        Clear Search
                    </button>
                )}
            </div>
        </section>
    )
}

function FavoriteSongCard({
    song,
    idx,
    isEditing,
    setEditBtnRef,
    onStartEdit,
    onUpdateSong,
    onCancelEdit,
    onRemoveSong,
}) {
    const songArtist = song.artist || song.display_artist
    const songTitle = song.title || song.display_title
    const key = songKey(songArtist, songTitle)
    const displayTitle = song.display_title || song.title
    const displayArtist = song.display_artist || song.artist
    const prefKey = clampPreferredKey(song.preferred_key)

    if (isEditing) {
        return (
            <li class="favorites-card">
                <FavoriteSongForm
                    heading="Edit Favorite Song"
                    headingAs="h3"
                    submitLabel="Save Changes"
                    initialTitle={displayTitle}
                    initialArtist={displayArtist}
                    initialKey={prefKey}
                    idPrefix={`edit-song-${key.replace(/[^a-z0-9]/g, '-')}`}
                    onSubmit={(payload) => onUpdateSong(song, payload)}
                    onCancel={() => onCancelEdit(key)}
                />
            </li>
        )
    }

    return (
        <li class="favorites-card">
            <div class="favorites-card-row">
                <div class="favorites-card-main">
                    <span class="favorites-card-star" aria-hidden="true">★</span>
                    <div class="favorites-card-info">
                        <div class="favorites-card-title">{displayTitle}</div>
                        <div class="favorites-card-meta">
                            <span class="favorites-card-artist">{displayArtist}</span>
                            {prefKey !== 0 ? (
                                <span class="favorites-key-badge">
                                    Key: {formatPreferredKey(prefKey)}
                                </span>
                            ) : (
                                <span class="favorites-key-standard">
                                    Standard Pitch
                                </span>
                            )}
                        </div>
                    </div>
                </div>
                <div class="favorites-card-actions">
                    <button
                        ref={setEditBtnRef(`song:${key}`)}
                        type="button"
                        class="favorites-icon-btn"
                        aria-label={`Edit ${displayTitle} by ${displayArtist}`}
                        onClick={() => onStartEdit(key)}
                    >
                        ✏️ Edit
                    </button>
                    <button
                        type="button"
                        class="favorites-icon-btn danger"
                        aria-label={`Remove ${displayTitle} by ${displayArtist} from favorites`}
                        onClick={() => onRemoveSong(song, idx)}
                    >
                        ✕ Remove
                    </button>
                </div>
            </div>
        </li>
    )
}

function FavoriteSongsSection({
    songs,
    editingSongKey,
    setEditBtnRef,
    onStartEdit,
    onUpdateSong,
    onCancelEdit,
    onRemoveSong,
}) {
    if (songs.length === 0) return null

    return (
        <section class="favorites-section" aria-labelledby="favorites-songs-heading">
            <h2 id="favorites-songs-heading" class="favorites-section-title">
                <span aria-hidden="true">★ </span>Favorite Songs ({songs.length})
            </h2>
            <ul class="favorites-list">
                {songs.map((song, idx) => {
                    const key = songKey(
                        song.artist || song.display_artist,
                        song.title || song.display_title
                    )
                    return (
                        <FavoriteSongCard
                            key={key}
                            song={song}
                            idx={idx}
                            isEditing={editingSongKey === key}
                            setEditBtnRef={setEditBtnRef}
                            onStartEdit={onStartEdit}
                            onUpdateSong={onUpdateSong}
                            onCancelEdit={onCancelEdit}
                            onRemoveSong={onRemoveSong}
                        />
                    )
                })}
            </ul>
        </section>
    )
}

function FavoriteArtistCard({
    artist,
    idx,
    isEditing,
    setEditBtnRef,
    onStartEdit,
    onUpdateArtist,
    onCancelEdit,
    onRemoveArtist,
}) {
    const artistId = artist.normalized || artist.display_name
    const displayName = artist.display_name || artist.normalized

    if (isEditing) {
        return (
            <li class="favorites-card">
                <FavoriteArtistForm
                    heading="Edit Favorite Artist"
                    headingAs="h3"
                    submitLabel="Save Changes"
                    initialName={displayName}
                    idPrefix={`edit-artist-${String(artistId || '').replace(/[^a-z0-9]/g, '-')}`}
                    onSubmit={(nextName) => onUpdateArtist(artistId, nextName)}
                    onCancel={() => onCancelEdit(artistId)}
                />
            </li>
        )
    }

    return (
        <li class="favorites-card">
            <div class="favorites-card-row">
                <div class="favorites-card-main">
                    <span class="favorites-card-star" aria-hidden="true">★</span>
                    <div class="favorites-card-info">
                        <div class="favorites-card-title">{displayName}</div>
                        <div class="favorites-card-meta">
                            <span class="favorites-artist-badge">Favorite Artist</span>
                        </div>
                    </div>
                </div>
                <div class="favorites-card-actions">
                    <button
                        ref={setEditBtnRef(`artist:${artistId}`)}
                        type="button"
                        class="favorites-icon-btn"
                        aria-label={`Edit favorite artist ${displayName}`}
                        onClick={() => onStartEdit(artistId)}
                    >
                        ✏️ Edit
                    </button>
                    <button
                        type="button"
                        class="favorites-icon-btn danger"
                        aria-label={`Remove ${displayName} from favorite artists`}
                        onClick={() => onRemoveArtist(artist, idx)}
                    >
                        ✕ Remove
                    </button>
                </div>
            </div>
        </li>
    )
}

function FavoriteArtistsSection({
    artists,
    editingArtistNorm,
    setEditBtnRef,
    onStartEdit,
    onUpdateArtist,
    onCancelEdit,
    onRemoveArtist,
}) {
    if (artists.length === 0) return null

    return (
        <section class="favorites-section" aria-labelledby="favorites-artists-heading">
            <h2 id="favorites-artists-heading" class="favorites-section-title">
                <span aria-hidden="true">★ </span>Favorite Artists ({artists.length})
            </h2>
            <ul class="favorites-list">
                {artists.map((artist, idx) => {
                    const artistId = artist.normalized || artist.display_name
                    return (
                        <FavoriteArtistCard
                            key={artistId}
                            artist={artist}
                            idx={idx}
                            isEditing={editingArtistNorm === artistId}
                            setEditBtnRef={setEditBtnRef}
                            onStartEdit={onStartEdit}
                            onUpdateArtist={onUpdateArtist}
                            onCancelEdit={onCancelEdit}
                            onRemoveArtist={onRemoveArtist}
                        />
                    )
                })}
            </ul>
        </section>
    )
}

export function FavoritesContent({
    totalCount,
    noVisibleMatches,
    activeFilter,
    query,
    isSearching,
    hiddenCategoryMatches,
    showSongs,
    showArtists,
    filteredSongs,
    filteredArtists,
    editingSongKey,
    editingArtistNorm,
    setEditBtnRef,
    onOpenAddMode,
    onShowAll,
    onClearSearch,
    onStartEditSong,
    onUpdateSong,
    onCancelEditSong,
    onRemoveSong,
    onStartEditArtist,
    onUpdateArtist,
    onCancelEditArtist,
    onRemoveArtist,
}) {
    if (totalCount === 0) {
        return <FavoritesEmptyRepertoire onOpenAddMode={onOpenAddMode} />
    }

    if (noVisibleMatches) {
        return (
            <FavoritesNoMatches
                activeFilter={activeFilter}
                query={query}
                isSearching={isSearching}
                hiddenCategoryMatches={hiddenCategoryMatches}
                onShowAll={onShowAll}
                onClearSearch={onClearSearch}
            />
        )
    }

    return (
        <>
            {showSongs && (
                <FavoriteSongsSection
                    songs={filteredSongs}
                    editingSongKey={editingSongKey}
                    setEditBtnRef={setEditBtnRef}
                    onStartEdit={onStartEditSong}
                    onUpdateSong={onUpdateSong}
                    onCancelEdit={onCancelEditSong}
                    onRemoveSong={onRemoveSong}
                />
            )}
            {showArtists && (
                <FavoriteArtistsSection
                    artists={filteredArtists}
                    editingArtistNorm={editingArtistNorm}
                    setEditBtnRef={setEditBtnRef}
                    onStartEdit={onStartEditArtist}
                    onUpdateArtist={onUpdateArtist}
                    onCancelEdit={onCancelEditArtist}
                    onRemoveArtist={onRemoveArtist}
                />
            )}
        </>
    )
}
