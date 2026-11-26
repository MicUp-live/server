export function removeDiacritics(str) {
    return (str || '')
        .normalize('NFD')
        .replace(/[\u0300-\u036f]/g, '')
}

export function uninvertArtist(artist) {
    const s = (artist || '').trim()
    if (!s.includes(',')) return s
    const parts = s.split(',')
    if (parts.length !== 2) return s
    const first = parts[0].trim()
    const second = parts[1].trim()
    const lowerSecond = second.toLowerCase()
    if (lowerSecond === 'the' || lowerSecond === 'a' || lowerSecond === 'an') {
        return `${second} ${first}`
    }
    if (second.split(/\s+/).length <= 2 && first.split(/\s+/).length <= 2) {
        return `${second} ${first}`
    }
    return s
}

/**
 * Moves a trailing article (", The", ", A", ", An") to the front of a song title
 * (e.g. "A Team, The" -> "The A Team") while preserving trailing qualifiers like "(Acoustic)".
 */
export function uninvertTitle(title) {
    const s = (title || '').trim()
    if (!s.includes(',')) return s
    const match = s.match(/^(.+?),\s*(the|a|an)(\s*(?:[\(\[].*)?)$/i)
    if (!match) return s
    const [, main, article, suffix] = match
    return `${article} ${main.trim()}${suffix || ''}`.trim()
}

export const BASE_ARTIST_ALIASES = {
    'ccr': 'creedence clearwater revival',
    'elo': 'electric light orchestra',
    'csny': 'crosby stills nash and young',
    'csn': 'crosby stills and nash',
    'omd': 'orchestral manoeuvres in the dark',
    'orchestral maneuvers in the dark': 'orchestral manoeuvres in the dark',
    'boc': 'blue oyster cult',
    'guns n roses': 'guns and roses',
    'guns n  roses': 'guns and roses',
    'snoop doggy dogg': 'snoop dogg',
    'kool and the gang': 'kool and the gang',
    'kool the gang': 'kool and the gang',
    'daryl hall and john oates': 'hall and oates',
    'daryl hall john oates': 'hall and oates',
    'hall and oates': 'hall and oates',
    'huey lewis and the news': 'huey lewis and the news',
    'huey lewis the news': 'huey lewis and the news',
    'kc and the sunshine band': 'kc and the sunshine band',
    'kc the sunshine band': 'kc and the sunshine band',
    'doctor dre': 'dr dre',
}

export const ARTIST_ALIASES = {
    ...BASE_ARTIST_ALIASES,
    'matchbox 20': 'matchbox twenty',
    'n sync': 'nsync',
    'meatloaf': 'meat loaf',
    '2 pac': '2pac',
    'tupac': '2pac',
    'rem': 'r e m',
    'p diddy': 'diddy',
}

export function stripBaseNoiseTags(title) {
    return (title || '')
        .replace(/\s*[\(\[](karaoke version|karaoke|instrumental|backing track|remastered|remastered \d{4}|original mix|album version|without backing vocals|with backing vocals|no backing vocals|with harmony|no harmony|with multiplex lead vocal|multiplex|original male key|original female key|original key|for solo male|for solo female|for solo singer|end cut down|\d+\s+semitones?\s+(up|down)|(one|two|three|four|five|six)\s+semitones?\s+(up|down)|\d{4}\s+remake)[\)\]]/gi, '')
        .replace(/\s*\[[a-z]{2,5}\d{3,5}[a-z]?(?:-\d{1,2}){0,2}\]/gi, '')
        .trim()
}

export function stripNoiseTags(title) {
    return stripBaseNoiseTags(title)
        .replace(/\s*[\(\[](radio version|radio edit|single version|clean version|explicit|mp3\s*\+\s*cdg[^)\]]*)[\)\]]/gi, '')
        .trim()
}

export function normalizeKey(str) {
    return removeDiacritics(str)
        .toLowerCase()
        .replace(/&/g, ' and ')
        .replace(/\+/g, ' and ')
        .replace(/[^a-z0-9\s]/g, ' ')
        .replace(/\s+/g, ' ')
        .trim()
}

