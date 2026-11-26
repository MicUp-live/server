import { KNOWN_DISC_CODE_SOURCE, resolveProvider } from './karaokeProvider.js'
import { candidateCompositeKeys, normalizeArtist } from './songNormalize.js'

const BRACKETED_DISC_RE = new RegExp(`\\s*[\\(\\[]\\s*(${KNOWN_DISC_CODE_SOURCE})\\s*[\\)\\]]`, 'i')
const LEADING_DISC_RE = new RegExp(`^\\s*(${KNOWN_DISC_CODE_SOURCE})\\s*(?:[-–—]+|_+)\\s*`, 'i')
const TRAILING_DISC_RE = new RegExp(`\\s*(?:[-–—]+|_+)\\s*(${KNOWN_DISC_CODE_SOURCE})\\s*$`, 'i')
const FORMAT_CLUTTER_RE = /\s*[\(\[](?:mp3\s*\+\s*cdg|cdg\s*\+\s*mp3|mp3\s*\+\s*g|karaoke\s*cdg)(?:\s*[-–—]\s*karaoke)?[\)\]]/gi
const SPACED_DELIM_RE = /\s+[-–—]\s+/
const UNSPACED_DELIM_RE = /[-–—]/

function looksLikeDiscCode(segment) {
    const s = (segment || '').trim()
    if (!s || s.length > 24 || /\s/.test(s)) return false
    return /[a-z]/i.test(s) && /\d/.test(s)
}

/**
 * Extracts a recognized karaoke manufacturer disc code from the raw filename stem
 * BEFORE delimiter splitting or underscore replacement, preserving codes like
 * "SC8101_02", "CBEP457-3-08", and "MM6380B-01".
 */
export function extractDiscFromRawStem(stem) {
    const raw = (stem || '').trim()
    if (!raw) return { disc: '', cleaned: '' }

    const bracketMatch = raw.match(BRACKETED_DISC_RE)
    if (bracketMatch && bracketMatch.index !== undefined) {
        const disc = bracketMatch[1].trim().toUpperCase()
        const cleaned = `${raw.slice(0, bracketMatch.index)} ${raw.slice(bracketMatch.index + bracketMatch[0].length)}`.trim()
        return { disc, cleaned }
    }

    const leadingMatch = raw.match(LEADING_DISC_RE)
    if (leadingMatch) {
        const rest = raw.slice(leadingMatch[0].length).trim()
        if (rest) {
            return { disc: leadingMatch[1].trim().toUpperCase(), cleaned: rest }
        }
    }

    const trailingMatch = raw.match(TRAILING_DISC_RE)
    if (trailingMatch && trailingMatch.index !== undefined) {
        const rest = raw.slice(0, trailingMatch.index).trim()
        if (rest) {
            return { disc: trailingMatch[1].trim().toUpperCase(), cleaned: rest }
        }
    }

    return { disc: '', cleaned: raw }
}

function pickBestCandidate(parts, extractedDisc, lookup) {
    if (parts.length === 0) return { disc: extractedDisc, artist: '', title: '' }
    if (parts.length === 1) return { disc: extractedDisc, artist: '', title: parts[0] }

    const candidates = []
    if (extractedDisc) {
        const hasTrailingDownloadId = parts.length >= 3 && /^\d{5,8}$/.test(parts[parts.length - 1])
        const baseParts = hasTrailingDownloadId ? parts.slice(0, -1) : parts
        candidates.push({
            disc: extractedDisc,
            artist: baseParts[0],
            title: baseParts.slice(1).join(' - '),
            order: 0,
        })
        candidates.push({
            disc: extractedDisc,
            title: baseParts.slice(0, -1).join(' - '),
            artist: baseParts[baseParts.length - 1],
            order: 1,
        })
    } else if (parts.length >= 3) {
        const hasTrailingDownloadId = /^\d{5,8}$/.test(parts[parts.length - 1])
        const baseParts = hasTrailingDownloadId ? parts.slice(0, -1) : parts
        // Put artist-title first so unknown tracks with digits (e.g. "Local 5 - Song - Demo"
        // or "U2 - One - 443579") stay artist-title unless reference lookup confirms a disc layout.
        candidates.push({
            disc: '',
            artist: baseParts[0],
            title: baseParts.slice(1).join(' - '),
            order: 0,
        })
        candidates.push({
            disc: '',
            title: baseParts.slice(0, -1).join(' - '),
            artist: baseParts[baseParts.length - 1],
            order: 1,
        })
        if (looksLikeDiscCode(parts[0])) {
            const discParts = (hasTrailingDownloadId && parts.length >= 4) ? parts.slice(0, -1) : parts
            candidates.push({
                disc: discParts[0],
                artist: discParts[1],
                title: discParts.slice(2).join(' - '),
                order: 2,
            })
            candidates.push({
                disc: discParts[0],
                title: discParts.slice(1, -1).join(' - '),
                artist: discParts[discParts.length - 1],
                order: 3,
            })
        }
    } else {
        candidates.push({
            disc: '',
            artist: parts[0],
            title: parts[1],
            order: 0,
        })
        candidates.push({
            disc: '',
            title: parts[0],
            artist: parts[1],
            order: 1,
        })
    }

    let best = candidates[0]
    let bestScore = -1
    for (const c of candidates) {
        let score = 0
        if (lookup) {
            if (candidateCompositeKeys(c.artist, c.title).some(k => lookup.hasTrack(k))) {
                score += 100
            } else if (lookup.hasArtist(normalizeArtist(c.artist))) {
                score += 50
            }
        }
        if (score > bestScore || (score === bestScore && c.order < best.order)) {
            bestScore = score
            best = c
        }
    }
    return best
}

