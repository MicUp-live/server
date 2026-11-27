import { useState, useEffect, useMemo, useRef } from 'preact/hooks'
import {
    FAVORITE_SONGS_STORAGE_KEY,
    FAVORITE_ARTISTS_STORAGE_KEY,
    songKey,
    getFavoriteSongs,
    getFavoriteArtists,
    addFavoriteSong,
    updateFavoriteSong,
    removeFavoriteSong,
    addFavoriteArtist,
    updateFavoriteArtist,
    removeFavoriteArtist,
} from '../utils/favoritesStore'
import { filterFavoriteSongs, filterFavoriteArtists } from './favoritesHelpers'
import {
    FavoritesAddSection,
    FavoritesToolbar,
    FavoritesContent,
} from './FavoritesSections'
import { showToast } from '../components/Toast'
import './Favorites.css'

const HIDDEN_COUNT_FILTER_KEYS = {
    songs: 'artists',
    artists: 'songs',
}

function isFavoritesStorageEvent(e) {
    return !e.key || e.key === FAVORITE_SONGS_STORAGE_KEY || e.key === FAVORITE_ARTISTS_STORAGE_KEY
}

function applyPendingFocus(target, { addSongBtnRef, addArtistBtnRef, headingRef, editBtnRefs }) {
    const staticRefs = {
        'add-song': addSongBtnRef,
        'add-artist': addArtistBtnRef,
        heading: headingRef,
    }
    if (staticRefs[target]) {
        staticRefs[target].current?.focus()
        return
    }
    const btn = editBtnRefs.current.get(target)
    if (btn) {
        btn.focus()
        return
    }
    headingRef.current?.focus()
}

function updateRefMap(map, id, el) {
    if (el) {
        map.set(id, el)
    } else {
        map.delete(id)
    }
}

function submitAddSong(payload, { activeFilter, isSearching, query, setAddMode, setActiveFilter, setQuery, pendingFocusRef }) {
    const res = addFavoriteSong(payload)
    if (!res.ok) return res
    setAddMode(null)
    if (activeFilter === 'artists') setActiveFilter('all')
    if (isSearching && filterFavoriteSongs([res.entry], query).length === 0) {
        setQuery('')
    }
    pendingFocusRef.current = `song:${songKey(res.entry.artist, res.entry.title)}`
    showToast(`★ Added "${res.entry.display_title}" to your favorites`)
    return res
}

function submitUpdateSong(originalSong, payload, { setEditingSongKey, pendingFocusRef }) {
    const res = updateFavoriteSong(originalSong, payload)
    if (!res.ok) return res
    setEditingSongKey(null)
    pendingFocusRef.current = `song:${songKey(res.entry.artist, res.entry.title)}`
    showToast(`Updated "${res.entry.display_title}"`)
    return res
}

