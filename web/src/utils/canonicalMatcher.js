import {
    removeDiacritics,
    uninvertArtist,
    uninvertTitle,
    stripNoiseTags,
    normalizeArtist,
    normalizeTitle,
    candidateCompositeKeys,
} from './songNormalize.js'
import {
    indexSecondaryMaps,
    recordTrackTokens,
    registerArtistTrack,
    buildPostingsBuffer,
    buildArtistDeleteIndex,
    countQueryTokens,
    collectSeedTokens,
    collectCandidateTids,
    pickBestRawCandidate,
    collectArtistCandidates,
    findBestArtistTypoMatch,
    findBestTitleTypoMatch,
} from './canonicalMatcherHelpers.js'

const STOP_WORDS = new Set(['the', 'a', 'an', 'and', 'ft', 'feat', 'featuring', 'with', 'vs'])
const GENERIC_DISC_PREFIX_RE = /^(?:MP3|MP4|M4A|CD\d+|DISC\d+|DISK\d+|TRACK\d+|TRK\d+|VOL\d+|SIDE\d+|PART\d+|PT\d+|NO\d+|TOP\d+)$/i

export function stripLeadingThe(str) {
    if (!str) return ''
    return str.startsWith('the ') && str.length > 4 ? str.slice(4) : str
}

export function extractPrimaryArtist(artist) {
    const pre = String(artist || '').replace(/\b(ft|feat)\.([a-z0-9])/gi, '$1. $2')
    const norm = normalizeArtist(pre)
    const parts = norm.split(/\s+(?:feat|featuring|ft|with|vs|duet)\s+/)
    return (parts[0] || norm).trim()
}

export function tokenizeRawForMatch(text) {
    const cleaned = removeDiacritics(String(text || ''))
        .replace(/[\(\[]\s*(?:mp3\s*\+\s*cdg|cdg\s*\+\s*mp3|mp3\s*\+\s*g|karaoke\s*cdg|radio\s+(?:version|edit)|clean\s+version|single\s+version|explicit(?:\s+lyrics)?|remastered(?:\s+\d{4})?|original\s+mix|album\s+version|instrumental(?:\s+version)?|backing\s+track|with(?:out)?\s+backing\s+vocals|no\s+backing\s+vocals|w\/?vocal)(?:\s*[-–—]\s*karaoke)?\s*[\)\]]/gi, ' ')
        .replace(/\b(ft|feat)\.([a-z0-9])/gi, '$1 $2')
        .replace(/\bp!nk\b/gi, 'pink')
        .replace(/\$/g, 's')
        .replace(/&/g, ' and ')
        .replace(/\+/g, ' and ')
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, ' ')
        .trim()
    if (!cleaned) return []
    return cleaned.split(' ').filter(Boolean)
}

function coreTokensFromNormalized(normStr) {
    const raw = (normStr || '').split(' ').filter(Boolean)
    if (raw.length === 0) return []
    return raw.filter(t => !STOP_WORDS.has(t))
}

function uniqueTokens(tokens) {
    if (tokens.length <= 1) return tokens
    const seen = new Set()
    const out = []
    for (let i = 0; i < tokens.length; i++) {
        const t = tokens[i]
        if (!seen.has(t)) {
            seen.add(t)
            out.push(t)
        }
    }
    return out
}

export function extractLeadingResidualDisc(rawText, matchedSet) {
    const s = String(rawText || '').trim()
    const m = s.match(/^([A-Za-z]{1,6}\d{2,6}[A-Za-z]?(?:[-_]\d{1,3}){0,2})(?:\s*[-–—_]+\s*(\d{1,2}))?(?=\s*[-–—_\s]+)/)
    if (!m) return ''
    const head = m[1].toUpperCase()
    if (GENERIC_DISC_PREFIX_RE.test(head) || GENERIC_DISC_PREFIX_RE.test(head.split(/[-_]/)[0])) return ''
    const headTok = head.toLowerCase().replace(/[^a-z0-9]/g, '')
    if (matchedSet && matchedSet.has(headTok)) return ''
    if (m[2] && !head.includes('-') && !head.includes('_')) {
        return `${head}-${m[2].padStart(2, '0')}`
    }
    return head.replace(/_/g, '-')
}

