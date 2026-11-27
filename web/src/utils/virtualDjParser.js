import { rowsToSongs } from './csvParser.js'

const XML_ENTITIES = {
    amp: '&',
    lt: '<',
    gt: '>',
    quot: '"',
    apos: "'",
}

export function decodeXmlEntities(str = '') {
    return str.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (match, entity) => {
        if (entity[0] === '#') {
            const isHex = entity[1] === 'x' || entity[1] === 'X'
            const code = parseInt(isHex ? entity.slice(2) : entity.slice(1), isHex ? 16 : 10)
            return Number.isFinite(code) && code >= 0 && code <= 0x10ffff
                ? String.fromCodePoint(code)
                : match
        }
        return XML_ENTITIES[entity] || match
    })
}

function parseXmlAttributes(attrText = '') {
    const attrs = {}
    const attrRegex = /([A-Za-z_][A-Za-z0-9_:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')/g
    let m
    while ((m = attrRegex.exec(attrText)) !== null) {
        attrs[m[1]] = decodeXmlEntities(m[2] !== undefined ? m[2] : (m[3] || ''))
    }
    return attrs
}

/**
 * Finds the end index of an XML start tag's `>` outside of single/double quotes.
 * Returns { tagEnd, selfClosing } or null if unclosed.
 */
function findTagEnd(text, startIdx) {
    let quote = null
    for (let i = startIdx; i < text.length; i++) {
        const ch = text[i]
        if (quote) {
            if (ch === quote) quote = null
        } else if (ch === '"' || ch === "'") {
            quote = ch
        } else if (ch === '>') {
            return { tagEnd: i, selfClosing: i > startIdx && text[i - 1] === '/' }
        }
    }
    return null
}

function extractFirstChildTagAttrs(inner, tagName) {
    const openRe = new RegExp(`<${tagName}\\b`, 'i')
    const m = openRe.exec(inner)
    if (!m) return {}
    const attrStart = m.index + m[0].length
    const end = findTagEnd(inner, attrStart)
    if (!end) return {}
    const rawAttrs = inner.slice(attrStart, end.selfClosing ? end.tagEnd - 1 : end.tagEnd)
    return parseXmlAttributes(rawAttrs)
}

function fallbackFromFilePath(filePath = '') {
    const base = filePath.split(/[/\\]/).pop() || ''
    const stem = base.replace(/\.[A-Za-z0-9]{2,5}$/, '').trim()
    const parts = stem.split(/\s+-\s+/)
    if (parts.length >= 2) {
        return { artist: parts[0].trim(), title: parts.slice(1).join(' - ').trim() }
    }
    return { artist: '', title: stem }
}

export function isVirtualDJDatabaseXML(text = '', fileName = '') {
    if (typeof text !== 'string') return false
    const head = text.slice(0, 8192).replace(/<!--[\s\S]*?-->/g, '')
    if (/<VirtualDJ_Database\b/i.test(head)) return true
    return fileName.toLowerCase().endsWith('.xml') && /<Song\b[^>]*FilePath\s*=/i.test(head)
}

function isExplicitKaraokeTrack(entry) {
    if ((entry.flag & 32) !== 0) return true
    const lowerPath = entry.filePath.toLowerCase()
    if (/\.(cdg|kar|kfn)$/.test(lowerPath)) return true
    if (entry.genre.toLowerCase().includes('karaoke')) return true
    return false
}

function extractSongInner(cleaned, end, songCloseRe) {
    const nextStart = end.tagEnd + 1
    if (end.selfClosing) {
        return { inner: '', nextOpenIndex: nextStart }
    }
    songCloseRe.lastIndex = nextStart
    const closeMatch = songCloseRe.exec(cleaned)
    // Bound inner search before any unclosed sibling <Song> to stay linear-time.
    const nextOpenPeek = cleaned.indexOf('<Song', nextStart)
    if (closeMatch && (nextOpenPeek === -1 || closeMatch.index < nextOpenPeek)) {
        return {
            inner: cleaned.slice(nextStart, closeMatch.index),
            nextOpenIndex: closeMatch.index + closeMatch[0].length,
        }
    }
    return { inner: '', nextOpenIndex: nextStart }
}

function buildSongEntry(songAttrs, tagAttrs) {
    const filePath = (songAttrs.FilePath || '').trim()
    const flag = parseInt(songAttrs.Flag || '0', 10) || 0
    let artist = (tagAttrs.Author || tagAttrs.Artist || '').trim()
    let title = (tagAttrs.Title || '').trim()
    const genre = (tagAttrs.Genre || '').trim()
    const source = (tagAttrs.Label || '').trim()

    if (!artist && !title && filePath) {
        const fb = fallbackFromFilePath(filePath)
        artist = fb.artist
        title = fb.title
    }
    if (!title && !artist) return null

    return { filePath, flag, artist, title, source, genre }
}

function selectKaraokeEntries(entries) {
    const explicitKaraoke = entries.filter(isExplicitKaraokeTrack)
    if (explicitKaraoke.length > 0) return explicitKaraoke
    const zipEntries = entries.filter(e => e.filePath.toLowerCase().endsWith('.zip'))
    if (zipEntries.length > 0) return zipEntries
    return entries
}

/**
 * Parses a VirtualDJ `database.xml` string into standardized rows & MicUp song models.
 * Filters to karaoke tracks when VirtualDJ Flag=32 / .cdg / karaoke genre markers are present
 * so hybrid DJ/KJ libraries don't import non-karaoke music tracks.
 */
export function parseVirtualDJDatabase(xmlText = '') {
    const cleaned = (typeof xmlText === 'string' ? xmlText : '').replace(/<!--[\s\S]*?-->/g, '')
    const entries = []
    const songOpenRe = /<Song\b/gi
    const songCloseRe = /<\/Song>/gi

    let openMatch
    while ((openMatch = songOpenRe.exec(cleaned)) !== null) {
        const attrStart = openMatch.index + openMatch[0].length
        const end = findTagEnd(cleaned, attrStart)
        if (!end) break

        const rawAttrs = cleaned.slice(attrStart, end.selfClosing ? end.tagEnd - 1 : end.tagEnd)
        const songAttrs = parseXmlAttributes(rawAttrs)

        const { inner, nextOpenIndex } = extractSongInner(cleaned, end, songCloseRe)
        songOpenRe.lastIndex = nextOpenIndex

        const tagAttrs = inner ? extractFirstChildTagAttrs(inner, 'Tags') : {}
        const entry = buildSongEntry(songAttrs, tagAttrs)
        if (entry) entries.push(entry)
    }

    const finalEntries = selectKaraokeEntries(entries)

    const headerRow = ['Artist', 'Title', 'Source', 'Genre', 'Location']
    const rows = [
        headerRow,
        ...finalEntries.map(e => [e.artist, e.title, e.source, e.genre, e.filePath]),
    ]
    const mapping = {
        artistIndex: 0,
        titleIndex: 1,
        discIndex: -1,
        sourceIndex: 2,
        trackIndex: -1,
        genreIndex: 3,
        videoUrlIndex: -1,
        locationIndex: 4,
    }
    const songs = rowsToSongs(rows, mapping, true)

    return {
        rows,
        headerRow,
        mapping,
        songs,
        totalScanned: entries.length,
    }
}
