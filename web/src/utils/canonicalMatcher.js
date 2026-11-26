import {
    removeDiacritics,
    uninvertArtist,
    uninvertTitle,
    stripNoiseTags,
    normalizeArtist,
    normalizeTitle,
    candidateCompositeKeys,
    boundedLevenshtein,
} from './songNormalize.js'

const STOP_WORDS = new Set(['the', 'a', 'an', 'and', 'ft', 'feat', 'featuring', 'with', 'vs'])
const NOISE_WORDS = new Set([
    'the', 'a', 'an', 'and', 'ft', 'feat', 'featuring', 'with', 'vs',
    'karaoke', 'cdg', 'mp3', 'zip', 'mp4', 'wav', 'm4a', 'kfn', 'wvocal',
])
const VARIANT_WORDS = new Set([
    'live', 'acoustic', 'remix', 'medley', 'duet', 'duo', 'multiplex',
    'spanish', 'french', 'german', 'italian', 'portuguese', 'reprise',
    'unplugged', 'parody', 'cover', 'tribute', 'remake', 'extended',
    'acapella', 'club', 'mashup', 'megamix', 'part', 'pt',
])
const GENERIC_DISC_PREFIX_RE = /^(?:MP3|MP4|M4A|CD\d+|DISC\d+|DISK\d+|TRACK\d+|TRK\d+|VOL\d+|SIDE\d+|PART\d+|PT\d+|NO\d+|TOP\d+)$/i
const MAX_SEED_DF = 1500
const MAX_SEED_TOKENS = 8

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

function isDiscLikeToken(tok) {
    return /^[a-z]{1,6}\d{2,6}[a-z0-9]*$/i.test(tok) && !GENERIC_DISC_PREFIX_RE.test(tok)
}