/**
 * Parses a single karaoke filename entry locally into a standardized MicUp song object.
 * Supports explicit patterns ('artist-title', 'title-artist', 'disc-artist-title')
 * and per-file 'auto' discovery guided by optional O(1) reference lookups.
 */
export function parseFolderFileEntry(file, pattern = 'auto', lookup = null) {
    const name = file?.name || ''
    const dotIdx = name.lastIndexOf('.')
    let stem = dotIdx > 0 ? name.slice(0, dotIdx) : name

    // Strip download clutter (e.g. "(MP3+CDG - Karaoke)") first, then extract any
    // known manufacturer disc code before checking for a trailing numeric download ID.
    stem = stem.replace(FORMAT_CLUTTER_RE, '').trim()

    let disc = ''
    const extracted = extractDiscFromRawStem(stem)
    if (extracted.disc) {
        disc = extracted.disc
        stem = extracted.cleaned
    }

    let partsRaw = SPACED_DELIM_RE.test(stem)
        ? stem.split(SPACED_DELIM_RE)
        : stem.split(UNSPACED_DELIM_RE)
    if (partsRaw.length === 1) {
        partsRaw = stem.split('_')
    }
    const parts = partsRaw
        .map(p => p.replace(/_/g, ' ').replace(/\s+/g, ' ').trim())
        .filter(Boolean)

    // For explicit patterns, strip a trailing 5-8 digit download ID (e.g. "Artist - Title - 443579")
    // only when at least two non-disc (artist + title) segments precede it. In 'auto' mode,
    // pickBestCandidate handles trailing download IDs once during candidate scoring.
    if (pattern !== 'auto') {
        const minPartsForDownloadId = (!disc && pattern === 'disc-artist-title') ? 4 : 3
        if (
            parts.length >= minPartsForDownloadId &&
            /^\d{5,8}$/.test(parts[parts.length - 1])
        ) {
            parts.pop()
        }
    }

    let artist = ''
    let title = ''

    if (pattern === 'auto') {
        const chosen = pickBestCandidate(parts, disc, lookup)
        disc = chosen.disc
        artist = chosen.artist
        title = chosen.title
    } else if (pattern === 'title-artist') {
        if (parts.length > 1) {
            title = parts.slice(0, -1).join(' - ')
            artist = parts[parts.length - 1]
        } else if (parts.length === 1) {
            title = parts[0]
        }
    } else if (pattern === 'disc-artist-title') {
        if (disc) {
            // Manufacturer disc was already extracted from the raw stem
            if (parts.length > 0) artist = parts[0]
            if (parts.length > 1) title = parts.slice(1).join(' - ')
        } else if (parts.length >= 3) {
            disc = parts[0]
            artist = parts[1]
            title = parts.slice(2).join(' - ')
        } else if (parts.length === 2) {
            // Only 2 segments and no recognized disc code: treat as Artist - Title
            // rather than shifting Artist into Disc and leaving Title blank.
            if (looksLikeDiscCode(parts[0])) {
                disc = parts[0]
                artist = parts[1]
            } else {
                artist = parts[0]
                title = parts[1]
            }
        } else if (parts.length === 1) {
            title = parts[0]
        }
    } else {
        // Explicit: artist-title
        if (parts.length > 0) artist = parts[0]
        if (parts.length > 1) title = parts.slice(1).join(' - ')
    }

    const source = resolveProvider(disc, '') || ''
    const location = file?.path || name
    const confidence = (artist && title) ? 'high' : 'low'

    return {
        artist,
        title,
        disc,
        source,
        genre: '',
        video_url: '',
        location,
        fingerprint: `${name}-${file?.size ?? 0}`,
        is_available: true,
        confidence,
    }
}

/**
 * Parses an array of folder file metadata objects locally in linear time with zero HTTP calls.
 */
export function parseFolderFilesLocal(fileList = [], pattern = 'auto', lookup = null) {
    if (!Array.isArray(fileList) || fileList.length === 0) return []
    const results = new Array(fileList.length)
    for (let i = 0; i < fileList.length; i++) {
        results[i] = parseFolderFileEntry(fileList[i], pattern, lookup)
    }
    return results
}
