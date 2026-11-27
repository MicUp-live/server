/**
 * Utility for parsing CSV/TSV karaoke song catalogs from PCDJ Karaoki,
 * SongbookDB, MTU Hoster, OpenKJ, CompuHost, and generic spreadsheets.
 */

function countUnquotedChar(line, targetChar) {
    let count = 0
    let inQuotes = false
    for (let i = 0; i < line.length; i++) {
        const ch = line[i]
        if (ch === '"') {
            if (inQuotes && line[i + 1] === '"') {
                i++ // Skip escaped quote
            } else {
                inQuotes = !inQuotes
            }
            continue
        }
        if (!inQuotes && ch === targetChar) {
            count++
        }
    }
    return count
}

function scoreDelimiter(lines, char) {
    const counts = lines.map(line => countUnquotedChar(line, char))
    if (counts.every(c => c === 0)) return -1

    const first = counts[0]
    const consistent = first > 0 && counts.every(c => c === first)
    const total = counts.reduce((a, b) => a + b, 0)
    return (consistent ? 1000 : 0) + total
}

/**
 * Detect the delimiter used in a CSV/TSV text sample.
 * Checks comma, tab, semicolon, and pipe.
 */
export function detectDelimiter(text) {
    const lines = text.split(/\r?\n/).filter(line => line.trim().length > 0).slice(0, 10)
    if (lines.length === 0) return ','

    const candidates = ['\t', ',', ';', '|']
    let bestChar = ','
    let bestScore = -1

    for (const char of candidates) {
        const score = scoreDelimiter(lines, char)
        if (score > bestScore) {
            bestScore = score
            bestChar = char
        }
    }

    return bestChar
}

function pushNonEmptyRow(rows, row) {
    if (row.some(field => field.length > 0)) {
        rows.push(row)
    }
}

function consumeQuotedStep(state, text) {
    const { i } = state
    if (text[i] !== '"') {
        state.currentField += text[i]
        state.i += 1
        return
    }
    if (text[i + 1] === '"') {
        state.currentField += '"'
        state.i += 2
        return
    }
    state.inQuotes = false
    state.i += 1
}

function consumeUnquotedStep(state, text, delim) {
    const { i } = state
    const char = text[i]
    if (char === '"') {
        state.inQuotes = true
        state.i += 1
        return
    }
    if (char === delim) {
        state.currentRow.push(state.currentField.trim())
        state.currentField = ''
        state.i += 1
        return
    }
    if (char === '\r' || char === '\n') {
        const isCrlf = char === '\r' && text[i + 1] === '\n'
        state.currentRow.push(state.currentField.trim())
        pushNonEmptyRow(state.rows, state.currentRow)
        state.currentField = ''
        state.currentRow = []
        state.i += isCrlf ? 2 : 1
        return
    }
    state.currentField += char
    state.i += 1
}

/**
 * Parse delimited text with RFC 4180 quotes support.
 */
export function parseDelimitedText(text, delimiter = null) {
    if (!text || typeof text !== 'string') return []
    const delim = delimiter || detectDelimiter(text)

    const state = {
        rows: [],
        currentRow: [],
        currentField: '',
        inQuotes: false,
        i: 0,
    }

    while (state.i < text.length) {
        if (state.inQuotes) {
            consumeQuotedStep(state, text)
        } else {
            consumeUnquotedStep(state, text, delim)
        }
    }

    if (state.currentField.length > 0 || state.currentRow.length > 0) {
        state.currentRow.push(state.currentField.trim())
        pushNonEmptyRow(state.rows, state.currentRow)
    }

    return state.rows
}

/**
 * Known software export profiles and detection rules.
 */
