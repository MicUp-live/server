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

function getDistinctFieldValue(song, allVersions, field) {
    if (!Array.isArray(allVersions) || allVersions.length <= 1) return ''
    const value = song[field]?.trim()
    if (!value) return ''
    const distinct = new Set(allVersions.map(v => normalizeGroupPart(v?.[field])).filter(Boolean))
    return distinct.size > 1 ? value : ''
}

function resolveVersionLabel(candidates, fallbackGenre, index) {
    const primary = candidates.find(Boolean)
    if (primary) return primary
    if (fallbackGenre) return `${fallbackGenre} • Version ${index + 1}`
    return `Version ${index + 1}`
}

function collectVersionDetails(entries, label) {
    const details = []
    for (const { value, allowSameAsLabel } of entries) {
        if (!value) continue
        if (!allowSameAsLabel && value === label) continue
        details.push(value)
    }
    return details
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
    const artistDiff = getDistinctFieldValue(song, allVersions, 'artist')
    const genreDiff = getDistinctFieldValue(song, allVersions, 'genre')

    const fallbackGenre = (song.genre || '').trim()
    const label = resolveVersionLabel(
        [provider, titleDiff, disc, videoLabel, genreDiff],
        fallbackGenre,
        index,
    )
    const details = collectVersionDetails([
        { value: titleDiff, allowSameAsLabel: false },
        { value: artistDiff, allowSameAsLabel: true },
        { value: genreDiff, allowSameAsLabel: false },
        { value: videoLabel, allowSameAsLabel: false },
    ], label)

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