/**
 * CanonicalIndex builds a compact 3-layer index over the singular canonical song list:
 *  - Layer 1: O(1) secondary exact maps (expanded key, leading-"The"-stripped, feat/collab primary artist)
 *  - Layer 2: Flat CSR Uint32Array inverted token index for delimiter/prefix-agnostic raw matching
 *  - Layer 3: 1-delete artist SymSpell index for artist typos with exact normalized titles
 */
export class CanonicalIndex {
    constructor(tracksObj) {
        this.rawTracks = null
        this.tracks = []
        this.expandedMap = new Map()
        this.noTheMap = new Map()
        this.primaryArtistMap = new Map()

        this.tokenOffsets = new Map()
        this.tokenLengths = new Map()
        this.postingsBuf = new Uint32Array(0)
        this.artistCoreTokens = []
        this.titleCoreTokens = []
        this.artistCoreLen = new Uint8Array(0)
        this.titleCoreLen = new Uint8Array(0)

        this.stamp = new Uint32Array(0)
        this.touched = new Uint32Array(0)
        this.querySeq = 0

        this.artistNames = []
        this.artistTrackIds = []
        this.artistToId = new Map()
        this.artistDeleteIndex = new Map() // deleteKey -> aid (number) | aid[] (array on collision)

        if (tracksObj) {
            this._build(tracksObj)
        }
    }

    _build(tracksObj) {
        const keys = Object.keys(tracksObj)
        const n = keys.length
        this.rawTracks = tracksObj
        this.tracks = new Array(n)
        this.artistCoreTokens = new Array(n)
        this.titleCoreTokens = new Array(n)
        this.artistCoreLen = new Uint8Array(n)
        this.titleCoreLen = new Uint8Array(n)

        const tokenCounts = new Map()
        this.artistToId = new Map()
        const mapDeps = { stripLeadingThe, extractPrimaryArtist }

        for (let tid = 0; tid < n; tid++) {
            const origKey = keys[tid]
            const t = tracksObj[origKey]
            this.tracks[tid] = t

            const normA = normalizeArtist(t.artist)
            const normT = normalizeTitle(t.title)
            indexSecondaryMaps(this, origKey, t, normA, normT, mapDeps)

            const aTokens = uniqueTokens(coreTokensFromNormalized(normA))
            const tTokens = uniqueTokens(coreTokensFromNormalized(normT))
            this.artistCoreTokens[tid] = aTokens
            this.titleCoreTokens[tid] = tTokens
            this.artistCoreLen[tid] = Math.min(255, aTokens.length)
            this.titleCoreLen[tid] = Math.min(255, tTokens.length)

            recordTrackTokens(tokenCounts, aTokens, tTokens)
            registerArtistTrack(this, normA, tid, stripLeadingThe)
        }

        buildPostingsBuffer(this, tokenCounts, n)
        this.stamp = new Uint32Array(n)
        this.touched = new Uint32Array(n)
        buildArtistDeleteIndex(this.artistNames, this.artistDeleteIndex)
    }

    hasNormArtist(normArtist) {
        return Boolean(normArtist && this.artistToId.has(stripLeadingThe(normArtist)))
    }

    lookupBaseExact(artist, title) {
        if (!this.rawTracks || !artist || !title) return null
        for (const key of candidateCompositeKeys(artist, title)) {
            if (Object.hasOwn(this.rawTracks, key)) return this.rawTracks[key]
        }
        return null
    }

    _lookupExact(key) {
        return this.expandedMap.get(key) || (this.rawTracks && Object.hasOwn(this.rawTracks, key) ? this.rawTracks[key] : null)
    }