export const SOFTWARE_PROFILES = {
    PCDJ_KARAOKI: {
        id: 'pcdj_karaoki',
        name: 'PCDJ Karaoki (TSV)',
        match: (headers, delimiter) => {
            const h = headers.map(normalizeHeader)
            const hasArtist = h.some(s => s === 'artist')
            const hasTitle = h.some(s => s === 'title')
            const hasKaraokiDisc = h.some(s => s === 'diskid' || s === 'discid')
            const hasPath = h.some(s => s === 'path' || s === 'filename')
            return (delimiter === '\t' || hasKaraokiDisc) && hasArtist && hasTitle
        },
        columnMapping: (headers) => {
            return guessColumnMapping(headers, {
                artist: ['artist'],
                title: ['title'],
                disc: ['diskid', 'discid', 'disc'],
                genre: ['genre'],
                location: ['filename', 'path']
            })
        }
    },
    SONGBOOKDB: {
        id: 'songbookdb',
        name: 'SongbookDB Pal Export',
        match: (headers) => {
            const h = headers.map(normalizeHeader)
            const hasArtist = h.some(s => s === 'artist')
            const hasTitle = h.some(s => s === 'song' || s === 'title')
            const hasSongbookDbDisc = h.some(s => s.includes('disc id') || s === 'discid' || s === 'disc')
            const hasTags = h.some(s => s === 'tags' || s.includes('date added') || s === 'dateadded')
            return hasArtist && hasTitle && (hasSongbookDbDisc || hasTags)
        },
        columnMapping: (headers) => {
            return guessColumnMapping(headers, {
                artist: ['artist'],
                title: ['song', 'title'],
                disc: ['disc id', 'discid', 'disc', 'track/disc', 'disc/track'],
                genre: ['tags', 'genre'],
                location: ['path', 'filename']
            })
        }
    },
    MTU_HOSTER: {
        id: 'mtu_hoster',
        name: 'MTU Hoster Database Export',
        match: (headers) => {
            const h = headers.map(normalizeHeader)
            const hasBookId = h.some(s => s === 'bookid' || s === 'book_id' || s === 'book id')
            const hasBrand = h.some(s => s === 'brand')
            const hasArtist = h.some(s => s === 'artist')
            return (hasBookId || hasBrand) && hasArtist
        },
        columnMapping: (headers) => {
            return guessColumnMapping(headers, {
                artist: ['artist'],
                title: ['title', 'song'],
                disc: ['bookid', 'book id', 'disc'],
                source: ['brand', 'source', 'publisher', 'provider'],
                location: ['filename', 'path']
            })
        }
    },
    OPENKJ: {
        id: 'openkj',
        name: 'OpenKJ Catalog Export',
        match: (headers) => {
            const h = headers.map(normalizeHeader)
            const hasArtist = h.some(s => s === 'artist')
            const hasTitle = h.some(s => s === 'title')
            const hasSongId = h.some(s => s === 'song_id' || s === 'songid')
            return hasArtist && hasTitle && hasSongId
        },
        columnMapping: (headers) => {
            return guessColumnMapping(headers, {
                artist: ['artist'],
                title: ['title'],
                disc: ['song_id', 'disc'],
                location: ['filename', 'path']
            })
        }
    },
    GENERIC: {
        id: 'generic',
        name: 'Standard Spreadsheet / CSV',
        match: () => true,
        columnMapping: (headers) => guessColumnMapping(headers)
    }
}

function normalizeHeader(str) {
    return String(str || '').trim().toLowerCase().replace(/[\-_]/g, ' ')
}

/**
 * Intelligent column mapping deduction from header names.
 */
export function guessColumnMapping(headers, customPriority = {}) {
    const norm = headers.map((h, index) => ({ original: h, normalized: normalizeHeader(h), index }))

    const claimed = new Set()
    const findMatch = (priorities) => {
        for (const target of priorities) {
            const exact = norm.find(item => !claimed.has(item.index) && item.normalized === target)
            if (exact) {
                claimed.add(exact.index)
                return exact.index
            }
        }
        for (const target of priorities) {
            const partial = norm.find(item => !claimed.has(item.index) && item.normalized.includes(target))
            if (partial) {
                claimed.add(partial.index)
                return partial.index
            }
        }
        return -1
    }

    const artist = findMatch(customPriority.artist || ['artist', 'performer', 'singer', 'band', 'creator'])
    const title = findMatch(customPriority.title || ['title', 'song', 'song title', 'track title', 'name'])
    // Claim private file/path columns before public source/video_url so headers like
    // "Source Path" or "Video File" can never leak local filesystem paths publicly.
    const location = findMatch(customPriority.location || ['filepath', 'file path', 'filename', 'file name', 'path', 'location', 'file'])
    const disc = findMatch(customPriority.disc || ['disc', 'disk', 'discid', 'diskid', 'disc id', 'cdg', 'bookid', 'book id', 'catalog'])
    const source = findMatch(customPriority.source || ['source', 'publisher', 'provider', 'manufacturer', 'brand'])
    const track = findMatch(customPriority.track || ['track', 'track #', 'track no', 'track number', 'tracknum'])
    const genre = findMatch(customPriority.genre || ['genre', 'tags', 'category', 'style'])
    const videoUrl = findMatch(customPriority.videoUrl || ['video url', 'videourl', 'youtube url', 'youtube', 'url'])

    return {
        artistIndex: artist,
        titleIndex: title,
        discIndex: disc,
        sourceIndex: source,
        trackIndex: track,
        genreIndex: genre,
        videoUrlIndex: videoUrl,
        locationIndex: location,
    }
}

