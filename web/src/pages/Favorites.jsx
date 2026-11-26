import { useState, useEffect, useMemo, useRef } from 'preact/hooks'
import {
    FAVORITE_SONGS_STORAGE_KEY,
    FAVORITE_ARTISTS_STORAGE_KEY,
    songKey,
    clampPreferredKey,
    getFavoriteSongs,
    getFavoriteArtists,
    addFavoriteSong,
    updateFavoriteSong,
    removeFavoriteSong,
    addFavoriteArtist,
    updateFavoriteArtist,
    removeFavoriteArtist,
    formatPreferredKey,
} from '../utils/favoritesStore'
import { filterFavoriteSongs, filterFavoriteArtists } from './favoritesHelpers'
import { FavoriteSongForm, FavoriteArtistForm } from './FavoritesForms'
import { showToast } from '../components/Toast'
import './Favorites.css'

export function Favorites() {
    const [songs, setSongs] = useState(() => getFavoriteSongs())
    const [artists, setArtists] = useState(() => getFavoriteArtists())
    const [query, setQuery] = useState('')
    const [activeFilter, setActiveFilter] = useState('all') // 'all' | 'songs' | 'artists'
    const [addMode, setAddMode] = useState(null) // null | 'song' | 'artist'
    const [editingSongKey, setEditingSongKey] = useState(null)
    const [editingArtistNorm, setEditingArtistNorm] = useState(null)

    const headingRef = useRef(null)
    const addSongBtnRef = useRef(null)
    const addArtistBtnRef = useRef(null)
    const editBtnRefs = useRef(new Map())
    const pendingFocusRef = useRef(null)

    useEffect(() => {
        const prevTitle = document.title
        document.title = 'My Favorites — MicUp.live'
        return () => {
            document.title = prevTitle
        }
    }, [])

    useEffect(() => {
        const sync = () => {
            setSongs(getFavoriteSongs())
            setArtists(getFavoriteArtists())
        }
        const handleStorage = (e) => {
            if (!e.key || e.key === FAVORITE_SONGS_STORAGE_KEY || e.key === FAVORITE_ARTISTS_STORAGE_KEY) {
                sync()
            }
        }
        window.addEventListener('micup_favorites_changed', sync)
        window.addEventListener('storage', handleStorage)
        return () => {
            window.removeEventListener('micup_favorites_changed', sync)
            window.removeEventListener('storage', handleStorage)
        }
    }, [])

    useEffect(() => {
        const target = pendingFocusRef.current
        if (!target) return
        pendingFocusRef.current = null

        if (target === 'add-song') {
            addSongBtnRef.current?.focus()
        } else if (target === 'add-artist') {
            addArtistBtnRef.current?.focus()
        } else if (target === 'heading') {
            headingRef.current?.focus()
        } else {
            const btn = editBtnRefs.current.get(target)
            if (btn) {
                btn.focus()
            } else {
                headingRef.current?.focus()
            }
        }
    })

    const filteredSongs = useMemo(() => filterFavoriteSongs(songs, query), [songs, query])
    const filteredArtists = useMemo(() => filterFavoriteArtists(artists, query), [artists, query])

    const totalCount = songs.length + artists.length
    const isSearching = query.trim().length > 0
    const showSongs = activeFilter === 'all' || activeFilter === 'songs'
    const showArtists = activeFilter === 'all' || activeFilter === 'artists'

    const visibleSongsCount = showSongs ? filteredSongs.length : 0
    const visibleArtistsCount = showArtists ? filteredArtists.length : 0
    const hiddenCategoryMatches = (activeFilter === 'songs' ? filteredArtists.length : 0)
        + (activeFilter === 'artists' ? filteredSongs.length : 0)
    const noVisibleMatches = totalCount > 0 && visibleSongsCount === 0 && visibleArtistsCount === 0

    const handleAddSong = (payload) => {
        const res = addFavoriteSong(payload)
        if (res.ok) {
            setAddMode(null)
            if (activeFilter === 'artists') setActiveFilter('all')
            if (isSearching && filterFavoriteSongs([res.entry], query).length === 0) {
                setQuery('')
            }
            pendingFocusRef.current = `song:${songKey(res.entry.artist, res.entry.title)}`
            showToast(`★ Added "${res.entry.display_title}" to your favorites`)
        }
        return res
    }

    const handleUpdateSong = (originalSong, payload) => {
        const res = updateFavoriteSong(originalSong, payload)
        if (res.ok) {
            setEditingSongKey(null)
            pendingFocusRef.current = `song:${songKey(res.entry.artist, res.entry.title)}`
            showToast(`Updated "${res.entry.display_title}"`)
        }
        return res
    }

    const handleRemoveSong = (song, idx) => {
        const songArtist = song.artist || song.display_artist
        const songTitle = song.title || song.display_title
        const removed = removeFavoriteSong(songArtist, songTitle, song.canonical_id)
        if (!removed) {
            showToast('Could not remove favorite from browser storage.', 'error')
            return
        }
        const removedKey = songKey(songArtist, songTitle)
        if (editingSongKey === removedKey) {
            setEditingSongKey(null)
        }
        const nextSong = filteredSongs[idx + 1] || filteredSongs[idx - 1]
        pendingFocusRef.current = nextSong
            ? `song:${songKey(nextSong.artist || nextSong.display_artist, nextSong.title || nextSong.display_title)}`
            : 'heading'
        showToast(`Removed "${song.display_title || song.title}" from favorites`)
    }

    const handleAddArtist = (artistName) => {
        const res = addFavoriteArtist(artistName)
        if (res.ok) {
            setAddMode(null)
            if (activeFilter === 'songs') setActiveFilter('all')
            if (isSearching && filterFavoriteArtists([res.entry], query).length === 0) {
                setQuery('')
            }
            pendingFocusRef.current = `artist:${res.entry.normalized}`
            showToast(`★ Added "${res.entry.display_name}" to favorite artists`)
        }
        return res
    }

    const handleUpdateArtist = (originalArtist, nextName) => {
        const res = updateFavoriteArtist(originalArtist, nextName)
        if (res.ok) {
            setEditingArtistNorm(null)
            pendingFocusRef.current = `artist:${res.entry.normalized}`
            showToast(`Updated artist to "${res.entry.display_name}"`)
        }
        return res
    }

    const handleRemoveArtist = (artist, idx) => {
        const artistIdentifier = artist.normalized || artist.display_name
        const removed = removeFavoriteArtist(artistIdentifier)
        if (!removed) {
            showToast('Could not remove favorite artist from browser storage.', 'error')
            return
        }
        if (editingArtistNorm === artist.normalized) {
            setEditingArtistNorm(null)
        }
        const nextArtist = filteredArtists[idx + 1] || filteredArtists[idx - 1]
        pendingFocusRef.current = nextArtist
            ? `artist:${nextArtist.normalized || nextArtist.display_name}`
            : 'heading'
        showToast(`Removed "${artist.display_name || artist.normalized}" from favorite artists`)
    }

    const openAddMode = (mode) => {
        setEditingSongKey(null)
        setEditingArtistNorm(null)
        setAddMode(prev => (prev === mode ? null : mode))
    }

    const setEditBtnRef = (id) => (el) => {
        if (el) {
            editBtnRefs.current.set(id, el)
        } else {
            editBtnRefs.current.delete(id)
        }
    }

    return (
        <div class="favorites-page">
            <div class="favorites-header">
                <div class="favorites-header-top">
                    <div>
                        <h1 ref={headingRef} tabIndex={-1} class="favorites-title">
                            <span class="favorites-star-accent" aria-hidden="true">★</span>
                            <span>My Favorites</span>
                        </h1>
                        <p class="favorites-subtitle">
                            Manage your go-to karaoke songs, favorite artists, and preferred pitch keys.
                            Saved in this browser and ready at any MicUp show — no songbook or account required.
                        </p>
                    </div>
                    <div class="favorites-header-actions">
                        <button
                            ref={addSongBtnRef}
                            id="btn-add-favorite-song"
                            type="button"
                            class="btn btn-primary favorites-action-btn"
                            aria-expanded={addMode === 'song'}
                            onClick={() => openAddMode('song')}
                        >
                            + Add Song
                        </button>
                        <button
                            ref={addArtistBtnRef}
                            id="btn-add-favorite-artist"
                            type="button"
                            class="btn btn-secondary favorites-action-btn"
                            aria-expanded={addMode === 'artist'}
                            onClick={() => openAddMode('artist')}
                        >
                            + Add Artist
                        </button>
                    </div>
                </div>
            </div>

            {addMode === 'song' && (
                <section class="favorites-form-card" aria-label="Add favorite song">
                    <FavoriteSongForm
                        heading="Add a Favorite Song"
                        submitLabel="Save Favorite Song"
                        idPrefix="add-fav-song"
                        onSubmit={handleAddSong}
                        onCancel={() => {
                            pendingFocusRef.current = 'add-song'
                            setAddMode(null)
                        }}
                    />
                </section>
            )}

            {addMode === 'artist' && (
                <section class="favorites-form-card" aria-label="Add favorite artist">
                    <FavoriteArtistForm
                        heading="Add a Favorite Artist"
                        submitLabel="Save Favorite Artist"
                        idPrefix="add-fav-artist"
                        onSubmit={handleAddArtist}
                        onCancel={() => {
                            pendingFocusRef.current = 'add-artist'
                            setAddMode(null)
                        }}
                    />
                </section>
            )}

            {totalCount > 0 && (
                <div class="favorites-toolbar">
                    <search>
                        <div class="favorites-search-wrap">
                            <label for="favorites-search" class="sr-only">
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
                                onInput={(e) => setQuery(e.target.value)}
                            />
                            {isSearching && (
                                <button
                                    type="button"
                                    class="favorites-search-clear"
                                    aria-label="Clear search"
                                    onClick={() => setQuery('')}
                                >
                                    ✕
                                </button>
                            )}
                        </div>
                    </search>

                    <div class="favorites-tabs" role="group" aria-label="Filter favorites by type">
                        <button
                            type="button"
                            class="favorites-tab"
                            aria-pressed={activeFilter === 'all'}
                            onClick={() => setActiveFilter('all')}
                        >
                            All ({totalCount})
                        </button>
                        <button
                            type="button"
                            class="favorites-tab"
                            aria-pressed={activeFilter === 'songs'}
                            onClick={() => setActiveFilter('songs')}
                        >
                            Songs ({songs.length})
                        </button>
                        <button
                            type="button"
                            class="favorites-tab"
                            aria-pressed={activeFilter === 'artists'}
                            onClick={() => setActiveFilter('artists')}
                        >
                            Artists ({artists.length})
                        </button>
                    </div>
                </div>
            )}

            {totalCount === 0 ? (
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
                            onClick={() => openAddMode('song')}
                        >
                            + Add Your First Song
                        </button>
                        <button
                            type="button"
                            class="btn btn-secondary favorites-action-btn"
                            onClick={() => openAddMode('artist')}
                        >
                            + Add Favorite Artist
                        </button>
                    </div>
                </section>
            ) : noVisibleMatches ? (
                <section class="favorites-empty">
                    <h2 class="favorites-empty-title">
                        {isSearching ? `No favorites matching "${query.trim()}"` : `No favorite ${activeFilter} yet`}
                    </h2>
                    <p class="favorites-empty-desc">
                        {hiddenCategoryMatches > 0
                            ? `${hiddenCategoryMatches} matching ${hiddenCategoryMatches === 1 ? 'item is' : 'items are'} in another category.`
                            : isSearching
                                ? 'Try a different search term or add it directly to your favorites.'
                                : `Add a favorite ${activeFilter === 'songs' ? 'song' : 'artist'} using the buttons above.`}
                    </p>
                    <div class="favorites-empty-actions">
                        {hiddenCategoryMatches > 0 && (
                            <button
                                type="button"
                                class="btn btn-primary favorites-action-btn"
                                onClick={() => setActiveFilter('all')}
                            >
                                Show All Favorites
                            </button>
                        )}
                        {isSearching && (
                            <button
                                type="button"
                                class="btn btn-secondary favorites-action-btn"
                                onClick={() => setQuery('')}
                            >
                                Clear Search
                            </button>
                        )}
                    </div>
                </section>
            ) : (
                <>
                    {showSongs && filteredSongs.length > 0 && (
                        <section class="favorites-section" aria-labelledby="favorites-songs-heading">
                            <h2 id="favorites-songs-heading" class="favorites-section-title">
                                <span aria-hidden="true">★ </span>Favorite Songs ({filteredSongs.length})
                            </h2>
                            <ul class="favorites-list">
                                {filteredSongs.map((song, idx) => {
                                    const songArtist = song.artist || song.display_artist
                                    const songTitle = song.title || song.display_title
                                    const key = songKey(songArtist, songTitle)
                                    const displayTitle = song.display_title || song.title
                                    const displayArtist = song.display_artist || song.artist
                                    const prefKey = clampPreferredKey(song.preferred_key)
                                    const isEditing = editingSongKey === key

                                    return (
                                        <li key={key} class="favorites-card">
                                            {isEditing ? (
                                                <FavoriteSongForm
                                                    heading="Edit Favorite Song"
                                                    headingAs="h3"
                                                    submitLabel="Save Changes"
                                                    initialTitle={displayTitle}
                                                    initialArtist={displayArtist}
                                                    initialKey={prefKey}
                                                    idPrefix={`edit-song-${key.replace(/[^a-z0-9]/g, '-')}`}
                                                    onSubmit={(payload) => handleUpdateSong(song, payload)}
                                                    onCancel={() => {
                                                        pendingFocusRef.current = `song:${key}`
                                                        setEditingSongKey(null)
                                                    }}
                                                />
                                            ) : (
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
                                                            onClick={() => {
                                                                setAddMode(null)
                                                                setEditingArtistNorm(null)
                                                                setEditingSongKey(key)
                                                            }}
                                                        >
                                                            ✏️ Edit
                                                        </button>
                                                        <button
                                                            type="button"
                                                            class="favorites-icon-btn danger"
                                                            aria-label={`Remove ${displayTitle} by ${displayArtist} from favorites`}
                                                            onClick={() => handleRemoveSong(song, idx)}
                                                        >
                                                            ✕ Remove
                                                        </button>
                                                    </div>
                                                </div>
                                            )}
                                        </li>
                                    )
                                })}
                            </ul>
                        </section>
                    )}

                    {showArtists && filteredArtists.length > 0 && (
                        <section class="favorites-section" aria-labelledby="favorites-artists-heading">
                            <h2 id="favorites-artists-heading" class="favorites-section-title">
                                <span aria-hidden="true">★ </span>Favorite Artists ({filteredArtists.length})
                            </h2>
                            <ul class="favorites-list">
                                {filteredArtists.map((artist, idx) => {
                                    const artistId = artist.normalized || artist.display_name
                                    const displayName = artist.display_name || artist.normalized
                                    const isEditing = editingArtistNorm === artistId

                                    return (
                                        <li key={artistId} class="favorites-card">
                                            {isEditing ? (
                                                <FavoriteArtistForm
                                                    heading="Edit Favorite Artist"
                                                    headingAs="h3"
                                                    submitLabel="Save Changes"
                                                    initialName={displayName}
                                                    idPrefix={`edit-artist-${String(artistId || '').replace(/[^a-z0-9]/g, '-')}`}
                                                    onSubmit={(nextName) => handleUpdateArtist(artistId, nextName)}
                                                    onCancel={() => {
                                                        pendingFocusRef.current = `artist:${artistId}`
                                                        setEditingArtistNorm(null)
                                                    }}
                                                />
                                            ) : (
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
                                                            onClick={() => {
                                                                setAddMode(null)
                                                                setEditingSongKey(null)
                                                                setEditingArtistNorm(artistId)
                                                            }}
                                                        >
                                                            ✏️ Edit
                                                        </button>
                                                        <button
                                                            type="button"
                                                            class="favorites-icon-btn danger"
                                                            aria-label={`Remove ${displayName} from favorite artists`}
                                                            onClick={() => handleRemoveArtist(artist, idx)}
                                                        >
                                                            ✕ Remove
                                                        </button>
                                                    </div>
                                                </div>
                                            )}
                                        </li>
                                    )
                                })}
                            </ul>
                        </section>
                    )}
                </>
            )}

            <div class="favorites-footer-cta">
                <span>Ready to sing? Join a live show or browse a host's songbook.</span>
                <a href="/" class="btn btn-secondary btn-sm">
                    Enter Show Code →
                </a>
            </div>
        </div>
    )
}