    lookupSecondaryExact(artist, title) {
        const cleanA = uninvertArtist(artist)
        const cleanT = stripNoiseTags(uninvertTitle(title))
        const normA = normalizeArtist(cleanA)
        const normT = normalizeTitle(cleanT)
        if (!normA || !normT) return null

        const expKey = `${normA}:::${normT}`
        const expMatch = this._lookupExact(expKey)
        if (expMatch) {
            return { track: expMatch, confidence: 0.99, reason: 'Matched canonical song list' }
        }

        const noTheA = stripLeadingThe(normA)
        const noTheT = stripLeadingThe(normT)
        const noTheKey = `${noTheA}:::${noTheT}`
        const noTheMatch = this.noTheMap.get(noTheKey) || (noTheKey !== expKey ? this._lookupExact(noTheKey) : null)
        if (noTheMatch) {
            return { track: noTheMatch, confidence: 0.98, reason: 'Matched canonical song (article normalized)' }
        }

        const primA = stripLeadingThe(extractPrimaryArtist(cleanA))
        if (primA && primA !== noTheA) {
            const primMatch = this._lookupExact(`${primA}:::${normT}`)
                || this._lookupExact(`${primA}:::${noTheT}`)
                || this.noTheMap.get(`${primA}:::${noTheT}`)
                || this.primaryArtistMap.get(`${primA}:::${normT}`)
            if (primMatch) {
                return { track: primMatch, confidence: 0.93, reason: 'Matched canonical song (collaboration normalized)' }
            }
        }

        const soloToCollab = this.primaryArtistMap.get(`${noTheA}:::${normT}`)
        if (soloToCollab) {
            return { track: soloToCollab, confidence: 0.92, reason: 'Matched canonical song (collaboration)' }
        }

        return null
    }

    matchRawTokens(rawText) {
        if (this.tracks.length === 0 || !rawText) return null
        const qTokens = tokenizeRawForMatch(rawText)
        if (qTokens.length < 2) return null

        const qUnique = uniqueTokens(qTokens)
        const seedable = collectSeedTokens(qUnique, this.tokenLengths)
        if (seedable.length === 0) return null

        const touchedCount = collectCandidateTids(this, seedable)
        if (touchedCount === 0) return null

        const best = pickBestRawCandidate(this, touchedCount, {
            qTokens,
            qUnique,
            qSet: new Set(qUnique),
            qTokenCounts: countQueryTokens(qTokens),
            totalDocs: this.tracks.length,
        })
        if (!best) return null

        const confidence = (best.rExtra === 0 && best.contiguous && best.totalCore >= 3) ? 0.96 : 0.88
        const rescuedDisc = extractLeadingResidualDisc(rawText, best.matchedSet)
        return {
            track: best.track,
            confidence,
            rescuedDisc,
            matchedSet: best.matchedSet,
            reason: rescuedDisc
                ? `Matched canonical song & extracted disc (${rescuedDisc})`
                : 'Matched canonical song from raw text',
        }
    }

    matchArtistTypo(artist, title) {
        const cleanA = uninvertArtist(artist)
        const normA = stripLeadingThe(normalizeArtist(cleanA))
        const normT = normalizeTitle(title)
        if (!normA || !normT || normA.length < 4) return null

        const compact = normA.replace(/\s+/g, '')
        if (compact.length < 4 || compact.length > 32) return null

        const candAids = collectArtistCandidates(this.artistDeleteIndex, compact)
        if (candAids.size === 0) return null

        const match = findBestArtistTypoMatch(this, candAids, compact, normT)
        if (!match) return null

        return {
            track: match.bestTrack,
            confidence: match.bestDist === 1 ? 0.90 : 0.85,
            reason: `Corrected artist spelling to "${match.bestTrack.artist}"`,
        }
    }

    matchTitleTypo(artist, title) {
        const cleanA = uninvertArtist(artist)
        const cleanT = stripNoiseTags(uninvertTitle(title))
        const normA = stripLeadingThe(normalizeArtist(cleanA))
        const normT = normalizeTitle(cleanT)
        if (!normA || !normT) return null

        const aid = this.artistToId.get(normA)
        if (aid === undefined) return null

        const match = findBestTitleTypoMatch(this.tracks, this.artistTrackIds[aid], normT)
        if (!match) return null

        return {
            track: match.bestTrack,
            confidence: Math.max(0.85, 1.0 - (match.bestDist * 0.05)),
            reason: `Fuzzy title match (${match.bestTrack.title})`,
        }
    }
}