/**
 * Detect software profile from headers and delimiter.
 */
export function detectSoftwareProfile(headers, delimiter) {
    if (SOFTWARE_PROFILES.PCDJ_KARAOKI.match(headers, delimiter)) return SOFTWARE_PROFILES.PCDJ_KARAOKI
    if (SOFTWARE_PROFILES.SONGBOOKDB.match(headers, delimiter)) return SOFTWARE_PROFILES.SONGBOOKDB
    if (SOFTWARE_PROFILES.MTU_HOSTER.match(headers, delimiter)) return SOFTWARE_PROFILES.MTU_HOSTER
    if (SOFTWARE_PROFILES.OPENKJ.match(headers, delimiter)) return SOFTWARE_PROFILES.OPENKJ
    return SOFTWARE_PROFILES.GENERIC
}

/**
 * Generates a stable deterministic fingerprint for a song.
 */
export function generateSongFingerprint(artist, title, disc, rowIdx) {
    const clean = (s) => String(s || '').trim().toLowerCase().replace(/[^a-z0-9]/g, '')
    const base = `${clean(artist)}:${clean(title)}:${clean(disc)}`
    if (base.length > 2) {
        return `fp-${base}`
    }
    return `fp-row-${rowIdx}-${Date.now()}`
}

function readCell(row, index) {
    if (index < 0 || !row[index]) return ''
    return row[index].trim()
}

function dedupeFingerprint(seenFingerprints, baseFingerprint) {
    const prevCount = seenFingerprints.get(baseFingerprint) || 0
    const nextCount = prevCount + 1
    seenFingerprints.set(baseFingerprint, nextCount)
    return prevCount === 0 ? baseFingerprint : `${baseFingerprint}-v${nextCount}`
}

function rowToSong(row, idx, mapping, seenFingerprints) {
    const { artistIndex, titleIndex, discIndex, sourceIndex = -1, trackIndex, genreIndex, videoUrlIndex = -1, locationIndex } = mapping
    const artist = readCell(row, artistIndex)
    const title = readCell(row, titleIndex)
    if (!artist && !title) return null // Skip empty rows

    const disc = readCell(row, discIndex)
    const source = readCell(row, sourceIndex)
    const track = parseInt(readCell(row, trackIndex), 10) || 0
    const genre = readCell(row, genreIndex)
    const rawVideoUrl = readCell(row, videoUrlIndex)
    const videoUrl = /^https?:\/\//i.test(rawVideoUrl) ? rawVideoUrl : ''
    const location = readCell(row, locationIndex)

    const fallbackKey = disc || source || videoUrl || location
    const baseFingerprint = generateSongFingerprint(artist, title, fallbackKey, idx)
    const fingerprint = dedupeFingerprint(seenFingerprints, baseFingerprint)

    return {
        artist: artist || 'Unknown Artist',
        title: title || 'Unknown Title',
        disc,
        source,
        track_number: track,
        genre,
        video_url: videoUrl,
        location,
        fingerprint,
        is_available: true,
        confidence: (artist && title) ? 'high' : 'medium',
    }
}

/**
 * Convert parsed rows and mapping into clean MicUp Song models ready for sync.
 */
export function rowsToSongs(rows, mapping, hasHeader = true) {
    const dataRows = hasHeader ? rows.slice(1) : rows
    const songs = []
    const seenFingerprints = new Map()

    dataRows.forEach((row, idx) => {
        const song = rowToSong(row, idx, mapping, seenFingerprints)
        if (song) songs.push(song)
    })

    return songs
}
