export function getLetterBucket(text) {
    const firstChar = (text || '').trim().charAt(0).toUpperCase()
    return /[A-Z]/.test(firstChar) ? firstChar : '#'
}

export function normalizePublicOverrides(value) {
    if (Array.isArray(value)) return value
    if (Array.isArray(value?.overrides)) return value.overrides
    return []
}

export function songIdentity(song) {
    return song?.song_id || song?.id || `${song?.artist || ''}\u0000${song?.title || song?.label || ''}`
}

export function applyPublicOverrides(baseSongs, overrides) {
    const songs = new Map((baseSongs || []).map(song => [songIdentity(song), song]))
    for (const change of overrides || []) {
        if (change.song_id === '__clear__' && change.deleted) {
            songs.clear()
        } else if (change.deleted) {
            songs.delete(change.song_id)
        } else if (change.song) {
            songs.set(songIdentity(change.song), change.song)
        }
    }
    return Array.from(songs.values())
}

export function searchSongs(songs, query) {
    const needle = (query || '').trim().toLowerCase()
    if (!needle) return songs || []
    return (songs || []).filter(song =>
        [song.artist, song.title, song.label, song.disc, song.genre]
            .some(value => (value || '').toLowerCase().includes(needle)))
}

export function mergePendingSearchResults(results, overrides, query) {
    const pending = []
    const seen = new Set((results || []).map(songIdentity))
    for (const change of overrides || []) {
        if (change.deleted || !change.song) continue
        const song = change.song
        if (!searchSongs([song], query).length) continue
        const id = songIdentity(song)
        if (seen.has(id)) continue
        seen.add(id)
        pending.push(song)
    }
    return [...(results || []), ...pending]
}

export function sortSongsByTitle(songs) {
    return [...(songs || [])].sort((a, b) => {
        const titleA = a.title || a.label || ''
        const titleB = b.title || b.label || ''
        const titleCmp = titleA.localeCompare(titleB, undefined, { sensitivity: 'base' })
        if (titleCmp !== 0) return titleCmp
        return (a.artist || '').localeCompare(b.artist || '', undefined, { sensitivity: 'base' })
    })
}

export function countSongsByLetter(songs) {
    const counts = {}
    for (const song of songs || []) {
        const bucket = getLetterBucket(song.title || song.label)
        counts[bucket] = (counts[bucket] || 0) + 1
    }
    return counts
}

export function countArtistsByLetter(artistsOrSongs) {
    const counts = {}
    const seen = new Set()
    for (const item of artistsOrSongs || []) {
        const artist = (item.artist || 'Unknown Artist').trim()
        if (seen.has(artist)) continue
        seen.add(artist)
        const bucket = getLetterBucket(artist)
        counts[bucket] = (counts[bucket] || 0) + 1
    }
    return counts
}

export function firstAvailableLetterFromCounts(counts) {
    if (!counts) return 'A'
    if (counts['A'] > 0) return 'A'
    const letters = Object.keys(counts).filter(k => counts[k] > 0)
    if (!letters.length) return 'A'
    letters.sort((a, b) => (a === '#' ? -1 : b === '#' ? 1 : a.localeCompare(b)))
    return letters[0] || 'A'
}

export function groupSongsByArtist(songs, { activeArtistLetter, isSearching = false, normalize = null } = {}) {
    const normFn = normalize || (text => (text || '').toLowerCase().replace(/[\s\-''`]/g, ''))
    let songsToGroup = songs || []
    if (!isSearching && activeArtistLetter) {
        songsToGroup = songsToGroup.filter(s => getLetterBucket(s.artist) === activeArtistLetter)
    }
    const map = new Map()
    for (const song of songsToGroup) {
        const artist = (song.artist || 'Unknown Artist').trim()
        const norm = normFn(artist)
        if (!map.has(norm)) {
            map.set(norm, { artist, norm, count: 0, songs: [] })
        }
        map.get(norm).count++
        map.get(norm).songs.push(song)
    }
    return Array.from(map.values()).sort((a, b) =>
        a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' })
    )
}

export async function loadOverlayOnlyCatalog(apiClient, songbookId, qs = '') {
    const rawOverrides = await apiClient.get(`/songbooks/${songbookId}/public-overrides${qs || ''}`)
    const loadedOverrides = normalizePublicOverrides(rawOverrides)
    const activeSongs = applyPublicOverrides([], loadedOverrides)
    const songs = sortSongsByTitle(activeSongs)
    const letterCounts = countSongsByLetter(songs)
    const artistLetterCounts = countArtistsByLetter(songs)
    const firstLetter = firstAvailableLetterFromCounts(letterCounts)
    const firstArtistLetter = firstAvailableLetterFromCounts(artistLetterCounts)

    return {
        songs,
        loadedOverrides,
        totalSongs: songs.length,
        letterCounts,
        artistLetterCounts,
        firstLetter,
        firstArtistLetter,
    }
}