export function baseNormalizeArtist(artist) {
    let s = uninvertArtist(artist)
    s = s.replace(/\bp!nk\b/gi, 'pink')
    s = s.replace(/\$/g, 's')
    const norm = normalizeKey(s)
    return BASE_ARTIST_ALIASES[norm] || norm
}

export function normalizeArtist(artist) {
    let s = uninvertArtist(artist)
    s = s.replace(/\bp!nk\b/gi, 'pink')
    s = s.replace(/\$/g, 's')
    const norm = normalizeKey(s)
    return ARTIST_ALIASES[norm] || norm
}

export function normalizeTitle(title) {
    return normalizeKey(stripNoiseTags(uninvertTitle(title)))
}

export function baseCompositeKey(artist, title) {
    return `${baseNormalizeArtist(artist)}:::${normalizeKey(stripBaseNoiseTags(title))}`
}

export function compositeKey(artist, title) {
    return `${normalizeArtist(artist)}:::${normalizeTitle(title)}`
}

export function candidateCompositeKeys(artist, title) {
    const baseArtist = baseNormalizeArtist(artist)
    const expArtist = normalizeArtist(artist)
    const baseTitle = normalizeKey(stripBaseNoiseTags(title))
    const expTitle = normalizeTitle(title)
    const candidates = [
        `${baseArtist}:::${baseTitle}`,
        `${baseArtist}:::${expTitle}`,
        `${expArtist}:::${baseTitle}`,
        `${expArtist}:::${expTitle}`,
    ]
    return [...new Set(candidates)]
}

export function normalizeDiscCode(code) {
    const c = (code || '').trim().toUpperCase()
    const m = c.match(/^([A-Z0-9]+)[-_]+(\d+)$/)
    if (m) {
        return `${m[1]}-${m[2].padStart(2, '0')}`
    }
    return c.replace(/[-_]+/g, '-')
}

export function boundedLevenshtein(s1, s2, maxDist = 2) {
    if (s1 === s2) return 0
    if (!s1) return s2.length <= maxDist ? s2.length : maxDist + 1
    if (!s2) return s1.length <= maxDist ? s1.length : maxDist + 1

    const m = s1.length
    const n = s2.length
    if (Math.abs(m - n) > maxDist) return maxDist + 1

    let prevRow = new Array(n + 1)
    let currRow = new Array(n + 1)
    for (let j = 0; j <= n; j++) prevRow[j] = j

    for (let i = 1; i <= m; i++) {
        const start = Math.max(1, i - maxDist)
        const end = Math.min(n, i + maxDist)
        currRow[0] = i
        if (start > 1) currRow[start - 1] = maxDist + 1

        let rowMin = i <= maxDist ? i : maxDist + 1
        const c1 = s1[i - 1]
        for (let j = start; j <= end; j++) {
            const cost = c1 === s2[j - 1] ? 0 : 1
            const above = j <= i - 1 + maxDist ? prevRow[j] + 1 : maxDist + 1
            const diag = prevRow[j - 1] + cost
            const left = currRow[j - 1] + 1
            const val = Math.min(left, above, diag)
            currRow[j] = val
            if (val < rowMin) rowMin = val
        }
        if (end < n) currRow[end + 1] = maxDist + 1
        if (rowMin > maxDist) return maxDist + 1

        const temp = prevRow
        prevRow = currRow
        currRow = temp
    }

    return prevRow[n] <= maxDist ? prevRow[n] : maxDist + 1
}

export function levenshteinDistance(s1, s2) {
    if (s1 === s2) return 0
    if (!s1) return s2.length
    if (!s2) return s1.length

    const m = s1.length
    const n = s2.length
    let prevRow = new Array(n + 1)
    let currRow = new Array(n + 1)

    for (let j = 0; j <= n; j++) prevRow[j] = j

    for (let i = 1; i <= m; i++) {
        currRow[0] = i
        const c1 = s1[i - 1]
        for (let j = 1; j <= n; j++) {
            const c2 = s2[j - 1]
            const cost = c1 === c2 ? 0 : 1
            currRow[j] = Math.min(
                currRow[j - 1] + 1,
                prevRow[j] + 1,
                prevRow[j - 1] + cost
            )
        }
        const temp = prevRow
        prevRow = currRow
        currRow = temp
    }

    return prevRow[n]
}
