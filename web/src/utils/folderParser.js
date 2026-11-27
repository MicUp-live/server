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

function buildCandidates(parts, extractedDisc) {
    const hasTrailingDownloadId = parts.length >= 3 && /^\d{5,8}$/.test(parts[parts.length - 1])
    const baseParts = hasTrailingDownloadId ? parts.slice(0, -1) : parts
    // Put artist-title first so unknown tracks with digits (e.g. "Local 5 - Song - Demo"
    // or "U2 - One - 443579") stay artist-title unless reference lookup confirms a disc layout.
    const candidates = [
        {
            disc: extractedDisc,
            artist: baseParts[0],
            title: baseParts.slice(1).join(' - '),
            order: 0,
        },
        {
            disc: extractedDisc,
            title: baseParts.slice(0, -1).join(' - '),
            artist: baseParts[baseParts.length - 1],
            order: 1,
        },
    ]

    if (!extractedDisc && parts.length >= 3 && looksLikeDiscCode(parts[0])) {
        const discParts = hasTrailingDownloadId && parts.length >= 4 ? parts.slice(0, -1) : parts
        candidates.push(
            {
                disc: discParts[0],
                artist: discParts[1],
                title: discParts.slice(2).join(' - '),
                order: 2,
            },
            {
                disc: discParts[0],
                title: discParts.slice(1, -1).join(' - '),
                artist: discParts[discParts.length - 1],
                order: 3,
            },
        )
    }

    return candidates
}

function scoreCandidate(candidate, lookup) {
    if (!lookup) return 0
    if (candidateCompositeKeys(candidate.artist, candidate.title).some(k => lookup.hasTrack(k))) {
        return 100
    }
    if (lookup.hasArtist(normalizeArtist(candidate.artist))) {
        return 50
    }
    return 0
}

function pickBestCandidate(parts, extractedDisc, lookup) {
    if (parts.length === 0) return { disc: extractedDisc, artist: '', title: '' }
    if (parts.length === 1) return { disc: extractedDisc, artist: '', title: parts[0] }

    const candidates = buildCandidates(parts, extractedDisc)
    let best = candidates[0]
    let bestScore = -1
    for (const c of candidates) {
        const score = scoreCandidate(c, lookup)
        if (score > bestScore || (score === bestScore && c.order < best.order)) {
            bestScore = score
            best = c
        }
    }
    return best
}

function splitStemParts(stem) {
    let partsRaw = SPACED_DELIM_RE.test(stem)
        ? stem.split(SPACED_DELIM_RE)
        : stem.split(UNSPACED_DELIM_RE)
    if (partsRaw.length === 1) {
        partsRaw = stem.split('_')
    }
    return partsRaw
        .map(p => p.replace(/_/g, ' ').replace(/\s+/g, ' ').trim())
        .filter(Boolean)
}

// For explicit patterns, strip a trailing 5-8 digit download ID (e.g. "Artist - Title - 443579")
// only when at least two non-disc (artist + title) segments precede it. In 'auto' mode,
// pickBestCandidate handles trailing download IDs once during candidate scoring.
function stripTrailingDownloadId(parts, pattern, hasDisc) {
    if (pattern === 'auto') return
    const minPartsForDownloadId = !hasDisc && pattern === 'disc-artist-title' ? 4 : 3
    if (parts.length >= minPartsForDownloadId && /^\d{5,8}$/.test(parts[parts.length - 1])) {
        parts.pop()
    }
}

function parseTitleArtist(parts, disc) {
    if (parts.length > 1) {
        return { disc, title: parts.slice(0, -1).join(' - '), artist: parts[parts.length - 1] }
    }
    if (parts.length === 1) {
        return { disc, title: parts[0], artist: '' }
    }
    return { disc, title: '', artist: '' }
}

function parseArtistTitle(parts, disc) {
    if (parts.length > 1) {
        return { disc, artist: parts[0], title: parts.slice(1).join(' - ') }
    }
    if (parts.length === 1) {
        return { disc, artist: parts[0], title: '' }
    }
    return { disc, artist: '', title: '' }
}

function parseDiscArtistTitle(parts, disc) {
    if (disc) {
        // Manufacturer disc was already extracted from the raw stem
        return parseArtistTitle(parts, disc)
    }
    if (parts.length >= 3) {
        return { disc: parts[0], artist: parts[1], title: parts.slice(2).join(' - ') }
    }
    if (parts.length === 2) {
        // Only 2 segments and no recognized disc code: treat as Artist - Title
        // rather than shifting Artist into Disc and leaving Title blank.
        if (looksLikeDiscCode(parts[0])) {
            return { disc: parts[0], artist: parts[1], title: '' }
        }
        return { disc: '', artist: parts[0], title: parts[1] }
    }
    if (parts.length === 1) {
        return { disc: '', artist: '', title: parts[0] }
    }
    return { disc: '', artist: '', title: '' }
}

const PATTERN_PARSERS = {
    'auto': (parts, disc, lookup) => pickBestCandidate(parts, disc, lookup),
    'title-artist': (parts, disc) => parseTitleArtist(parts, disc),
    'disc-artist-title': (parts, disc) => parseDiscArtistTitle(parts, disc),
    'artist-title': (parts, disc) => parseArtistTitle(parts, disc),
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

    const extracted = extractDiscFromRawStem(stem)
    const disc = extracted.disc
    if (disc) {
        stem = extracted.cleaned
    }

    const parts = splitStemParts(stem)
    stripTrailingDownloadId(parts, pattern, Boolean(disc))

    const resolveParts = PATTERN_PARSERS[pattern] || PATTERN_PARSERS['artist-title']
    const resolved = resolveParts(parts, disc, lookup)

    const source = resolveProvider(resolved.disc, '') || ''
    const location = file?.path || name
    const confidence = resolved.artist && resolved.title ? 'high' : 'low'

    return {
        artist: resolved.artist,
        title: resolved.title,
        disc: resolved.disc,
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