function hasContiguousSpan(qTokens, targetTokens) {
    if (targetTokens.length === 0) return false
    if (targetTokens.length === 1) return qTokens.includes(targetTokens[0])
    const first = targetTokens[0]
    for (let i = 0; i < qTokens.length; i++) {
        if (qTokens[i] !== first) continue
        let qi = i + 1
        let ti = 1
        let skipped = 0
        while (ti < targetTokens.length && qi < qTokens.length && skipped <= 2) {
            if (qTokens[qi] === targetTokens[ti]) ti++
            else skipped++
            qi++
        }
        if (ti === targetTokens.length) return true
    }
    return false
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

function trackRichness(t) {
    return (t.genre ? 2 : 0) + (t.year ? 1 : 0) + (/^the\s+/i.test(t.artist || '') ? 0.5 : 0)
}

function coreSignature(aTokens, tTokens) {
    return `${aTokens.join(' ')}:::${tTokens.join(' ')}`
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
        const artistToId = this.artistToId

        for (let tid = 0; tid < n; tid++) {
            const origKey = keys[tid]
            const t = tracksObj[origKey]
            this.tracks[tid] = t

            const normA = normalizeArtist(t.artist)
            const normT = normalizeTitle(t.title)
            const expKey = `${normA}:::${normT}`
            if (expKey !== origKey) {
                const existingExp = this.expandedMap.get(expKey)
                if (!existingExp || trackRichness(t) > trackRichness(existingExp)) {
                    this.expandedMap.set(expKey, t)
                }
            }

            const noTheKey = `${stripLeadingThe(normA)}:::${stripLeadingThe(normT)}`
            if (noTheKey !== expKey) {
                const existingNoThe = this.noTheMap.get(noTheKey)
                if (!existingNoThe || trackRichness(t) > trackRichness(existingNoThe)) {
                    this.noTheMap.set(noTheKey, t)
                }
            }

            const primA = stripLeadingThe(extractPrimaryArtist(t.artist))
            if (primA && primA !== stripLeadingThe(normA) && normT) {
                const primKey = `${primA}:::${normT}`
                const existingPrim = this.primaryArtistMap.get(primKey)
                if (!existingPrim || trackRichness(t) > trackRichness(existingPrim)) {
                    this.primaryArtistMap.set(primKey, t)
                }
            }

            const aTokens = uniqueTokens(coreTokensFromNormalized(normA))
            const tTokens = uniqueTokens(coreTokensFromNormalized(normT))
            this.artistCoreTokens[tid] = aTokens
            this.titleCoreTokens[tid] = tTokens
            this.artistCoreLen[tid] = Math.min(255, aTokens.length)
            this.titleCoreLen[tid] = Math.min(255, tTokens.length)

            if (aTokens.length > 0 && tTokens.length > 0) {
                const seenInTrack = new Set()
                for (let i = 0; i < aTokens.length; i++) seenInTrack.add(aTokens[i])
                for (let i = 0; i < tTokens.length; i++) seenInTrack.add(tTokens[i])
                for (const tok of seenInTrack) {
                    tokenCounts.set(tok, (tokenCounts.get(tok) || 0) + 1)
                }
            }

            const artistKey = stripLeadingThe(normA)
            if (artistKey) {
                let aid = artistToId.get(artistKey)
                if (aid === undefined) {
                    aid = this.artistNames.length
                    artistToId.set(artistKey, aid)
                    this.artistNames.push(artistKey)
                    this.artistTrackIds.push([])
                }
                this.artistTrackIds[aid].push(tid)
            }
        }

        let totalPostings = 0
        for (const [tok, count] of tokenCounts.entries()) {
            this.tokenOffsets.set(tok, totalPostings)
            this.tokenLengths.set(tok, count)
            totalPostings += count
        }
        this.postingsBuf = new Uint32Array(totalPostings)
        const writeCursor = new Map(this.tokenOffsets)

        for (let tid = 0; tid < n; tid++) {
            const aTokens = this.artistCoreTokens[tid]
            const tTokens = this.titleCoreTokens[tid]
            if (aTokens.length === 0 || tTokens.length === 0) continue
            const seenInTrack = new Set()
            for (let i = 0; i < aTokens.length; i++) seenInTrack.add(aTokens[i])
            for (let i = 0; i < tTokens.length; i++) seenInTrack.add(tTokens[i])
            for (const tok of seenInTrack) {
                const pos = writeCursor.get(tok)
                this.postingsBuf[pos] = tid
                writeCursor.set(tok, pos + 1)
            }
        }

        this.stamp = new Uint32Array(n)
        this.touched = new Uint32Array(n)

        const addDel = (key, aid) => {
            const cur = this.artistDeleteIndex.get(key)
            if (cur === undefined) {
                this.artistDeleteIndex.set(key, aid)
            } else if (typeof cur === 'number') {
                if (cur !== aid) this.artistDeleteIndex.set(key, [cur, aid])
            } else if (cur.length < 24 && !cur.includes(aid)) {
                cur.push(aid)
            }
        }
        for (let aid = 0; aid < this.artistNames.length; aid++) {
            const compact = this.artistNames[aid].replace(/\s+/g, '')
            if (compact.length < 4 || compact.length > 32) continue
            addDel(compact, aid)
            for (let i = 0; i < compact.length; i++) {
                addDel(compact.slice(0, i) + compact.slice(i + 1), aid)
            }
        }
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
        const qSet = new Set(qUnique)
        const qTokenCounts = new Map()
        for (let i = 0; i < qTokens.length; i++) {
            qTokenCounts.set(qTokens[i], (qTokenCounts.get(qTokens[i]) || 0) + 1)
        }

        const seedable = []
        for (let i = 0; i < qUnique.length; i++) {
            const tok = qUnique[i]
            const df = this.tokenLengths.get(tok)
            if (df !== undefined && df > 0 && df <= MAX_SEED_DF) {
                seedable.push({ tok, df })
            }
        }
        if (seedable.length === 0) return null
        seedable.sort((a, b) => a.df - b.df)

        this.querySeq++
        if (this.querySeq === 0xffffffff) {
            this.stamp.fill(0)
            this.querySeq = 1
        }
        const seq = this.querySeq
        let touchedCount = 0

        const seedLimit = Math.min(seedable.length, MAX_SEED_TOKENS)
        for (let s = 0; s < seedLimit; s++) {
            const tok = seedable[s].tok
            const offset = this.tokenOffsets.get(tok)
            const end = offset + this.tokenLengths.get(tok)
            for (let p = offset; p < end; p++) {
                const tid = this.postingsBuf[p]
                if (this.stamp[tid] !== seq) {
                    this.stamp[tid] = seq
                    this.touched[touchedCount++] = tid
                }
            }
        }
        if (touchedCount === 0) return null

        const totalDocs = this.tracks.length
        let best = null
        let bestScore = -Infinity
        let secondScore = -Infinity
        let secondSig = ''

        for (let i = 0; i < touchedCount; i++) {
            const tid = this.touched[i]
            const aLen = this.artistCoreLen[tid]
            const tLen = this.titleCoreLen[tid]
            if (aLen === 0 || tLen === 0 || aLen > qUnique.length || tLen > qUnique.length) continue

            const aTokens = this.artistCoreTokens[tid]
            const tTokens = this.titleCoreTokens[tid]

            let allArtist = true
            for (let j = 0; j < aTokens.length; j++) {
                if (!qSet.has(aTokens[j])) { allArtist = false; break }
            }
            if (!allArtist) continue

            let allTitle = true
            for (let j = 0; j < tTokens.length; j++) {
                if (!qSet.has(tTokens[j])) { allTitle = false; break }
            }
            if (!allTitle) continue

            const matchedSet = new Set()
            for (let j = 0; j < aTokens.length; j++) matchedSet.add(aTokens[j])
            for (let j = 0; j < tTokens.length; j++) matchedSet.add(tTokens[j])

            // Self-titled 1-word guard: if artist and title share a single token (e.g. "Hair - Hair"),
            // require that token to appear at least twice in qTokens.
            if (matchedSet.size < 2) {
                const onlyTok = aTokens[0]
                if ((qTokenCounts.get(onlyTok) || 0) < 2) continue
            }

            let rVar = 0
            let rExtra = 0
            for (let j = 0; j < qUnique.length; j++) {
                const tok = qUnique[j]
                if (matchedSet.has(tok)) continue
                if (NOISE_WORDS.has(tok) || isDiscLikeToken(tok) || /^\d{1,2}$/.test(tok) || /^\d{5,8}$/.test(tok)) {
                    continue
                }
                if (VARIANT_WORDS.has(tok)) rVar++
                else rExtra++
            }

            const totalCore = aLen + tLen
            if (rVar > 0) continue
            if (totalCore <= 3 && rExtra > 0) continue
            if (rExtra > 1) continue

            let idfSum = 0
            for (const tok of matchedSet) {
                const df = this.tokenLengths.get(tok) || 1
                idfSum += Math.log(1 + totalDocs / df)
            }

            const artistContig = hasContiguousSpan(qTokens, aTokens)
            const titleContig = hasContiguousSpan(qTokens, tTokens)
            const spanBonus = (artistContig ? 1.0 : 0) + (titleContig ? 1.0 : 0)
            const score = idfSum + (matchedSet.size * 0.6) + spanBonus - (rExtra * 2.2) + (trackRichness(this.tracks[tid]) * 0.05)

            const sig = coreSignature(aTokens, tTokens)
            const track = this.tracks[tid]
            if (score > bestScore) {
                if (best && best.sig !== sig) {
                    secondScore = bestScore
                    secondSig = best.sig
                }
                bestScore = score
                best = {
                    track,
                    sig,
                    rExtra,
                    totalCore,
                    contiguous: artistContig && titleContig,
                    matchedSet,
                }
            } else if (best && sig !== best.sig && score > secondScore) {
                secondScore = score
                secondSig = sig
            }
        }

        if (!best) return null
        if (secondSig && (bestScore - secondScore) < 0.25) return null

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

        const candAids = new Set()
        const collect = (key) => {
            const val = this.artistDeleteIndex.get(key)
            if (val === undefined) return
            if (typeof val === 'number') candAids.add(val)
            else for (let i = 0; i < val.length; i++) candAids.add(val[i])
        }
        collect(compact)
        for (let i = 0; i < compact.length; i++) {
            collect(compact.slice(0, i) + compact.slice(i + 1))
        }
        if (candAids.size === 0) return null

        const maxArtistDist = compact.length <= 6 ? 1 : 2
        let bestTrack = null
        let bestAid = -1
        let bestDist = maxArtistDist + 1
        let tied = false

        for (const aid of candAids) {
            const candCompact = this.artistNames[aid].replace(/\s+/g, '')
            const dist = boundedLevenshtein(compact, candCompact, maxArtistDist)
            if (dist === 0 || dist > maxArtistDist) continue

            const tids = this.artistTrackIds[aid]
            let richestForAid = null
            for (let j = 0; j < tids.length; j++) {
                const t = this.tracks[tids[j]]
                if (normalizeTitle(t.title) === normT) {
                    if (!richestForAid || trackRichness(t) > trackRichness(richestForAid)) {
                        richestForAid = t
                    }
                }
            }
            if (!richestForAid) continue

            if (dist < bestDist) {
                bestDist = dist
                bestTrack = richestForAid
                bestAid = aid
                tied = false
            } else if (dist === bestDist && bestAid !== aid) {
                tied = true
            }
        }

        if (!bestTrack || tied) return null
        return {
            track: bestTrack,
            confidence: bestDist === 1 ? 0.90 : 0.85,
            reason: `Corrected artist spelling to "${bestTrack.artist}"`,
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

        const tids = this.artistTrackIds[aid]
        let bestDist = 3
        let bestTrack = null
        let bestNormTitle = ''
        let tied = false

        for (let i = 0; i < tids.length; i++) {
            const t = this.tracks[tids[i]]
            const candNormTitle = normalizeTitle(t.title)
            const dist = boundedLevenshtein(normT, candNormTitle, 2)
            if (dist > 0 && dist < bestDist) {
                bestDist = dist
                bestTrack = t
                bestNormTitle = candNormTitle
                tied = false
            } else if (dist === bestDist && bestTrack) {
                if (candNormTitle === bestNormTitle) {
                    if (trackRichness(t) > trackRichness(bestTrack)) bestTrack = t
                } else {
                    tied = true
                }
            }
        }

        if (tied || bestDist > 2 || !bestTrack) return null
        return {
            track: bestTrack,
            confidence: Math.max(0.85, 1.0 - (bestDist * 0.05)),
            reason: `Fuzzy title match (${bestTrack.title})`,
        }
    }
}
