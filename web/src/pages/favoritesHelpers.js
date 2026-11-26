import { normalizeString } from '../utils/favoritesStore.js'

export const PITCH_KEY_OPTIONS = [
    { value: -3, label: '-3♭ (-3 semitones)' },
    { value: -2, label: '-2♭ (-2 semitones)' },
    { value: -1, label: '-1♭ (-1 semitone)' },
    { value: 0, label: 'Original Key (0)' },
    { value: 1, label: '+1♯ (+1 semitone)' },
    { value: 2, label: '+2♯ (+2 semitones)' },
    { value: 3, label: '+3♯ (+3 semitones)' },
]

export function filterFavoriteSongs(songs, query) {
    const q = normalizeString(query)
    const list = Array.isArray(songs) ? songs : []
    const filtered = q
        ? list.filter(item => {
            const title = normalizeString(item.display_title || item.title)
            const artist = normalizeString(item.display_artist || item.artist)
            return title.includes(q) || artist.includes(q)
        })
        : list

    return [...filtered].sort((a, b) => {
        const titleA = (a.display_title || a.title || '').toLowerCase()
        const titleB = (b.display_title || b.title || '').toLowerCase()
        const cmp = titleA.localeCompare(titleB)
        if (cmp !== 0) return cmp
        const artistA = (a.display_artist || a.artist || '').toLowerCase()
        const artistB = (b.display_artist || b.artist || '').toLowerCase()
        return artistA.localeCompare(artistB)
    })
}

export function filterFavoriteArtists(artists, query) {
    const q = normalizeString(query)
    const list = Array.isArray(artists) ? artists : []
    const filtered = q
        ? list.filter(item => {
            const name = normalizeString(item.display_name || item.normalized)
            return name.includes(q)
        })
        : list

    return [...filtered].sort((a, b) => {
        const nameA = (a.display_name || a.normalized || '').toLowerCase()
        const nameB = (b.display_name || b.normalized || '').toLowerCase()
        return nameA.localeCompare(nameB)
    })
}
