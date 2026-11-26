import { useState, useEffect, useRef, useCallback, useMemo } from 'preact/hooks'
import { api } from '../../../api/client.js'
import {
    firstAvailableLetter,
    loadCatalogManifest,
    loadCatalogShard,
    createCatalogWorker,
} from '../../../api/catalogAssets.js'
import { showToast } from '../../../components/toastState.js'
import {
    normalizeString,
    songKey,
    isFavoriteSong,
    isFavoriteArtist,
} from '../../../utils/favoritesStore.js'
import { groupSongVersions } from '../utils/groupSongVersions.js'

export {
    getLetterBucket,
    normalizePublicOverrides,
    songIdentity,
    applyPublicOverrides,
    searchSongs,
    mergePendingSearchResults,
} from '../utils/overlayCatalog.js'
import {
    getLetterBucket,
    normalizePublicOverrides,
    applyPublicOverrides,
    searchSongs,
    mergePendingSearchResults,
    loadOverlayOnlyCatalog,
} from '../utils/overlayCatalog.js'

export function useCatalogSearch({ resolvedSongbookId, eventCode, eventId, favSongs = [], favArtists = [], viewMode = 'songs', retryCount = 0 }) {
    const [allSongs, setAllSongs] = useState([])
    const [publicOverrides, setPublicOverrides] = useState([])
    const [catalogName, setCatalogName] = useState('')
    const [totalSongs, setTotalSongs] = useState(0)
    const [loadingSongs, setLoadingSongs] = useState(false)
    const [query, setQuery] = useState('')
    const [searchResults, setSearchResults] = useState([])
    const [artistSearchResults, setArtistSearchResults] = useState([])
    const [indexing, setIndexing] = useState(false)
    const [workerReady, setWorkerReady] = useState(false)
    const [workerArtists, setWorkerArtists] = useState([])
    const [staticManifest, setStaticManifest] = useState(null)
    const [activeLetter, setActiveLetter] = useState('A')
    const [activeArtistLetter, setActiveArtistLetter] = useState('A')
    const [activeArtistFilter, setActiveArtistFilter] = useState(null)
    const [expandedArtists, setExpandedArtists] = useState(new Set())
    const [expandedArtistSongs, setExpandedArtistSongs] = useState({})
    const [loadingArtistSongs, setLoadingArtistSongs] = useState({})
    const [staticResolvedFavorites, setStaticResolvedFavorites] = useState({ inBook: [], offBook: [] })
    const [catalogError, setCatalogError] = useState('')

    const workerRef = useRef(null)
    const initialShardRef = useRef(null)

    // Cleanup worker on unmount
    useEffect(() => {
        return () => {
            workerRef.current?.terminate()
            workerRef.current = null
        }
    }, [])

    // Load initial manifest, worker, and first shard
    useEffect(() => {
        if (!resolvedSongbookId) return

        let cancelled = false

        if (workerRef.current) {
            workerRef.current.terminate()
            workerRef.current = null
        }
        setWorkerReady(false)
        setWorkerArtists([])
        setPublicOverrides(null)
        setExpandedArtistSongs({})
        setLoadingArtistSongs({})
        setStaticResolvedFavorites({ inBook: [], offBook: [] })
        setLoadingSongs(true)
        setAllSongs([])
        setTotalSongs(0)
        setSearchResults([])
        setArtistSearchResults([])
        setIndexing(false)
        setCatalogError('')

        const loadManifestAndFirstShard = async () => {
            try {
                const queryParams = new URLSearchParams()
                if (eventCode) queryParams.set('code', eventCode)
                if (eventId) queryParams.set('event', eventId)
                const qs = queryParams.toString() ? `?${queryParams.toString()}` : ''

                const resolution = await api.get(`/songbooks/${resolvedSongbookId}/public-manifest${qs}`)
                if (!resolution?.static_ready) {
                    if (resolution?.overlay_ready) {
                        const overlay = await loadOverlayOnlyCatalog(api, resolvedSongbookId, qs)
                        if (cancelled) return
                        setPublicOverrides(overlay.loadedOverrides)
                        setCatalogName(resolution.name || 'Songbook')
                        setAllSongs(overlay.songs)
                        setTotalSongs(overlay.totalSongs)
                        setActiveLetter(overlay.firstLetter || 'A')
                        if (overlay.firstArtistLetter) setActiveArtistLetter(overlay.firstArtistLetter)
                        return
                    }
                    throw new Error('This catalog is still being prepared. Please try again shortly.')
                }
                const manifest = await loadCatalogManifest(resolution.manifest_url)
                const baseDir = resolution.manifest_url.substring(0, resolution.manifest_url.lastIndexOf('/') + 1)
                manifest._baseDir = baseDir
                const letter = firstAvailableLetter(manifest)
                const shards = (manifest.letters?.[letter]?.shards || []).map(s => baseDir + s)
                const pages = await Promise.all(shards.map(loadCatalogShard))
                if (cancelled) return

                let songs = pages.flat()
                let loadedOverrides = null
                try {
                    const rawOverrides = await api.get(`/songbooks/${resolvedSongbookId}/public-overrides${qs}`)
                    if (cancelled) return
                    loadedOverrides = normalizePublicOverrides(rawOverrides)
                    setPublicOverrides(loadedOverrides)
                    songs = applyPublicOverrides(songs, loadedOverrides)
                } catch {
                    if (cancelled) return
                }
                songs = songs.sort((a, b) => (a.title || '').localeCompare(b.title || '', undefined, { sensitivity: 'base' }))
                const resolvedLetter = letter || 'A'
                initialShardRef.current = { manifest, letter: resolvedLetter, overrides: loadedOverrides }
                setStaticManifest(manifest)
                setCatalogName(resolution.name || 'Songbook')
                setAllSongs(songs)
                setTotalSongs(manifest.total || 0)
                setActiveLetter(resolvedLetter)

                const worker = createCatalogWorker()
                if (worker) {
                    workerRef.current = worker
                    setIndexing(true)
                    worker.init(baseDir + (manifest.search_path || 'search.json.gz'))
                        .then((res) => {
                            if (cancelled) return
                            setWorkerArtists(res.artists || [])
                            setWorkerReady(true)
                        })
                        .catch((err) => {
                            console.error('Failed to init catalog worker:', err)
                            if (!cancelled) showToast('Full search is temporarily unavailable', 'error')
                        })
                        .finally(() => {
                            if (!cancelled) setIndexing(false)
                        })
                }
            } catch (err) {
                if (cancelled) return
                setCatalogError(err.message || 'Failed to load catalog')
            } finally {
                if (!cancelled) {
                    setLoadingSongs(false)
                }
            }
        }

        loadManifestAndFirstShard()

        return () => {
            cancelled = true
        }
    }, [resolvedSongbookId, eventCode, eventId, retryCount])

    // Load shard when active letter changes
    useEffect(() => {
        if (!staticManifest || !activeLetter) return
        const initial = initialShardRef.current
        if (initial && initial.manifest === staticManifest && initial.letter === activeLetter && (initial.overrides === null || initial.overrides === publicOverrides)) {
            initialShardRef.current = null
            return
        }
        initialShardRef.current = null
        const baseDir = staticManifest._baseDir || ''
        const shards = (staticManifest.letters?.[activeLetter]?.shards || []).map(s => baseDir + s)
        if (shards.length === 0) {
            setAllSongs([])
            return
        }
        let cancelled = false
        setLoadingSongs(true)
        Promise.all(shards.map(loadCatalogShard)).then((pages) => {
            if (!cancelled) {
                const songs = applyPublicOverrides(pages.flat(), publicOverrides)
                setAllSongs(songs.sort((a, b) => (a.title || '').localeCompare(b.title || '', undefined, { sensitivity: 'base' })))
            }
        }).catch(() => {
            if (!cancelled) showToast('Failed to load catalog page', 'error')
        }).finally(() => {
            if (!cancelled) setLoadingSongs(false)
        })
        return () => { cancelled = true }
    }, [staticManifest, activeLetter, publicOverrides])

    // Search query changes
    useEffect(() => {
        if (!query.trim()) {
            setSearchResults([])
            setArtistSearchResults([])
            return
        }

        let cancelled = false
        if (viewMode === 'artists' && workerRef.current) {
            workerRef.current.searchArtists(query.trim())
                .then(res => {
                    if (!cancelled) setArtistSearchResults(res.results || [])
                })
                .catch(console.error)
        } else {
            const search = workerRef.current?.search(query.trim())
                || Promise.resolve({ results: searchSongs(allSongs, query) })
            search
                .then(res => {
                    if (!cancelled) {
                        setSearchResults(mergePendingSearchResults(res.results || [], publicOverrides, query))
                    }
                })
                .catch(() => {
                    if (!cancelled) setSearchResults(mergePendingSearchResults(searchSongs(allSongs, query), publicOverrides, query))
                })
        }
        return () => { cancelled = true }
    }, [query, indexing, workerReady, viewMode, publicOverrides, allSongs])

    // Resolve favorites against static worker
    useEffect(() => {
        if (!staticManifest || !workerRef.current) return
        if (!favSongs.length) {
            setStaticResolvedFavorites({ inBook: [], offBook: [] })
            return
        }
        let cancelled = false
        workerRef.current.resolveFavorites(favSongs)
            .then(res => {
                if (!cancelled) {
                    setStaticResolvedFavorites({
                        inBook: res.inBookTracks || [],
                        offBook: res.offBookFavorites || [],
                    })
                }
            })
            .catch(console.error)
        return () => { cancelled = true }
    }, [staticManifest, favSongs, workerReady])

    const toggleArtistExpand = useCallback((norm) => {
        setExpandedArtists(prev => {
            const next = new Set(prev)
            const willExpand = !next.has(norm)
            if (willExpand) {
                next.add(norm)
                if (staticManifest && workerRef.current && !expandedArtistSongs[norm]) {
                    setLoadingArtistSongs(s => ({ ...s, [norm]: true }))
                    workerRef.current.getArtistSongs(norm)
                        .then(res => {
                            setExpandedArtistSongs(s => ({ ...s, [norm]: groupSongVersions(res.songs || []) }))
                        })
                        .catch(console.error)
                        .finally(() => {
                            setLoadingArtistSongs(s => ({ ...s, [norm]: false }))
                        })
                }
            } else {
                next.delete(norm)
            }
            return next
        })
    }, [staticManifest, expandedArtistSongs])

    const expandAllArtists = useCallback((groups = []) => {
        const next = new Set(groups.map(g => g.norm))
        setExpandedArtists(next)
        if (staticManifest && workerRef.current) {
            for (const g of groups) {
                if (!expandedArtistSongs[g.norm]) {
                    setLoadingArtistSongs(s => ({ ...s, [g.norm]: true }))
                    workerRef.current.getArtistSongs(g.norm)
                        .then(res => {
                            setExpandedArtistSongs(s => ({ ...s, [g.norm]: groupSongVersions(res.songs || []) }))
                        })
                        .catch(console.error)
                        .finally(() => {
                            setLoadingArtistSongs(s => ({ ...s, [g.norm]: false }))
                        })
                }
            }
        }
    }, [staticManifest, expandedArtistSongs])

    const collapseAllArtists = useCallback(() => {
        setExpandedArtists(new Set())
    }, [])

    const isSearching = query.trim().length > 0

    const songsByLetter = useMemo(() => {
        const buckets = new Map()
        for (const song of allSongs) {
            const letter = getLetterBucket(song.title || song.label)
            const bucket = buckets.get(letter) || []
            bucket.push(song)
            buckets.set(letter, bucket)
        }
        return buckets
    }, [allSongs])

    const letterCounts = useMemo(() => staticManifest ? Object.fromEntries(
        Object.entries(staticManifest.letters || {}).map(([letter, value]) => [letter, value.count])
    ) : Object.fromEntries(
        Array.from(songsByLetter, ([letter, songs]) => [letter, songs.length])
    ), [songsByLetter, staticManifest])

    const artistLetterCounts = useMemo(() => {
        const counts = {}
        const sourceArtists = (staticManifest && workerArtists.length > 0)
            ? workerArtists
            : Array.from(new Set(allSongs.map(s => (s.artist || 'Unknown Artist').trim()))).map(name => ({ artist: name }))

        for (const a of sourceArtists) {
            const bucket = getLetterBucket(a.artist)
            counts[bucket] = (counts[bucket] || 0) + 1
        }
        return counts
    }, [staticManifest, workerArtists, allSongs])

    useEffect(() => {
        if (!artistLetterCounts[activeArtistLetter]) {
            const first = Object.keys(artistLetterCounts).sort((a, b) => a === '#' ? -1 : b === '#' ? 1 : a.localeCompare(b))[0]
            if (first) setActiveArtistLetter(first)
        }
    }, [artistLetterCounts, activeArtistLetter])

    // Filter songs by activeArtistFilter if active
    const displaySongs = useMemo(() => {
        const currentLetterSongs = staticManifest ? allSongs : (songsByLetter.get(activeLetter) || [])
        let list = isSearching ? searchResults : currentLetterSongs
        if (activeArtistFilter) {
            const normFilter = normalizeString(activeArtistFilter)
            list = list.filter(s => normalizeString(s.artist) === normFilter)
        }
        return groupSongVersions(list)
    }, [isSearching, searchResults, allSongs, songsByLetter, activeLetter, activeArtistFilter, staticManifest])

    // Group songs by artist for collapsible artist view
    const artistGroups = useMemo(() => {
        if (staticManifest && workerArtists.length > 0) {
            let list = isSearching ? artistSearchResults : workerArtists
            if (activeArtistFilter) {
                const normFilter = normalizeString(activeArtistFilter)
                list = list.filter(a => a.norm === normFilter)
            } else if (!isSearching) {
                list = list.filter(a => getLetterBucket(a.artist) === activeArtistLetter)
            }
            const groups = list.map(item => ({
                artist: item.artist,
                norm: item.norm,
                count: item.count,
                songs: expandedArtistSongs[item.norm] || [],
                isFav: isFavoriteArtist(item.artist),
                matchedBySong: item.matchedBySong,
            }))
            if (!isSearching) {
                groups.sort((a, b) => {
                    if (a.isFav && !b.isFav) return -1
                    if (!a.isFav && b.isFav) return 1
                    return a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' })
                })
            }
            return groups
        }

        let songsToGroup = isSearching ? searchResults : allSongs
        if (activeArtistFilter) {
            const normFilter = normalizeString(activeArtistFilter)
            songsToGroup = songsToGroup.filter(s => normalizeString(s.artist) === normFilter)
        } else if (!isSearching) {
            songsToGroup = songsToGroup.filter(s => getLetterBucket(s.artist) === activeArtistLetter)
        }

        const map = new Map()
        for (const song of songsToGroup) {
            const artist = (song.artist || 'Unknown Artist').trim()
            const norm = normalizeString(artist)
            if (!map.has(norm)) {
                map.set(norm, {
                    artist,
                    norm,
                    count: 0,
                    songs: [],
                    isFav: isFavoriteArtist(artist)
                })
            }
            map.get(norm).count++
            map.get(norm).songs.push(song)
        }

        const groups = Array.from(map.values()).map(g => {
            const grouped = groupSongVersions(g.songs)
            return { ...g, songs: grouped, count: grouped.length }
        })
        if (!isSearching) {
            groups.sort((a, b) => {
                if (a.isFav && !b.isFav) return -1
                if (!a.isFav && b.isFav) return 1
                return a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' })
            })
        }
        return groups
    }, [allSongs, searchResults, artistSearchResults, isSearching, activeArtistFilter, favArtists, staticManifest, workerArtists, expandedArtistSongs, activeArtistLetter])

    const uniqueArtistsCount = useMemo(() => {
        if (staticManifest) return workerReady ? workerArtists.length : (indexing ? null : new Set(allSongs.map(s => normalizeString(s.artist)).filter(Boolean)).size)
        return new Set(allSongs.map(s => normalizeString(s.artist)).filter(Boolean)).size
    }, [allSongs, staticManifest, workerReady, indexing, workerArtists])

    // Songs favorited by the singer present in tonight's catalog
    const tonightFavoriteSongs = useMemo(() => {
        if (staticManifest) return staticResolvedFavorites.inBook
        if (!allSongs.length) return []
        return allSongs.filter(song => isFavoriteSong(song.artist, song.title || song.label))
    }, [staticManifest, staticResolvedFavorites, allSongs, favSongs])

    // Favorites from other shows that are not in tonight's catalog
    const offBookFavorites = useMemo(() => {
        if (staticManifest) return staticResolvedFavorites.offBook
        if (!favSongs.length) return []
        const catalogKeys = new Set(allSongs.map(s => songKey(s.artist, s.title || s.label)))
        return favSongs.filter(f => !catalogKeys.has(songKey(f.artist, f.title)))
    }, [staticManifest, staticResolvedFavorites, allSongs, favSongs])

    // Quick favorite artist chips present in tonight's catalog
    const catalogFavoriteArtists = useMemo(() => {
        if (!favArtists.length) return []
        if (staticManifest && workerArtists.length > 0) {
            const artistMap = new Map(workerArtists.map(a => [a.norm, a.count]))
            return favArtists
                .filter(a => artistMap.has(a.normalized))
                .map(a => ({
                    ...a,
                    songCount: artistMap.get(a.normalized)
                }))
        }
        if (!allSongs.length) return []
        const catalogCounts = new Map()
        for (const s of allSongs) {
            const norm = normalizeString(s.artist)
            catalogCounts.set(norm, (catalogCounts.get(norm) || 0) + 1)
        }
        return favArtists
            .filter(a => catalogCounts.has(a.normalized))
            .map(a => ({
                ...a,
                songCount: catalogCounts.get(a.normalized)
            }))
    }, [favArtists, allSongs, staticManifest, workerArtists])

    const offBookFavoriteArtists = useMemo(() => {
        if (!favArtists.length) return []
        const inBookNorms = new Set(catalogFavoriteArtists.map(a => a.normalized))
        return favArtists.filter(a => !inBookNorms.has(a.normalized))
    }, [favArtists, catalogFavoriteArtists])

    const favoriteArtistGroups = useMemo(() => {
        if (!catalogFavoriteArtists.length) return []
        return catalogFavoriteArtists.map(fa => {
            const norm = fa.normalized
            const artistName = fa.display_name
            const count = fa.songCount || 0
            const songs = staticManifest ? (expandedArtistSongs[norm] || []) : allSongs.filter(s => normalizeString(s.artist) === norm)
            return {
                artist: artistName,
                norm,
                count,
                songs,
                isFav: true
            }
        })
    }, [catalogFavoriteArtists, staticManifest, expandedArtistSongs, allSongs])

    const isRepertoireEmpty = favSongs.length === 0 && favArtists.length === 0

    const displayedFavoriteArtists = useMemo(() => {
        if (!isSearching) return favoriteArtistGroups
        const q = normalizeString(query)
        return favoriteArtistGroups.filter(g => normalizeString(g.artist).includes(q))
    }, [favoriteArtistGroups, isSearching, query])

    const displayedTonightSongs = useMemo(() => {
        if (!isSearching) return tonightFavoriteSongs
        const q = normalizeString(query)
        return tonightFavoriteSongs.filter(s => 
            normalizeString(s.title || s.label).includes(q) || normalizeString(s.artist).includes(q)
        )
    }, [tonightFavoriteSongs, isSearching, query])

    const displayedOffBookSongs = useMemo(() => {
        if (!isSearching) return offBookFavorites
        const q = normalizeString(query)
        return offBookFavorites.filter(s => 
            normalizeString(s.display_title || s.title).includes(q) || normalizeString(s.display_artist || s.artist).includes(q)
        )
    }, [offBookFavorites, isSearching, query])

    const displayedOffBookArtists = useMemo(() => {
        if (!isSearching) return offBookFavoriteArtists
        const q = normalizeString(query)
        return offBookFavoriteArtists.filter(a => normalizeString(a.display_name).includes(q))
    }, [offBookFavoriteArtists, isSearching, query])

    const totalTonightFavoritesCount = tonightFavoriteSongs.length + catalogFavoriteArtists.length

    return {
        allSongs,
        publicOverrides,
        catalogName,
        totalSongs,
        loadingSongs,
        query,
        setQuery,
        searchResults,
        artistSearchResults,
        indexing,
        workerReady,
        workerArtists,
        staticManifest,
        activeLetter,
        setActiveLetter,
        activeArtistLetter,
        setActiveArtistLetter,
        activeArtistFilter,
        setActiveArtistFilter,
        expandedArtists,
        setExpandedArtists,
        expandedArtistSongs,
        loadingArtistSongs,
        staticResolvedFavorites,
        catalogError,
        toggleArtistExpand,
        expandAllArtists,
        collapseAllArtists,
        isSearching,
        displaySongs,
        artistGroups,
        letterCounts,
        artistLetterCounts,
        uniqueArtistsCount,
        tonightFavoriteSongs,
        offBookFavorites,
        catalogFavoriteArtists,
        offBookFavoriteArtists,
        favoriteArtistGroups,
        displayedFavoriteArtists,
        displayedTonightSongs,
        displayedOffBookSongs,
        displayedOffBookArtists,
        totalTonightFavoritesCount,
        isRepertoireEmpty,
    }
}