function submitRemoveSong(song, idx, { filteredSongs, editingSongKey, setEditingSongKey, pendingFocusRef }) {
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

function submitAddArtist(artistName, { activeFilter, isSearching, query, setAddMode, setActiveFilter, setQuery, pendingFocusRef }) {
    const res = addFavoriteArtist(artistName)
    if (!res.ok) return res
    setAddMode(null)
    if (activeFilter === 'songs') setActiveFilter('all')
    if (isSearching && filterFavoriteArtists([res.entry], query).length === 0) {
        setQuery('')
    }
    pendingFocusRef.current = `artist:${res.entry.normalized}`
    showToast(`★ Added "${res.entry.display_name}" to favorite artists`)
    return res
}

function submitUpdateArtist(originalArtist, nextName, { setEditingArtistNorm, pendingFocusRef }) {
    const res = updateFavoriteArtist(originalArtist, nextName)
    if (!res.ok) return res
    setEditingArtistNorm(null)
    pendingFocusRef.current = `artist:${res.entry.normalized}`
    showToast(`Updated artist to "${res.entry.display_name}"`)
    return res
}

function submitRemoveArtist(artist, idx, { filteredArtists, editingArtistNorm, setEditingArtistNorm, pendingFocusRef }) {
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
            if (isFavoritesStorageEvent(e)) sync()
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
        applyPendingFocus(target, { addSongBtnRef, addArtistBtnRef, headingRef, editBtnRefs })
    })

    const filteredSongs = useMemo(() => filterFavoriteSongs(songs, query), [songs, query])
    const filteredArtists = useMemo(() => filterFavoriteArtists(artists, query), [artists, query])

    const totalCount = songs.length + artists.length
    const isSearching = query.trim().length > 0
    const showSongs = activeFilter !== 'artists'
    const showArtists = activeFilter !== 'songs'

    const visibleSongsCount = showSongs ? filteredSongs.length : 0
    const visibleArtistsCount = showArtists ? filteredArtists.length : 0
    const hiddenCategoryKey = HIDDEN_COUNT_FILTER_KEYS[activeFilter]
    const hiddenCounts = { songs: filteredSongs.length, artists: filteredArtists.length }
    const hiddenCategoryMatches = hiddenCounts[hiddenCategoryKey] || 0
    const noVisibleMatches = totalCount > 0 && visibleSongsCount === 0 && visibleArtistsCount === 0

    const openAddMode = (mode) => {
        setEditingSongKey(null)
        setEditingArtistNorm(null)
        setAddMode((prev) => (prev === mode ? null : mode))
    }

    const setEditBtnRef = (id) => (el) => updateRefMap(editBtnRefs.current, id, el)

    const startEditSong = (key) => {
        setAddMode(null)
        setEditingArtistNorm(null)
        setEditingSongKey(key)
    }

    const startEditArtist = (artistId) => {
        setAddMode(null)
        setEditingSongKey(null)
        setEditingArtistNorm(artistId)
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

            <FavoritesAddSection
                addMode={addMode}
                onAddSong={(payload) => submitAddSong(payload, {
                    activeFilter, isSearching, query, setAddMode, setActiveFilter, setQuery, pendingFocusRef,
                })}
                onAddArtist={(artistName) => submitAddArtist(artistName, {
                    activeFilter, isSearching, query, setAddMode, setActiveFilter, setQuery, pendingFocusRef,
                })}
                onCancel={(focusTarget) => {
                    pendingFocusRef.current = focusTarget
                    setAddMode(null)
                }}
            />

            {totalCount > 0 && (
                <FavoritesToolbar
                    totalCount={totalCount}
                    songsCount={songs.length}
                    artistsCount={artists.length}
                    query={query}
                    isSearching={isSearching}
                    activeFilter={activeFilter}
                    onQueryChange={setQuery}
                    onClearQuery={() => setQuery('')}
                    onFilterChange={setActiveFilter}
                />
            )}

            <FavoritesContent
                totalCount={totalCount}
                noVisibleMatches={noVisibleMatches}
                activeFilter={activeFilter}
                query={query}
                isSearching={isSearching}
                hiddenCategoryMatches={hiddenCategoryMatches}
                showSongs={showSongs}
                showArtists={showArtists}
                filteredSongs={filteredSongs}
                filteredArtists={filteredArtists}
                editingSongKey={editingSongKey}
                editingArtistNorm={editingArtistNorm}
                setEditBtnRef={setEditBtnRef}
                onOpenAddMode={openAddMode}
                onShowAll={() => setActiveFilter('all')}
                onClearSearch={() => setQuery('')}
                onStartEditSong={startEditSong}
                onUpdateSong={(song, payload) => submitUpdateSong(song, payload, { setEditingSongKey, pendingFocusRef })}
                onCancelEditSong={(key) => {
                    pendingFocusRef.current = `song:${key}`
                    setEditingSongKey(null)
                }}
                onRemoveSong={(song, idx) => submitRemoveSong(song, idx, {
                    filteredSongs, editingSongKey, setEditingSongKey, pendingFocusRef,
                })}
                onStartEditArtist={startEditArtist}
                onUpdateArtist={(artistId, nextName) => submitUpdateArtist(artistId, nextName, {
                    setEditingArtistNorm, pendingFocusRef,
                })}
                onCancelEditArtist={(artistId) => {
                    pendingFocusRef.current = `artist:${artistId}`
                    setEditingArtistNorm(null)
                }}
                onRemoveArtist={(artist, idx) => submitRemoveArtist(artist, idx, {
                    filteredArtists, editingArtistNorm, setEditingArtistNorm, pendingFocusRef,
                })}
            />

            <div class="favorites-footer-cta">
                <span>Ready to sing? Join a live show or browse a host's songbook.</span>
                <a href="/" class="btn btn-secondary btn-sm">
                    Enter Show Code →
                </a>
            </div>
        </div>
    )
}
