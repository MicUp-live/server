import { resolveTrackProvider } from '../../../utils/karaokeProvider.js'

function normalizeGroupPart(str) {
    return (str || '').trim().toLowerCase().replace(/\s+/g, ' ')
}

export function getTrackDisc(song) {
    if (!song) return ''
    return (song.disc || song.disc_code || song.disc_id || '').trim()
}

function getVideoSourceLabel(videoUrl) {
    const url = (videoUrl || '').trim().toLowerCase()
    if (!url) return ''
    if (url.includes('youtube.com') || url.includes('youtu.be')) return 'YouTube'
    return 'Video'
}

/**
 * Strips trailing parenthetical/bracketed qualifiers to find a shared base title.
 */
function stripTrailingQualifiers(title) {
    return (title || '').trim().replace(/(?:\s*[\(\[][^\)\]]+[\)\]])+\s*$/, '').trim()
}

function escapeRegExp(str) {
    return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Computes a concise title difference for one version relative to sibling versions
 * when multiple tracks share a canonical_id but have distinct titles/modifiers.
 */
function extractTitleDifference(song, allVersions = []) {
    const rawTitle = (song?.title || song?.label || '').trim()
    if (!rawTitle || !Array.isArray(allVersions) || allVersions.length <= 1) return ''

    const normTitles = new Set(
        allVersions.map(v => normalizeGroupPart(v?.title || v?.label)).filter(Boolean)
    )
    if (normTitles.size <= 1) return ''

    const baseCandidates = allVersions
        .map(v => stripTrailingQualifiers(v?.title || v?.label || ''))
        .filter(Boolean)
        .sort((a, b) => a.length - b.length)
    const baseTitle = baseCandidates[0] || ''
    const normBase = normalizeGroupPart(baseTitle)
    const normTitle = normalizeGroupPart(rawTitle)

    if (!normBase || normTitle === normBase) return ''

    // Build a whitespace-tolerant anchored prefix regex and require a non-alphanumeric
    // boundary after the base title so "Love Songs" isn't sliced to "s" relative to "Love Song".
    const basePattern = normBase.split(' ').map(escapeRegExp).join('\\s+')
    const prefixMatch = rawTitle.match(new RegExp(`^${basePattern}(?![a-z0-9])(.*)$`, 'i'))
    if (prefixMatch) {
        const remainder = (prefixMatch[1] || '').trim()
        const parts = []
        const tokenRe = /[\(\[]([^\)\]]+)[\)\]]|([^\(\)\[\]]+)/g
        let m
        while ((m = tokenRe.exec(remainder)) !== null) {
            const cleaned = (m[1] ?? m[2] ?? '').replace(/^[\s\-–—:·•,]+|[\s\-–—:·•,]+$/g, '').trim()
            if (cleaned) parts.push(cleaned)
        }
        if (parts.length > 0) {
            return parts.join(' · ')
        }
    }

    return rawTitle
}

/**
 * Stable grouping key for collapsing duplicate backing-track versions of the same song.
 * Prefers canonical_id when available, falling back to normalized artist + title.
 */
export function songGroupKey(song) {
    if (!song) return ''
    if (song.canonical_id) {
        return `can:${song.canonical_id}`
    }
    const artist = normalizeGroupPart(song.artist)
    const title = normalizeGroupPart(song.title || song.label)
    return `txt:${artist}:::${title}`
}

function versionRank(song) {
    let score = 0
    if (song.is_custom === false) score += 2
    if (resolveTrackProvider(song)) score += 2
    if (getTrackDisc(song)) score += 1
    return score
}

/**
 * Collapses duplicate versions of the same song into a single representative card
 * while preserving the input list ordering (relevance or alphabetical).
 * Multi-version songs get a `versions` array sorted with the best-annotated track first.
 */
export function groupSongVersions(songs = []) {
    if (!Array.isArray(songs) || songs.length === 0) return []

    const orderedKeys = []
    const buckets = new Map()

    for (const song of songs) {
        if (!song) continue
        const key = songGroupKey(song)
        let bucket = buckets.get(key)
        if (!bucket) {
            bucket = []
            buckets.set(key, bucket)
            orderedKeys.push(key)
        }
        bucket.push(song)
    }

    return orderedKeys.map((key) => {
        const bucket = buckets.get(key)
        if (bucket.length === 1) {
            return bucket[0]
        }
        const sorted = [...bucket].sort((a, b) => versionRank(b) - versionRank(a))
        const primary = sorted[0]
        return {
            ...primary,
            versions: sorted,
        }
    })
}

/**
 * Returns structured distinguishing metadata for a backing-track version relative to its siblings.
 */
export function describeVersion(song, index = 0, allVersions = []) {
    if (!song) {
        return {
            provider: '',
            disc: '',
            label: `Version ${index + 1}`,
            details: [],
            hasMetadata: false,
        }
    }

    const provider = resolveTrackProvider(song)
    const disc = getTrackDisc(song)
    const titleDiff = extractTitleDifference(song, allVersions)
    const videoLabel = getVideoSourceLabel(song.video_url)

    let artistDiff = ''
    let genreDiff = ''
    if (Array.isArray(allVersions) && allVersions.length > 1) {
        const distinctArtists = new Set(allVersions.map(v => normalizeGroupPart(v?.artist)).filter(Boolean))
        if (distinctArtists.size > 1 && song.artist && song.artist.trim()) {
            artistDiff = song.artist.trim()
        }
        const distinctGenres = new Set(allVersions.map(v => normalizeGroupPart(v?.genre)).filter(Boolean))
        if (distinctGenres.size > 1 && song.genre && song.genre.trim()) {
            genreDiff = song.genre.trim()
        }
    }

    const fallbackGenre = (song.genre || '').trim()
    let label = `Version ${index + 1}`
    if (provider) {
        label = provider
    } else if (titleDiff) {
        label = titleDiff
    } else if (disc) {
        label = disc
    } else if (videoLabel) {
        label = videoLabel
    } else if (genreDiff) {
        label = genreDiff
    } else if (fallbackGenre) {
        label = `${fallbackGenre} • Version ${index + 1}`
    }

    const details = []
    if (titleDiff && titleDiff !== label) details.push(titleDiff)
    if (artistDiff) details.push(artistDiff)
    if (genreDiff && genreDiff !== label) details.push(genreDiff)
    if (videoLabel && videoLabel !== label) details.push(videoLabel)

    const hasMetadata = Boolean(provider || disc || titleDiff || artistDiff || genreDiff || videoLabel)

    return {
        provider,
        disc,
        label,
        details,
        hasMetadata,
    }
}

/**
 * Formats a human-readable single-line label for a specific backing-track version.
 */
export function formatVersionLabel(song, index = 0, allVersions = []) {
    const { label, disc, details } = describeVersion(song, index, allVersions)
    const parts = [label]
    if (disc && disc !== label) parts.push(disc)
    for (const detail of details) {
        parts.push(detail)
    }
    return parts.join(' • ')
}
